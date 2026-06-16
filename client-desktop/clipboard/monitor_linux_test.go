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
