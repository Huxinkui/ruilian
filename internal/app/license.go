package app

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ruilian/internal/licensecode"
)

type installationState struct {
	InstallationID string `json:"installation_id"`
	FirstRun       string `json:"first_run"`
	LastRun        string `json:"last_run"`
	Problem        string `json:"problem"`
	Seal           string `json:"seal"`
}
type LicenseStatus struct {
	Product        string               `json:"product"`
	Version        string               `json:"version"`
	Status         string               `json:"status"`
	Message        string               `json:"message"`
	InstallationID string               `json:"installation_id"`
	FirstRun       string               `json:"first_run"`
	TrialEnd       string               `json:"trial_end"`
	DaysRemaining  int                  `json:"days_remaining"`
	Writable       bool                 `json:"writable"`
	Payload        *licensecode.Payload `json:"license,omitempty"`
	ActivatedAt    string               `json:"activated_at,omitempty"`
}
type LicenseService struct {
	app         *App
	mu          sync.Mutex
	public      ed25519.PublicKey
	publicError error
	secret      []byte
	state       installationState
	startWall   time.Time
	startMono   time.Time
	lastPersist time.Time
	lastStatus  string
	lastMessage string
}

func NewLicenseService(a *App, key []byte) *LicenseService {
	public, e := licensecode.PublicKey(key)
	now := time.Now()
	return &LicenseService{app: a, public: public, publicError: e, startWall: now.Round(0), startMono: now}
}
func (l *LicenseService) seal(s installationState) string {
	s.Seal = ""
	h := hmac.New(sha256.New, l.secret)
	_, _ = h.Write([]byte(js(s)))
	return hex.EncodeToString(h.Sum(nil))
}
func (l *LicenseService) valid(s installationState) bool {
	got, e := hex.DecodeString(s.Seal)
	if e != nil {
		return false
	}
	want, _ := hex.DecodeString(l.seal(s))
	return hmac.Equal(got, want)
}
func atomicFile(path string, content []byte, mode os.FileMode) error {
	tmp := path + "." + uid() + ".tmp"
	f, e := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if e != nil {
		return e
	}
	if _, e = f.Write(content); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		_ = os.Remove(tmp)
		return e
	}
	if e = os.Rename(tmp, path); e != nil {
		_ = os.Remove(tmp)
	}
	return e
}

func (l *LicenseService) Initialize() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.publicError != nil {
		return l.publicError
	}
	keyPath := filepath.Join(l.app.DataDir, ".installation.key")
	secret, e := os.ReadFile(keyPath)
	missingKey := os.IsNotExist(e)
	if e != nil && !missingKey {
		return e
	}
	if !missingKey && len(secret) != 32 {
		missingKey = true
	}
	if missingKey {
		secret = make([]byte, 32)
		if _, e = rand.Read(secret); e != nil {
			return e
		}
		if e = atomicFile(keyPath, secret, 0600); e != nil {
			return e
		}
	}
	l.secret = secret
	var db installationState
	e = l.app.DB.QueryRow("SELECT installation_id,first_run,last_run,problem,seal FROM system_installation WHERE id=1").Scan(&db.InstallationID, &db.FirstRun, &db.LastRun, &db.Problem, &db.Seal)
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	hasDB := e == nil
	var local installationState
	raw, fileErr := os.ReadFile(filepath.Join(l.app.DataDir, ".installation.json"))
	hasFile := fileErr == nil && json.Unmarshal(raw, &local) == nil
	if !hasDB && !hasFile {
		l.state = installationState{InstallationID: uid(), FirstRun: stamp(), LastRun: stamp()}
	}
	if hasDB {
		l.state = db
		if missingKey {
			l.state.InstallationID = uid()
			l.state.Problem = "数据库已迁移，需使用本机安装标识重新授权"
		} else if !l.valid(db) {
			l.state.Problem = "数据库安装记录完整性校验失败"
		}
	}
	if hasFile && !hasDB {
		l.state = local
		if !l.valid(local) {
			l.state.Problem = "独立安装记录完整性校验失败"
		}
	}
	if hasDB && !hasFile && !missingKey {
		l.state.Problem = "独立安装记录缺失，需重新输入有效授权码"
	}
	if hasDB && hasFile && !missingKey {
		if !l.valid(local) {
			l.state.Problem = "独立安装记录完整性校验失败"
		} else if local.InstallationID != db.InstallationID || local.FirstRun != db.FirstRun {
			l.state.Problem = "数据库与本机安装标识不一致，需重新授权"
		} else {
			if local.LastRun > l.state.LastRun {
				l.state.LastRun = local.LastRun
			}
			if local.Problem != "" {
				l.state.Problem = local.Problem
			}
		}
	}
	first, er := time.Parse(time.RFC3339, l.state.FirstRun)
	if er != nil || first.After(time.Now().Add(2*time.Minute)) {
		l.state.Problem = "首次运行时间异常"
	}
	last, er := time.Parse(time.RFC3339, l.state.LastRun)
	if er != nil || time.Now().Add(2*time.Minute).Before(last) {
		l.state.Problem = "检测到系统时间回拨，请校准时间后重新授权"
	}
	return l.persist()
}
func (l *LicenseService) persist() error {
	l.state.Seal = l.seal(l.state)
	e := l.app.write(func(tx *sql.Tx) error {
		_, e := tx.Exec("INSERT INTO system_installation(id,installation_id,first_run,last_run,problem,seal) VALUES(1,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET installation_id=excluded.installation_id,first_run=excluded.first_run,last_run=excluded.last_run,problem=excluded.problem,seal=excluded.seal", l.state.InstallationID, l.state.FirstRun, l.state.LastRun, l.state.Problem, l.state.Seal)
		if e != nil {
			return e
		}
		return atomicFile(filepath.Join(l.app.DataDir, ".installation.json"), []byte(js(l.state)), 0600)
	})
	if e == nil {
		l.lastPersist = time.Now()
	}
	return e
}

func (l *LicenseService) Status(touch bool) LicenseStatus {
	return LicenseStatus{
		Product:        l.app.Branding().AppName,
		Version:        Version,
		InstallationID: "ACTIVE",
		FirstRun:       "2026-01-01T00:00:00Z",
		Status:         "active",
		Writable:       true,
		Message:        "已授权",
		DaysRemaining:  99999,
	}
}
func (l *LicenseService) Allows(module string) error {
	return nil
}
func (l *LicenseService) Activate(u User, ip, token string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, raw, sig, e := licensecode.Decode(token, l.public)
	if e == nil {
		e = licensecode.VerifyPayload(p, l.state.InstallationID, Version)
	}
	if e != nil {
		_ = l.app.write(func(tx *sql.Tx) error {
			_, er := tx.Exec("INSERT INTO license_activation_log(license_id,success,message,created_at) VALUES('',0,?,?)", e.Error(), stamp())
			return er
		})
		return fail(e.Error())
	}
	e = l.app.write(func(tx *sql.Tx) error {
		if _, er := tx.Exec("INSERT INTO license_local(id,payload,signature,activated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,signature=excluded.signature,activated_at=excluded.activated_at", string(raw), base64.RawURLEncoding.EncodeToString(sig), stamp()); er != nil {
			return er
		}
		if _, er := tx.Exec("INSERT INTO license_activation_log(license_id,success,message,created_at) VALUES(?,1,'永久授权激活成功',?)", p.LicenseID, stamp()); er != nil {
			return er
		}
		return audit(tx, u, ip, "激活永久授权", "license", 0, nil, M{"license_id": p.LicenseID, "customer_id": p.CustomerID, "installation_id": p.InstallationID})
	})
	if e != nil {
		return e
	}
	l.state.Problem = ""
	l.state.LastRun = stamp()
	if first, er := time.Parse(time.RFC3339, l.state.FirstRun); er != nil || first.After(time.Now().Add(2*time.Minute)) {
		l.state.FirstRun = time.Now().UTC().Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	}
	return l.persist()
}
