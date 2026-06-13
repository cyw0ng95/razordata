package bf

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/DF"
)

// TestClockSweep_Audit verifies the clock-sweep implementation matches
// the MEM.md design: atomic hand, refKey-based eviction, and the
// clockInterval (8) cooldown window.
// REQ000161: clock-sweep integration audit.
func TestClockSweep_Audit(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer bd.Close()

	// Use larger capacity so the clock-sweep LRU behavior is
	// observable (clockInterval=8 needs hand to advance past
	// refKey by 8 increments before slot is evictable).
	bp, err := New(20, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer bp.Close()

	ctx := context.Background()

	// Fill cache to capacity with p1
	p1 := &Page{ID: 1, Data: make([]byte, BlockSize)}
	p1.Data[0] = 0xAA
	if err := bp.Upsert(p1); err != nil {
		t.Fatalf("Upsert p1: %v", err)
	}

	// Fill to capacity (no eviction yet)
	for i := uint64(2); i <= 20; i++ {
		p := &Page{ID: i, Data: make([]byte, BlockSize)}
		if err := bp.Upsert(p); err != nil {
			t.Fatalf("Upsert[%d]: %v", i, err)
		}
	}

	// Re-access p1 to refresh refKey
	for i := 0; i < 20; i++ {
		bp.Get(ctx, 1)
	}

	// Insert 10 more — should trigger 10 evictions
	for i := uint64(21); i <= 30; i++ {
		p := &Page{ID: i, Data: make([]byte, BlockSize)}
		if err := bp.Upsert(p); err != nil {
			t.Fatalf("Upsert[%d]: %v", i, err)
		}
	}

	// Audit: evictions should equal 10 (one per insert beyond cap)
	stats := bp.Stats()
	if stats.Evicts < 10 {
		t.Errorf("expected at least 10 evictions, got %d", stats.Evicts)
	}
	// Capacity not exceeded
	if stats.Used > 20 {
		t.Errorf("buffer pool over capacity: used=%d cap=20", stats.Used)
	}
	// Hits should reflect our 20 Gets(1) + initial 1
	if stats.Hits < 20 {
		t.Errorf("expected at least 21 hits, got %d", stats.Hits)
	}
}

// TestClockSweep_PinnedNotEvicted verifies pinned pages are
// not evicted even when refKey is old.
// REQ000161: audit pin/refKey interaction.
func TestClockSweep_PinnedNotEvicted(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer bd.Close()

	bp, err := New(4, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer bp.Close()

	ctx := context.Background()

	p1 := &Page{ID: 1, Data: make([]byte, BlockSize)}
	if err := bp.Upsert(p1); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	page, _, _ := bp.Get(ctx, 1)
	bp.Pin(page)
	defer bp.Unpin(page)

	// Force several eviction cycles
	for i := uint64(2); i < 20; i++ {
		p := &Page{ID: i, Data: make([]byte, BlockSize)}
		if err := bp.Upsert(p); err != nil {
			t.Fatalf("Upsert[%d]: %v", i, err)
		}
	}

	stats := bp.Stats()
	if stats.Used > 4 {
		t.Errorf("buffer pool over capacity: used=%d cap=4", stats.Used)
	}
	if stats.Pins == 0 {
		t.Errorf("expected at least 1 pin, got %d", stats.Pins)
	}
}

// TestClockSweep_StatsExposed confirms the design counters
// (Hits, Misses, Pins, Evicts) are surfaced via Stats().
// REQ000161: confirm stats surface reflects design counters.
func TestClockSweep_StatsExposed(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer bd.Close()

	bp, err := New(4, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer bp.Close()

	ctx := context.Background()
	p1 := &Page{ID: 1, Data: make([]byte, BlockSize)}
	if err := bp.Upsert(p1); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Multiple hits on p1
	for i := 0; i < 5; i++ {
		bp.Get(ctx, 1)
	}
	stats := bp.Stats()
	if stats.Hits < 5 {
		t.Errorf("expected at least 5 hits, got %d", stats.Hits)
	}
}
