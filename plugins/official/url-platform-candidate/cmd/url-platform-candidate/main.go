package main

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
)

var urlRe = regexp.MustCompile(`https?://[^\s]+`)

// pluginInput 只声明当前插件需要读取的事件字段。
type pluginInput struct {
	Event struct {
		Clipboard struct {
			TextPreview string `json:"text_preview"`
			Payload     string `json:"payload,omitempty"`
		} `json:"clipboard"`
	} `json:"event"`
}

// pluginOutput 是写回 stdout 的任务结果。
type pluginOutput struct {
	Status  string            `json:"status"`
	Message string            `json:"message,omitempty"`
	Tags    []string          `json:"tags,omitempty"`
	Result  map[string]string `json:"result,omitempty"`
}

func main() {
	// 插件通过 stdin 接收服务端传入的事件载荷。
	data, _ := io.ReadAll(os.Stdin)
	var input pluginInput
	_ = json.Unmarshal(data, &input)

	// 优先使用 text_preview，必要时回退到 payload。
	text := strings.TrimSpace(input.Event.Clipboard.TextPreview)
	if text == "" {
		text = strings.TrimSpace(input.Event.Clipboard.Payload)
	}
	// 当前官方插件只返回候选信息，不进行真实下载。
	url := urlRe.FindString(text)
	platform := platformOf(text)
	result := map[string]string{
		"platform": platform,
		"url":      url,
		"action":   "download_reserved",
	}
	message := "supported platform URL candidate detected"
	if url == "" || platform == "unknown" {
		message = "no supported platform URL detected"
	}

	// stdout 必须只输出一个机器可解析对象，服务端会把它记录到任务结果中。
	_ = json.NewEncoder(os.Stdout).Encode(pluginOutput{
		Status:  "succeeded",
		Message: message,
		Tags:    []string{"url_candidate", "platform_url", platform},
		Result:  result,
	})
}

// platformOf 根据文本中的链接域名识别平台。
func platformOf(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "douyin.com") || strings.Contains(lower, "iesdouyin.com"):
		return "douyin"
	case strings.Contains(lower, "kuaishou.com") || strings.Contains(lower, "gifshow.com"):
		return "kuaishou"
	default:
		return "unknown"
	}
}
