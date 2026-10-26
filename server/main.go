// ClipCascade server - 一个用 Go 编写的轻量级剪贴板同步 server。
//
// 这取代了原来的 Java Spring Boot server，只有一个 ~10MB 的二进制文件。
// 使用 Fiber 框架处理 HTTP/WebSocket，GORM+SQLite 进行持久化。
package main

import (
	"embed"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/clipcascade/pkg/netutil"
	"github.com/clipcascade/pkg/sizefmt"
	"github.com/clipcascade/server/automation"
	"github.com/clipcascade/server/config"
	"github.com/clipcascade/server/handler"
	"github.com/clipcascade/server/middleware"
	"github.com/clipcascade/server/model"
	"github.com/glebarez/sqlite" // 纯 Go 实现的 SQLite (无需 CGO)
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/session"
	"github.com/gofiber/template/html/v2"
	"github.com/grandcat/zeroconf"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

//go:embed web/templates/*.html
var templatesFS embed.FS

//go:embed web/static
var staticFS embed.FS

func main() {
	// 设置结构化日志
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// 加载 config
	cfg := config.Load()
	flag.Func("file-relay-max-size", "Maximum relay file size, e.g. 2GiB, 20GiB or bytes (overrides CC_FILE_RELAY_MAX_BYTES)", func(value string) error {
		size, err := config.ParseFileRelaySize(value)
		if err == nil {
			cfg.FileRelayMaxBytes = size
		}
		return err
	})
	flag.Parse()
	if err := cfg.ValidateFileRelaySize(); err != nil {
		slog.Error("文件大小上限配置无效", "error", err)
		os.Exit(1)
	}
	slog.Info("配置已加载",
		"port", cfg.Port,
		"p2p", cfg.P2PEnabled,
		"signup", cfg.SignupEnabled,
		"max_msg_mib", cfg.MaxMessageSizeMiB,
		"file_relay", cfg.FileRelayEnabled,
		"file_relay_max_bytes", cfg.FileRelayMaxBytes,
		"automation", cfg.AutomationEnabled,
		"automation_mode", cfg.AutomationMode,
	)

	// 初始化界面数据库
	dbDir := filepath.Dir(cfg.DatabasePath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		slog.Error("创建数据库目录失败", "error", err)
		os.Exit(1)
	}

	// SQLite 打开参数：WAL 模式 + busy_timeout，避免高并发写下 database is locked。
	// 写连接串行为 1（SetMaxOpenConns(1)），彻底规避 SQLite 单写锁竞争。
	dsn := cfg.DatabasePath
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		slog.Error("打开数据库失败", "error", err)
		os.Exit(1)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(0)
	} else {
		slog.Warn("无法配置数据库连接池", "error", err)
	}

	if err := model.InitDB(db); err != nil {
		slog.Error("数据库迁移失败", "error", err)
		os.Exit(1)
	}
	if cfg.FileRelayEnabled {
		if err := os.MkdirAll(cfg.FileRelayDir, 0o755); err != nil {
			slog.Error("创建文件中转目录失败", "error", err)
			os.Exit(1)
		}
	}

	// 如果不存在，播种默认 admin user
	seedAdminUser(db)

	// 初始化 session 存储
	handler.Store = session.New(session.Config{
		Expiration:     time.Duration(cfg.SessionTimeoutMinutes) * time.Minute,
		CookieHTTPOnly: true,
		CookieSameSite: "Lax",
	})

	// 使用嵌入的 HTML 模板初始化 Fiber 模板引擎
	engine := html.NewFileSystem(http.FS(templatesFS), ".html")

	// 创建带有模板引擎的 Fiber app
	bodyLimit := int(cfg.EffectiveMaxMessageBytes()) + 1024*1024
	if cfg.FileRelayEnabled && cfg.FileRelayMaxBytes > 0 {
		relayLimit := int(cfg.FileRelayMaxBytes + 1024*1024)
		if relayLimit > bodyLimit {
			bodyLimit = relayLimit
		}
	}
	app := fiber.New(fiber.Config{
		Views:                        engine,
		BodyLimit:                    bodyLimit,
		StreamRequestBody:            true,
		DisablePreParseMultipartForm: true,
	})

	// 全局中间件
	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format:     "${time} | ${status} | ${latency} | ${method} ${path}\n",
		TimeFormat: "15:04:05",
	}))
	// CORS：仅当显式配置了 CC_ALLOWED_ORIGINS 时启用。
	// 默认（空）= 仅同源：浏览器同源请求本就不需要 CORS 头，跨域一律被浏览器拦截。
	if cfg.AllowedOrigins != "" {
		app.Use(cors.New(cors.Config{
			AllowOrigins:     cfg.AllowedOrigins,
			AllowCredentials: cfg.AllowedOrigins != "*",
		}))
	}
	app.Use(func(c *fiber.Ctx) error {
		if c.Path() == "/api/files/upload" {
			return c.Next()
		}
		contentLength := c.Request().Header.ContentLength()
		if contentLength < 0 && c.Method() != http.MethodGet && c.Method() != http.MethodHead {
			return c.Status(fiber.StatusLengthRequired).JSON(fiber.Map{"error": "request body length required"})
		}
		if maxBytes := cfg.EffectiveMaxMessageBytes(); maxBytes > 0 && int64(contentLength) > maxBytes {
			return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "request body too large", "max_bytes": maxBytes})
		}
		return c.Next()
	})

	// API 速率限制：每 IP 每分钟上限（默认 240，CC_API_RATE_LIMIT 可调，<=0 关闭）
	app.Use("/api", middleware.NewAPIRateLimit(cfg.APIRateLimit, time.Minute))

	// 提供嵌入的静态文件服务
	app.Use("/assets", filesystem.New(filesystem.Config{
		Root:       http.FS(staticFS),
		PathPrefix: "web/static",
	}))

	// 创建 handlers 和中间件
	wsHub := handler.NewWSHub()
	// WS 单条消息上限：消息上限 * 4/3（base64 膨胀）+ 1MB 帧开销，封顶 256MB
	wsReadLimit := cfg.EffectiveMaxMessageBytes()
	if wsReadLimit <= 0 {
		wsReadLimit = 64 << 20 // P2P 模式下消息不限大小，WS 通道仍设 64MiB 上限
	}
	wsReadLimit = wsReadLimit*4/3 + 1<<20
	if wsReadLimit > 256<<20 {
		wsReadLimit = 256 << 20
	}
	wsHub.SetReadLimit(wsReadLimit)
	var automationEngine *automation.Engine
	if cfg.AutomationEnabled {
		var err error
		automationEngine, err = automation.NewEngine(db, cfg)
		if err != nil {
			slog.Error("自动化引擎加载失败，已禁用自动化能力", "error", err)
		} else {
			wsHub.SetAutomationEngine(automationEngine)
			pluginCatalog := automationEngine.PluginCatalog()
			slog.Info("自动化引擎已启用",
				"mode", cfg.AutomationMode,
				"rules_path", cfg.AutomationRulesPath,
				"commands_path", cfg.AutomationCommandsPath,
				"plugins_dir", pluginCatalog.Root,
				"plugins_total", pluginCatalog.Stats.Total,
				"plugins_enabled", pluginCatalog.Stats.Enabled,
			)
		}
	}
	bfa := middleware.NewBruteForceProtection(cfg)
	go bfa.Cleanup() // 启动后台清理任务

	authHandler := handler.NewAuthHandler(db, cfg, bfa)
	statusHandler := handler.NewStatusHandler(cfg)
	fileTransferHandler := handler.NewFileTransferHandler(db, cfg, wsHub)
	automationHandler := handler.NewAutomationHandler(db, cfg, automationEngine)
	fileRelayStop := make(chan struct{})
	if cfg.FileRelayEnabled {
		go fileTransferHandler.RunCleanupLoop(10*time.Minute, fileRelayStop)
	}

	// ---- 公共路由 (无需 auth) ----
	// 应用蛮力攻击保护中间件到登录和注册
	app.Get("/login", bfa.Check, authHandler.LoginPage)
	app.Post("/login", bfa.Check, authHandler.LoginPost)
	app.Get("/signup", bfa.Check, authHandler.SignupPage)
	app.Post("/signup", bfa.Check, authHandler.SignupPost)
	app.Get("/health", statusHandler.Health)
	app.Get("/ping", statusHandler.Ping)

	// ---- 所有后续路由的 auth 中间件 ----
	app.Use(func(c *fiber.Ctx) error {
		// 跳过公共路径的 auth
		path := c.Path()
		if path == "/login" || path == "/signup" || path == "/health" || path == "/ping" {
			return c.Next()
		}

		sess, err := handler.Store.Get(c)
		if err != nil || sess.Get("username") == nil {
			// API 路由返回 JSON 401；页面路由重定向
			if strings.HasPrefix(path, "/api/") {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
			}
			return c.Redirect("/login")
		}

		// 回查数据库校验用户状态：禁用/删除的用户立即失效，角色实时生效，
		// 不再依赖登录时写入 session 的快照。
		username, _ := sess.Get("username").(string)
		var u model.User
		if err := db.Where("username = ?", username).First(&u).Error; err != nil || !u.Enabled {
			_ = sess.Destroy()
			if strings.HasPrefix(path, "/api/") {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
			}
			return c.Redirect("/login")
		}

		// 在 local 中为下游 handlers 设置 user 信息（以数据库实时值为准）
		c.Locals("username", u.Username)
		c.Locals("user_id", u.ID)
		c.Locals("role", u.Role)
		return c.Next()
	})

	// ---- 受保护的路由 ----
	app.Get("/", func(c *fiber.Ctx) error {
		mode := "Server-Based (STOMP)"
		if cfg.P2PEnabled {
			mode = "Peer-to-Peer (WebRTC)"
		}
		return c.Render("web/templates/home", fiber.Map{
			"username":           c.Locals("username"),
			"mode":               mode,
			"port":               cfg.Port,
			"max_size_mib":       cfg.MaxMessageSizeMiB,
			"is_admin":           c.Locals("role") == "ADMIN",
			"file_relay_enabled": cfg.FileRelayEnabled,
			"file_relay_max":     sizefmt.FormatBytes(cfg.FileRelayMaxBytes),
		})
	})
	app.Get("/logout", authHandler.Logout)

	// API 端点
	app.Get("/api/server-mode", statusHandler.ServerMode)
	app.Get("/api/stun-url", statusHandler.GetStunURL)
	app.Get("/api/max-size", statusHandler.GetMaxSizeAllowed)
	app.Get("/api/files/config", statusHandler.FileRelayConfig)
	app.Get("/api/validate-session", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"valid": true})
	})
	app.Get("/api/whoami", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"username": c.Locals("username"),
			"role":     c.Locals("role"),
		})
	})
	app.Get("/api/ws-stats", func(c *fiber.Ctx) error {
		return c.JSON(wsHub.Stats())
	})
	app.Get("/api/devices/online", func(c *fiber.Ctx) error {
		username, _ := c.Locals("username").(string)
		online := wsHub.UserConnectionCount(username)
		syncTargets := online - 1
		if syncTargets < 0 {
			syncTargets = 0
		}
		slog.Debug("API：在线设备统计", "用户名", username, "在线设备", online, "同步目标", syncTargets)
		return c.JSON(fiber.Map{
			"online_devices": online,
			"sync_targets":   syncTargets,
			"username":       username,
		})
	})
	app.Get("/api/files/deferred", fileTransferHandler.ListDeferred)
	app.Get("/api/files/deferred/:deferredID", fileTransferHandler.GetDeferred)
	app.Delete("/api/files/deferred/:deferredID", fileTransferHandler.DeleteDeferred)
	app.Post("/api/files/deferred/register", fileTransferHandler.RegisterDeferred)
	app.Post("/api/files/deferred/:deferredID/request", fileTransferHandler.RequestDeferred)
	app.Post("/api/files/deferred/:deferredID/progress", fileTransferHandler.ProgressDeferred)
	app.Post("/api/files/deferred/:deferredID/complete", fileTransferHandler.CompleteDeferred)
	app.Post("/api/files/deferred/:deferredID/fail", fileTransferHandler.FailDeferred)
	app.Post("/api/files/upload", fileTransferHandler.Upload)
	app.Get("/api/files/:relayID", fileTransferHandler.Download)
	app.Get("/api/automation/tasks", automationHandler.ListMyTasks)
	app.Post("/api/automation/copy", automationHandler.TrustedCopy)
	app.Get("/api/user-info", func(c *fiber.Ctx) error {
		userID, _ := c.Locals("user_id").(uint)
		var info model.UserInfo
		if err := db.Where("user_id = ?", userID).First(&info).Error; err != nil {
			return c.JSON(fiber.Map{"salt": c.Locals("username"), "hash_rounds": 100000})
		}
		return c.JSON(fiber.Map{"salt": info.Salt, "hash_rounds": info.HashRounds})
	})

	// WebSocket 端点 (STOMP)
	app.Use("/clipsocket", func(c *fiber.Ctx) error {
		if !originAllowed(cfg, c) {
			return fiber.ErrForbidden
		}
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})
	app.Get("/clipsocket", websocket.New(func(c *websocket.Conn) {
		wsHub.HandleWebSocket(c)
	}))

	// P2P WebSocket signaling 端点
	p2pSignaling := handler.NewP2PSignaling()
	app.Use("/p2p", func(c *fiber.Ctx) error {
		if !originAllowed(cfg, c) {
			return fiber.ErrForbidden
		}
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})
	app.Get("/p2p", websocket.New(func(c *websocket.Conn) {
		p2pSignaling.HandleP2P(c)
	}))

	// 仅限 admin 的路由
	adminHandler := handler.NewAdminHandler(db, cfg, wsHub, p2pSignaling)
	admin := app.Group("/api/admin", handler.AdminOnly)
	admin.Get("/users", adminHandler.ListUsers)
	admin.Delete("/users/:id", adminHandler.DeleteUser)
	admin.Put("/users/:id/password", adminHandler.ResetPassword)
	admin.Put("/users/:id/role", adminHandler.SetRole)
	admin.Put("/users/:id/status", adminHandler.ToggleUserStatus)
	admin.Put("/users/:id/username", adminHandler.UpdateUsername)
	admin.Post("/users", adminHandler.RegisterUser)
	admin.Get("/stats", adminHandler.ServerStats)
	admin.Get("/server-version", adminHandler.ServerVersion)
	admin.Get("/server-time", adminHandler.ServerTime)
	admin.Get("/automation/tasks", automationHandler.ListAdminTasks)
	admin.Get("/automation/audits", automationHandler.ListAdminAudits)
	admin.Get("/automation/plugins", automationHandler.ListAdminPlugins)
	admin.Put("/automation/plugins/:id/enabled", automationHandler.SetAdminPluginEnabled)
	app.Get("/advance", handler.AdminOnly, adminHandler.AdvancePage)
	app.Get("/plugins", handler.AdminOnly, automationHandler.PluginsPage)

	// 自助端点 (任何经过身份验证的 user)
	app.Put("/api/update-password", handler.UpdatePasswordSelf(db))

	// 找到可用的端口并启动 server
	var ln net.Listener
	startPort := cfg.Port
	for {
		addr := fmt.Sprintf(":%d", cfg.Port)
		l, err := net.Listen("tcp", addr)
		if err == nil {
			ln = l
			break
		}
		slog.Warn("端口已被占用，正在尝试下一个端口", "port", cfg.Port)
		cfg.Port++
		if cfg.Port > startPort+100 {
			slog.Error("在范围内无法找到可用端口", "start", startPort)
			os.Exit(1)
		}
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port
	slog.Info("🚀 ClipCascade 服务正在启动", "port", actualPort)

	// 显示所有可用的本地 IP 地址
	fmt.Printf("\n  ClipCascade Server v2 dev server running at:\n\n")
	fmt.Printf("  ➜  Local:   http://localhost:%d/\n", actualPort)
	ips := getLocalIPs()
	for _, ip := range ips {
		fmt.Printf("  ➜  Network: http://%s:%d/\n", ip, actualPort)
	}
	fmt.Printf("\n")

	// 启动 mDNS 服务广播
	serverName := fmt.Sprintf("ClipCascade-%d", actualPort)
	mdnsIfaces := netutil.UsableLANInterfaces()
	mdnsServer, err := zeroconf.Register(serverName, "_clipcascade._tcp", "local.", actualPort, []string{"txtv=1", "app=clipcascade"}, mdnsIfaces)
	if err != nil {
		slog.Warn("注册 mDNS 服务失败", "error", err)
	} else {
		defer mdnsServer.Shutdown()
		slog.Info("📢 局域网发现已激活 (mDNS)", "service", "_clipcascade._tcp", "name", serverName, "interfaces", interfaceNames(mdnsIfaces))
	}

	// 优雅关机处理
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan
		slog.Info("正在关闭服务器...")
		close(fileRelayStop)
		if mdnsServer != nil {
			mdnsServer.Shutdown()
		}
		_ = app.Shutdown()
	}()

	if err := app.Listener(ln); err != nil {
		slog.Error("服务器启动失败", "error", err)
		os.Exit(1)
	}
}

// seedAdminUser 如果不存在 user，则创建默认 admin user。
// originAllowed 校验 WebSocket 升级请求的 Origin，防跨站 WebSocket 劫持（CSWSH）。
// 规则：
//   - 原生客户端（桌面/移动 Go 客户端）不发 Origin → 直接放行；
//   - CC_ALLOWED_ORIGINS="*" → 不限制；
//   - 浏览器请求必须同源（Origin host == 请求 host）或命中白名单。
func originAllowed(cfg *config.Config, c *fiber.Ctx) bool {
	origin := c.Get("Origin")
	if origin == "" {
		return true
	}
	if cfg.AllowedOrigins == "*" {
		return true
	}
	if u, err := url.Parse(origin); err == nil && u.Host != "" && strings.EqualFold(u.Host, c.Hostname()) {
		return true
	}
	for _, o := range cfg.AllowedOriginsList() {
		if strings.TrimSuffix(o, "/") == origin {
			return true
		}
	}
	return false
}

func seedAdminUser(db *gorm.DB) {
	var count int64
	db.Model(&model.User{}).Count(&count)
	if count > 0 {
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("对默认管理员密码进行哈希处理失败", "error", err)
		return
	}

	admin := model.User{
		Username: "admin",
		Password: string(hashed),
		Role:     "ADMIN",
		Enabled:  true,
	}
	if err := db.Create(&admin).Error; err != nil {
		slog.Error("创建管理员用户失败", "error", err)
		return
	}

	db.Create(&model.UserInfo{
		UserID:     admin.ID,
		Salt:       "admin",
		HashRounds: 100000,
	})

	slog.Info("✅ 默认管理员用户已创建 (admin / admin123)")
}

// getLocalIPs 返回适合局域网访问的 IPv4 地址。
func getLocalIPs() []string {
	return netutil.LocalIPv4s()
}

func interfaceNames(ifaces []net.Interface) []string {
	names := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		names = append(names, iface.Name)
	}
	return names
}
