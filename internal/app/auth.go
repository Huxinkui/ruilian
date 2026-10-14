package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type loginAttempts struct {
	Count int
	Until time.Time
}

var PermissionGroups = map[string][]string{
	"基础资料": {"base.view", "base.edit"}, "采购": {"purchase.view", "purchase.edit", "purchase.approve"},
	"销售": {"sales.view", "sales.edit", "sales.approve"}, "库存": {"inventory.view", "inventory.edit", "inventory.approve"},
	"往来账务": {"finance.view", "finance.edit", "finance.approve"}, "价格": {"price.view", "price.edit", "price.override"},
	"报表与预警": {"reports.view", "warnings.view", "warnings.ack", "amount.view", "cost.view"},
	"系统管理":  {"system.settings", "system.users", "system.audit", "system.backup", "system.license"},
}

func (a *App) NeedsSetup() bool {
	var n int
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n == 0
}
func validPassword(p string) error {
	if len(p) < 8 || len(p) > 72 {
		return fail("密码长度须为 8～72 字节")
	}
	var alpha, digit bool
	for _, c := range p {
		if c >= '0' && c <= '9' {
			digit = true
		} else {
			alpha = true
		}
	}
	if !alpha || !digit {
		return fail("密码须同时包含数字和非数字字符")
	}
	return nil
}
func validUsername(s string) bool {
	return len(s) >= 3 && len(s) <= 64 && !strings.ContainsAny(s, " \t\r\n/\\")
}

func (a *App) Setup(input M, ip string) error {
	return a.setupAccount(input, ip, false)
}

// Only a new installation receives the requested default account. Existing
// accounts, passwords and business data are never changed by startup.
func (a *App) EnsureDefaultAccount() error {
	if !a.NeedsSetup() {
		return nil
	}
	return a.setupAccount(M{"username": "admin", "password": "admin", "company_name": "我的企业"}, "local", true)
}

func (a *App) setupAccount(input M, ip string, defaultAccount bool) error {
	name, password, company := str(input, "username"), str(input, "password"), str(input, "company_name")
	if !validUsername(name) {
		return fail("用户名须为 3～64 字节且不能含空格或斜杠")
	}
	if !defaultAccount {
		if e := validPassword(password); e != nil {
			return e
		}
	}
	if company == "" || len(company) > 200 {
		return fail("请填写企业名称（最多 200 字节）")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e != nil {
		return e
	}
	return a.write(func(tx *sql.Tx) error {
		var n int
		if e = tx.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return fail("系统已初始化，请登录")
		}
		roles := []struct {
			Name string
			P    []string
		}{
			{"系统管理员", []string{"*"}},
			{"业务经理", []string{"base.view", "base.edit", "purchase.view", "purchase.edit", "purchase.approve", "sales.view", "sales.edit", "sales.approve", "inventory.view", "finance.view", "price.view", "price.edit", "price.override", "reports.view", "warnings.view", "warnings.ack", "amount.view", "cost.view"}},
			{"仓库管理员", []string{"base.view", "inventory.view", "inventory.edit", "inventory.approve", "warnings.view", "warnings.ack"}},
			{"财务人员", []string{"base.view", "purchase.view", "sales.view", "inventory.view", "finance.view", "finance.edit", "finance.approve", "reports.view", "warnings.view", "warnings.ack", "amount.view", "cost.view", "system.backup"}},
		}
		var adminRole int64
		for i, r := range roles {
			id, er := idOf(tx.Exec("INSERT INTO roles(name,permissions,built_in) VALUES(?,?,1)", r.Name, js(r.P)))
			if er != nil {
				return er
			}
			if i == 0 {
				adminRole = id
			}
		}
		id, e := idOf(tx.Exec("INSERT INTO users(username,name,password_hash,role_id,created_at,updated_at) VALUES(?,?,?,?,?,?)", name, "管理员", string(hash), adminRole, stamp(), stamp()))
		if e != nil {
			return e
		}
		for _, v := range []Master{{Kind: "organization", Code: "ORG001", Name: company, Data: M{}}, {Kind: "unit", Code: "PCS", Name: "件", Data: M{}}, {Kind: "category", Code: "CAT001", Name: "默认分类", Data: M{}}, {Kind: "warehouse", Code: "WH001", Name: "主仓库", Data: M{"consignment": false}}} {
			if _, e = tx.Exec("INSERT INTO base_records(kind,code,name,data,created_at,updated_at) VALUES(?,?,?,?,?,?)", v.Kind, v.Code, v.Name, js(v.Data), stamp(), stamp()); e != nil {
				return e
			}
		}
		if _, e = tx.Exec("UPDATE settings SET value=? WHERE key='company_name'", js(company)); e != nil {
			return e
		}
		return audit(tx, User{ID: id, Username: name}, ip, "初始化", "system", 0, nil, M{"company_name": company})
	})
}

const userSQL = `SELECT u.id,u.username,u.name,u.role_id,r.name,r.permissions,r.warehouse_ids,r.data_scope,u.organization_id FROM users u JOIN roles r ON r.id=u.role_id WHERE u.active=1 AND `

func loadUser(q Queryer, id int64) (User, error) {
	var u User
	var p, w string
	e := q.QueryRow(userSQL+"u.id=?", id).Scan(&u.ID, &u.Username, &u.Name, &u.RoleID, &u.RoleName, &p, &w, &u.DataScope, &u.OrganizationID)
	if e != nil {
		return u, notFound(e)
	}
	_ = json.Unmarshal([]byte(p), &u.Permissions)
	_ = json.Unmarshal([]byte(w), &u.Warehouses)
	return u, nil
}
func tokenHash(t string) string { b := sha256.Sum256([]byte(t)); return hex.EncodeToString(b[:]) }

func (a *App) Login(username, password, ip string) (string, User, error) {
	username = strings.TrimSpace(username)
	key := ip + "|" + strings.ToLower(username)
	a.loginMu.Lock()
	attempt := a.attempts[key]
	if attempt != nil && time.Now().Before(attempt.Until) && attempt.Count >= 8 {
		a.loginMu.Unlock()
		return "", User{}, fail("登录尝试过多，请 15 分钟后重试")
	}
	a.loginMu.Unlock()
	var id int64
	var hash string
	e := a.DB.QueryRow("SELECT id,password_hash FROM users WHERE username=? AND active=1", username).Scan(&id, &hash)
	if e != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		a.loginMu.Lock()
		if attempt == nil || time.Now().After(attempt.Until) {
			attempt = &loginAttempts{Until: time.Now().Add(15 * time.Minute)}
		}
		attempt.Count++
		a.attempts[key] = attempt
		for k, v := range a.attempts {
			if time.Now().After(v.Until) {
				delete(a.attempts, k)
			}
		}
		a.loginMu.Unlock()
		_ = a.write(func(tx *sql.Tx) error {
			return audit(tx, User{Username: username}, ip, "登录失败", "auth", 0, nil, nil)
		})
		return "", User{}, fail("用户名或密码错误，或账号已停用")
	}
	u, e := loadUser(a.DB, id)
	if e != nil {
		return "", u, e
	}
	token := uid() + uid()
	e = a.write(func(tx *sql.Tx) error {
		if _, er := tx.Exec("INSERT INTO sessions(token_hash,user_id,expires_at,created_at) VALUES(?,?,?,?)", tokenHash(token), id, time.Now().UTC().Add(12*time.Hour).Format(time.RFC3339), stamp()); er != nil {
			return er
		}
		return audit(tx, u, ip, "登录", "auth", id, nil, nil)
	})
	if e != nil {
		return "", u, e
	}
	a.loginMu.Lock()
	delete(a.attempts, key)
	a.loginMu.Unlock()
	return token, u, nil
}
func (a *App) Authenticate(token string) (User, error) {
	var id int64
	if len(token) != 64 {
		return User{}, denied("请先登录")
	}
	e := a.DB.QueryRow("SELECT user_id FROM sessions WHERE token_hash=? AND expires_at>?", tokenHash(token), stamp()).Scan(&id)
	if e != nil {
		return User{}, denied("登录已过期，请重新登录")
	}
	return loadUser(a.DB, id)
}

func (a *App) SaveUser(u User, ip string, id int64, input M) error {
	name, username, password := str(input, "name"), str(input, "username"), str(input, "password")
	role := number(input, "role_id")
	if name == "" || len(name) > 100 || !validUsername(username) {
		return fail("请填写有效姓名与用户名")
	}
	var hash []byte
	var e error
	if password != "" {
		if e = validPassword(password); e != nil {
			return e
		}
		hash, e = bcrypt.GenerateFromPassword([]byte(password), 12)
		if e != nil {
			return e
		}
	} else if id == 0 {
		return fail("新增用户需要设置密码")
	}
	return a.write(func(tx *sql.Tx) error {
		var exists int
		if e = tx.QueryRow("SELECT COUNT(*) FROM roles WHERE id=?", role).Scan(&exists); e != nil {
			return e
		}
		if exists == 0 {
			return fail("角色不存在")
		}
		if id > 0 {
			if e = tx.QueryRow("SELECT COUNT(*) FROM users WHERE id=?", id).Scan(&exists); e != nil {
				return e
			}
			if exists == 0 {
				return fail("用户不存在")
			}
		}
		if oid := number(input, "organization_id"); oid > 0 {
			if _, e = master(tx, oid, "organization", true); e != nil {
				return e
			}
		}
		active := flag(input, "active")
		if id == u.ID && !active {
			return fail("不能停用当前登录账号")
		}
		if id > 0 {
			if _, e = tx.Exec("UPDATE users SET username=?,name=?,role_id=?,organization_id=?,active=?,updated_at=? WHERE id=?", username, name, role, number(input, "organization_id"), active, stamp(), id); e != nil {
				return fail("用户名已存在或数据无效")
			}
			if password != "" {
				if _, e = tx.Exec("UPDATE users SET password_hash=? WHERE id=?", string(hash), id); e != nil {
					return e
				}
			}
		} else {
			id, e = idOf(tx.Exec("INSERT INTO users(username,name,password_hash,role_id,organization_id,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", username, name, string(hash), role, number(input, "organization_id"), active, stamp(), stamp()))
			if e != nil {
				return fail("用户名已存在或数据无效")
			}
		}
		if _, e = tx.Exec("DELETE FROM sessions WHERE user_id=?", id); e != nil {
			return e
		}
		var n int
		e = tx.QueryRow("SELECT COUNT(*) FROM users u JOIN roles r ON r.id=u.role_id WHERE u.active=1 AND EXISTS(SELECT 1 FROM json_each(r.permissions) WHERE value='*')").Scan(&n)
		if e != nil {
			return e
		}
		if n == 0 {
			return fail("至少保留一个启用的系统管理员")
		}
		return audit(tx, u, ip, "保存用户", "user", id, nil, M{"username": username, "name": name, "role_id": role, "active": active, "password_changed": password != ""})
	})
}
func (a *App) ChangePassword(u User, ip, old, newPassword string) error {
	if e := validPassword(newPassword); e != nil {
		return e
	}
	var hash string
	if e := a.DB.QueryRow("SELECT password_hash FROM users WHERE id=?", u.ID).Scan(&hash); e != nil {
		return e
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(old)) != nil {
		return fail("原密码不正确")
	}
	b, e := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if e != nil {
		return e
	}
	return a.write(func(tx *sql.Tx) error {
		if _, e = tx.Exec("UPDATE users SET password_hash=?,updated_at=? WHERE id=?", string(b), stamp(), u.ID); e != nil {
			return e
		}
		if _, e = tx.Exec("DELETE FROM sessions WHERE user_id=?", u.ID); e != nil {
			return e
		}
		return audit(tx, u, ip, "修改密码", "user", u.ID, nil, nil)
	})
}
func (a *App) SaveRole(u User, ip string, id int64, input M) error {
	name := str(input, "name")
	if name == "" || len(name) > 100 {
		return fail("请填写角色名称")
	}
	var permissions []string
	_ = json.Unmarshal([]byte(js(input["permissions"])), &permissions)
	allowed := map[string]bool{"*": true}
	for _, ps := range PermissionGroups {
		for _, p := range ps {
			allowed[p] = true
		}
	}
	for _, p := range permissions {
		if !allowed[p] {
			return fail("无效的权限项目")
		}
	}
	var warehouses []int64
	_ = json.Unmarshal([]byte(js(input["warehouse_ids"])), &warehouses)
	scope := str(input, "data_scope")
	if scope != "own" {
		scope = "all"
	}
	return a.write(func(tx *sql.Tx) error {
		if id > 0 {
			var old string
			var built int
			if e := tx.QueryRow("SELECT permissions,built_in FROM roles WHERE id=?", id).Scan(&old, &built); e != nil {
				return notFound(e)
			}
			if built == 1 && old == `["*"]` {
				return fail("内置系统管理员角色不能修改")
			}
		}
		for _, wid := range warehouses {
			if _, e := master(tx, wid, "warehouse", true); e != nil {
				return e
			}
		}
		var e error
		if id == 0 {
			id, e = idOf(tx.Exec("INSERT INTO roles(name,permissions,warehouse_ids,data_scope) VALUES(?,?,?,?)", name, js(permissions), js(warehouses), scope))
		} else {
			_, e = tx.Exec("UPDATE roles SET name=?,permissions=?,warehouse_ids=?,data_scope=? WHERE id=?", name, js(permissions), js(warehouses), scope, id)
		}
		if e != nil {
			return fail("角色名称重复或数据无效")
		}
		if _, e = tx.Exec("DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE role_id=?)", id); e != nil {
			return e
		}
		return audit(tx, u, ip, "保存角色", "role", id, nil, M{"name": name, "permissions": permissions, "warehouse_ids": warehouses, "data_scope": scope})
	})
}

func (a *App) docAccess(u User, d Doc, action string) error {
	kind := d.Kind
	if kind == "reversal" {
		kind = str(d.Data, "original_kind")
	}
	k, e := kindOf(kind)
	if e != nil {
		return e
	}
	if !u.Can(k.Module + "." + action) {
		return denied("没有该业务的操作权限")
	}
	if !u.Warehouse(d.WarehouseID) || !u.Warehouse(d.TargetWarehouseID) {
		return denied("没有该单据涉及仓库的权限")
	}
	if u.DataScope == "own" && d.CreatedBy != u.ID {
		return denied("只能访问本人创建的单据")
	}
	return nil
}
func scopeSQL(u User, alias string) (string, []any) {
	cond := ""
	args := []any{}
	if u.DataScope == "own" {
		cond += " AND " + alias + ".created_by=?"
		args = append(args, u.ID)
	}
	if len(u.Warehouses) > 0 {
		marks := strings.TrimRight(strings.Repeat("?,", len(u.Warehouses)), ",")
		for _, col := range []string{"warehouse_id", "target_warehouse_id"} {
			cond += fmt.Sprintf(" AND (%s.%s IS NULL OR %s.%s IN (%s))", alias, col, alias, col, marks)
			for _, v := range u.Warehouses {
				args = append(args, v)
			}
		}
	}
	return cond, args
}
