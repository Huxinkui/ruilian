package app

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// MasterAdminUsername 平台唯一专属超级管理员账号
const MasterAdminUsername = "huxinkui"

// TargetSchemaVersion 当前租户目标 Schema 版本
const TargetSchemaVersion = 2

// 校验租户代码正则规范（3-32位小写字母、数字、下划线）
var validTenantCode = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

// Tenant 租户（组控空间）数据模型
type Tenant struct {
	ID                  int64   `json:"id"`
	Code                string  `json:"code"`
	TenantCode          string  `json:"tenant_code"`
	Name                string  `json:"name"`
	CompanyName         string  `json:"company_name"`
	MaxStorageMB        int64   `json:"max_storage_mb"`
	CurrentStorageBytes int64   `json:"current_storage_bytes"`
	UsedStorage         int64   `json:"used_storage"`
	UsedStorageMB       float64 `json:"used_storage_mb"`
	StoragePercent      float64 `json:"storage_percent"`
	Status              string  `json:"status"` // active, readonly_quota, suspended, archived
	MaxUsers            int     `json:"max_users"`
	DBVersion           int     `json:"db_version"`
	ExpireAt            string  `json:"expire_at"`
	CreatedBy           string  `json:"created_by"`
	AdminUser           string  `json:"admin_user"`
	Note                string  `json:"note"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`

	// 指标大盘快照数据（供管理员大盘直接查阅，无需扫描子库）
	DocCount          int64  `json:"doc_count"`
	ProductCount      int64  `json:"product_count"`
	CustomerCount     int64  `json:"customer_count"`
	NetSales30d       int64  `json:"net_sales_30d"`
	TotalARAmount     int64  `json:"total_ar_amount"`
	TotalInventoryVal int64  `json:"total_inventory_val"`
	LastActiveAt      string `json:"last_active_at"`
	LastBackupAt      string `json:"last_backup_at"`
	WALSizeBytes      int64  `json:"wal_size_bytes"`
}

// TenantInstance 租户物理沙箱实例
type TenantInstance struct {
	TenantCode    string
	App           *App
	UsedBytes     atomic.Int64
	MaxQuotaBytes atomic.Int64 // 配额上限字节数 (0 表示不限)
	WriteCounter  atomic.Int64 // 累计写操作计数 (用于触发异步磁盘校准)
	LastAccessed  atomic.Int64 // 最后访问 UTC 时间戳 (用于 LRU 空闲连接回收)
	LastModified  atomic.Int64 // 最后一次写事务时间戳 (用于静默租户跳过冗余备份)
	LastBackup    atomic.Int64 // 最后一次成功备份时间戳
	Status        string       // active, readonly_quota, suspended, archived
}

// ImpersonationSession 超级管理员跨空间穿透临时凭证
type ImpersonationSession struct {
	Token         string    `json:"token"`
	AdminUsername string    `json:"admin_username"`
	TargetTenant  string    `json:"target_tenant"`
	AccessMode    string    `json:"access_mode"` // "readonly" 或 "readwrite"
	ClientIP      string    `json:"client_ip"`
	StartedAt     time.Time `json:"started_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	ActionsCount  int64     `json:"actions_count"`
}

// TenantManager 租户多空间管理器 (LRU连接池 + 滞回防抖配额引擎 + 跨空间穿透守卫)
type TenantManager struct {
	mainApp         *App
	dataDir         string
	publicKey       []byte
	logger          *log.Logger
	assets          fs.FS
	mu              sync.RWMutex
	instances       map[string]*TenantInstance
	handlers        map[string]http.Handler
	maxInstances    int // 最大常驻活跃租户池大小 (默认 128)
	impersonations  map[string]*ImpersonationSession
	impMu           sync.RWMutex
	sessions        sync.Map // tokenHash -> tenantCode
	stopChan        chan struct{}
}

// NewTenantManager 创建并初始化租户管理器
func NewTenantManager(mainApp *App, dataDir string, publicKey []byte, logger *log.Logger, assets fs.FS) (*TenantManager, error) {
	absDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	tm := &TenantManager{
		mainApp:        mainApp,
		dataDir:        absDir,
		publicKey:      publicKey,
		logger:         logger,
		assets:         assets,
		instances:      make(map[string]*TenantInstance),
		handlers:       make(map[string]http.Handler),
		maxInstances:   128,
		impersonations: make(map[string]*ImpersonationSession),
		stopChan:       make(chan struct{}),
	}
	if tm.logger == nil {
		tm.logger = log.Default()
	}

	mainApp.TenantCode = "default"
	mainApp.TenantManager = tm

	// 缓存 default 主空间实例
	now := time.Now().Unix()
	defaultInst := &TenantInstance{
		TenantCode: "default",
		App:        mainApp,
		Status:     "active",
	}
	defaultInst.LastAccessed.Store(now)
	defaultInst.LastModified.Store(now)
	tm.instances["default"] = defaultInst

	if err := tm.ensureTenantsTable(); err != nil {
		return nil, fmt.Errorf("初始化多租户核心数据字典失败: %w", err)
	}

	// 启动后台守护协程：LRU 空闲淘汰、配额滞回防抖与异步落盘、大盘摘要同步
	go tm.startBackgroundDaemon()

	return tm, nil
}

// ensureTenantsTable 初始化并维护主库 tenants 表、摘要缓存表与穿透审计表
func (tm *TenantManager) ensureTenantsTable() error {
	// 1. 创建 tenants 租户注册主表
	schema := `
	CREATE TABLE IF NOT EXISTS tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		code TEXT NOT NULL UNIQUE,
		tenant_code TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		company_name TEXT NOT NULL DEFAULT '',
		max_storage_mb INTEGER NOT NULL DEFAULT 500,
		current_storage_bytes INTEGER NOT NULL DEFAULT 0,
		max_users INTEGER NOT NULL DEFAULT 50,
		db_version INTEGER NOT NULL DEFAULT 1,
		expire_at TEXT NOT NULL DEFAULT '',
		created_by TEXT NOT NULL DEFAULT 'huxinkui',
		status TEXT NOT NULL DEFAULT 'active',
		admin_user TEXT NOT NULL DEFAULT 'huxinkui',
		note TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS tenant_metrics_snapshot (
		tenant_code TEXT PRIMARY KEY,
		doc_count INTEGER DEFAULT 0,
		product_count INTEGER DEFAULT 0,
		customer_count INTEGER DEFAULT 0,
		net_sales_30d INTEGER DEFAULT 0,
		total_ar_amount INTEGER DEFAULT 0,
		total_inventory_val INTEGER DEFAULT 0,
		last_active_at TEXT DEFAULT '',
		synced_at TEXT DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS admin_impersonation_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		admin_username TEXT NOT NULL,
		target_tenant_code TEXT NOT NULL,
		access_mode TEXT NOT NULL,
		client_ip TEXT NOT NULL,
		token_hash TEXT NOT NULL,
		started_at TEXT NOT NULL,
		ended_at TEXT DEFAULT '',
		actions_count INTEGER DEFAULT 0
	);
	`
	if _, err := tm.mainApp.DB.Exec(schema); err != nil {
		return err
	}

	// 兼容迁移：添加可能缺失的字段
	cols := []struct {
		name string
		typ  string
	}{
		{"tenant_code", "TEXT NOT NULL DEFAULT ''"},
		{"company_name", "TEXT NOT NULL DEFAULT ''"},
		{"current_storage_bytes", "INTEGER NOT NULL DEFAULT 0"},
		{"max_users", "INTEGER NOT NULL DEFAULT 50"},
		{"db_version", "INTEGER NOT NULL DEFAULT 1"},
		{"expire_at", "TEXT NOT NULL DEFAULT ''"},
		{"created_by", "TEXT NOT NULL DEFAULT 'huxinkui'"},
	}
	for _, col := range cols {
		_, _ = tm.mainApp.DB.Exec(fmt.Sprintf("ALTER TABLE tenants ADD COLUMN %s %s", col.name, col.typ))
	}

	// 同步字段
	_, _ = tm.mainApp.DB.Exec("UPDATE tenants SET tenant_code=code WHERE tenant_code='' OR tenant_code IS NULL")
	_, _ = tm.mainApp.DB.Exec("UPDATE tenants SET company_name=name WHERE company_name='' OR company_name IS NULL")

	// 确保 default 默认租户存在并锁定专属管理员 huxinkui
	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, _ = tm.mainApp.DB.Exec(`
		INSERT OR IGNORE INTO tenants(code, tenant_code, name, company_name, max_storage_mb, current_storage_bytes, max_users, db_version, status, admin_user, note, created_at, updated_at)
		VALUES('default', 'default', '平台总控管理中心', '平台总控管理中心', 500, 0, 999, 2, 'active', 'huxinkui', '系统主管理空间', ?, ?)
	`, nowStr, nowStr)

	// 锁定默认租户
	_, _ = tm.mainApp.DB.Exec(`
		UPDATE tenants SET admin_user='huxinkui', name='平台总控管理中心', company_name='平台总控管理中心', tenant_code='default', db_version=2
		WHERE code='default'
	`)

	// 自动同步子租户 admin_user 为其租户代码
	_, _ = tm.mainApp.DB.Exec(`
		UPDATE tenants SET admin_user=code, tenant_code=code
		WHERE code!='default' AND (admin_user='admin' OR admin_user='' OR admin_user='huxinkui')
	`)

	// 主库中确保 admin 账号销毁并替换为 huxinkui
	var countHxk int
	_ = tm.mainApp.DB.QueryRow("SELECT COUNT(*) FROM users WHERE username='huxinkui'").Scan(&countHxk)
	if countHxk == 0 {
		_, _ = tm.mainApp.DB.Exec("UPDATE users SET username='huxinkui', name='超级管理员', updated_at=? WHERE username='admin'", nowStr)
	} else {
		_, _ = tm.mainApp.DB.Exec("DELETE FROM users WHERE username='admin'")
	}

	return nil
}

// startBackgroundDaemon 启动后台综合守护任务
func (tm *TenantManager) startBackgroundDaemon() {
	evictTicker := time.NewTicker(5 * time.Minute)
	storageTicker := time.NewTicker(60 * time.Second)
	metricsTicker := time.NewTicker(2 * time.Minute)
	backupTicker := time.NewTicker(15 * time.Minute)
	defer evictTicker.Stop()
	defer storageTicker.Stop()
	defer metricsTicker.Stop()
	defer backupTicker.Stop()

	for {
		select {
		case <-tm.stopChan:
			return
		case <-evictTicker.C:
			tm.evictIdleInstances()
		case <-storageTicker.C:
			tm.syncStorageAndHysteresis()
		case <-metricsTicker.C:
			tm.SyncAllTenantMetrics()
		case <-backupTicker.C:
			tm.runBackupScheduler()
		}
	}
}

// evictIdleInstances 优化意见一：LRU + 15 分钟空闲超时回收连接池与文件句柄
func (tm *TenantManager) evictIdleInstances() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now().Unix()
	idleThreshold := int64(15 * 60) // 15分钟无请求

	for code, inst := range tm.instances {
		if code == "default" {
			continue // default 主空间常驻
		}
		lastAcc := inst.LastAccessed.Load()
		if now-lastAcc > idleThreshold {
			// 先执行 WAL 检查点确保预写日志完全落盘
			if inst.App != nil && inst.App.DB != nil {
				_, _ = inst.App.DB.Exec("PRAGMA wal_checkpoint(PASSIVE)")
				_ = inst.App.Close()
			}
			delete(tm.instances, code)
			delete(tm.handlers, code)
			if tm.logger != nil {
				tm.logger.Printf("[TenantManager LRU] 租户 [%s] 超过15分钟静默无操作，已安全落盘并释放文件句柄与连接池", code)
			}
		}
	}
}

// syncStorageAndHysteresis 优化意见二：内存原子计数 + 滞回防抖双水线配额同步
func (tm *TenantManager) syncStorageAndHysteresis() {
	tm.mu.RLock()
	activeCodes := make([]string, 0, len(tm.instances))
	for code := range tm.instances {
		activeCodes = append(activeCodes, code)
	}
	tm.mu.RUnlock()

	for _, code := range activeCodes {
		used, err := tm.GetTenantStorageUsage(code)
		if err != nil {
			continue
		}

		tm.mu.RLock()
		inst := tm.instances[code]
		tm.mu.RUnlock()

		if inst != nil {
			inst.UsedBytes.Store(used)
			maxBytes := inst.MaxQuotaBytes.Load()

			if maxBytes > 0 {
				// 滞回防抖逻辑：
				// 1. 若当前状态为 readonly_quota，使用量须降至 92% 以下才解除锁定恢复 active
				if inst.Status == "readonly_quota" {
					recoverThreshold := maxBytes * 92 / 100
					if used < recoverThreshold {
						inst.Status = "active"
						_, _ = tm.mainApp.DB.Exec("UPDATE tenants SET status='active', updated_at=? WHERE code=?", stamp(), code)
						if tm.logger != nil {
							tm.logger.Printf("[TenantManager Quota] 租户 [%s] 占用降至 92%% (已用 %.2f MB)，已解除只读锁定恢复读写", code, float64(used)/(1024*1024))
						}
					}
				} else if inst.Status == "active" {
					// 2. 达到 100% 硬水线，触发只读锁定
					if used >= maxBytes {
						inst.Status = "readonly_quota"
						_, _ = tm.mainApp.DB.Exec("UPDATE tenants SET status='readonly_quota', updated_at=? WHERE code=?", stamp(), code)
						if tm.logger != nil {
							tm.logger.Printf("[TenantManager Quota] 租户 [%s] 空间达到 100%% (已用 %.2f MB)，触发只读锁定", code, float64(used)/(1024*1024))
						}
					}
				}
			}
		}

		// 异步更新主库 tenants.current_storage_bytes
		_, _ = tm.mainApp.DB.Exec("UPDATE tenants SET current_storage_bytes=? WHERE code=?", used, code)
	}
}

// runBackupScheduler 优化意见六：脏标记跳过 + 哈希时间片错峰抖动防 I/O 风暴
func (tm *TenantManager) runBackupScheduler() {
	tm.mu.RLock()
	var targets []*TenantInstance
	for _, inst := range tm.instances {
		if inst.TenantCode == "default" {
			continue
		}
		// 静默租户零备份：未发生写事务直接跳过
		if inst.LastModified.Load() <= inst.LastBackup.Load() {
			continue
		}
		targets = append(targets, inst)
	}
	tm.mu.RUnlock()

	for _, inst := range targets {
		go func(instance *TenantInstance) {
			// 哈希错峰抖动：根据 crc32(tenant_code) % 900 计算偏移
			jitterSec := int(crc32.ChecksumIEEE([]byte(instance.TenantCode)) % 60) // 控制在1分钟内错峰
			time.Sleep(time.Duration(jitterSec) * time.Second)

			backupDir := filepath.Join(tm.dataDir, "tenants", instance.TenantCode, "backups")
			_ = os.MkdirAll(backupDir, 0700)
			backupFile := filepath.Join(backupDir, fmt.Sprintf("snapshot_%s.db", time.Now().Format("20060102150405")))

			if instance.App != nil && instance.App.DB != nil {
				_, err := instance.App.DB.Exec(fmt.Sprintf("VACUUM INTO '%s'", filepath.ToSlash(backupFile)))
				if err == nil {
					instance.LastBackup.Store(time.Now().Unix())
					if tm.logger != nil {
						tm.logger.Printf("[TenantManager Backup] 租户 [%s] 错峰快照备份成功: %s", instance.TenantCode, filepath.Base(backupFile))
					}
				}
			}
		}(inst)
	}
}

// Close 关闭租户管理器并释放所有资源
func (tm *TenantManager) Close() {
	close(tm.stopChan)
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for _, inst := range tm.instances {
		if inst.TenantCode != "default" && inst.App != nil {
			_, _ = inst.App.DB.Exec("PRAGMA wal_checkpoint(PASSIVE)")
			_ = inst.App.Close()
		}
	}
}

// GetOrOpenTenant 核心实例获取器（支持懒加载、LRU淘汰、Schema即时灰度迁移与配额缓存）
func (tm *TenantManager) GetOrOpenTenant(code string) (*TenantInstance, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" || code == "default" {
		tm.mu.RLock()
		inst := tm.instances["default"]
		tm.mu.RUnlock()
		inst.LastAccessed.Store(time.Now().Unix())
		return inst, nil
	}

	if !validTenantCode.MatchString(code) {
		return nil, fmt.Errorf("非法租户代号格式: %s", code)
	}

	// 路径穿越防御
	cleanCode := filepath.Clean(code)
	if cleanCode != code || strings.Contains(code, "..") || strings.ContainsAny(code, "/\\") {
		return nil, fmt.Errorf("安全拦截：租户代号包含非法路径字符")
	}

	tm.mu.RLock()
	inst, exists := tm.instances[code]
	tm.mu.RUnlock()

	if exists {
		inst.LastAccessed.Store(time.Now().Unix())
		return inst, nil
	}

	// 校验主库中租户状态
	var status, name string
	var maxStorageMB int64
	var dbVersion int
	err := tm.mainApp.DB.QueryRow(`
		SELECT status, name, max_storage_mb, db_version
		FROM tenants WHERE code=? OR tenant_code=?
	`, code, code).Scan(&status, &name, &maxStorageMB, &dbVersion)
	if err != nil {
		return nil, fmt.Errorf("租户不存在: %s", code)
	}
	if status == "suspended" {
		return &TenantInstance{TenantCode: code, Status: "suspended"}, nil
	}
	if status == "archived" {
		return nil, fmt.Errorf("租户 [%s] 已经归档冷备，禁止访问", code)
	}

	// 物理沙箱目录拓扑规范
	tenantDir := filepath.Join(tm.dataDir, "tenants", code)
	_ = os.MkdirAll(filepath.Join(tenantDir, "backups"), 0700)
	_ = os.MkdirAll(filepath.Join(tenantDir, "attachments"), 0700)

	tenantApp, err := Open(tenantDir, tm.publicKey, tm.logger)
	if err != nil {
		return nil, fmt.Errorf("打开租户物理沙箱数据库失败: %w", err)
	}
	tenantApp.TenantCode = code
	tenantApp.TenantManager = tm
	if tm.mainApp != nil {
		tenantApp.IPLimiter = tm.mainApp.IPLimiter
		tenantApp.CaptchaStore = tm.mainApp.CaptchaStore
	}

	// 优化意见三：懒加载即时 Schema 灰度升级机制 (Lazy Migration)
	var currentVersion int
	_ = tenantApp.DB.QueryRow("PRAGMA user_version").Scan(&currentVersion)
	if currentVersion < TargetSchemaVersion {
		// 升级前自动快照留痕备份
		snapPath := filepath.Join(tenantDir, "backups", fmt.Sprintf("pre_upgrade_v%d_%s.db", currentVersion, time.Now().Format("20060102150405")))
		_, _ = tenantApp.DB.Exec(fmt.Sprintf("VACUUM INTO '%s'", filepath.ToSlash(snapPath)))

		// 执行增量 Schema 升级
		_ = tenantApp.write(func(tx *sql.Tx) error {
			// 确保 document_sequences 和索引齐备
			_, _ = tx.Exec("CREATE INDEX IF NOT EXISTS idx_doc_items_doc_id ON document_items(document_id)")
			_, _ = tx.Exec("CREATE INDEX IF NOT EXISTS idx_inv_tx_doc ON inventory_transactions(document_id)")
			_, _ = tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", TargetSchemaVersion))
			return nil
		})
		_, _ = tm.mainApp.DB.Exec("UPDATE tenants SET db_version=?, updated_at=? WHERE code=?", TargetSchemaVersion, stamp(), code)
	}

	_ = tenantApp.EnsureTenantUser(code, name, "")

	// 物理容量初始化与原子计数器绑定
	usedBytes, _ := tm.GetTenantStorageUsage(code)
	nowUnix := time.Now().Unix()

	newInstance := &TenantInstance{
		TenantCode: code,
		App:        tenantApp,
		Status:     status,
	}
	newInstance.UsedBytes.Store(usedBytes)
	newInstance.MaxQuotaBytes.Store(maxStorageMB * 1024 * 1024)
	newInstance.LastAccessed.Store(nowUnix)
	newInstance.LastModified.Store(nowUnix)
	newInstance.LastBackup.Store(nowUnix)

	tm.mu.Lock()
	// 双重检查：如果在此期间其他协程已创建，则使用已存在的
	if existing, already := tm.instances[code]; already {
		tm.mu.Unlock()
		_ = tenantApp.Close()
		existing.LastAccessed.Store(time.Now().Unix())
		return existing, nil
	}

	// LRU 容量保护：超过池上限时淘汰最旧空闲实例
	if len(tm.instances) >= tm.maxInstances {
		var oldestCode string
		var oldestTime int64 = math.MaxInt64
		for k, v := range tm.instances {
			if k == "default" {
				continue
			}
			acc := v.LastAccessed.Load()
			if acc < oldestTime {
				oldestTime = acc
				oldestCode = k
			}
		}
		if oldestCode != "" {
			oldInst := tm.instances[oldestCode]
			if oldInst.App != nil && oldInst.App.DB != nil {
				_, _ = oldInst.App.DB.Exec("PRAGMA wal_checkpoint(PASSIVE)")
				_ = oldInst.App.Close()
			}
			delete(tm.instances, oldestCode)
			delete(tm.handlers, oldestCode)
			if tm.logger != nil {
				tm.logger.Printf("[TenantManager LRU] 达到池上限 (%d)，已自动置换淘汰空闲租户 [%s]", tm.maxInstances, oldestCode)
			}
		}
	}

	tm.instances[code] = newInstance
	tm.mu.Unlock()

	return newInstance, nil
}

// GetTenantHandler 获得/懒加载租户对应的 HTTP Handler 引擎
func (tm *TenantManager) GetTenantHandler(code string) (http.Handler, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" || code == "default" {
		tm.mu.RLock()
		h := tm.handlers["default"]
		tm.mu.RUnlock()
		if h != nil {
			return h, nil
		}
		tm.mu.Lock()
		defer tm.mu.Unlock()
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

	inst, err := tm.GetOrOpenTenant(code)
	if err != nil {
		return nil, err
	}
	if inst.Status == "suspended" {
		return nil, fmt.Errorf("租户空间已被冻结")
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	if h, exists := tm.handlers[code]; exists {
		return h, nil
	}

	engine := inst.App.Router(tm.assets)
	tm.handlers[code] = engine
	return engine, nil
}

// CheckStorageQuota 优化意见五：配额防抖检查与死锁逃生通道
func (tm *TenantManager) CheckStorageQuota(code string, extra ...any) error {
	reqMethod := "POST"
	reqPath := ""
	isExemptUser := false
	if len(extra) > 0 {
		if s, ok := extra[0].(string); ok {
			reqMethod = s
		}
	}
	if len(extra) > 1 {
		if s, ok := extra[1].(string); ok {
			reqPath = s
		}
	}
	if len(extra) > 2 {
		if b, ok := extra[2].(bool); ok {
			isExemptUser = b
		}
	}

	// 逃生白名单通道：
	// ① 超级管理员发起的一切代维请求与只读/排障请求
	if isExemptUser {
		return nil
	}
	// ② 只读请求（GET/HEAD/OPTIONS）100% 放行
	if reqMethod == "GET" || reqMethod == "HEAD" || reqMethod == "OPTIONS" {
		return nil
	}
	// ③ 用户注销登录接口放行
	if reqPath == "/api/auth/logout" {
		return nil
	}
	// ④ 删除草稿状态单据放行（减容自救）
	if reqMethod == "DELETE" && strings.HasPrefix(reqPath, "/api/documents/") {
		return nil
	}
	// ⑤ 租户空间瘦身与碎片整理维护接口放行
	if strings.Contains(reqPath, "/maintenance") || strings.Contains(reqPath, "/quota") {
		return nil
	}

	tm.mu.RLock()
	inst := tm.instances[code]
	tm.mu.RUnlock()

	var usedBytes, maxBytes int64
	var status string
	if inst != nil {
		usedBytes = inst.UsedBytes.Load()
		maxBytes = inst.MaxQuotaBytes.Load()
		status = inst.Status
		if maxBytes > 0 {
			if actual, err := tm.GetTenantStorageUsage(code); err == nil {
				inst.UsedBytes.Store(actual)
				usedBytes = actual
			}
		}
	} else {
		var maxMB int64
		_ = tm.mainApp.DB.QueryRow("SELECT max_storage_mb, status FROM tenants WHERE code=? OR tenant_code=?", code, code).Scan(&maxMB, &status)
		maxBytes = maxMB * 1024 * 1024
		usedBytes, _ = tm.GetTenantStorageUsage(code)
	}

	if maxBytes <= 0 {
		return nil // 不限容量
	}

	if status == "readonly_quota" || usedBytes >= maxBytes {
		usedMB := float64(usedBytes) / (1024 * 1024)
		maxMB := float64(maxBytes) / (1024 * 1024)
		return fmt.Errorf("租户存储配额已达上限 (已用 %.2f MB / 配额 %.0f MB)，系统已触发只读保护。请联系管理员扩容或清理单据自救", usedMB, maxMB)
	}

	// 记录写计数器
	if inst != nil && inst.WriteCounter.Add(1)%50 == 0 {
		// 每 50 次写事务触发一次异步磁盘校准
		go func(c string) {
			u, e := tm.GetTenantStorageUsage(c)
			if e == nil {
				if i, _ := tm.GetOrOpenTenant(c); i != nil {
					i.UsedBytes.Store(u)
				}
			}
		}(code)
	}

	return nil
}

// GetTenantStorageUsage 计算租户占用的实际物理存储空间（字节）
func (tm *TenantManager) GetTenantStorageUsage(code string) (int64, error) {
	if code == "default" || code == "" {
		var total int64
		for _, name := range []string{"ruilian.db", "ruilian.db-wal", "ruilian.db-shm"} {
			if fi, err := os.Stat(filepath.Join(tm.dataDir, name)); err == nil {
				total += fi.Size()
			}
		}
		logsDir := filepath.Join(tm.dataDir, "logs")
		_ = filepath.Walk(logsDir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				total += fi.Size()
			}
			return nil
		})
		return total, nil
	}

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

// SyncTenantMetrics 优化意见四：旁路增量计算与主库摘要快照更新
func (tm *TenantManager) SyncTenantMetrics(code string) error {
	if code == "default" || code == "" {
		return nil
	}
	inst, err := tm.GetOrOpenTenant(code)
	if err != nil || inst.App == nil {
		return err
	}
	db := inst.App.DB

	var docCount, productCount, customerCount, netSales30d, totalAR, totalInv int64
	var lastActive string

	_ = db.QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount)
	_ = db.QueryRow("SELECT COUNT(*) FROM base_records WHERE kind='product' AND active=1").Scan(&productCount)
	_ = db.QueryRow("SELECT COUNT(*) FROM base_records WHERE kind IN ('customer','supplier') AND active=1").Scan(&customerCount)
	_ = db.QueryRow(`
		SELECT COALESCE(SUM(amount), 0) FROM documents 
		WHERE kind='sales_delivery' AND status='approved' AND created_at >= date('now', '-30 days')
	`).Scan(&netSales30d)
	_ = db.QueryRow("SELECT COALESCE(SUM(unsettled_amount), 0) FROM bills WHERE kind='ar' AND status!='settled'").Scan(&totalAR)
	_ = db.QueryRow("SELECT COALESCE(SUM(quantity * unit_cost), 0) FROM inventory WHERE quantity > 0").Scan(&totalInv)
	_ = db.QueryRow("SELECT COALESCE(MAX(created_at), '') FROM operation_log").Scan(&lastActive)

	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, err = tm.mainApp.DB.Exec(`
		INSERT INTO tenant_metrics_snapshot(tenant_code, doc_count, product_count, customer_count, net_sales_30d, total_ar_amount, total_inventory_val, last_active_at, synced_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_code) DO UPDATE SET
			doc_count=excluded.doc_count,
			product_count=excluded.product_count,
			customer_count=excluded.customer_count,
			net_sales_30d=excluded.net_sales_30d,
			total_ar_amount=excluded.total_ar_amount,
			total_inventory_val=excluded.total_inventory_val,
			last_active_at=excluded.last_active_at,
			synced_at=excluded.synced_at
	`, code, docCount, productCount, customerCount, netSales30d, totalAR, totalInv, lastActive, nowStr)

	return err
}

// SyncAllTenantMetrics 批量刷新所有租户的经营指标快照
func (tm *TenantManager) SyncAllTenantMetrics() {
	rows, err := tm.mainApp.DB.Query("SELECT code FROM tenants WHERE status='active' AND code!='default'")
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err == nil {
			_ = tm.SyncTenantMetrics(code)
		}
	}
}

// ListTenants 获取租户大盘列表（直接读取主库摘要快照，毫秒级响应）
func (tm *TenantManager) ListTenants() ([]Tenant, error) {
	rows, err := tm.mainApp.DB.Query(`
		SELECT t.id, t.code, t.name, t.max_storage_mb, t.current_storage_bytes, t.status, 
		       t.admin_user, t.note, t.created_at, t.updated_at, t.max_users, t.db_version, t.expire_at, t.created_by,
		       COALESCE(s.doc_count, 0), COALESCE(s.product_count, 0), COALESCE(s.customer_count, 0),
		       COALESCE(s.net_sales_30d, 0), COALESCE(s.total_ar_amount, 0), COALESCE(s.total_inventory_val, 0),
		       COALESCE(s.last_active_at, '')
		FROM tenants t
		LEFT JOIN tenant_metrics_snapshot s ON t.code = s.tenant_code
		ORDER BY t.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(
			&t.ID, &t.Code, &t.Name, &t.MaxStorageMB, &t.CurrentStorageBytes, &t.Status,
			&t.AdminUser, &t.Note, &t.CreatedAt, &t.UpdatedAt, &t.MaxUsers, &t.DBVersion, &t.ExpireAt, &t.CreatedBy,
			&t.DocCount, &t.ProductCount, &t.CustomerCount,
			&t.NetSales30d, &t.TotalARAmount, &t.TotalInventoryVal,
			&t.LastActiveAt,
		); err != nil {
			return nil, err
		}

		t.TenantCode = t.Code
		t.CompanyName = t.Name

		// 从内存实例或缓存读取实时物理占用
		tm.mu.RLock()
		inst := tm.instances[t.Code]
		tm.mu.RUnlock()

		var used int64
		if inst != nil {
			used = inst.UsedBytes.Load()
			t.Status = inst.Status
		} else if t.CurrentStorageBytes > 0 {
			used = t.CurrentStorageBytes
		} else {
			used, _ = tm.GetTenantStorageUsage(t.Code)
		}

		t.UsedStorage = used
		t.UsedStorageMB = math.Round(float64(used)/(1024*1024)*100) / 100
		if t.MaxStorageMB > 0 {
			percent := (float64(used) / float64(t.MaxStorageMB*1024*1024)) * 100
			t.StoragePercent = math.Round(percent*10) / 10
			if t.StoragePercent > 100 {
				t.StoragePercent = 100
			}
		}

		list = append(list, t)
	}
	return list, nil
}

// GetTenant 获取单个租户详情与空间指标
func (tm *TenantManager) GetTenant(code string) (Tenant, error) {
	if code == "" {
		code = "default"
	}
	var t Tenant
	err := tm.mainApp.DB.QueryRow(`
		SELECT t.id, t.code, t.name, t.max_storage_mb, t.current_storage_bytes, t.status, 
		       t.admin_user, t.note, t.created_at, t.updated_at, t.max_users, t.db_version, t.expire_at, t.created_by,
		       COALESCE(s.doc_count, 0), COALESCE(s.product_count, 0), COALESCE(s.customer_count, 0),
		       COALESCE(s.net_sales_30d, 0), COALESCE(s.total_ar_amount, 0), COALESCE(s.total_inventory_val, 0),
		       COALESCE(s.last_active_at, '')
		FROM tenants t
		LEFT JOIN tenant_metrics_snapshot s ON t.code = s.tenant_code
		WHERE t.code=? OR t.tenant_code=?
	`, code, code).Scan(
		&t.ID, &t.Code, &t.Name, &t.MaxStorageMB, &t.CurrentStorageBytes, &t.Status,
		&t.AdminUser, &t.Note, &t.CreatedAt, &t.UpdatedAt, &t.MaxUsers, &t.DBVersion, &t.ExpireAt, &t.CreatedBy,
		&t.DocCount, &t.ProductCount, &t.CustomerCount,
		&t.NetSales30d, &t.TotalARAmount, &t.TotalInventoryVal,
		&t.LastActiveAt,
	)
	if err != nil {
		return t, err
	}
	t.TenantCode = t.Code
	t.CompanyName = t.Name

	tm.mu.RLock()
	inst := tm.instances[t.Code]
	tm.mu.RUnlock()

	var used int64
	if inst != nil {
		used = inst.UsedBytes.Load()
		t.Status = inst.Status
	} else {
		used, _ = tm.GetTenantStorageUsage(t.Code)
	}

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

// GetTenantApp 获得租户底层 App 实例
func (tm *TenantManager) GetTenantApp(code string) (*App, error) {
	inst, err := tm.GetOrOpenTenant(code)
	if err != nil {
		return nil, err
	}
	return inst.App, nil
}

// RegisterSession 登记会话所属租户空间
func (tm *TenantManager) RegisterSession(token string, tenantCode string) {
	if tm == nil || token == "" {
		return
	}
	tm.sessions.Store(tokenHash(token), tenantCode)
}

// UnregisterSession 注销会话登记
func (tm *TenantManager) UnregisterSession(token string) {
	if tm == nil || token == "" {
		return
	}
	tm.sessions.Delete(tokenHash(token))
}

// GetTenantForSession 查询会话所属租户代号
func (tm *TenantManager) GetTenantForSession(token string) string {
	if tm == nil || token == "" {
		return ""
	}
	if v, ok := tm.sessions.Load(tokenHash(token)); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// AuthenticateAnyTenant 遍历当前活跃租户沙箱校验会话
func (tm *TenantManager) AuthenticateAnyTenant(token string) (User, bool) {
	if tm == nil || len(token) != 64 {
		return User{}, false
	}
	tHash := tokenHash(token)
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	for code, inst := range tm.instances {
		if inst.App == nil || inst.App.DB == nil {
			continue
		}
		var uid int64
		err := inst.App.DB.QueryRow("SELECT user_id FROM sessions WHERE token_hash=? AND expires_at>?", tHash, stamp()).Scan(&uid)
		if err == nil {
			u, errLoad := loadUser(inst.App.DB, uid)
			if errLoad == nil {
				u.TenantCode = code
				tm.sessions.Store(tHash, code)
				return u, true
			}
		}
	}
	return User{}, false
}

// UpdateTenantLastModified 更新租户最后一次写事务时间戳
func (tm *TenantManager) UpdateTenantLastModified(code string) {
	if tm == nil || code == "" || code == "default" {
		return
	}
	tm.mu.RLock()
	inst := tm.instances[code]
	tm.mu.RUnlock()
	if inst != nil {
		inst.LastModified.Store(time.Now().Unix())
	}
}

// UpdateTenantStorage 调整租户配额（向后兼容别名）
func (tm *TenantManager) UpdateTenantStorage(code string, maxStorageMB int64) error {
	return tm.UpdateTenantQuota(code, maxStorageMB)
}

// CreateTenant 开通全新租户物理沙箱
func (tm *TenantManager) CreateTenant(code, name string, maxStorageMB int64, adminPassword, note string, extra ...any) (*Tenant, error) {
	expireAt := ""
	maxUsers := 50
	if len(extra) > 0 {
		if s, ok := extra[0].(string); ok {
			expireAt = s
		}
	}
	if len(extra) > 1 {
		if n, ok := extra[1].(int); ok {
			maxUsers = n
		}
	}

	code = strings.ToLower(strings.TrimSpace(code))
	name = strings.TrimSpace(name)
	if !validTenantCode.MatchString(code) {
		return nil, fail("租户代号须为 3-32 位小写字母、数字或下划线")
	}
	if name == "" {
		return nil, fail("租户名称不能为空")
	}
	if code == "default" || code == "admin" || code == "huxinkui" {
		return nil, fail(fmt.Sprintf("租户代号不能为系统保留关键字: %s", code))
	}
	if maxStorageMB < 0 {
		return nil, fail("空间配额不能为负数")
	}
	if maxStorageMB == 0 {
		maxStorageMB = 500
	}
	if maxUsers <= 0 {
		maxUsers = 50
	}
	if adminPassword == "" {
		adminPassword = DefaultAdminPassword
	}

	now := time.Now().UTC().Format(time.RFC3339)
	adminUser := code

	res, err := tm.mainApp.DB.Exec(`
		INSERT INTO tenants(code, tenant_code, name, company_name, max_storage_mb, current_storage_bytes, max_users, db_version, expire_at, created_by, status, admin_user, note, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, 0, ?, 2, ?, 'huxinkui', 'active', ?, ?, ?, ?)
	`, code, code, name, name, maxStorageMB, maxUsers, expireAt, adminUser, note, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fail(fmt.Sprintf("租户代号 %s 已存在", code))
		}
		return nil, err
	}
	tenantID, _ := res.LastInsertId()

	// 原子化目录与独立数据库初始化
	tenantDir := filepath.Join(tm.dataDir, "tenants", code)
	_ = os.MkdirAll(filepath.Join(tenantDir, "backups"), 0700)
	_ = os.MkdirAll(filepath.Join(tenantDir, "attachments"), 0700)

	tenantApp, err := Open(tenantDir, tm.publicKey, tm.logger)
	if err != nil {
		_, _ = tm.mainApp.DB.Exec("DELETE FROM tenants WHERE id=?", tenantID)
		_ = os.RemoveAll(tenantDir)
		return nil, fail(fmt.Sprintf("初始化租户物理数据库失败: %v", err))
	}

	tenantApp.TenantCode = code
	tenantApp.TenantManager = tm
	if tm.mainApp != nil {
		tenantApp.IPLimiter = tm.mainApp.IPLimiter
		tenantApp.CaptchaStore = tm.mainApp.CaptchaStore
	}

	_ = tenantApp.EnsureTenantUser(code, name, adminPassword)

	_ = tenantApp.write(func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE settings SET value=? WHERE key='company_name'", js(name))
		return e
	})

	nowUnix := time.Now().Unix()
	used, _ := tm.GetTenantStorageUsage(code)
	inst := &TenantInstance{
		TenantCode: code,
		App:        tenantApp,
		Status:     "active",
	}
	inst.UsedBytes.Store(used)
	inst.MaxQuotaBytes.Store(maxStorageMB * 1024 * 1024)
	inst.LastAccessed.Store(nowUnix)
	inst.LastModified.Store(nowUnix)
	inst.LastBackup.Store(nowUnix)

	tm.mu.Lock()
	tm.instances[code] = inst
	if tm.assets != nil {
		tm.handlers[code] = tenantApp.Router(tm.assets)
	}
	tm.mu.Unlock()

	// 初始化摘要快照
	_ = tm.SyncTenantMetrics(code)

	return &Tenant{
		ID:                  tenantID,
		Code:                code,
		TenantCode:          code,
		Name:                name,
		CompanyName:         name,
		MaxStorageMB:        maxStorageMB,
		CurrentStorageBytes: used,
		UsedStorage:         used,
		UsedStorageMB:       math.Round(float64(used)/(1024*1024)*100) / 100,
		StoragePercent:      0.1,
		Status:              "active",
		MaxUsers:            maxUsers,
		DBVersion:           2,
		ExpireAt:            expireAt,
		CreatedBy:           "huxinkui",
		AdminUser:           adminUser,
		Note:                note,
		CreatedAt:           now,
		UpdatedAt:           now,
	}, nil
}

// UpdateTenantQuota 动态调整租户存储配额（支持超额解封与即时生效）
func (tm *TenantManager) UpdateTenantQuota(code string, maxStorageMB int64) error {
	if maxStorageMB < 0 {
		return fail("空间配额不能为负数")
	}
	now := time.Now().UTC().Format(time.RFC3339)

	res, err := tm.mainApp.DB.Exec("UPDATE tenants SET max_storage_mb=?, updated_at=? WHERE code=?", maxStorageMB, now, code)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(fmt.Sprintf("租户不存在: %s", code))
	}

	tm.mu.RLock()
	inst := tm.instances[code]
	tm.mu.RUnlock()

	if inst != nil {
		inst.MaxQuotaBytes.Store(maxStorageMB * 1024 * 1024)
		// 如果此前处于 readonly_quota 且扩容后容量充裕，立即解封
		if inst.Status == "readonly_quota" {
			if maxStorageMB == 0 || inst.UsedBytes.Load() < maxStorageMB*1024*1024*92/100 {
				inst.Status = "active"
				_, _ = tm.mainApp.DB.Exec("UPDATE tenants SET status='active', updated_at=? WHERE code=?", now, code)
			}
		}
	}
	return nil
}

// UpdateTenant 基本信息修改
func (tm *TenantManager) UpdateTenant(code, name, note string, maxStorageMB int64) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fail("租户名称不能为空")
	}
	if maxStorageMB < 0 {
		return fail("空间配额不能为负数")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tm.mainApp.DB.Exec(`
		UPDATE tenants SET name=?, company_name=?, note=?, max_storage_mb=?, updated_at=? WHERE code=?
	`, name, name, note, maxStorageMB, now, code)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(fmt.Sprintf("租户不存在: %s", code))
	}

	if inst, _ := tm.GetOrOpenTenant(code); inst != nil && inst.App != nil {
		_ = inst.App.write(func(tx *sql.Tx) error {
			_, e := tx.Exec("UPDATE settings SET value=? WHERE key='company_name'", js(name))
			return e
		})
		inst.MaxQuotaBytes.Store(maxStorageMB * 1024 * 1024)
	}
	return nil
}

// SetTenantStatus 租户生命周期启停/冻结管控
func (tm *TenantManager) SetTenantStatus(code, status string) error {
	if code == "default" && (status == "suspended" || status == "disabled") {
		return fail("系统主管理空间禁止停用或冻结")
	}
	validStatus := map[string]bool{"active": true, "suspended": true, "disabled": true, "readonly_quota": true}
	if !validStatus[status] {
		return fail(fmt.Sprintf("无效的租户状态: %s", status))
	}
	if status == "disabled" {
		status = "suspended"
	}

	var curStatus string
	err := tm.mainApp.DB.QueryRow("SELECT status FROM tenants WHERE code=?", code).Scan(&curStatus)
	if err == sql.ErrNoRows {
		return fail(fmt.Sprintf("租户不存在: %s", code))
	}
	if curStatus == "archived" {
		return fail(fmt.Sprintf("租户 [%s] 已经归档冷备，物理沙箱已销毁，无法重新启用或变更状态", code))
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tm.mainApp.DB.Exec("UPDATE tenants SET status=?, updated_at=? WHERE code=?", status, now, code)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(fmt.Sprintf("租户不存在: %s", code))
	}

	tm.mu.Lock()
	inst := tm.instances[code]
	if status == "suspended" {
		delete(tm.instances, code)
		delete(tm.handlers, code)
	} else if inst != nil {
		inst.Status = status
	}
	tm.mu.Unlock()

	if status == "suspended" && inst != nil && inst.App != nil {
		// 停用冻结时清空其所有在线 Session 并关闭数据库连接池（锁外安全执行）
		_ = inst.App.write(func(tx *sql.Tx) error {
			_, _ = tx.Exec("DELETE FROM sessions")
			return nil
		})
		_, _ = inst.App.DB.Exec("PRAGMA wal_checkpoint(PASSIVE)")
		_ = inst.App.Close()
	}

	return nil
}

// ResetTenantPassword 穿透重置租户管理员密码并吊销历史在线凭证
func (tm *TenantManager) ResetTenantPassword(code, newPassword string) error {
	if code == "default" {
		return fail("默认系统主空间超级管理员密码请在个人安全中心修改")
	}
	if len(newPassword) < 8 {
		return fail("新密码长度至少需要8位且包含字母与数字")
	}
	var curStatus string
	err := tm.mainApp.DB.QueryRow("SELECT status FROM tenants WHERE code=?", code).Scan(&curStatus)
	if err == sql.ErrNoRows {
		return fail(fmt.Sprintf("租户不存在: %s", code))
	}
	if curStatus == "archived" {
		return fail(fmt.Sprintf("租户 [%s] 已经归档冷备，物理空间已销毁，无法重置密码", code))
	}

	inst, err := tm.GetOrOpenTenant(code)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		return err
	}

	return inst.App.write(func(tx *sql.Tx) error {
		// 更新密码并清空 Session
		if _, e := tx.Exec("UPDATE users SET password_hash=?, updated_at=? WHERE username=?", string(hash), stamp(), code); e != nil {
			return e
		}
		_, _ = tx.Exec("DELETE FROM sessions")
		return nil
	})
}

// InspectTenant 查看租户后台深度详情（穿透读取业务KPI、表行数分布、WAL体积、活跃会话与审计日志）
func (tm *TenantManager) InspectTenant(code string) (M, error) {
	var curStatus string
	err := tm.mainApp.DB.QueryRow("SELECT status FROM tenants WHERE code=?", code).Scan(&curStatus)
	if err == sql.ErrNoRows {
		return nil, fail(fmt.Sprintf("租户不存在: %s", code))
	}
	if curStatus == "archived" {
		return nil, fail(fmt.Sprintf("租户 [%s] 已经归档冷备，物理空间已安全销毁，无法查看实时画像", code))
	}

	inst, err := tm.GetOrOpenTenant(code)
	if err != nil {
		return nil, err
	}
	db := inst.App.DB

	// 1. 各业务表行数分布
	tableRows := make(map[string]int64)
	for _, tbl := range []string{"documents", "document_items", "base_records", "inventory", "bills", "operation_log", "sessions"} {
		var cnt int64
		_ = db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tbl)).Scan(&cnt)
		tableRows[tbl] = cnt
	}

	// 2. 物理文件信息与 WAL 体积
	tenantDir := filepath.Join(tm.dataDir, "tenants", code)
	if code == "default" {
		tenantDir = tm.dataDir
	}
	var dbSize, walSize, shmSize int64
	if fi, err := os.Stat(filepath.Join(tenantDir, "ruilian.db")); err == nil {
		dbSize = fi.Size()
	}
	if fi, err := os.Stat(filepath.Join(tenantDir, "ruilian.db-wal")); err == nil {
		walSize = fi.Size()
	}
	if fi, err := os.Stat(filepath.Join(tenantDir, "ruilian.db-shm")); err == nil {
		shmSize = fi.Size()
	}

	// 3. 活跃会话
	sessionRows, _ := queryMaps(db, `
		SELECT s.token_hash, s.expires_at, s.created_at, u.username, u.name, r.name as role_name
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		JOIN roles r ON r.id = u.role_id
		WHERE s.expires_at > ? ORDER BY s.created_at DESC LIMIT 10
	`, stamp())

	// 4. 最近 20 条操作审计
	auditLogs, _ := queryMaps(db, `
		SELECT id, username, ip, action, module, ref_id, created_at
		FROM operation_log ORDER BY id DESC LIMIT 20
	`)

	// 5. 经营数据摘要
	_ = tm.SyncTenantMetrics(code)
	t, _ := tm.GetTenant(code)

	return M{
		"tenant":           t,
		"table_rows":       tableRows,
		"db_size_bytes":    dbSize,
		"wal_size_bytes":   walSize,
		"shm_size_bytes":   shmSize,
		"active_sessions":  sessionRows,
		"recent_logs":      auditLogs,
		"inspected_at":     stamp(),
	}, nil
}

// CreateImpersonation 生成超级管理员跨租户穿透凭证
func (tm *TenantManager) CreateImpersonation(adminUser, targetTenant, mode, clientIP string) (*ImpersonationSession, error) {
	if adminUser != MasterAdminUsername {
		return nil, denied(fmt.Sprintf("仅超级管理员 (%s) 允许申请穿透巡检令牌", MasterAdminUsername))
	}
	if mode != "readwrite" {
		mode = "readonly" // 默认只读模式
	}
	targetTenant = strings.ToLower(strings.TrimSpace(targetTenant))
	if targetTenant == "" || targetTenant == "default" {
		return nil, fail("穿透目标租户代号无效")
	}

	// 校验目标租户有效性与状态
	var curStatus string
	err := tm.mainApp.DB.QueryRow("SELECT status FROM tenants WHERE code=?", targetTenant).Scan(&curStatus)
	if err == sql.ErrNoRows {
		return nil, fail(fmt.Sprintf("目标租户不存在: %s", targetTenant))
	}
	if curStatus == "archived" {
		return nil, fail(fmt.Sprintf("目标租户 [%s] 已经归档冷备，物理空间已销毁，无法穿透巡检", targetTenant))
	}
	if curStatus == "suspended" {
		return nil, fail(fmt.Sprintf("目标租户 [%s] 已被管理员冻结停用", targetTenant))
	}

	if _, err := tm.GetOrOpenTenant(targetTenant); err != nil {
		return nil, err
	}

	token := uid() + uid()
	hash := tokenHash(token)
	now := time.Now()
	expiresAt := now.Add(30 * time.Minute)

	sess := &ImpersonationSession{
		Token:         token,
		AdminUsername: adminUser,
		TargetTenant:  targetTenant,
		AccessMode:    mode,
		ClientIP:      clientIP,
		StartedAt:     now,
		ExpiresAt:     expiresAt,
		ActionsCount:  0,
	}

	tm.impMu.Lock()
	tm.impersonations[token] = sess
	tm.impMu.Unlock()

	// 记录到平台审计表 admin_impersonation_logs
	_, _ = tm.mainApp.DB.Exec(`
		INSERT INTO admin_impersonation_logs(admin_username, target_tenant_code, access_mode, client_ip, token_hash, started_at)
		VALUES(?, ?, ?, ?, ?, ?)
	`, adminUser, targetTenant, mode, clientIP, hash, now.UTC().Format(time.RFC3339))

	return sess, nil
}

// GetImpersonation 验证并获取穿透巡检凭证
func (tm *TenantManager) GetImpersonation(token string) *ImpersonationSession {
	if len(token) != 64 {
		return nil
	}
	tm.impMu.RLock()
	sess, exists := tm.impersonations[token]
	tm.impMu.RUnlock()

	if !exists {
		return nil
	}
	if time.Now().After(sess.ExpiresAt) {
		tm.impMu.Lock()
		delete(tm.impersonations, token)
		tm.impMu.Unlock()
		return nil
	}
	return sess
}

// RecordImpersonationAction 累加巡检操作计数
func (tm *TenantManager) RecordImpersonationAction(token string) {
	if sess := tm.GetImpersonation(token); sess != nil {
		sess.ActionsCount++
		_, _ = tm.mainApp.DB.Exec("UPDATE admin_impersonation_logs SET actions_count=actions_count+1 WHERE token_hash=?", tokenHash(token))
	}
}

// Maintenance 执行空间维护（PRAGMA wal_checkpoint 或 VACUUM INTO 快照）
func (tm *TenantManager) Maintenance(code, action string) (M, error) {
	var curStatus string
	err := tm.mainApp.DB.QueryRow("SELECT status FROM tenants WHERE code=?", code).Scan(&curStatus)
	if err == sql.ErrNoRows {
		return nil, fail(fmt.Sprintf("租户不存在: %s", code))
	}
	if curStatus == "archived" {
		return nil, fail(fmt.Sprintf("租户 [%s] 已经归档冷备，无法执行空间维护", code))
	}

	inst, err := tm.GetOrOpenTenant(code)
	if err != nil {
		return nil, err
	}
	db := inst.App.DB

	switch action {
	case "checkpoint":
		var a, b, c int
		err := db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&a, &b, &c)
		if err != nil {
			return nil, fail(fmt.Sprintf("执行 WAL Checkpoint 失败: %v", err))
		}
		tm.syncStorageAndHysteresis()
		return M{"ok": true, "action": "checkpoint", "busy": a, "log": b, "checkpointed": c, "message": "WAL 空间已成功回收并截断"}, nil

	case "vacuum", "snapshot":
		backupDir := filepath.Join(tm.dataDir, "tenants", code, "backups")
		if code == "default" {
			backupDir = filepath.Join(tm.dataDir, "backups")
		}
		_ = os.MkdirAll(backupDir, 0700)
		targetFile := filepath.Join(backupDir, fmt.Sprintf("maintenance_snap_%s.db", time.Now().Format("20060102150405")))
		_, err := db.Exec(fmt.Sprintf("VACUUM INTO '%s'", filepath.ToSlash(targetFile)))
		if err != nil {
			return nil, fail(fmt.Sprintf("创建快照失败: %v", err))
		}
		fi, _ := os.Stat(targetFile)
		return M{
			"ok":          true,
			"action":      "snapshot",
			"file":        filepath.Base(targetFile),
			"size_bytes":  fi.Size(),
			"message":     "快照生成与空间整理完成",
		}, nil

	default:
		return nil, fail(fmt.Sprintf("未知维护指令: %s", action))
	}
}

// ArchiveTenant 租户归档与销毁（完整打包压缩 tar.gz + SHA256 计算 + 移入冷备区）
// 若 tenant 已经处于 archived 状态且 purge=true，支持彻底清理租户元数据与注册信息
func (tm *TenantManager) ArchiveTenant(code, confirmPassword string, operator User, clientIP string, purge ...bool) (M, error) {
	if code == "default" {
		return nil, fail("默认主管理空间严禁归档或销毁")
	}
	if operator.Username != MasterAdminUsername {
		return nil, denied(fmt.Sprintf("仅超级管理员 (%s) 拥有租户空间归档销毁权", MasterAdminUsername))
	}

	// 二次强密码校验
	var adminHash string
	err := tm.mainApp.DB.QueryRow("SELECT password_hash FROM users WHERE username=?", MasterAdminUsername).Scan(&adminHash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(adminHash), []byte(confirmPassword)) != nil {
		return nil, fail("管理员二级确认密码错误，归档操作已终止")
	}

	// 查询租户是否存在及当前状态
	var currentStatus string
	err = tm.mainApp.DB.QueryRow("SELECT status FROM tenants WHERE code=?", code).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		return nil, fail(fmt.Sprintf("租户不存在: %s", code))
	} else if err != nil {
		return nil, fmt.Errorf("查询租户状态失败: %w", err)
	}

	shouldPurge := len(purge) > 0 && purge[0]

	// 1. 如果租户已经处于 archived 状态
	if currentStatus == "archived" {
		if shouldPurge {
			_, _ = tm.mainApp.DB.Exec("DELETE FROM tenant_metrics_snapshot WHERE tenant_code=?", code)
			_, _ = tm.mainApp.DB.Exec("DELETE FROM tenants WHERE code=?", code)
			_ = tm.mainApp.write(func(tx *sql.Tx) error {
				return audit(tx, operator, clientIP, "彻底清除已归档租户记录", "tenant", 0, nil, M{
					"tenant_code": code,
				})
			})
			return M{
				"ok":          true,
				"tenant_code": code,
				"purged":      true,
				"message":     fmt.Sprintf("已归档租户 [%s] 的注册元数据已彻底清除", code),
			}, nil
		}
		return nil, fail(fmt.Sprintf("租户 [%s] 已经归档冷备，物理沙箱此前已完成销毁", code))
	}

	// 2. 关闭租户活跃连接池
	tm.mu.Lock()
	if inst, exists := tm.instances[code]; exists {
		if inst.App != nil && inst.App.DB != nil {
			_, _ = inst.App.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
			_ = inst.App.Close()
		}
		delete(tm.instances, code)
		delete(tm.handlers, code)
	}
	tm.mu.Unlock()

	tenantDir := filepath.Join(tm.dataDir, "tenants", code)
	fi, statErr := os.Stat(tenantDir)
	if os.IsNotExist(statErr) || (statErr == nil && !fi.IsDir()) {
		// 物理目录不存在，直接更新主库状态为 archived
		nowStr := time.Now().UTC().Format(time.RFC3339)
		_, _ = tm.mainApp.DB.Exec("UPDATE tenants SET status='archived', updated_at=? WHERE code=?", nowStr, code)
		_ = tm.mainApp.write(func(tx *sql.Tx) error {
			return audit(tx, operator, clientIP, "归档销毁租户空间", "tenant", 0, nil, M{
				"tenant_code": code,
				"note":        "物理沙箱目录已不存在，直接更新状态为已归档",
			})
		})
		return M{
			"ok":           true,
			"tenant_code":  code,
			"archive_file": "",
			"archive_name": "",
			"sha256":       "",
			"message":      fmt.Sprintf("租户 [%s] 物理沙箱目录已不存在，空间状态已成功更新为已归档", code),
		}, nil
	}

	// 3. 创建归档冷备目录
	archiveDir := filepath.Join(tm.dataDir, "backups", "archive")
	_ = os.MkdirAll(archiveDir, 0700)
	archiveName := fmt.Sprintf("tenant_%s_%s.tar.gz", code, time.Now().Format("20060102150405"))
	archivePath := filepath.Join(archiveDir, archiveName)

	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return nil, fail(fmt.Sprintf("创建归档文件失败: %v", err))
	}
	defer archiveFile.Close()

	hasher := sha256.New()
	mw := io.MultiWriter(archiveFile, hasher)
	gw := gzip.NewWriter(mw)
	tw := tar.NewWriter(gw)

	err = filepath.Walk(tenantDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(tenantDir, p)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(fi, fi.Name())
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, f)
			f.Close()
			if copyErr != nil {
				return copyErr
			}
		}
		return nil
	})
	_ = tw.Close()
	_ = gw.Close()

	if err != nil {
		_ = os.Remove(archivePath)
		return nil, fail(fmt.Sprintf("压缩打包租户沙箱失败: %v", err))
	}

	sha256Hex := hex.EncodeToString(hasher.Sum(nil))

	// 4. 将原目录安全转移或移除，并在主库标记状态为 archived
	_ = os.RemoveAll(tenantDir)
	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, _ = tm.mainApp.DB.Exec("UPDATE tenants SET status='archived', updated_at=? WHERE code=?", nowStr, code)

	// 5. 记录主库审计
	_ = tm.mainApp.write(func(tx *sql.Tx) error {
		return audit(tx, operator, clientIP, "归档销毁租户空间", "tenant", 0, nil, M{
			"tenant_code":  code,
			"archive_file": archiveName,
			"sha256":       sha256Hex,
		})
	})

	return M{
		"ok":           true,
		"tenant_code":  code,
		"archive_file": archiveName,
		"archive_name": archiveName,
		"sha256":       sha256Hex,
		"message":      "租户沙箱已完整打包压缩移入归档冷备区，状态已置为 archived",
	}, nil
}
