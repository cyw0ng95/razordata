package ls

import (
	"context"
	"testing"
	"time"
)

// TestRateLimiter_BasicBurst verifies the token bucket allows an
// initial burst up to `burst` and then throttles to `rate` bytes/s.
// REQ000318.
func TestRateLimiter_BasicBurst(t *testing.T) {
	rl := NewRateLimiter(1000, 500)
	if rl == nil {
		t.Fatal("expected non-nil rate limiter")
	}
	// First 500 bytes should be near-instant (burst).
	start := time.Now()
	rl.Wait(500)
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		t.Errorf("burst should be near-instant, took %v", elapsed)
	}
	// Next 200 bytes should take ~200ms (200/1000 = 0.2s).
	start = time.Now()
	rl.Wait(200)
	elapsed = time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("expected ~200ms wait, got %v", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected <500ms wait, got %v", elapsed)
	}
}

// TestRateLimiter_NilSafe verifies a nil RateLimiter is a no-op.
func TestRateLimiter_NilSafe(t *testing.T) {
	var rl *RateLimiter
	rl.Wait(1000) // must not panic
}

// TestRateLimiter_ZeroRate verifies NewRateLimiter(0) returns nil
// (disabled).
func TestRateLimiter_ZeroRate(t *testing.T) {
	rl := NewRateLimiter(0, 0)
	if rl != nil {
		t.Errorf("expected nil for rate=0, got %v", rl)
	}
}

// TestCompactionManager_RateLimiter verifies SetRateLimiter
// installs/clears the limiter. REQ000318.
func TestCompactionManager_RateLimiter(t *testing.T) {
	dir := t.TempDir()
	mfst, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	// Initialize 6 levels (R0..R5).
	cur := mfst.current.Load()
	cur.levels = make([][]SSTFileMeta, 6)
	mfst.current.Store(cur)

	cm := newCompactionManager(DefaultFS(), dir, mfst)
	defer cm.Stop(context.Background())

	// Default: no limiter.
	if cm.rateLimiter.Load() != nil {
		t.Errorf("expected nil rate limiter initially")
	}
	rl := NewRateLimiter(1024*1024, 64*1024)
	cm.SetRateLimiter(rl)
	if cm.rateLimiter.Load() == nil {
		t.Errorf("expected rate limiter to be set")
	}
	cm.SetRateLimiter(nil)
	if cm.rateLimiter.Load() != nil {
		t.Errorf("expected rate limiter to be cleared")
	}
}
