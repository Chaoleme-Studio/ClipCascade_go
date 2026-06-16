//go:build linux

package clipboard

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"

	pkgcrypto "github.com/clipcascade/pkg/crypto"
)

// getPlatformChangeCount 对于 Linux 零 CGO 方案，较难直接获取 SequenceNumber，
// 我们通过读取 xclip 的元数据或直接由 Watch 内部状态管理。
func getPlatformChangeCount() int64 {
	// 简单实现：由于 Watch 已经是 500ms 轮询，这里可以配合 clipboard.Watch 的原生信号
	// 或者针对 Linux 做一个内容哈希（虽然低效但仅限 Linux 兜底）
	cmd := exec.Command("xclip", "-selection", "clipboard", "-o", "-t", "TARGETS")
	out, _ := cmd.Output()
	return int64(pkgcrypto.XXHash64(string(out)))
}

// getPlatformFilePaths 尝试从 Wayland/X11 剪贴板获取文件 URI。
func getPlatformFilePaths() ([]string, error) {
	out, err := readURIListClipboard()
	if err != nil {
		return nil, nil
	}

	raw := string(bytes.TrimSpace(out))
	if raw == "" {
		return nil, nil
	}

	var validPaths []string
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "file://") {
			if parsed, err := url.Parse(line); err == nil {
				// 严格磁盘校验
				if _, err := os.Stat(parsed.Path); err == nil {
					validPaths = append(validPaths, parsed.Path)
				}
			}
		}
	}

	return validPaths, nil
}

// setPlatformFilePaths 使用 wl-copy/xclip 写入文件路径。
func setPlatformFilePaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	payload := buildURIList(paths)
	if payload == "" {
		return nil
	}
	if _, err := exec.LookPath("wl-copy"); err == nil && os.Getenv("WAYLAND_DISPLAY") != "" {
		slog.Info("剪贴板：通过 wl-copy 将文件路径写入 Linux 剪贴板...")
		cmd := exec.Command("wl-copy", "--type", "text/uri-list")
		cmd.Stdin = strings.NewReader(payload)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("wl-copy failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		slog.Info("剪贴板：通过 xclip 将文件路径写入 Linux 剪贴板...")
		cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "text/uri-list")
		cmd.Stdin = strings.NewReader(payload)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("xclip failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return fmt.Errorf("missing wl-copy or xclip for Linux file clipboard")
}

func readURIListClipboard() ([]byte, error) {
	if _, err := exec.LookPath("wl-paste"); err == nil && os.Getenv("WAYLAND_DISPLAY") != "" {
		return exec.Command("wl-paste", "--no-newline", "--type", "text/uri-list").Output()
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return exec.Command("xclip", "-selection", "clipboard", "-o", "-t", "text/uri-list").Output()
	}
	return nil, fmt.Errorf("missing wl-paste or xclip")
}

func buildURIList(paths []string) string {
	uriList := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		uri := url.URL{Scheme: "file", Path: p}
		uriList = append(uriList, uri.String())
	}
	return strings.Join(uriList, "\n")
}

func getPlatformImage() ([]byte, error) {
	return nil, nil
}

func getPlatformText() ([]byte, error) {
	return nil, nil
}

func setPlatformText(text string) error {
	return nil
}

func setPlatformImage(data []byte) error {
	return nil
}
