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

// getPlatformChangeCount 对于 Linux 零 CGO 方案，较难直接获取 SequenceNumber，
// 我们通过读取 xclip 的元数据或直接由 Watch 内部状态管理。
func getPlatformChangeCount() int64 {
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
		// gnome 系数据以 "copy"/"cut" 起头，且常带结尾 NUL；这里一并清洗。
		line = strings.Trim(line, "\x00\r\t ")
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
	target, payload := buildFileClipboardData(paths)
	if payload == "" {
		return nil
	}
	if _, err := exec.LookPath("wl-copy"); err == nil && os.Getenv("WAYLAND_DISPLAY") != "" {
		slog.Info("剪贴板：通过 wl-copy 将文件路径写入 Linux 剪贴板...", "target", target)
		return runClipboardOwner("wl-copy", payload, "--type", target)
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		slog.Info("剪贴板：通过 xclip 将文件路径写入 Linux 剪贴板...", "target", target)
		return runClipboardOwner("xclip", payload, "-selection", "clipboard", "-t", target)
	}
	return fmt.Errorf("missing wl-copy or xclip for Linux file clipboard")
}

// runClipboardOwner 启动剪贴板属主进程后立即返回，不要等待。
//
// xclip / wl-copy 默认会 fork 到后台持有 selection，但 fork 出的子进程会继承
// 调用方的 stdout/stderr。一旦用 CombinedOutput/Output 捕获输出，管道在子进程
// 退出前不会 EOF，调用方就会一直阻塞到剪贴板被别的程序接管为止（实测可达数十秒
// 甚至永久），从而卡死整个剪贴板接收流程。
//
// 这里把 stdio 指向 /dev/null（传 *os.File 时 exec 直接复用 fd，不会额外建管道），
// 只等父进程退出，后台子进程继续持有剪贴板；Wait 放到 goroutine 里回收，不阻塞调用方。
func runClipboardOwner(bin, payload string, args ...string) error {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(payload)
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	if err := cmd.Start(); err != nil {
		_ = devNull.Close()
		return fmt.Errorf("%s failed to start: %w", bin, err)
	}
	go func() {
		_ = cmd.Wait()
		_ = devNull.Close()
	}()
	return nil
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
	useWayland := os.Getenv("WAYLAND_DISPLAY") != ""
	_, wlErr := exec.LookPath("wl-paste")
	_, xErr := exec.LookPath("xclip")
	if wlErr != nil && xErr != nil {
		return nil, fmt.Errorf("missing wl-paste or xclip")
	}

	for _, target := range fileClipboardReadTargets() {
		var cmd *exec.Cmd
		if useWayland && wlErr == nil {
			cmd = exec.Command("wl-paste", "--no-newline", "--type", target)
		} else if xErr == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard", "-o", "-t", target)
		} else {
			continue
		}
		out, err := cmd.Output()
		if err != nil || len(bytes.TrimSpace(out)) == 0 {
			continue
		}
		return out, nil
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
