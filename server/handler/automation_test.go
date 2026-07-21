package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/clipcascade/server/automation"
	"github.com/clipcascade/server/config"
	"github.com/gofiber/fiber/v2"
)

func TestListAdminPluginsReturnsEmptyCatalogWhenEngineDisabled(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	expectedDir := filepath.Join(filepath.Dir(exe), "./plugins")
	h := NewAutomationHandler(newTestDB(t), &config.Config{
		AutomationEnabled:    false,
		AutomationPluginsDir: "./plugins",
	}, nil)

	app := fiber.New()
	app.Get("/api/admin/automation/plugins", h.ListAdminPlugins)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/automation/plugins", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var body struct {
		Enabled    bool                    `json:"enabled"`
		PluginsDir string                  `json:"plugins_dir"`
		Stats      automation.PluginStats  `json:"stats"`
		Plugins    []automation.PluginInfo `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Enabled {
		t.Fatal("automation should be reported disabled")
	}
	if body.PluginsDir != expectedDir {
		t.Fatalf("unexpected plugins dir: %s", body.PluginsDir)
	}
	if len(body.Plugins) != 0 || body.Stats.Total != 0 {
		t.Fatalf("expected empty catalog, got stats=%+v plugins=%d", body.Stats, len(body.Plugins))
	}
}

func TestListAdminPluginsScansCatalogWhenEngineDisabled(t *testing.T) {
	root := t.TempDir()
	writeHandlerPlugin(t, root)
	h := NewAutomationHandler(newTestDB(t), &config.Config{
		AutomationEnabled:    false,
		AutomationPluginsDir: root,
	}, nil)

	app := fiber.New()
	app.Get("/api/admin/automation/plugins", h.ListAdminPlugins)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/automation/plugins", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var body struct {
		Enabled    bool                    `json:"enabled"`
		PluginsDir string                  `json:"plugins_dir"`
		Stats      automation.PluginStats  `json:"stats"`
		Plugins    []automation.PluginInfo `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Enabled {
		t.Fatal("automation should be globally disabled")
	}
	if body.PluginsDir != root {
		t.Fatalf("unexpected plugins dir: %s", body.PluginsDir)
	}
	if body.Stats.Loaded != 1 || len(body.Plugins) != 1 {
		t.Fatalf("expected one discovered plugin, got stats=%+v plugins=%d", body.Stats, len(body.Plugins))
	}
}

func TestListAdminPluginsReturnsLoadedCatalog(t *testing.T) {
	root := t.TempDir()
	writeHandlerPlugin(t, root)
	db := newTestDB(t)
	cfg := &config.Config{
		AutomationEnabled:               true,
		AutomationMode:                  "observe",
		AutomationPluginsDir:            root,
		AutomationPluginTimeoutSeconds:  5,
		AutomationPluginMaxOutputBytes:  64 * 1024,
		AutomationCommandTimeoutSeconds: 5,
		AutomationCommandMaxOutputBytes: 64 * 1024,
	}
	engine, err := automation.NewEngine(db, cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	h := NewAutomationHandler(db, cfg, engine)

	app := fiber.New()
	app.Get("/api/admin/automation/plugins", h.ListAdminPlugins)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/automation/plugins", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var body struct {
		Enabled bool                    `json:"enabled"`
		Stats   automation.PluginStats  `json:"stats"`
		Plugins []automation.PluginInfo `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Enabled {
		t.Fatal("automation should be reported enabled")
	}
	if body.Stats.Loaded != 1 || len(body.Plugins) != 1 {
		t.Fatalf("expected one loaded plugin, got stats=%+v plugins=%d", body.Stats, len(body.Plugins))
	}
	if body.Plugins[0].Manifest.Repository != nil {
		t.Fatal("repository should be optional")
	}
}

func TestSetAdminPluginEnabledStoresOverrideWhenEngineDisabled(t *testing.T) {
	root := t.TempDir()
	writeHandlerPlugin(t, root)
	db := newTestDB(t)
	h := NewAutomationHandler(db, &config.Config{
		AutomationEnabled:    false,
		AutomationPluginsDir: root,
	}, nil)

	app := fiber.New()
	app.Put("/api/admin/automation/plugins/:id/enabled", h.SetAdminPluginEnabled)
	app.Get("/api/admin/automation/plugins", h.ListAdminPlugins)

	req := httptest.NewRequest(http.MethodPut, "/api/admin/automation/plugins/example.handler-plugin/enabled", bytes.NewReader([]byte(`{"enabled":false}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/automation/plugins", nil)
	listResp, err := app.Test(listReq, -1)
	if err != nil {
		t.Fatalf("list request failed: %v", err)
	}
	defer listResp.Body.Close()
	var body struct {
		Stats   automation.PluginStats  `json:"stats"`
		Plugins []automation.PluginInfo `json:"plugins"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&body); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if body.Stats.Disabled != 1 || body.Plugins[0].EffectiveEnabled {
		t.Fatalf("expected disabled override, got stats=%+v plugin=%+v", body.Stats, body.Plugins[0])
	}
}

func TestSetAdminPluginEnabledStoresOverride(t *testing.T) {
	root := t.TempDir()
	writeHandlerPlugin(t, root)
	db := newTestDB(t)
	cfg := &config.Config{
		AutomationEnabled:               true,
		AutomationMode:                  "observe",
		AutomationPluginsDir:            root,
		AutomationPluginTimeoutSeconds:  5,
		AutomationPluginMaxOutputBytes:  64 * 1024,
		AutomationCommandTimeoutSeconds: 5,
		AutomationCommandMaxOutputBytes: 64 * 1024,
	}
	engine, err := automation.NewEngine(db, cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	h := NewAutomationHandler(db, cfg, engine)

	app := fiber.New()
	app.Put("/api/admin/automation/plugins/:id/enabled", h.SetAdminPluginEnabled)
	app.Get("/api/admin/automation/plugins", h.ListAdminPlugins)

	req := httptest.NewRequest(http.MethodPut, "/api/admin/automation/plugins/example.handler-plugin/enabled", bytes.NewReader([]byte(`{"enabled":false}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/automation/plugins", nil)
	listResp, err := app.Test(listReq, -1)
	if err != nil {
		t.Fatalf("list request failed: %v", err)
	}
	defer listResp.Body.Close()
	var body struct {
		Stats   automation.PluginStats  `json:"stats"`
		Plugins []automation.PluginInfo `json:"plugins"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&body); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if body.Stats.Disabled != 1 || body.Plugins[0].EffectiveEnabled {
		t.Fatalf("expected disabled override, got stats=%+v plugin=%+v", body.Stats, body.Plugins[0])
	}
}

func writeHandlerPlugin(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "handler-plugin")
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "handler-plugin"), 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	writeHandlerWASMPlugin(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Handler Plugin\n\nUsed by handler tests.\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	manifest := `{
		// 处理器测试插件使用 JSONC 清单。
		"publisher": "example",
		"name": "handler-plugin",
		"displayName": "Handler Plugin",
		"description": "Loaded by automation handler tests.",
		"version": "0.1.0",
		"engines": {"clipcascade": ">=2.0.0", "wasi": "wasip1"},
		"clipcascade": {
			"schemaVersion": 1,
			"enabled": true,
			"entrypoint": {"type": "wasi", "path": "./handler-plugin.wasm"},
			"capabilities": ["content_redact"]
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, automation.PluginManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func writeHandlerWASMPlugin(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module handlerplugin\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	source := `package main

import (
	"encoding/json"
	"os"
)

func main() {
	// 处理器测试插件只返回成功状态。
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "succeeded"})
}
`
	if err := os.WriteFile(filepath.Join(dir, "cmd", "handler-plugin", "main.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", filepath.Join(dir, "handler-plugin.wasm"), "./cmd/handler-plugin")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build handler wasm plugin: %v\n%s", err, string(out))
	}
}
