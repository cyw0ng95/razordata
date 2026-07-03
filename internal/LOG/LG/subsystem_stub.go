//go:build !debug

package lg

import "log/slog"

// NewSubsystemLogger returns a plain slog.Logger without level overrides.
// With the debug tag, this is replaced by a version that reads per-subsystem
// atomic level overrides.
func NewSubsystemLogger(_ string, _ slog.Level) *slog.Logger {
	return slog.Default()
}

// SetSubsystemLevel is a no-op without the debug tag.
func SetSubsystemLevel(_ string, _ slog.Level) {}

// GetSubsystemLevel returns slog.LevelInfo without the debug tag.
func GetSubsystemLevel(_ string) slog.Level {
	return slog.LevelInfo
}
