//go:build linux

package clipboard

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	native "golang.design/x/clipboard"
)

func TestLinuxSnapshotMIMEPriority(t *testing.T) {
	text := native.FmtText
	img := native.FmtImage
	file := native.FmtFiles
	dir := t.TempDir()
	path := filepath.Join(dir, "文件 one.txt")
	if err := os.WriteFile(path, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		formats []native.Format
		data    map[native.Format][]byte
		want    string
	}{
		{"image before text", []native.Format{text, img}, map[native.Format][]byte{text: []byte("text"), img: []byte("png")}, "image/png"},
		{"file before image", []native.Format{text, img, file}, map[native.Format][]byte{file: []byte(buildURIList([]string{path})), img: []byte("png"), text: []byte("text")}, targetTextURIList},
		{"web URL remains text", []native.Format{text, file}, map[native.Format][]byte{file: []byte("https://example.com"), text: []byte("link")}, text.MIME()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, err := readLinuxSnapshotFrom(context.Background(), tc.formats, func(_ context.Context, f native.Format) ([]byte, error) { return tc.data[f], nil })
			if err != nil || snapshot.target != tc.want {
				t.Fatalf("target=%q, want %q, err=%v", snapshot.target, tc.want, err)
			}
		})
	}
}

func TestLinuxFileWritePublishesFormatsForAllDesktops(t *testing.T) {
	items := linuxFileClipboardItems([]string{"/tmp/文件 one.txt"})
	got := make(map[string]string)
	for _, option := range items {
		item, ok := option.(native.Item)
		if !ok {
			t.Fatalf("unexpected option: %T", option)
		}
		got[item.Format.MIME()] = string(item.Bytes)
	}
	if len(got) != 3 || !strings.HasPrefix(got[targetTextURIList], "file:///") {
		t.Fatalf("missing standard file target: %v", got)
	}
	for _, mime := range []string{targetGnomeCopied, targetMateCopied} {
		if got[mime] != "copy\n"+got[targetTextURIList] {
			t.Fatalf("bad file representation for %s", mime)
		}
	}
}

func TestLinuxSendsFirstCopyAndSuppressesRemoteEcho(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events := make(chan native.Data)
	snapshots := make(chan linuxClipboardSnapshot)
	read := func(ctx context.Context) (linuxClipboardSnapshot, error) {
		select {
		case s := <-snapshots:
			return s, nil
		case <-ctx.Done():
			return linuxClipboardSnapshot{}, ctx.Err()
		}
	}
	m := NewManager()
	copies := make(chan string, 4)
	m.OnCopy(func(payload, kind, filename string) { copies <- payload })
	done := make(chan struct{})
	go func() { defer close(done); m.watchLinuxEvents(ctx, events, read) }()
	feed := func(text string) {
		t.Helper()
		select {
		case events <- native.Data{}:
		case <-ctx.Done():
			t.Fatal("watcher stopped")
		}
		select {
		case snapshots <- linuxClipboardSnapshot{target: "text/plain", data: []byte(text)}:
		case <-ctx.Done():
			t.Fatal("watcher stopped reading")
		}
	}
	feed("first copy")
	select {
	case got := <-copies:
		if got != "first copy" {
			t.Fatalf("first copy lost: %q", got)
		}
	case <-ctx.Done():
		t.Fatal("first copy lost")
	}
	m.mu.Lock()
	m.lastHash = contentHash("remote text", "text")
	m.mu.Unlock()
	feed("remote text")
	feed("second copy")
	select {
	case got := <-copies:
		if got != "second copy" {
			t.Fatalf("remote echo was sent: %q", got)
		}
	case <-ctx.Done():
		t.Fatal("second copy lost")
	}
	cancel()
	<-done
}

func TestNativeLinuxClipboardWithoutCommandLineHelpers(t *testing.T) {
	if os.Getenv("CLIPCASCADE_TEST_WAYLAND") != "1" {
		t.Skip("requires an active desktop; read-only probe")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"wayland", "x11"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "x11" && os.Getenv("DISPLAY") == "" {
				t.Skip("no X11 compatibility display available")
			}
			cmd := exec.Command(exe, "-test.run=^TestNativeLinuxReadOnlyHelper$", "-test.v")
			cmd.Env = append(os.Environ(), "CLIPCASCADE_NATIVE_HELPER=1", "PATH="+t.TempDir())
			if mode == "wayland" {
				cmd.Env = append(cmd.Env, "DISPLAY=")
			} else {
				cmd.Env = append(cmd.Env, "WAYLAND_DISPLAY=")
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s native probe: %v\n%s", mode, err, out)
			}
		})
	}
}

func TestNativeLinuxReadOnlyHelper(t *testing.T) {
	if os.Getenv("CLIPCASCADE_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	if err := initClipboardBackend(); err != nil {
		t.Fatal(err)
	}
	if _, err := readLinuxSnapshot(context.Background()); err != nil && !errors.Is(err, native.ErrNoData) {
		t.Fatal(err)
	}
}

func TestMissingDesktopDoesNotRecommendInstallingAnotherClipboard(t *testing.T) {
	if os.Getenv("CLIPCASCADE_MISSING_DISPLAY_HELPER") == "1" {
		err := initClipboardBackend()
		if !errors.Is(err, native.ErrUnavailable) || strings.Contains(err.Error(), "apt install") || strings.Contains(strings.ToLower(err.Error()), "xvfb") {
			t.Fatalf("unexpected desktop capability diagnostic: %v", err)
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestMissingDesktopDoesNotRecommendInstallingAnotherClipboard$")
	cmd.Env = append(os.Environ(), "CLIPCASCADE_MISSING_DISPLAY_HELPER=1", "PATH="+t.TempDir(), "DISPLAY=", "WAYLAND_DISPLAY=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("missing display probe: %v\n%s", err, out)
	}
}
