// Package wr — SQLite-style wal-index hash table and locking (REQ001299).
//
// The -shm file layout:
//
//	[0..LockRegionSize)   lock-byte array (WalLockTotal × 4 bytes)
//	[LockRegionSize..HdrRegionEnd) checkpoint header
//	[HdrRegionEnd..)      hash table (WalHashTableR × WalHashTableNslot × 4)
//
// Lock byte semantics (per SQLite's WAL protocol):
//   - 0 = unlocked
//   - nonzero = locked (reader count for read locks, 1 for write/ckpt locks)
package wr

import (
	"errors"
	"sync/atomic"
	"unsafe"
)

const (
	// WalNReadLock is the number of read locks reserved in the
	// wal-index. SQLite reserves one read lock per CPU up to
	// MAX_READ_LOCKS, then a single write lock and a single ckpt
	// lock at indices WalNReadLock and WalNReadLock+1. REQ001299.
	WalNReadLock = 5

	// WalLockWriteLock is the index of the writer lock byte.
	WalLockWriteLock = WalNReadLock

	// WalLockCkptLock is the index of the checkpoint lock byte.
	WalLockCkptLock = WalNReadLock + 1

	// WalLockTotal is the size of the lock array.
	WalLockTotal = WalNReadLock + 2

	// LockRegionSize is the byte size of the lock-byte region in -shm.
	LockRegionSize = WalLockTotal * 4

	// WalIndexHdrSize is the byte size of the checkpoint header.
	WalIndexHdrSize = 32
)

// WalIndexHeader is the checkpoint header stored in the -shm file.
type WalIndexHeader struct {
	MxFrame   uint32 // highest frame index committed
	NBackfill uint32 // number of frames backfilled to main db
	Salt2     uint32 // salt2 at last checkpoint
	Readers   [WalNReadLock]uint32 // read-lock holder counts per slot
}

// WalShmSize returns the total byte size of the -shm file.
func WalShmSize() int {
	return LockRegionSize + WalIndexHdrSize + WalIndexSize()
}

// WalIndexSlotFor returns the hash-slot index for a (pageNo,
// salt1, salt2) triple, mirroring SQLite's walHash function.
func WalIndexSlotFor(pageNo uint32, salt1 uint32, salt2 uint32) uint32 {
	h := pageNo % WalHashTableNslot
	h = (h + ((salt1+salt2)>>1) + salt1) & (WalHashTableNslot - 1)
	return uint32(WalHashTableR) * h
}

// WalIndexSize returns the byte size of the wal-index hash table region.
func WalIndexSize() int {
	return WalHashTableR * WalHashTableNslot * 4
}

// WalLockByteSize returns the byte size of the wal-index lock-byte array.
func WalLockByteSize() int {
	return WalLockTotal * 4
}

// WalIndex is a process-local in-memory wal-index with lock primitives.
// REQ001299. Uses atomic operations for thread safety without MAP_SHARED.
// The real shm-backed implementation wraps an mmap'd region.
type WalIndex struct {
	locks [WalLockTotal]atomic.Uint32
	hdr   WalIndexHeader
	slots []atomic.Uint32
}

// NewWalIndex allocates a wal-index with the canonical slot count.
func NewWalIndex() *WalIndex {
	return &WalIndex{
		slots: make([]atomic.Uint32, WalHashTableR*WalHashTableNslot),
	}
}

// AcquireReadLock finds a free read-lock slot and acquires it.
// Returns the slot index on success. Returns ErrWalBusy if all
// read locks are held, or a write lock is active. REQ001299.
func (w *WalIndex) AcquireReadLock() (int, error) {
	if w.locks[WalLockWriteLock].Load() != 0 {
		return 0, ErrWalBusy
	}
	for i := 0; i < WalNReadLock; i++ {
		if w.locks[i].CompareAndSwap(0, 1) {
			return i, nil
		}
	}
	return 0, ErrWalBusy
}

// ReleaseReadLock releases a previously acquired read lock. REQ001299.
func (w *WalIndex) ReleaseReadLock(slot int) {
	if slot >= 0 && slot < WalNReadLock {
		w.locks[slot].Store(0)
	}
}

// TryAcquireWriteLock attempts to acquire the write lock without blocking.
// Returns nil on success, ErrWalBusy if the write lock is held or any
// read lock is active. REQ001299.
func (w *WalIndex) TryAcquireWriteLock() error {
	if !w.locks[WalLockWriteLock].CompareAndSwap(0, 1) {
		return ErrWalBusy
	}
	// Ensure no readers are active after taking the write lock.
	for i := 0; i < WalNReadLock; i++ {
		if w.locks[i].Load() != 0 {
			w.locks[WalLockWriteLock].Store(0)
			return ErrWalBusy
		}
	}
	return nil
}

// ReleaseWriteLock releases the write lock. REQ001299.
func (w *WalIndex) ReleaseWriteLock() {
	w.locks[WalLockWriteLock].Store(0)
}

// TryAcquireCkptLock attempts to acquire the checkpoint lock.
// Returns nil on success, ErrWalBusy if the ckpt lock is held.
func (w *WalIndex) TryAcquireCkptLock() error {
	if !w.locks[WalLockCkptLock].CompareAndSwap(0, 1) {
		return ErrWalBusy
	}
	return nil
}

// ReleaseCkptLock releases the checkpoint lock.
func (w *WalIndex) ReleaseCkptLock() {
	w.locks[WalLockCkptLock].Store(0)
}

// MxFrame returns the highest committed frame index.
func (w *WalIndex) MxFrame() uint32 { return w.hdr.MxFrame }

// SetMxFrame updates the highest committed frame index.
func (w *WalIndex) SetMxFrame(v uint32) { w.hdr.MxFrame = v }

// NBackfill returns the number of backfilled frames.
func (w *WalIndex) NBackfill() uint32 { return w.hdr.NBackfill }

// SetNBackfill updates the backfilled frame count.
func (w *WalIndex) SetNBackfill(v uint32) { w.hdr.NBackfill = v }

// Slot returns the current frame-offset at the slot for (pageNo, salt1, salt2).
// Returns 0 when empty.
func (w *WalIndex) Slot(pageNo, salt1, salt2 uint32) uint32 {
	return w.slots[WalIndexSlotFor(pageNo, salt1, salt2)].Load()
}

// Put updates the slot for (pageNo, salt1, salt2) to a frame offset.
func (w *WalIndex) Put(pageNo, salt1, salt2 uint32, frameOffset uint32) {
	w.slots[WalIndexSlotFor(pageNo, salt1, salt2)].Store(frameOffset)
}

// LockState returns the current lock byte values — useful for diagnostics.
func (w *WalIndex) LockState() [WalLockTotal]uint32 {
	var out [WalLockTotal]uint32
	for i := range out {
		out[i] = w.locks[i].Load()
	}
	return out
}

// ErrWalBusy is returned when a lock cannot be acquired.
var ErrWalBusy = errors.New("wr: WAL busy")

// ShmWalIndex wraps a MAP_SHARED mmap'd byte slice with lock and
// hash table operations. REQ001299. The layout matches the -shm
// file protocol: lock bytes, checkpoint header, hash table.
// Platform-neutral — the actual mmap is done by MmapShm (Linux)
// or returns an error on other platforms.
type ShmWalIndex struct {
	data []byte
}

// NewShmWalIndex wraps a byte slice (typically from an mmap'd -shm
// file) into a ShmWalIndex. Returns nil if the slice is too small.
func NewShmWalIndex(data []byte) *ShmWalIndex {
	if len(data) < WalShmSize() {
		return nil
	}
	return &ShmWalIndex{data: data}
}

// lockPtr returns a pointer to the uint32 at the given lock index.
func (s *ShmWalIndex) lockPtr(idx int) *uint32 {
	// Lock bytes start at offset 0 of the -shm file.
	return (*uint32)(unsafe.Pointer(&s.data[idx*4]))
}

// slotPtr returns a pointer to the uint32 at the given hash slot index.
func (s *ShmWalIndex) slotPtr(slotIdx uint32) *uint32 {
	// Hash table starts after locks + checkpoint header.
	off := LockRegionSize + WalIndexHdrSize + slotIdx*4
	return (*uint32)(unsafe.Pointer(&s.data[off]))
}

// AcquireReadLock finds a free read-lock slot and acquires it.
func (s *ShmWalIndex) AcquireReadLock() (int, error) {
	if atomic.LoadUint32(s.lockPtr(WalLockWriteLock)) != 0 {
		return 0, ErrWalBusy
	}
	for i := 0; i < WalNReadLock; i++ {
		if atomic.CompareAndSwapUint32(s.lockPtr(i), 0, 1) {
			return i, nil
		}
	}
	return 0, ErrWalBusy
}

// ReleaseReadLock releases a previously acquired read lock.
func (s *ShmWalIndex) ReleaseReadLock(slot int) {
	if slot >= 0 && slot < WalNReadLock {
		atomic.StoreUint32(s.lockPtr(slot), 0)
	}
}

// TryAcquireWriteLock attempts to acquire the write lock without blocking.
func (s *ShmWalIndex) TryAcquireWriteLock() error {
	if !atomic.CompareAndSwapUint32(s.lockPtr(WalLockWriteLock), 0, 1) {
		return ErrWalBusy
	}
	for i := 0; i < WalNReadLock; i++ {
		if atomic.LoadUint32(s.lockPtr(i)) != 0 {
			atomic.StoreUint32(s.lockPtr(WalLockWriteLock), 0)
			return ErrWalBusy
		}
	}
	return nil
}

// ReleaseWriteLock releases the write lock.
func (s *ShmWalIndex) ReleaseWriteLock() {
	atomic.StoreUint32(s.lockPtr(WalLockWriteLock), 0)
}

// TryAcquireCkptLock attempts to acquire the checkpoint lock.
func (s *ShmWalIndex) TryAcquireCkptLock() error {
	if !atomic.CompareAndSwapUint32(s.lockPtr(WalLockCkptLock), 0, 1) {
		return ErrWalBusy
	}
	return nil
}

// ReleaseCkptLock releases the checkpoint lock.
func (s *ShmWalIndex) ReleaseCkptLock() {
	atomic.StoreUint32(s.lockPtr(WalLockCkptLock), 0)
}

// Slot returns the frame offset at the slot for (pageNo, salt1, salt2).
func (s *ShmWalIndex) Slot(pageNo, salt1, salt2 uint32) uint32 {
	return atomic.LoadUint32(s.slotPtr(WalIndexSlotFor(pageNo, salt1, salt2)))
}

// Put updates the slot for (pageNo, salt1, salt2) to a frame offset.
func (s *ShmWalIndex) Put(pageNo, salt1, salt2 uint32, frameOffset uint32) {
	atomic.StoreUint32(s.slotPtr(WalIndexSlotFor(pageNo, salt1, salt2)), frameOffset)
}

// MxFrame returns the highest committed frame index from shm header.
func (s *ShmWalIndex) MxFrame() uint32 {
	return atomic.LoadUint32(s.hdrPtr())
}

// SetMxFrame updates the highest committed frame index in shm header.
func (s *ShmWalIndex) SetMxFrame(v uint32) {
	atomic.StoreUint32(s.hdrPtr(), v)
}

// NBackfill returns the number of backfilled frames from shm header.
func (s *ShmWalIndex) NBackfill() uint32 {
	return atomic.LoadUint32(s.hdrPtrOffset(4))
}

// SetNBackfill updates the backfilled frame count in shm header.
func (s *ShmWalIndex) SetNBackfill(v uint32) {
	atomic.StoreUint32(s.hdrPtrOffset(4), v)
}

// hdrPtr returns pointer to the checkpoint header's first uint32 field.
func (s *ShmWalIndex) hdrPtr() *uint32 {
	return (*uint32)(unsafe.Pointer(&s.data[LockRegionSize]))
}

// hdrPtrOffset returns pointer to the checkpoint header at byte offset.
func (s *ShmWalIndex) hdrPtrOffset(off int) *uint32 {
	return (*uint32)(unsafe.Pointer(&s.data[LockRegionSize+off]))
}

// WalIndexSnapshot provides a read-only snapshot overlay.
// Kept for backward compatibility. REQ001299.
type WalIndexSnapshot struct {
	slots []uint32
}

// NewWalIndexSnapshot allocates an empty index snapshot.
func NewWalIndexSnapshot() *WalIndexSnapshot {
	return &WalIndexSnapshot{
		slots: make([]uint32, WalHashTableR*WalHashTableNslot),
	}
}

// Slot returns the frame-offset at the slot for (pageNo, salt1, salt2).
func (w *WalIndexSnapshot) Slot(pageNo, salt1, salt2 uint32) uint32 {
	return w.slots[WalIndexSlotFor(pageNo, salt1, salt2)]
}

// Put updates the slot for (pageNo, salt1, salt2).
func (w *WalIndexSnapshot) Put(pageNo, salt1, salt2 uint32, frameOffset uint32) {
	w.slots[WalIndexSlotFor(pageNo, salt1, salt2)] = frameOffset
}

// Size returns the slot count.
func (w *WalIndexSnapshot) Size() int {
	return len(w.slots)
}
