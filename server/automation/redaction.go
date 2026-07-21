package automation

import "regexp"

type textRedactor struct {
	Name    string
	Pattern *regexp.Regexp
}

var taskInputRedactors = []textRedactor{
	{Name: "private_key", Pattern: regexp.MustCompile(`-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`)},
	{Name: "github_token", Pattern: regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{20,}\b|github_pat_[A-Za-z0-9_]{20,}`)},
	{Name: "openai_key", Pattern: regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b`)},
	{Name: "aws_access_key_id", Pattern: regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{Name: "google_api_key", Pattern: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{Name: "stripe_secret_key", Pattern: regexp.MustCompile(`\b(?:sk_live|rk_live)_[0-9A-Za-z]{20,}\b`)},
	{Name: "slack_token", Pattern: regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{20,}\b`)},
	{Name: "jwt", Pattern: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	{Name: "database_url_with_password", Pattern: regexp.MustCompile(`(?i)\b(?:postgres|postgresql|mysql|mongodb|redis)://[^\s:/?#]+:[^\s@]+@[^\s]+`)},
	{Name: "bearer_token", Pattern: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{24,}\b`)},
}

// sanitizeEventForStorage 只处理持久化副本，不影响规则匹配和插件执行。
func sanitizeEventForStorage(event ClipboardEvent) ClipboardEvent {
	event.Clipboard.TextPreview = redactTextPreview(event.Clipboard.TextPreview)
	return event
}

// redactTextPreview 替换常见密钥候选，避免任务输入记录保存敏感原文。
func redactTextPreview(text string) string {
	for _, redactor := range taskInputRedactors {
		text = redactor.Pattern.ReplaceAllString(text, "[REDACTED:"+redactor.Name+"]")
	}
	return text
}
