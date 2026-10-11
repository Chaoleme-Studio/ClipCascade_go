package app

import (
	"testing"

	"github.com/clipcascade/desktop/config"
)

func TestOfflineCopyWaitsForEncryptionKey(t *testing.T) {
	a := &Application{cfg: &config.Config{SendText: true, E2EEEnabled: true}}
	a.onCopy("offline clipboard", "text", "")
	if len(a.pendingSends) != 1 || a.pendingSends[0].payload != "offline clipboard" || a.pendingSends[0].body != "" {
		t.Fatalf("offline copy should remain raw until encryption is ready: %+v", a.pendingSends)
	}
}
