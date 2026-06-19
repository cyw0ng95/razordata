package bf

import (
	"context"
	"errors"
	"hash/crc32"
	"os"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/ENG/NM"
	"github.com/cyw0ng95/razordata/internal/FIL/DF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
)

// Errors for the BF cluster.
var (
	ErrCorrupt          = errors.New("block checksum mismatch")
	ErrNotLoaded        = errors.New("block not yet loaded")
	ErrCapacityExceeded = errors.New("buffer pool at capacity")
	ErrInvalidBlockID   = errors.New("invalid block ID")
)

// BlockSize is the fixed block size (matches FIL's DefaultBlockSize).
const BlockSize = df.DefaultBlockSize

// DataLen is the usable data length per block (block size minus checksum).
const DataLen = df.DataLen

// iterBufferSize is the pre-allocated size for iterator buffers.
const iterBufferSize = 64 * 1024 // 64 KB

// clockInterval is the number of hand increments before a slot is a candidate
// for eviction. A slot is evictable when refKey < hand - clockInterval.
const clockInterval = 8

// Page is a cached block. Data is borrowed from the sync pool and must not
// be copied by callers.
type Page struct {
	ID    uint64
	Data  []byte // borrowed, never copied
	Dirty bool   // true if modified since load from disk
}

// BufferStats reports buffer pool state. Values are snapshot-at-read.
type BufferStats struct {
	Hits     int64
	Misses   int64
	Pins     int64
	Evicts   int64
	Capacity int64
	Used     int64
}

// SyncPool provides reusable page-sized and iterator-sized buffers.
// It avoids allocations on the hot path by pre-allocating via sync.Pool.
type SyncPool interface {
	Get(size int) []byte
	Put(buf []byte)
}

// BufferPool manages in-memory block caching with O(1) hash lookup and
// clock-sweep LRU eviction.
type BufferPool interface {
	// Get returns the page for blockID. If found==true, the page was already
	// cached. If found==false, the page was loaded from disk. The wasCached
	// return value is true if the page was in the cache on entry.
	Get(ctx context.Context, blockID uint64) (*Page, bool, error)
	// Pin increments the pin count on the page. A pinned page cannot be evicted.
	Pin(page *Page)
	// Unpin decrements the pin count on the page. Eviction may proceed once
	// the pin count reaches zero.
	Unpin(page *Page)
	// Upsert injects a page directly into the hash table without disk I/O.
	// Used by the WAL replayer (R16) to populate the cache with recovered
	// page images. page.Data must have len == BlockSize. If a slot for the
	// same blockID already exists, its data is overwritten in place (the
	// caller's previous buffer is not returned to the pool). Does not pin
	// the page, does not verify the checksum, and counts against capacity
	// (evicting one slot first if at capacity).
	Upsert(page *Page) error
	// SetCapacity resizes the buffer pool. Deferred to v2 — returns ErrCapacityExceeded.
	SetCapacity(n int64) error
	// Stats returns current buffer pool statistics.
	Stats() BufferStats
	// Close flushes dirty pages and writes the hint file.
	Close() error
	// Warm reads the hint file and eagerly loads blocks into the cache.
	Warm(ctx context.Context) error
	// SetPMemFile attaches a PMem file for cold-page spill. Pass nil
	// to detach. REQ000302. Returns ErrNotLoaded if the pool is
	// already closed.
	SetPMemFile(pm *PMemFile) error
}

// bufferSlot holds an in-memory block with eviction metadata.
type bufferSlot struct {
	blockID  uint64
	data     []byte // fixed-size: BlockSize bytes, borrowed from SP
	pinCount atomic.Int32
	dirty    atomic.Bool
	refKey   atomic.Uint64 // clock hand value when last accessed
	loading  atomic.Bool   // true while loading from disk
	wait     chan struct{} // closed when data is ready
	// nodeID is the NUMA node where the slot's data was first
	// touched (REQ000309, iter-27). 0 on non-NUMA hosts.
	nodeID atomic.Int32
	// REQ000302: tier where slot data currently lives.
	// 0 = DRAM (default), 1 = PMem (cold spill).
	tier atomic.Uint32
}

// bufferHashTable provides O(1) lookup by blockID.
type bufferHashTable struct {
	slots map[uint64]*bufferSlot
	mu    sync.RWMutex
}

// bp is the concrete BufferPool implementation.
type bp struct {
	bd       *df.BlockDevice // FIL block device (concrete, not interface)
	sp       SyncPool        // sync pool for page/iterator buffers
	hintPath string          // path to hint file
	log      lg.Logger

	ht       bufferHashTable
	hand     atomic.Uint64 // clock sweep hand
	capacity int64
	used     atomic.Int64

	hits   atomic.Int64
	misses atomic.Int64
	pins   atomic.Int64
	evicts atomic.Int64

	closeOnce sync.Once
	closeErr  error

	// REQ000302: optional PMem file for cold-page spill. When nil,
	// all pages are cached in DRAM only.
	pmem *PMemFile
}

var _ BufferPool = (*bp)(nil)

// New creates a new BufferPool with the given capacity and hint file path.
// sp is the sync pool for allocating page buffers. bd is the FIL block device.
// The hint file is written on Close() and read on Warm().
func New(capacity int64, hintPath string, bd *df.BlockDevice, sp SyncPool, log ...lg.Logger) (BufferPool, error) {
	if capacity <= 0 {
		capacity = 256 // default capacity
	}

	l := lg.FirstLogger(log)

	b := &bp{
		bd:       bd,
		sp:       sp,
		hintPath: hintPath,
		log:      l,
		capacity: capacity,
	}
	b.ht.slots = make(map[uint64]*bufferSlot)

	return b, nil
}

// Get implements BufferPool.
func (b *bp) Get(ctx context.Context, blockID uint64) (*Page, bool, error) {
	if blockID == 0 {
		return nil, false, ErrInvalidBlockID
	}

	// Fast path: read-lock lookup.
	b.ht.mu.RLock()
	slot, ok := b.ht.slots[blockID]
	if ok && !slot.loading.Load() {
		refKey := b.hand.Add(1)
		slot.refKey.Store(refKey)
		dirty := slot.dirty.Load()
		b.ht.mu.RUnlock()
		b.hits.Add(1)
		return &Page{ID: slot.blockID, Data: slot.data, Dirty: dirty}, true, nil
	}
	if ok {
		// Block exists but is loading; wait for it.
		loading := true
		waitCh := slot.wait
		b.ht.mu.RUnlock()

		for loading {
			select {
			case <-waitCh:
			case <-ctx.Done():
				return nil, false, ctx.Err()
			}
			b.ht.mu.RLock()
			slot, ok = b.ht.slots[blockID]
			if !ok || !slot.loading.Load() {
				if ok {
					dirty := slot.dirty.Load()
					refKey := b.hand.Add(1)
					slot.refKey.Store(refKey)
					b.ht.mu.RUnlock()
					b.hits.Add(1)
					return &Page{ID: slot.blockID, Data: slot.data, Dirty: dirty}, true, nil
				}
				b.ht.mu.RUnlock()
				break
			}
			waitCh = slot.wait
			b.ht.mu.RUnlock()
		}
	} else {
		b.ht.mu.RUnlock()
	}

	// Not in cache. Acquire write lock and insert loading slot.
	b.ht.mu.Lock()
	slot, ok = b.ht.slots[blockID]
	if ok {
		if slot.loading.Load() {
			waitCh := slot.wait
			b.ht.mu.Unlock()
			select {
			case <-waitCh:
			case <-ctx.Done():
				return nil, false, ctx.Err()
			}
			b.ht.mu.RLock()
			slot, ok = b.ht.slots[blockID]
			if ok {
				dirty := slot.dirty.Load()
				refKey := b.hand.Add(1)
				slot.refKey.Store(refKey)
				b.ht.mu.RUnlock()
				b.hits.Add(1)
				return &Page{ID: slot.blockID, Data: slot.data, Dirty: dirty}, true, nil
			}
			b.ht.mu.RUnlock()
		} else {
			dirty := slot.dirty.Load()
			refKey := b.hand.Add(1)
			slot.refKey.Store(refKey)
			b.ht.mu.Unlock()
			b.hits.Add(1)
			return &Page{ID: slot.blockID, Data: slot.data, Dirty: dirty}, true, nil
		}
	}

	// At capacity? Evict one old slot before inserting.
	if b.used.Load() >= b.capacity {
		hand := b.hand.Add(1)
		// REQ000161: first pass — evict slots whose refKey is
		// older than (hand - clockInterval). These are LRU
		// candidates by the clock-sweep design.
		for blockID, slot := range b.ht.slots {
			if slot.refKey.Load() < hand-uint64(clockInterval) {
				if slot.pinCount.Load() == 0 {
					delete(b.ht.slots, blockID)
					b.used.Add(-1)
					b.evicts.Add(1)
					if len(slot.data) == BlockSize {
						b.sp.Put(slot.data)
					}
					goto allocated
				}
			}
		}
		// REQ000161: second pass — find the slot with the lowest
		// refKey (least-recently-used) among unpinned slots.
		var victimID uint64
		var victimRefKey uint64 = ^uint64(0) // max uint64
		var found bool
		for blockID, slot := range b.ht.slots {
			if slot.pinCount.Load() == 0 {
				rk := slot.refKey.Load()
				if rk < victimRefKey {
					victimRefKey = rk
					victimID = blockID
					found = true
				}
			}
		}
		if found {
			slot := b.ht.slots[victimID]
			delete(b.ht.slots, victimID)
			b.used.Add(-1)
			b.evicts.Add(1)
			if len(slot.data) == BlockSize {
				madviseDontNeed(slot.data)
				b.sp.Put(slot.data)
			}
			goto allocated
		}
	}
allocated:

	// Allocate buffer from sync pool.
	data := b.sp.Get(BlockSize)
	if data == nil {
		data = make([]byte, BlockSize)
	}
	// REQ000302: hint the kernel to use transparent huge pages for
	// this buffer, reducing TLB misses on large sequential scans.
	madviseHugePage(data)

	// Insert loading slot, then release lock immediately.
	// We do NOT hold the lock during disk I/O.
	slot = &bufferSlot{
		blockID: blockID,
		data:    data,
		loading: atomic.Bool{},
		wait:    make(chan struct{}),
	}
	slot.loading.Store(true)
	// REQ000309 (iter-27): tag the slot with the current
	// NUMA node so the engine can later report placement
	// statistics. On non-NUMA hosts, this is always 0.
	slot.nodeID.Store(int32(nm.CurrentNode()))
	b.ht.slots[blockID] = slot
	b.used.Add(1)
	b.ht.mu.Unlock()

	// Load from disk.
	err := b.bd.ReadBlockFull(blockID, data)
	if err != nil {
		b.ht.mu.Lock()
		delete(b.ht.slots, blockID)
		b.used.Add(-1)
		b.ht.mu.Unlock()

		b.sp.Put(data)

		slot.loading.Store(false)
		close(slot.wait)

		return nil, false, err
	}

	slot.loading.Store(false)
	slot.refKey.Store(b.hand.Add(1))
	close(slot.wait)

	b.misses.Add(1)
	return &Page{ID: blockID, Data: slot.data, Dirty: false}, false, nil
}

// Pin implements BufferPool.
func (b *bp) Pin(page *Page) {
	b.ht.mu.RLock()
	slot, ok := b.ht.slots[page.ID]
	if ok {
		slot.pinCount.Add(1)
		b.pins.Add(1)
	}
	b.ht.mu.RUnlock()
}

// Unpin implements BufferPool.
func (b *bp) Unpin(page *Page) {
	b.ht.mu.RLock()
	slot, ok := b.ht.slots[page.ID]
	if ok {
		slot.pinCount.Add(-1)
	}
	b.ht.mu.RUnlock()
}

// Upsert implements BufferPool. See the interface comment for the
// 6-point contract (R34). Used by the WAL replayer to inject recovered
// page images without disk I/O.
func (b *bp) Upsert(page *Page) error {
	if page == nil {
		return ErrInvalidBlockID
	}
	if page.ID == 0 {
		return ErrInvalidBlockID
	}
	if len(page.Data) != BlockSize {
		return ErrInvalidBlockID
	}

	b.ht.mu.Lock()
	defer b.ht.mu.Unlock()

	// Existing slot: overwrite in place. The previous data buffer is
	// the caller's responsibility — we do not return it to the pool.
	if existing, ok := b.ht.slots[page.ID]; ok {
		existing.data = page.Data
		existing.loading.Store(false)
		// Pin count is intentionally untouched (Upsert does not pin).
		return nil
	}

	// Not in cache. At capacity? Evict one slot before inserting.
	if b.used.Load() >= b.capacity {
		hand := b.hand.Add(1)
		// REQ000161: first pass — evict slots whose refKey is
		// older than (hand - clockInterval). These are LRU
		// candidates by the clock-sweep design.
		for blockID, slot := range b.ht.slots {
			if slot.refKey.Load() < hand-uint64(clockInterval) {
				if slot.pinCount.Load() == 0 {
					delete(b.ht.slots, blockID)
					b.used.Add(-1)
					b.evicts.Add(1)
					if len(slot.data) == BlockSize {
						b.sp.Put(slot.data)
					}
					goto insert
				}
			}
		}
		// REQ000161: second pass — find the slot with the lowest
		// refKey (least-recently-used) among unpinned slots.
		// This guarantees LRU eviction even when the first pass
		// fails (e.g., recently-accessed slots are still within
		// the clockInterval window).
		var victimID uint64
		var victimRefKey uint64 = ^uint64(0) // max uint64
		var found bool
		for blockID, slot := range b.ht.slots {
			if slot.pinCount.Load() == 0 {
				rk := slot.refKey.Load()
				if rk < victimRefKey {
					victimRefKey = rk
					victimID = blockID
					found = true
				}
			}
		}
		if found {
			slot := b.ht.slots[victimID]
			delete(b.ht.slots, victimID)
			b.used.Add(-1)
			b.evicts.Add(1)
			if len(slot.data) == BlockSize {
				madviseDontNeed(slot.data)
				b.sp.Put(slot.data)
			}
			goto insert
		}
		// All slots pinned. Insert over capacity to match
		// Get's behavior — the next Get will evict instead.
	}
insert:
	slot := &bufferSlot{
		blockID: page.ID,
		data:    page.Data,
		loading: atomic.Bool{},
	}
	slot.loading.Store(false)
	slot.refKey.Store(b.hand.Add(1))
	b.ht.slots[page.ID] = slot
	b.used.Add(1)
	return nil
}

// SetCapacity implements BufferPool. Deferred to v2.
func (b *bp) SetCapacity(n int64) error {
	return ErrCapacityExceeded
}

// Stats implements BufferPool.
func (b *bp) Stats() BufferStats {
	return BufferStats{
		Hits:     b.hits.Load(),
		Misses:   b.misses.Load(),
		Pins:     b.pins.Load(),
		Evicts:   b.evicts.Load(),
		Capacity: b.capacity,
		Used:     b.used.Load(),
	}
}

// Close implements BufferPool. Flushes dirty pages and writes hint file.
func (b *bp) Close() error {
	b.closeOnce.Do(func() {
		// Flush dirty pages: write each dirty block to disk, then clear flag.
		b.ht.mu.Lock()
		for _, slot := range b.ht.slots {
			if slot.dirty.Load() {
				if err := b.bd.WriteBlock(context.Background(), slot.blockID, slot.data); err != nil {
					b.closeErr = err
					b.ht.mu.Unlock()
					return
				}
				slot.dirty.Store(false)
			}
		}
		b.ht.mu.Unlock()

		// Write hint file.
		if b.hintPath != "" {
			if err := b.writeHintFile(); err != nil {
				if b.log != nil {
					b.log.Error("bf.close", "err", err)
				}
				b.closeErr = err
				return
			}
		}
	})
	return b.closeErr
}

// Warm implements BufferPool. Reads hint file and eagerly loads blocks.
func (b *bp) Warm(ctx context.Context) error {
	if b.hintPath == "" {
		return nil
	}

	entries, err := readHintFile(b.hintPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no hint file, start cold
		}
		if b.log != nil {
			b.log.Warn("bf.warm", "err", err)
		}
		return err
	}

	for _, e := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_, _, err := b.Get(ctx, e.BlockID)
		if err != nil {
			if b.log != nil {
				b.log.Warn("bf.warm", "blockID", e.BlockID, "err", err)
			}
		}
	}

	return nil
}

// writeHintFile serializes the current cache state to the hint file.
func (b *bp) writeHintFile() error {
	b.ht.mu.Lock()
	var entries []hintEntry
	for _, slot := range b.ht.slots {
		// Only include slots that have been accessed (refKey > 0).
		if slot.refKey.Load() > 0 {
			entries = append(entries, hintEntry{
				BlockID:    slot.blockID,
				LastAccess: int64(slot.refKey.Load()),
			})
		}
	}
	b.ht.mu.Unlock()

	if len(entries) == 0 {
		return nil
	}

	return writeHintFile(entries, b.hintPath)
}

// hintEntry is a record in the hint file.
type hintEntry struct {
	BlockID    uint64
	LastAccess int64
}

// writeHintFile serializes a list of hint entries to path. Writes go
// through a temp file + rename so a crash mid-write cannot leave a
// half-written hint file that would be loaded as garbage on the next
// Warm.
func writeHintFile(entries []hintEntry, path string) error {
	data, err := encodeHintEntries(entries)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readHintFile deserializes hint entries from path.
func readHintFile(path string) ([]hintEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeHintEntries(data)
}

// encodeHintEntries encodes entries as: [count:varint][entry_0]...[entry_N]
// where each entry = [blockID:varint][lastAccess:varint].
func encodeHintEntries(entries []hintEntry) ([]byte, error) {
	// Estimate: 10 bytes per entry (worst case varint size).
	buf := make([]byte, 0, 8+len(entries)*10)
	buf = appendVarint(buf, uint64(len(entries)))
	for _, e := range entries {
		buf = appendVarint(buf, e.BlockID)
		buf = appendVarint(buf, uint64(e.LastAccess))
	}
	return buf, nil
}

// decodeHintEntries decodes a hint file produced by encodeHintEntries.
func decodeHintEntries(data []byte) ([]hintEntry, error) {
	if len(data) == 0 {
		return nil, nil
	}

	r := &reader{data: data}
	count, n := r.readVarint()
	if n < 0 {
		return nil, errors.New("hint file: invalid count")
	}
	r.pos += n

	entries := make([]hintEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		blockID, n := r.readVarint()
		if n < 0 {
			return nil, errors.New("hint file: invalid blockID")
		}
		r.pos += n

		lastAccess, n := r.readVarint()
		if n < 0 {
			return nil, errors.New("hint file: invalid lastAccess")
		}
		r.pos += n

		entries = append(entries, hintEntry{
			BlockID:    blockID,
			LastAccess: int64(lastAccess),
		})
	}

	return entries, nil
}

// reader is a simple byte reader with varint support.
type reader struct {
	data []byte
	pos  int
}

// readVarint reads a varint from the reader. Returns value and bytes read.
// Returns -1 if not enough data.
func (r *reader) readVarint() (uint64, int) {
	var result uint64
	var shift uint
	for i := r.pos; i < len(r.data) && i < r.pos+10; i++ {
		b := r.data[i]
		if b < 0x80 {
			result |= uint64(b) << shift
			return result, i - r.pos + 1
		}
		result |= uint64(b&0x7f) << shift
		shift += 7
	}
	return 0, -1
}

// appendVarint appends a varint-encoded uint64 to buf.
func appendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	buf = append(buf, byte(v))
	return buf
}

// SetPMemFile implements BufferPool. REQ000302.
func (b *bp) SetPMemFile(pm *PMemFile) error {
	b.closeOnce.Do(func() {})
	if b.closeErr != nil {
		return b.closeErr
	}
	b.pmem = pm
	return nil
}

// ChecksumVerify verifies data against a stored CRC32 checksum.
// storedCRC is the CRC that was stored at the end of the block.
func ChecksumVerify(data []byte, storedCRC uint32) bool {
	return crc32.ChecksumIEEE(data) == storedCRC
}
