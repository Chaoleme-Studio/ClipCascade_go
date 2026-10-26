//go:build linux

package clipboard

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.design/x/clipboard"
)

// 文件剪贴板的 X11/Wayland target。不同桌面环境约定不同，写错 target 会让文件
// 管理器"看得到剪贴板更新、却粘不出文件"（典型症状：Ctrl+V 无反应、粘贴项置灰）。
//
//   - 统信 UOS / deepin 的 dde-file-manager 源码里只认 x-special/gnome-copied-files
//     （其常量 kGnomeCopyKey），text/uri-list 会被直接忽略；
//   - GNOME / Unity / Cinnamon / XFCE / MATE 同样使用 gnome 系约定；
//   - KDE (Dolphin) 使用 text/uri-list。
//
// gnome 系 target 的数据格式约定为 "copy\n" 前缀 + 每行一个 file:// URI。
const (
	targetTextURIList = "text/uri-list"
	targetGnomeCopied = "x-special/gnome-copied-files"
	targetMateCopied  = "x-special/mate-copied-files"
)

// fileClipboardFormat 描述当前桌面环境期望的文件剪贴板 target 与数据格式。
type fileClipboardFormat struct {
	target       string
	gnomePrelude bool // true 时数据需以 "copy\n" 前缀开头
}

// detectFileClipboardFormat 依据 XDG_CURRENT_DESKTOP 推断文件剪贴板格式。
// 探测不到或非 KDE/MATE 时统一按 gnome 约定处理，这是覆盖 UOS/deepin 的关键。
func detectFileClipboardFormat() fileClipboardFormat {
	de := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
	switch {
	case strings.Contains(de, "kde"):
		return fileClipboardFormat{target: targetTextURIList}
	case strings.Contains(de, "mate"):
		return fileClipboardFormat{target: targetMateCopied, gnomePrelude: true}
	default:
		return fileClipboardFormat{target: targetGnomeCopied, gnomePrelude: true}
	}
}

// Linux 使用后端事件，不使用物理变更计数器。
func getPlatformChangeCount() int64 { return 0 }

// getPlatformFilePaths 尝试从 Wayland/X11 剪贴板获取文件 URI。
func getPlatformFilePaths() ([]string, error) {
	out, err := readURIListClipboard()
	if err != nil {
		return nil, nil
	}

	return parseLinuxFilePaths(out), nil
}

func parseLinuxFilePaths(out []byte) []string {
	raw := string(bytes.TrimSpace(out))
	if raw == "" {
		return nil
	}

	var validPaths []string
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		// gnome 系数据以 "copy"/"cut" 起头，且常带结尾 NUL；这里一并清洗。
		line = strings.Trim(line, "\x00\r\t ")
		if strings.HasPrefix(line, "file://") {
			if parsed, err := url.Parse(line); err == nil {
				if parsed.Host != "" && parsed.Host != "localhost" {
					continue
				}
				// 严格磁盘校验
				if _, err := os.Stat(parsed.Path); err == nil {
					validPaths = append(validPaths, parsed.Path)
				}
			}
		}
	}

	return validPaths
}

// 同时发布标准 URI 和 GNOME/MATE 文件格式，兼容不同文件管理器。
func setPlatformFilePaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := clipboard.WriteAll(ctx, linuxFileClipboardItems(paths)...)
	return err
}

func linuxFileClipboardItems(paths []string) []clipboard.Option {
	uris := buildURIList(paths)
	if uris == "" {
		return nil
	}
	return []clipboard.Option{
		clipboard.Item{Format: clipboard.FmtFiles, Bytes: []byte(uris)},
		clipboard.Item{Format: clipboard.Register(targetGnomeCopied), Bytes: []byte("copy\n" + uris)},
		clipboard.Item{Format: clipboard.Register(targetMateCopied), Bytes: []byte("copy\n" + uris)},
	}
}

// fileClipboardReadTargets 返回读取文件剪贴板时应依次尝试的 target 列表。
// 首选当前桌面环境的目标，再退回通用格式，兼容从其它应用复制过来的文件。
func fileClipboardReadTargets() []string {
	ordered := []string{
		detectFileClipboardFormat().target,
		targetTextURIList,
		targetGnomeCopied,
		targetMateCopied,
	}
	seen := make(map[string]struct{}, len(ordered))
	targets := make([]string, 0, len(ordered))
	for _, t := range ordered {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		targets = append(targets, t)
	}
	return targets
}

func readURIListClipboard() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, target := range fileClipboardReadTargets() {
		out, err := clipboard.Read(ctx, clipboard.Register(target))
		if err == nil && len(bytes.TrimSpace(out)) > 0 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("no file uri-list in clipboard")
}

// buildFileClipboardData 按当前桌面环境生成文件剪贴板的 target 与负载。
func buildFileClipboardData(paths []string) (target string, payload string) {
	format := detectFileClipboardFormat()
	uris := buildURIList(paths)
	if uris == "" {
		return format.target, ""
	}
	if format.gnomePrelude {
		// gnome 系约定："copy" 表示复制（"cut" 表示剪切），随后每行一个 URI。
		return format.target, "copy\n" + uris
	}
	return format.target, uris
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
	return clipboard.Read(context.Background(), clipboard.FmtImage)
}

func getPlatformText() ([]byte, error) {
	return clipboard.Read(context.Background(), clipboard.FmtText)
}

func setPlatformText(text string) error {
	_, err := clipboard.Write(context.Background(), clipboard.FmtText, []byte(text))
	return err
}

func setPlatformImage(data []byte) error {
	_, err := clipboard.Write(context.Background(), clipboard.FmtImage, data)
	return err
}
