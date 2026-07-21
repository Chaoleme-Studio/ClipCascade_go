package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/clipcascade/pkg/protocol"
	"github.com/clipcascade/server/automation"
	"github.com/clipcascade/server/config"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/clipcascade/server/model"
)

// AutomationHandler 提供自动化任务、审计、插件和可信复制接口。
type AutomationHandler struct {
	DB     *gorm.DB
	Config *config.Config
	Engine *automation.Engine
}

// NewAutomationHandler 创建绑定当前规则引擎的自动化处理器。
func NewAutomationHandler(db *gorm.DB, cfg *config.Config, engine *automation.Engine) *AutomationHandler {
	return &AutomationHandler{DB: db, Config: cfg, Engine: engine}
}

// ListMyTasks 返回当前登录用户最近的自动化任务。
func (h *AutomationHandler) ListMyTasks(c *fiber.Ctx) error {
	username, _ := c.Locals("username").(string)
	var tasks []model.AutomationTask
	if err := h.DB.Where("username = ?", username).
		Order("created_at desc").
		Limit(pageSize(c)).
		Find(&tasks).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load automation tasks"})
	}
	return c.JSON(tasks)
}

// ListAdminTasks 返回全部用户最近的自动化任务。
func (h *AutomationHandler) ListAdminTasks(c *fiber.Ctx) error {
	var tasks []model.AutomationTask
	if err := h.DB.Order("created_at desc").
		Limit(pageSize(c)).
		Find(&tasks).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load automation tasks"})
	}
	return c.JSON(tasks)
}

// ListAdminAudits 返回全部用户最近的自动化审计记录。
func (h *AutomationHandler) ListAdminAudits(c *fiber.Ctx) error {
	var audits []model.AutomationAudit
	if err := h.DB.Order("created_at desc").
		Limit(pageSize(c)).
		Find(&audits).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load automation audits"})
	}
	return c.JSON(audits)
}

// ListAdminPlugins 返回叠加管理员启停覆盖值后的插件目录。
func (h *AutomationHandler) ListAdminPlugins(c *fiber.Ctx) error {
	catalog, err := h.loadPluginCatalog()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load plugin catalog"})
	}
	catalog = automation.ApplyPluginSettings(catalog, h.pluginSettings())
	enabled := false
	pluginsDir := ""
	if h.Config != nil {
		enabled = h.Config.AutomationEnabled
		pluginsDir = h.Config.AutomationPluginsDir
	}
	if catalog.Root != "" {
		pluginsDir = catalog.Root
	}
	return c.JSON(fiber.Map{
		"enabled":     enabled,
		"plugins_dir": pluginsDir,
		"stats":       catalog.Stats,
		"plugins":     catalog.Plugins,
	})
}

// SetAdminPluginEnabled 保存插件启停状态的管理员覆盖值。
func (h *AutomationHandler) SetAdminPluginEnabled(c *fiber.Ctx) error {
	pluginID := strings.TrimSpace(c.Params("id"))
	if pluginID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing plugin id"})
	}
	if h.Engine == nil {
		plugin, ok := h.pluginByID(pluginID)
		if !ok {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "plugin not found"})
		}
		pluginID = plugin.Manifest.ID
	} else if plugin, ok := h.Engine.PluginByID(pluginID); ok {
		pluginID = plugin.Manifest.ID
	} else {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "plugin not found"})
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid body"})
	}
	setting := model.AutomationPluginSetting{PluginID: pluginID, EnabledOverride: &body.Enabled}
	if err := h.DB.Where("plugin_id = ?", pluginID).Assign(model.AutomationPluginSetting{EnabledOverride: &body.Enabled}).FirstOrCreate(&setting).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to save plugin setting"})
	}
	return c.JSON(fiber.Map{
		"plugin_id": pluginID,
		"enabled":   body.Enabled,
	})
}

func (h *AutomationHandler) loadPluginCatalog() (automation.PluginCatalog, error) {
	if h.Engine != nil {
		return h.Engine.PluginCatalog(), nil
	}
	if h.Config == nil || strings.TrimSpace(h.Config.AutomationPluginsDir) == "" {
		return automation.PluginCatalog{Plugins: []automation.PluginInfo{}}, nil
	}
	return automation.LoadPluginCatalog(h.Config.AutomationPluginsDir)
}

func (h *AutomationHandler) pluginByID(pluginID string) (automation.PluginInfo, bool) {
	catalog, err := h.loadPluginCatalog()
	if err != nil {
		return automation.PluginInfo{}, false
	}
	pluginID = strings.TrimSpace(pluginID)
	for _, plugin := range catalog.Plugins {
		if plugin.Manifest.ID == pluginID {
			return plugin, true
		}
	}
	return automation.PluginInfo{}, false
}

// PluginsPage 渲染管理员插件 registry 页面。
func (h *AutomationHandler) PluginsPage(c *fiber.Ctx) error {
	return c.Render("web/templates/plugins", fiber.Map{
		"username": c.Locals("username"),
	})
}

func (h *AutomationHandler) pluginSettings() map[string]*bool {
	out := map[string]*bool{}
	if h == nil || h.DB == nil {
		return out
	}
	var settings []model.AutomationPluginSetting
	if err := h.DB.Find(&settings).Error; err != nil {
		return out
	}
	for _, setting := range settings {
		if setting.PluginID == "" {
			continue
		}
		out[setting.PluginID] = setting.EnabledOverride
	}
	return out
}

// TrustedCopy 只对剪贴板载荷执行自动化评估，不进行同步广播。
func (h *AutomationHandler) TrustedCopy(c *fiber.Ctx) error {
	if h.Config == nil || !h.Config.AutomationTrustedCopyEnabled {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "automation copy disabled"})
	}
	if h.Engine == nil || !h.Config.AutomationEnabled {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{"error": "automation engine disabled"})
	}
	if h.Config.AutomationCopyMaxBytes > 0 && int64(len(c.Body())) > h.Config.AutomationCopyMaxBytes {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "automation copy too large", "max_bytes": h.Config.AutomationCopyMaxBytes})
	}
	var body protocol.ClipboardData
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid automation copy body"})
	}
	username, _ := c.Locals("username").(string)
	event := automation.NewClipboardEvent(username, c.IP(), body, false, string(c.Body()))
	event.Transport = "automation_copy"
	decision := h.Engine.Evaluate(context.Background(), event)
	return c.JSON(fiber.Map{
		"event_id": event.EventID,
		"outcome":  decision.Outcome,
		"notices":  decision.Notices,
	})
}

func pageSize(c *fiber.Ctx) int {
	n, err := strconv.Atoi(c.Query("pageSize", "50"))
	if err != nil || n <= 0 {
		return 50
	}
	if n > 200 {
		return 200
	}
	return n
}
