package reverb

import (
	"log/slog"
	"os"
)

// newLogger returns a slog.Logger configured for the given mode.
// "prod" uses JSON output; all other values (including "") use human-readable text.
func newLogger(mode string) *slog.Logger {
	if mode == "prod" {
		return slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}
