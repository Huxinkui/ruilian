package app

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ruilian/internal/branding"

	_ "modernc.org/sqlite"
)

type App struct {
	DB           *sql.DB
	DataDir      string
	writeMu      sync.Mutex
	backupMu     sync.Mutex
	License      *LicenseService
	Stop         func()
	DesktopToken string
	loginMu      sync.Mutex
	attempts     map[string]*loginAttempts
	Logger       *log.Logger
}

func Open(dataDir string, publicKey []byte, logger *log.Logger) (*App, error) {
	absDir, e := filepath.Abs(dataDir)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(absDir, 0700); e != nil {
		return nil, e
	}
	u := url.URL{Scheme: "file", Path: sqliteURIPath(filepath.Join(absDir, "ruilian.db"))}
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(10000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Set("_txlock", "immediate")
	u.RawQuery = query.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if e = db.Ping(); e != nil {
		db.Close()
		return nil, fmt.Errorf("打开 SQLite %s: %w", u.String(), e)
	}
	a := &App{DB: db, DataDir: absDir, Logger: logger, attempts: map[string]*loginAttempts{}}
	if a.Logger == nil {
		a.Logger = log.Default()
	}
	if e = a.migrate(); e != nil {
		db.Close()
		return nil, e
	}
	a.License = NewLicenseService(a, publicKey)
	if e = a.License.Initialize(); e != nil {
		db.Close()
		return nil, e
	}
	return a, nil
}

func (a *App) Close() error { return a.DB.Close() }
func sqliteURIPath(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}
func (a *App) write(fn func(*sql.Tx) error) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit()
}

func (a *App) migrate() error {
	var version int
	if e := a.DB.QueryRow("PRAGMA user_version").Scan(&version); e != nil {
		return e
	}
	if version > 1 {
		return fmt.Errorf("数据库来自较新的软件版本，不能使用当前程序打开")
	}
	return a.write(func(tx *sql.Tx) error {
		_, e := tx.Exec(schema)
		if e != nil {
			return e
		}
		defaults := map[string]any{
			"company_name": "我的企业", "app_name": branding.Default.AppName, "brand_short": branding.Default.BrandShort, "brand_logo": "",
			"allow_negative_stock": false, "credit_policy": "block", "overdue_policy": "warn", "low_price_policy": "block",
			"stale_days": 90, "backup_interval_minutes": 15, "backup_daily_keep": 14, "backup_weekly_keep": 12, "backup_monthly_keep": 12,
			"backup_destinations": []string{}, "document_prefixes": M{}, "currency": "CNY",
		}
		for k, v := range defaults {
			if _, e = tx.Exec("INSERT OR IGNORE INTO settings(key,value) VALUES(?,?)", k, js(v)); e != nil {
				return e
			}
		}
		_, e = tx.Exec("PRAGMA user_version=1")
		return e
	})
}

func setting(q Queryer, key string) string {
	var v string
	_ = q.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	return v
}
func settingString(q Queryer, key string) string {
	v := setting(q, key)
	if len(v) > 1 && v[0] == '"' {
		return str(obj("{\"v\":"+v+"}"), "v")
	}
	return v
}
func settingBool(q Queryer, key string) bool { return setting(q, key) == "true" }
func audit(q Queryer, u User, ip, action, entity string, id int64, before, after any) error {
	_, e := q.Exec("INSERT INTO operation_log(user_id,username,ip,action,entity,entity_id,before_data,after_data,created_at) VALUES(?,?,?,?,?,?,?,?,?)", u.ID, u.Username, ip, action, entity, id, js(before), js(after), stamp())
	return e
}
func (a *App) recordError(action string, err error) { a.Logger.Printf("%s: %v", action, err) }

const schema = `
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS roles(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT NOT NULL UNIQUE,permissions TEXT NOT NULL,warehouse_ids TEXT NOT NULL DEFAULT '[]',data_scope TEXT NOT NULL DEFAULT 'all',built_in INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY AUTOINCREMENT,username TEXT NOT NULL UNIQUE,name TEXT NOT NULL,password_hash TEXT NOT NULL,role_id INTEGER NOT NULL REFERENCES roles(id),organization_id INTEGER NOT NULL DEFAULT 0,active INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY,user_id INTEGER NOT NULL REFERENCES users(id),expires_at TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS base_records(id INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT NOT NULL,code TEXT NOT NULL,name TEXT NOT NULL,data TEXT NOT NULL DEFAULT '{}',active INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(kind,code));
CREATE INDEX IF NOT EXISTS idx_base_kind ON base_records(kind,active,name);
CREATE TABLE IF NOT EXISTS document_sequences(prefix TEXT NOT NULL,day TEXT NOT NULL,value INTEGER NOT NULL,PRIMARY KEY(prefix,day));
CREATE TABLE IF NOT EXISTS documents(id INTEGER PRIMARY KEY AUTOINCREMENT,number TEXT NOT NULL UNIQUE,kind TEXT NOT NULL,status TEXT NOT NULL,partner_id INTEGER REFERENCES base_records(id),warehouse_id INTEGER REFERENCES base_records(id),target_warehouse_id INTEGER REFERENCES base_records(id),source_id INTEGER REFERENCES documents(id),date TEXT NOT NULL,due_date TEXT NOT NULL DEFAULT '',total INTEGER NOT NULL DEFAULT 0,cost INTEGER NOT NULL DEFAULT 0,note TEXT NOT NULL DEFAULT '',data TEXT NOT NULL DEFAULT '{}',created_by INTEGER NOT NULL REFERENCES users(id),created_at TEXT NOT NULL,updated_at TEXT NOT NULL,approved_by INTEGER REFERENCES users(id),approved_at TEXT NOT NULL DEFAULT '',revision INTEGER NOT NULL DEFAULT 1,reversal_id INTEGER REFERENCES documents(id));
CREATE INDEX IF NOT EXISTS idx_docs_filter ON documents(kind,status,date);
CREATE INDEX IF NOT EXISTS idx_docs_partner ON documents(partner_id,date);
CREATE INDEX IF NOT EXISTS idx_docs_source ON documents(source_id);
CREATE TABLE IF NOT EXISTS document_items(id INTEGER PRIMARY KEY AUTOINCREMENT,document_id INTEGER NOT NULL REFERENCES documents(id),product_id INTEGER NOT NULL REFERENCES base_records(id),product_name TEXT NOT NULL,product_code TEXT NOT NULL,unit TEXT NOT NULL,location_id INTEGER NOT NULL DEFAULT 0,target_location_id INTEGER NOT NULL DEFAULT 0,source_item_id INTEGER REFERENCES document_items(id),batch TEXT NOT NULL DEFAULT '',serials TEXT NOT NULL DEFAULT '[]',flow TEXT NOT NULL DEFAULT '',quantity INTEGER NOT NULL,price INTEGER NOT NULL,amount INTEGER NOT NULL,cost INTEGER NOT NULL DEFAULT 0,book_quantity INTEGER NOT NULL DEFAULT 0,price_source TEXT NOT NULL DEFAULT '',note TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_items_doc ON document_items(document_id);
CREATE INDEX IF NOT EXISTS idx_items_source ON document_items(source_item_id);
CREATE TABLE IF NOT EXISTS inventory(id INTEGER PRIMARY KEY AUTOINCREMENT,product_id INTEGER NOT NULL REFERENCES base_records(id),warehouse_id INTEGER NOT NULL REFERENCES base_records(id),location_id INTEGER NOT NULL DEFAULT 0,batch TEXT NOT NULL DEFAULT '',quantity INTEGER NOT NULL DEFAULT 0,reserved INTEGER NOT NULL DEFAULT 0,value INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL,UNIQUE(product_id,warehouse_id,location_id,batch));
CREATE TABLE IF NOT EXISTS inventory_transactions(id INTEGER PRIMARY KEY AUTOINCREMENT,document_id INTEGER NOT NULL REFERENCES documents(id),item_id INTEGER NOT NULL REFERENCES document_items(id),inventory_id INTEGER NOT NULL REFERENCES inventory(id),before_quantity INTEGER NOT NULL,delta INTEGER NOT NULL,after_quantity INTEGER NOT NULL,cost_delta INTEGER NOT NULL,before_value INTEGER NOT NULL,after_value INTEGER NOT NULL,serials TEXT NOT NULL DEFAULT '[]',created_by INTEGER NOT NULL,created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_inv_tx_doc ON inventory_transactions(document_id);
CREATE INDEX IF NOT EXISTS idx_inv_tx_inv ON inventory_transactions(inventory_id,id);
CREATE TABLE IF NOT EXISTS cost_adjustments(id INTEGER PRIMARY KEY AUTOINCREMENT,document_id INTEGER NOT NULL REFERENCES documents(id),item_id INTEGER NOT NULL REFERENCES document_items(id),amount INTEGER NOT NULL,note TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS inventory_serials(id INTEGER PRIMARY KEY AUTOINCREMENT,product_id INTEGER NOT NULL REFERENCES base_records(id),serial TEXT NOT NULL,inventory_id INTEGER NOT NULL REFERENCES inventory(id),state TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(product_id,serial));
CREATE TABLE IF NOT EXISTS stock_reservations(id INTEGER PRIMARY KEY AUTOINCREMENT,document_id INTEGER NOT NULL REFERENCES documents(id),item_id INTEGER NOT NULL REFERENCES document_items(id),inventory_id INTEGER NOT NULL REFERENCES inventory(id),quantity INTEGER NOT NULL,consumed INTEGER NOT NULL DEFAULT 0,UNIQUE(item_id,inventory_id));
CREATE TABLE IF NOT EXISTS bills(id INTEGER PRIMARY KEY AUTOINCREMENT,document_id INTEGER NOT NULL UNIQUE REFERENCES documents(id),side TEXT NOT NULL,partner_id INTEGER NOT NULL REFERENCES base_records(id),amount INTEGER NOT NULL,settled INTEGER NOT NULL DEFAULT 0,due_date TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'active',created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_bills_party ON bills(side,partner_id,state,due_date);
CREATE TABLE IF NOT EXISTS settlements(id INTEGER PRIMARY KEY AUTOINCREMENT,document_id INTEGER NOT NULL UNIQUE REFERENCES documents(id),side TEXT NOT NULL,partner_id INTEGER NOT NULL REFERENCES base_records(id),amount INTEGER NOT NULL,allocated INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'active',created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS writeoffs(id INTEGER PRIMARY KEY AUTOINCREMENT,settlement_id INTEGER NOT NULL REFERENCES settlements(id),bill_id INTEGER NOT NULL REFERENCES bills(id),amount INTEGER NOT NULL,created_by INTEGER NOT NULL,created_at TEXT NOT NULL,reversed INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS settlement_offsets(id INTEGER PRIMARY KEY AUTOINCREMENT,positive_settlement_id INTEGER NOT NULL REFERENCES settlements(id),negative_settlement_id INTEGER NOT NULL REFERENCES settlements(id),amount INTEGER NOT NULL,created_by INTEGER NOT NULL,created_at TEXT NOT NULL,reversed INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS bill_offsets(id INTEGER PRIMARY KEY AUTOINCREMENT,positive_bill_id INTEGER NOT NULL REFERENCES bills(id),negative_bill_id INTEGER NOT NULL REFERENCES bills(id),amount INTEGER NOT NULL,document_id INTEGER NOT NULL REFERENCES documents(id),created_by INTEGER NOT NULL,created_at TEXT NOT NULL,reversed INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS price_rules(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT NOT NULL,kind TEXT NOT NULL,product_id INTEGER NOT NULL REFERENCES base_records(id),customer_id INTEGER REFERENCES base_records(id),customer_level TEXT NOT NULL DEFAULT '',min_quantity INTEGER NOT NULL DEFAULT 0,max_quantity INTEGER NOT NULL DEFAULT 0,price INTEGER NOT NULL,min_price INTEGER NOT NULL DEFAULT 0,start_date TEXT NOT NULL DEFAULT '',end_date TEXT NOT NULL DEFAULT '',priority INTEGER NOT NULL DEFAULT 0,active INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS warning_ack(warning_key TEXT PRIMARY KEY,created_by INTEGER NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS operation_log(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,username TEXT NOT NULL,ip TEXT NOT NULL,action TEXT NOT NULL,entity TEXT NOT NULL,entity_id INTEGER NOT NULL,before_data TEXT NOT NULL,after_data TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_log_date ON operation_log(created_at);
CREATE TABLE IF NOT EXISTS idempotency(user_id INTEGER NOT NULL,key TEXT NOT NULL,endpoint TEXT NOT NULL,request_hash TEXT NOT NULL,response TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(user_id,key));
CREATE TABLE IF NOT EXISTS backup_records(id INTEGER PRIMARY KEY AUTOINCREMENT,filename TEXT NOT NULL UNIQUE,sha256 TEXT NOT NULL,size INTEGER NOT NULL,integrity TEXT NOT NULL,replicas TEXT NOT NULL,kind TEXT NOT NULL,status TEXT NOT NULL,message TEXT NOT NULL,created_at TEXT NOT NULL,verified_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS background_jobs(id INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT NOT NULL,status TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,message TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS system_installation(id INTEGER PRIMARY KEY CHECK(id=1),installation_id TEXT NOT NULL,first_run TEXT NOT NULL,last_run TEXT NOT NULL,problem TEXT NOT NULL DEFAULT '',seal TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS license_local(id INTEGER PRIMARY KEY CHECK(id=1),payload TEXT NOT NULL,signature TEXT NOT NULL,activated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS license_activation_log(id INTEGER PRIMARY KEY AUTOINCREMENT,license_id TEXT NOT NULL,success INTEGER NOT NULL,message TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS license_event_log(id INTEGER PRIMARY KEY AUTOINCREMENT,event TEXT NOT NULL,message TEXT NOT NULL,created_at TEXT NOT NULL);
`

func (a *App) cleanSessions() {
	_, _ = a.DB.Exec("DELETE FROM sessions WHERE expires_at < ?", time.Now().UTC().Format(time.RFC3339))
}
