package automation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/clipcascade/pkg/constants"
	"github.com/clipcascade/server/config"
	"github.com/clipcascade/server/model"
)

func TestLoadPluginsReadsManifestReadmeAndOptionalRepository(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "example", "content-redact", true, "content_redact", true)

	catalog, err := loadPlugins(root)
	if err != nil {
		t.Fatalf("loadPlugins: %v", err)
	}
	if catalog.Stats.Total != 1 || catalog.Stats.Loaded != 1 || catalog.Stats.Enabled != 1 {
		t.Fatalf("unexpected stats: %+v", catalog.Stats)
	}
	if catalog.Root != root {
		t.Fatalf("unexpected plugin root: %s", catalog.Root)
	}
	plugin := catalog.Plugins[0]
	if plugin.Manifest.Name != "content-redact" {
		t.Fatalf("unexpected plugin name: %s", plugin.Manifest.Name)
	}
	if plugin.Manifest.Repository != nil {
		t.Fatal("repository should be optional and absent")
	}
	if !strings.Contains(plugin.ReadmeSummary, "Content Redact") {
		t.Fatalf("README summary not loaded: %q", plugin.ReadmeSummary)
	}
}

func TestLoadPluginAcceptsVSCodeStyleManifest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "content-redact")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Content Redaction\n\nVS Code-style manifest.\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "content-redact.wasm"), []byte("placeholder"), 0o644); err != nil {
		t.Fatalf("write wasm placeholder: %v", err)
	}
	manifest := `{
		// 顶层字段参考 VS Code extension manifest。
		"publisher": "example",
		"name": "content-redact",
		"displayName": "Content Redaction",
		"description": "Detects and redacts sensitive clipboard text.",
		"version": "0.1.0",
		"license": "MIT",
		"categories": ["Automation", "Security"],
		"keywords": ["clipboard", "privacy"],
		"engines": {"clipcascade": ">=2.0.0", "wasi": "wasip1"},
		"activationEvents": ["onClipboard:text", "onCapability:content_redact"],
		"repository": "https://github.com/example/content-redact",
		"author": "Example",
		"contributes": {
			"automation": {
				"capabilities": ["content_redact"],
				"rules": [],
				"commands": []
			}
		},
		// ClipCascade 专属执行字段必须放在 clipcascade 块中。
		"clipcascade": {
			"schemaVersion": 1,
			"enabled": true,
			"entrypoint": {"type": "wasi", "path": "./content-redact.wasm"},
			"capabilities": ["content_redact"],
			"runtime": {"timeout_seconds": 5, "max_output_bytes": 65536}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, PluginManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	info := loadPlugin(dir)
	if info.Status != PluginStatusLoaded {
		t.Fatalf("expected loaded, got %s: %s", info.Status, info.Error)
	}
	if info.Manifest.ID != "example.content-redact" {
		t.Fatalf("unexpected plugin id: %s", info.Manifest.ID)
	}
	if info.Manifest.DisplayName != "Content Redaction" {
		t.Fatalf("unexpected display name: %s", info.Manifest.DisplayName)
	}
	if info.Manifest.Repository == nil || info.Manifest.Repository.URL == "" {
		t.Fatalf("repository string was not normalized: %#v", info.Manifest.Repository)
	}
	if info.Manifest.Author == nil || info.Manifest.Author.Name != "Example" {
		t.Fatalf("author string was not normalized: %#v", info.Manifest.Author)
	}
}

func TestRepositoryJSONCFilesParse(t *testing.T) {
	root := filepath.Join("..", "..", "plugins")
	manifestPaths := []string{filepath.Join(root, "_templates", "go-wasi", PluginManifestFile)}
	officialEntries, err := os.ReadDir(filepath.Join(root, "official"))
	if err != nil {
		t.Fatalf("read official plugins: %v", err)
	}
	for _, entry := range officialEntries {
		if !entry.IsDir() {
			continue
		}
		manifestPaths = append(manifestPaths, filepath.Join(root, "official", entry.Name(), PluginManifestFile))
	}
	for _, manifestPath := range manifestPaths {
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatalf("read manifest %s: %v", manifestPath, err)
		}
		if err := validatePluginManifestKeys(data); err != nil {
			t.Fatalf("validate manifest keys %s: %v", manifestPath, err)
		}
		var manifest PluginManifest
		if err := unmarshalJSONC(data, &manifest); err != nil {
			t.Fatalf("parse manifest %s: %v", manifestPath, err)
		}
		normalizePluginManifest(&manifest)
		if err := validatePluginManifest(manifest); err != nil {
			t.Fatalf("validate manifest %s: %v", manifestPath, err)
		}
	}

	schemaPath := filepath.Join(root, "clipcascade.plugin.schema.jsonc")
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema map[string]any
	if err := unmarshalJSONC(schemaData, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
}

func TestLoadPluginRejectsOldTopLevelFields(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "old")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Old\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, PluginManifestFile), []byte(`{
		// 旧版顶层 id 不再允许。
		"id": "old",
		"publisher": "example",
		"name": "old",
		"description": "Old manifest.",
		"version": "0.1.0",
		"engines": {"clipcascade": ">=2.0.0"},
		"clipcascade": {
			"schemaVersion": 1,
			"enabled": true,
			"entrypoint": {"type": "wasi", "path": "./old.wasm"},
			"capabilities": ["content_redact"]
		}
	}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	info := loadPlugin(dir)
	if info.Status != PluginStatusInvalid {
		t.Fatalf("expected invalid, got %s", info.Status)
	}
	if !strings.Contains(info.Error, "不允许顶层字段 id") {
		t.Fatalf("unexpected error: %s", info.Error)
	}
}

func TestLoadPluginRequiresReadme(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bad")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, PluginManifestFile), []byte(`{
		// 缺少 README 时，插件应保持 invalid。
		"publisher": "example",
		"name": "bad",
		"description": "Missing README.",
		"version": "0.1.0",
		"engines": {"clipcascade": ">=2.0.0"},
		"clipcascade": {
			"schemaVersion": 1,
			"enabled": true,
			"entrypoint": {"type": "wasi", "path": "./bad.wasm"},
			"capabilities": ["content_redact"]
		}
	}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	info := loadPlugin(dir)
	if info.Status != PluginStatusInvalid {
		t.Fatalf("expected invalid, got %s", info.Status)
	}
	if !strings.Contains(info.Error, "README.md is required") {
		t.Fatalf("unexpected error: %s", info.Error)
	}
}

func TestPluginEntryCannotEscapeDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "escape")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Escape\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, PluginManifestFile), []byte(`{
		// 入口文件不能逃逸插件目录。
		"publisher": "example",
		"name": "escape",
		"description": "Escapes plugin dir.",
		"version": "0.1.0",
		"engines": {"clipcascade": ">=2.0.0"},
		"clipcascade": {
			"schemaVersion": 1,
			"enabled": true,
			"entrypoint": {"type": "wasi", "path": "../outside.wasm"},
			"capabilities": ["content_redact"]
		}
	}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	info := loadPlugin(dir)
	if info.Status != PluginStatusInvalid {
		t.Fatalf("expected invalid, got %s", info.Status)
	}
	if !strings.Contains(info.Error, "inside plugin directory") {
		t.Fatalf("unexpected error: %s", info.Error)
	}
}

func TestRunPluginActionCreatesSucceededTask(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "example", "content-redact", true, "content_redact", true)
	db := testDB(t)
	cfg := &config.Config{
		AutomationEnabled:               true,
		AutomationMode:                  "observe",
		AutomationPluginsDir:            root,
		AutomationPluginTimeoutSeconds:  5,
		AutomationPluginMaxOutputBytes:  64 * 1024,
		AutomationCommandTimeoutSeconds: 5,
		AutomationCommandMaxOutputBytes: 64 * 1024,
		AutomationTrustedCopyEnabled:    false,
		AutomationCopyMaxBytes:          256 * 1024,
	}
	engine, err := NewEngine(db, cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	engine.rules = []Rule{
		{
			ID:       "redact-candidate",
			Enabled:  true,
			Priority: 1,
			When: Condition{All: []Condition{
				{Field: "clipboard.type", Op: "eq", Value: constants.TypeText},
			}},
			Actions: []RuleAction{
				{Type: ActionRunPlugin, PluginID: "example.content-redact", Params: map[string]any{"mode": "placeholder"}},
			},
		},
	}

	event := ClipboardEvent{
		EventID:   "evt-plugin",
		Username:  "alice",
		Transport: "websocket",
		Clipboard: ClipboardSummary{Type: constants.TypeText, TextPreview: "secret token"},
	}
	decision := engine.Evaluate(context.Background(), event)
	if decision.Outcome != OutcomeAllow {
		t.Fatalf("unexpected outcome: %s", decision.Outcome)
	}

	var task model.AutomationTask
	// 插件是 WASI 解释执行（wazero），-race 下会慢近一个数量级；这里的等待上限必须
	// 明显大于 cfg.AutomationPluginTimeoutSeconds（本用例为 5s），否则 -race 下
	// 必然超时（本机实测：单独跑约 17s，整套并行跑更久）。
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		err = db.Where("event_id = ?", "evt-plugin").First(&task).Error
		if err == nil && task.Status == model.AutomationTaskSucceeded {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if task.Status != model.AutomationTaskSucceeded {
		t.Fatalf("expected succeeded task, got status=%q err=%v task=%+v", task.Status, err, task)
	}
	if !strings.Contains(task.ResultJSON, "redaction_candidate") {
		t.Fatalf("expected plugin result in task, got %s", task.ResultJSON)
	}
}

func writePlugin(t *testing.T, root, publisher, id string, enabled bool, capability string, executable bool) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(filepath.Join(dir, "cmd", id), 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	if executable {
		writeTestWASMPlugin(t, dir, id)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Content Redact\n\nPlaceholder redaction plugin.\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	manifest := `{
		// publisher 和 name 组成规范 ID。
		"publisher": "` + publisher + `",
		"name": "` + id + `",
		"displayName": "Content Redact",
		"description": "Detects and redacts sensitive clipboard text.",
		"version": "0.1.0",
		"engines": {"clipcascade": ">=2.0.0", "wasi": "wasip1"},
		"clipcascade": {
			"schemaVersion": 1,
			"enabled": ` + boolLiteral(enabled) + `,
			"entrypoint": {"type": "wasi", "path": "./` + id + `.wasm"},
			"capabilities": ["` + capability + `"],
			"runtime": {"timeout_seconds": 5, "max_output_bytes": 65536}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, PluginManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func writeTestWASMPlugin(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testplugin\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	mainPath := filepath.Join(dir, "cmd", id, "main.go")
	source := `package main

import (
	"encoding/json"
	"io"
	"os"
)

type output struct {
	Status string ` + "`json:\"status\"`" + `
	Tags []string ` + "`json:\"tags\"`" + `
	Result map[string]any ` + "`json:\"result\"`" + `
}

func main() {
	// 测试插件只验证 WASI 调用链路，不处理输入内容。
	_, _ = io.ReadAll(os.Stdin)
	_ = json.NewEncoder(os.Stdout).Encode(output{
		Status: "succeeded",
		Tags: []string{"redaction_candidate"},
		Result: map[string]any{"reserved": true},
	})
}
`
	if err := os.WriteFile(mainPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", filepath.Join(dir, id+".wasm"), "./cmd/"+id)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build wasm plugin: %v\n%s", err, string(out))
	}
}

func boolLiteral(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
