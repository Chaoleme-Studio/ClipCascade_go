// Package config 为 ClipCascade server 提供基于环境变量的 config，
// 将 CC_* 环境变量映射到 Go struct。
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/clipcascade/pkg/constants"
)

// Config 保存所有 server config。
type Config struct {
	Port                            int
	MaxMessageSizeMiB               int
	MaxMessageSizeBytes             int64
	P2PEnabled                      bool
	P2PStunURL                      string
	AllowedOrigins                  string
	SignupEnabled                   bool
	MaxUserAccounts                 int
	AccountPurgeTimeout             int64 // 秒，-1 表示关闭
	SessionTimeoutMinutes           int
	MaxUniqueIPAttempts             int
	MaxAttemptsPerIP                int
	LockTimeoutSeconds              int
	LockScalingFactor               int
	BFACacheEnabled                 bool
	DatabasePath                    string
	FileRelayEnabled                bool
	FileRelayDir                    string
	FileRelayMaxBytes               int64
	FileRelayTTLSeconds             int64
	ExternalBrokerEnabled           bool
	BrokerHost                      string
	BrokerPort                      int
	AutomationEnabled               bool
	AutomationMode                  string
	AutomationRulesPath             string
	AutomationCommandsPath          string
	AutomationPluginsDir            string
	AutomationCommandTimeoutSeconds int
	AutomationCommandMaxOutputBytes int64
	AutomationPluginTimeoutSeconds  int
	AutomationPluginMaxOutputBytes  int64
	AutomationTrustedCopyEnabled    bool
	AutomationCopyMaxBytes          int64
}

// Load 从带有合理默认值的环境变量中读取 config。
func Load() *Config {
	baseDir := executableDir()
	c := &Config{
		Port:                            envInt("CC_PORT", constants.DefaultPort),
		MaxMessageSizeMiB:               envInt("CC_MAX_MESSAGE_SIZE_IN_MiB", constants.DefaultMaxMessageSizeMiB),
		MaxMessageSizeBytes:             envInt64("CC_MAX_MESSAGE_SIZE_IN_BYTES", 0),
		P2PEnabled:                      envBool("CC_P2P_ENABLED", false),
		P2PStunURL:                      envStr("CC_P2P_STUN_URL", constants.DefaultStunURL),
		AllowedOrigins:                  envStr("CC_ALLOWED_ORIGINS", "*"),
		SignupEnabled:                   envBool("CC_SIGNUP_ENABLED", false),
		MaxUserAccounts:                 envInt("CC_MAX_USER_ACCOUNTS", -1),
		AccountPurgeTimeout:             envInt64("CC_ACCOUNT_PURGE_TIMEOUT_SECONDS", -1),
		SessionTimeoutMinutes:           envInt("CC_SESSION_TIMEOUT", constants.DefaultSessionTimeoutMin),
		MaxUniqueIPAttempts:             envInt("CC_MAX_UNIQUE_IP_ATTEMPTS", constants.DefaultMaxUniqueIPAttempts),
		MaxAttemptsPerIP:                envInt("CC_MAX_ATTEMPTS_PER_IP", constants.DefaultMaxAttemptsPerIP),
		LockTimeoutSeconds:              envInt("CC_LOCK_TIMEOUT_SECONDS", constants.DefaultLockTimeoutSeconds),
		LockScalingFactor:               envInt("CC_LOCK_TIMEOUT_SCALING_FACTOR", constants.DefaultLockScalingFactor),
		BFACacheEnabled:                 envBool("CC_BFA_CACHE_ENABLED", true),
		DatabasePath:                    resolveRuntimePath(baseDir, envStr("CC_DATABASE_PATH", "./database/clipcascade.db")),
		FileRelayEnabled:                envBool("CC_FILE_RELAY_ENABLED", true),
		FileRelayDir:                    resolveRuntimePath(baseDir, envStr("CC_FILE_RELAY_DIR", "./uploads")),
		FileRelayMaxBytes:               envInt64("CC_FILE_RELAY_MAX_BYTES", constants.DefaultFileRelayMaxBytes),
		FileRelayTTLSeconds:             envInt64("CC_FILE_RELAY_TTL_SECONDS", 86400),
		ExternalBrokerEnabled:           envBool("CC_EXTERNAL_BROKER_ENABLED", false),
		BrokerHost:                      envStr("CC_BROKER_HOST", "localhost"),
		BrokerPort:                      envInt("CC_BROKER_PORT", 61613),
		AutomationEnabled:               envBool("CC_AUTOMATION_ENABLED", false),
		AutomationMode:                  envStr("CC_AUTOMATION_MODE", "observe"),
		AutomationRulesPath:             resolveRuntimePath(baseDir, envStr("CC_AUTOMATION_RULES_PATH", "")),
		AutomationCommandsPath:          resolveRuntimePath(baseDir, envStr("CC_AUTOMATION_COMMANDS_PATH", "")),
		AutomationPluginsDir:            resolveRuntimePath(baseDir, envStr("CC_AUTOMATION_PLUGINS_DIR", "./plugins")),
		AutomationCommandTimeoutSeconds: envInt("CC_AUTOMATION_COMMAND_TIMEOUT_SECONDS", 30),
		AutomationCommandMaxOutputBytes: envInt64("CC_AUTOMATION_COMMAND_MAX_OUTPUT_BYTES", 64*1024),
		AutomationPluginTimeoutSeconds:  envInt("CC_AUTOMATION_PLUGIN_TIMEOUT_SECONDS", 30),
		AutomationPluginMaxOutputBytes:  envInt64("CC_AUTOMATION_PLUGIN_MAX_OUTPUT_BYTES", 64*1024),
		AutomationTrustedCopyEnabled:    envBool("CC_AUTOMATION_TRUSTED_COPY_ENABLED", false),
		AutomationCopyMaxBytes:          envInt64("CC_AUTOMATION_COPY_MAX_BYTES", 256*1024),
	}
	return c
}

// EffectiveMaxMessageBytes 返回以字节为单位的有效最大消息大小。
func (c *Config) EffectiveMaxMessageBytes() int64 {
	if c.P2PEnabled {
		return 0 // P2P 模式下不限制大小
	}
	if c.MaxMessageSizeBytes > 0 {
		return c.MaxMessageSizeBytes
	}
	return int64(c.MaxMessageSizeMiB) * 1024 * 1024
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

func resolveRuntimePath(baseDir, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "file:") {
		return value
	}
	if baseDir == "" {
		return value
	}
	return filepath.Join(baseDir, value)
}

// AllowedOriginsList 将允许的 origins 字符串解析为切片。
func (c *Config) AllowedOriginsList() []string {
	if c.AllowedOrigins == "*" {
		return []string{"*"}
	}
	origins := strings.Split(c.AllowedOrigins, ",")
	for i := range origins {
		origins[i] = strings.TrimSpace(origins[i])
	}
	return origins
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
