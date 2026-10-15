package app

import (
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var validTenantCode = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

// Tenant 租户（组控空间）数据模型
type Tenant struct {
	ID             int64   `json:"id"`
	Code           string  `json:"code"`
	Name           string  `json:"name"`
	MaxStorageMB   int64   `json:"max_storage_mb"` // 空间配额 (MB)，0 为不限制
	UsedStorage    int64   `json:"used_storage"`   // 实际占用字节数
	UsedStorageMB  float64 `json:"used_storage_mb"` // 实际占用 MB
	StoragePercent float64 `json:"storage_percent"` // 占用百分比 0~100
	Status         string  `json:"status"`         // active, disabled
	AdminUser      string  `json:"admin_user"`     // 租户管理员用户名
	Note           string  `json:"note"`           // 备注
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

// TenantManager 租户多空间管理器
type TenantManager struct {
	mainApp   *App
	dataDir   string
	publicKey []byte
	logger    *log.Logger
	assets    fs.FS
	mu        sync.RWMutex
	apps      map[string]*App
	handlers  map[string]http.Handler
}

// NewTenantManager 创建并初始化租户管理器
func NewTenantManager(mainApp *App, dataDir string, publicKey []byte, logger *log.Logger, assets fs.FS) (*TenantManager, error) {
	absDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	tm := &TenantManager{
		mainApp:   mainApp,
		dataDir:   absDir,
		publicKey: publicKey,
		logger:    logger,
		assets:    assets,
		apps:      make(map[string]*App),
		handlers:  make(map[string]http.Handler),
	}
	if tm.logger == nil {
		tm.logger = log.Default()
	}

	// 缓存主 App (default 租户)
	tm.apps["default"] = mainApp
	mainApp.TenantCode = "default"
	mainApp.TenantManager = tm

	if err := tm.ensureTenantsTable(); err != nil {
		return nil, fmt.Errorf("初始化租户数据表失败: %w", err)
	}
	return tm, nil
}

// ensureTenantsTable 在主库中确保 tenants 表存在并初始化默认租户
func (tm *TenantManager) ensureTenantsTable() error {
	schema := `
	CREATE TABLE IF NOT EXISTS tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		code TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		max_storage_mb INTEGER NOT NULL DEFAULT 500,
		status TEXT NOT NULL DEFAULT 'active',
		admin_user TEXT NOT NULL DEFAULT 'huxinkui',
		note TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	`
	if _, err := tm.mainApp.DB.Exec(schema); err != nil {
		return err
	}

	// 确保 default 默认租户存在
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := tm.mainApp.DB.Exec(`
		INSERT OR IGNORE INTO tenants(code, name, max_storage_mb, status, admin_user, note, created_at, updated_at)
		VALUES('default', '平台总控管理中心', 500, 'active', 'huxinkui', '系统主管理空间', ?, ?)
	`, now, now)
	if err != nil {
		return err
	}

	// 升级默认租户的 admin_user 为 huxinkui
	_, _ = tm.mainApp.DB.Exec(`
		UPDATE tenants SET admin_user='huxinkui', name='平台总控管理中心' WHERE code='default' AND (admin_user='admin' OR admin_user='' OR name='默认租户空间')
	`)

	// 自动同步现有子租户的 admin_user 为其租户代码 (如 xuyonghong)
	_, _ = tm.mainApp.DB.Exec(`
		UPDATE tenants SET admin_user=code WHERE code!='default' AND (admin_user='admin' OR admin_user='' OR admin_user='huxinkui')
	`)

	// 自动将主数据库中的 admin 用户迁移为 huxinkui
	var countHxk int
	_ = tm.mainApp.DB.QueryRow("SELECT COUNT(*) FROM users WHERE username='huxinkui'").Scan(&countHxk)
	if countHxk == 0 {
		_, _ = tm.mainApp.DB.Exec("UPDATE users SET username='huxinkui', name='超级管理员', updated_at=? WHERE username='admin'", now)
	} else {
		_, _ = tm.mainApp.DB.Exec("DELETE FROM users WHERE username='admin'")
	}
	return nil
}

// GetTenantStorageUsage 计算租户占用的实际物理存储空间（字节）
func (tm *TenantManager) GetTenantStorageUsage(code string) (int64, error) {
	if code == "default" || code == "" {
		// 默认租户统计主数据目录中的主库及其 WAL 等文件
		var total int64
		for _, name := range []string{"ruilian.db", "ruilian.db-wal", "ruilian.db-shm"} {
			if fi, err := os.Stat(filepath.Join(tm.dataDir, name)); err == nil {
				total += fi.Size()
			}
		}
		// 加上主数据目录下 logs 的体积
		logsDir := filepath.Join(tm.dataDir, "logs")
		_ = filepath.Walk(logsDir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				total += fi.Size()
			}
			return nil
		})
		return total, nil
	}

	// 子租户统计 tenants/<code} 目录内所有文件的大小总和
	tenantDir := filepath.Join(tm.dataDir, "tenants", code)
	if _, err := os.Stat(tenantDir); os.IsNotExist(err) {
		return 0, nil
	}
	var total int64
	err := filepath.Walk(tenantDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// ListTenants 获取所有租户列表并计算实时空间使用情况
func (tm *TenantManager) ListTenants() ([]Tenant, error) {
	rows, err := tm.mainApp.DB.Query(`
		SELECT id, code, name, max_storage_mb, status, admin_user, note, created_at, updated_at
		FROM tenants ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Code, &t.Name, &t.MaxStorageMB, &t.Status, &t.AdminUser, &t.Note, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		if t.Code == "default" && (t.AdminUser == "" || t.AdminUser == "admin") {
			t.AdminUser = "huxinkui"
		} else if t.Code != "default" && (t.AdminUser == "" || t.AdminUser == "admin" || t.AdminUser == "huxinkui") {
			t.AdminUser = t.Code
		}
		used, _ := tm.GetTenantStorageUsage(t.Code)
		t.UsedStorage = used
		t.UsedStorageMB = math.Round(float64(used)/(1024*1024)*100) / 100
		if t.MaxStorageMB > 0 {
			percent := (float64(used) / float64(t.MaxStorageMB*1024*1024)) * 100
			t.StoragePercent = math.Round(percent*10) / 10
			if t.StoragePercent > 100 {
				t.StoragePercent = 100
			}
		} else {
			t.StoragePercent = 0
		}
		list = append(list, t)
	}
	return list, nil
}

// GetTenant 获取指定租户及空间信息
func (tm *TenantManager) GetTenant(code string) (Tenant, error) {
	if code == "" {
		code = "default"
	}
	var t Tenant
	err := tm.mainApp.DB.QueryRow(`
		SELECT id, code, name, max_storage_mb, status, admin_user, note, created_at, updated_at
		FROM tenants WHERE code=?
	`, code).Scan(&t.ID, &t.Code, &t.Name, &t.MaxStorageMB, &t.Status, &t.AdminUser, &t.Note, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return t, err
	}
	if t.Code == "default" && (t.AdminUser == "" || t.AdminUser == "admin") {
		t.AdminUser = "huxinkui"
	} else if t.Code != "default" && (t.AdminUser == "" || t.AdminUser == "admin" || t.AdminUser == "huxinkui") {
		t.AdminUser = t.Code
	}
	used, _ := tm.GetTenantStorageUsage(t.Code)
	t.UsedStorage = used
	t.UsedStorageMB = math.Round(float64(used)/(1024*1024)*100) / 100
	if t.MaxStorageMB > 0 {
		percent := (float64(used) / float64(t.MaxStorageMB*1024*1024)) * 100
		t.StoragePercent = math.Round(percent*10) / 10
		if t.StoragePercent > 100 {
			t.StoragePercent = 100
		}
	}
	return t, nil
}

// CreateTenant 创建新租户，设置空间配额并初始化独立数据库
func (tm *TenantManager) CreateTenant(code, name string, maxStorageMB int64, adminPassword, note string) (*Tenant, error) {
	code = strings.TrimSpace(code)
	name = strings.TrimSpace(name)
	if !validTenantCode.MatchString(code) {
		return nil, fmt.Errorf("租户编码必须为1-32位字母、数字、下划线或连字符")
	}
	if name == "" {
		return nil, fmt.Errorf("租户名称不能为空")
	}
	if maxStorageMB < 0 {
		return nil, fmt.Errorf("空间配额不能为负数")
	}
	if maxStorageMB == 0 {
		maxStorageMB = 500 // 默认 500MB
	}
	if adminPassword == "" {
		adminPassword = DefaultAdminPassword
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	adminUser := code
	res, err := tm.mainApp.DB.Exec(`
		INSERT INTO tenants(code, name, max_storage_mb, status, admin_user, note, created_at, updated_at)
		VALUES(?, ?, ?, 'active', ?, ?, ?, ?)
	`, code, name, maxStorageMB, adminUser, note, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("租户编码 %s 已存在", code)
		}
		return nil, err
	}
	tenantID, _ := res.LastInsertId()

	// 初始化租户目录与独立数据库
	tenantDir := filepath.Join(tm.dataDir, "tenants", code)
	if err := os.MkdirAll(tenantDir, 0700); err != nil {
		_, _ = tm.mainApp.DB.Exec("DELETE FROM tenants WHERE id=?", tenantID)
		return nil, fmt.Errorf("创建租户目录失败: %w", err)
	}

	tenantApp, err := Open(tenantDir, tm.publicKey, tm.logger)
	if err != nil {
		_, _ = tm.mainApp.DB.Exec("DELETE FROM tenants WHERE id=?", tenantID)
		_ = os.RemoveAll(tenantDir)
		return nil, fmt.Errorf("初始化租户数据库失败: %w", err)
	}
	tenantApp.TenantCode = code
	tenantApp.TenantManager = tm
	if tm.mainApp != nil {
		tenantApp.IPLimiter = tm.mainApp.IPLimiter
		tenantApp.CaptchaStore = tm.mainApp.CaptchaStore
	}

	_ = tenantApp.EnsureTenantUser(code, name, adminPassword)

	// 设置该租户的企业名称
	_ = tenantApp.write(func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE settings SET value=? WHERE key='company_name'", js(name))
		return e
	})

	tm.apps[code] = tenantApp
	if tm.assets != nil {
		tm.handlers[code] = tenantApp.Router(tm.assets)
	}

	used, _ := tm.GetTenantStorageUsage(code)
	return &Tenant{
		ID:             tenantID,
		Code:           code,
		Name:           name,
		MaxStorageMB:   maxStorageMB,
		UsedStorage:    used,
		UsedStorageMB:  math.Round(float64(used)/(1024*1024)*100) / 100,
		StoragePercent: 0.1,
		Status:         "active",
		AdminUser:      adminUser,
		Note:           note,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// UpdateTenantStorage 管理员调整租户使用空间配额
func (tm *TenantManager) UpdateTenantStorage(code string, maxStorageMB int64) error {
	if maxStorageMB < 0 {
		return fmt.Errorf("空间配额不能为负数")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tm.mainApp.DB.Exec(`
		UPDATE tenants SET max_storage_mb=?, updated_at=? WHERE code=?
	`, maxStorageMB, now, code)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("租户不存在: %s", code)
	}
	return nil
}

// UpdateTenant 管理员修改租户信息（名称、备注、空间配额）
func (tm *TenantManager) UpdateTenant(code, name, note string, maxStorageMB int64) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("租户名称不能为空")
	}
	if maxStorageMB < 0 {
		return fmt.Errorf("空间配额不能为负数")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tm.mainApp.DB.Exec(`
		UPDATE tenants SET name=?, note=?, max_storage_mb=?, updated_at=? WHERE code=?
	`, name, note, maxStorageMB, now, code)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("租户不存在: %s", code)
	}

	// 同步更新租户数据库中的企业名称和专属用户显示名称
	if app, _ := tm.GetTenantApp(code); app != nil {
		_ = app.write(func(tx *sql.Tx) error {
			if _, e := tx.Exec("UPDATE settings SET value=? WHERE key='company_name'", js(name)); e != nil {
				return e
			}
			_, e := tx.Exec("UPDATE users SET name=?, updated_at=? WHERE username=?", name, stamp(), code)
			return e
		})
	}
	return nil
}

// SetTenantStatus 启用或停用租户
func (tm *TenantManager) SetTenantStatus(code, status string) error {
	if code == "default" && status == "disabled" {
		return fmt.Errorf("默认系统租户不能停用")
	}
	if status != "active" && status != "disabled" {
		return fmt.Errorf("无效的状态值")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tm.mainApp.DB.Exec(`
		UPDATE tenants SET status=?, updated_at=? WHERE code=?
	`, status, now, code)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("租户不存在: %s", code)
	}
	return nil
}

// ResetTenantPassword 重置租户管理员密码
func (tm *TenantManager) ResetTenantPassword(code, newPassword string) error {
	if len(newPassword) < 8 {
		return fmt.Errorf("新密码长度至少需要8位")
	}
	app, err := tm.GetTenantApp(code)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		return err
	}
	return app.write(func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE users SET password_hash=?, updated_at=? WHERE username=?", string(hash), stamp(), code)
		return e
	})
}

// GetTenantApp 获得/懒加载租户对应的 App 实例
func (tm *TenantManager) GetTenantApp(code string) (*App, error) {
	if code == "" || code == "default" {
		return tm.mainApp, nil
	}

	tm.mu.RLock()
	app, exists := tm.apps[code]
	tm.mu.RUnlock()
	if exists {
		return app, nil
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	if app, exists := tm.apps[code]; exists {
		return app, nil
	}

	var status, name string
	err := tm.mainApp.DB.QueryRow("SELECT status, name FROM tenants WHERE code=?", code).Scan(&status, &name)
	if err != nil {
		return nil, fmt.Errorf("租户不存在: %s", code)
	}
	if status == "disabled" {
		return nil, fmt.Errorf("租户 [%s] 已被系统管理员停用", code)
	}

	tenantDir := filepath.Join(tm.dataDir, "tenants", code)
	if err := os.MkdirAll(tenantDir, 0700); err != nil {
		return nil, err
	}
	tenantApp, err := Open(tenantDir, tm.publicKey, tm.logger)
	if err != nil {
		return nil, fmt.Errorf("打开租户数据库失败: %w", err)
	}
	tenantApp.TenantCode = code
	tenantApp.TenantManager = tm
	if tm.mainApp != nil {
		tenantApp.IPLimiter = tm.mainApp.IPLimiter
		tenantApp.CaptchaStore = tm.mainApp.CaptchaStore
	}

	_ = tenantApp.EnsureTenantUser(code, name, "")
	tm.apps[code] = tenantApp
	return tenantApp, nil
}

// GetTenantHandler 获得/懒加载租户对应的 HTTP Handler 引擎
func (tm *TenantManager) GetTenantHandler(code string) (http.Handler, error) {
	if code == "" || code == "default" {
		if tm.handlers["default"] == nil && tm.assets != nil {
			tm.handlers["default"] = tm.mainApp.Router(tm.assets)
		}
		return tm.handlers["default"], nil
	}

	tm.mu.RLock()
	h, exists := tm.handlers[code]
	tm.mu.RUnlock()
	if exists {
		return h, nil
	}

	app, err := tm.GetTenantApp(code)
	if err != nil {
		return nil, err
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	if h, exists := tm.handlers[code]; exists {
		return h, nil
	}

	engine := app.Router(tm.assets)
	tm.handlers[code] = engine
	return engine, nil
}

// CheckStorageQuota 检查租户当前空间是否超限（用于写操作前拦截）
func (tm *TenantManager) CheckStorageQuota(code string) error {
	tenant, err := tm.GetTenant(code)
	if err != nil {
		return nil
	}
	if tenant.MaxStorageMB <= 0 {
		return nil // 不限容量
	}
	usedBytes, err := tm.GetTenantStorageUsage(code)
	if err != nil {
		return nil
	}
	maxBytes := tenant.MaxStorageMB * 1024 * 1024
	if usedBytes >= maxBytes {
		usedMB := float64(usedBytes) / (1024 * 1024)
		return fmt.Errorf("租户使用空间已达上限 (已用 %.2f MB / 配额 %d MB)，已被限制写入。请联系系统管理员调整空间配额", usedMB, tenant.MaxStorageMB)
	}
	return nil
}
