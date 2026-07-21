package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesRuntimeRelativePathsFromExecutableDir(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	baseDir := filepath.Dir(exe)

	t.Setenv("CC_DATABASE_PATH", "./database/custom.db")
	t.Setenv("CC_FILE_RELAY_DIR", "./relay")
	t.Setenv("CC_AUTOMATION_RULES_PATH", "./rules/clipboard.jsonc")
	t.Setenv("CC_AUTOMATION_COMMANDS_PATH", "./commands/allowlist.jsonc")
	t.Setenv("CC_AUTOMATION_PLUGINS_DIR", "./plugins")

	cfg := Load()
	expected := map[string]string{
		"database": cfg.DatabasePath,
		"relay":    cfg.FileRelayDir,
		"rules":    cfg.AutomationRulesPath,
		"commands": cfg.AutomationCommandsPath,
		"plugins":  cfg.AutomationPluginsDir,
	}
	want := map[string]string{
		"database": filepath.Join(baseDir, "./database/custom.db"),
		"relay":    filepath.Join(baseDir, "./relay"),
		"rules":    filepath.Join(baseDir, "./rules/clipboard.jsonc"),
		"commands": filepath.Join(baseDir, "./commands/allowlist.jsonc"),
		"plugins":  filepath.Join(baseDir, "./plugins"),
	}
	for name, got := range expected {
		if got != want[name] {
			t.Fatalf("%s path = %q, want %q", name, got, want[name])
		}
	}
}

func TestLoadKeepsAbsolutePathsAndSQLiteFileDSN(t *testing.T) {
	absolutePlugins := filepath.Join(t.TempDir(), "plugins")

	t.Setenv("CC_DATABASE_PATH", "file:clipcascade.db?cache=shared")
	t.Setenv("CC_AUTOMATION_PLUGINS_DIR", absolutePlugins)

	cfg := Load()
	if cfg.DatabasePath != "file:clipcascade.db?cache=shared" {
		t.Fatalf("database path = %q", cfg.DatabasePath)
	}
	if cfg.AutomationPluginsDir != absolutePlugins {
		t.Fatalf("plugins dir = %q, want %q", cfg.AutomationPluginsDir, absolutePlugins)
	}
}
