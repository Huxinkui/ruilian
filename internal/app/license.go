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
	"fmt"
	"math"
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
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	s := LicenseStatus{Product: l.app.Branding().AppName, Version: Version, InstallationID: l.state.InstallationID, FirstRun: l.state.FirstRun, Status: "trial", Writable: true}
	oldProblem := l.state.Problem
	last, _ := time.Parse(time.RFC3339, l.state.LastRun)
	if now.Add(2*time.Minute).Before(last) || now.Sub(l.startWall)+2*time.Minute < time.Since(l.startMono) {
		l.state.Problem = "检测到系统时间回拨，请校准时间并重新授权"
	}
	var db installationState
	er := l.app.DB.QueryRow("SELECT installation_id,first_run,last_run,problem,seal FROM system_installation WHERE id=1").Scan(&db.InstallationID, &db.FirstRun, &db.LastRun, &db.Problem, &db.Seal)
	if er != nil || !l.valid(db) || db.InstallationID != l.state.InstallationID || db.FirstRun != l.state.FirstRun {
		l.state.Problem = "安装记录完整性校验失败"
	}
	var local installationState
	raw, er := os.ReadFile(filepath.Join(l.app.DataDir, ".installation.json"))
	if er != nil || json.Unmarshal(raw, &local) != nil || !l.valid(local) || local.InstallationID != l.state.InstallationID || local.FirstRun != l.state.FirstRun {
		l.state.Problem = "独立安装记录完整性校验失败"
	}
	if touch && (time.Since(l.lastPersist) > time.Minute || oldProblem != l.state.Problem) {
		if now.After(last) {
			l.state.LastRun = now.UTC().Format(time.RFC3339)
		}
		if er = l.persist(); er != nil {
			l.state.Problem = "安装状态写入失败，请检查磁盘权限"
			l.app.recordError("license persist", er)
		}
	}
	first, e := time.Parse(time.RFC3339, l.state.FirstRun)
	if e != nil {
		l.state.Problem = "试用起始时间无效"
	}
	end := first.Add(10 * 24 * time.Hour)
	s.TrialEnd = end.Format(time.RFC3339)
	s.DaysRemaining = int(math.Ceil(end.Sub(now).Hours() / 24))
	if s.DaysRemaining < 0 {
		s.DaysRemaining = 0
	}
	s.Message = fmt.Sprintf("10 天完整试用，剩余 %d 天", s.DaysRemaining)
	if !now.Before(end) {
		s.Status = "expired"
		s.Writable = false
		s.Message = "试用已到期，可查看、导出、备份和激活授权"
	}
	var payload, signature, activated string
	err := l.app.DB.QueryRow("SELECT payload,signature,activated_at FROM license_local WHERE id=1").Scan(&payload, &signature, &activated)
	if err == nil {
		token := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + signature
		p, _, _, e := licensecode.Decode(token, l.public)
		if e == nil {
			e = licensecode.VerifyPayload(p, l.state.InstallationID, Version)
		}
		if e != nil {
			s.Status = "abnormal"
			s.Writable = false
			s.Message = e.Error()
		} else {
			s.Status = "active"
			s.Writable = true
			s.Message = "已激活永久授权"
			s.Payload = &p
			s.ActivatedAt = activated
		}
	} else if err != sql.ErrNoRows {
		s.Status = "abnormal"
		s.Writable = false
		s.Message = "读取授权记录失败"
	}
	if l.state.Problem != "" {
		s.Status = "abnormal"
		s.Writable = false
		s.Message = l.state.Problem
	}
	if (s.Status == "expired" || s.Status == "abnormal") && (l.lastStatus != s.Status || l.lastMessage != s.Message) {
		_ = l.app.write(func(tx *sql.Tx) error {
			_, e := tx.Exec("INSERT INTO license_event_log(event,message,created_at) VALUES(?,?,?)", s.Status, s.Message, stamp())
			return e
		})
	}
	l.lastStatus = s.Status
	l.lastMessage = s.Message
	return s
}
func (l *LicenseService) Allows(module string) error {
	s := l.Status(true)
	if !s.Writable {
		return &BusinessError{"LICENSE_RESTRICTED", s.Message}
	}
	if s.Payload != nil {
		for _, f := range s.Payload.Features {
			if f == "*" || f == module {
				return nil
			}
		}
		return &BusinessError{"LICENSE_RESTRICTED", "授权未包含此业务模块"}
	}
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
