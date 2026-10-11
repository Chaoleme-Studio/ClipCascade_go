package automation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/clipcascade/server/config"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/clipcascade/server/model"
)

const (
	// PluginManifestFile 是每个插件包必须提供的 JSONC 清单文件名。
	PluginManifestFile = "clipcascade.plugin.jsonc"

	// PluginStatusLoaded 表示插件清单、README 和 WASI 入口都可用。
	PluginStatusLoaded = "loaded"
	// PluginStatusDisabled 表示插件有效，但被清单或管理员开关禁用。
	PluginStatusDisabled = "disabled"
	// PluginStatusInvalid 表示插件包没有通过清单或 README 校验。
	PluginStatusInvalid = "invalid"
	// PluginStatusMissingEntry 表示清单有效，但声明的入口文件不存在。
	PluginStatusMissingEntry = "missing_entry"

	// PluginCapabilityURLDownloadCandidate 预留 URL 下载候选提取能力。
	PluginCapabilityURLDownloadCandidate = "url_download_candidate"
	// PluginCapabilityImageSensitiveScan 预留图片敏感信息检测能力。
	PluginCapabilityImageSensitiveScan = "image_sensitive_scan"
	// PluginCapabilityOCRScan 预留 OCR 检测能力。
	PluginCapabilityOCRScan = "ocr_scan"
	// PluginCapabilityContentRedact 预留内容脱敏或替换能力。
	PluginCapabilityContentRedact = "content_redact"
	// PluginCapabilitySecretDetect 预留密钥、Token、连接串等凭证候选检测能力。
	PluginCapabilitySecretDetect = "secret_detect"
	// PluginCapabilityResultToClipboard 预留将结果回填剪贴板的能力。
	PluginCapabilityResultToClipboard = "result_to_clipboard"
	// PluginCapabilityResultToFileRelay 预留将结果作为文件 relay 的能力。
	PluginCapabilityResultToFileRelay = "result_to_file_relay"
)

var (
	pluginNamePattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-_.]*$`)
	pluginPublisherPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-_.]*$`)
	pluginSemverPattern    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$`)
)

// PluginManifest 描述一个 ClipCascade 插件包。
//
// 顶层字段参考 VS Code extension manifest，用于插件展示、作者信息和能力声明。
// ClipCascade 专属执行字段必须放在 clipcascade 块中；旧版顶层执行字段不再接受。
type PluginManifest struct {
	ID          string `json:"id,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
	License     string `json:"license,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Markdown    string `json:"markdown,omitempty"`
	Pricing     string `json:"pricing,omitempty"`
	Preview     bool   `json:"preview,omitempty"`

	Categories            []string          `json:"categories,omitempty"`
	Keywords              []string          `json:"keywords,omitempty"`
	ActivationEvents      []string          `json:"activationEvents,omitempty"`
	ExtensionDependencies []string          `json:"extensionDependencies,omitempty"`
	ExtensionPack         []string          `json:"extensionPack,omitempty"`
	ExtensionKind         []string          `json:"extensionKind,omitempty"`
	Badges                []PluginBadge     `json:"badges,omitempty"`
	GalleryBanner         *PluginBanner     `json:"galleryBanner,omitempty"`
	Sponsor               *PluginSponsor    `json:"sponsor,omitempty"`
	Bugs                  *PluginBugs       `json:"bugs,omitempty"`
	Repository            *PluginRepository `json:"repository,omitempty"`
	Author                *PluginAuthor     `json:"author,omitempty"`
	Engines               PluginEngines     `json:"engines,omitempty"`
	Scripts               map[string]string `json:"scripts,omitempty"`
	Metadata              map[string]any    `json:"metadata,omitempty"`

	Main        string            `json:"main,omitempty"`
	Browser     string            `json:"browser,omitempty"`
	Contributes PluginContributes `json:"contributes,omitempty"`

	ClipCascade PluginPlatform `json:"clipcascade,omitempty"`
}

// PluginEntrypoint 标识按需加载的可执行插件产物。
type PluginEntrypoint struct {
	Type string `json:"type,omitempty"`
	Path string `json:"path,omitempty"`
}

// PluginRepository 描述插件源码仓库。
type PluginRepository struct {
	Type      string `json:"type,omitempty"`
	URL       string `json:"url,omitempty"`
	Directory string `json:"directory,omitempty"`
}

// PluginAuthor 描述插件作者或所属组织。
type PluginAuthor struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// PluginRuntime 定义单次插件调用的资源限制。
type PluginRuntime struct {
	TimeoutSeconds int   `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int64 `json:"max_output_bytes,omitempty"`
}

// PluginEngines 声明插件运行环境兼容范围。
type PluginEngines struct {
	ClipCascade string `json:"clipcascade,omitempty"`
	WASI        string `json:"wasi,omitempty"`
	VSCode      string `json:"vscode,omitempty"`
}

// PluginBadge 描述插件市场样式的徽章信息。
type PluginBadge struct {
	URL         string `json:"url,omitempty"`
	Href        string `json:"href,omitempty"`
	Description string `json:"description,omitempty"`
}

// PluginBanner 描述插件市场横幅样式信息。
type PluginBanner struct {
	Color string `json:"color,omitempty"`
	Theme string `json:"theme,omitempty"`
}

// PluginSponsor 描述插件赞助链接。
type PluginSponsor struct {
	URL string `json:"url,omitempty"`
}

// PluginBugs 描述插件问题反馈入口。
type PluginBugs struct {
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

// PluginContributes 保留插件未来可贡献的自动化能力声明。
type PluginContributes map[string]json.RawMessage

// PluginPlatform 包含 ClipCascade 专属的执行字段。
type PluginPlatform struct {
	SchemaVersion int              `json:"schemaVersion,omitempty"`
	Enabled       *bool            `json:"enabled,omitempty"`
	Entrypoint    PluginEntrypoint `json:"entrypoint,omitempty"`
	Capabilities  []string         `json:"capabilities,omitempty"`
	Runtime       PluginRuntime    `json:"runtime,omitempty"`
}

// PluginInfo 是服务端发现到的单个插件包视图。
type PluginInfo struct {
	Manifest         PluginManifest `json:"manifest"`
	Dir              string         `json:"dir"`
	ManifestPath     string         `json:"manifest_path"`
	EntrypointPath   string         `json:"entrypoint_path"`
	ReadmePath       string         `json:"readme_path,omitempty"`
	ReadmeSummary    string         `json:"readme_summary,omitempty"`
	Status           string         `json:"status"`
	EffectiveEnabled bool           `json:"effective_enabled"`
	EnabledOverride  *bool          `json:"enabled_override,omitempty"`
	Error            string         `json:"error,omitempty"`
}

// PluginStats 汇总当前插件目录状态，供管理员页面展示。
type PluginStats struct {
	Total        int `json:"total"`
	Enabled      int `json:"enabled"`
	Loaded       int `json:"loaded"`
	Disabled     int `json:"disabled"`
	Invalid      int `json:"invalid"`
	Capabilities int `json:"capabilities"`
}

// PluginCatalog 是包含全部插件包的稳定 API 载荷。
type PluginCatalog struct {
	Root    string       `json:"root,omitempty"`
	Stats   PluginStats  `json:"stats"`
	Plugins []PluginInfo `json:"plugins"`
}

// PluginInput 是通过 stdin 传给 WASI 插件的机器载荷。
type PluginInput struct {
	Event  ClipboardEvent `json:"event"`
	Action RuleAction     `json:"action"`
	Params map[string]any `json:"params,omitempty"`
}

// PluginOutput 是 WASI 插件通过 stdout 返回的机器载荷。
type PluginOutput struct {
	Status           string         `json:"status"`
	Message          string         `json:"message,omitempty"`
	Tags             []string       `json:"tags,omitempty"`
	Result           map[string]any `json:"result,omitempty"`
	ReplaceClipboard map[string]any `json:"replace_clipboard,omitempty"`
	RelayFile        map[string]any `json:"relay_file,omitempty"`
}

// UnmarshalJSON 支持 VS Code/npm 风格的字符串仓库或对象仓库。
func (r *PluginRepository) UnmarshalJSON(data []byte) error {
	var repoURL string
	if err := json.Unmarshal(data, &repoURL); err == nil {
		r.URL = strings.TrimSpace(repoURL)
		return nil
	}
	type alias PluginRepository
	var value alias
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*r = PluginRepository(value)
	return nil
}

// UnmarshalJSON 支持 VS Code/npm 风格的字符串作者或对象作者。
func (a *PluginAuthor) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		a.Name = strings.TrimSpace(name)
		return nil
	}
	type alias PluginAuthor
	var value alias
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*a = PluginAuthor(value)
	return nil
}

// LoadPluginCatalog 扫描插件目录并返回可展示的插件目录。
//
// 这个函数只负责发现和校验插件包，不代表自动化引擎已经启用。
func LoadPluginCatalog(root string) (PluginCatalog, error) {
	root = strings.TrimSpace(root)
	catalog := PluginCatalog{Plugins: []PluginInfo{}}
	if root == "" {
		return catalog, nil
	}
	resolvedRoot, err := resolvePluginRoot(root)
	if err != nil {
		return catalog, err
	}
	catalog.Root = resolvedRoot
	plugins, err := scanPluginRoot(resolvedRoot)
	if err != nil {
		return catalog, err
	}
	catalog.Plugins = plugins
	sortPluginCatalog(catalog.Plugins)
	catalog.Stats = pluginStats(catalog.Plugins)
	return catalog, nil
}

func loadPlugins(root string) (PluginCatalog, error) {
	return LoadPluginCatalog(root)
}

func resolvePluginRoot(root string) (string, error) {
	if filepath.IsAbs(root) {
		return root, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), root), nil
}

func scanPluginRoot(root string) ([]PluginInfo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	plugins := []PluginInfo{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		manifestPath := filepath.Join(root, name, PluginManifestFile)
		if _, err := os.Stat(manifestPath); err != nil {
			continue
		}
		info := loadPlugin(filepath.Join(root, name))
		plugins = append(plugins, info)
	}
	return plugins, nil
}

func sortPluginCatalog(plugins []PluginInfo) {
	sort.SliceStable(plugins, func(i, j int) bool {
		return plugins[i].Manifest.ID < plugins[j].Manifest.ID
	})
}

func loadPlugin(dir string) PluginInfo {
	info := PluginInfo{
		Dir:          dir,
		ManifestPath: filepath.Join(dir, PluginManifestFile),
		ReadmePath:   filepath.Join(dir, "README.md"),
		Status:       PluginStatusInvalid,
	}
	data, err := os.ReadFile(info.ManifestPath)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	if err := validatePluginManifestKeys(data); err != nil {
		info.Error = err.Error()
		return info
	}
	if err := unmarshalJSONC(data, &info.Manifest); err != nil {
		info.Error = err.Error()
		return info
	}
	normalizePluginManifest(&info.Manifest)
	if err := validatePluginManifest(info.Manifest); err != nil {
		info.Error = err.Error()
		return info
	}
	readme, err := os.ReadFile(info.ReadmePath)
	if err != nil {
		info.Error = "README.md is required: " + err.Error()
		return info
	}
	info.ReadmeSummary = readmeSummary(string(readme), 800)
	entryPath, err := resolvePluginEntrypointPath(dir, info.Manifest.ClipCascade.Entrypoint.Path)
	if err != nil {
		info.Status = PluginStatusInvalid
		info.Error = err.Error()
		return info
	}
	info.EntrypointPath = entryPath
	stat, err := os.Stat(entryPath)
	if err != nil {
		info.Status = PluginStatusMissingEntry
		info.Error = err.Error()
		return info
	}
	if stat.IsDir() {
		info.Status = PluginStatusMissingEntry
		info.Error = "entrypoint path is a directory"
		return info
	}
	if !pluginEnabled(info.Manifest) {
		info.Status = PluginStatusDisabled
		info.EffectiveEnabled = false
		return info
	}
	info.Status = PluginStatusLoaded
	info.EffectiveEnabled = true
	return info
}

func normalizePluginManifest(manifest *PluginManifest) {
	if manifest == nil {
		return
	}
	manifest.ID = strings.TrimSpace(manifest.ID)
	manifest.Publisher = strings.TrimSpace(manifest.Publisher)
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.DisplayName = strings.TrimSpace(manifest.DisplayName)
	manifest.Description = strings.TrimSpace(manifest.Description)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.License = strings.TrimSpace(manifest.License)
	manifest.Homepage = strings.TrimSpace(manifest.Homepage)
	manifest.Icon = strings.TrimSpace(manifest.Icon)
	manifest.Markdown = strings.TrimSpace(manifest.Markdown)
	manifest.Pricing = strings.TrimSpace(manifest.Pricing)
	manifest.Main = strings.TrimSpace(manifest.Main)
	manifest.Browser = strings.TrimSpace(manifest.Browser)

	if manifest.DisplayName == "" {
		manifest.DisplayName = manifest.Name
	}
	if manifest.Publisher != "" && manifest.Name != "" {
		manifest.ID = manifest.Publisher + "." + manifest.Name
	}
	manifest.Categories = normalizeStringList(manifest.Categories)
	manifest.Keywords = normalizeStringList(manifest.Keywords)
	manifest.ActivationEvents = normalizeStringList(manifest.ActivationEvents)
	manifest.ExtensionDependencies = normalizeStringList(manifest.ExtensionDependencies)
	manifest.ExtensionPack = normalizeStringList(manifest.ExtensionPack)
	manifest.ExtensionKind = normalizeStringList(manifest.ExtensionKind)
	manifest.ClipCascade.Entrypoint.Type = strings.TrimSpace(manifest.ClipCascade.Entrypoint.Type)
	manifest.ClipCascade.Entrypoint.Path = strings.TrimSpace(manifest.ClipCascade.Entrypoint.Path)
	manifest.ClipCascade.Capabilities = normalizeStringList(manifest.ClipCascade.Capabilities)
}

func pluginEnabled(manifest PluginManifest) bool {
	return manifest.ClipCascade.Enabled == nil || *manifest.ClipCascade.Enabled
}

func validatePluginManifest(manifest PluginManifest) error {
	if manifest.ClipCascade.SchemaVersion != 1 {
		return fmt.Errorf("unsupported clipcascade.schemaVersion: %d", manifest.ClipCascade.SchemaVersion)
	}
	if manifest.Publisher == "" {
		return errors.New("publisher is required")
	}
	if !pluginPublisherPattern.MatchString(manifest.Publisher) {
		return errors.New("publisher must be lowercase and contain no spaces")
	}
	if manifest.Name == "" {
		return errors.New("name is required")
	}
	if !pluginNamePattern.MatchString(manifest.Name) {
		return errors.New("name must be lowercase and contain no spaces")
	}
	if manifest.ID != manifest.Publisher+"."+manifest.Name {
		return errors.New("computed id must match publisher.name")
	}
	if manifest.Description == "" {
		return errors.New("description is required")
	}
	if manifest.Version == "" {
		return errors.New("version is required")
	}
	if !pluginSemverPattern.MatchString(manifest.Version) {
		return errors.New("version must be SemVer compatible")
	}
	if manifest.Engines.ClipCascade == "" {
		return errors.New("engines.clipcascade is required")
	}
	if manifest.ClipCascade.Entrypoint.Type != "wasi" {
		return fmt.Errorf("unsupported clipcascade.entrypoint.type: %s", manifest.ClipCascade.Entrypoint.Type)
	}
	if manifest.ClipCascade.Entrypoint.Path == "" {
		return errors.New("clipcascade.entrypoint.path is required")
	}
	if len(manifest.ClipCascade.Capabilities) == 0 {
		return errors.New("clipcascade.capabilities are required")
	}
	return nil
}

func validatePluginManifestKeys(data []byte) error {
	var raw map[string]json.RawMessage
	if err := unmarshalJSONC(data, &raw); err != nil {
		return err
	}
	disallowed := []string{
		"id",
		"schema_version",
		"display_name",
		"enabled",
		"entrypoint",
		"capabilities",
		"runtime",
	}
	for _, key := range disallowed {
		if _, ok := raw[key]; ok {
			return fmt.Errorf("JSONC 插件清单不允许顶层字段 %s", key)
		}
	}
	return nil
}

func normalizeStringList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func resolvePluginEntrypointPath(dir string, entryPath string) (string, error) {
	if strings.TrimSpace(entryPath) == "" {
		return "", errors.New("entrypoint path is required")
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	resolved := entryPath
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(base, resolved)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", errors.New("entrypoint path must stay inside plugin directory")
	}
	return resolved, nil
}

func readmeSummary(text string, limit int) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" && len(out) == 0 {
			continue
		}
		if strings.HasPrefix(line, "#") {
			line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
		out = append(out, line)
		if line == "" && len(out) > 1 {
			break
		}
	}
	summary := strings.TrimSpace(strings.Join(out, " "))
	if summary == "" {
		summary = strings.TrimSpace(text)
	}
	runes := []rune(summary)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return summary
}

func pluginStats(plugins []PluginInfo) PluginStats {
	stats := PluginStats{Total: len(plugins)}
	capabilities := map[string]struct{}{}
	for _, plugin := range plugins {
		switch plugin.Status {
		case PluginStatusLoaded:
			stats.Loaded++
			stats.Enabled++
		case PluginStatusDisabled:
			stats.Disabled++
		default:
			stats.Invalid++
		}
		for _, cap := range plugin.Manifest.ClipCascade.Capabilities {
			if cap != "" {
				capabilities[cap] = struct{}{}
			}
		}
	}
	stats.Capabilities = len(capabilities)
	return stats
}

// PluginCatalog 返回引擎启动时发现的插件目录。
func (e *Engine) PluginCatalog() PluginCatalog {
	if e == nil {
		return PluginCatalog{Plugins: []PluginInfo{}}
	}
	return e.plugins
}

// PluginByID 按规范 ID 查找插件。
func (e *Engine) PluginByID(pluginID string) (PluginInfo, bool) {
	if e == nil {
		return PluginInfo{}, false
	}
	plugin, ok := e.pluginByID[strings.TrimSpace(pluginID)]
	return plugin, ok
}

// ApplyPluginSettings 将数据库中的启停覆盖值叠加到插件目录上。
func ApplyPluginSettings(catalog PluginCatalog, settings map[string]*bool) PluginCatalog {
	for i := range catalog.Plugins {
		plugin := &catalog.Plugins[i]
		if override, ok := settings[plugin.Manifest.ID]; ok {
			plugin.EnabledOverride = override
			if override != nil && !*override {
				plugin.EffectiveEnabled = false
				if plugin.Status == PluginStatusLoaded {
					plugin.Status = PluginStatusDisabled
				}
			}
			if override != nil && *override && plugin.Status == PluginStatusDisabled {
				plugin.EffectiveEnabled = true
				plugin.Status = PluginStatusLoaded
			}
		}
	}
	catalog.Stats = pluginStats(catalog.Plugins)
	return catalog
}

func (e *Engine) runPluginTask(event ClipboardEvent, action RuleAction, taskID string) (map[string]any, error) {
	if e == nil {
		return nil, errors.New("automation engine disabled")
	}
	requestedPluginID := strings.TrimSpace(action.PluginID)
	if requestedPluginID == "" {
		return nil, errors.New("plugin_id is required")
	}
	plugin, ok := e.pluginByID[requestedPluginID]
	if !ok {
		return nil, errors.New("plugin not found: " + requestedPluginID)
	}
	pluginID := plugin.Manifest.ID
	enabled := e.isPluginEnabled(pluginID)
	if !enabled {
		return nil, errors.New("plugin is disabled: " + pluginID)
	}
	if plugin.Status != PluginStatusLoaded && plugin.Status != PluginStatusDisabled {
		return nil, fmt.Errorf("plugin is not loaded: %s", plugin.Status)
	}
	timeout := time.Duration(e.cfg.AutomationPluginTimeoutSeconds) * time.Second
	if plugin.Manifest.ClipCascade.Runtime.TimeoutSeconds > 0 {
		timeout = time.Duration(plugin.Manifest.ClipCascade.Runtime.TimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	input := PluginInput{Event: event, Action: action, Params: action.Params}
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	stdout, stderr, err := runWASIPlugin(ctx, plugin.EntrypointPath, inputBytes, pluginOutputLimit(e.cfg, plugin.Manifest))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return map[string]any{"stdout": stdout, "stderr": stderr, "task_id": taskID}, errors.New("plugin timed out")
		}
		return map[string]any{"stdout": stdout, "stderr": stderr, "task_id": taskID}, err
	}
	var pluginOutput PluginOutput
	if err := json.Unmarshal([]byte(stdout), &pluginOutput); err != nil {
		return map[string]any{"stdout": stdout, "stderr": stderr, "task_id": taskID}, fmt.Errorf("invalid plugin output: %w", err)
	}
	if pluginOutput.Status == "" {
		pluginOutput.Status = "succeeded"
	}
	result := map[string]any{
		"plugin_id": pluginID,
		"status":    pluginOutput.Status,
		"message":   pluginOutput.Message,
		"tags":      pluginOutput.Tags,
		"result":    pluginOutput.Result,
		"task_id":   taskID,
	}
	if stderr != "" {
		result["stderr"] = stderr
	}
	if pluginOutput.ReplaceClipboard != nil {
		result["replace_clipboard"] = pluginOutput.ReplaceClipboard
		result["reserved"] = "clipboard replacement is recorded only in this phase"
	}
	if pluginOutput.RelayFile != nil {
		result["relay_file"] = pluginOutput.RelayFile
		result["reserved_relay"] = "file relay result is recorded only in this phase"
	}
	if pluginOutput.Status != "succeeded" && pluginOutput.Status != "ok" {
		return result, fmt.Errorf("plugin returned status: %s", pluginOutput.Status)
	}
	return result, nil
}

func (e *Engine) isPluginEnabled(pluginID string) bool {
	if e == nil || e.db == nil {
		return false
	}
	plugin, ok := e.pluginByID[strings.TrimSpace(pluginID)]
	if !ok {
		return false
	}
	pluginID = plugin.Manifest.ID
	var setting model.AutomationPluginSetting
	if err := e.db.Where("plugin_id = ?", pluginID).Limit(1).Find(&setting).Error; err == nil && setting.PluginID != "" {
		return setting.EnabledOverride == nil || *setting.EnabledOverride
	}
	return plugin.EffectiveEnabled
}

func runWASIPlugin(ctx context.Context, wasmPath string, input []byte, outputLimit int64) (string, string, error) {
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		return "", "", err
	}

	// 完整性校验：插件目录即信任边界，这里提供两层防护——
	// 1) 计算并记录 wasm 的 SHA-256（审计可追溯）；
	// 2) 若设置了 CC_PLUGIN_SHA256（逗号分隔白名单），未命中的插件拒绝执行。
	sum := sha256.Sum256(wasm)
	wasmHash := hex.EncodeToString(sum[:])
	slog.Debug("WASI 插件完整性", "path", filepath.Base(wasmPath), "sha256", wasmHash)
	if allowed := os.Getenv("CC_PLUGIN_SHA256"); allowed != "" {
		trusted := false
		for _, h := range strings.Split(allowed, ",") {
			if strings.EqualFold(strings.TrimSpace(h), wasmHash) {
				trusted = true
				break
			}
		}
		if !trusted {
			return "", "", fmt.Errorf("插件 sha256=%s 不在 CC_PLUGIN_SHA256 白名单中，拒绝执行", wasmHash[:16]+"...")
		}
	}

	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(context.Background())
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		return "", "", err
	}
	compiled, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		return "", "", err
	}
	defer compiled.Close(context.Background())

	var stdout limitedBuffer
	stdout.max = outputLimit
	var stderr limitedBuffer
	stderr.max = 8 * 1024
	config := wazero.NewModuleConfig().
		WithName("clipcascade-plugin").
		WithStdin(bytes.NewReader(input)).
		WithStdout(&stdout).
		WithStderr(&stderr)
	_, err = runtime.InstantiateModule(ctx, compiled, config)
	output := stdout.String()
	if stdout.truncated {
		output += "\n[truncated]"
	}
	return output, stderr.String(), err
}

func pluginOutputLimit(cfg *config.Config, manifest PluginManifest) int64 {
	limit := cfg.AutomationPluginMaxOutputBytes
	if manifest.ClipCascade.Runtime.MaxOutputBytes > 0 {
		limit = manifest.ClipCascade.Runtime.MaxOutputBytes
	}
	if limit <= 0 {
		limit = 64 * 1024
	}
	return limit
}
