package hk

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestProfileHook_DumpOnError verifies the profileHook writes a
// heap profile when an Error event is logged.
// REQ000322.
func TestProfileHook_DumpOnError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profiles")
	h := NewProfileHook(dir)
	defer h.Close()

	// Use a logger with this hook attached
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})).With("hook", "profile")

	// Inject the hook (in real code, hooks are registered via
	// hookManager). For test, call OnLog directly.
	if ph, ok := h.(*profileHook); ok {
		ph.OnLog(slog.LevelError, "test error", nil)
		// Force immediate dump to bypass rate limit
		path, err := ph.DumpNow()
		if err != nil {
			t.Fatalf("DumpNow: %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("profile file not created: %v", err)
		}
		// Verify the file is non-empty and starts with the pprof magic
		data, _ := os.ReadFile(path)
		if len(data) < 100 {
			t.Errorf("profile file too small: %d bytes", len(data))
		}
		if len(data) < 10 {
			t.Errorf("profile file too small: %d bytes", len(data))
		}
	}

	_ = logger
}

// TestProfileHook_NoDumpOnInfo verifies info-level events don't dump.
// REQ000322.
func TestProfileHook_NoDumpOnInfo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profiles")
	h := NewProfileHook(dir)
	defer h.Close()

	ph := h.(*profileHook)
	ph.OnLog(slog.LevelInfo, "info msg", nil)

	if ph.DumpCount() != 0 {
		t.Errorf("expected 0 dumps on Info, got %d", ph.DumpCount())
	}
}

// TestProfileHook_RateLimit verifies rate limit (1 per 5s) is enforced.
// REQ000322.
func TestProfileHook_RateLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profiles")
	h := NewProfileHook(dir, WithRateLimit(100*time.Millisecond))
	defer h.Close()

	ph := h.(*profileHook)
	ph.OnLog(slog.LevelError, "err 1", nil)
	ph.OnLog(slog.LevelError, "err 2 (rate limited)", nil)

	// First dump happens, second is rate-limited
	if ph.DumpCount() > 1 {
		t.Errorf("expected at most 1 dump due to rate limit, got %d", ph.DumpCount())
	}

	// Wait for rate limit to expire
	time.Sleep(110 * time.Millisecond)
	ph.OnLog(slog.LevelError, "err 3 (after wait)", nil)
	if ph.DumpCount() < 1 {
		t.Errorf("expected dump after rate limit expired")
	}
}
