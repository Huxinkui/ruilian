package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// RegisterMobileRoutes 注册移动端 APK 专属及适配路由组
func (a *App) RegisterMobileRoutes(r *gin.Engine, api *gin.RouterGroup) {
	// ==========================================
	// 1. 公开接口（无需登录鉴权）：版本检测与 APK 下载
	// ==========================================
	r.GET("/api/mobile/version/check", func(c *gin.Context) {
		platform := strings.ToLower(strings.TrimSpace(c.DefaultQuery("platform", "android")))
		clientCode, _ := strconv.Atoi(c.DefaultQuery("version_code", "0"))
		resp, err := a.CheckAppVersion(platform, clientCode)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, resp)
	})

	r.GET("/api/mobile/version/download", func(c *gin.Context) {
		a.ServeAPKDownload(c)
	})

	r.GET("/api/mobile/uploads/*filepath", func(c *gin.Context) {
		relPath := c.Param("filepath")
		relPath = strings.TrimPrefix(relPath, "/")
		if strings.Contains(relPath, "..") {
			c.AbortWithStatusJSON(http.StatusForbidden, M{"ok": false, "error": M{"code": "FORBIDDEN", "message": "非法路径"}})
			return
		}
		fullPath := filepath.Join(a.DataDir, "uploads", relPath)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			c.AbortWithStatusJSON(http.StatusNotFound, M{"ok": false, "error": M{"code": "NOT_FOUND", "message": "附件不存在"}})
			return
		}
		c.File(fullPath)
	})

	// ==========================================
	// 2. 鉴权接口（需要登录）：会话、设备、业务作业
	// ==========================================
	mob := api.Group("/mobile")

	// 2.1 会话心跳与设备绑定
	mob.POST("/auth/refresh", func(c *gin.Context) {
		token := c.GetHeader("X-Session-Token")
		if token == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}
		if token == "" {
			cookie, _ := c.Cookie("ruilian_session")
			token = cookie
		}
		err := a.TouchSession(token, 30*24*time.Hour)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, M{
			"refreshed":  true,
			"expires_in": int64(30 * 24 * 3600),
			"message":    "移动端会话续期成功（30天有效）",
		})
	})

	mob.POST("/device/register", func(c *gin.Context) {
		var input MobileDevice
		if !a.bind(c, &input) {
			return
		}
		u := currentUser(c)
		tenantCode := a.TenantCode
		if tenantCode == "" {
			tenantCode = "default"
		}
		err := a.RegisterMobileDevice(u, c.ClientIP(), tenantCode, input)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, M{"registered": true, "device_id": input.DeviceID})
	})

	// 2.2 移动轻量经营驾驶舱
	mob.GET("/dashboard", func(c *gin.Context) {
		u := currentUser(c)
		dash, err := a.GetMobileDashboard(u)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, dash)
	})

	// 2.3 PDA / 摄像头扫码商品极速查品与多仓实时库存
	mob.GET("/scan", func(c *gin.Context) {
		u := currentUser(c)
		barcode := strings.TrimSpace(c.Query("barcode"))
		if barcode == "" {
			barcode = strings.TrimSpace(c.Query("code"))
		}
		if barcode == "" {
			barcode = strings.TrimSpace(c.Query("q"))
		}
		if barcode == "" {
			a.error(c, fail("请提供条形码、二维码或商品编码 (barcode / code)"))
			return
		}
		result, err := a.ScanProductBarcode(u, barcode)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, result)
	})

	// 2.4 移动端快速扫码盘点作业
	mob.POST("/quick-inventory", a.require("inventory.edit"), a.licensed("inventory"), func(c *gin.Context) {
		var input struct {
			WarehouseID int64                `json:"warehouse_id"`
			Items       []QuickInventoryItem `json:"items"`
			Note        string               `json:"note"`
			AutoSubmit  bool                 `json:"auto_submit"`
		}
		if !a.bind(c, &input) {
			return
		}
		u := currentUser(c)
		doc, err := a.QuickInventoryCheck(u, c.ClientIP(), input.WarehouseID, input.Items, input.Note, input.AutoSubmit)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, doc)
	})

	// 2.5 移动端极速开单作业（销售订单 / 销售出库）
	mob.POST("/quick-order", a.require("sales.edit"), a.licensed("sales"), func(c *gin.Context) {
		var input struct {
			Kind        string           `json:"kind"` // sales_order 或 sales_delivery
			PartnerID   int64            `json:"partner_id"`
			WarehouseID int64            `json:"warehouse_id"`
			Items       []QuickOrderItem `json:"items"`
			Note        string           `json:"note"`
			AutoSubmit  bool             `json:"auto_submit"`
		}
		if !a.bind(c, &input) {
			return
		}
		if input.Kind == "" {
			input.Kind = "sales_delivery"
		}
		u := currentUser(c)
		doc, err := a.QuickOrder(u, c.ClientIP(), input.Kind, input.PartnerID, input.WarehouseID, input.Items, input.Note, input.AutoSubmit)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, doc)
	})

	// 2.6 移动现场拍照与凭证图片上传 (multipart/form-data)
	mob.POST("/upload", func(c *gin.Context) {
		fileHeader, err := c.FormFile("file")
		if err != nil {
			a.error(c, fail("未获取到上传文件: "+err.Error()))
			return
		}
		u := currentUser(c)
		tenantCode := a.TenantCode
		if tenantCode == "" {
			tenantCode = "default"
		}
		res, err := a.UploadMobileAttachment(u, c.ClientIP(), tenantCode, fileHeader)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, res)
	})

	// 2.7 离线数据增量同步 (Delta Sync)
	mob.GET("/sync/products", a.require("base.view"), func(c *gin.Context) {
		since := strings.TrimSpace(c.Query("since"))
		items, lastSync, err := a.SyncIncrementalProducts(since)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, M{
			"items":          items,
			"count":          len(items),
			"last_sync_time": lastSync,
		})
	})

	mob.GET("/sync/customers", a.require("base.view"), func(c *gin.Context) {
		since := strings.TrimSpace(c.Query("since"))
		items, lastSync, err := a.SyncIncrementalCustomers(since)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, M{
			"items":          items,
			"count":          len(items),
			"last_sync_time": lastSync,
		})
	})

	// 2.8 APK 版本发布与安装包管理（仅超级管理员可操作）
	mob.POST("/version/publish", a.requireMasterAdmin(), func(c *gin.Context) {
		var input AppVersion
		contentType := c.ContentType()
		if strings.HasPrefix(contentType, "multipart/form-data") {
			// 支持直接带 APK 二进制文件上传发布
			input.Platform = c.PostForm("platform")
			input.VersionName = c.PostForm("version_name")
			input.VersionCode, _ = strconv.Atoi(c.PostForm("version_code"))
			input.MinSupportedCode, _ = strconv.Atoi(c.PostForm("min_supported_code"))
			input.ReleaseNotes = c.PostForm("release_notes")
			input.DownloadURL = c.PostForm("download_url")

			fileHeader, err := c.FormFile("apk_file")
			if err == nil && fileHeader != nil {
				savedPath, size, hash, errSave := a.SaveUploadedAPK(fileHeader, input.VersionCode)
				if errSave != nil {
					a.error(c, fail("保存 APK 文件失败: "+errSave.Error()))
					return
				}
				input.APKSize = size
				input.APKSHA256 = hash
				if input.DownloadURL == "" {
					input.DownloadURL = fmt.Sprintf("/api/mobile/version/download?v=%d", input.VersionCode)
				}
				if a.Logger != nil {
					a.Logger.Printf("已上传并持久化 APK 安装包: %s (大小: %d 字节, SHA256: %s)", savedPath, size, hash)
				}
			}
		} else {
			if !a.bind(c, &input) {
				return
			}
		}

		u := currentUser(c)
		err := a.PublishAppVersion(u, c.ClientIP(), input)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, M{"published": true, "version": input})
	})

	// 2.9 已登记移动设备清单
	mob.GET("/devices", a.require("system.settings"), func(c *gin.Context) {
		u := currentUser(c)
		tenantCode := a.TenantCode
		if tenantCode == "" {
			tenantCode = "default"
		}
		devices, err := a.ListMobileDevices(u, tenantCode)
		if err != nil {
			a.error(c, err)
			return
		}
		a.ok(c, M{"items": devices, "total": len(devices)})
	})
}

// CheckAppVersion 检查移动端 APK 是否有可用更新
func (a *App) CheckAppVersion(platform string, clientVersionCode int) (*AppVersionCheckResponse, error) {
	if platform == "" {
		platform = "android"
	}
	var v AppVersion
	var isLatestInt int
	err := a.DB.QueryRow(`SELECT id, platform, version_name, version_code, min_supported_code, download_url, apk_size, apk_sha256, release_notes, is_latest, published_at FROM app_versions WHERE platform=? AND is_latest=1 ORDER BY version_code DESC LIMIT 1`, platform).
		Scan(&v.ID, &v.Platform, &v.VersionName, &v.VersionCode, &v.MinSupportedCode, &v.DownloadURL, &v.APKSize, &v.APKSHA256, &v.ReleaseNotes, &isLatestInt, &v.PublishedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			// 库中尚未发布自定义版本时，基于程序内嵌版本返回默认响应
			return &AppVersionCheckResponse{
				HasUpdate:        false,
				ForceUpdate:      false,
				VersionName:      Version,
				VersionCode:      100,
				MinSupportedCode: 100,
				DownloadURL:      "/api/mobile/version/download",
				APKSize:          0,
				APKSHA256:        "",
				ReleaseNotes:     "当前已是最新版本",
			}, nil
		}
		return nil, err
	}

	hasUpdate := v.VersionCode > clientVersionCode
	forceUpdate := clientVersionCode > 0 && clientVersionCode < v.MinSupportedCode

	return &AppVersionCheckResponse{
		HasUpdate:        hasUpdate,
		ForceUpdate:      forceUpdate,
		VersionName:      v.VersionName,
		VersionCode:      v.VersionCode,
		MinSupportedCode: v.MinSupportedCode,
		DownloadURL:      v.DownloadURL,
		APKSize:          v.APKSize,
		APKSHA256:        v.APKSHA256,
		ReleaseNotes:     v.ReleaseNotes,
	}, nil
}

// PublishAppVersion 发布全新 APK 版本
func (a *App) PublishAppVersion(u User, ip string, v AppVersion) error {
	if v.Platform == "" {
		v.Platform = "android"
	}
	if v.VersionName == "" || v.VersionCode <= 0 {
		return fail("版本名称与数字版本号 (version_code > 0) 不能为空")
	}
	if v.MinSupportedCode <= 0 {
		v.MinSupportedCode = v.VersionCode
	}
	if v.DownloadURL == "" {
		v.DownloadURL = fmt.Sprintf("/api/mobile/version/download?v=%d", v.VersionCode)
	}

	return a.write(func(tx *sql.Tx) error {
		// 先将该平台的其他版本 is_latest 设为 0
		_, _ = tx.Exec("UPDATE app_versions SET is_latest=0 WHERE platform=?", v.Platform)

		// 写入新版本
		_, err := tx.Exec(`INSERT INTO app_versions(platform, version_name, version_code, min_supported_code, download_url, apk_size, apk_sha256, release_notes, is_latest, published_at)
			VALUES(?,?,?,?,?,?,?,?,1,?)`,
			v.Platform, v.VersionName, v.VersionCode, v.MinSupportedCode, v.DownloadURL, v.APKSize, v.APKSHA256, v.ReleaseNotes, stamp())
		if err != nil {
			return err
		}
		return audit(tx, u, ip, fmt.Sprintf("发布移动端版本 %s (%s v%d)", v.Platform, v.VersionName, v.VersionCode), "system", 0, nil, v)
	})
}

// SaveUploadedAPK 持久化上传的 APK 文件
func (a *App) SaveUploadedAPK(fh *multipart.FileHeader, versionCode int) (string, int64, string, error) {
	apkDir := filepath.Join(a.DataDir, "apk")
	if err := os.MkdirAll(apkDir, 0700); err != nil {
		return "", 0, "", err
	}

	filename := fmt.Sprintf("ruilian-v%d.apk", versionCode)
	targetPath := filepath.Join(apkDir, filename)

	src, err := fh.Open()
	if err != nil {
		return "", 0, "", err
	}
	defer src.Close()

	dst, err := os.Create(targetPath)
	if err != nil {
		return "", 0, "", err
	}
	defer dst.Close()

	hasher := sha256.New()
	multiWriter := io.MultiWriter(dst, hasher)

	written, err := io.Copy(multiWriter, src)
	if err != nil {
		return "", 0, "", err
	}

	hashStr := hex.EncodeToString(hasher.Sum(nil))

	// 同时生成并更新软链或命名为 ruilian-latest.apk 供默认下载使用
	latestPath := filepath.Join(apkDir, "ruilian-latest.apk")
	_ = os.Remove(latestPath)
	_ = os.Link(targetPath, latestPath)

	return targetPath, written, hashStr, nil
}

// ServeAPKDownload 分发 APK 安装包文件（支持断点续传与正规 MIME 类型）
func (a *App) ServeAPKDownload(c *gin.Context) {
	apkDir := filepath.Join(a.DataDir, "apk")
	vStr := c.Query("v")

	var targetPath string
	var downloadFilename string

	if vStr != "" {
		targetPath = filepath.Join(apkDir, fmt.Sprintf("ruilian-v%s.apk", vStr))
		downloadFilename = fmt.Sprintf("ruilian-erp-v%s.apk", vStr)
	}

	if targetPath == "" || !fileExists(targetPath) {
		targetPath = filepath.Join(apkDir, "ruilian-latest.apk")
		downloadFilename = "ruilian-erp-latest.apk"
	}

	if !fileExists(targetPath) {
		// 遍历查找最新的一份 APK
		entries, _ := os.ReadDir(apkDir)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".apk") {
				targetPath = filepath.Join(apkDir, e.Name())
				downloadFilename = e.Name()
				break
			}
		}
	}

	if !fileExists(targetPath) {
		c.JSON(http.StatusNotFound, M{
			"ok": false,
			"error": M{
				"code":    "APK_NOT_FOUND",
				"message": "服务端尚未上传或生成 APK 安装包，请联系平台管理员上传安装包文件",
			},
		})
		return
	}

	c.Header("Content-Type", "application/vnd.android.package-archive")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, downloadFilename))
	c.Header("Accept-Ranges", "bytes")
	http.ServeFile(c.Writer, c.Request, targetPath)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// RegisterMobileDevice 登记/更新移动设备台账与推送凭证
func (a *App) RegisterMobileDevice(u User, ip string, tenantCode string, d MobileDevice) error {
	d.DeviceID = strings.TrimSpace(d.DeviceID)
	if d.DeviceID == "" {
		return fail("设备标识 (device_id) 不能为空")
	}
	now := stamp()
	return a.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO mobile_devices(user_id, tenant_code, device_id, device_model, os_version, app_version, push_token, last_active_at, created_at)
			VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(tenant_code, device_id) DO UPDATE SET
				user_id=excluded.user_id,
				device_model=excluded.device_model,
				os_version=excluded.os_version,
				app_version=excluded.app_version,
				push_token=excluded.push_token,
				last_active_at=excluded.last_active_at`,
			u.ID, tenantCode, d.DeviceID, d.DeviceModel, d.OSVersion, d.AppVersion, d.PushToken, now, now)
		return err
	})
}

// ListMobileDevices 获取当前租户绑定的移动设备清单
func (a *App) ListMobileDevices(u User, tenantCode string) ([]MobileDevice, error) {
	rows, err := a.DB.Query(`SELECT id, user_id, tenant_code, device_id, device_model, os_version, app_version, push_token, last_active_at, created_at FROM mobile_devices WHERE tenant_code=? ORDER BY last_active_at DESC`, tenantCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []MobileDevice
	for rows.Next() {
		var item MobileDevice
		if err := rows.Scan(&item.ID, &item.UserID, &item.TenantCode, &item.DeviceID, &item.DeviceModel, &item.OSVersion, &item.AppVersion, &item.PushToken, &item.LastActiveAt, &item.CreatedAt); err == nil {
			list = append(list, item)
		}
	}
	return list, nil
}

// GetMobileDashboard 移动端快速汇总看板
func (a *App) GetMobileDashboard(u User) (*MobileDashboard, error) {
	todayStr := today()
	dash := &MobileDashboard{
		TodaySalesAmount:   "0.00",
		TodayReceiptAmount: "0.00",
		TenantCode:         a.TenantCode,
		TenantName:         settingString(a.DB, "company_name"),
		UserName:           u.Name,
	}
	if dash.TenantName == "" {
		dash.TenantName = "睿链企业空间"
	}

	// 1. 今日销售单数与销售额
	var todaySalesTotal int64
	var todayOrdersCount int
	_ = a.DB.QueryRow(`SELECT COALESCE(SUM(total), 0), COUNT(*) FROM documents WHERE kind IN ('sales_order', 'sales_delivery') AND date=? AND status != 'cancelled'`, todayStr).
		Scan(&todaySalesTotal, &todayOrdersCount)
	dash.TodaySalesAmount = money(todaySalesTotal)
	dash.TodayOrdersCount = todayOrdersCount

	// 2. 今日收款总额
	var todayReceiptTotal int64
	_ = a.DB.QueryRow(`SELECT COALESCE(SUM(total), 0) FROM documents WHERE kind='ar_receipt' AND date=? AND status != 'cancelled'`, todayStr).
		Scan(&todayReceiptTotal)
	dash.TodayReceiptAmount = money(todayReceiptTotal)

	// 3. 待审批单据数
	var pendingCount int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM documents WHERE status='submitted'`).Scan(&pendingCount)
	dash.PendingApprovalCount = pendingCount

	// 4. 零库存/缺货预警品类数
	var lowStockCount int
	_ = a.DB.QueryRow(`SELECT COUNT(DISTINCT product_id) FROM inventory WHERE quantity <= 0`).Scan(&lowStockCount)
	dash.LowStockCount = lowStockCount

	return dash, nil
}

// ScanProductBarcode 条码/二维码/商品编码快速检索商品及多仓实时库存
func (a *App) ScanProductBarcode(u User, query string) (*MobileScanResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fail("条形码不能为空")
	}

	// 1. 查询商品档案（支持编码、名称、条码属性匹配）
	var m Master
	var dStr, createdAt string
	err := a.DB.QueryRow(`SELECT id, kind, code, name, data, active, created_at, updated_at FROM base_records
		WHERE kind='product' AND active=1 AND (code=? OR json_extract(data, '$.barcode')=? OR name=?) LIMIT 1`,
		query, query, query).
		Scan(&m.ID, &m.Kind, &m.Code, &m.Name, &dStr, &m.Active, &createdAt, &m.UpdatedAt)

	if err != nil {
		// 模糊宽容搜索
		err = a.DB.QueryRow(`SELECT id, kind, code, name, data, active, created_at, updated_at FROM base_records
			WHERE kind='product' AND active=1 AND (code LIKE ? OR json_extract(data, '$.barcode') LIKE ?) LIMIT 1`,
			"%"+query+"%", "%"+query+"%").
			Scan(&m.ID, &m.Kind, &m.Code, &m.Name, &dStr, &m.Active, &createdAt, &m.UpdatedAt)
		if err != nil {
			return nil, fail(fmt.Sprintf("未找到条码/编码为 [%s] 的有效商品", query))
		}
	}
	_ = json.Unmarshal([]byte(dStr), &m.Data)
	if m.Data == nil {
		m.Data = M{}
	}

	// 2. 查询所有仓库对应该商品的实时库存
	rows, err := a.DB.Query(`SELECT w.id, w.code, w.name, COALESCE(SUM(i.quantity), 0), COALESCE(SUM(i.reserved), 0)
		FROM base_records w
		LEFT JOIN inventory i ON i.warehouse_id=w.id AND i.product_id=?
		WHERE w.kind='warehouse' AND w.active=1
		GROUP BY w.id, w.code, w.name
		ORDER BY w.id ASC`, m.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var whList []MobileWarehouseStock
	var totalQ, totalR int64
	for rows.Next() {
		var whID int64
		var whCode, whName string
		var q, r int64
		if err := rows.Scan(&whID, &whCode, &whName, &q, &r); err == nil {
			avail := q - r
			if avail < 0 {
				avail = 0
			}
			whList = append(whList, MobileWarehouseStock{
				WarehouseID:   whID,
				WarehouseCode: whCode,
				WarehouseName: whName,
				Quantity:      qty(q),
				Reserved:      qty(r),
				Available:     qty(avail),
			})
			totalQ += q
			totalR += r
		}
	}

	totalAvail := totalQ - totalR
	if totalAvail < 0 {
		totalAvail = 0
	}

	defaultPrice := "0.00"
	if p, ok := m.Data["sale_price"]; ok && p != nil {
		defaultPrice = fmt.Sprint(p)
	}

	return &MobileScanResult{
		Product:        m,
		Warehouses:     whList,
		TotalQuantity:  qty(totalQ),
		TotalReserved:  qty(totalR),
		TotalAvailable: qty(totalAvail),
		DefaultPrice:   defaultPrice,
	}, nil
}

// QuickInventoryCheck 移动端快速盘点作业
func (a *App) QuickInventoryCheck(u User, ip string, warehouseID int64, items []QuickInventoryItem, note string, autoSubmit bool) (*Doc, error) {
	if warehouseID <= 0 {
		return nil, fail("请选择盘点仓库")
	}
	if len(items) == 0 {
		return nil, fail("盘点商品明细不能为空")
	}

	var whName string
	err := a.DB.QueryRow("SELECT name FROM base_records WHERE id=? AND kind='warehouse' AND active=1", warehouseID).Scan(&whName)
	if err != nil {
		return nil, fail("指定的盘点仓库不存在或已停用")
	}

	var docItems []ItemInput
	for _, it := range items {
		if it.ProductID <= 0 {
			continue
		}
		docItems = append(docItems, ItemInput{
			ProductID: it.ProductID,
			Batch:     it.Batch,
			Quantity:  it.CountedQuantity,
			Note:      it.Note,
		})
	}

	if len(docItems) == 0 {
		return nil, fail("未包含有效的盘点明细")
	}

	docInput := DocInput{
		Kind:        "inventory_check",
		WarehouseID: warehouseID,
		Date:        today(),
		Note:        strings.TrimSpace("移动扫码盘点: " + note),
		Items:       docItems,
	}

	var createdDoc Doc
	err = a.write(func(tx *sql.Tx) error {
		d, errSave := a.saveDocument(tx, u, ip, 0, docInput)
		if errSave != nil {
			return errSave
		}
		createdDoc = d
		if autoSubmit {
			dSub, errSub := a.actDocument(tx, u, ip, d.ID, "submit", "")
			if errSub == nil {
				createdDoc = dSub
				if u.Can(moduleOf(d.Kind) + ".approve") {
					if dAppr, errAppr := a.actDocument(tx, u, ip, d.ID, "approve", ""); errAppr == nil {
						createdDoc = dAppr
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &createdDoc, nil
}

// QuickOrder 移动端极速开单（支持现场销售订单 / 销售出库）
func (a *App) QuickOrder(u User, ip string, kind string, partnerID int64, warehouseID int64, items []QuickOrderItem, note string, autoSubmit bool) (*Doc, error) {
	if kind != "sales_order" && kind != "sales_delivery" {
		kind = "sales_delivery"
	}
	if partnerID <= 0 {
		return nil, fail("请选择客户")
	}
	if warehouseID <= 0 {
		return nil, fail("请选择发货/出库仓库")
	}
	if len(items) == 0 {
		return nil, fail("开单明细不能为空")
	}

	var docItems []ItemInput
	for _, it := range items {
		if it.ProductID <= 0 {
			continue
		}
		price := it.Price
		if price == "" || price == "0" {
			var pDataStr string
			err := a.DB.QueryRow("SELECT data FROM base_records WHERE id=? AND kind='product'", it.ProductID).Scan(&pDataStr)
			if err == nil {
				var pData M
				_ = json.Unmarshal([]byte(pDataStr), &pData)
				if p, ok := pData["sales_price"]; ok && p != nil {
					price = fmt.Sprint(p)
				}
			}
		}

		docItems = append(docItems, ItemInput{
			ProductID: it.ProductID,
			Batch:     it.Batch,
			Quantity:  it.Quantity,
			Price:     price,
			Note:      it.Note,
		})
	}

	if len(docItems) == 0 {
		return nil, fail("未包含有效的开单明细")
	}

	docInput := DocInput{
		Kind:        kind,
		PartnerID:   partnerID,
		WarehouseID: warehouseID,
		Date:        today(),
		Note:        strings.TrimSpace("移动开单: " + note),
		Items:       docItems,
	}

	var createdDoc Doc
	err := a.write(func(tx *sql.Tx) error {
		d, errSave := a.saveDocument(tx, u, ip, 0, docInput)
		if errSave != nil {
			return errSave
		}
		createdDoc = d
		if autoSubmit {
			dSub, errSub := a.actDocument(tx, u, ip, d.ID, "submit", "")
			if errSub == nil {
				createdDoc = dSub
				if u.Can(moduleOf(d.Kind) + ".approve") {
					if dAppr, errAppr := a.actDocument(tx, u, ip, d.ID, "approve", ""); errAppr == nil {
						createdDoc = dAppr
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &createdDoc, nil
}

// UploadMobileAttachment 移动端图片凭证与附件持久化上传
func (a *App) UploadMobileAttachment(u User, ip string, tenantCode string, fh *multipart.FileHeader) (M, error) {
	if fh == nil {
		return nil, fail("上传文件为空")
	}
	if fh.Size > 15*1024*1024 {
		return nil, fail("图片或附件体积不能超过 15 MB")
	}

	ext := strings.ToLower(filepath.Ext(fh.Filename))
	allowed := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true, ".pdf": true,
	}
	if !allowed[ext] {
		return nil, fail("仅支持上传 jpg, png, webp, gif, pdf 格式的凭证与照片")
	}

	subFolder := time.Now().Format("200601")
	dirPath := filepath.Join(a.DataDir, "uploads", tenantCode, subFolder)
	if err := os.MkdirAll(dirPath, 0700); err != nil {
		return nil, err
	}

	src, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()

	hasher := sha256.New()
	content, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	hasher.Write(content)
	hashStr := hex.EncodeToString(hasher.Sum(nil))

	newFilename := fmt.Sprintf("%s_%d%s", hashStr[:16], time.Now().Unix(), ext)
	targetPath := filepath.Join(dirPath, newFilename)

	if err := os.WriteFile(targetPath, content, 0644); err != nil {
		return nil, err
	}

	relURL := fmt.Sprintf("/api/mobile/uploads/%s/%s/%s", tenantCode, subFolder, newFilename)

	mimeType := "image/jpeg"
	if ext == ".png" {
		mimeType = "image/png"
	} else if ext == ".pdf" {
		mimeType = "application/pdf"
	}

	_ = a.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO attachments(tenant_code, user_id, filename, file_path, file_size, mime_type, sha256, created_at)
			VALUES(?,?,?,?,?,?,?,?)`,
			tenantCode, u.ID, fh.Filename, targetPath, fh.Size, mimeType, hashStr, stamp())
		return err
	})

	return M{
		"url":       relURL,
		"filename":  fh.Filename,
		"size":      fh.Size,
		"mime_type": mimeType,
		"sha256":    hashStr,
	}, nil
}

// SyncIncrementalProducts 移动端离线数据增量同步（商品）
func (a *App) SyncIncrementalProducts(since string) ([]Master, string, error) {
	nowStamp := stamp()
	var rows *sql.Rows
	var err error

	if since != "" {
		rows, err = a.DB.Query(`SELECT id, kind, code, name, data, active, created_at, updated_at FROM base_records WHERE kind='product' AND updated_at > ? ORDER BY id ASC`, since)
	} else {
		rows, err = a.DB.Query(`SELECT id, kind, code, name, data, active, created_at, updated_at FROM base_records WHERE kind='product' AND active=1 ORDER BY id ASC`)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var list []Master
	for rows.Next() {
		var m Master
		var d, createdAt string
		if err := rows.Scan(&m.ID, &m.Kind, &m.Code, &m.Name, &d, &m.Active, &createdAt, &m.UpdatedAt); err == nil {
			_ = json.Unmarshal([]byte(d), &m.Data)
			list = append(list, m)
		}
	}
	return list, nowStamp, nil
}

// SyncIncrementalCustomers 移动端离线数据增量同步（客户）
func (a *App) SyncIncrementalCustomers(since string) ([]Master, string, error) {
	nowStamp := stamp()
	var rows *sql.Rows
	var err error

	if since != "" {
		rows, err = a.DB.Query(`SELECT id, kind, code, name, data, active, created_at, updated_at FROM base_records WHERE kind='customer' AND updated_at > ? ORDER BY id ASC`, since)
	} else {
		rows, err = a.DB.Query(`SELECT id, kind, code, name, data, active, created_at, updated_at FROM base_records WHERE kind='customer' AND active=1 ORDER BY id ASC`)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var list []Master
	for rows.Next() {
		var m Master
		var d, createdAt string
		if err := rows.Scan(&m.ID, &m.Kind, &m.Code, &m.Name, &d, &m.Active, &createdAt, &m.UpdatedAt); err == nil {
			_ = json.Unmarshal([]byte(d), &m.Data)
			list = append(list, m)
		}
	}
	return list, nowStamp, nil
}
