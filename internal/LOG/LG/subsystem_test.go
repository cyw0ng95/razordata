//go:build debug

package lg

import (
	"log/slog"
	"testing"
)

func TestSubsystemLogger_DefaultLevel(t *testing.T) {
	logger := NewSubsystemLogger("ENG", slog.LevelInfo)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
	logger.Info("test info message")
}

func TestSubsystemLogger_LevelOverride(t *testing.T) {
	SetSubsystemLevel("ENG", slog.LevelDebug)
	lvl := GetSubsystemLevel("ENG")
	if lvl != slog.LevelDebug {
		t.Errorf("expected Debug level, got %v", lvl)
	}

	SetSubsystemLevel("ENG", slog.LevelWarn)
	lvl = GetSubsystemLevel("ENG")
	if lvl != slog.LevelWarn {
		t.Errorf("expected Warn level, got %v", lvl)
	}
}

func TestSubsystemLogger_UnknownSubsystem(t *testing.T) {
	lvl := GetSubsystemLevel("NONEXISTENT")
	if lvl != slog.LevelInfo {
		t.Errorf("expected Info for unknown subsystem, got %v", lvl)
	}
}
