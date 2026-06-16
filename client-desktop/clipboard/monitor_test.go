package clipboard

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/clipcascade/pkg/constants"
)

func TestHandleChangeAllowsFallbackAfterDuplicateCandidate(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(30 * x), G: uint8(40 * y), B: 180, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	imagePayload := base64.StdEncoding.EncodeToString(buf.Bytes())

	m := NewManager()
	var gotType string
	var gotPayload string
	m.OnCopy(func(payload string, payloadType string, filename string) {
		gotType = payloadType
		gotPayload = payload
	})

	if !m.handleChange(imagePayload, constants.TypeImage, "") {
		t.Fatal("expected first image to be forwarded")
	}

	if m.handleChange(imagePayload, constants.TypeImage, "") {
		t.Fatal("expected duplicate image to be skipped")
	}

	textPayload := "second clipboard payload"
	if !m.handleChange(textPayload, constants.TypeText, "") {
		t.Fatal("expected fallback text payload to be forwarded")
	}

	if gotType != constants.TypeText || gotPayload != textPayload {
		t.Fatalf("unexpected forwarded payload: type=%q payload=%q", gotType, gotPayload)
	}
}

func TestPasteLocalFileSuppressesMatchingFileEcho(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote.mp4")
	if err := os.WriteFile(path, []byte("video payload"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := NewManager()
	var got []string
	m.OnCopy(func(payload string, payloadType string, filename string) {
		got = append(got, payloadType+":"+payload)
	})

	m.mu.Lock()
	m.lastHash = fileClipboardHash([]string{path})
	m.mu.Unlock()

	payload, filename, ok := buildFileEagerPayload(path)
	if !ok {
		t.Fatal("expected small file to use eager payload")
	}
	if m.handleChange(payload, constants.TypeFileEager, filename) {
		t.Fatal("expected file written by remote paste to be skipped")
	}
	if len(got) != 0 {
		t.Fatalf("expected no echoed payload, got %#v", got)
	}

	other := filepath.Join(dir, "local.mp4")
	if err := os.WriteFile(other, []byte("different video payload"), 0o644); err != nil {
		t.Fatalf("write other file: %v", err)
	}
	otherPayload, otherName, ok := buildFileEagerPayload(other)
	if !ok {
		t.Fatal("expected other small file to use eager payload")
	}
	if !m.handleChange(otherPayload, constants.TypeFileEager, otherName) {
		t.Fatal("expected different local file to be forwarded")
	}
	if len(got) != 1 || got[0] != constants.TypeFileEager+":"+otherPayload {
		t.Fatalf("unexpected forwarded payloads: %#v", got)
	}
}

func TestFileStubHashUsesSingleFileContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote-large.mp4")
	content := bytes.Repeat([]byte("x"), int(constants.DefaultFileEagerThresholdBytes)+1)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	hashA := contentHash(path, constants.TypeFileStub)
	hashB := contentHash(filepath.Clean(path)+"\n", constants.TypeFileStub)
	if hashA != hashB {
		t.Fatalf("same single file should hash identically: %d != %d", hashA, hashB)
	}

	other := filepath.Join(dir, "other-large.mp4")
	if err := os.WriteFile(other, append([]byte("y"), content...), 0o644); err != nil {
		t.Fatalf("write other file: %v", err)
	}
	hashOther := contentHash(other, constants.TypeFileStub)
	if hashOther == hashA {
		t.Fatal("different file content should not share file_stub hash")
	}
}

func TestConsumeIgnoredChangeCountOnlyOnce(t *testing.T) {
	m := NewManager()
	m.ignoreChangeCounts = map[int64]struct{}{42: {}}

	if !m.consumeIgnoredChangeCount(42) {
		t.Fatal("expected registered change count to be ignored")
	}
	if m.consumeIgnoredChangeCount(42) {
		t.Fatal("expected ignored change count to be consumed only once")
	}
	if m.consumeIgnoredChangeCount(43) {
		t.Fatal("unexpected ignore for unrelated change count")
	}
}
