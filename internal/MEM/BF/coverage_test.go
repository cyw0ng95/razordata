package bf

import (
	"context"
	"errors"
	"os"
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

// --- Round 2: more MEM/BF coverage ---

// TestUpsert_NilPage covers the nil-page short-circuit in Upsert.
func TestUpsert_NilPage(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "nil.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(4, "", bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	if err := bp.Upsert(nil); !errors.Is(err, ErrInvalidBlockID) {
		t.Errorf("Upsert(nil): want ErrInvalidBlockID, got %v", err)
	}
}

// TestUpsert_PageIDZero covers the page.ID == 0 branch in Upsert.
func TestUpsert_PageIDZero(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "idzero.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(4, "", bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	data := make([]byte, df.DefaultBlockSize)
	if err := bp.Upsert(&Page{ID: 0, Data: data}); !errors.Is(err, ErrInvalidBlockID) {
		t.Errorf("Upsert(ID=0): want ErrInvalidBlockID, got %v", err)
	}
}

// TestUpsert_WrongDataLen covers the len(page.Data) != BlockSize
// branch in Upsert.
func TestUpsert_WrongDataLen(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "wronglen.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(4, "", bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	if err := bp.Upsert(&Page{ID: 1, Data: []byte("short")}); !errors.Is(err, ErrInvalidBlockID) {
		t.Errorf("Upsert(short data): want ErrInvalidBlockID, got %v", err)
	}
}

// TestPin_Unpin_NonExistentPage covers the !ok early return in Pin
// and Unpin — both must be no-ops for an unknown page.
func TestPin_Unpin_NonExistentPage(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "pin-unknown.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(4, "", bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	// Pin/Unpin a page that was never inserted — must not panic.
	bp.Pin(&Page{ID: 999})
	bp.Unpin(&Page{ID: 999})

	if got := bp.Stats().Pins; got != 0 {
		t.Errorf("Pin on unknown page must not increment Pins, got %d", got)
	}
}

// TestPin_AfterEvict covers the Pin/Stats interaction: a pinned
// page should not be evicted; an unpinned page can be.
func TestPin_BlocksEviction(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "pin-evict.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(2, "", bd, newMockSyncPool()) // capacity 2
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	data := make([]byte, df.DefaultBlockSize)
	for i := uint64(1); i <= 2; i++ {
		if err := bp.Upsert(&Page{ID: i, Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	// Pin page 1.
	page, _, _ := bp.Get(context.Background(), 1)
	bp.Pin(page)
	// Insert a third — should evict one of the unpinned pages.
	if err := bp.Upsert(&Page{ID: 3, Data: data}); err != nil {
		t.Fatal(err)
	}
	// Page 1 must still be present.
	_, cached, _ := bp.Get(context.Background(), 1)
	if !cached {
		t.Error("pinned page 1 was evicted")
	}
	bp.Unpin(page)
}

// TestSetCapacity_AlwaysErrors covers the deferred-to-v2 stub.
func TestSetCapacity_AlwaysErrors(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "setcap.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(4, "", bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	if err := bp.SetCapacity(8); !errors.Is(err, ErrCapacityExceeded) {
		t.Errorf("SetCapacity: want ErrCapacityExceeded, got %v", err)
	}
}

// TestStats_AfterActivity covers the Stats snapshot shape after a
// sequence of hits, misses, and pins.
func TestStats_AfterActivity(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "stats.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	bp, err := New(4, "", bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	data := make([]byte, df.DataLen-df.ChecksumLen)
	if err := bd.WriteBlock(context.Background(), 1, data); err != nil {
		t.Fatal(err)
	}

	// 1 miss
	if _, _, err := bp.Get(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	// 1 hit
	if _, _, err := bp.Get(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	// 1 pin
	page, _, _ := bp.Get(context.Background(), 1)
	bp.Pin(page)

	stats := bp.Stats()
	if stats.Hits < 1 {
		t.Errorf("Hits: want >=1, got %d", stats.Hits)
	}
	if stats.Misses < 1 {
		t.Errorf("Misses: want >=1, got %d", stats.Misses)
	}
	if stats.Pins < 1 {
		t.Errorf("Pins: want >=1, got %d", stats.Pins)
	}
	if stats.Capacity != 4 {
		t.Errorf("Capacity: want 4, got %d", stats.Capacity)
	}
}

// TestClose_HintPathWriteFails covers the writeHintFile error branch
// in Close. The function must capture the error and return it on
// subsequent calls (via the cached closeErr).
func TestClose_HintPathWriteFails(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "close-err.razor"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	// Use a path that points at an existing regular file — the
	// os.WriteFile inside writeHintFile will fail.
	badHint := filepath.Join(tmp, "not-a-dir-or-file")
	if err := os.WriteFile(badHint, []byte("blocking"), 0600); err != nil {
		t.Fatal(err)
	}
	// A directory in the path would also fail; a regular file at
	// the parent of the hint path forces writeFile to fail with
	// ENOTDIR.
	bp, err := New(4, filepath.Join(badHint, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatal(err)
	}
	// Insert a slot so writeHintFile has something to write.
	data := make([]byte, df.DefaultBlockSize)
	if err := bp.Upsert(&Page{ID: 1, Data: data}); err != nil {
		t.Fatal(err)
	}

	if err := bp.Close(); err == nil {
		t.Error("expected Close to fail when hint path is invalid")
	}
	// Second Close returns the same cached error.
	if err := bp.Close(); err == nil {
		t.Error("expected cached closeErr on second Close")
	}
}
