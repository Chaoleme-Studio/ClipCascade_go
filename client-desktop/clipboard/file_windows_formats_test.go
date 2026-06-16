package clipboard

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

func TestBuildWindowsHDropPayloadUsesWideDropFilesList(t *testing.T) {
	payload := buildWindowsHDropPayload([]string{
		`C:\Temp\video.mp4`,
		`D:\Downloads\archive.zip`,
	})
	if len(payload) <= dropFilesHeaderSize {
		t.Fatalf("expected DROPFILES payload, got %d bytes", len(payload))
	}
	if got := binary.LittleEndian.Uint32(payload[0:4]); got != dropFilesHeaderSize {
		t.Fatalf("unexpected pFiles offset: got %d want %d", got, dropFilesHeaderSize)
	}
	if got := binary.LittleEndian.Uint32(payload[16:20]); got != 1 {
		t.Fatalf("expected wide-character file list, got fWide=%d", got)
	}

	units := make([]uint16, (len(payload)-dropFilesHeaderSize)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(payload[dropFilesHeaderSize+i*2:])
	}
	got := utf16.Decode(units)
	want := []rune("C:\\Temp\\video.mp4\x00D:\\Downloads\\archive.zip\x00\x00")
	if string(got) != string(want) {
		t.Fatalf("unexpected file list: got %q want %q", string(got), string(want))
	}
}
