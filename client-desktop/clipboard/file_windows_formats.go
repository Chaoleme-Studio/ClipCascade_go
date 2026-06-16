package clipboard

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

const dropFilesHeaderSize = 20

func buildWindowsHDropPayload(paths []string) []byte {
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" {
			normalized = append(normalized, path)
		}
	}
	if len(normalized) == 0 {
		return nil
	}

	var fileList []uint16
	for _, path := range normalized {
		fileList = append(fileList, utf16.Encode([]rune(path))...)
		fileList = append(fileList, 0)
	}
	fileList = append(fileList, 0)

	out := make([]byte, dropFilesHeaderSize+len(fileList)*2)
	binary.LittleEndian.PutUint32(out[0:4], dropFilesHeaderSize)
	binary.LittleEndian.PutUint32(out[16:20], 1) // fWide
	for i, unit := range fileList {
		binary.LittleEndian.PutUint16(out[dropFilesHeaderSize+i*2:], unit)
	}
	return out
}
