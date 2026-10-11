package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"
	"gorm.io/gorm"
)

//go:embed web/templates
var templatesFS embed.FS

const (
	pageSize       = 10
	nameMaxRunes   = 20
	maxUploadBytes = 500 << 20 // 500MB
	csrfCookieName = "webclip_csrf"
	defaultName    = "Anonymous"
)

var db *gorm.DB
var uploadDir string

type Message struct {
	ID        uint           `gorm:"primaryKey"`
	Name      string         `gorm:"size:20"`
	Body      string         `gorm:"type:text"`
	Timestamp time.Time      `gorm:"index"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func initDB(dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}
	// WAL + busy_timeout，避免并发写锁冲突
	sep := "?"
	if strings.Contains(dbPath, "?") {
		sep = "&"
	}
	dbPath += sep + "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	var err error
	db, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	return db.AutoMigrate(&Message{})
}

// inlineExts 是允许浏览器内联预览的扩展名白名单。
// html/svg 等可执行脚本类型一律强制 attachment 下载，杜绝存储型 XSS。
var inlineExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".bmp": true, ".ico": true, ".avif": true,
	".mp4": true, ".webm": true, ".mov": true, ".m4v": true,
	".mp3": true, ".wav": true, ".ogg": true, ".flac": true,
	".pdf": true, ".txt": true, ".md": true, ".csv": true, ".json": true,
}

func serveUpload(c *fiber.Ctx) error {
	name := filepath.Base(c.Params("name"))
	if name == "" || name == "." {
		return fiber.ErrNotFound
	}
	p := filepath.Join(uploadDir, name)
	if _, err := os.Stat(p); err != nil {
		return fiber.ErrNotFound
	}
	ext := strings.ToLower(filepath.Ext(name))
	c.Set("X-Content-Type-Options", "nosniff")
	disposition := "attachment"
	if inlineExts[ext] {
		disposition = "inline"
	}
	c.Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disposition, url.PathEscape(name)))
	return c.SendFile(p)
}

func parsePage(raw string) int {
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 {
		return 1
	}
	return page
}

func calcTotalPages(total int64) int {
	pages := int(math.Ceil(float64(total) / float64(pageSize)))
	if pages < 1 {
		return 1
	}
	return pages
}

func listUploadFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func loadMessages(page int) ([]Message, int64, int, error) {
	var total int64
	if err := db.Model(&Message{}).Count(&total).Error; err != nil {
		return nil, 0, 0, err
	}
	var msgs []Message
	if err := db.Order("timestamp desc").Offset((page-1)*pageSize).Limit(pageSize).Find(&msgs).Error; err != nil {
		return nil, 0, 0, err
	}
	return msgs, total, calcTotalPages(total), nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ensureCSRFToken(c *fiber.Ctx) (string, error) {
	token := c.Cookies(csrfCookieName)
	if token != "" {
		return token, nil
	}
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	c.Cookie(&fiber.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		MaxAge:   86400 * 30,
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
	})
	return token, nil
}

func validateCSRF(c *fiber.Ctx) (bool, error) {
	cookieToken := c.Cookies(csrfCookieName)
	if cookieToken == "" {
		return false, c.Status(fiber.StatusForbidden).SendString("missing csrf cookie")
	}
	formToken := c.FormValue("csrf_token")
	if formToken == "" || subtle.ConstantTimeCompare([]byte(cookieToken), []byte(formToken)) != 1 {
		return false, c.Status(fiber.StatusForbidden).SendString("invalid csrf token")
	}
	return true, nil
}

func normalizeName(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return defaultName
	}
	if len([]rune(name)) > nameMaxRunes {
		return string([]rune(name)[:nameMaxRunes])
	}
	return name
}

func normalizeBody(raw string) (string, error) {
	body := strings.TrimSpace(raw)
	if body == "" {
		return "", errors.New("body is required")
	}
	return body, nil
}

func uniqueUploadPath(dir, fileName string) string {
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	candidate := fileName
	for i := 1; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return filepath.Join(dir, candidate)
		}
		candidate = fmt.Sprintf("%s_%d%s", base, i, ext)
	}
}

func main() {
	// 默认路径基于二进制所在目录，避免相对路径混乱
	exeDir := func() string {
		exe, err := os.Executable()
		if err != nil {
			return "."
		}
		return filepath.Dir(exe)
	}()

	var portFlag int
	flag.IntVar(&portFlag, "p", 0, "port to listen on")
	flag.IntVar(&portFlag, "port", 0, "port to listen on")
	flag.Parse()

	dbPath := strings.TrimSpace(os.Getenv("WEBCLIP_DB_PATH"))
	if dbPath == "" {
		dbPath = filepath.Join(exeDir, "data.db")
	}
	uploadDir = strings.TrimSpace(os.Getenv("WEBCLIP_UPLOAD_DIR"))
	if uploadDir == "" {
		uploadDir = filepath.Join(exeDir, "uploads")
	}
	startPort := 8090
	if portFlag > 0 {
		startPort = portFlag
	} else if p := strings.TrimSpace(os.Getenv("WEBCLIP_PORT")); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			startPort = n
		}
	}

	if err := initDB(dbPath); err != nil {
		log.Fatal("init db:", err)
	}
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		log.Fatal("create upload dir:", err)
	}

	fmt.Printf("\n  database : %s\n", dbPath)
	fmt.Printf("  uploads  : %s\n\n", uploadDir)

	engine := html.NewFileSystem(http.FS(templatesFS), ".html")
	engine.AddFunc("add", func(a, b int) int { return a + b })
	engine.AddFunc("sub", func(a, b int) int { return a - b })

	app := fiber.New(fiber.Config{
		Views:     engine,
		BodyLimit: maxUploadBytes,
	})

	// ── 可选口令认证：设置 WEBCLIP_PASSWORD 后全站需要登录；未设置则与原来一致 ──
	authPassword := strings.TrimSpace(os.Getenv("WEBCLIP_PASSWORD"))
	authCookieName := "webclip_auth"
	authToken := ""
	if authPassword != "" {
		sum := sha256.Sum256([]byte("webclip-auth:" + authPassword))
		authToken = hex.EncodeToString(sum[:])
	}
	renderLoginPage := func(c *fiber.Ctx, errMsg string) error {
		errHTML := ""
		if errMsg != "" {
			errHTML = fmt.Sprintf(`<p style="color:#e53e3e;margin-top:12px">%s</p>`, errMsg)
		}
		c.Set("Content-Type", "text/html; charset=utf-8")
		return c.Status(fiber.StatusUnauthorized).SendString(`<!doctype html><html lang="zh"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>WebClip 登录</title></head>
<body style="font-family:system-ui;display:flex;justify-content:center;padding-top:15vh;background:#f7faf8">
<div style="width:320px;background:#fff;border-radius:12px;box-shadow:0 1px 6px rgba(0,0,0,.08);padding:28px">
<h2 style="margin:0 0 16px;font-size:18px">WebClip 访问口令</h2>
<form method="post" action="/weblogin">
<input type="password" name="password" placeholder="访问口令" required autofocus
 style="width:100%;box-sizing:border-box;padding:10px;border:1px solid #d1d5db;border-radius:8px">
` + errHTML + `
<button type="submit" style="width:100%;margin-top:12px;padding:10px;background:#059669;color:#fff;border:0;border-radius:8px;cursor:pointer">进入</button>
</form></div></body></html>`)
	}
	app.Use(func(c *fiber.Ctx) error {
		if authToken == "" {
			return c.Next()
		}
		if c.Path() == "/weblogin" {
			return c.Next()
		}
		if c.Cookies(authCookieName) != "" &&
			subtle.ConstantTimeCompare([]byte(c.Cookies(authCookieName)), []byte(authToken)) == 1 {
			return c.Next()
		}
		return renderLoginPage(c, "")
	})
	app.Post("/weblogin", func(c *fiber.Ctx) error {
		if authToken == "" {
			return c.Redirect("/")
		}
		if subtle.ConstantTimeCompare([]byte(c.FormValue("password")), []byte(authPassword)) != 1 {
			return renderLoginPage(c, "口令错误，请重试")
		}
		c.Cookie(&fiber.Cookie{
			Name:     authCookieName,
			Value:    authToken,
			MaxAge:   86400 * 30,
			Path:     "/",
			HTTPOnly: true,
			SameSite: "Lax",
		})
		return c.Redirect("/")
	})

	// 上传文件受控下载：白名单类型内联预览，其余强制 attachment（防存储型 XSS）
	app.Get("/uploads/:name", serveUpload)

	// ── 首页：列表 + 提交 ──
	app.Get("/", func(c *fiber.Ctx) error {
		page := parsePage(c.Query("page", "1"))
		msgs, total, totalPages, err := loadMessages(page)
		if err != nil {
			return c.Status(500).SendString("failed to load messages")
		}
		files, err := listUploadFiles(uploadDir)
		if err != nil {
			return c.Status(500).SendString("failed to list files")
		}
		csrfToken, err := ensureCSRFToken(c)
		if err != nil {
			return c.Status(500).SendString("csrf error")
		}
		return c.Render("web/templates/index", fiber.Map{
			"messages":   msgs,
			"page":       page,
			"totalPages": totalPages,
			"total":      total,
			"fileNames":  files,
			"csrfToken":  csrfToken,
		})
	})

	app.Post("/", func(c *fiber.Ctx) error {
		ok, err := validateCSRF(c)
		if !ok {
			return err
		}
		body, err := normalizeBody(c.FormValue("body"))
		if err != nil {
			return c.Status(400).SendString(err.Error())
		}
		msg := Message{
			Name:      normalizeName(c.FormValue("name")),
			Body:      body,
			Timestamp: time.Now(),
		}
		if err := db.Create(&msg).Error; err != nil {
			return c.Status(500).SendString("save failed")
		}
		return c.Redirect("/")
	})

	// ── 文件上传 ──
	app.Post("/upload", func(c *fiber.Ctx) error {
		ok, err := validateCSRF(c)
		if !ok {
			return err
		}
		fh, err := c.FormFile("file")
		if err != nil {
			return c.Status(400).SendString("invalid file")
		}
		if fh.Size <= 0 || fh.Size > maxUploadBytes {
			return c.Status(400).SendString("invalid file size")
		}
		name := filepath.Base(fh.Filename)
		if name == "" || name == "." {
			return c.Status(400).SendString("invalid filename")
		}
		if err := c.SaveFile(fh, uniqueUploadPath(uploadDir, name)); err != nil {
			return c.Status(500).SendString("upload failed")
		}
		fmt.Printf("[upload] %s  size=%d bytes  from=%s\n", name, fh.Size, c.IP())
		return c.Redirect("/")
	})

	// ── 管理页 ──
	app.Get("/manage", func(c *fiber.Ctx) error {
		page := parsePage(c.Query("page", "1"))
		msgs, _, totalPages, err := loadMessages(page)
		if err != nil {
			return c.Status(500).SendString("failed to load messages")
		}
		files, err := listUploadFiles(uploadDir)
		if err != nil {
			return c.Status(500).SendString("failed to list files")
		}
		csrfToken, err := ensureCSRFToken(c)
		if err != nil {
			return c.Status(500).SendString("csrf error")
		}
		return c.Render("web/templates/manage", fiber.Map{
			"messages":   msgs,
			"page":       page,
			"totalPages": totalPages,
			"fileNames":  files,
			"csrfToken":  csrfToken,
		})
	})

	app.Post("/manage/create", func(c *fiber.Ctx) error {
		ok, err := validateCSRF(c)
		if !ok {
			return err
		}
		body, err := normalizeBody(c.FormValue("body"))
		if err != nil {
			return c.Status(400).SendString(err.Error())
		}
		msg := Message{
			Name:      normalizeName(c.FormValue("name")),
			Body:      body,
			Timestamp: time.Now(),
		}
		if err := db.Create(&msg).Error; err != nil {
			return c.Status(500).SendString("save failed")
		}
		return c.Redirect("/manage?page=1")
	})

	handleManageAction := func(c *fiber.Ctx) error {
		ok, err := validateCSRF(c)
		if !ok {
			return err
		}
		page := parsePage(c.FormValue("page"))
		action := c.FormValue("action")

		switch action {
		case "bulk_delete":
			ids := c.Request().PostArgs().PeekMulti("delete_ids")
			if len(ids) > 0 {
				strIDs := make([]string, len(ids))
				for i, id := range ids {
					strIDs[i] = string(id)
				}
				db.Where("id IN ?", strIDs).Delete(&Message{})
			}
		case "delete_one":
			id, err := strconv.ParseUint(c.FormValue("delete_id"), 10, 32)
			if err != nil {
				return c.Status(400).SendString("invalid id")
			}
			db.Where("id = ?", id).Delete(&Message{})
		case "edit":
			id, err := strconv.ParseUint(c.FormValue("edit_id"), 10, 32)
			if err != nil {
				return c.Status(400).SendString("invalid id")
			}
			rawID := c.FormValue("edit_id")
			newBody, err := normalizeBody(c.FormValue("new_body_" + rawID))
			if err != nil {
				return c.Status(400).SendString(err.Error())
			}
			db.Model(&Message{}).Where("id = ?", id).Update("body", newBody)
		default:
			return c.Status(400).SendString("invalid action")
		}

		var count int64
		db.Model(&Message{}).Count(&count)
		maxPage := calcTotalPages(count)
		if page > maxPage {
			page = maxPage
		}
		return c.Redirect("/manage?page=" + strconv.Itoa(page))
	}

	app.Post("/manage", handleManageAction)

	app.Post("/file/delete", func(c *fiber.Ctx) error {
		ok, err := validateCSRF(c)
		if !ok {
			return err
		}
		name := filepath.Base(c.FormValue("filename"))
		if name == "" || name == "." {
			return c.Status(400).SendString("invalid filename")
		}
		if err := os.Remove(filepath.Join(uploadDir, name)); err != nil {
			return c.Status(500).SendString("delete failed")
		}
		return c.Redirect("/manage")
	})

	// 端口自增：如果目标端口被占用，自动往后找
	port := startPort
	var ln net.Listener
	for {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			ln = l
			break
		}
		fmt.Printf("[warn] port %d in use, trying %d\n", port, port+1)
		port++
		if port > startPort+100 {
			log.Fatal("no available port found")
		}
	}

	fmt.Printf("  ➜  http://localhost:%d/\n\n", port)
	log.Fatal(app.Listener(ln))
}
