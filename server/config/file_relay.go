package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/clipcascade/pkg/constants"
)

// ParseFileRelaySize accepts integer byte counts or binary KiB/MiB/GiB/TiB units.
func ParseFileRelaySize(value string) (int64, error) {
	number := strings.ToUpper(strings.TrimSpace(value))
	multiplier := int64(1)
	for i, suffix := range []string{"KIB", "MIB", "GIB", "TIB"} {
		if strings.HasSuffix(number, suffix) {
			number = strings.TrimSpace(strings.TrimSuffix(number, suffix))
			multiplier = int64(1) << (10 * (i + 1))
			break
		}
	}
	count, err := strconv.ParseInt(number, 10, 64)
	// Fiber's BodyLimit uses int and also needs room for multipart overhead.
	maxBytes := int64(^uint(0)>>1) - 1024*1024
	if err != nil || count <= 0 || count > maxBytes/multiplier {
		return 0, fmt.Errorf("file limit must be a positive byte count or integer KiB/MiB/GiB/TiB size fitting this platform")
	}
	return count * multiplier, nil
}

func (c *Config) ValidateFileRelaySize() error {
	_, err := ParseFileRelaySize(strconv.FormatInt(c.FileRelayMaxBytes, 10))
	return err
}

func fileRelayMaxBytesFromEnv() int64 {
	value := strings.TrimSpace(os.Getenv("CC_FILE_RELAY_MAX_BYTES"))
	if value == "" {
		return constants.DefaultFileRelayMaxBytes
	}
	size, _ := ParseFileRelaySize(value)
	// Invalid input is rejected by startup validation, unless overridden by a flag.
	return size
}
