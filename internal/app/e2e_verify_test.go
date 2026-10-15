package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"ruilian/internal/licensecode"
)

func TestHuxinkuiAdminAndTenantSecurityVerification(t *testing.T) {
	dataDir := t.TempDir()
	a, err := Open(dataDir, licensecode.PublicKeyPEM, nil)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer a.Close()

	if err := a.EnsureDefaultAccount(); err != nil {
		t.Fatalf("EnsureDefaultAccount failed: %v", err)
	}

	router := a.Router(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}})

	// 1. 验证默认 admin 账号登录被彻底拒绝（防止撞库和扫描攻击）
	_, _, errAdmin := a.Login("admin", DefaultAdminPassword, "127.0.0.1")
	if errAdmin == nil {
		t.Fatal("expected admin login to fail, but succeeded")
	}

	// 2. 验证 huxinkui 作为超级管理员登录成功
	tokenHxk, uHxk, errHxk := a.Login("huxinkui", DefaultAdminPassword, "127.0.0.1")
	if errHxk != nil {
		t.Fatalf("huxinkui login failed: %v", errHxk)
	}
	if uHxk.Username != "huxinkui" {
		t.Fatalf("expected username huxinkui, got %s", uHxk.Username)
	}

	// 3. 验证 huxinkui 具有超级管理员身份 IsMasterAdmin
	authUser, errAuth := a.Authenticate(tokenHxk)
	if errAuth != nil || !authUser.IsMasterAdmin {
		t.Fatalf("huxinkui should be master admin, got err=%v, isMaster=%v", errAuth, authUser.IsMasterAdmin)
	}

	// 4. 验证 huxinkui 可以正常访问租户管理接口 GET /api/system/tenants
	reqTenants := httptest.NewRequest("GET", "/api/system/tenants", nil)
	reqTenants.AddCookie(&http.Cookie{Name: "ruilian_session", Value: tokenHxk})
	wTenants := httptest.NewRecorder()
	router.ServeHTTP(wTenants, reqTenants)
	if wTenants.Code != 200 {
		t.Fatalf("huxinkui GET /api/system/tenants failed with %d: %s", wTenants.Code, wTenants.Body.String())
	}

	// 5. 创建独立租户 corp_demo
	tenantDemo, err := a.TenantManager.CreateTenant("corp_demo", "测试分公司", 500, "Demo-Password-2026", "演示租户")
	if err != nil {
		t.Fatalf("create tenant failed: %v", err)
	}
	if tenantDemo.AdminUser != "corp_demo" {
		t.Fatalf("expected tenant admin_user to be corp_demo, got %s", tenantDemo.AdminUser)
	}

	// 6. 验证租户专属用户 corp_demo 正常登录
	tenantApp, err := a.TenantManager.GetTenantApp("corp_demo")
	if err != nil {
		t.Fatalf("GetTenantApp failed: %v", err)
	}
	tokenDemo, uDemo, errDemo := tenantApp.Login("corp_demo", "Demo-Password-2026", "127.0.0.1")
	if errDemo != nil {
		t.Fatalf("tenant login failed: %v", errDemo)
	}
	if uDemo.Username != "corp_demo" {
		t.Fatalf("expected tenant username corp_demo, got %s", uDemo.Username)
	}

	// 7. 核心验证：租户用户 corp_demo 绝不能访问多租户管理接口 /api/system/tenants
	reqTenantAccess := httptest.NewRequest("GET", "/api/system/tenants", nil)
	reqTenantAccess.AddCookie(&http.Cookie{Name: "ruilian_session", Value: tokenDemo})
	reqTenantAccess.AddCookie(&http.Cookie{Name: "ruilian_tenant", Value: "corp_demo"})
	wTenantAccess := httptest.NewRecorder()
	router.ServeHTTP(wTenantAccess, reqTenantAccess)
	if wTenantAccess.Code == 200 {
		t.Fatalf("security violation: tenant corp_demo was able to access /api/system/tenants! status=%d", wTenantAccess.Code)
	}

	// 8. 验证租户空间内禁止使用 admin 或 huxinkui 登录
	_, _, errAdminInTenant := tenantApp.Login("admin", "Demo-Password-2026", "127.0.0.1")
	if errAdminInTenant == nil {
		t.Fatal("expected admin login in tenant to fail, but succeeded")
	}
	_, _, errHxkInTenant := tenantApp.Login("huxinkui", "Demo-Password-2026", "127.0.0.1")
	if errHxkInTenant == nil {
		t.Fatal("expected huxinkui login in tenant to fail, but succeeded")
	}
}
