package automation

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/clipcascade/pkg/constants"
	"github.com/clipcascade/pkg/protocol"
	"github.com/clipcascade/pkg/sizefmt"
)

// NewClipboardEvent 将传输层剪贴板载荷转换为受限自动化事件。
func NewClipboardEvent(username string, sourceIP string, clipData protocol.ClipboardData, encrypted bool, wireBody string) ClipboardEvent {
	msgType := clipData.Type
	payload := clipData.Payload
	payloadSize := int64(len(clipData.Payload))
	if encrypted {
		msgType = "e2ee_envelope"
		payload = ""
		payloadSize = int64(len(wireBody))
	}
	event := ClipboardEvent{
		EventID:   uuid.NewString(),
		Timestamp: time.Now(),
		Username:  username,
		Source:    EventSource{IP: sourceIP},
		Transport: "websocket",
		Clipboard: ClipboardSummary{
			Type:        msgType,
			Payload:     payload,
			PayloadSize: payloadSize,
			FileName:    clipData.FileName,
			IsEncrypted: encrypted,
		},
	}
	if !encrypted && (clipData.Type == constants.TypeImage || clipData.Type == constants.TypeFileEager) {
		event.Clipboard.PayloadSize = int64(sizefmt.EstimatedBase64DecodedSize(clipData.Payload))
	}
	if clipData.Type == constants.TypeText && !encrypted {
		event.Clipboard.TextPreview = truncatePreview(clipData.Payload, 512)
	}
	return event
}

func truncatePreview(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
