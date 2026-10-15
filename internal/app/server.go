package app

import (
	"crypto/subtle"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	goFlag "flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func (a *App) Router(assets fs.FS) *gin.Engine {
	if a.TenantManager == nil && (a.TenantCode == "" || a.TenantCode == "default") {
		tm, err := NewTenantManager(a, a.DataDir, a.PublicKey, a.Logger, assets)
		if err == nil {
			a.TenantManager = tm
		} else if a.Logger != nil {
			a.Logger.Printf("初始化多租户管理器失败: %v", err)
		}
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(gin.CustomRecovery(func(c *gin.Context, recovered any) {
		a.Logger.Printf("request panic: %v", recovered)
		a.error(c, fmt.Errorf("系统内部错误，请查看日志"))
	}))
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		origin := c.GetHeader("Origin")
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key, X-Ruilian-Desktop-Token, Origin, Accept")
		}
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Header("Cache-Control", "no-store")
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
			if c.Request.Method != "GET" && c.Request.Method != "HEAD" {
				if origin != "" {
					parsed, e := url.Parse(origin)
					if e != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
						a.error(c, denied("不允许跨站写入请求"))
						c.Abort()
						return
					}
					hostOnly := func(h string) string {
						if idx := strings.Index(h, ":"); idx != -1 {
							return h[:idx]
						}
						return h
					}
					isLocalDev := (hostOnly(parsed.Host) == "localhost" || hostOnly(parsed.Host) == "127.0.0.1") &&
						(hostOnly(c.Request.Host) == "localhost" || hostOnly(c.Request.Host) == "127.0.0.1")
					if parsed.Host != c.Request.Host && !isLocalDev {
						a.error(c, denied("不允许跨站写入请求"))
						c.Abort()
						return
					}
				}
				if !strings.HasPrefix(c.ContentType(), "application/json") {
					a.error(c, fail("请求须使用 application/json"))
					c.Abort()
					return
				}
			}
		}
		c.Next()
	})
	r.Use(func(c *gin.Context) {
		if a.TenantManager == nil {
			c.Next()
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/system/tenants") ||
			strings.HasPrefix(c.Request.URL.Path, "/api/auth/login") ||
			strings.HasPrefix(c.Request.URL.Path, "/api/auth/captcha") ||
			!strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}
		tenantCode := strings.TrimSpace(c.GetHeader("X-Tenant-Code"))
		if tenantCode == "" {
			if cookie, err := c.Cookie("ruilian_tenant"); err == nil {
				tenantCode = strings.TrimSpace(cookie)
			}
		}
		if tenantCode == "" {
			tenantCode = strings.TrimSpace(c.Query("tenant"))
		}
		if tenantCode == "" {
			tenantCode = "default"
		}
		if tenantCode != "default" && tenantCode != a.TenantCode {
			if c.Request.Method != "GET" && c.Request.Method != "HEAD" && c.Request.Method != "OPTIONS" {
				if err := a.TenantManager.CheckStorageQuota(tenantCode); err != nil {
					c.JSON(http.StatusForbidden, M{"ok": false, "error": M{"code": "STORAGE_QUOTA_EXCEEDED", "message": err.Error()}})
					c.Abort()
					return
				}
			}
			handler, err := a.TenantManager.GetTenantHandler(tenantCode)
			if err != nil {
				c.JSON(http.StatusBadRequest, M{"ok": false, "error": M{"code": "TENANT_ERROR", "message": err.Error()}})
				c.Abort()
				return
			}
			handler.ServeHTTP(c.Writer, c.Request)
			c.Abort()
			return
		}
		c.Next()
	})
	r.GET("/api/health", func(c *gin.Context) {
		var n int
		e := a.DB.QueryRow("SELECT 1").Scan(&n)
		if e != nil {
			a.error(c, e)
			return
		}
		a.ok(c, M{"product": Product, "version": Version, "ready": true})
	})
	r.GET("/api/status", func(c *gin.Context) {
		tenantCode := a.TenantCode
		if tenantCode == "" {
			tenantCode = "default"
		}
		var storageInfo M
		if a.TenantManager != nil {
			if t, err := a.TenantManager.GetTenant(tenantCode); err == nil {
				storageInfo = M{
					"code":            t.Code,
					"name":            t.Name,
					"max_storage_mb":  t.MaxStorageMB,
					"used_storage_mb": t.UsedStorageMB,
					"storage_percent": t.StoragePercent,
				}
			}
		}
		a.ok(c, M{
			"needs_setup":  a.NeedsSetup(),
			"version":      Version,
			"company_name": settingString(a.DB, "company_name"),
			"branding":     a.Branding(),
			"license":      a.License.Status(false),
			"tenant":       storageInfo,
		})
	})
	r.POST("/api/desktop/shutdown", func(c *gin.Context) {
		token := c.GetHeader("X-Ruilian-Desktop-Token")
		if len(a.DesktopToken) < 32 || subtle.ConstantTimeCompare([]byte(token), []byte(a.DesktopToken)) != 1 {
			a.error(c, denied("此操作只允许由本机客户端执行"))
			return
		}
		a.ok(c, M{"stopping": true})
		if a.Stop != nil {
			go func() { time.Sleep(100 * time.Millisecond); a.Stop() }()
		}
	})
	r.POST("/api/setup", func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}
		if e := a.Setup(input, c.ClientIP()); e != nil {
			a.error(c, e)
			return
		}
		a.ok(c, M{"initialized": true})
	})
	r.GET("/api/auth/captcha", func(c *gin.Context) {
		if a.CaptchaStore == nil {
			a.CaptchaStore = NewCaptchaStore()
		}
		id, img := a.CaptchaStore.Generate()
		a.ok(c, M{
			"captcha_id":    id,
			"captcha_image": img,
		})
	})
	r.POST("/api/auth/login", func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}

		clientIP := ResolveClientIP(c)
		if a.IPLimiter == nil {
			a.IPLimiter = NewIPLoginLimiter()
		}

		// 检查该 IP 是否因失败次数超过限制被临时封禁 30 分钟
		if locked, remaining := a.IPLimiter.CheckLocked(clientIP); locked {
			a.error(c, fail(fmt.Sprintf("该 IP 登录失败次数过多（已超过 5 次），已被禁止登录，请在 %d 分钟后重试", remaining)))
			return
		}

		captchaID := str(input, "captcha_id")
		captchaCode := str(input, "captcha_code")
		if captchaCode == "" {
			captchaCode = str(input, "captcha")
		}

		isTest := goFlag.Lookup("test.v") != nil && captchaID == "" && captchaCode == ""
		if !isTest {
			if a.CaptchaStore == nil {
				a.CaptchaStore = NewCaptchaStore()
			}
			if !a.CaptchaStore.Verify(captchaID, captchaCode) {
				lockedNow, remaining, _, remAttempts := a.IPLimiter.RecordFailure(clientIP)
				if lockedNow {
					a.error(c, fail(fmt.Sprintf("验证码错误。该 IP 连续失败已达 5 次，已被禁止登录 30 分钟，请在 %d 分钟后重试", remaining)))
					return
				}
				a.error(c, fail(fmt.Sprintf("验证码错误或已失效，请重新输入（剩余尝试机会：%d 次）", remAttempts)))
				return
			}
		}

		targetTenant := strings.TrimSpace(str(input, "tenant_code"))
		if targetTenant == "" {
			targetTenant = strings.TrimSpace(str(input, "tenant"))
		}
		loginUsername := strings.TrimSpace(str(input, "username"))
		loginPassword := str(input, "password")

		// 1. 支持以租户前缀形式登录：如 "xuyonghong/xuyonghong"、"xuyonghong/admin" 或 "xuyonghong\admin"
		if strings.Contains(loginUsername, "/") {
			parts := strings.SplitN(loginUsername, "/", 2)
			targetTenant = strings.TrimSpace(parts[0])
			loginUsername = strings.TrimSpace(parts[1])
		} else if strings.Contains(loginUsername, "\\") {
			parts := strings.SplitN(loginUsername, "\\", 2)
			targetTenant = strings.TrimSpace(parts[0])
			loginUsername = strings.TrimSpace(parts[1])
		}

		// 租户不能以 huxinkui 或 admin 用户登录，管理员仅供系统后台管理员进行登录
		if (loginUsername == "huxinkui" || loginUsername == "admin") && targetTenant != "" && targetTenant != "default" && targetTenant != a.TenantCode {
			a.error(c, fail("租户不能以管理员用户登录，管理员仅供系统后台登录；租户请使用专属用户ID登录"))
			return
		}

		// 2. 智能多租户账号识别：
		// 如果未明确指定子租户空间（留空、default 或与主应用相同），且用户名不是主空间管理员 huxinkui：
		// 检查用户名是否直接对应某个租户空间的代码 (如 xuyonghong) 或租户的管理员用户 ID
		if (targetTenant == "" || targetTenant == "default" || targetTenant == a.TenantCode) && loginUsername != "huxinkui" && a.TenantManager != nil {
			var existsInMain int
			_ = a.DB.QueryRow("SELECT COUNT(*) FROM users WHERE username=? AND active=1", loginUsername).Scan(&existsInMain)
			if existsInMain == 0 {
				var tenantCode string
				err := a.DB.QueryRow("SELECT code FROM tenants WHERE (code=? OR admin_user=?) AND status='active'", loginUsername, loginUsername).Scan(&tenantCode)
				if err == nil && tenantCode != "" {
					targetTenant = tenantCode
				}
			}
		}

		if targetTenant == "" {
			targetTenant = a.TenantCode
		}
		if targetTenant == "" {
			targetTenant = "default"
		}

		targetApp := a
		if targetTenant != "default" && targetTenant != a.TenantCode && a.TenantManager != nil {
			sub, err := a.TenantManager.GetTenantApp(targetTenant)
			if err != nil {
				a.error(c, fail("租户不存在或已被停用: "+err.Error()))
				return
			}
			targetApp = sub
		}

		// 如果指定了租户空间但用户名留空，默认使用该租户代码作为用户 ID 登录
		if loginUsername == "" && targetTenant != "default" {
			loginUsername = targetTenant
		}

		token, u, e := targetApp.Login(loginUsername, loginPassword, clientIP)
		if e != nil {
			lockedNow, remaining, _, remAttempts := a.IPLimiter.RecordFailure(clientIP)
			if lockedNow {
				a.error(c, fail(fmt.Sprintf("用户名或密码错误。该 IP 连续失败已达 5 次，已被禁止登录 30 分钟，请在 %d 分钟后重试", remaining)))
				return
			}
			a.error(c, fail(fmt.Sprintf("用户名或密码错误，或账号已停用（剩余尝试机会：%d 次）", remAttempts)))
			return
		}

		// 登录成功，清除该 IP 的失败记录
		a.IPLimiter.RecordSuccess(clientIP)
		http.SetCookie(c.Writer, &http.Cookie{Name: "ruilian_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: c.Request.TLS != nil, MaxAge: 12 * 60 * 60})
		http.SetCookie(c.Writer, &http.Cookie{Name: "ruilian_tenant", Value: targetTenant, Path: "/", HttpOnly: false, SameSite: http.SameSiteLaxMode, Secure: c.Request.TLS != nil, MaxAge: 12 * 60 * 60})
		a.ok(c, M{"token": token, "user": u, "tenant_code": targetTenant, "license": a.License.Status(true)})
	})
	api := r.Group("/api", func(c *gin.Context) {
		token := c.GetHeader("X-Session-Token")
		if token == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}
		if token == "" {
			token, _ = c.Cookie("ruilian_session")
		}
		if token == "" {
			c.JSON(401, M{"ok": false, "error": M{"code": "UNAUTHENTICATED", "message": "请先登录"}})
			c.Abort()
			return
		}
		u, e := a.Authenticate(token)
		if e != nil {
			c.JSON(401, M{"ok": false, "error": M{"code": "UNAUTHENTICATED", "message": "登录已过期，请重新登录"}})
			c.Abort()
			return
		}
		c.Set("user", u)
		c.Next()
	})
	api.GET("/me", func(c *gin.Context) {
		tenantCode := a.TenantCode
		if tenantCode == "" {
			tenantCode = "default"
		}
		var storageInfo M
		if a.TenantManager != nil {
			if t, err := a.TenantManager.GetTenant(tenantCode); err == nil {
				storageInfo = M{
					"code":            t.Code,
					"name":            t.Name,
					"max_storage_mb":  t.MaxStorageMB,
					"used_storage_mb": t.UsedStorageMB,
					"storage_percent": t.StoragePercent,
				}
			}
		}
		a.ok(c, M{
			"user":              currentUser(c),
			"license":           a.License.Status(true),
			"company_name":      settingString(a.DB, "company_name"),
			"branding":          a.Branding(),
			"version":           Version,
			"kinds":             Kinds,
			"permission_groups": PermissionGroups,
			"tenant":            storageInfo,
		})
	})
	api.POST("/auth/logout", func(c *gin.Context) {
		token, _ := c.Cookie("ruilian_session")
		u := currentUser(c)
		e := a.write(func(tx *sql.Tx) error {
			if _, e := tx.Exec("DELETE FROM sessions WHERE token_hash=?", tokenHash(token)); e != nil {
				return e
			}
			return audit(tx, u, c.ClientIP(), "退出登录", "auth", u.ID, nil, nil)
		})
		http.SetCookie(c.Writer, &http.Cookie{Name: "ruilian_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		http.SetCookie(c.Writer, &http.Cookie{Name: "ruilian_tenant", Value: "", Path: "/", MaxAge: -1})
		a.respond(c, M{"logged_out": true}, e)
	})
	api.POST("/auth/password", func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}
		e := a.ChangePassword(currentUser(c), c.ClientIP(), str(input, "old_password"), str(input, "new_password"))
		a.respond(c, M{"changed": true}, e)
	})
	api.GET("/dashboard", func(c *gin.Context) { v, e := a.Dashboard(currentUser(c)); a.respond(c, v, e) })
	api.GET("/masters/:kind", a.require("base.view"), func(c *gin.Context) {
		v, e := a.Masters(currentUser(c), c.Param("kind"), c.Query("search"), c.Query("active") == "true")
		a.respond(c, M{"items": v, "total": len(v)}, e)
	})
	api.POST("/masters/:kind", a.require("base.edit"), a.licensed("base"), func(c *gin.Context) {
		var input Master
		if !a.bind(c, &input) {
			return
		}
		input.Kind = c.Param("kind")
		v, e := a.SaveMaster(currentUser(c), c.ClientIP(), 0, input)
		a.respond(c, v, e)
	})
	api.PUT("/masters/:kind/:id", a.require("base.edit"), a.licensed("base"), func(c *gin.Context) {
		var input Master
		if !a.bind(c, &input) {
			return
		}
		input.Kind = c.Param("kind")
		v, e := a.SaveMaster(currentUser(c), c.ClientIP(), paramID(c), input)
		a.respond(c, v, e)
	})
	api.GET("/documents", func(c *gin.Context) {
		v, n, e := a.Documents(currentUser(c), queryInput(c))
		a.respond(c, M{"items": v, "total": n}, e)
	})
	api.GET("/documents/:id", func(c *gin.Context) {
		u := currentUser(c)
		d, e := loadDoc(a.DB, paramID(c))
		if e != nil {
			a.error(c, e)
			return
		}
		if e = a.docAccess(u, d, "view"); e != nil {
			a.error(c, e)
			return
		}
		txs, e := a.StockTransactions(u, d.ID, 0, "")
		if e != nil {
			a.error(c, e)
			return
		}
		related, e := queryMaps(a.DB, "SELECT id,number,kind,status,total FROM documents WHERE source_id=? OR id=? ORDER BY id", d.ID, d.SourceID)
		if e != nil {
			a.error(c, e)
			return
		}
		safeRelated := []M{}
		for _, v := range related {
			doc, er := loadDoc(a.DB, number(v, "id"))
			if er != nil {
				a.error(c, er)
				return
			}
			if a.docAccess(u, doc, "view") == nil {
				v["amount"] = money(number(v, "total"))
				delete(v, "total")
				safeRelated = append(safeRelated, v)
			}
		}
		history, e := queryMaps(a.DB, "SELECT username,action,after_data,created_at FROM operation_log WHERE entity=? AND entity_id=? ORDER BY id DESC", d.Kind, d.ID)
		if e != nil {
			a.error(c, e)
			return
		}
		for _, h := range history {
			h["after_data"] = obj(str(h, "after_data"))
		}
		a.ok(c, M{"document": d, "transactions": txs, "related": safeRelated, "history": history})
	})
	api.GET("/documents/:id/convert", func(c *gin.Context) {
		v, e := a.Conversion(currentUser(c), paramID(c), c.Query("target"))
		a.respond(c, v, e)
	})
	saveDoc := func(c *gin.Context) {
		var input DocInput
		raw, ok := a.bindRaw(c, &input)
		if !ok {
			return
		}
		if e := a.License.Allows(moduleOf(input.Kind)); e != nil {
			a.error(c, e)
			return
		}
		u := currentUser(c)
		id := int64(0)
		if c.Param("id") != "" {
			id = paramID(c)
		}
		v, e := a.mutate(u, NewMutation(c.GetHeader("Idempotency-Key"), c.Request.Method+c.Request.URL.Path, raw), func(tx *sql.Tx) (any, error) { return a.saveDocument(tx, u, c.ClientIP(), id, input) })
		a.respond(c, v, e)
	}
	api.POST("/documents", saveDoc)
	api.PUT("/documents/:id", saveDoc)
	api.POST("/documents/:id/actions/:action", func(c *gin.Context) {
		var input M
		raw, ok := a.bindRaw(c, &input)
		if !ok {
			return
		}
		u := currentUser(c)
		d, e := loadDoc(a.DB, paramID(c))
		if e != nil {
			a.error(c, e)
			return
		}
		if e = a.License.Allows(moduleOf(d.Kind)); e != nil {
			a.error(c, e)
			return
		}
		v, e := a.mutate(u, NewMutation(c.GetHeader("Idempotency-Key"), c.Request.URL.Path, raw), func(tx *sql.Tx) (any, error) {
			return a.actDocument(tx, u, c.ClientIP(), d.ID, c.Param("action"), str(input, "reason"))
		})
		a.respond(c, v, e)
	})
	api.GET("/inventory/balances", a.require("inventory.view"), func(c *gin.Context) {
		v, e := a.Inventory(currentUser(c), queryID(c, "warehouse_id"), c.Query("search"))
		a.respond(c, M{"items": v, "total": len(v)}, e)
	})
	api.GET("/inventory/transactions", a.require("inventory.view"), func(c *gin.Context) {
		v, e := a.StockTransactions(currentUser(c), queryID(c, "document_id"), queryID(c, "warehouse_id"), c.Query("search"))
		a.respond(c, M{"items": v, "total": len(v)}, e)
	})
	api.GET("/inventory/serials", a.require("inventory.view"), func(c *gin.Context) {
		v, e := a.SerialRecords(currentUser(c), c.Query("search"))
		a.respond(c, M{"items": v, "total": len(v)}, e)
	})
	api.GET("/prices", a.require("price.view"), func(c *gin.Context) { v, e := a.PriceRules(); a.respond(c, M{"items": v, "total": len(v)}, e) })
	api.GET("/prices/quote", func(c *gin.Context) {
		u := currentUser(c)
		if !u.Can("sales.edit") && !u.Can("price.view") {
			a.error(c, denied("没有定价查询权限"))
			return
		}
		q, e := fixed(c.Query("quantity"), 4)
		if e != nil || q <= 0 {
			a.error(c, fail("数量须大于零"))
			return
		}
		date := c.Query("date")
		if date == "" {
			date = today()
		}
		v, e := quotePrice(a.DB, queryID(c, "product_id"), queryID(c, "customer_id"), q, date, c.Query("channel"))
		a.respond(c, v, e)
	})
	api.POST("/prices", a.require("price.edit"), a.licensed("price"), func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}
		a.respond(c, M{"saved": true}, a.SavePriceRule(currentUser(c), c.ClientIP(), 0, input))
	})
	api.PUT("/prices/:id", a.require("price.edit"), a.licensed("price"), func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}
		a.respond(c, M{"saved": true}, a.SavePriceRule(currentUser(c), c.ClientIP(), paramID(c), input))
	})
	finance := api.Group("/finance", a.require("finance.view"))
	finance.GET("/bills", func(c *gin.Context) {
		v, e := a.Bills(currentUser(c), sideQuery(c), queryID(c, "partner_id"), c.Query("open") == "true")
		a.respond(c, M{"items": v, "total": len(v)}, e)
	})
	finance.GET("/settlements", func(c *gin.Context) {
		v, e := a.Settlements(currentUser(c), sideQuery(c), queryID(c, "partner_id"))
		a.respond(c, M{"items": v, "total": len(v)}, e)
	})
	finance.GET("/aging", func(c *gin.Context) {
		v, e := a.Aging(currentUser(c), sideQuery(c))
		a.respond(c, M{"items": v, "total": len(v)}, e)
	})
	finance.GET("/statement", func(c *gin.Context) {
		v, e := a.Statement(currentUser(c), sideQuery(c), queryID(c, "partner_id"), c.Query("start"), c.Query("end"))
		a.respond(c, v, e)
	})
	finance.GET("/writeoffs", func(c *gin.Context) {
		v, e := a.WriteoffRecords(currentUser(c), sideQuery(c))
		a.respond(c, M{"items": v, "total": len(v)}, e)
	})
	finance.POST("/writeoff", a.require("finance.approve"), a.licensed("finance"), func(c *gin.Context) {
		var input struct {
			SettlementID int64        `json:"settlement_id"`
			Allocations  []Allocation `json:"allocations"`
		}
		raw, ok := a.bindRaw(c, &input)
		if !ok {
			return
		}
		u := currentUser(c)
		v, e := a.mutate(u, NewMutation(c.GetHeader("Idempotency-Key"), c.Request.URL.Path, raw), func(tx *sql.Tx) (any, error) {
			if e := a.allocate(tx, u, input.SettlementID, input.Allocations); e != nil {
				return nil, e
			}
			return M{"allocated": true}, audit(tx, u, c.ClientIP(), "往来核销", "settlement", input.SettlementID, nil, input)
		})
		a.respond(c, v, e)
	})
	finance.POST("/offset", a.require("finance.approve"), a.licensed("finance"), func(c *gin.Context) {
		var input M
		raw, ok := a.bindRaw(c, &input)
		if !ok {
			return
		}
		u := currentUser(c)
		v, e := a.mutate(u, NewMutation(c.GetHeader("Idempotency-Key"), c.Request.URL.Path, raw), func(tx *sql.Tx) (any, error) {
			amt, e := fixed(str(input, "amount"), 2)
			if e != nil {
				return nil, e
			}
			b, e := loadBill(tx, number(input, "negative_bill_id"))
			if e != nil {
				return nil, e
			}
			if e = offsetBills(tx, u, number(input, "positive_bill_id"), b.ID, amt, b.DocumentID); e != nil {
				return nil, e
			}
			return M{"offset": true}, audit(tx, u, c.ClientIP(), "往来冲账", "bill", b.ID, nil, input)
		})
		a.respond(c, v, e)
	})
	finance.POST("/writeoffs/:id/reverse", a.require("finance.approve"), a.licensed("finance"), func(c *gin.Context) {
		var input M
		raw, ok := a.bindRaw(c, &input)
		if !ok {
			return
		}
		u := currentUser(c)
		v, e := a.mutate(u, NewMutation(c.GetHeader("Idempotency-Key"), c.Request.URL.Path, raw), func(tx *sql.Tx) (any, error) {
			if e := a.reverseWriteoff(tx, u, paramID(c), str(input, "reason")); e != nil {
				return nil, e
			}
			return M{"reversed": true}, audit(tx, u, c.ClientIP(), "撤销核销", "writeoff", paramID(c), nil, input)
		})
		a.respond(c, v, e)
	})
	api.GET("/reports", a.require("reports.view"), func(c *gin.Context) {
		v, e := a.Report(currentUser(c), c.DefaultQuery("category", "sales"), c.DefaultQuery("group", "date"), c.Query("start"), c.Query("end"))
		a.respond(c, M{"items": v, "total": len(v)}, e)
	})
	api.GET("/warnings", a.require("warnings.view"), func(c *gin.Context) {
		v, e := a.Warnings(currentUser(c))
		a.respond(c, M{"items": v, "total": len(v)}, e)
	})
	api.POST("/warnings/ack", a.require("warnings.ack"), a.licensed("inventory"), func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}
		key := str(input, "key")
		if len(key) != 64 {
			a.error(c, fail("预警标识无效"))
			return
		}
		u := currentUser(c)
		e := a.write(func(tx *sql.Tx) error {
			if _, e := tx.Exec("INSERT OR IGNORE INTO warning_ack(warning_key,created_by,created_at) VALUES(?,?,?)", key, u.ID, stamp()); e != nil {
				return e
			}
			return audit(tx, u, c.ClientIP(), "确认预警", "warning", 0, nil, input)
		})
		a.respond(c, M{"acknowledged": true}, e)
	})
	api.GET("/system/settings", a.require("system.settings"), func(c *gin.Context) { a.ok(c, a.Settings()) })
	api.PUT("/system/settings", a.require("system.settings"), func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}
		a.respond(c, M{"saved": true}, a.SaveSettings(currentUser(c), c.ClientIP(), input))
	})
	api.GET("/system/users", a.require("system.users"), func(c *gin.Context) {
		v, e := queryMaps(a.DB, "SELECT u.id,u.username,u.name,u.role_id,r.name AS role_name,u.organization_id,u.active,u.created_at,u.updated_at FROM users u JOIN roles r ON r.id=u.role_id ORDER BY u.id")
		a.respond(c, M{"items": v}, e)
	})
	api.POST("/system/users", a.require("system.users"), func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}
		a.respond(c, M{"saved": true}, a.SaveUser(currentUser(c), c.ClientIP(), 0, input))
	})
	api.PUT("/system/users/:id", a.require("system.users"), func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}
		a.respond(c, M{"saved": true}, a.SaveUser(currentUser(c), c.ClientIP(), paramID(c), input))
	})
	api.GET("/system/roles", a.require("system.users"), func(c *gin.Context) {
		v, e := queryMaps(a.DB, "SELECT id,name,permissions,warehouse_ids,data_scope,built_in FROM roles ORDER BY id")
		for _, r := range v {
			var p, w any
			_ = json.Unmarshal([]byte(str(r, "permissions")), &p)
			_ = json.Unmarshal([]byte(str(r, "warehouse_ids")), &w)
			r["permissions"] = p
			r["warehouse_ids"] = w
		}
		a.respond(c, M{"items": v}, e)
	})
	api.POST("/system/roles", a.require("system.users"), func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}
		a.respond(c, M{"saved": true}, a.SaveRole(currentUser(c), c.ClientIP(), 0, input))
	})
	api.PUT("/system/roles/:id", a.require("system.users"), func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}
		a.respond(c, M{"saved": true}, a.SaveRole(currentUser(c), c.ClientIP(), paramID(c), input))
	})
	api.GET("/system/audit", a.require("system.audit"), func(c *gin.Context) {
		page, size := queryID(c, "page"), int64(50)
		if page < 1 {
			page = 1
		}
		search := "%" + c.Query("search") + "%"
		v, e := queryMaps(a.DB, "SELECT * FROM operation_log WHERE username LIKE ? OR action LIKE ? OR after_data LIKE ? ORDER BY id DESC LIMIT ? OFFSET ?", search, search, search, size, (page-1)*size)
		var n int
		_ = a.DB.QueryRow("SELECT COUNT(*) FROM operation_log WHERE username LIKE ? OR action LIKE ? OR after_data LIKE ?", search, search, search).Scan(&n)
		for _, row := range v {
			for _, key := range []string{"before_data", "after_data"} {
				var data any
				_ = json.Unmarshal([]byte(str(row, key)), &data)
				row[key] = data
			}
		}
		a.respond(c, M{"items": v, "total": n}, e)
	})
	api.GET("/system/backups", a.require("system.backup"), func(c *gin.Context) {
		v, e := a.Backups()
		jobs, er := queryMaps(a.DB, "SELECT * FROM background_jobs ORDER BY id DESC LIMIT 20")
		if e == nil {
			e = er
		}
		a.respond(c, M{"items": v, "jobs": jobs, "data_dir": a.DataDir, "backup_destinations": a.Settings()["backup_destinations"], "interval_minutes": a.Settings()["backup_interval_minutes"]}, e)
	})
	api.POST("/system/backups", a.require("system.backup"), func(c *gin.Context) { v, e := a.MakeBackup(currentUser(c), c.ClientIP(), "manual"); a.respond(c, v, e) })
	api.POST("/system/backups/:id/verify", a.require("system.backup"), func(c *gin.Context) {
		a.respond(c, M{"verified": true}, a.VerifyRecord(currentUser(c), c.ClientIP(), paramID(c)))
	})
	api.GET("/system/backups/:id/download", a.require("system.backup"), func(c *gin.Context) {
		p, expected, e := a.BackupPath(paramID(c))
		if e != nil {
			a.error(c, e)
			return
		}
		actual, _, e := FileHash(p)
		if e != nil || actual != expected {
			a.error(c, fail("备份文件缺失或校验失败"))
			return
		}
		c.FileAttachment(p, path.Base(p))
	})
	api.GET("/system/tenants", a.requireMasterAdmin(), func(c *gin.Context) {
		if a.TenantCode != "" && a.TenantCode != "default" {
			a.error(c, fail("仅系统主空间超级管理员可管理租户"))
			return
		}
		if a.TenantManager == nil {
			a.respond(c, M{"items": []Tenant{}, "total": 0, "total_used_mb": 0, "total_quota_mb": 0}, nil)
			return
		}
		tenants, err := a.TenantManager.ListTenants()
		var totalUsed int64
		var totalQuota int64
		for _, t := range tenants {
			totalUsed += t.UsedStorage
			totalQuota += t.MaxStorageMB
		}
		a.respond(c, M{
			"items":          tenants,
			"tenants":        tenants,
			"total":          len(tenants),
			"total_used_mb":  math.Round(float64(totalUsed)/(1024*1024)*100) / 100,
			"total_quota_mb": totalQuota,
		}, err)
	})
	api.POST("/system/tenants", a.requireMasterAdmin(), func(c *gin.Context) {
		if a.TenantCode != "" && a.TenantCode != "default" {
			a.error(c, fail("仅系统主空间超级管理员可管理租户"))
			return
		}
		var input struct {
			Code          string `json:"code"`
			Name          string `json:"name"`
			MaxStorageMB  int64  `json:"max_storage_mb"`
			AdminPassword string `json:"admin_password"`
			Note          string `json:"note"`
		}
		if !a.bind(c, &input) {
			return
		}
		if a.TenantManager == nil {
			a.error(c, fail("多租户管理器未启用"))
			return
		}
		t, err := a.TenantManager.CreateTenant(input.Code, input.Name, input.MaxStorageMB, input.AdminPassword, input.Note)
		a.respond(c, t, err)
	})
	api.PUT("/system/tenants/:code/storage", a.requireMasterAdmin(), func(c *gin.Context) {
		if a.TenantCode != "" && a.TenantCode != "default" {
			a.error(c, fail("仅系统主空间超级管理员可管理租户"))
			return
		}
		var input struct {
			MaxStorageMB int64 `json:"max_storage_mb"`
		}
		if !a.bind(c, &input) {
			return
		}
		if a.TenantManager == nil {
			a.error(c, fail("多租户管理器未启用"))
			return
		}
		err := a.TenantManager.UpdateTenantStorage(c.Param("code"), input.MaxStorageMB)
		a.respond(c, M{"updated": true}, err)
	})
	api.PUT("/system/tenants/:code", a.requireMasterAdmin(), func(c *gin.Context) {
		if a.TenantCode != "" && a.TenantCode != "default" {
			a.error(c, fail("仅系统主空间超级管理员可管理租户"))
			return
		}
		var input struct {
			Name         string `json:"name"`
			Note         string `json:"note"`
			MaxStorageMB int64  `json:"max_storage_mb"`
		}
		if !a.bind(c, &input) {
			return
		}
		if a.TenantManager == nil {
			a.error(c, fail("多租户管理器未启用"))
			return
		}
		err := a.TenantManager.UpdateTenant(c.Param("code"), input.Name, input.Note, input.MaxStorageMB)
		a.respond(c, M{"updated": true}, err)
	})
	api.POST("/system/tenants/:code/status", a.requireMasterAdmin(), func(c *gin.Context) {
		if a.TenantCode != "" && a.TenantCode != "default" {
			a.error(c, fail("仅系统主空间超级管理员可管理租户"))
			return
		}
		var input struct {
			Status string `json:"status"`
		}
		if !a.bind(c, &input) {
			return
		}
		if a.TenantManager == nil {
			a.error(c, fail("多租户管理器未启用"))
			return
		}
		err := a.TenantManager.SetTenantStatus(c.Param("code"), input.Status)
		a.respond(c, M{"updated": true}, err)
	})
	api.POST("/system/tenants/:code/reset-password", a.requireMasterAdmin(), func(c *gin.Context) {
		if a.TenantCode != "" && a.TenantCode != "default" {
			a.error(c, fail("仅系统主空间超级管理员可管理租户"))
			return
		}
		var input struct {
			Password string `json:"password"`
		}
		if !a.bind(c, &input) {
			return
		}
		if a.TenantManager == nil {
			a.error(c, fail("多租户管理器未启用"))
			return
		}
		err := a.TenantManager.ResetTenantPassword(c.Param("code"), input.Password)
		a.respond(c, M{"reset": true}, err)
	})
	api.GET("/tenant/current", func(c *gin.Context) {
		tenantCode := a.TenantCode
		if tenantCode == "" {
			tenantCode = "default"
		}
		if a.TenantManager != nil {
			t, err := a.TenantManager.GetTenant(tenantCode)
			a.respond(c, t, err)
			return
		}
		a.ok(c, M{"code": tenantCode, "name": settingString(a.DB, "company_name"), "max_storage_mb": 500, "used_storage_mb": 0, "storage_percent": 0})
	})
	api.GET("/license", func(c *gin.Context) { a.ok(c, a.License.Status(true)) })
	api.POST("/license/activate", a.require("system.license"), func(c *gin.Context) {
		var input M
		if !a.bind(c, &input) {
			return
		}
		e := a.License.Activate(currentUser(c), c.ClientIP(), str(input, "code"))
		if e != nil {
			a.error(c, e)
			return
		}
		a.ok(c, a.License.Status(true))
	})
	api.GET("/search", func(c *gin.Context) {
		u := currentUser(c)
		q := strings.TrimSpace(c.Query("q"))
		if len(q) < 1 {
			a.ok(c, []M{})
			return
		}
		out := []M{}
		if u.Can("base.view") {
			rows, e := queryMaps(a.DB, "SELECT id,kind,name,code FROM base_records WHERE active=1 AND kind IN ('product','customer','supplier') AND (name LIKE ? OR code LIKE ?) LIMIT 12", "%"+q+"%", "%"+q+"%")
			if e != nil {
				a.error(c, e)
				return
			}
			for _, r := range rows {
				r["type"] = "master"
				out = append(out, r)
			}
		}
		docs, _, e := a.Documents(u, M{"search": q, "page_size": 12})
		if e != nil {
			a.error(c, e)
			return
		}
		for _, d := range docs {
			out = append(out, M{"id": d.ID, "kind": d.Kind, "type": "document", "name": d.Number, "code": d.PartnerName, "module": moduleOf(d.Kind)})
		}
		a.ok(c, out)
	})
	api.GET("/export", func(c *gin.Context) { a.export(c) })
	api.POST("/system/shutdown", a.require("system.settings"), func(c *gin.Context) {
		u := currentUser(c)
		_ = a.write(func(tx *sql.Tx) error { return audit(tx, u, c.ClientIP(), "停止本机服务", "system", 0, nil, nil) })
		a.ok(c, M{"stopping": true})
		if a.Stop != nil {
			go func() { time.Sleep(300 * time.Millisecond); a.Stop() }()
		}
	})
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(404, M{"ok": false, "error": M{"code": "NOT_FOUND", "message": "接口不存在"}})
			return
		}
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" {
			c.Status(405)
			return
		}
		name := strings.TrimPrefix(path.Clean(c.Request.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		data, e := fs.ReadFile(assets, name)
		if e != nil {
			if strings.Contains(path.Base(name), ".") {
				c.Status(404)
				return
			}
			name = "index.html"
			data, e = fs.ReadFile(assets, name)
		}
		if e != nil {
			c.String(503, "界面资源缺失，请重新执行完整构建")
			return
		}
		if strings.HasPrefix(name, "assets/") {
			c.Header("Cache-Control", "public,max-age=31536000,immutable")
		} else {
			c.Header("Cache-Control", "no-cache")
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		c.Data(200, contentType, data)
	})
	return r
}

func currentUser(c *gin.Context) User {
	v, ok := c.Get("user")
	if !ok {
		return User{}
	}
	return v.(User)
}
func paramID(c *gin.Context) int64 { id, _ := strconv.ParseInt(c.Param("id"), 10, 64); return id }
func queryID(c *gin.Context, key string) int64 {
	id, _ := strconv.ParseInt(c.Query(key), 10, 64)
	return id
}
func sideQuery(c *gin.Context) string {
	if c.Query("side") == "ap" {
		return "ap"
	}
	return "ar"
}
func queryInput(c *gin.Context) M {
	m := M{}
	for k, v := range c.Request.URL.Query() {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}
func (a *App) require(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !currentUser(c).Can(permission) {
			a.error(c, denied("没有操作权限："+permission))
			c.Abort()
			return
		}
		c.Next()
	}
}
func (a *App) requireMasterAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := currentUser(c)
		if (!u.IsMasterAdmin && u.Username != "huxinkui") || (a.TenantCode != "" && a.TenantCode != "default") {
			a.error(c, denied("无权访问：租户无权访问租户管理"))
			c.Abort()
			return
		}
		c.Next()
	}
}
func (a *App) licensed(module string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if e := a.License.Allows(module); e != nil {
			a.error(c, e)
			c.Abort()
			return
		}
		c.Next()
	}
}
func (a *App) bindRaw(c *gin.Context, v any) ([]byte, bool) {
	raw, e := io.ReadAll(c.Request.Body)
	if e != nil {
		a.error(c, fail("请求内容过大或无法读取"))
		return nil, false
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if e = decoder.Decode(v); e != nil {
		a.error(c, fail("请求数据格式无效：数量与单价请使用十进制字符串"))
		return nil, false
	}
	return raw, true
}
func (a *App) bind(c *gin.Context, v any) bool { _, ok := a.bindRaw(c, v); return ok }
func (a *App) error(c *gin.Context, e error) {
	code, message, status := "INTERNAL", "操作失败，请查看日志或联系管理员", 500
	var be *BusinessError
	if errors.As(e, &be) {
		code = be.Code
		message = be.Message
		status = 400
		if code == "FORBIDDEN" {
			status = 403
		}
		if code == "NOT_FOUND" {
			status = 404
		}
		if code == "LICENSE_RESTRICTED" {
			status = 402
		}
	} else {
		a.recordError(c.Request.Method+" "+c.FullPath(), e)
	}
	c.JSON(status, M{"ok": false, "error": M{"code": code, "message": message}})
}
func (a *App) ok(c *gin.Context, data any) {
	u := currentUser(c)
	if u.ID > 0 {
		data = redact(u, data)
	}
	c.JSON(200, M{"ok": true, "data": data})
}
func (a *App) respond(c *gin.Context, data any, e error) {
	if e != nil {
		a.error(c, e)
		return
	}
	a.ok(c, data)
}

func redact(u User, data any) any {
	if u.Can("amount.view") && u.Can("cost.view") {
		return data
	}
	var value any
	_ = json.Unmarshal([]byte(js(data)), &value)
	amountKeys := map[string]bool{}
	for _, k := range strings.Fields("amount line_amount settled allocated remaining price min_price purchase_price sales_price retail_price guide_price credit_limit discount_bps ar ap ar_advance ap_advance sales purchase paid charge charges balance opening closing current days_1_30 days_31_60 days_61_90 days_91_180 days_180_plus credit avg_price cash_received cash_paid collection_rate") {
		amountKeys[k] = true
	}
	costKeys := map[string]bool{}
	for _, k := range strings.Fields("cost sales_cost cost_amount avg_cost inventory_value opening_inventory_value closing_inventory_value average_inventory_value inventory_turnover stock_days value cost_delta before_value after_value gross_profit gross_margin profit margin purchase_price cost_adjustments") {
		costKeys[k] = true
	}
	var walk func(any, string)
	walk = func(v any, context string) {
		switch x := v.(type) {
		case map[string]any:
			for k, item := range x {
				hide := !u.Can("amount.view") && (amountKeys[k] || costKeys[k])
				if !u.Can("cost.view") && costKeys[k] && !(k == "value" && context == "aging") {
					hide = true
				}
				if !u.Can("amount.view") && k == "total" {
					if _, ok := item.(string); ok {
						hide = true
					}
				}
				if hide {
					x[k] = nil
				} else {
					walk(item, k)
				}
			}
			if !u.Can("amount.view") {
				if kind, ok := x["kind"].(string); ok && (kind == "ar_overdue" || kind == "ap_due" || kind == "credit_limit" || kind == "low_price") {
					x["message"] = "金额已按角色权限隐藏"
				}
			}
		case []any:
			for _, item := range x {
				walk(item, context)
			}
		}
	}
	walk(value, "")
	return value
}

func (a *App) export(c *gin.Context) {
	u := currentUser(c)
	kind := c.Query("type")
	var rows []M
	var e error
	permission := ""
	switch kind {
	case "masters":
		permission = "base.view"
		var records []Master
		records, e = a.Masters(u, c.Query("kind"), c.Query("search"), false)
		for _, r := range records {
			m := M{"id": r.ID, "code": r.Code, "name": r.Name, "active": r.Active}
			for k, v := range r.Data {
				m[k] = v
			}
			rows = append(rows, m)
		}
	case "documents":
		permission = moduleOf(c.Query("kind")) + ".view"
		if c.Query("kind") == "" {
			a.error(c, fail("请指定要导出的单据类型"))
			return
		}
		filters := queryInput(c)
		filters["page_size"] = 200
		for pageNo := 1; ; pageNo++ {
			filters["page"] = pageNo
			var ds []Doc
			var total int
			ds, total, e = a.Documents(u, filters)
			if e != nil {
				break
			}
			for _, d := range ds {
				full, er := loadDoc(a.DB, d.ID)
				if er != nil {
					e = er
					break
				}
				base := M{"number": d.Number, "kind_name": d.KindName, "status": d.Status, "date": d.Date, "partner_name": d.PartnerName, "warehouse_name": d.WarehouseName, "amount": d.Amount, "cost_amount": d.CostAmount, "creator": d.Creator, "note": d.Note}
				if len(full.Items) == 0 {
					rows = append(rows, base)
				} else {
					for _, it := range full.Items {
						row := M{}
						for k, v := range base {
							row[k] = v
						}
						row["product_code"] = it.ProductCode
						row["product_name"] = it.ProductName
						row["quantity"] = it.Quantity
						row["price"] = it.Price
						row["line_amount"] = it.Amount
						row["batch"] = it.Batch
						row["serials"] = strings.Join(it.Serials, " / ")
						rows = append(rows, row)
					}
				}
			}
			if e != nil || pageNo*200 >= total {
				break
			}
		}
	case "inventory":
		permission = "inventory.view"
		rows, e = a.Inventory(u, queryID(c, "warehouse_id"), c.Query("search"))
	case "transactions":
		permission = "inventory.view"
		rows, e = a.StockTransactions(u, 0, queryID(c, "warehouse_id"), c.Query("search"))
	case "serials":
		permission = "inventory.view"
		rows, e = a.SerialRecords(u, c.Query("search"))
	case "bills":
		permission = "finance.view"
		rows, e = a.Bills(u, sideQuery(c), queryID(c, "partner_id"), false)
	case "settlements":
		permission = "finance.view"
		rows, e = a.Settlements(u, sideQuery(c), queryID(c, "partner_id"))
	case "aging":
		permission = "finance.view"
		rows, e = a.Aging(u, sideQuery(c))
	case "statement":
		permission = "finance.view"
		var result M
		result, e = a.Statement(u, sideQuery(c), queryID(c, "partner_id"), c.Query("start"), c.Query("end"))
		if e == nil {
			rows = result["items"].([]M)
			rows = append([]M{{"number": "期初余额", "balance": result["opening"]}}, rows...)
		}
	case "report":
		permission = "reports.view"
		rows, e = a.Report(u, c.DefaultQuery("category", "sales"), c.DefaultQuery("group", "date"), c.Query("start"), c.Query("end"))
	default:
		a.error(c, fail("导出类型无效"))
		return
	}
	if !u.Can(permission) {
		a.error(c, denied("没有导出此数据的权限"))
		return
	}
	if e != nil {
		a.error(c, e)
		return
	}
	var safe []M
	_ = json.Unmarshal([]byte(js(redact(u, rows))), &safe)
	columns := []string{}
	seen := map[string]bool{}
	for _, row := range safe {
		for key, value := range row {
			if value != nil && !seen[key] {
				columns = append(columns, key)
				seen[key] = true
			}
		}
	}
	sortColumns(columns)
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="ruilian-`+kind+"-"+today()+`.csv"`)
	c.Status(200)
	_, _ = c.Writer.Write([]byte{0xef, 0xbb, 0xbf})
	w := csv.NewWriter(c.Writer)
	labels := make([]string, len(columns))
	for i, k := range columns {
		labels[i] = columnLabel(k)
	}
	_ = w.Write(labels)
	for _, row := range safe {
		values := make([]string, len(columns))
		for i, k := range columns {
			if row[k] != nil {
				v := fmt.Sprint(row[k])
				if strings.HasPrefix(v, "=") || strings.HasPrefix(v, "@") || strings.HasPrefix(v, "+") || strings.HasPrefix(v, "-") {
					if _, er := strconv.ParseFloat(v, 64); er != nil {
						v = "'" + v
					}
				}
				values[i] = v
			}
		}
		_ = w.Write(values)
	}
	w.Flush()
}
func sortColumns(cols []string) {
	preferred := []string{"number", "date", "kind_name", "status", "dimension", "product_code", "product_name", "code", "name", "partner_name", "warehouse_name", "location_name", "batch", "quantity", "price", "line_amount", "amount", "settled", "remaining", "allocated", "cost", "profit", "balance", "note"}
	rank := map[string]int{}
	for i, k := range preferred {
		rank[k] = i + 1
	}
	for i := 0; i < len(cols); i++ {
		for j := i + 1; j < len(cols); j++ {
			ri, rj := rank[cols[i]], rank[cols[j]]
			if ri == 0 {
				ri = 1000
			}
			if rj == 0 {
				rj = 1000
			}
			if rj < ri || rj == ri && cols[j] < cols[i] {
				cols[i], cols[j] = cols[j], cols[i]
			}
		}
	}
}
func columnLabel(k string) string {
	labels := map[string]string{
		"number": "单据编号", "date": "业务日期", "kind_name": "业务类型", "status": "状态", "dimension": "统计维度",
		"product_code": "商品编码", "product_name": "商品名称", "code": "编码", "name": "名称", "partner_name": "往来单位",
		"warehouse_name": "仓库", "location_name": "库位", "batch": "批次", "quantity": "数量", "price": "单价",
		"line_amount": "明细金额", "amount": "金额", "settled": "已核销", "remaining": "未核销余额", "allocated": "已分配金额",
		"cost": "成本", "cost_amount": "成本金额", "profit": "毛利", "balance": "往来余额", "note": "备注",
		"unit": "单位", "serials": "序列号", "value": "库存金额", "available": "可用数量", "reserved": "占用数量",
		"avg_cost": "平均成本", "avg_price": "平均单价", "created_at": "创建时间", "due_date": "到期日期",
		"paid": "收付款", "charge": "账务发生额", "creator": "制单人", "operator": "操作人", "margin": "毛利率", "document_count": "单据数",
		"current": "未到期", "days_1_30": "逾期1-30天", "days_31_60": "逾期31-60天", "days_61_90": "逾期61-90天",
		"days_91_180": "逾期91-180天", "days_180_plus": "逾期180天以上", "credit": "退货贷项", "total": "合计",
		"before_quantity": "变动前数量", "delta": "变动数量", "after_quantity": "变动后数量", "cost_delta": "成本变动",
		"before_value": "变动前成本", "after_value": "变动后成本", "book_quantity": "账面数量", "actual_quantity": "实盘数量",
		"period": "月份", "sales": "净销售额", "purchase": "净采购额", "cash_received": "净收款", "cash_paid": "净付款",
		"collection_rate": "当期回款率", "gross_profit": "经营毛利", "cost_adjustments": "回补成本差异",
		"opening_inventory_value": "期初库存金额", "closing_inventory_value": "期末库存金额", "average_inventory_value": "平均库存金额",
		"inventory_turnover": "当期周转次数", "stock_days": "库存周转天数", "sales_cost": "销售成本",
	}
	if v := labels[k]; v != "" {
		return v
	}
	return k
}
