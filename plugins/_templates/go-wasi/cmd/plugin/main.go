package main

import (
	"encoding/json"
	"io"
	"os"
)

type pluginOutput struct {
	Status  string         `json:"status"`
	Message string         `json:"message,omitempty"`
	Tags    []string       `json:"tags,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
}

func main() {
	// 模板插件读取 stdin，后续可以在这里解析 ClipCascade 传入的事件载荷。
	_, _ = io.ReadAll(os.Stdin)
	// stdout 必须只输出一个机器可解析对象，服务端会把它记录到任务结果中。
	_ = json.NewEncoder(os.Stdout).Encode(pluginOutput{
		Status:  "succeeded",
		Message: "template plugin executed",
		Tags:    []string{"template"},
		Result:  map[string]any{"reserved": true},
	})
}
