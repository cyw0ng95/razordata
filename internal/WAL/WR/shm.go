// Package wr — SQLite-style wal-index hash table (REQ001299).
//
// This file is the in-memory skeleton for the SQLite-style WAL
// indexing layer. The real readers/writer concurrency protocol
// (-shm mapping, atomic lock acquisition, transactional semantics)
// is intentionally not present here — it belongs to a future
// iteration that lands together with the -wal/-shm file plumbing
// in Engine.Open.
//
// The skeleton owns:
//   - the lock-byte array sized for the multi-reader/writer protocol
//     (Locks array length = N_LOCK + 1),
//   - the hash table slot count (WalHashTableR * WalHashTableNslot
//     32-bit words),
//   - the addressing function Slot(pageNo, salt1, salt2) that maps a
//     triple to a slot index in the table.
//
// The -shm file would carry the same layout, with ShmSize() returning
// the byte count to allocate or mmap. Frame offsets stored in the
// table are 32 bits per SQLite's design — page index pointers in
// razordata will fit if the WAL file is bounded (with segment
// fallback; see TODO).
package wr

const (
	// WalNReadLock is the number of read locks reserved in the
	// wal-index. SQLite reserves one read lock per CPU up to
	// MAX_READ_LOCKS, then a single write lock and a single ckpt
	// lock at indices WalNReadLock and WalNReadLock+1. REQ001299.
	//
	// The skeleton reserves the canonical 5 since the constant is
	// baked into the SQLite-compatible on-disk layout; production
	// razordata will likely raise it to match the number of readers
	// it actually expects to run concurrently.
	WalNReadLock = 5

	// WalLockWriteLock is the index of the writer lock byte. REQ001299.
	WalLockWriteLock = WalNReadLock

	// WalLockCkptLock is the index of the checkpoint lock byte.
	// REQ001299.
	WalLockCkptLock = WalNReadLock + 1

	// WalLockTotal is the size of the lock array. REQ001299.
	WalLockTotal = WalNReadLock + 2
)

// WalIndexSlotFor returns the hash-slot index for a (pageNo,
// salt1, salt2) triple, mirroring SQLite's walHash function:
//
//	h = (pageNo % HASHTABLE_NSLOT)
//	h += (salt1 + salt2)/2 + salt1
//	h &= (HASHTABLE_NSLOT - 1)
//	return HASH_TABLE_R * h
//
// REQ001299. The reader and writer must agree on this function or
// lookups diverge silently; we expose it here for the replayer test
// suite to import and assert.
func WalIndexSlotFor(pageNo uint32, salt1 uint32, salt2 uint32) uint32 {
	h := pageNo % WalHashTableNslot
	h = (h + ((salt1+salt2)>>1) + salt1) & (WalHashTableNslot - 1)
	return uint32(WalHashTableR) * h
}

// WalIndexSize returns the byte size of the wal-index hash table
// region of the -shm file. Each slot stores a single uint32 (a WAL
// frame offset); the table has WalHashTableR * WalHashTableNslot
// slots. REQ001299.
//
// (The real -shm file is larger — it also reserves space for the
// checkpoint header (mxFrame, nBackfill, salt2, readers count) and
// the lock-byte array. ShmSize() composes the full layout when
// -shm plumbing lands in a later iteration. For now this constant is
// the only piece of the layout integrated with the rest of the
// package.)
func WalIndexSize() int {
	return WalHashTableR * WalHashTableNslot * 4
}

// WalLockByteSize returns the byte size of the wal-index lock-byte
// array. Real -shm layout will place this immediately before the
// hash table; callers building a -shm region need it. REQ001299.
func WalLockByteSize() int {
	return WalLockTotal * 4
}

// WalIndexSnapshot is a process-local view of a wal-index hash table.
// The real -shm backed implementation will hold an *os.File mmap'd
// with MAP_SHARED. The skeleton holds a Go slice so it can be unit
// tested without cross-process semantics. REQ001299.
type WalIndexSnapshot struct {
	slots []uint32
}

// NewWalIndexSnapshot allocates an empty index snapshot with the
// canonical slot count.
func NewWalIndexSnapshot() *WalIndexSnapshot {
	return &WalIndexSnapshot{
		slots: make([]uint32, WalHashTableR*WalHashTableNslot),
	}
}

// Slot returns the current frame-offset value stored at the slot
// for (pageNo, salt1, salt2). REQ001299. Returns 0 when the slot
// is empty (zero-valued), matching SQLite's behavior.
func (w *WalIndexSnapshot) Slot(pageNo, salt1, salt2 uint32) uint32 {
	return w.slots[WalIndexSlotFor(pageNo, salt1, salt2)]
}

// Put updates the slot for the given triple to a new frame offset.
// The salt pair is the same one the writer used; readers verify on
// lookup that stored salt1/salt2 match before trusting the frame
// offset. The skeleton accepts them here to keep the API symmetric
// with the future -shm backed implementation.
func (w *WalIndexSnapshot) Put(pageNo, salt1, salt2 uint32, frameOffset uint32) {
	w.slots[WalIndexSlotFor(pageNo, salt1, salt2)] = frameOffset
}

// Size returns the slot count — useful for tests that compare
// snapshot identities.
func (w *WalIndexSnapshot) Size() int {
	return len(w.slots)
}
