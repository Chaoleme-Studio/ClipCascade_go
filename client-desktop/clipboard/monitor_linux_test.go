//go:build linux

package clipboard

import (
	"strings"
	"testing"
)

func TestBuildURIListEscapesFilePaths(t *testing.T) {
	got := buildURIList([]string{"/tmp/文件 one.txt"})
	if !strings.Contains(got, "file:///tmp/") {
		t.Fatalf("expected file URI, got %q", got)
	}
	if !strings.Contains(got, "%20") {
		t.Fatalf("expected spaces to be escaped, got %q", got)
	}
	if strings.Contains(got, " ") {
		t.Fatalf("URI list should not contain raw spaces, got %q", got)
	}
}

// 统信 UOS / deepin 的 dde-file-manager 只认 x-special/gnome-copied-files，
// 写 text/uri-list 会导致粘贴不出文件。
func TestDetectFileClipboardFormatForUOS(t *testing.T) {
	cases := []struct {
		desktop     string
		wantTarget  string
		wantPrelude bool
	}{
		{"DDE", targetGnomeCopied, true},
		{"Deepin", targetGnomeCopied, true},
		{"deepin", targetGnomeCopied, true},
		{"GNOME", targetGnomeCopied, true},
		{"X-Cinnamon", targetGnomeCopied, true},
		{"", targetGnomeCopied, true},
		{"KDE", targetTextURIList, false},
		{"MATE", targetMateCopied, true},
	}
	for _, tc := range cases {
		t.Setenv("XDG_CURRENT_DESKTOP", tc.desktop)
		got := detectFileClipboardFormat()
		if got.target != tc.wantTarget || got.gnomePrelude != tc.wantPrelude {
			t.Fatalf("Desktop=%q: got {target:%q prelude:%v}, want {target:%q prelude:%v}",
				tc.desktop, got.target, got.gnomePrelude, tc.wantTarget, tc.wantPrelude)
		}
	}
}

func TestBuildFileClipboardDataGnomePrelude(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "DDE")
	target, payload := buildFileClipboardData([]string{"/tmp/ClipCascade/a.txt"})
	if target != targetGnomeCopied {
		t.Fatalf("expected gnome target, got %q", target)
	}
	if !strings.HasPrefix(payload, "copy\nfile:///tmp/ClipCascade/a.txt") {
		t.Fatalf("expected copy-prefixed uri, got %q", payload)
	}
}

func TestBuildFileClipboardDataKDE(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
	target, payload := buildFileClipboardData([]string{"/tmp/a.txt"})
	if target != targetTextURIList {
		t.Fatalf("expected text/uri-list, got %q", target)
	}
	if strings.HasPrefix(payload, "copy\n") {
		t.Fatalf("KDE payload must not carry the gnome prelude, got %q", payload)
	}
}

func TestBuildFileClipboardDataEmpty(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "DDE")
	if _, payload := buildFileClipboardData([]string{"", "  "}); payload != "" {
		t.Fatalf("expected empty payload, got %q", payload)
	}
}
