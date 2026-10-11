package engine

import (
	"testing"
)

func TestFailedFlushKeepsWholeBatchWithoutDuplicates(t *testing.T) {
	e := NewEngine("http://localhost", "alice", "secret", false)
	for _, payload := range []string{"first", "second"} {
		if err := e.SendClipboard(payload, "text"); err == nil {
			t.Fatal("expected disconnected send to fail")
		}
	}
	if len(e.pendingSends) != 2 {
		t.Fatalf("expected two queued messages, got %d", len(e.pendingSends))
	}
	firstBody := e.pendingSends[0].body
	e.flushPendingSends()
	e.flushPendingSends()
	if len(e.pendingSends) != 2 || e.pendingSends[0].body != firstBody {
		t.Fatalf("failed flush lost or duplicated messages: %+v", e.pendingSends)
	}
}

func TestOfflineEncryptedSendIsNeverQueuedAsPlaintext(t *testing.T) {
	e := NewEngine("http://localhost", "alice", "secret", true)
	if err := e.SendClipboard("private", "text"); err == nil {
		t.Fatal("expected send to wait for encryption key")
	}
	if len(e.pendingSends) != 1 || e.pendingSends[0].body != "" {
		t.Fatalf("message was encoded before key was ready: %+v", e.pendingSends)
	}
	e.mu.Lock()
	e.encKey = []byte("01234567890123456789012345678901")
	e.mu.Unlock()
	e.flushPendingSends()
	if len(e.pendingSends) != 1 || e.pendingSends[0].body == "" || e.pendingSends[0].body == "private" {
		t.Fatalf("message was not encrypted before retry: %+v", e.pendingSends)
	}
}
