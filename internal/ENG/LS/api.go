package ls

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
)

var tombstoneValue = []byte{0xDE, 0xAD, 0xBE, 0xEF}

// REQ001257: sync.Pool for mergeIterator and sub-iterators.
// Pool returns a struct that is reset (cleared mutable state) on
// acquire and the caller returns the struct to the pool via the
// corresponding release* helper. The pool does not guarantee
// pointer identity — functional reuse is the contract.
var (
	mergeIteratorPool = sync.Pool{
		New: func() any { return &mergeIterator{} },
	}
	memtableIterPool = sync.Pool{
		New: func() any { return &memtableIter{} },
	}
	sstIterPool = sync.Pool{
		New: func() any { return &sstIter{} },
	}
)

// acquireMergeIterator returns a zeroed mergeIterator from the pool.
// REQ001257.
func acquireMergeIterator() *mergeIterator {
	mi := mergeIteratorPool.Get().(*mergeIterator)
	mi.reset()
	return mi
}

// releaseMergeIterator returns a mergeIterator to the pool after
// the caller has closed all sources. The pool will hand the same
// (or a different) struct to a future acquirer with all mutable
// state cleared. REQ001257.
func releaseMergeIterator(mi *mergeIterator) {
	if mi == nil {
		return
	}
	mi.reset()
	mergeIteratorPool.Put(mi)
}

// reset clears mutable state of a mergeIterator so it can be
// handed back to the pool without leaking references to closed
// sources or stale heap entries. The fixed configuration
// (fs/manifest/dir/blockCache) is set per-init and not reset.
// Slices keep their backing array capacity for reuse. REQ001257
// + REQ001258.
func (mi *mergeIterator) reset() {
	mi.closed.Store(false)
	mi.curKey = nil
	mi.curVal = nil
	mi.err = nil
	// Detach sources slice — keep capacity for reuse.
	for i := range mi.sources {
		mi.sources[i] = nil
	}
	mi.sources = mi.sources[:0]
	// Clear heap; keep backing array capacity. REQ001261.
	mi.h.n = 0
	mi.h.items = mi.h.items[:0]
	// Clear per-source key/value ring slots in place — keep
	// backing array capacity. REQ001258.
	for i := range mi.sourceKeys {
		mi.sourceKeys[i] = mi.sourceKeys[i][:0]
		mi.sourceVals[i] = mi.sourceVals[i][:0]
	}
	mi.sourceKeys = mi.sourceKeys[:0]
	mi.sourceVals = mi.sourceVals[:0]
}

// acquireMemtableIter returns a zeroed memtableIter from the pool.
// REQ001257.
func acquireMemtableIter() *memtableIter {
	mi := memtableIterPool.Get().(*memtableIter)
	mi.it = nil
	return mi
}

// releaseMemtableIter returns a memtableIter to the pool after
// its underlying Iterator has been exhausted or closed. REQ001257.
func releaseMemtableIter(m *memtableIter) {
	if m == nil {
		return
	}
	m.it = nil
	memtableIterPool.Put(m)
}

// resetMemtableIter clears the iter field so the pooled struct
// can be safely reused. Exported for tests.
func resetMemtableIter(m *memtableIter) {
	if m == nil {
		return
	}
	m.it = nil
}

// acquireSSTIter returns a zeroed sstIter from the pool. REQ001257.
func acquireSSTIter() *sstIter {
	si := sstIterPool.Get().(*sstIter)
	si.it = nil
	si.data = nil
	return si
}

// releaseSSTIter returns an sstIter to the pool after its
// underlying iterator has been exhausted or closed. REQ001257.
func releaseSSTIter(s *sstIter) {
	if s == nil {
		return
	}
	s.it = nil
	s.data = nil
	sstIterPool.Put(s)
}

func isTombstone(v []byte) bool {
	return bytes.Equal(v, tombstoneValue)
}

// Engine is the public storage handle for the LSM engine.
type Engine struct {
	e *engine
}

// Open creates or opens a database stored in dir.
func Open(dir string) (*Engine, error) {
	e, err := newEngine(dir)
	if err != nil {
		return nil, err
	}
	return &Engine{e: e}, nil
}

// OpenWithOptions creates or opens a database with the given options. REQ000537.
func OpenWithOptions(dir string, opts Options) (*Engine, error) {
	e, err := newEngineWithOptions(dir, opts)
	if err != nil {
		return nil, err
	}
	return &Engine{e: e}, nil
}

// Insert writes a key-value pair to the engine. Triggers a flush when the
// active memtable exceeds its size threshold.
func (eng *Engine) Insert(key, value []byte) error {
	if eng == nil || eng.e == nil {
		return ErrClosed
	}
	return eng.e.Write(key, value)
}

// Get returns the value stored at key, or ErrNotFound.
func (eng *Engine) Get(key []byte) ([]byte, error) {
	if eng == nil || eng.e == nil {
		return nil, ErrClosed
	}
	v, err := eng.e.Read(key)
	if err != nil {
		return nil, err
	}
	if isTombstone(v) {
		return nil, ErrNotFound
	}
	return v, nil
}

// Delete writes a tombstone for key.
func (eng *Engine) Delete(key []byte) error {
	if eng == nil || eng.e == nil {
		return ErrClosed
	}
	return eng.e.Write(key, tombstoneValue)
}

// NewIterator returns an iterator over all live keys with the given prefix.
func (eng *Engine) NewIterator(prefix []byte) RangeIter {
	e := eng.e
	e.mu.RLock()

	// Build list of memtables to iterate over
	var memtables []*memtable

	// Add active memtable shards
	if e.activeMem != nil {
		for _, shard := range e.activeMem.shards() {
			if !shard.IsFrozen() {
				memtables = append(memtables, shard)
			}
		}
	}

	// Add frozen memtables
	for _, mt := range e.memtables {
		// frozen memtables are stored as memtableAdapter in the slice
		if adapter, ok := mt.(memtableAdapter); ok {
			memtables = append(memtables, adapter.memtable)
		}
	}

	manifest := e.manifest
	dir := e.dir
	// REQ001244: check if all SST files have RowCount ≤ SmallTableRows
	smallTableRows := e.opts.SmallTableRows
	mmapCache := e.mmapCache
	e.mu.RUnlock()
	return newMergeIterator(memtables, manifest, dir, e.fs, prefix, e.blockCache, smallTableRows, mmapCache)
}

// Close releases engine resources. Calling Close twice is a no-op.
func (eng *Engine) Close() error {
	if eng == nil || eng.e == nil {
		return nil
	}
	_ = eng.e.Sync()
	err := eng.e.Close()
	eng.e = nil
	return err
}

// Sync flushes the active memtable and blocks until complete.
func (eng *Engine) Sync() error {
	if eng == nil || eng.e == nil {
		return ErrClosed
	}
	return eng.e.Sync()
}

// Stats returns a snapshot of the engine's read-path counters.
func (eng *Engine) Stats() ReadStats {
	if eng == nil || eng.e == nil {
		return ReadStats{}
	}
	return eng.e.Stats()
}

// Compaction returns the underlying compaction manager.
func (eng *Engine) Compaction() *compactionManager {
	if eng == nil || eng.e == nil {
		return nil
	}
	return eng.e.cm
}

// Flush returns the underlying flush manager.
func (eng *Engine) Flush() *flushManager {
	if eng == nil || eng.e == nil {
		return nil
	}
	return eng.e.fm
}

// ManualCompact triggers a full compaction (REQ000257).
func (eng *Engine) ManualCompact() error {
	if eng == nil || eng.e == nil {
		return ErrClosed
	}
	return eng.e.cm.ManualCompact()
}

// RangeIter is an iterator over a sorted range of keys.
type RangeIter interface {
	Next() bool
	Key() []byte
	Value() []byte
	Err() error
	Close() error
}

type memtableIter struct {
	it *Iterator
}

func (m *memtableIter) Next() bool    { return m.it.Next() }
func (m *memtableIter) Key() []byte   { return m.it.Key() }
func (m *memtableIter) Value() []byte { return m.it.Value() }
func (m *memtableIter) Err() error    { return nil }
func (m *memtableIter) Close() error  { return nil }

type sstIter struct {
	it   *sstIterator
	data []byte // holds SST data alive while iterator is active
}

func (s *sstIter) Next() bool    { return s.it.Next() }
func (s *sstIter) Key() []byte   { return s.it.Key() }
func (s *sstIter) Value() []byte { return s.it.Value() }
func (s *sstIter) Err() error    { return s.it.Err() }
func (s *sstIter) Close() error  { return s.it.Close() }

type iterHeapItem struct {
	key   []byte // borrowed from mergeIterator.sourceKeys[src] (REQ001258)
	value []byte // borrowed from mergeIterator.sourceVals[src] (REQ001258)
	src   int
}

type iterHeap []iterHeapItem

// iterHeap is kept for the legacy TestIterHeapPushPop test which
// uses container/heap. Production code uses mergeHeap (see below).
// REQ001261.

func (h iterHeap) Len() int { return len(h) }
func (h iterHeap) Less(i, j int) bool {
	return bytes.Compare(h[i].key, h[j].key) < 0
}
func (h *iterHeap) Swap(i, j int) { (*h)[i], (*h)[j] = (*h)[j], (*h)[i] }
func (h *iterHeap) Push(x any)    { *h = append(*h, x.(iterHeapItem)) }
func (h *iterHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// mergeHeap is a manual min-heap of iterHeapItem that avoids
// the `any`-boxing imposed by container/heap. The backing array
// is pre-allocated to a known capacity (typically len(sources))
// and grown geometrically when exceeded. REQ001261.
type mergeHeap struct {
	items []iterHeapItem
	n     int // number of valid items in items[0:n]
}

// init prepares the heap for a fresh session with a backing
// array of at least capHint items. Existing capacity is reused.
// REQ001261.
func (h *mergeHeap) init(capHint int) {
	if cap(h.items) < capHint {
		h.items = make([]iterHeapItem, 0, capHint)
	} else {
		h.items = h.items[:0]
	}
	h.n = 0
}

// Len returns the number of items in the heap.
func (h *mergeHeap) Len() int { return h.n }

// push adds an item to the heap, growing the backing array if
// necessary. REQ001261.
func (h *mergeHeap) push(item iterHeapItem) {
	if h.n >= cap(h.items) {
		// Grow geometrically. First grow goes to capHint if
		// the slice was empty, otherwise double.
		newCap := cap(h.items) * 2
		if newCap == 0 {
			newCap = 4
		}
		newItems := make([]iterHeapItem, h.n, newCap)
		copy(newItems, h.items[:h.n])
		h.items = newItems
	}
	// Use direct array assignment for the common case (no grow).
	if h.n < len(h.items) {
		h.items[h.n] = item
	} else {
		h.items = append(h.items, item)
	}
	h.n++
	h.siftUp(h.n - 1)
}

// pop removes and returns the minimum item. The caller is
// responsible for ensuring Len() > 0. REQ001261.
func (h *mergeHeap) pop() iterHeapItem {
	item := h.items[0]
	h.n--
	if h.n > 0 {
		h.items[0] = h.items[h.n]
		h.siftDown(0)
	}
	// Note: we do NOT zero items[h.n] — the slot will be
	// overwritten on the next push. This is safe because the
	// next push writes a new iterHeapItem.
	return item
}

// siftUp restores the heap property by moving items[i] up while
// it is smaller than its parent. REQ001261.
func (h *mergeHeap) siftUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(i, parent) {
			return
		}
		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
}

// siftDown restores the heap property by moving items[i] down
// while it is larger than either child. n is the heap size
// (excludes the slot being sifted). REQ001261.
func (h *mergeHeap) siftDown(i int) {
	for {
		left := 2*i + 1
		if left >= h.n {
			return
		}
		smallest := left
		right := left + 1
		if right < h.n && h.less(right, left) {
			smallest = right
		}
		if !h.less(smallest, i) {
			return
		}
		h.items[i], h.items[smallest] = h.items[smallest], h.items[i]
		i = smallest
	}
}

func (h *mergeHeap) less(i, j int) bool {
	return bytes.Compare(h.items[i].key, h.items[j].key) < 0
}

// mergeIterator merges all sources filtered by prefix (REQ000598).
// It uses owned copies in the heap to avoid iterator invalidation.
// Zero-copy optimization for SST blocks is achieved via borrowed pointers
// in sst_reader.go (decodeBlock returns pointers into the SST data).
//
// REQ001257: struct is reused via sync.Pool (mergeIteratorPool).
// REQ001258: key/value byte slices for heap entries are drawn from
// a pre-allocated per-source ring buffer (sourceKeys/sourceVals).
type mergeIterator struct {
	fs         FS
	manifest   *manifest
	dir        string
	prefix     []byte
	sources    []RangeIter
	h          mergeHeap // REQ001261 — manual min-heap, no any-boxing
	curKey     []byte // owned copy (nil if none)
	curVal     []byte // owned copy (nil if none)
	err        error
	closed     atomic.Bool
	blockCache *BlockCache // REQ001242

	// mmapCache maps SST file paths to their mmap'd data.
	// When non-nil and a path entry exists, init() constructs
	// the SST reader from the mmap slice (zero-copy) instead
	// of calling fs.ReadFile (which copies the entire file).
	mmapCache map[string][]byte

	// REQ001258: per-source ring buffer slots. sourceKeys[i] and
	// sourceVals[i] hold the current key/value for the i-th
	// source — the entry currently in the heap for that source.
	// On each Push for source i, the slot is overwritten with
	// the new key/value, avoiding per-push byte-slice allocation.
	sourceKeys [][]byte
	sourceVals [][]byte
	// curKey/curVal are dedicated owned-copy slices for the
	// winner row exposed via Key()/Value(). The copy is made
	// exactly once per Next() call that returns true, so the
	// user can read curKey/curVal safely until the next Next().
}

func newMergeIterator(memtables []*memtable, manifest *manifest, dir string, fs FS, prefix []byte, blockCache *BlockCache, smallTableRows int64, mmapCache map[string][]byte) *mergeIterator {
	mi := acquireMergeIterator()
	mi.fs = fs
	mi.manifest = manifest
	mi.dir = dir
	mi.prefix = append(mi.prefix[:0], prefix...)
	mi.blockCache = blockCache
	mi.mmapCache = mmapCache
	skipSST := smallTableRows > 0 && manifestAllSmall(manifest, smallTableRows)
	mi.init(memtables, skipSST)
	return mi
}

func (mi *mergeIterator) init(memtables []*memtable, skipSST bool) {
	activeMem := memtables[len(memtables)-1]

	// REQ001257: pull sub-iterators from the pool.
	mti := acquireMemtableIter()
	mti.it = activeMem.Iterator()
	mi.sources = append(mi.sources, mti)
	for i := len(memtables) - 2; i >= 0; i-- {
		mt := memtables[i]
		mti2 := acquireMemtableIter()
		mti2.it = mt.Iterator()
		mi.sources = append(mi.sources, mti2)
	}
	v := mi.manifest.Current()
	if v != nil && !skipSST {
		upper := prefixUpperBound(mi.prefix)
		for _, level := range v.levels {
			for _, f := range level {
				if !fileOverlapsPrefix(f.MinKey, f.MaxKey, mi.prefix, upper) {
					continue
				}
			sstPath := filepath.Join(mi.dir, fileName(&f))
			var reader *sstReader
			var sstData []byte
			if mmapData, ok := mi.mmapCache[sstPath]; ok {
				var err error
				reader, err = openSSTWithPath(mmapData, sstPath)
				if err != nil {
					continue
				}
				reader.mmap = mmapData
				sstData = mmapData
			} else {
				var err error
				sstData, err = mi.fs.ReadFile(sstPath)
				if err != nil {
					continue
				}
				reader, err = openSST(sstData)
				if err != nil {
					continue
				}
			}
			reader.blockCache = mi.blockCache // REQ001242
			// REQ001257: pull sstIter from the pool.
			si := acquireSSTIter()
			si.it = reader.Iterator()
			si.data = sstData
			mi.sources = append(mi.sources, si)
			}
		}
	}
	// REQ001258: pre-allocate per-source key/value slots.
	mi.initSourceSlots(len(mi.sources))
	// REQ001261: pre-allocate the manual min-heap. Typical
	// heap size is bounded by len(sources).
	mi.h.init(len(mi.sources))
	for i, src := range mi.sources {
		if src.Next() {
			// Copy the source's first key/value into its
			// pre-allocated ring slot. The iterHeapItem
			// references the slot — no allocation.
			mi.copySourceSlot(i, src)
			mi.h.push(iterHeapItem{
				key:   mi.sourceKeys[i],
				value: mi.sourceVals[i],
				src:   i,
			})
		}
	}
}

// initSourceSlots resets the per-source key/value ring buffer
// for a fresh mergeIterator session. The buffer is sized to n
// slots; on reuse, existing backing arrays are kept. REQ001258.
func (mi *mergeIterator) initSourceSlots(n int) {
	if n == 0 {
		mi.sourceKeys = nil
		mi.sourceVals = nil
		return
	}
	if cap(mi.sourceKeys) < n {
		mi.sourceKeys = make([][]byte, n)
		mi.sourceVals = make([][]byte, n)
	} else {
		mi.sourceKeys = mi.sourceKeys[:n]
		mi.sourceVals = mi.sourceVals[:n]
		for i := range mi.sourceKeys {
			mi.sourceKeys[i] = mi.sourceKeys[i][:0]
			mi.sourceVals[i] = mi.sourceVals[i][:0]
		}
	}
}

// copySourceSlot copies the source's current key/value into its
// pre-allocated slot. REQ001258.
func (mi *mergeIterator) copySourceSlot(srcIdx int, src RangeIter) {
	mi.sourceKeys[srcIdx] = append(mi.sourceKeys[srcIdx][:0], src.Key()...)
	mi.sourceVals[srcIdx] = append(mi.sourceVals[srcIdx][:0], src.Value()...)
}

// manifestAllSmall returns true if the manifest has SST files and every
// one of them has RowCount ≤ limit. Used by REQ001244 to skip SST reads
// for small tables whose data lives entirely in frozen memtables.
func manifestAllSmall(m *manifest, limit int64) bool {
	v := m.Current()
	if v == nil || len(v.levels) == 0 {
		return false
	}
	for _, level := range v.levels {
		for _, f := range level {
			if f.RowCount > limit {
				return false
			}
		}
	}
	return true
}

func fileOverlapsPrefix(minKey, maxKey, prefix, upper []byte) bool {
	if len(upper) == 0 {
		return bytes.Compare(maxKey, prefix) >= 0
	}
	if bytes.Compare(maxKey, prefix) < 0 {
		return false
	}
	if len(minKey) > 0 && bytes.Compare(minKey, upper) >= 0 {
		return false
	}
	return true
}

func prefixUpperBound(prefix []byte) []byte {
	if len(prefix) == 0 {
		return nil
	}
	out := append([]byte(nil), prefix...)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] < 0xFF {
			out[i]++
			return out[:i+1]
		}
	}
	return nil
}

// Next advances the iterator. Tombstoned keys are skipped.
func (mi *mergeIterator) Next() bool {
	if mi.closed.Load() {
		return false
	}
	// Clear the previous winner's reference. The underlying
	// curKey/curVal slices are reused on the next copy.
	mi.curKey = nil
	mi.curVal = nil

	for mi.h.Len() > 0 {
		top := mi.h.pop() // REQ001261 — manual heap pop, no any-boxing
		// Note: top.key/top.value reference mi.sourceKeys[top.src]
		// — a slice whose backing array will be reused by the
		// next copySourceSlot call below. We must NOT use them
		// after the next iteration of this loop. REQ001258.
		srcIdx := top.src
		src := mi.sources[srcIdx]

		// Deduplicate: remove all other sources with the same key.
		// We must check dedup BEFORE advancing, because the
		// dedup comparison uses the popped item's key, which is
		// invalidated by the next copySourceSlot call. REQ001261:
		// the heap top is mi.h.items[0]; peek before pop.
		for mi.h.Len() > 0 && bytes.Equal(top.key, mi.h.items[0].key) {
			dup := mi.h.pop()
			dupSrc := mi.sources[dup.src]
			if dupSrc.Next() {
				mi.copySourceSlot(dup.src, dupSrc)
				mi.h.push(iterHeapItem{
					key:   mi.sourceKeys[dup.src],
					value: mi.sourceVals[dup.src],
					src:   dup.src,
				})
			}
		}

		// Check prefix and tombstone — must be done before
		// advancing the source.
		if !bytes.HasPrefix(top.key, mi.prefix) {
			// Skip: advance the source and re-loop.
			if src.Next() {
				mi.copySourceSlot(srcIdx, src)
				mi.h.push(iterHeapItem{
					key:   mi.sourceKeys[srcIdx],
					value: mi.sourceVals[srcIdx],
					src:   srcIdx,
				})
			}
			continue
		}
		if isTombstone(top.value) {
			if src.Next() {
				mi.copySourceSlot(srcIdx, src)
				mi.h.push(iterHeapItem{
					key:   mi.sourceKeys[srcIdx],
					value: mi.sourceVals[srcIdx],
					src:   srcIdx,
				})
			}
			continue
		}

		// Winner. Copy the popped item's key/value into the
		// dedicated curKey/curVal slices BEFORE advancing the
		// source. REQ001258 — one copy per Next call.
		mi.curKey = append(mi.curKey[:0], top.key...)
		mi.curVal = append(mi.curVal[:0], top.value...)

		// Now safe to advance the source for the next round.
		if src.Next() {
			mi.copySourceSlot(srcIdx, src)
			mi.h.push(iterHeapItem{
				key:   mi.sourceKeys[srcIdx],
				value: mi.sourceVals[srcIdx],
				src:   srcIdx,
			})
		}
		return true
	}
	return false
}

func (mi *mergeIterator) Key() []byte {
	if mi.curKey == nil {
		return nil
	}
	// REQ000876: mi.curKey is already an owned copy from the heap pop
	// in Next(). Return it directly — no extra copy needed.
	return mi.curKey
}

func (mi *mergeIterator) Value() []byte {
	if mi.curVal == nil {
		return nil
	}
	// REQ000876: mi.curVal is already an owned copy from the heap pop.
	return mi.curVal
}

func (mi *mergeIterator) Err() error { return mi.err }

func (mi *mergeIterator) Close() error {
	if mi.closed.Load() {
		return nil
	}
	mi.closed.Store(true)

	// Clear heap (REQ001261 — manual min-heap, no any-boxing).
	for mi.h.Len() > 0 {
		_ = mi.h.pop()
	}

	// Close all sources and return sub-iterators to the pool.
	// REQ001257.
	for _, src := range mi.sources {
		src.Close()
		switch s := src.(type) {
		case *memtableIter:
			releaseMemtableIter(s)
		case *sstIter:
			releaseSSTIter(s)
		}
	}

	// REQ001257: return the mergeIterator itself to the pool.
	releaseMergeIterator(mi)
	return nil
}

func (mi *mergeIterator) String() string {
	return fmt.Sprintf("mergeIterator(prefix=%x sources=%d)", mi.prefix, len(mi.sources))
}
