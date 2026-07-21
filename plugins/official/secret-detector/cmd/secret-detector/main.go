package main

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

const previewLimit = 512

// pluginInput 只声明当前插件需要读取的事件字段。
type pluginInput struct {
	Event struct {
		Clipboard struct {
			Type        string `json:"type"`
			TextPreview string `json:"text_preview"`
			IsEncrypted bool   `json:"is_encrypted"`
		} `json:"clipboard"`
	} `json:"event"`
}

// pluginOutput 是写回 stdout 的任务结果。
type pluginOutput struct {
	Status  string          `json:"status"`
	Message string          `json:"message,omitempty"`
	Tags    []string        `json:"tags,omitempty"`
	Result  detectionResult `json:"result,omitempty"`
}

// detectionResult 只返回命中类型和统计，不回显任何密钥原文。
type detectionResult struct {
	Matched        bool      `json:"matched"`
	Total          int       `json:"total"`
	HighestRisk    string    `json:"highest_risk"`
	Types          []finding `json:"types"`
	Source         string    `json:"source"`
	PreviewLimited bool      `json:"preview_limited"`
	Encrypted      bool      `json:"encrypted"`
}

// finding 描述一种疑似敏感凭证的命中数量。
type finding struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

// detector 定义一个凭证识别规则。
type detector struct {
	Type     string
	Severity string
	Pattern  *regexp.Regexp
}

var detectors = []detector{
	{
		Type:     "private_key",
		Severity: "critical",
		Pattern:  regexp.MustCompile(`-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`),
	},
	{
		Type:     "github_token",
		Severity: "critical",
		Pattern:  regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{20,}\b|github_pat_[A-Za-z0-9_]{20,}`),
	},
	{
		Type:     "openai_key",
		Severity: "critical",
		Pattern:  regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b`),
	},
	{
		Type:     "aws_access_key_id",
		Severity: "high",
		Pattern:  regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	},
	{
		Type:     "google_api_key",
		Severity: "high",
		Pattern:  regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
	},
	{
		Type:     "stripe_secret_key",
		Severity: "critical",
		Pattern:  regexp.MustCompile(`\b(?:sk_live|rk_live)_[0-9A-Za-z]{20,}\b`),
	},
	{
		Type:     "slack_token",
		Severity: "high",
		Pattern:  regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{20,}\b`),
	},
	{
		Type:     "jwt",
		Severity: "medium",
		Pattern:  regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
	},
	{
		Type:     "database_url_with_password",
		Severity: "high",
		Pattern:  regexp.MustCompile(`(?i)\b(?:postgres|postgresql|mysql|mongodb|redis)://[^\s:/?#]+:[^\s@]+@[^\s]+`),
	},
	{
		Type:     "bearer_token",
		Severity: "medium",
		Pattern:  regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{24,}\b`),
	},
}

func main() {
	// 插件通过 stdin 接收服务端传入的事件载荷。
	data, _ := io.ReadAll(os.Stdin)
	var input pluginInput
	_ = json.Unmarshal(data, &input)

	text := strings.TrimSpace(input.Event.Clipboard.TextPreview)
	result := detectSecrets(text, input.Event.Clipboard.IsEncrypted)
	message := "no secret candidate detected"
	tags := []string{"secret_detect", "no_match"}
	if result.Matched {
		message = "secret candidate detected"
		tags = []string{"secret_detect", "secret_candidate", result.HighestRisk}
	}

	// stdout 必须只输出一个机器可解析对象，服务端会把它记录到任务结果中。
	_ = json.NewEncoder(os.Stdout).Encode(pluginOutput{
		Status:  "succeeded",
		Message: message,
		Tags:    tags,
		Result:  result,
	})
}

// detectSecrets 在受限文本预览中查找疑似密钥，不返回原始命中内容。
func detectSecrets(text string, encrypted bool) detectionResult {
	result := detectionResult{
		Source:         "clipboard.text_preview",
		PreviewLimited: len([]rune(text)) >= previewLimit,
		Encrypted:      encrypted,
		HighestRisk:    "none",
	}
	if text == "" || encrypted {
		return result
	}

	findings := make([]finding, 0, len(detectors))
	for _, detector := range detectors {
		count := len(detector.Pattern.FindAllStringIndex(text, -1))
		if count == 0 {
			continue
		}
		findings = append(findings, finding{
			Type:     detector.Type,
			Severity: detector.Severity,
			Count:    count,
		})
		result.Total += count
		result.HighestRisk = higherRisk(result.HighestRisk, detector.Severity)
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if riskRank(findings[i].Severity) == riskRank(findings[j].Severity) {
			return findings[i].Type < findings[j].Type
		}
		return riskRank(findings[i].Severity) > riskRank(findings[j].Severity)
	})
	result.Types = findings
	result.Matched = result.Total > 0
	return result
}

// higherRisk 返回两个风险等级中更高的一个。
func higherRisk(left, right string) string {
	if riskRank(right) > riskRank(left) {
		return right
	}
	return left
}

// riskRank 将风险等级映射成可排序数字。
func riskRank(risk string) int {
	switch risk {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
