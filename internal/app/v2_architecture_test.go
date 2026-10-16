package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"ruilian/internal/licensecode"
)

func TestV2ArchitectureFullSuite(t *testing.T) {
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

	// 1. 登录专属超级管理员 huxinkui
	respLoginHxk, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"username": MasterAdminUsername,
		"password": DefaultAdminPassword,
	}, nil, "")
	if respLoginHxk.Code != 200 {
		t.Fatalf("huxinkui login failed: %s", respLoginHxk.Body.String())
	}
	var loginData struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(respLoginHxk.Body.Bytes(), &loginData)
	tokenHxk := loginData.Data.Token

	// 2. 验证通用 admin 账号熔断拦截
	respAdmin, _ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"username": "admin",
		"password": DefaultAdminPassword,
	}, nil, "")
	if respAdmin.Code == 200 {
		t.Fatal("expected admin login to be rejected, but succeeded")
	}

	// 3. 创建子租户 corp_bj
	tenantBJ, err := a.TenantManager.CreateTenant("corp_bj", "北京分公司", 100, "Bj-Admin-2026", "华北总部", "2030-01-01", 100)
	if err != nil {
		t.Fatalf("create tenant corp_bj failed: %v", err)
	}
	if tenantBJ.TenantCode != "corp_bj" || tenantBJ.CompanyName != "北京分公司" {
		t.Fatalf("unexpected tenant data: %+v", tenantBJ)
	}

	// 4. 验证 Inspect API: 查看租户后台详情
	reqInspect := httptest.NewRequest("GET", "/api/system/tenants/corp_bj/inspect", nil)
	reqInspect.Header.Set("Authorization", "Bearer "+tokenHxk)
	wInspect := httptest.NewRecorder()
	router.ServeHTTP(wInspect, reqInspect)
	if wInspect.Code != 200 {
		t.Fatalf("Inspect API failed (%d): %s", wInspect.Code, wInspect.Body.String())
	}
	var inspectData struct {
		Data struct {
			TableRows map[string]int64 `json:"table_rows"`
			Tenant    Tenant           `json:"tenant"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wInspect.Body.Bytes(), &inspectData)
	if inspectData.Data.Tenant.Code != "corp_bj" {
		t.Fatalf("inspect tenant code mismatch: %s", inspectData.Data.Tenant.Code)
	}

	// 5. 验证 Impersonation 跨空间穿透巡检与只读保护 (IMPERSONATION_READONLY_GUARD)
	// 5.1 申请只读巡检令牌 (默认 readonly)
	reqImp := httptest.NewRequest("POST", "/api/system/tenants/corp_bj/impersonate", bytes.NewBufferString(`{"mode":"readonly"}`))
	reqImp.Header.Set("Content-Type", "application/json")
	reqImp.Header.Set("Authorization", "Bearer "+tokenHxk)
	wImp := httptest.NewRecorder()
	router.ServeHTTP(wImp, reqImp)
	if wImp.Code != 200 {
		t.Fatalf("Impersonate API failed (%d): %s", wImp.Code, wImp.Body.String())
	}
	var impRes struct {
		Data struct {
			Token        string `json:"token"`
			AccessMode   string `json:"access_mode"`
			TargetTenant string `json:"target_tenant"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wImp.Body.Bytes(), &impRes)
	impToken := impRes.Data.Token
	if impRes.Data.AccessMode != "readonly" || impRes.Data.TargetTenant != "corp_bj" {
		t.Fatalf("unexpected impersonation session: %+v", impRes)
	}

	// 5.2 在只读巡检令牌下，GET 查询应当正常放行
	uAuth, errAuth := a.Authenticate(impToken)
	t.Logf("a.Authenticate: uAuth=%+v, errAuth=%v", uAuth, errAuth)
	bjApp, _ := a.TenantManager.GetTenantApp("corp_bj")
	uAuthBJ, errAuthBJ := bjApp.Authenticate(impToken)
	t.Logf("bjApp.Authenticate: uAuthBJ=%+v, errAuthBJ=%v", uAuthBJ, errAuthBJ)

	reqGetList := httptest.NewRequest("GET", "/api/masters/product", nil)
	reqGetList.Header.Set("X-Tenant-Code", "corp_bj")
	reqGetList.Header.Set("X-Impersonate-Token", impToken)
	wGetList := httptest.NewRecorder()
	router.ServeHTTP(wGetList, reqGetList)
	if wGetList.Code != 200 {
		t.Fatalf("read-only GET should succeed, got %d: %s", wGetList.Code, wGetList.Body.String())
	}

	// 5.3 在只读巡检模式下，POST 写操作必须被严厉拦截！
	reqWriteProduct := httptest.NewRequest("POST", "/api/masters/product", bytes.NewBufferString(js(M{
		"code": "PROD_BJ_001",
		"name": "北京特产",
	})))
	reqWriteProduct.Header.Set("Content-Type", "application/json")
	reqWriteProduct.Header.Set("X-Tenant-Code", "corp_bj")
	reqWriteProduct.Header.Set("X-Impersonate-Token", impToken)
	wWriteProduct := httptest.NewRecorder()
	router.ServeHTTP(wWriteProduct, reqWriteProduct)
	if wWriteProduct.Code != 403 || !strings.Contains(wWriteProduct.Body.String(), "IMPERSONATION_READONLY_GUARD") {
		t.Fatalf("expected write to be blocked by IMPERSONATION_READONLY_GUARD, got %d: %s", wWriteProduct.Code, wWriteProduct.Body.String())
	}

	// 5.4 申请排障读写巡检令牌 (readwrite mode)
	reqImpRW := httptest.NewRequest("POST", "/api/system/tenants/corp_bj/impersonate", bytes.NewBufferString(`{"mode":"readwrite"}`))
	reqImpRW.Header.Set("Content-Type", "application/json")
	reqImpRW.Header.Set("Authorization", "Bearer "+tokenHxk)
	wImpRW := httptest.NewRecorder()
	router.ServeHTTP(wImpRW, reqImpRW)
	var impRWRes struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wImpRW.Body.Bytes(), &impRWRes)
	impRWToken := impRWRes.Data.Token

	// 在读写模式下创建商品应当成功
	reqWriteRW := httptest.NewRequest("POST", "/api/masters/product", bytes.NewBufferString(js(M{
		"code": "PROD_BJ_002",
		"name": "北京烤鸭",
		"data": M{"unit": "只"},
	})))
	reqWriteRW.Header.Set("Content-Type", "application/json")
	reqWriteRW.Header.Set("X-Tenant-Code", "corp_bj")
	reqWriteRW.Header.Set("X-Impersonate-Token", impRWToken)
	reqWriteRW.Header.Set("X-Impersonate-Mode", "readwrite")
	wWriteRW := httptest.NewRecorder()
	router.ServeHTTP(wWriteRW, reqWriteRW)
	if wWriteRW.Code != 200 {
		t.Fatalf("read-write impersonation should succeed, got %d: %s", wWriteRW.Code, wWriteRW.Body.String())
	}

	// 5.5 验证子租户 operation_log 记录审计为 "huxinkui (平台管理员代维)"
	var loggedUser string
	_ = bjApp.DB.QueryRow("SELECT username FROM operation_log ORDER BY id DESC LIMIT 1").Scan(&loggedUser)
	if loggedUser != "huxinkui (平台管理员代维)" {
		t.Fatalf("expected audit username to be 'huxinkui (平台管理员代维)', got %s", loggedUser)
	}

	// 6. 验证防横向越权机制 (TENANT_ISOLATION_VIOLATION)
	// 6.1 子租户用户登录
	respLoginBJ, cookiesBJ := apiRequest(t, router, "POST", "/api/auth/login", M{
		"username": "corp_bj",
		"password": "Bj-Admin-2026",
		"tenant":   "corp_bj",
	}, nil, "")
	if respLoginBJ.Code != 200 {
		t.Fatalf("corp_bj login failed: %s", respLoginBJ.Body.String())
	}

	// 6.2 创建第二个租户 corp_sz
	_, _ = a.TenantManager.CreateTenant("corp_sz", "深圳分公司", 100, "Sz-Admin-2026", "华南")

	// 6.3 corp_bj 试图跨空间访问 corp_sz 数据，必须被拦截并返回 403 TENANT_ISOLATION_VIOLATION！
	reqCross := httptest.NewRequest("GET", "/api/masters/product", nil)
	reqCross.Header.Set("X-Tenant-Code", "corp_sz") // 试图访问 sz
	for _, c := range cookiesBJ {
		reqCross.AddCookie(c)
	}
	wCross := httptest.NewRecorder()
	router.ServeHTTP(wCross, reqCross)
	if wCross.Code != 403 || !strings.Contains(wCross.Body.String(), "TENANT_ISOLATION_VIOLATION") {
		t.Fatalf("expected cross-tenant access to be blocked with TENANT_ISOLATION_VIOLATION, got %d: %s", wCross.Code, wCross.Body.String())
	}

	// 7. 空间碎片整理与维护接口验证 (Maintenance)
	reqMaint := httptest.NewRequest("POST", "/api/system/tenants/corp_bj/maintenance", bytes.NewBufferString(`{"action":"checkpoint"}`))
	reqMaint.Header.Set("Content-Type", "application/json")
	reqMaint.Header.Set("Authorization", "Bearer "+tokenHxk)
	wMaint := httptest.NewRecorder()
	router.ServeHTTP(wMaint, reqMaint)
	if wMaint.Code != 200 {
		t.Fatalf("maintenance checkpoint failed: %s", wMaint.Body.String())
	}

	// 8. 验证配额死锁逃生通道 (Quota Exempt Routes)
	// 8.1 将 corp_bj 配额设为 1MB，并放入大文件使之处于 readonly_quota 状态
	_ = a.TenantManager.UpdateTenantQuota("corp_bj", 1)
	bigFile := filepath.Join(dataDir, "tenants", "corp_bj", "dummy_exceed.db")
	_ = os.WriteFile(bigFile, make([]byte, 2*1024*1024), 0600)
	// 触发配额检查锁定
	_ = a.TenantManager.CheckStorageQuota("corp_bj")

	// 8.2 普通写操作被拦截
	reqBlocked := httptest.NewRequest("POST", "/api/masters/product", bytes.NewBufferString(js(M{
		"code": "PROD_FAIL",
		"name": "超限商品",
	})))
	reqBlocked.Header.Set("Content-Type", "application/json")
	reqBlocked.Header.Set("X-Tenant-Code", "corp_bj")
	for _, c := range cookiesBJ {
		reqBlocked.AddCookie(c)
	}
	wBlocked := httptest.NewRecorder()
	router.ServeHTTP(wBlocked, reqBlocked)
	if wBlocked.Code != 403 || !strings.Contains(wBlocked.Body.String(), "STORAGE_QUOTA_EXCEEDED") {
		t.Fatalf("expected STORAGE_QUOTA_EXCEEDED, got %d: %s", wBlocked.Code, wBlocked.Body.String())
	}

	// 8.3 逃生通道一：超级管理员 huxinkui 依然可以请求该租户进行代维修复
	reqAdminFix := httptest.NewRequest("POST", "/api/masters/product", bytes.NewBufferString(js(M{
		"code": "PROD_ADMIN_FIX",
		"name": "管理员修复商品",
	})))
	reqAdminFix.Header.Set("Content-Type", "application/json")
	reqAdminFix.Header.Set("X-Tenant-Code", "corp_bj")
	reqAdminFix.Header.Set("Authorization", "Bearer "+tokenHxk)
	reqAdminFix.Header.Set("X-Impersonate-Mode", "readwrite")
	wAdminFix := httptest.NewRecorder()
	router.ServeHTTP(wAdminFix, reqAdminFix)
	if wAdminFix.Code != 200 {
		t.Fatalf("admin exempt request should succeed during quota exceeded, got %d: %s", wAdminFix.Code, wAdminFix.Body.String())
	}

	// 8.4 逃生通道二：登出接口放行
	reqLogout := httptest.NewRequest("POST", "/api/auth/logout", bytes.NewBufferString("{}"))
	reqLogout.Header.Set("Content-Type", "application/json")
	reqLogout.Header.Set("X-Tenant-Code", "corp_bj")
	for _, c := range cookiesBJ {
		reqLogout.AddCookie(c)
	}
	wLogout := httptest.NewRecorder()
	router.ServeHTTP(wLogout, reqLogout)
	if wLogout.Code != 200 {
		t.Fatalf("logout exempt route should succeed, got %d: %s", wLogout.Code, wLogout.Body.String())
	}

	// 清理大文件并扩容
	_ = os.Remove(bigFile)
	_ = a.TenantManager.UpdateTenantQuota("corp_bj", 200)

	// 9. 验证租户冻结停用与 423 Locked
	_ = a.TenantManager.SetTenantStatus("corp_bj", "suspended")
	reqSuspended := httptest.NewRequest("GET", "/api/masters/product", nil)
	reqSuspended.Header.Set("X-Tenant-Code", "corp_bj")
	for _, c := range cookiesBJ {
		reqSuspended.AddCookie(c)
	}
	wSuspended := httptest.NewRecorder()
	router.ServeHTTP(wSuspended, reqSuspended)
	if wSuspended.Code != 423 {
		t.Fatalf("suspended tenant request should return 423 Locked, got %d: %s", wSuspended.Code, wSuspended.Body.String())
	}

	// 10. 租户归档与销毁 (DELETE /api/system/tenants/:code)
	// 密码错误拒绝
	reqDelWrong := httptest.NewRequest("DELETE", "/api/system/tenants/corp_sz", bytes.NewBufferString(`{"confirm_password":"wrong-password"}`))
	reqDelWrong.Header.Set("Content-Type", "application/json")
	reqDelWrong.Header.Set("Authorization", "Bearer "+tokenHxk)
	wDelWrong := httptest.NewRecorder()
	router.ServeHTTP(wDelWrong, reqDelWrong)
	if wDelWrong.Code == 200 {
		t.Fatal("archive tenant with wrong password should fail, but succeeded")
	}

	// 正确密码归档
	reqDel := httptest.NewRequest("DELETE", "/api/system/tenants/corp_sz", bytes.NewBufferString(fmt.Sprintf(`{"confirm_password":"%s"}`, DefaultAdminPassword)))
	reqDel.Header.Set("Content-Type", "application/json")
	reqDel.Header.Set("Authorization", "Bearer "+tokenHxk)
	wDel := httptest.NewRecorder()
	router.ServeHTTP(wDel, reqDel)
	if wDel.Code != 200 {
		t.Fatalf("archive tenant failed: %s", wDel.Body.String())
	}

	// 验证冷备归档文件已生成并计算了 sha256
	var delRes struct {
		Data struct {
			ArchiveFile string `json:"archive_file"`
			SHA256      string `json:"sha256"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wDel.Body.Bytes(), &delRes)
	if delRes.Data.ArchiveFile == "" || len(delRes.Data.SHA256) != 64 {
		t.Fatalf("archive file or SHA256 missing: %+v", delRes)
	}
	archivePath := filepath.Join(dataDir, "backups", "archive", delRes.Data.ArchiveFile)
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive file not found on disk: %s", archivePath)
	}

	t.Logf("V2.0 Full Architecture Suite successfully verified!")
}
