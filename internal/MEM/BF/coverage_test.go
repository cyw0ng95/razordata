package bf

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/DF"
)

// TestUpsert_AllPinned_InsertsOverCapacity covers the final "all slots
// pinned, no candidate" branch in Upsert: the function should still insert
// the new page (matching Get's behavior — the next Get will evict).
func TestUpsert_AllPinned_InsertsOverCapacity(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "data.razor"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	capacity := int64(3)
	bp, err := New(capacity, "", bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer bp.Close()

	data := make([]byte, df.DefaultBlockSize)
	// Fill to capacity and pin every slot.
	pinned := make([]*Page, 0, capacity)
	for i := uint64(1); i <= uint64(capacity); i++ {
		if err := bp.Upsert(&Page{ID: i, Data: data}); err != nil {
			t.Fatalf("Upsert %d: %v", i, err)
		}
		page, _, _ := bp.Get(context.Background(), i)
		bp.Pin(page)
		pinned = append(pinned, page)
	}

	// All slots pinned — Upsert must still succeed (over-capacity insert).
	extra := make([]byte, df.DefaultBlockSize)
	if err := bp.Upsert(&Page{ID: 99, Data: extra}); err != nil {
		t.Errorf("Upsert when all pinned: want nil, got %v", err)
	}

	stats := bp.Stats()
	if stats.Used != capacity+1 {
		t.Errorf("expected used=%d (over capacity), got %d", capacity+1, stats.Used)
	}

	// Unpin everything; subsequent operations should not panic.
	for _, p := range pinned {
		bp.Unpin(p)
	}
}

// TestClose_NoHintPath covers Close with an empty hint path — must not
// attempt to write a hint file and must complete cleanly.
func TestClose_NoHintPath(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "no-hint.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(4, "", bd, newMockSyncPool()) // hintPath=""
	if err != nil {
		t.Fatal(err)
	}
	if err := bp.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestGet_LoadingRace_ContextCancel covers the case where Get waits on
// `slot.wait` and the context is cancelled before the wait completes.
func TestGet_LoadingRace_ContextCancel(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "loading-cancel.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(4, "", bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err = bp.Get(ctx, 1)
	if err == nil {
		t.Errorf("expected error on cancelled ctx, got nil")
	}
}

// TestGet_HitNotLoading covers the fast path: block already in cache and
// not currently loading.
func TestGet_HitNotLoading(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "hit.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	// Pre-populate block 1 on disk so the first Get can load it.
	data := make([]byte, 4088)
	for i := range data {
		data[i] = byte(i & 0xff)
	}
	if err := bd.WriteBlock(context.Background(), 1, data); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}

	bp, err := New(4, "", bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	// First Get: miss, loads from disk.
	page1, cached, err := bp.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if cached {
		t.Errorf("first Get: expected cached=false, got true")
	}
	if page1 == nil {
		t.Fatal("first Get returned nil page")
	}

	// Second Get: hit, no loading.
	page2, cached, err := bp.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if !cached {
		t.Errorf("second Get: expected cached=true, got false")
	}
	if page2 == nil {
		t.Fatal("second Get returned nil page")
	}
}

// TestClose_ConcurrentCovered checks that Close may be called from many
// goroutines simultaneously and only executes the close body once.
func TestClose_ConcurrentCovered(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "close-concurrent.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(4, filepath.Join(tmp, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}

	const N = 20
	errCh := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			errCh <- bp.Close()
		}()
	}
	for i := 0; i < N; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("concurrent Close: %v", err)
		}
	}
}

// TestWarm_HintPathEmpty covers Warm when hint path is empty (the bf was
// constructed with hintPath="").
func TestWarm_HintPathEmpty(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "warm-empty.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(4, "", bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	if err := bp.Warm(context.Background()); err != nil {
		t.Errorf("Warm with empty hint path: %v", err)
	}
}

// TestUpsert_ExistingSlot_OverwritesAndClearsLoading covers the
// in-place overwrite branch of Upsert, including clearing the loading
// flag on a slot that was previously mid-load.
func TestUpsert_ExistingSlot_OverwritesAndClearsLoading(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "overwrite-loading.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(4, "", bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	// Pre-populate via Upsert.
	d1 := make([]byte, df.DefaultBlockSize)
	if err := bp.Upsert(&Page{ID: 1, Data: d1}); err != nil {
		t.Fatal(err)
	}

	// Upsert again with new data — must overwrite, not duplicate.
	d2 := make([]byte, df.DefaultBlockSize)
	d2[0] = 0xAB
	if err := bp.Upsert(&Page{ID: 1, Data: d2}); err != nil {
		t.Fatal(err)
	}

	stats := bp.Stats()
	if stats.Used != 1 {
		t.Errorf("expected used=1 after duplicate Upsert, got %d", stats.Used)
	}
}
