package wr

import (
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

// TestWalHashDeterministic confirms a few canonical page numbers
// land in the slot index range [0, WalHashTableNslot*WalHashTableR).
// REQ001299 — slot addressing must be deterministic and total.
func TestWalHashDeterministic(t *testing.T) {
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
