package observability

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger 返回结构化 slog.Logger，默认 JSON。
func NewLogger(level string) *slog.Logger {
	lv := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	return slog.New(h)
}
