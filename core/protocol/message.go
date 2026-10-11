// Package protocol 定义了用于 server 和所有 client 之间交换剪贴板内容的 ClipboardData 消息模型。
package protocol

import "encoding/json"

// FragmentMetadata 包含分片消息的调试和重组信息。
type FragmentMetadata struct {
	ID             string `json:"id"`
	Index          int    `json:"index"`
	TotalFragments int    `json:"totalFragments"`
	IsFragmented   bool   `json:"isFragmented"`
}

// ClipboardData 表示通过 WebSocket 或 P2P 传输的剪贴板 payload。
// 它是所有 ClipCascade 组件之间共享的核心消息格式。
type ClipboardData struct {
	// Payload 是剪贴板内容，对于图像/文件，通常是 base64 编码的或者是文件路径列表。
	Payload string `json:"payload"`
	// Type 表示内容类型："text"、"image"、"file_stub"、"file_eager"、"file_offer" 或 "file_request"。
	Type string `json:"type"`
	// FileName 保存用于小文件闪电直传的原始文件名。
	FileName string `json:"filename,omitempty"`
	// Metadata 包含分片信息（可选）。
	Metadata *FragmentMetadata `json:"metadata,omitempty"`

	// --- v2.2 新增：幂等/去重/冲突解决（旧客户端会忽略未知字段，向后兼容） ---

	// MessageUUID 是本次复制事件的唯一 ID，用于端到端去重与幂等。
	MessageUUID string `json:"msg_uuid,omitempty"`
	// DeviceID 是发送方设备标识，用于回环过滤与多设备冲突溯源。
	DeviceID string `json:"device_id,omitempty"`
	// TimestampMs 是发送方 Unix 毫秒时间戳，用于多设备同时复制时的 last-write-wins 判断。
	TimestampMs int64 `json:"ts_ms,omitempty"`
}

// Encode 将 ClipboardData 序列化为 JSON 字节 slice。
func (cd *ClipboardData) Encode() ([]byte, error) {
	return json.Marshal(cd)
}

// DecodeClipboardData 将 JSON 字节 slice 反序列化为 ClipboardData。
func DecodeClipboardData(data []byte) (*ClipboardData, error) {
	var cd ClipboardData
	if err := json.Unmarshal(data, &cd); err != nil {
		return nil, err
	}
	return &cd, nil
}
