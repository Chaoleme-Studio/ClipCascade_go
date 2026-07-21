package automation

import (
	"bytes"
	"encoding/json"
)

// unmarshalJSONC 先移除 JSONC 注释，再交给标准解码器解析。
func unmarshalJSONC(data []byte, v any) error {
	return json.Unmarshal(stripJSONCComments(data), v)
}

// stripJSONCComments 移除 // 和 /* */ 注释，同时保留字符串里的内容不变。
func stripJSONCComments(data []byte) []byte {
	var out bytes.Buffer
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		ch := data[i]
		if inString {
			out.WriteByte(ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			out.WriteByte(ch)
			continue
		}
		if ch == '/' && i+1 < len(data) {
			next := data[i+1]
			if next == '/' {
				i += 2
				for i < len(data) && data[i] != '\n' && data[i] != '\r' {
					i++
				}
				if i < len(data) {
					out.WriteByte(data[i])
				}
				continue
			}
			if next == '*' {
				i += 2
				for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
					if data[i] == '\n' || data[i] == '\r' {
						out.WriteByte(data[i])
					}
					i++
				}
				if i+1 < len(data) {
					i++
				}
				continue
			}
		}
		out.WriteByte(ch)
	}
	return out.Bytes()
}
