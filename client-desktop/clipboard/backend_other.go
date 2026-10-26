//go:build !linux

package clipboard

import (
	"context"

	"golang.design/x/clipboard"
)

func initClipboardBackend() error { return clipboard.Init() }

func (m *Manager) watchClipboardBackend(ctx context.Context) {
	m.watchLegacyByChangeCount(ctx)
}
