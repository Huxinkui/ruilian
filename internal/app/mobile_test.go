package app

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"ruilian/internal/licensecode"
)

func setupTestApp(t *testing.T) (*App, http.Handler, string) {
	dataDir := t.TempDir()
	a, err := Open(dataDir, licensecode.PublicKeyPEM, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := a.EnsureDefaultAccount(); err != nil {
		t.Fatal(err)
	}

	router := a.Router(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}})

	// Admin 登录获取主空间 Token
	token, _, err := a.Login(MasterAdminUsername, DefaultAdminPassword, "127.0.0.1")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	return a, router, token
}

func TestMobileVersionCheckAndPublish(t *testing.T) {
	a, router, adminToken := setupTestApp(t)
	defer a.Close()

	// 1. 初始未发布版本：检查版本接口
	req := httptest.NewRequest("GET", "/api/mobile/version/check?platform=android&version_code=100", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body: %s", w.Code, w.Body.String())
	}
	var resCheck M
	_ = json.Unmarshal(w.Body.Bytes(), &resCheck)
	dataCheck := resCheck["data"].(map[string]any)
	if dataCheck["has_update"] != false {
		t.Fatalf("expected has_update false, got %v", dataCheck["has_update"])
	}

	// 2. 超级管理员发布全新 APK 版本 (含二进制包上传)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("platform", "android")
	_ = writer.WriteField("version_name", "2.1.0")
	_ = writer.WriteField("version_code", "210")
	_ = writer.WriteField("min_supported_code", "200")
	_ = writer.WriteField("release_notes", "移动端APK全新上线：PDA条码激光查品、快速盘点、极速开单")

	// 模拟写入 APK 假二进制文件内容
	fakeAPKContent := []byte("PK\x03\x04RuilianFakeAPKBinaryDataPayloadForTesting1234567890abcdef")
	part, err := writer.CreateFormFile("apk_file", "ruilian-v2.1.0.apk")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(fakeAPKContent)
	_ = writer.Close()

	pubReq := httptest.NewRequest("POST", "/api/mobile/version/publish", body)
	pubReq.Header.Set("Content-Type", writer.FormDataContentType())
	pubReq.Header.Set("Authorization", "Bearer "+adminToken)
	pubW := httptest.NewRecorder()
	router.ServeHTTP(pubW, pubReq)
	if pubW.Code != http.StatusOK {
		t.Fatalf("expected status 200 on publish, got %d, body: %s", pubW.Code, pubW.Body.String())
	}

	// 3. 客户端检查版本：旧版本 v100 检查更新
	req2 := httptest.NewRequest("GET", "/api/mobile/version/check?platform=android&version_code=100", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w2.Code)
	}
	var res2 M
	_ = json.Unmarshal(w2.Body.Bytes(), &res2)
	data2 := res2["data"].(map[string]any)
	if data2["has_update"] != true {
		t.Fatalf("expected has_update true, got %v", data2["has_update"])
	}
	if data2["force_update"] != true { // 100 < 200 (min_supported_code)
		t.Fatalf("expected force_update true for client v100 < min 200, got %v", data2["force_update"])
	}
	if data2["version_name"] != "2.1.0" {
		t.Fatalf("expected version_name 2.1.0, got %v", data2["version_name"])
	}

	// 4. 下载 APK 包
	dlReq := httptest.NewRequest("GET", "/api/mobile/version/download?v=210", nil)
	dlW := httptest.NewRecorder()
	router.ServeHTTP(dlW, dlReq)
	if dlW.Code != http.StatusOK {
		t.Fatalf("expected status 200 downloading APK, got %d", dlW.Code)
	}
	if !bytes.Equal(dlW.Body.Bytes(), fakeAPKContent) {
		t.Fatalf("downloaded APK content does not match original")
	}

	// 5. 断点续传 Range 下载测试 (bytes=0-9)
	rangeReq := httptest.NewRequest("GET", "/api/mobile/version/download?v=210", nil)
	rangeReq.Header.Set("Range", "bytes=0-9")
	rangeW := httptest.NewRecorder()
	router.ServeHTTP(rangeW, rangeReq)
	if rangeW.Code != http.StatusPartialContent {
		t.Fatalf("expected status 206 Partial Content, got %d", rangeW.Code)
	}
	if !bytes.Equal(rangeW.Body.Bytes(), fakeAPKContent[:10]) {
		t.Fatalf("range content mismatch: expected %d bytes, got %d bytes", len(fakeAPKContent[:10]), len(rangeW.Body.Bytes()))
	}
}

func TestMobileLoginAndSessionRenewal(t *testing.T) {
	a, router, _ := setupTestApp(t)
	defer a.Close()

	// 1. 移动端登录请求 (带 client_type: android)
	loginBody, _ := json.Marshal(M{
		"username":    MasterAdminUsername,
		"password":    DefaultAdminPassword,
		"client_type": "android",
		"device_id":   "test-device-pda-001",
		"platform":    "android",
	})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d, body: %s", w.Code, w.Body.String())
	}
	var res M
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	data := res["data"].(map[string]any)
	token := data["token"].(string)
	expiresIn := int64(data["expires_in"].(float64))

	// 移动端应该分配 30 天超长会话 (30 * 24 * 3600 = 2592000 秒)
	if expiresIn != 30*24*3600 {
		t.Fatalf("expected mobile expiresIn 2592000, got %d", expiresIn)
	}

	// 2. 移动端设备登记注册
	devBody, _ := json.Marshal(M{
		"device_id":     "test-device-pda-001",
		"device_model":  "Honeywell-EDA51K",
		"os_version":    "Android 11",
		"app_version":   "2.1.0",
		"app_build":     210,
		"screen_dpi":    "320dpi",
		"push_token":    "test-fcm-token-123456",
		"scanner_type":  "laser_honeywell",
	})
	devReq := httptest.NewRequest("POST", "/api/mobile/device/register", bytes.NewReader(devBody))
	devReq.Header.Set("Content-Type", "application/json")
	devReq.Header.Set("Authorization", "Bearer "+token)
	devW := httptest.NewRecorder()
	router.ServeHTTP(devW, devReq)
	if devW.Code != http.StatusOK {
		t.Fatalf("expected device register 200, got %d, body: %s", devW.Code, devW.Body.String())
	}

	// 3. 查询已登记设备列表
	listReq := httptest.NewRequest("GET", "/api/mobile/devices", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listW := httptest.NewRecorder()
	router.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("expected devices list 200, got %d, body: %s", listW.Code, listW.Body.String())
	}
	var listRes M
	_ = json.Unmarshal(listW.Body.Bytes(), &listRes)
	listData := listRes["data"].(map[string]any)
	if listData["total"].(float64) < 1 {
		t.Fatalf("expected at least 1 registered device, got %v", listData["total"])
	}

	// 4. 移动端会话静默无感续期
	refreshReq := httptest.NewRequest("POST", "/api/mobile/auth/refresh", strings.NewReader("{}"))
	refreshReq.Header.Set("Content-Type", "application/json")
	refreshReq.Header.Set("Authorization", "Bearer "+token)
	refreshW := httptest.NewRecorder()
	router.ServeHTTP(refreshW, refreshReq)
	if refreshW.Code != http.StatusOK {
		t.Fatalf("expected session refresh 200, got %d, body: %s", refreshW.Code, refreshW.Body.String())
	}
	var refRes M
	_ = json.Unmarshal(refreshW.Body.Bytes(), &refRes)
	refData := refRes["data"].(map[string]any)
	if refData["refreshed"] != true {
		t.Fatalf("expected refreshed true, got %v", refData["refreshed"])
	}
}

func TestMobileScanAndBusinessOperations(t *testing.T) {
	a, router, token := setupTestApp(t)
	defer a.Close()

	// 1. 准备主数据：仓库与商品
	u, _ := a.Authenticate(token)
	wh, err := a.SaveMaster(u, "127.0.0.1", 0, Master{
		Kind:   "warehouse",
		Name:   "主仓库-A区",
		Code:   "WH-01",
		Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	whID := wh.ID

	prod, err := a.SaveMaster(u, "127.0.0.1", 0, Master{
		Kind:   "product",
		Name:   "高精度红外测距仪",
		Code:   "P-IR-999",
		Active: true,
		Data: M{
			"barcode":     "6901234567890",
			"unit":        "台",
			"sales_price": "299.00",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prodID := prod.ID

	// 准备客户
	cust, err := a.SaveMaster(u, "127.0.0.1", 0, Master{
		Kind:   "customer",
		Name:   "深圳市华创智能科技有限公司",
		Code:   "CUST-SZ-001",
		Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	custID := cust.ID

	// 2. PDA 条码激光扫码查品接口 (/api/mobile/scan?barcode=6901234567890)
	scanReq := httptest.NewRequest("GET", "/api/mobile/scan?barcode=6901234567890", nil)
	scanReq.Header.Set("Authorization", "Bearer "+token)
	scanW := httptest.NewRecorder()
	router.ServeHTTP(scanW, scanReq)
	if scanW.Code != http.StatusOK {
		t.Fatalf("expected scan 200, got %d, body: %s", scanW.Code, scanW.Body.String())
	}
	var scanRes M
	_ = json.Unmarshal(scanW.Body.Bytes(), &scanRes)
	scanData := scanRes["data"].(map[string]any)
	prodMap := scanData["product"].(map[string]any)
	if prodMap["name"] != "高精度红外测距仪" || prodMap["code"] != "P-IR-999" {
		t.Fatalf("scan result mismatch: %+v", scanData)
	}

	// 3. 移动端快速扫码盘点作业 (/api/mobile/quick-inventory)
	invBody, _ := json.Marshal(M{
		"warehouse_id": whID,
		"note":         "PDA 移动端仓库巡检实盘",
		"auto_submit":  true,
		"items": []M{
			{
				"product_id":      prodID,
				"actual_quantity": "50",
				"location":        "A-01-01",
			},
		},
	})
	invReq := httptest.NewRequest("POST", "/api/mobile/quick-inventory", bytes.NewReader(invBody))
	invReq.Header.Set("Content-Type", "application/json")
	invReq.Header.Set("Authorization", "Bearer "+token)
	invW := httptest.NewRecorder()
	router.ServeHTTP(invW, invReq)
	if invW.Code != http.StatusOK {
		t.Fatalf("expected quick-inventory 200, got %d, body: %s", invW.Code, invW.Body.String())
	}
	var invRes M
	_ = json.Unmarshal(invW.Body.Bytes(), &invRes)
	invData := invRes["data"].(map[string]any)
	if invData["kind"] != "inventory_check" {
		t.Fatalf("expected document kind inventory_check, got %v", invData["kind"])
	}

	// 盘点后再次扫码查库存，应该反映最新库存
	scanW2 := httptest.NewRecorder()
	router.ServeHTTP(scanW2, scanReq)
	if scanW2.Code != http.StatusOK {
		t.Fatalf("expected scan 200, got %d", scanW2.Code)
	}
	var scanRes2 M
	_ = json.Unmarshal(scanW2.Body.Bytes(), &scanRes2)
	scanData2 := scanRes2["data"].(map[string]any)
	stocks := scanData2["warehouses"].([]any)
	if len(stocks) == 0 {
		t.Fatalf("expected warehouse stocks after inventory count")
	}

	// 4. 移动端极速开单作业 (/api/mobile/quick-order)
	orderBody, _ := json.Marshal(M{
		"kind":         "sales_delivery",
		"partner_id":   custID,
		"warehouse_id": whID,
		"note":         "PDA 现场即时配送出库",
		"auto_submit":  true,
		"items": []M{
			{
				"product_id": prodID,
				"quantity":   "5",
				"price":      "299.00",
			},
		},
	})
	orderReq := httptest.NewRequest("POST", "/api/mobile/quick-order", bytes.NewReader(orderBody))
	orderReq.Header.Set("Content-Type", "application/json")
	orderReq.Header.Set("Authorization", "Bearer "+token)
	orderW := httptest.NewRecorder()
	router.ServeHTTP(orderW, orderReq)
	if orderW.Code != http.StatusOK {
		t.Fatalf("expected quick-order 200, got %d, body: %s", orderW.Code, orderW.Body.String())
	}
	var orderRes M
	_ = json.Unmarshal(orderW.Body.Bytes(), &orderRes)
	orderData := orderRes["data"].(map[string]any)
	if orderData["kind"] != "sales_delivery" {
		t.Fatalf("expected sales_delivery, got %v", orderData["kind"])
	}

	// 5. 移动驾驶舱指标 (/api/mobile/dashboard)
	dashReq := httptest.NewRequest("GET", "/api/mobile/dashboard", nil)
	dashReq.Header.Set("Authorization", "Bearer "+token)
	dashW := httptest.NewRecorder()
	router.ServeHTTP(dashW, dashReq)
	if dashW.Code != http.StatusOK {
		t.Fatalf("expected dashboard 200, got %d, body: %s", dashW.Code, dashW.Body.String())
	}
	var dashRes M
	_ = json.Unmarshal(dashW.Body.Bytes(), &dashRes)
	dashData := dashRes["data"].(map[string]any)
	if dashData["tenant_code"] == "" || dashData["user_name"] == "" {
		t.Fatalf("expected tenant_code and user_name in dashboard, got %+v", dashData)
	}

	// 6. 移动端照片/凭证附件上传与回显
	photoBody := &bytes.Buffer{}
	photoWriter := multipart.NewWriter(photoBody)
	photoPart, err := photoWriter.CreateFormFile("file", "delivery_receipt_photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	fakePhotoBytes := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x01\x00`\x00`\x00\x00\xff\xdb\x00C\x00testjpegphoto")
	_, _ = photoPart.Write(fakePhotoBytes)
	_ = photoWriter.Close()

	uploadReq := httptest.NewRequest("POST", "/api/mobile/upload", photoBody)
	uploadReq.Header.Set("Content-Type", photoWriter.FormDataContentType())
	uploadReq.Header.Set("Authorization", "Bearer "+token)
	uploadW := httptest.NewRecorder()
	router.ServeHTTP(uploadW, uploadReq)
	if uploadW.Code != http.StatusOK {
		t.Fatalf("expected upload 200, got %d, body: %s", uploadW.Code, uploadW.Body.String())
	}
	var uploadRes M
	_ = json.Unmarshal(uploadW.Body.Bytes(), &uploadRes)
	uploadData := uploadRes["data"].(map[string]any)
	fileURL := uploadData["url"].(string)

	// 回显附件
	getImgReq := httptest.NewRequest("GET", fileURL, nil)
	getImgW := httptest.NewRecorder()
	router.ServeHTTP(getImgW, getImgReq)
	if getImgW.Code != http.StatusOK {
		t.Fatalf("expected image get 200, got %d", getImgW.Code)
	}
	if !bytes.Equal(getImgW.Body.Bytes(), fakePhotoBytes) {
		t.Fatalf("uploaded and retrieved photo content mismatch")
	}

	// 7. 弱网离线增量同步 (/api/mobile/sync/products, /api/mobile/sync/customers)
	syncPReq := httptest.NewRequest("GET", "/api/mobile/sync/products?since=2020-01-01T00:00:00Z", nil)
	syncPReq.Header.Set("Authorization", "Bearer "+token)
	syncPW := httptest.NewRecorder()
	router.ServeHTTP(syncPW, syncPReq)
	if syncPW.Code != http.StatusOK {
		t.Fatalf("expected sync products 200, got %d, body: %s", syncPW.Code, syncPW.Body.String())
	}
	var syncPRes M
	_ = json.Unmarshal(syncPW.Body.Bytes(), &syncPRes)
	syncPData := syncPRes["data"].(map[string]any)
	if syncPData["count"].(float64) < 1 {
		t.Fatalf("expected synced products count >= 1, got %v", syncPData["count"])
	}

	// 使用刚刚同步的最新时间进行无更新查询
	lastSync := syncPData["last_sync_time"].(string)
	syncPReq2 := httptest.NewRequest("GET", "/api/mobile/sync/products?since="+lastSync, nil)
	syncPReq2.Header.Set("Authorization", "Bearer "+token)
	syncPW2 := httptest.NewRecorder()
	router.ServeHTTP(syncPW2, syncPReq2)
	if syncPW2.Code != http.StatusOK {
		t.Fatalf("expected sync products 200, got %d", syncPW2.Code)
	}
	var syncPRes2 M
	_ = json.Unmarshal(syncPW2.Body.Bytes(), &syncPRes2)
	syncPData2 := syncPRes2["data"].(map[string]any)
	if syncPData2["count"].(float64) != 0 {
		t.Fatalf("expected delta count 0 for unchanged products, got %v", syncPData2["count"])
	}
}
