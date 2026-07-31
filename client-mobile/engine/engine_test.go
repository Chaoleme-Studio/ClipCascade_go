package engine

import (
	"testing"

	pkgcrypto "github.com/clipcascade/pkg/crypto"
	"github.com/clipcascade/pkg/protocol"
)

type recordingCallback struct {
	payload     string
	payloadType string
	fileName    string
}

func (c *recordingCallback) OnMessage(payload string, payloadType string, fileName string) {
	c.payload = payload
	c.payloadType = payloadType
	c.fileName = fileName
}

func TestHandleIncomingDataPreservesEagerFileName(t *testing.T) {
	callback := &recordingCallback{}
	engine := &Engine{callback: callback}
	data, err := (&protocol.ClipboardData{
		Type:     "file_eager",
		Payload:  "ZmlsZQ==",
		FileName: "report.pdf",
	}).Encode()
	if err != nil {
		t.Fatalf("encode eager file message: %v", err)
	}

	engine.handleIncomingData(string(data))

	if callback.payloadType != "file_eager" || callback.fileName != "report.pdf" {
		t.Fatalf("unexpected callback metadata: type=%q filename=%q", callback.payloadType, callback.fileName)
	}
}

func (c *recordingCallback) OnStatusChange(string) {}

func TestHandleIncomingDataAcceptsPlainServerControlMessageWithE2EEEnabled(t *testing.T) {
	callback := &recordingCallback{}
	engine := &Engine{e2ee: true, encKey: []byte("01234567890123456789012345678901"), callback: callback}
	data, err := (&protocol.ClipboardData{Type: "file_request", Payload: "{\"deferred_id\":\"abc\"}"}).Encode()
	if err != nil {
		t.Fatalf("encode control message: %v", err)
	}

	engine.handleIncomingData(string(data))

	if callback.payloadType != "file_request" || callback.payload != "{\"deferred_id\":\"abc\"}" {
		t.Fatalf("unexpected callback: type=%q payload=%q", callback.payloadType, callback.payload)
	}
}

func TestHandleIncomingDataStillDecryptsEncryptedClipboardMessage(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	callback := &recordingCallback{}
	engine := &Engine{e2ee: true, encKey: key, callback: callback}
	data, err := (&protocol.ClipboardData{Type: "text", Payload: "secret"}).Encode()
	if err != nil {
		t.Fatalf("encode clipboard message: %v", err)
	}
	encrypted, err := pkgcrypto.Encrypt(key, data)
	if err != nil {
		t.Fatalf("encrypt clipboard message: %v", err)
	}
	body, err := pkgcrypto.EncodeToJSONString(encrypted)
	if err != nil {
		t.Fatalf("encode encrypted envelope: %v", err)
	}

	engine.handleIncomingData(body)

	if callback.payloadType != "text" || callback.payload != "secret" {
		t.Fatalf("unexpected callback: type=%q payload=%q", callback.payloadType, callback.payload)
	}
}
