package bf

import (
	"context"
	"errors"
	"hash/crc32"
	"os"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/FIL/DF"
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
)

var (
	ErrCorrupt          = errors.New("block checksum mismatch")
	ErrNotLoaded        = errors.New("block not yet loaded")
	ErrCapacityExceeded = errors.New("buffer pool at capacity")
	ErrInvalidBlockID   = errors.New("invalid block ID")
)

const (
	BlockSize      = df.DefaultBlockSize
	DataLen        = df.DataLen
	iterBufferSize = 64 * 1024 // 64 KB
	clockInterval  = 8
)

type Page struct {
	ID    uint64
	Data  []byte // borrowed, never copied
	Dirty bool   // true if modified since load from disk
}

// BufferStats reports buffer pool state.
type BufferStats struct {
	Hits     int64
	Misses   int64
	Pins     int64
	Evicts   int64
	Capacity int64
	Used     int64
}

// SyncPool provides reusable page-sized and iterator-sized buffers.
type SyncPool interface {
	Get(size int) []byte
	Put(buf []byte)
}

// Options configures the buffer pool.
type Options struct {
	ShardCount int // number of shards; default 32
	// NumaNodes is the number of NUMA nodes on the host; 0 means
	// NUMA is disabled (REQ001201).
	NumaNodes int
	// GetNode returns the current NUMA node id; nil disables NUMA
	// tagging (REQ001201).
	GetNode func() int
}

// BufferPool manages in-memory block caching.
type BufferPool interface {
	Get(ctx context.Context, blockID uint64) (*Page, bool, error)
	Pin(page *Page)
	Unpin(page *Page)
	Upsert(page *Page) error
	SetCapacity(n int64) error
	Stats() BufferStats
	Close() error
	Warm(ctx context.Context) error
	SetPMemFile(pm *PMemFile) error
}

// bufferSlot holds an in-memory block with eviction metadata.
type bufferSlot struct {
	blockID  uint64
	data     []byte // fixed-size: BlockSize bytes, borrowed from SP
	pinCount atomic.Int32
	dirty    atomic.Bool
	refKey   atomic.Uint64 // clock hand value when last accessed
	loading  atomic.Bool
	wait     chan struct{} // closed when data is ready
	// nodeID is the NUMA node where the slot's data was first
	// touched (REQ000309, iter-27). 0 on non-NUMA hosts.
	nodeID atomic.Int32
	// REQ000302: tier where slot data currently lives.
	// 0 = DRAM (default), 1 = PMem (cold spill).
	tier atomic.Uint32
	// REQ000549: version range for block-level MVCC.
	// Enables cross-transaction cache sharing with snapshot consistency.
	versionStartTS atomic.Uint64
	versionEndTS   atomic.Uint64
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
	hintPath string
	log      lg.Logger

	sbp      *shardedBufferPool // sharded buffer pool (REQ000539)
	capacity int64

	hits   atomic.Int64
	misses atomic.Int64
	pins   atomic.Int64
	evicts atomic.Int64

	closeOnce sync.Once
	closeErr  error

	// REQ000302: optional PMem file for cold-page spill. When nil,
	// all pages are cached in DRAM only.
	pmem    *PMemFile
	getNode func() int // REQ001201: NUMA node getter from options
}

var _ BufferPool = (*bp)(nil)

// New creates a new BufferPool with the given capacity and hint file path.
// sp is the sync pool for allocating page buffers. bd is the FIL block device.
// The hint file is written on Close() and read on Warm().
func New(capacity int64, hintPath string, bd *df.BlockDevice, sp SyncPool, log ...lg.Logger) (BufferPool, error) {
	return NewWithOptions(capacity, hintPath, bd, sp, Options{}, log...)
}

// NewWithOptions creates a new BufferPool with the given options.
func NewWithOptions(capacity int64, hintPath string, bd *df.BlockDevice, sp SyncPool, opts Options, log ...lg.Logger) (BufferPool, error) {
	if capacity <= 0 {
		capacity = 256 // default capacity
	}

	l := lg.FirstLogger(log)

	sbp := newShardedBufferPool(opts.ShardCount)

	b := &bp{
		bd:       bd,
		sp:       sp,
		hintPath: hintPath,
		log:      l,
		sbp:      sbp,
		capacity: capacity,
		getNode:  opts.GetNode,
	}

	return b, nil
}

// closeWaitCh closes a loading-barrier channel safely. It recovers
// from panic if the channel is already closed (double-close detection).
func closeWaitCh(ch chan struct{}, blockID uint64) {
	defer func() {
		if r := recover(); r != nil {
			EC.WARN_ON(true, "bf.closeWaitCh: panic closing slot.wait for block %d: %v", blockID, r)
		}
	}()
	close(ch)
}

// Get implements BufferPool.
func (b *bp) Get(ctx context.Context, blockID uint64) (*Page, bool, error) {
	if blockID == 0 {
		return nil, false, ErrInvalidBlockID
	}

	// Fast path: sharded R-lock lookup.
	slot := b.sbp.Slot(blockID)
	if slot != nil && !slot.loading.Load() {
		shard := b.sbp.shards[b.sbp.shardFor(blockID)]
		refKey := shard.hand.Add(1)
		slot.refKey.Store(refKey)
		dirty := slot.dirty.Load()
		b.hits.Add(1)
		return &Page{ID: slot.blockID, Data: slot.data, Dirty: dirty}, true, nil
	}
	if slot != nil {
		// Block exists but is loading; wait for it.
		EC.BUG_ON(slot.wait == nil, "bf.Get: loading slot %d has nil wait channel", blockID)
		waitCh := slot.wait
		for {
			select {
			case <-waitCh:
			case <-ctx.Done():
				return nil, false, ctx.Err()
			}
			slot = b.sbp.Slot(blockID)
			if slot == nil || !slot.loading.Load() {
				if slot != nil {
					dirty := slot.dirty.Load()
					shard := b.sbp.shards[b.sbp.shardFor(blockID)]
					refKey := shard.hand.Add(1)
					slot.refKey.Store(refKey)
					b.hits.Add(1)
					return &Page{ID: slot.blockID, Data: slot.data, Dirty: dirty}, true, nil
				}
				break
			}
			waitCh = slot.wait
		}
	}

	// Not in cache. Acquire write lock on the shard and insert loading slot.
	idx := b.sbp.shardFor(blockID)
	shard := b.sbp.shards[idx]
	shard.mu.Lock()

	// Double-check after acquiring write lock.
	slot, ok := shard.slots[blockID]
	if ok {
		if slot.loading.Load() {
			EC.BUG_ON(slot.wait == nil, "bf.Get: double-check loading slot %d has nil wait channel", blockID)
			waitCh := slot.wait
			shard.mu.Unlock()
			select {
			case <-waitCh:
			case <-ctx.Done():
				return nil, false, ctx.Err()
			}
			slot = b.sbp.Slot(blockID)
			if slot != nil {
				dirty := slot.dirty.Load()
				refKey := shard.hand.Add(1)
				slot.refKey.Store(refKey)
				b.hits.Add(1)
				return &Page{ID: slot.blockID, Data: slot.data, Dirty: dirty}, true, nil
			}
			shard.mu.Unlock()
		} else {
			dirty := slot.dirty.Load()
			refKey := shard.hand.Add(1)
			slot.refKey.Store(refKey)
			shard.mu.Unlock()
			b.hits.Add(1)
			return &Page{ID: slot.blockID, Data: slot.data, Dirty: dirty}, true, nil
		}
	}

	// At capacity? Evict one old slot from this shard before inserting.
	if b.sbp.totalUsed.Load() >= b.capacity {
		evictedData, ok := b.sbp.evictOne(0, shard, idx)
		if ok {
			b.evicts.Add(1)
			if len(evictedData) == BlockSize {
				b.sp.Put(evictedData)
			}
			goto allocated
		}
	}
allocated:

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
	slot.nodeID.Store(b.nodeID())
	shard.slots[blockID] = slot
	b.sbp.totalUsed.Add(1)
	shard.mu.Unlock()

	err := b.bd.ReadBlockFull(blockID, data)
	if err != nil {
		shard.mu.Lock()
		delete(shard.slots, blockID)
		b.sbp.totalUsed.Add(-1)
		shard.mu.Unlock()

		b.sp.Put(data)

		slot.loading.Store(false)
		closeWaitCh(slot.wait, blockID)

		return nil, false, err
	}

	slot.loading.Store(false)
	slot.refKey.Store(shard.hand.Add(1))
	closeWaitCh(slot.wait, blockID)

	b.misses.Add(1)
	return &Page{ID: blockID, Data: slot.data, Dirty: false}, false, nil
}

// GetWithTS returns a page whose version range covers readTS
// (REQ000549). If no version covers readTS, returns the latest
// version and the caller should filter via the version chain.
func (b *bp) GetWithTS(ctx context.Context, blockID uint64, readTS uint64) (*Page, bool, error) {
	page, found, err := b.Get(ctx, blockID)
	if err != nil || !found {
		return page, found, err
	}
	// For now, return the page as-is. The caller is responsible
	// for version chain filtering at read time.
	// Future: check versionStartTS/versionEndTS on the slot.
	return page, found, nil
}

// Pin implements BufferPool.
func (b *bp) Pin(page *Page) {
	slot := b.sbp.Slot(page.ID)
	if slot != nil {
		EC.BUG_ON(slot.pinCount.Load() < 0, "bf.Pin: pinCount underflow for page %d", page.ID)
		slot.pinCount.Add(1)
		b.pins.Add(1)
	}
}

// Unpin implements BufferPool.
func (b *bp) Unpin(page *Page) {
	slot := b.sbp.Slot(page.ID)
	if slot != nil {
		newCount := slot.pinCount.Add(-1)
		EC.WARN_ON(newCount < 0, "bf.Unpin: double-unpin for page %d", page.ID)
	}
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

	idx := b.sbp.shardFor(page.ID)
	shard := b.sbp.shards[idx]
	shard.mu.Lock()
	defer shard.mu.Unlock()

	// Existing slot: overwrite in place. The previous data buffer is
	// the caller's responsibility — we do not return it to the pool.
	if existing, ok := shard.slots[page.ID]; ok {
		existing.data = page.Data
		existing.loading.Store(false)
		// Pin count is intentionally untouched (Upsert does not pin).
		return nil
	}

	// Not in cache. At capacity? Evict one slot before inserting.
	if b.sbp.totalUsed.Load() >= b.capacity {
		evictedData, ok := b.sbp.evictOne(0, shard, idx)
		if ok {
			b.evicts.Add(1)
			if len(evictedData) == BlockSize {
				b.sp.Put(evictedData)
			}
			goto insert
		}
	}
insert:
	slot := &bufferSlot{
		blockID: page.ID,
		data:    page.Data,
		loading: atomic.Bool{},
	}
	slot.loading.Store(false)
	slot.refKey.Store(shard.hand.Add(1))
	shard.slots[page.ID] = slot
	b.sbp.totalUsed.Add(1)
	return nil
}

// SetCapacity implements BufferPool. Deferred to v2.
func (b *bp) SetCapacity(n int64) error {
	return ErrCapacityExceeded
}

// nodeID returns the current NUMA node (REQ001201). Defaults to 0 if no
// GetNode function is configured.
func (b *bp) nodeID() int32 {
	if b.getNode != nil {
		return int32(b.getNode())
	}
	return 0
}

// Stats implements BufferPool.
func (b *bp) Stats() BufferStats {
	return BufferStats{
		Hits:     b.hits.Load(),
		Misses:   b.misses.Load(),
		Pins:     b.pins.Load(),
		Evicts:   b.evicts.Load(),
		Capacity: b.capacity,
		Used:     b.sbp.totalUsed.Load(),
	}
}

// Close implements BufferPool. Flushes dirty pages and writes hint file.
func (b *bp) Close() error {
	b.closeOnce.Do(func() {
		// Flush dirty pages: write each dirty block to disk, then clear flag.
		// Lock all shards for consistency.
		for _, shard := range b.sbp.shards {
			shard.mu.Lock()
		}
		for _, shard := range b.sbp.shards {
			for _, slot := range shard.slots {
				if slot.dirty.Load() {
					if err := b.bd.WriteBlock(context.Background(), slot.blockID, slot.data); err != nil {
						b.closeErr = err
						for _, s := range b.sbp.shards {
							s.mu.Unlock()
						}
						return
					}
					slot.dirty.Store(false)
				}
			}
		}
		for _, shard := range b.sbp.shards {
			shard.mu.Unlock()
		}

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
	// Lock all shards.
	for _, shard := range b.sbp.shards {
		shard.mu.Lock()
	}
	var entries []hintEntry
	for _, shard := range b.sbp.shards {
		for _, slot := range shard.slots {
			// Only include slots that have been accessed (refKey > 0).
			if slot.refKey.Load() > 0 {
				entries = append(entries, hintEntry{
					BlockID:    slot.blockID,
					LastAccess: int64(slot.refKey.Load()),
				})
			}
		}
	}
	for _, shard := range b.sbp.shards {
		shard.mu.Unlock()
	}

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
