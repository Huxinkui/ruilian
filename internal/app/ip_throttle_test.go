package app

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"ruilian/internal/licensecode"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestIPLoginLimiter_ConsecutiveFailuresAndLockout(t *testing.T) {
	limiter := NewIPLoginLimiter()
	testIP := "192.168.1.100"

	// Initially not locked
	locked, _ := limiter.CheckLocked(testIP)
	if locked {
		t.Fatal("expected IP to not be locked initially")
	}

	// 1st failure
	lockedNow, remaining, count, remainingAttempts := limiter.RecordFailure(testIP)
	if lockedNow || count != 1 || remainingAttempts != 4 {
		t.Fatalf("unexpected attempt 1 state: lockedNow=%v, count=%d, remainingAttempts=%d", lockedNow, count, remainingAttempts)
	}

	// 2nd failure
	lockedNow, remaining, count, remainingAttempts = limiter.RecordFailure(testIP)
	if lockedNow || count != 2 || remainingAttempts != 3 {
		t.Fatalf("unexpected attempt 2 state: lockedNow=%v, count=%d, remainingAttempts=%d", lockedNow, count, remainingAttempts)
	}

	// 3rd failure
	lockedNow, remaining, count, remainingAttempts = limiter.RecordFailure(testIP)
	if lockedNow || count != 3 || remainingAttempts != 2 {
		t.Fatalf("unexpected attempt 3 state: lockedNow=%v, count=%d, remainingAttempts=%d", lockedNow, count, remainingAttempts)
	}

	// 4th failure
	lockedNow, remaining, count, remainingAttempts = limiter.RecordFailure(testIP)
	if lockedNow || count != 4 || remainingAttempts != 1 {
		t.Fatalf("unexpected attempt 4 state: lockedNow=%v, count=%d, remainingAttempts=%d", lockedNow, count, remainingAttempts)
	}

	// IP still not locked before 5th failure
	locked, _ = limiter.CheckLocked(testIP)
	if locked {
		t.Fatal("expected IP to not be locked after 4 failures")
	}

	// 5th failure - MUST LOCK
	lockedNow, remaining, count, remainingAttempts = limiter.RecordFailure(testIP)
	if !lockedNow || count != 5 || remainingAttempts != 0 || remaining <= 0 {
		t.Fatalf("expected attempt 5 to trigger lock: lockedNow=%v, count=%d, remainingAttempts=%d, remaining=%d", lockedNow, count, remainingAttempts, remaining)
	}

	// Verify CheckLocked returns true
	locked, remaining = limiter.CheckLocked(testIP)
	if !locked || remaining <= 0 || remaining > 30 {
		t.Fatalf("expected IP to be locked: locked=%v, remaining=%d", locked, remaining)
	}

	// Another IP should not be affected
	otherIP := "192.168.1.101"
	lockedOther, _ := limiter.CheckLocked(otherIP)
	if lockedOther {
		t.Fatal("different IP should not be locked")
	}

	// Reset / success clears lockout
	limiter.RecordSuccess(testIP)
	locked, _ = limiter.CheckLocked(testIP)
	if locked {
		t.Fatal("expected IP lockout to be cleared after RecordSuccess")
	}
}

func TestIPLoginLimiter_ResetOnSuccess(t *testing.T) {
	limiter := NewIPLoginLimiter()
	testIP := "10.0.0.1"

	// 3 failures
	for i := 0; i < 3; i++ {
		limiter.RecordFailure(testIP)
	}

	// Successful login resets counter
	limiter.RecordSuccess(testIP)

	// Next failure should be attempt 1 again
	lockedNow, _, count, remainingAttempts := limiter.RecordFailure(testIP)
	if lockedNow || count != 1 || remainingAttempts != 4 {
		t.Fatalf("expected counter reset after success: count=%d, remainingAttempts=%d", count, remainingAttempts)
	}
}

func TestIPLoginLimiter_AutoExpiry(t *testing.T) {
	limiter := NewIPLoginLimiter()
	testIP := "10.0.0.2"

	// Manually set an expired lock
	limiter.mu.Lock()
	limiter.records[testIP] = &ipLoginRecord{
		FailCount:   5,
		LockedUntil: time.Now().Add(-1 * time.Minute), // expired
		LastFailed:  time.Now().Add(-31 * time.Minute),
	}
	limiter.mu.Unlock()

	locked, _ := limiter.CheckLocked(testIP)
	if locked {
		t.Fatal("expected expired lock to report not locked")
	}
}

func TestResolveClientIP(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1. CF-Connecting-IP
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set("CF-Connecting-IP", "1.2.3.4")
	c.Request.Header.Set("X-Forwarded-For", "5.6.7.8, 9.10.11.12")
	c.Request.Header.Set("X-Real-IP", "13.14.15.16")

	if ip := ResolveClientIP(c); ip != "1.2.3.4" {
		t.Fatalf("expected CF-Connecting-IP 1.2.3.4, got %s", ip)
	}

	// 2. X-Forwarded-For leftmost
	c.Request.Header.Del("CF-Connecting-IP")
	if ip := ResolveClientIP(c); ip != "5.6.7.8" {
		t.Fatalf("expected X-Forwarded-For leftmost 5.6.7.8, got %s", ip)
	}

	// 3. X-Real-IP
	c.Request.Header.Del("X-Forwarded-For")
	if ip := ResolveClientIP(c); ip != "13.14.15.16" {
		t.Fatalf("expected X-Real-IP 13.14.15.16, got %s", ip)
	}
}

func TestLoginHTTP_IPThrottling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	app, err := Open(dir, licensecode.PublicKeyPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.DB.Close()

	if err := app.EnsureDefaultAccount(); err != nil {
		t.Fatal(err)
	}

	router := app.Router(nil)
	testIP := "203.0.113.42"

	doLogin := func(body map[string]any, ip string) (*httptest.ResponseRecorder, map[string]any) {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Real-IP", ip)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var res map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		return w, res
	}

	// 1. Enter wrong captcha 4 times
	for i := 1; i <= 4; i++ {
		cID, _ := app.CaptchaStore.Generate()
		w, _ := doLogin(map[string]any{
			"username":     "admin",
			"password":     DefaultAdminPassword,
			"captcha_id":   cID,
			"captcha_code": "WRONG",
		}, testIP)

		if w.Code != 400 {
			t.Fatalf("attempt %d: expected 400 on wrong captcha, got %d: %s", i, w.Code, w.Body.String())
		}
	}

	// Still not locked before 5th failure
	locked, _ := app.IPLimiter.CheckLocked(testIP)
	if locked {
		t.Fatal("expected IP not locked after 4 errors")
	}

	// 2. 5th error with wrong password (valid captcha)
	cID5, _ := app.CaptchaStore.Generate()
	app.CaptchaStore.mu.RLock()
	realCode5 := app.CaptchaStore.items[cID5].Code
	app.CaptchaStore.mu.RUnlock()

	w5, res5 := doLogin(map[string]any{
		"username":     "admin",
		"password":     "wrong-password-here",
		"captcha_id":   cID5,
		"captcha_code": realCode5,
	}, testIP)

	if w5.Code != 400 {
		t.Fatalf("expected 400 on 5th error, got %d: %s", w5.Code, w5.Body.String())
	}
	errObj5, _ := res5["error"].(map[string]any)
	msg5, _ := errObj5["message"].(string)
	if !strings.Contains(msg5, "已被禁止登录 30 分钟") {
		t.Fatalf("expected 30m ban message on 5th failure, got: %s", msg5)
	}

	// 3. Now the IP MUST be locked!
	locked, remaining := app.IPLimiter.CheckLocked(testIP)
	if !locked || remaining <= 0 {
		t.Fatalf("expected IP to be locked for 30 minutes, got locked=%v, remaining=%d", locked, remaining)
	}

	// 4. 6th attempt even with correct credentials and captcha MUST be rejected immediately due to IP lock
	cID6, _ := app.CaptchaStore.Generate()
	app.CaptchaStore.mu.RLock()
	realCode6 := app.CaptchaStore.items[cID6].Code
	app.CaptchaStore.mu.RUnlock()

	w6, res6 := doLogin(map[string]any{
		"username":     "admin",
		"password":     DefaultAdminPassword,
		"captcha_id":   cID6,
		"captcha_code": realCode6,
	}, testIP)

	if w6.Code != 400 {
		t.Fatalf("expected 400 on locked IP, got %d: %s", w6.Code, w6.Body.String())
	}
	errObj6, _ := res6["error"].(map[string]any)
	msg6, _ := errObj6["message"].(string)
	if !strings.Contains(msg6, "已被禁止登录") {
		t.Fatalf("expected ban message, got %s", msg6)
	}

	// 5. Another IP should be able to log in normally
	otherIP := "203.0.113.99"
	cID7, _ := app.CaptchaStore.Generate()
	app.CaptchaStore.mu.RLock()
	realCode7 := app.CaptchaStore.items[cID7].Code
	app.CaptchaStore.mu.RUnlock()

	w7, _ := doLogin(map[string]any{
		"username":     "admin",
		"password":     DefaultAdminPassword,
		"captcha_id":   cID7,
		"captcha_code": realCode7,
	}, otherIP)

	if w7.Code != 200 {
		t.Fatalf("expected 200 for another IP, got %d: %s", w7.Code, w7.Body.String())
	}
}
