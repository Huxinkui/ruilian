package app

import (
	"encoding/json"
	"ruilian/internal/licensecode"
	"strings"
	"testing"
	"time"
)

func TestCaptchaGenerationAndVerification(t *testing.T) {
	store := NewCaptchaStore()

	// 1. 生成验证码
	id, dataURL := store.Generate()
	if id == "" {
		t.Fatal("expected non-empty captcha id")
	}
	if !strings.HasPrefix(dataURL, "data:image/svg+xml;base64,") {
		t.Fatalf("expected base64 svg data URL, got: %s", dataURL)
	}

	// 验证生成了存储项
	store.mu.RLock()
	entry, exists := store.items[id]
	store.mu.RUnlock()
	if !exists {
		t.Fatal("captcha entry not found in store")
	}
	if len(entry.Code) != 4 {
		t.Fatalf("expected 4-character code, got %q", entry.Code)
	}

	// 2. 错误验证码应验证失败
	if store.Verify(id, "WRONG") {
		t.Fatal("expected wrong captcha to fail")
	}

	// 3. 验证码应为一次性，已尝试过的验证码再次使用应失败
	if store.Verify(id, entry.Code) {
		t.Fatal("expected single-use captcha to fail on replay")
	}

	// 4. 正确验证码验证通过（忽略大小写）
	id2, _ := store.Generate()
	store.mu.RLock()
	code2 := store.items[id2].Code
	store.mu.RUnlock()

	// 传入不同大小写形式验证
	lowerCode := strings.ToLower(code2)
	if !store.Verify(id2, lowerCode) {
		t.Fatalf("case-insensitive verification failed for %s with input %s", code2, lowerCode)
	}

	// 5. 过期验证码失效
	id3, _ := store.Generate()
	store.mu.Lock()
	store.items[id3] = CaptchaEntry{
		Code:      "ABCD",
		ExpiresAt: time.Now().Add(-1 * time.Second),
	}
	store.mu.Unlock()
	if store.Verify(id3, "ABCD") {
		t.Fatal("expected expired captcha to fail verification")
	}
}

func TestCaptchaHTTPAPI(t *testing.T) {
	dataDir := t.TempDir()
	a, err := Open(dataDir, licensecode.PublicKeyPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if err := a.EnsureDefaultAccount(); err != nil {
		t.Fatal(err)
	}

	router := a.Router(nil)

	// 1. 获取验证码
	resp, _ := apiRequest(t, router, "GET", "/api/auth/captcha", nil, nil, "")
	if resp.Code != 200 {
		t.Fatalf("captcha API returned status %d: %s", resp.Code, resp.Body.String())
	}
	var res struct {
		Ok   bool `json:"ok"`
		Data struct {
			CaptchaID    string `json:"captcha_id"`
			CaptchaImage string `json:"captcha_image"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Data.CaptchaID == "" || !strings.HasPrefix(res.Data.CaptchaImage, "data:image/svg+xml;base64,") {
		t.Fatalf("invalid captcha API response: %+v", res)
	}

	// 2. 携带错误验证码登录失败
	resp, _ = apiRequest(t, router, "POST", "/api/auth/login", M{
		"username":     "admin",
		"password":     DefaultAdminPassword,
		"captcha_id":   res.Data.CaptchaID,
		"captcha_code": "INVL",
	}, nil, "")
	if resp.Code == 200 {
		t.Fatal("expected login with invalid captcha to fail")
	}

	// 3. 携带正确验证码登录成功
	resp2, _ := apiRequest(t, router, "GET", "/api/auth/captcha", nil, nil, "")
	_ = json.Unmarshal(resp2.Body.Bytes(), &res)
	a.CaptchaStore.mu.RLock()
	realCode := a.CaptchaStore.items[res.Data.CaptchaID].Code
	a.CaptchaStore.mu.RUnlock()

	respLogin, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"username":     "admin",
		"password":     DefaultAdminPassword,
		"captcha_id":   res.Data.CaptchaID,
		"captcha_code": realCode,
	}, nil, "")
	if respLogin.Code != 200 {
		t.Fatalf("expected login with valid captcha to succeed, got %d: %s", respLogin.Code, respLogin.Body.String())
	}
}

