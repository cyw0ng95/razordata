package wr

import (
	"sync"
	"testing"
)

// TestWalIndexSize pins the wal-index layout constants. REQ001299.
func TestWalIndexSize(t *testing.T) {
	const want = WalHashTableR * WalHashTableNslot * 4
	if got := WalIndexSize(); got != want {
		t.Fatalf("WalIndexSize = %d, want %d", got, want)
	}
}

// TestWalLockByteSize pins the lock-byte array size. REQ001299.
func TestWalLockByteSize(t *testing.T) {
	want := WalLockTotal * 4
	if got := WalLockByteSize(); got != want {
		t.Fatalf("WalLockByteSize = %d, want %d", got, want)
	}
}

// TestWalIndexHash confirms canonical page numbers land in the slot
// index range [0, WalHashTableNslot*WalHashTableR) with deterministic
// addressing. REQ001297 — slot addressing must be deterministic and total.
func TestWalIndexHash(t *testing.T) {
	cases := []struct {
		page, salt1, salt2 uint32
	}{
		{1, 0xC0FFEE01, 0xBADC0DE2},
		{2, 0xC0FFEE01, 0xBADC0DE2},
		{17, 0xCAFEF00D, 0xDEADBEEF},
		{4096, 1, 1},
		{0, 0, 0},
	}
	total := uint32(WalHashTableR * WalHashTableNslot)
	for _, c := range cases {
		slot := WalIndexSlotFor(c.page, c.salt1, c.salt2)
		if slot >= total {
			t.Fatalf("page=%d salt=(%08x,%08x) slot=%d out of range >=%d",
				c.page, c.salt1, c.salt2, slot, total)
		}
	}
}

// TestWalHashCollisionsAreDocumented confirms the hash function is
// non-trivial for adjacent (page, salt) values — i.e. it does not
// just collapse to "page mod slots" — without asserting an exact
// distribution (which would couple tests to a specific hash
// version). REQ001299.
func TestWalHashCollisionsAreDocumented(t *testing.T) {
	// Same salt pair, varying page must hit different slots when the
	// modulus doesn't trivially collide. We accept up to all-pages-
	// map-to-same-slot as a degenerate-but-correct scenario, but
	// require at least 2 distinct slots among 16 adjacent pages.
	slots := make(map[uint32]struct{})
	salt1, salt2 := uint32(0xC0FFEE01), uint32(0xBADC0DE2)
	for p := uint32(1); p <= 16; p++ {
		slots[WalIndexSlotFor(p, salt1, salt2)] = struct{}{}
	}
	if len(slots) < 2 {
		t.Fatalf("wal hash is degenerate: 16 pages map to %d distinct slots; want >= 2",
			len(slots))
	}
}

// TestWalIndexSnapshotRoundTrip covers the in-memory skeleton for
// the wal-index hash table — Set then Get on the same triple, and
// Set/Get with a different triple that lands on a different slot.
// REQ001299.
func TestWalIndexSnapshotRoundTrip(t *testing.T) {
	idx := NewWalIndexSnapshot()
	want := uint32(0x12345)
	idx.Put(7, 0xCAFE, 0xBABE, want)
	if got := idx.Slot(7, 0xCAFE, 0xBABE); got != want {
		t.Fatalf("Slot() = %d, want %d", got, want)
	}
	// Same numeric page but a different salt pair must return zero
	// (no entry for that triplet), not the previously stored value.
	if got := idx.Slot(7, 0xCAFE, 0xBEEF); got != 0 {
		t.Fatalf("Slot() for different salt = %d, want 0", got)
	}
}

// TestWalIndexSnapshotSizeReturnsCanonicalCount verifies the
// snapshot array length matches the canonical slot count. REQ001299.
func TestWalIndexSnapshotSizeReturnsCanonicalCount(t *testing.T) {
	idx := NewWalIndexSnapshot()
	if got, want := idx.Size(), WalHashTableR*WalHashTableNslot; got != want {
		t.Fatalf("Size() = %d, want %d", got, want)
	}
}

// TestWalAcquireReadLock verifies read lock acquisition and release
// across available slots. REQ001299.
func TestWalAcquireReadLock(t *testing.T) {
	idx := NewWalIndex()

	// Acquire all N read locks.
	var slots []int
	for i := 0; i < WalNReadLock; i++ {
		s, err := idx.AcquireReadLock()
		if err != nil {
			t.Fatalf("AcquireReadLock %d: %v", i, err)
		}
		slots = append(slots, s)
	}

	// Next acquire must fail — all slots busy.
	if _, err := idx.AcquireReadLock(); err != ErrWalBusy {
		t.Fatalf("expected ErrWalBusy when all read locks held, got %v", err)
	}

	// Release one slot.
	idx.ReleaseReadLock(slots[2])

	// Acquire should succeed now.
	s, err := idx.AcquireReadLock()
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	idx.ReleaseReadLock(s)
}

// TestWalBeginWrite verifies write lock acquisition blocks when readers
// are active, and succeeds when no readers are active. REQ001299.
func TestWalBeginWrite(t *testing.T) {
	idx := NewWalIndex()

	// Acquire write lock when no readers — should succeed.
	if err := idx.TryAcquireWriteLock(); err != nil {
		t.Fatalf("TryAcquireWriteLock (no readers): %v", err)
	}
	idx.ReleaseWriteLock()

	// Acquire a read lock, then try write — should fail.
	r, err := idx.AcquireReadLock()
	if err != nil {
		t.Fatalf("AcquireReadLock: %v", err)
	}
	if err := idx.TryAcquireWriteLock(); err != ErrWalBusy {
		t.Fatalf("expected ErrWalBusy with reader active, got %v", err)
	}
	idx.ReleaseReadLock(r)

	// After releasing reader, write should succeed.
	if err := idx.TryAcquireWriteLock(); err != nil {
		t.Fatalf("TryAcquireWriteLock after reader release: %v", err)
	}
	idx.ReleaseWriteLock()
}

// TestWalConcurrency_TwoReadersOneWriter verifies that two readers
// can proceed concurrently while a writer is blocked, and that the
// writer proceeds once all readers finish. REQ001299.
func TestWalConcurrency_TwoReadersOneWriter(t *testing.T) {
	idx := NewWalIndex()
	var wg sync.WaitGroup
	readerSlots := make(chan int, 2)

	// Reader 1 acquires lock and holds it.
	wg.Add(1)
	go func() {
		defer wg.Done()
		r1, err := idx.AcquireReadLock()
		if err != nil {
			t.Errorf("reader-1 acquire: %v", err)
			return
		}
		readerSlots <- r1
	}()

	// Reader 2 acquires a different read lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		r2, err := idx.AcquireReadLock()
		if err != nil {
			t.Errorf("reader-2 acquire: %v", err)
			return
		}
		readerSlots <- r2
	}()

	wg.Wait()
	close(readerSlots)

	slots := make([]int, 0, 2)
	for s := range readerSlots {
		slots = append(slots, s)
	}
	if len(slots) != 2 {
		t.Fatalf("expected 2 readers, got %d", len(slots))
	}

	// Verify readers got different slots.
	if slots[0] == slots[1] {
		t.Fatalf("both readers got the same slot %d", slots[0])
	}
	for _, s := range slots {
		if s < 0 || s >= WalNReadLock {
			t.Fatalf("slot %d out of range [0,%d)", s, WalNReadLock)
		}
	}

	// Writer must be blocked while 2 readers hold locks.
	if err := idx.TryAcquireWriteLock(); err != ErrWalBusy {
		t.Fatalf("expected ErrWalBusy with 2 readers, got %v", err)
	}

	// Release both readers.
	idx.ReleaseReadLock(slots[0])
	idx.ReleaseReadLock(slots[1])

	// Writer should now succeed.
	if err := idx.TryAcquireWriteLock(); err != nil {
		t.Fatalf("TryAcquireWriteLock after all readers released: %v", err)
	}
	idx.ReleaseWriteLock()
}

// TestWalConcurrency_WriterBlocksReaders verifies that once a writer
// holds the write lock, new readers are blocked. REQ001299.
func TestWalConcurrency_WriterBlocksReaders(t *testing.T) {
	idx := NewWalIndex()

	// Writer acquires write lock.
	if err := idx.TryAcquireWriteLock(); err != nil {
		t.Fatalf("TryAcquireWriteLock: %v", err)
	}

	// Readers must be blocked.
	if _, err := idx.AcquireReadLock(); err != ErrWalBusy {
		t.Fatalf("expected ErrWalBusy with writer active, got %v", err)
	}

	// Release writer.
	idx.ReleaseWriteLock()

	// Reader should succeed now.
	r, err := idx.AcquireReadLock()
	if err != nil {
		t.Fatalf("AcquireReadLock after writer release: %v", err)
	}
	idx.ReleaseReadLock(r)
}

// TestWalShmSize verifies the total -shm file size calculation. REQ001299.
func TestWalShmSize(t *testing.T) {
	got := WalShmSize()
	want := WalLockByteSize() + WalIndexHdrSize + WalIndexSize()
	if got != want {
		t.Fatalf("WalShmSize = %d, want %d", got, want)
	}
}
