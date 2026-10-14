package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type BackupInfo struct {
	ID         int64  `json:"id"`
	Filename   string `json:"filename"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Integrity  string `json:"integrity"`
	Replicas   []M    `json:"replicas"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	CreatedAt  string `json:"created_at"`
	VerifiedAt string `json:"verified_at"`
}

func readonlySQLite(path string) (*sql.DB, error) {
	absPath, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	u := url.URL{Scheme: "file", Path: sqliteURIPath(absPath)}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Set("immutable", "1")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
func FileHash(path string) (string, int64, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func VerifyBackup(path string) (string, int64, error) {
	if wal, e := os.Stat(path + "-wal"); e == nil && wal.Size() > 0 {
		return "", 0, fmt.Errorf("该文件仍有 WAL 数据，请使用软件生成的一致性备份，不能直接恢复运行中的数据库")
	}
	info, e := os.Stat(path)
	if e != nil {
		return "", 0, e
	}
	if info.IsDir() {
		return "", 0, fmt.Errorf("备份路径不是文件")
	}
	db, e := readonlySQLite(path)
	if e != nil {
		return "", 0, e
	}
	defer db.Close()
	var integrity string
	if e = db.QueryRow("PRAGMA integrity_check").Scan(&integrity); e != nil {
		return "", 0, e
	}
	if integrity != "ok" {
		return "", 0, fmt.Errorf("备份完整性检查失败：%s", integrity)
	}
	var version int
	if e = db.QueryRow("PRAGMA user_version").Scan(&version); e != nil {
		return "", 0, e
	}
	if version != 1 {
		return "", 0, fmt.Errorf("备份数据库版本不兼容")
	}
	for _, table := range []string{"settings", "users", "roles", "base_records", "documents", "document_items", "inventory", "inventory_transactions", "inventory_serials", "stock_reservations", "bills", "settlements", "writeoffs", "bill_offsets", "settlement_offsets", "price_rules", "system_installation", "license_local", "operation_log", "backup_records", "background_jobs"} {
		var n int
		if e = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); e != nil {
			return "", 0, fmt.Errorf("备份缺少必要业务数据：%w", e)
		}
	}
	rows, e := db.Query("PRAGMA foreign_key_check")
	if e != nil {
		return "", 0, e
	}
	invalid := rows.Next()
	er := rows.Err()
	rows.Close()
	if er != nil {
		return "", 0, er
	}
	if invalid {
		return "", 0, fmt.Errorf("备份外键一致性检查失败")
	}
	if e = CheckLedger(db); e != nil {
		return "", 0, e
	}
	return FileHash(path)
}
func CheckLedger(q Queryer) error {
	checks := []struct{ name, sql string }{
		{"库存余额与流水", `SELECT COUNT(*) FROM inventory i WHERE i.quantity<>COALESCE((SELECT SUM(delta) FROM inventory_transactions t WHERE t.inventory_id=i.id),0) OR i.value<>COALESCE((SELECT SUM(cost_delta) FROM inventory_transactions t WHERE t.inventory_id=i.id),0)`},
		{"库存流水前后数量", `SELECT COUNT(*) FROM inventory_transactions WHERE before_quantity+delta<>after_quantity OR before_value+cost_delta<>after_value`},
		{"库存占用", `SELECT COUNT(*) FROM inventory i WHERE reserved<0 OR reserved<>COALESCE((SELECT SUM(quantity-consumed) FROM stock_reservations r WHERE r.inventory_id=i.id),0)`},
		{"订单占用数量", `SELECT COUNT(*) FROM stock_reservations WHERE consumed<0 OR quantity<consumed`},
		{"往来账单核销", `SELECT COUNT(*) FROM bills b WHERE b.settled<>COALESCE((SELECT SUM(w.amount) FROM writeoffs w WHERE w.bill_id=b.id AND w.reversed=0),0)+COALESCE((SELECT SUM(o.amount) FROM bill_offsets o WHERE o.positive_bill_id=b.id AND o.reversed=0),0)-COALESCE((SELECT SUM(o.amount) FROM bill_offsets o WHERE o.negative_bill_id=b.id AND o.reversed=0),0) OR abs(b.settled)>abs(b.amount)`},
		{"收付款分配", `SELECT COUNT(*) FROM settlements s WHERE s.allocated<>COALESCE((SELECT SUM(w.amount) FROM writeoffs w WHERE w.settlement_id=s.id AND w.reversed=0),0)+COALESCE((SELECT SUM(o.amount) FROM settlement_offsets o WHERE o.positive_settlement_id=s.id AND o.reversed=0),0)-COALESCE((SELECT SUM(o.amount) FROM settlement_offsets o WHERE o.negative_settlement_id=s.id AND o.reversed=0),0) OR abs(s.allocated)>abs(s.amount)`},
		{"序列号库存", `SELECT COUNT(*) FROM inventory i JOIN base_records p ON p.id=i.product_id WHERE json_extract(p.data,'$.serial_tracking')=1 AND i.quantity<>(SELECT COUNT(*)*10000 FROM inventory_serials s WHERE s.inventory_id=i.id AND s.state='in')`},
	}
	for _, check := range checks {
		var n int64
		if e := q.QueryRow(check.sql).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return fmt.Errorf("业务一致性检查失败：%s（%d 条异常）", check.name, n)
		}
	}
	return nil
}
func copyFile(source, destination string) error {
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	if e == nil {
		e = out.Sync()
	}
	ce := out.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		_ = os.Remove(destination)
	}
	return e
}

func (a *App) MakeBackup(u User, ip, kind string) (BackupInfo, error) {
	a.backupMu.Lock()
	defer a.backupMu.Unlock()
	b := BackupInfo{Kind: kind, Status: "ok", Integrity: "ok", Replicas: []M{}, CreatedAt: stamp(), VerifiedAt: stamp()}
	if kind != "manual" && kind != "snapshot" && kind != "daily" && kind != "weekly" && kind != "monthly" {
		return b, fail("备份类型无效")
	}
	dir := filepath.Join(a.DataDir, "backups")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return b, e
	}
	b.Filename = "ruilian-" + kind + "-" + time.Now().Format("20060102-150405") + "-" + uid()[:6] + ".sqlite"
	path := filepath.Join(dir, b.Filename)
	a.writeMu.Lock()
	_, e := a.DB.Exec("VACUUM main INTO ?", path)
	a.writeMu.Unlock()
	if e != nil {
		return b, e
	}
	f, e := os.OpenFile(path, os.O_RDWR, 0600)
	if e != nil {
		return b, e
	}
	e = f.Sync()
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return b, e
	}
	b.SHA256, b.Size, e = VerifyBackup(path)
	if e != nil {
		b.Status = "failed"
		b.Integrity = "failed"
		b.Message = e.Error()
	}
	var destinations []string
	_ = json.Unmarshal([]byte(setting(a.DB, "backup_destinations")), &destinations)
	if e == nil {
		for _, destination := range destinations {
			result := M{"path": destination, "status": "failed", "attempts": 0}
			target := filepath.Join(destination, b.Filename)
			var copyErr error
			for attempt := 1; attempt <= 3; attempt++ {
				result["attempts"] = attempt
				copyErr = os.MkdirAll(destination, 0700)
				if copyErr == nil {
					if _, statErr := os.Stat(target); os.IsNotExist(statErr) {
						copyErr = copyFile(path, target)
					}
				}
				if copyErr == nil {
					digest, _, er := FileHash(target)
					copyErr = er
					if copyErr == nil && digest != b.SHA256 {
						copyErr = fmt.Errorf("异地副本 SHA-256 不一致")
					}
				}
				if copyErr == nil {
					break
				}
				time.Sleep(time.Duration(attempt) * 150 * time.Millisecond)
			}
			if copyErr != nil {
				result["message"] = copyErr.Error()
				b.Status = "partial"
				b.Message = "本地备份成功；部分异盘 / 异地副本失败，详见副本状态"
			} else {
				result["status"] = "ok"
				result["sha256"] = b.SHA256
			}
			b.Replicas = append(b.Replicas, result)
		}
	}
	if b.Message == "" {
		b.Message = "完整性、外键一致性与 SHA-256 校验通过"
	}
	recordErr := a.write(func(tx *sql.Tx) error {
		var er error
		b.ID, er = idOf(tx.Exec("INSERT INTO backup_records(filename,sha256,size,integrity,replicas,kind,status,message,created_at,verified_at) VALUES(?,?,?,?,?,?,?,?,?,?)", b.Filename, b.SHA256, b.Size, b.Integrity, js(b.Replicas), b.Kind, b.Status, b.Message, b.CreatedAt, b.VerifiedAt))
		if er != nil {
			return er
		}
		return audit(tx, u, ip, "数据库备份", "backup", b.ID, nil, b)
	})
	if recordErr != nil {
		return b, recordErr
	}
	if er := atomicFile(path+".json", []byte(js(b)), 0600); er != nil {
		return b, er
	}
	if e != nil {
		return b, e
	}
	return b, nil
}
func (a *App) Backups() ([]BackupInfo, error) {
	rows, e := a.DB.Query("SELECT id,filename,sha256,size,integrity,replicas,kind,status,message,created_at,verified_at FROM backup_records ORDER BY id DESC LIMIT 500")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []BackupInfo{}
	for rows.Next() {
		var b BackupInfo
		var replicas string
		if e = rows.Scan(&b.ID, &b.Filename, &b.SHA256, &b.Size, &b.Integrity, &replicas, &b.Kind, &b.Status, &b.Message, &b.CreatedAt, &b.VerifiedAt); e != nil {
			return nil, e
		}
		_ = json.Unmarshal([]byte(replicas), &b.Replicas)
		out = append(out, b)
	}
	return out, rows.Err()
}
func (a *App) BackupPath(id int64) (string, string, error) {
	var filename, hash string
	if e := a.DB.QueryRow("SELECT filename,sha256 FROM backup_records WHERE id=?", id).Scan(&filename, &hash); e != nil {
		return "", "", notFound(e)
	}
	if filepath.Base(filename) != filename {
		return "", "", fail("备份路径无效")
	}
	return filepath.Join(a.DataDir, "backups", filename), hash, nil
}
func (a *App) VerifyRecord(u User, ip string, id int64) error {
	path, expected, e := a.BackupPath(id)
	if e != nil {
		return e
	}
	digest, _, e := VerifyBackup(path)
	if e == nil && digest != expected {
		e = fmt.Errorf("SHA-256 与备份记录不一致")
	}
	status := "ok"
	message := "恢复验证通过：可打开备份、读取核心表并通过完整性检查"
	if e != nil {
		status = "failed"
		message = e.Error()
	}
	er := a.write(func(tx *sql.Tx) error {
		if _, er := tx.Exec("UPDATE backup_records SET integrity=?,verified_at=?,message=? WHERE id=?", status, stamp(), message, id); er != nil {
			return er
		}
		return audit(tx, u, ip, "备份恢复验证", "backup", id, nil, M{"status": status, "message": message})
	})
	if er != nil {
		return er
	}
	return e
}

// RestoreDatabase must be called while holding the process lock and before Open.
// Existing main, WAL and SHM files are copied before any replacement takes place.
func RestoreDatabase(dataDir, source string) (string, error) {
	dir, e := filepath.Abs(dataDir)
	if e != nil {
		return "", e
	}
	src, e := filepath.Abs(source)
	if e != nil {
		return "", e
	}
	target := filepath.Join(dir, "ruilian.db")
	if strings.EqualFold(src, target) {
		return "", fmt.Errorf("不能将当前数据库作为恢复源")
	}
	digest, _, e := VerifyBackup(src)
	if e != nil {
		return "", e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	oldDir := filepath.Join(dir, "pre-restore-"+time.Now().Format("20060102-150405")+"-"+uid()[:6])
	if e = os.MkdirAll(oldDir, 0700); e != nil {
		return "", e
	}
	for _, name := range []string{"ruilian.db", "ruilian.db-wal", "ruilian.db-shm", ".installation.json", ".installation.key"} {
		path := filepath.Join(dir, name)
		if _, er := os.Stat(path); er == nil {
			if er = copyFile(path, filepath.Join(oldDir, name)); er != nil {
				return oldDir, er
			}
		} else if !os.IsNotExist(er) {
			return oldDir, er
		}
	}
	tmp := filepath.Join(dir, "restore-"+uid()+".sqlite")
	if e = copyFile(src, tmp); e != nil {
		return oldDir, e
	}
	defer os.Remove(tmp)
	hash, _, e := VerifyBackup(tmp)
	if e != nil {
		return oldDir, e
	}
	if hash != digest {
		return oldDir, fmt.Errorf("恢复文件复制校验失败")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		path := target + suffix
		if _, er := os.Stat(path); er == nil {
			if er = os.Remove(path); er != nil {
				return oldDir, er
			}
		}
	}
	if e = os.Rename(tmp, target); e != nil {
		return oldDir, e
	}
	db, e := sql.Open("sqlite", target)
	if e != nil {
		return oldDir, fmt.Errorf("数据已恢复，但无法写入恢复审计：%w", e)
	}
	defer db.Close()
	if e = audit(db, User{Username: "恢复工具"}, "local", "数据库恢复", "system", 0, M{"preserved_at": oldDir}, M{"source": src, "sha256": digest}); e != nil {
		return oldDir, fmt.Errorf("数据已恢复，但恢复审计写入失败：%w", e)
	}
	_, e = db.Exec("INSERT INTO license_event_log(event,message,created_at) VALUES('database_restored',?,?)", "数据库恢复完成，下次启动重新检查安装标识与授权", stamp())
	return oldDir, e
}

func (a *App) Background(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.License.Status(true)
			a.cleanSessions()
			if a.NeedsSetup() {
				continue
			}
			kind := a.dueBackup()
			if kind == "" {
				continue
			}
			var jobID int64
			_ = a.write(func(tx *sql.Tx) error {
				var e error
				jobID, e = idOf(tx.Exec("INSERT INTO background_jobs(kind,status,created_at,updated_at) VALUES(?,'running',?,?)", kind, stamp(), stamp()))
				return e
			})
			var err error
			attempt := 0
			for attempt = 1; attempt <= 3; attempt++ {
				if ctx.Err() != nil {
					return
				}
				_, err = a.MakeBackup(User{Username: "自动备份"}, "local", kind)
				if err == nil {
					break
				}
				a.recordError("automatic backup", err)
			}
			status, message := "completed", "备份完成"
			if err != nil {
				status = "dead_letter"
				message = err.Error()
				attempt = 3
			}
			_ = a.write(func(tx *sql.Tx) error {
				_, e := tx.Exec("UPDATE background_jobs SET status=?,attempts=?,message=?,updated_at=? WHERE id=?", status, attempt, message, stamp(), jobID)
				return e
			})
			if err == nil {
				a.pruneBackups()
			}
		}
	}
}
func (a *App) dueBackup() string {
	now := time.Now()
	for _, kind := range []string{"monthly", "weekly", "daily"} {
		if kind == "monthly" && now.Day() != 1 {
			continue
		}
		if kind == "weekly" && now.Weekday() != time.Sunday {
			continue
		}
		var last string
		_ = a.DB.QueryRow("SELECT COALESCE(MAX(created_at),'') FROM backup_records WHERE kind=? AND status IN ('ok','partial')", kind).Scan(&last)
		t, _ := time.Parse(time.RFC3339, last)
		if t.In(time.Local).Format("2006-01-02") != today() {
			return kind
		}
	}
	minutes, _ := strconv.Atoi(settingString(a.DB, "backup_interval_minutes"))
	if minutes < 1 {
		minutes = 15
	}
	var last string
	_ = a.DB.QueryRow("SELECT COALESCE(MAX(created_at),'') FROM backup_records WHERE status IN ('ok','partial')").Scan(&last)
	t, _ := time.Parse(time.RFC3339, last)
	if time.Since(t) >= time.Duration(minutes)*time.Minute {
		return "snapshot"
	}
	return ""
}
func (a *App) pruneBackups() {
	// Only files recorded by this application are subject to the retention policy.
	rows, e := a.DB.Query("SELECT id,filename,kind,replicas FROM backup_records WHERE status IN ('ok','partial') ORDER BY id DESC")
	if e != nil {
		return
	}
	type entry struct {
		id                   int64
		name, kind, replicas string
	}
	all := []entry{}
	for rows.Next() {
		var v entry
		if rows.Scan(&v.id, &v.name, &v.kind, &v.replicas) == nil {
			all = append(all, v)
		}
	}
	rows.Close()
	limits := map[string]int{"snapshot": 192, "manual": 0}
	for _, k := range []string{"daily", "weekly", "monthly"} {
		v, _ := strconv.Atoi(settingString(a.DB, "backup_"+k+"_keep"))
		if v < 1 {
			v = 14
		}
		limits[k] = v
	}
	seen := map[string]int{}
	for _, v := range all {
		seen[v.kind]++
		if limits[v.kind] == 0 || seen[v.kind] <= limits[v.kind] || filepath.Base(v.name) != v.name {
			continue
		}
		path := filepath.Join(a.DataDir, "backups", v.name)
		if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
			a.recordError("backup retention", e)
			continue
		}
		_ = os.Remove(path + ".json")
		_ = a.write(func(tx *sql.Tx) error {
			_, e := tx.Exec("UPDATE backup_records SET status='expired',message='本地副本按保留策略清理；异盘副本由介质管理策略保留' WHERE id=?", v.id)
			return e
		})
	}
}

func (a *App) Settings() M {
	rows, e := a.DB.Query("SELECT key,value FROM settings ORDER BY key")
	if e != nil {
		return M{}
	}
	defer rows.Close()
	out := M{}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			var decoded any
			if json.Unmarshal([]byte(v), &decoded) == nil {
				out[k] = decoded
			}
		}
	}
	return out
}
func (a *App) SaveSettings(u User, ip string, input M) error {
	allowed := map[string]bool{"app_name": true, "brand_short": true, "brand_logo": true, "company_name": true, "allow_negative_stock": true, "credit_policy": true, "overdue_policy": true, "low_price_policy": true, "stale_days": true, "backup_interval_minutes": true, "backup_daily_keep": true, "backup_weekly_keep": true, "backup_monthly_keep": true, "backup_destinations": true, "document_prefixes": true}
	for k, v := range input {
		if !allowed[k] {
			return fail("不支持的设置项：" + k)
		}
		switch k {
		case "app_name", "brand_short":
			maximum := 64
			if k == "brand_short" {
				maximum = 12
			}
			text, e := validateBrandText(v, maximum)
			if e != nil {
				return e
			}
			input[k] = text
		case "brand_logo":
			if e := validateBrandLogo(v); e != nil {
				return e
			}
		case "company_name":
			if str(input, k) == "" || len(str(input, k)) > 200 {
				return fail("企业名称无效")
			}
		case "allow_negative_stock":
			if _, ok := v.(bool); !ok {
				return fail("负库存开关须为布尔值")
			}
		case "credit_policy", "overdue_policy", "low_price_policy":
			if v != "block" && v != "warn" {
				return fail("控制策略只能为 block 或 warn")
			}
		case "stale_days", "backup_interval_minutes", "backup_daily_keep", "backup_weekly_keep", "backup_monthly_keep":
			n := number(input, k)
			if n < 1 || n > 3650 {
				return fail("周期与保留数量须为 1～3650 的整数")
			}
			if k == "backup_interval_minutes" && n > 1440 {
				return fail("备份间隔不能超过 1440 分钟")
			}
		case "backup_destinations":
			var ds []string
			if json.Unmarshal([]byte(js(v)), &ds) != nil || len(ds) > 3 {
				return fail("最多配置三个异盘 / 异地备份目录")
			}
			seen := map[string]bool{}
			for _, d := range ds {
				clean := filepath.Clean(strings.TrimSpace(d))
				if !filepath.IsAbs(clean) || strings.EqualFold(clean, filepath.Join(a.DataDir, "backups")) || seen[strings.ToLower(clean)] {
					return fail("备份副本须使用不同的绝对目录，不能与本地备份目录相同")
				}
				seen[strings.ToLower(clean)] = true
			}
			input[k] = ds
		case "document_prefixes":
			var prefixes map[string]string
			if json.Unmarshal([]byte(js(v)), &prefixes) != nil {
				return fail("单据前缀格式无效")
			}
			ps := []string{}
			for kind, p := range prefixes {
				if _, e := kindOf(kind); e != nil {
					return e
				}
				if len(p) < 1 || len(p) > 16 || strings.ContainsAny(p, " /\\\r\n\t") {
					return fail("单据前缀须为 1～16 字节且不含空格与斜杠")
				}
				ps = append(ps, p)
			}
			sort.Strings(ps)
			for i := 1; i < len(ps); i++ {
				if ps[i] == ps[i-1] {
					return fail("不同单据类型建议使用不同编号前缀，当前存在重复")
				}
			}
		}
	}
	return a.write(func(tx *sql.Tx) error {
		before := M{}
		for k, v := range input {
			before[k] = setting(tx, k)
			if _, e := tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, js(v)); e != nil {
				return e
			}
		}
		return audit(tx, u, ip, "修改系统参数", "settings", 0, before, input)
	})
}
