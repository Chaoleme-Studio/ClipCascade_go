//go:build linux

package clipboard

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/clipcascade/pkg/constants"
	"golang.design/x/clipboard"
)

func initClipboardBackend() error {
	// 上游实际探测 data-control 能力，优先原生 Wayland，必要时使用已有 X11。
	// 不依赖 wl-copy、wl-paste、xclip，也不要求用户切换桌面会话。
	if err := clipboard.Init(); err != nil {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return fmt.Errorf("当前 Wayland 会话未提供可访问的剪贴板接口（需要 data-control 或会话已有的 XWayland 兼容接口）；额外安装剪贴板管理器不能补齐该接口: %w", clipboard.ErrUnavailable)
		}
		return fmt.Errorf("无法连接当前桌面的剪贴板，请使用已登录桌面的用户在图形会话中启动程序: %w", clipboard.ErrUnavailable)
	}
	slog.Info("剪贴板：已初始化原生 Linux 后端，自动选择 Wayland / X11")
	return nil
}

func (m *Manager) watchClipboardBackend(ctx context.Context) {
	options := []clipboard.Option{clipboard.FmtText, clipboard.FmtImage, clipboard.FmtFiles}
	for _, mime := range []string{targetGnomeCopied, targetMateCopied, "image/jpeg", "image/webp", "image/gif"} {
		options = append(options, clipboard.Register(mime))
	}
	// Wayland 使用 data-control 事件；X11 使用上游的变化检测。
	m.watchLinuxEvents(ctx, clipboard.Watch(ctx, options...), readLinuxSnapshot)
}

type linuxClipboardSnapshot struct {
	target string
	data   []byte
}

func readLinuxSnapshot(parent context.Context) (linuxClipboardSnapshot, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	formats, err := clipboard.Formats(ctx)
	if err != nil {
		return linuxClipboardSnapshot{}, err
	}
	return readLinuxSnapshotFrom(ctx, formats, func(ctx context.Context, format clipboard.Format) ([]byte, error) {
		return clipboard.Read(ctx, format)
	})
}

func readLinuxSnapshotFrom(ctx context.Context, formats []clipboard.Format, read func(context.Context, clipboard.Format) ([]byte, error)) (linuxClipboardSnapshot, error) {
	available := make(map[string]clipboard.Format, len(formats))
	for _, format := range formats {
		available[format.MIME()] = format
	}
	targets := append(fileClipboardReadTargets(), "image/png", "image/jpeg", "image/webp", "image/gif", "text/plain;charset=utf-8", "text/plain", "UTF8_STRING", "TEXT", "STRING")
	for _, target := range targets {
		format, ok := available[target]
		if !ok {
			continue
		}
		data, err := read(ctx, format)
		if err != nil {
			return linuxClipboardSnapshot{}, err
		}
		if (target == targetTextURIList || target == targetGnomeCopied || target == targetMateCopied) && len(parseLinuxFilePaths(data)) == 0 {
			continue // URI target 也可能只是网页链接，继续读取其文本表示。
		}
		return linuxClipboardSnapshot{target: target, data: data}, nil
	}
	return linuxClipboardSnapshot{}, nil
}

func (m *Manager) watchLinuxEvents(ctx context.Context, events <-chan clipboard.Data, read func(context.Context) (linuxClipboardSnapshot, error)) {
	// 上游 Watch 已同步建立初始快照，不能再次丢弃首次复制事件。
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				if ctx.Err() == nil {
					slog.Warn("剪贴板：Linux 监听已停止，请检查当前图形会话连接")
				}
				return
			}
			m.handleLinuxEvent(ctx, read)
		}
	}
}

func (m *Manager) handleLinuxEvent(ctx context.Context, read func(context.Context) (linuxClipboardSnapshot, error)) {
	m.clipboardMu.Lock()
	defer m.clipboardMu.Unlock()
	snapshot, err := read(ctx)
	if err != nil || len(snapshot.data) == 0 {
		return
	}
	switch snapshot.target {
	case targetTextURIList, targetGnomeCopied, targetMateCopied:
		m.handleFilePaths(parseLinuxFilePaths(snapshot.data))
	default:
		if strings.HasPrefix(snapshot.target, "image/") {
			m.handleChange(base64.StdEncoding.EncodeToString(snapshot.data), constants.TypeImage, "")
		} else {
			m.handleChange(string(snapshot.data), constants.TypeText, "")
		}
	}
}
