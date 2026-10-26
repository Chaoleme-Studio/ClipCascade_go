package config

import (
	"testing"

	"github.com/clipcascade/pkg/constants"
)

func TestParseFileRelaySize(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  int64
	}{
		{"2147483648", 2 << 30}, {"20GiB", 20 << 30},
		{" 512 mib ", 512 << 20}, {"4KiB", 4 << 10}, {"1TiB", 1 << 40},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseFileRelaySize(tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("got %d, %v; want %d", got, err, tc.want)
			}
		})
	}
	for _, input := range []string{"", "0", "-1", "1.5GiB", "huge", "9223372036854775807", "9223372036854775807TiB"} {
		if _, err := ParseFileRelaySize(input); err == nil {
			t.Errorf("expected %q to fail", input)
		}
	}
}

func TestLoadFileRelayLimit(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
		valid bool
	}{
		{"", constants.DefaultFileRelayMaxBytes, true},
		{"21474836480", 20 << 30, true}, {"20GiB", 20 << 30, true},
		{"0", 0, false}, {"-1", 0, false}, {"bad", 0, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("CC_FILE_RELAY_MAX_BYTES", tc.value)
			cfg := Load()
			if cfg.FileRelayMaxBytes != tc.want {
				t.Fatalf("got %d, want %d", cfg.FileRelayMaxBytes, tc.want)
			}
			if err := cfg.ValidateFileRelaySize(); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, validation error=%v", tc.valid, err)
			}
		})
	}
}
