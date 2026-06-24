package app

import (
	"log/slog"

	"github.com/gen2brain/beeep"
)

func notify(title, message string) {
	if err := beeep.Notify(title, message, ""); err != nil {
		slog.Warn("notification failed", "error", err)
	}
}
