//go:build linux

package clipboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	native "golang.design/x/clipboard"
)

// Run only against an isolated compositor/display: this test replaces its clipboard.
func TestIsolatedLinuxClipboardRoundTrip(t *testing.T) {
	if os.Getenv("CLIPCASCADE_ISOLATED_CLIPBOARD") != "1" {
		t.Skip("requires a dedicated test compositor")
	}
	if !strings.Contains(os.Getenv("XDG_RUNTIME_DIR"), "clipcascade-test-") {
		t.Fatal("refusing to replace a non-test clipboard")
	}
	t.Setenv("PATH", t.TempDir())
	if err := initClipboardBackend(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := native.Write(ctx, native.FmtText, []byte("initial clipboard")); err != nil {
		t.Fatal(err)
	}
	// Watch establishes its baseline before returning, making the first change test deterministic.
	events := native.Watch(ctx, native.FmtText, native.FmtImage, native.FmtFiles,
		native.Register(targetGnomeCopied), native.Register(targetMateCopied))
	type copyEvent struct{ payload, kind, filename string }
	copies := make(chan copyEvent, 20)
	m := NewManager()
	m.OnCopy(func(payload, kind, filename string) { copies <- copyEvent{payload, kind, filename} })
	done := make(chan struct{})
	go func() { defer close(done); m.watchLinuxEvents(ctx, events, readLinuxSnapshot) }()
	defer func() { cancel(); <-done }()
	waitCopy := func() copyEvent {
		t.Helper()
		select {
		case event := <-copies:
			return event
		case <-time.After(6 * time.Second):
			t.Fatal("clipboard change was not delivered")
			return copyEvent{}
		}
	}
	if err := setPlatformText("first copy"); err != nil {
		t.Fatal(err)
	}
	if event := waitCopy(); event.kind != "text" || event.payload != "first copy" {
		t.Fatalf("wrong first event: %+v", event)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 180, A: 255})
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, img); err != nil {
		t.Fatal(err)
	}
	if err := setPlatformImage(pngBytes.Bytes()); err != nil {
		t.Fatal(err)
	}
	if event := waitCopy(); event.kind != "image" || contentHash(event.payload, "image") != contentHash(base64.StdEncoding.EncodeToString(pngBytes.Bytes()), "image") {
		t.Fatalf("wrong image event: type=%s", event.kind)
	}
	path := filepath.Join(t.TempDir(), "文件 one.txt")
	if err := os.WriteFile(path, []byte("file probe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := setPlatformFilePaths([]string{path}); err != nil {
		t.Fatal(err)
	}
	if event := waitCopy(); event.kind != "file_eager" || event.filename != filepath.Base(path) || event.payload != base64.StdEncoding.EncodeToString([]byte("file probe")) {
		t.Fatalf("wrong file event: type=%s name=%s", event.kind, event.filename)
	}
	formats, err := native.Formats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	available := make(map[string]bool)
	for _, format := range formats {
		available[format.MIME()] = true
	}
	for _, target := range []string{targetTextURIList, targetGnomeCopied, targetMateCopied} {
		if !available[target] {
			t.Errorf("missing file manager representation: %s", target)
		}
	}
	// A remote paste must not echo back before the next local copy.
	m.Paste("remote text", "text", "")
	if err := setPlatformText("second local copy"); err != nil {
		t.Fatal(err)
	}
	if event := waitCopy(); event.kind != "text" || event.payload != "second local copy" {
		t.Fatalf("remote echo or lost local copy: %+v", event)
	}
}
