package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"ruilian/internal/licensecode"
)

func TestMultiTenantCreationAndStorageManagement(t *testing.T) {
	dataDir := t.TempDir()
	a, err := Open(dataDir, licensecode.PublicKeyPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if err := a.EnsureDefaultAccount(); err != nil {
		t.Fatal(err)
	}

	router := a.Router(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}})
	if a.TenantManager == nil {
		t.Fatal("TenantManager should be initialized")
	}

	// 1. 验证默认租户
	tenants, err := a.TenantManager.ListTenants()
	if err != nil {
		t.Fatal(err)
	}
	if len(tenants) != 1 || tenants[0].Code != "default" {
		t.Fatalf("expected 1 default tenant, got %+v", tenants)
	}
	if tenants[0].MaxStorageMB != 500 {
		t.Fatalf("default tenant quota expected 500MB, got %d", tenants[0].MaxStorageMB)
	}

	// 2. 超级管理员创建新租户
	tenant1, err := a.TenantManager.CreateTenant("corp_sh", "上海分公司", 100, "Sh-2026-Admin", "华东分部")
	if err != nil {
		t.Fatalf("create tenant failed: %v", err)
	}
	if tenant1.Code != "corp_sh" || tenant1.MaxStorageMB != 100 {
		t.Fatalf("unexpected tenant data: %+v", tenant1)
	}

	// 验证租户物理目录创建
	tenantDir := filepath.Join(dataDir, "tenants", "corp_sh")
	if fi, err := os.Stat(filepath.Join(tenantDir, "ruilian.db")); err != nil || fi.Size() == 0 {
		t.Fatalf("tenant database not created properly: %v", err)
	}

	// 3. 管理员调整租户空间配额 (设置使用空间)
	if err := a.TenantManager.UpdateTenantStorage("corp_sh", 250); err != nil {
		t.Fatalf("update storage quota failed: %v", err)
	}
	updated, err := a.TenantManager.GetTenant("corp_sh")
	if err != nil {
		t.Fatal(err)
	}
	if updated.MaxStorageMB != 250 {
		t.Fatalf("expected quota 250MB, got %d", updated.MaxStorageMB)
	}

	// 4. 测试空间超限拦截
	// 将配额设置极小 (例如 0 表示无限制，设为 1MB 但通过 mock 或写入大文件触发超限)
	// 这里通过设置配额为 1MB 并检查
	_ = a.TenantManager.UpdateTenantStorage("corp_sh", 1) // 1MB 额度
	// 往租户目录写入测试大文件模拟数据库体积膨胀超过 1MB
	bigFile := filepath.Join(tenantDir, "dummy_large.db")
	_ = os.WriteFile(bigFile, make([]byte, 2*1024*1024), 0600) // 2MB

	quotaErr := a.TenantManager.CheckStorageQuota("corp_sh")
	if quotaErr == nil {
		t.Fatal("expected storage quota exceeded error, but got nil")
	}

	// 5. 管理员扩容租户空间
	if err := a.TenantManager.UpdateTenantStorage("corp_sh", 100); err != nil {
		t.Fatal(err)
	}
	if err := a.TenantManager.CheckStorageQuota("corp_sh"); err != nil {
		t.Fatalf("quota should not exceed after expansion: %v", err)
	}
	_ = os.Remove(bigFile)

	// 6. 测试 HTTP API: 租户列表与配额修改
	_, u, _ := a.Login("huxinkui", DefaultAdminPassword, "127.0.0.1")
	cookies := []*http.Cookie{
		{Name: "ruilian_session", Value: u.Username}, // mock
	}
	// 实际登录获取有效 token (使用 huxinkui)
	resp, cookies := apiRequest(t, router, "POST", "/api/auth/login", M{"username": "huxinkui", "password": DefaultAdminPassword}, nil, "")
	if resp.Code != 200 {
		t.Fatalf("huxinkui login failed: %s", resp.Body.String())
	}

	// GET /api/system/tenants
	resp, _ = apiRequest(t, router, "GET", "/api/system/tenants", nil, cookies, "")
	if resp.Code != 200 {
		t.Fatalf("get tenants API failed: %s", resp.Body.String())
	}

	// PUT /api/system/tenants/corp_sh/storage 修改配额为 300MB
	reqBody := bytes.NewBufferString(`{"max_storage_mb": 300}`)
	req := httptest.NewRequest("PUT", "/api/system/tenants/corp_sh/storage", reqBody)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("update storage API failed: %s", w.Body.String())
	}

	checkTenant, _ := a.TenantManager.GetTenant("corp_sh")
	if checkTenant.MaxStorageMB != 300 {
		t.Fatalf("expected updated quota 300MB, got %d", checkTenant.MaxStorageMB)
	}

	// 7. 验证 admin 携带主空间 Token 可以访问并修改子租户 corp_sh 的数据
	respMe, _ := tenantAPIRequest(t, router, "GET", "/api/me", nil, cookies, "corp_sh")
	if respMe.Code != 200 {
		t.Fatalf("admin access corp_sh /api/me failed: %s", respMe.Body.String())
	}
	var meResult struct {
		Data struct {
			Tenant struct {
				Code string `json:"code"`
				Name string `json:"name"`
			} `json:"tenant"`
			User struct {
				Username      string `json:"username"`
				IsMasterAdmin bool   `json:"is_master_admin"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(respMe.Body.Bytes(), &meResult)
	if meResult.Data.Tenant.Code != "corp_sh" {
		t.Fatalf("expected corp_sh tenant in /api/me, got %s", meResult.Data.Tenant.Code)
	}
	if !meResult.Data.User.IsMasterAdmin {
		t.Fatal("expected is_master_admin to be true in subtenant")
	}

	// 在子租户 corp_sh 中创建商品 (修改子租户数据)
	respProduct, _ := tenantAPIRequest(t, router, "POST", "/api/masters/product", M{
		"code": "PROD_SH_001",
		"name": "上海特色商品",
		"data": M{
			"barcode": "6901234567890",
			"spec":    "箱",
			"unit":    "件",
		},
	}, cookies, "corp_sh")
	if respProduct.Code != 200 {
		t.Fatalf("admin create product in corp_sh failed: %s", respProduct.Body.String())
	}

	// 验证在 corp_sh 中能查到该商品
	respList, _ := tenantAPIRequest(t, router, "GET", "/api/masters/product", nil, cookies, "corp_sh")
	if respList.Code != 200 || !strings.Contains(respList.Body.String(), "上海特色商品") {
		t.Fatalf("expected product to exist in corp_sh: %s", respList.Body.String())
	}

	// 验证在主空间 default 中不存在该商品 (数据隔离验证)
	respMainList, _ := tenantAPIRequest(t, router, "GET", "/api/masters/product", nil, cookies, "default")
	if respMainList.Code != 200 || strings.Contains(respMainList.Body.String(), "上海特色商品") {
		t.Fatalf("product in corp_sh should not appear in default tenant: %s", respMainList.Body.String())
	}

	// 8. 验证租户使用自己的专属用户ID (corp_sh) 进行登录（无需输入 tenant，直接使用租户用户ID）
	respTenantLogin, tenantCookies := apiRequest(t, router, "POST", "/api/auth/login", M{
		"username": "corp_sh",
		"password": "Sh-2026-Admin",
	}, nil, "")
	if respTenantLogin.Code != 200 {
		t.Fatalf("tenant user corp_sh login failed: %s", respTenantLogin.Body.String())
	}
	var loginData struct {
		Data struct {
			TenantCode string `json:"tenant_code"`
			User       struct {
				Username string `json:"username"`
				Name     string `json:"name"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(respTenantLogin.Body.Bytes(), &loginData)
	if loginData.Data.TenantCode != "corp_sh" {
		t.Fatalf("expected tenant_code corp_sh, got %s", loginData.Data.TenantCode)
	}
	if loginData.Data.User.Username != "corp_sh" {
		t.Fatalf("expected username corp_sh, got %s", loginData.Data.User.Username)
	}

	// 9. 验证携带租户专属 Cookie 访问 /api/me 成功识别租户与用户
	respTenantMe, _ := apiRequest(t, router, "GET", "/api/me", nil, tenantCookies, "")
	if respTenantMe.Code != 200 {
		t.Fatalf("tenant user corp_sh /api/me failed: %s", respTenantMe.Body.String())
	}
	_ = json.Unmarshal(respTenantMe.Body.Bytes(), &loginData)
	if loginData.Data.TenantCode != "corp_sh" && loginData.Data.User.Username != "corp_sh" {
		t.Fatalf("expected corp_sh in /api/me, got %+v", loginData)
	}

	// 10. 验证显式携带 tenant=corp_sh 与 username=corp_sh 也可正常登录
	respExplicitLogin, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"tenant":   "corp_sh",
		"username": "corp_sh",
		"password": "Sh-2026-Admin",
	}, nil, "")
	if respExplicitLogin.Code != 200 {
		t.Fatalf("explicit tenant login failed: %s", respExplicitLogin.Body.String())
	}

	// 11. 验证租户禁止以 admin 身份登录子租户空间，admin 仅供系统后台管理员登录
	respAdminInTenantLogin, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"tenant":   "corp_sh",
		"username": "admin",
		"password": "Sh-2026-Admin",
	}, nil, "")
	if respAdminInTenantLogin.Code == 200 {
		t.Fatal("expected admin login in subtenant corp_sh to fail, but succeeded")
	}
}

func tenantAPIRequest(t *testing.T, router http.Handler, method, path string, body any, cookies []*http.Cookie, tenantCode string) (*httptest.ResponseRecorder, []*http.Cookie) {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewBufferString(js(body))
	}
	request := httptest.NewRequest(method, path, r)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uid())
	request.Header.Set("X-Tenant-Code", tenantCode)
	request.Header.Set("X-Impersonate-Mode", "readwrite")
	for _, cookie := range cookies {
		if cookie.Name != "ruilian_tenant" {
			request.AddCookie(cookie)
		}
	}
	request.AddCookie(&http.Cookie{Name: "ruilian_tenant", Value: tenantCode})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	returned := response.Result().Cookies()
	if len(returned) == 0 {
		returned = cookies
	}
	return response, returned
}

func TestTenantXuyonghongLoginAndMultiTenantIsolation(t *testing.T) {
	dataDir := t.TempDir()
	a, err := Open(dataDir, licensecode.PublicKeyPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if err := a.EnsureDefaultAccount(); err != nil {
		t.Fatal(err)
	}

	router := a.Router(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}})

	// 1. 创建租户 xuyonghong
	tenantXyh, err := a.TenantManager.CreateTenant("xuyonghong", "徐永红", 500, "Xyh-Secret-2026", "个人租户")
	if err != nil {
		t.Fatalf("failed to create xuyonghong tenant: %v", err)
	}
	if tenantXyh.AdminUser != "xuyonghong" {
		t.Fatalf("expected admin_user to be xuyonghong, got %s", tenantXyh.AdminUser)
	}

	// 2. 检查租户列表中的 admin_user
	tenants, err := a.TenantManager.ListTenants()
	if err != nil {
		t.Fatal(err)
	}
	var foundXyh bool
	for _, item := range tenants {
		if item.Code == "xuyonghong" {
			foundXyh = true
			if item.AdminUser != "xuyonghong" {
				t.Fatalf("expected item.AdminUser xuyonghong, got %s", item.AdminUser)
			}
			if item.Name != "徐永红" {
				t.Fatalf("expected item.Name 徐永红, got %s", item.Name)
			}
		}
	}
	if !foundXyh {
		t.Fatal("xuyonghong tenant not found in ListTenants")
	}

	// 3. 用户直接在用户名输入 xuyonghong 进行登录（未输入租户空间）
	respLogin1, cookies1 := apiRequest(t, router, "POST", "/api/auth/login", M{
		"username": "xuyonghong",
		"password": "Xyh-Secret-2026",
	}, nil, "")
	if respLogin1.Code != 200 {
		t.Fatalf("login with username xuyonghong failed: %s", respLogin1.Body.String())
	}

	var res1 struct {
		Data struct {
			TenantCode string `json:"tenant_code"`
			User       struct {
				Username string `json:"username"`
				Name     string `json:"name"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(respLogin1.Body.Bytes(), &res1)
	if res1.Data.TenantCode != "xuyonghong" {
		t.Fatalf("expected tenant_code xuyonghong, got %s", res1.Data.TenantCode)
	}
	if res1.Data.User.Username != "xuyonghong" {
		t.Fatalf("expected user.username xuyonghong, got %s", res1.Data.User.Username)
	}
	if res1.Data.User.Name != "徐永红" {
		t.Fatalf("expected user.name 徐永红, got %s", res1.Data.User.Name)
	}

	// 4. 使用得到的 session 访问 /api/me，验证租户空间信息
	respMe, _ := apiRequest(t, router, "GET", "/api/me", nil, cookies1, "")
	if respMe.Code != 200 {
		t.Fatalf("xuyonghong /api/me failed: %s", respMe.Body.String())
	}
	var meData struct {
		Data struct {
			Tenant struct {
				Code string `json:"code"`
				Name string `json:"name"`
			} `json:"tenant"`
			User struct {
				Username      string `json:"username"`
				IsMasterAdmin bool   `json:"is_master_admin"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(respMe.Body.Bytes(), &meData)
	if meData.Data.Tenant.Code != "xuyonghong" || meData.Data.User.Username != "xuyonghong" {
		t.Fatalf("unexpected /api/me data: %+v", meData)
	}
	if meData.Data.User.IsMasterAdmin {
		t.Fatal("subtenant user xuyonghong should not be master admin")
	}

	// 5. 显式输入 tenant=xuyonghong 且 username=xuyonghong 登录
	respLogin2, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"tenant":   "xuyonghong",
		"username": "xuyonghong",
		"password": "Xyh-Secret-2026",
	}, nil, "")
	if respLogin2.Code != 200 {
		t.Fatalf("explicit xuyonghong login failed: %s", respLogin2.Body.String())
	}

	// 6. 验证租户禁止使用 admin 或 huxinkui 登录，必须使用专属租户用户 ID
	respLogin3, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"tenant":   "xuyonghong",
		"username": "admin",
		"password": "Xyh-Secret-2026",
	}, nil, "")
	if respLogin3.Code == 200 {
		t.Fatal("expected admin login in tenant to fail, but succeeded")
	}

	respLoginHxkInTenant, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"tenant":   "xuyonghong",
		"username": "huxinkui",
		"password": "Xyh-Secret-2026",
	}, nil, "")
	if respLoginHxkInTenant.Code == 200 {
		t.Fatal("expected huxinkui login in subtenant to fail, but succeeded")
	}

	// 6.1 验证普通租户 xuyonghong 访问租户管理接口被权限拒绝 (401 未登录主空间或 403 权限拒绝)
	respDenied, _ := apiRequest(t, router, "GET", "/api/system/tenants", nil, cookies1, "")
	if respDenied.Code != 401 && respDenied.Code != 403 {
		t.Fatalf("expected tenant access to /api/system/tenants to be denied (401/403), got %d: %s", respDenied.Code, respDenied.Body.String())
	}

	// 7. 验证旧 admin 用户在后台登录失败（防扫描被移除）
	respOldAdmin, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"username": "admin",
		"password": DefaultAdminPassword,
	}, nil, "")
	if respOldAdmin.Code == 200 {
		t.Fatal("expected old admin username to be disabled, but succeeded")
	}

	// 7.1 验证 huxinkui 作为系统后台管理员进行登录 (不指定租户或使用 default)
	respAdminMain, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"username": "huxinkui",
		"password": DefaultAdminPassword,
	}, nil, "")
	if respAdminMain.Code != 200 {
		t.Fatalf("backend huxinkui login failed: %s", respAdminMain.Body.String())
	}
	var resMainAdmin struct {
		Data struct {
			TenantCode string `json:"tenant_code"`
			User       struct {
				Username string `json:"username"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(respAdminMain.Body.Bytes(), &resMainAdmin)
	if resMainAdmin.Data.TenantCode != "default" || resMainAdmin.Data.User.Username != "huxinkui" {
		t.Fatalf("expected backend huxinkui login in default space, got %+v", resMainAdmin)
	}

	// 8. 测试多租户分别使用自己的租户代码登录：创建租户 branch_gz
	_, err = a.TenantManager.CreateTenant("branch_gz", "广州分部", 200, "Gz-Secret-8888", "华南租户")
	if err != nil {
		t.Fatalf("failed to create branch_gz tenant: %v", err)
	}
	respLoginGz, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"username": "branch_gz",
		"password": "Gz-Secret-8888",
	}, nil, "")
	if respLoginGz.Code != 200 {
		t.Fatalf("branch_gz login failed: %s", respLoginGz.Body.String())
	}
	var resGz struct {
		Data struct {
			TenantCode string `json:"tenant_code"`
			User       struct {
				Username string `json:"username"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(respLoginGz.Body.Bytes(), &resGz)
	if resGz.Data.TenantCode != "branch_gz" || resGz.Data.User.Username != "branch_gz" {
		t.Fatalf("branch_gz login data mismatch: %+v", resGz)
	}
}

func TestReopenTenantSandboxWithV2SchemaVersion(t *testing.T) {
	dataDir := t.TempDir()
	a, err := Open(dataDir, licensecode.PublicKeyPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if err := a.EnsureDefaultAccount(); err != nil {
		t.Fatal(err)
	}

	router := a.Router(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}})

	// 1. 创建租户并确保其 Schema 版本升级到 TargetSchemaVersion (2)
	tCode := "tenant_v2_reopen"
	_, err = a.TenantManager.CreateTenant(tCode, "V2重开测试租户", 200, "V2-Pass-123456", "测试")
	if err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	inst, err := a.TenantManager.GetOrOpenTenant(tCode)
	if err != nil {
		t.Fatalf("first open failed: %v", err)
	}
	var ver int
	_ = inst.App.DB.QueryRow("PRAGMA user_version").Scan(&ver)
	if ver != TargetSchemaVersion {
		t.Fatalf("expected user_version %d, got %d", TargetSchemaVersion, ver)
	}

	// 2. 模拟 LRU 缓存淘汰或服务重启：从活跃连接池中彻底剔除并关闭连接
	a.TenantManager.mu.Lock()
	inst.App.Close()
	delete(a.TenantManager.instances, tCode)
	delete(a.TenantManager.handlers, tCode)
	a.TenantManager.mu.Unlock()

	// 3. 再次重新打开该物理数据库（此时数据库 user_version 已经是 2）
	reopenedInst, err := a.TenantManager.GetOrOpenTenant(tCode)
	if err != nil {
		t.Fatalf("reopening tenant with user_version=%d failed: %v", TargetSchemaVersion, err)
	}
	defer reopenedInst.App.Close()

	var reVer int
	_ = reopenedInst.App.DB.QueryRow("PRAGMA user_version").Scan(&reVer)
	if reVer != TargetSchemaVersion {
		t.Fatalf("expected user_version %d after reopen, got %d", TargetSchemaVersion, reVer)
	}

	// 4. 验证重新打开后 HTTP 登录与数据访问正常无误
	respLogin, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"username": tCode,
		"password": "V2-Pass-123456",
	}, nil, "")
	if respLogin.Code != 200 {
		t.Fatalf("login on reopened tenant failed: %s", respLogin.Body.String())
	}
}

func TestTenantArchiveLifecycleAndPurge(t *testing.T) {
	dataDir := t.TempDir()
	a, err := Open(dataDir, licensecode.PublicKeyPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if err := a.EnsureDefaultAccount(); err != nil {
		t.Fatal(err)
	}

	router := a.Router(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}})

	// 登录超级管理员 huxinkui
	respLogin, cookies := apiRequest(t, router, "POST", "/api/auth/login", M{
		"username": MasterAdminUsername,
		"password": DefaultAdminPassword,
	}, nil, "")
	if respLogin.Code != 200 {
		t.Fatalf("login huxinkui failed: %s", respLogin.Body.String())
	}
	var loginData struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(respLogin.Body.Bytes(), &loginData)
	token := loginData.Data.Token

	// 1. 创建待测试租户
	code := "corp_archive_test"
	_, err = a.TenantManager.CreateTenant(code, "归档测试分部", 200, "Pass123456", "测试归档与彻底清除")
	if err != nil {
		t.Fatalf("create tenant failed: %v", err)
	}

	tenantDir := filepath.Join(dataDir, "tenants", code)
	if _, err := os.Stat(tenantDir); err != nil {
		t.Fatalf("expected tenant dir %s to exist", tenantDir)
	}

	doDelete := func(targetCode string, body M) *httptest.ResponseRecorder {
		req := httptest.NewRequest("DELETE", "/api/system/tenants/"+targetCode, bytes.NewBufferString(js(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// 2. 错误二级确认密码：应返回 400 业务错误，包含明确提示，绝不能报 500
	respWrongPwd := doDelete(code, M{
		"confirm_password": "wrong-admin-password",
	})
	if respWrongPwd.Code != 400 {
		t.Fatalf("expected 400 for wrong password, got %d: %s", respWrongPwd.Code, respWrongPwd.Body.String())
	}
	if !strings.Contains(respWrongPwd.Body.String(), "管理员二级确认密码错误") {
		t.Fatalf("expected error message to mention wrong password, got %s", respWrongPwd.Body.String())
	}

	// 3. 正确密码归档：应生成 tar.gz，SHA256，移除物理目录，标记 status='archived'
	respArchive := doDelete(code, M{
		"confirm_password": DefaultAdminPassword,
	})
	if respArchive.Code != 200 {
		t.Fatalf("archive tenant failed with %d: %s", respArchive.Code, respArchive.Body.String())
	}
	var archiveRes struct {
		Data struct {
			ArchiveFile string `json:"archive_file"`
			ArchiveName string `json:"archive_name"`
			SHA256      string `json:"sha256"`
		} `json:"data"`
	}
	_ = json.Unmarshal(respArchive.Body.Bytes(), &archiveRes)
	if archiveRes.Data.ArchiveFile == "" || archiveRes.Data.ArchiveName == "" || len(archiveRes.Data.SHA256) != 64 {
		t.Fatalf("archive response missing fields: %+v", archiveRes)
	}
	archivePath := filepath.Join(dataDir, "backups", "archive", archiveRes.Data.ArchiveFile)
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive tar.gz file not found: %s", archivePath)
	}
	// 验证物理沙箱已被安全销毁
	if _, err := os.Stat(tenantDir); !os.IsNotExist(err) {
		t.Fatalf("tenant physical dir %s should have been removed", tenantDir)
	}
	// 验证主库状态为 archived
	tInfo, err := a.TenantManager.GetTenant(code)
	if err != nil {
		t.Fatalf("get tenant failed: %v", err)
	}
	if tInfo.Status != "archived" {
		t.Fatalf("expected status 'archived', got %s", tInfo.Status)
	}

	// 4. 对已归档租户重复点击归档：应返回 400 友好业务提示，绝不能抛 500 "操作失败，请查看日志或联系管理员"
	respReArchive := doDelete(code, M{
		"confirm_password": DefaultAdminPassword,
	})
	if respReArchive.Code != 400 {
		t.Fatalf("expected 400 for re-archiving, got %d: %s", respReArchive.Code, respReArchive.Body.String())
	}
	if !strings.Contains(respReArchive.Body.String(), "已经归档冷备") {
		t.Fatalf("expected friendly message for re-archive, got: %s", respReArchive.Body.String())
	}

	// 5. 对已归档租户执行其他操作：应全部安全拦截并返回 400
	if err := a.TenantManager.SetTenantStatus(code, "active"); err == nil {
		t.Fatal("enabling archived tenant should fail")
	}
	if err := a.TenantManager.ResetTenantPassword(code, "NewPass1234"); err == nil {
		t.Fatal("resetting password on archived tenant should fail")
	}
	if _, err := a.TenantManager.InspectTenant(code); err == nil {
		t.Fatal("inspecting archived tenant should fail")
	}
	if _, err := a.TenantManager.CreateImpersonation(MasterAdminUsername, code, "readonly", "127.0.0.1"); err == nil {
		t.Fatal("impersonating archived tenant should fail")
	}

	// 6. 对已归档租户执行彻底清除 (purge=true)
	respPurge := doDelete(code, M{
		"confirm_password": DefaultAdminPassword,
		"purge":            true,
	})
	if respPurge.Code != 200 {
		t.Fatalf("purge archived tenant failed with %d: %s", respPurge.Code, respPurge.Body.String())
	}
	// 验证主库记录已被彻底清除
	var cnt int
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM tenants WHERE code=?", code).Scan(&cnt)
	if cnt != 0 {
		t.Fatalf("tenant record should be purged from database, found %d", cnt)
	}
	// 验证冷备归档文件依然安全保留
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("cold backup archive must be preserved after purge: %s", archivePath)
	}

	// 7. 测试物理目录已被手工删除/缺失的租户归档容错性
	codeMissingDir := "corp_missing_dir"
	_, err = a.TenantManager.CreateTenant(codeMissingDir, "缺失目录租户", 200, "Pass123456", "测试缺失目录")
	if err != nil {
		t.Fatalf("create tenant failed: %v", err)
	}
	_ = os.RemoveAll(filepath.Join(dataDir, "tenants", codeMissingDir))
	// 归档缺失目录的租户：不应报 500，应平滑置为 archived 状态
	respMissingArchive := doDelete(codeMissingDir, M{
		"confirm_password": DefaultAdminPassword,
	})
	if respMissingArchive.Code != 200 {
		t.Fatalf("expected 200 for archiving tenant with missing dir, got %d: %s", respMissingArchive.Code, respMissingArchive.Body.String())
	}
	tMissing, _ := a.TenantManager.GetTenant(codeMissingDir)
	if tMissing.Status != "archived" {
		t.Fatalf("expected status 'archived', got %s", tMissing.Status)
	}
}

