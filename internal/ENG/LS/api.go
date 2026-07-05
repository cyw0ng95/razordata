package ls

import (
	"bytes"
	"container/heap"
	"fmt"
	"path/filepath"
	"sync/atomic"
)

var tombstoneValue = []byte{0xDE, 0xAD, 0xBE, 0xEF}

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
	e.mu.RUnlock()
	return newMergeIterator(memtables, manifest, dir, e.fs, prefix, e.blockCache, smallTableRows)
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
	key   []byte // owned copy (required because iterator values are invalidated on Next())
	value []byte // owned copy
	src   int
}

type iterHeap []iterHeapItem

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

// mergeIterator merges all sources filtered by prefix (REQ000598).
// It uses owned copies in the heap to avoid iterator invalidation.
// Zero-copy optimization for SST blocks is achieved via borrowed pointers
// in sst_reader.go (decodeBlock returns pointers into the SST data).
type mergeIterator struct {
	fs         FS
	manifest   *manifest
	dir        string
	prefix     []byte
	sources    []RangeIter
	h          iterHeap
	curKey     []byte // owned copy (nil if none)
	curVal     []byte // owned copy (nil if none)
	err        error
	closed     atomic.Bool
	blockCache *BlockCache // REQ001242
}

func newMergeIterator(memtables []*memtable, manifest *manifest, dir string, fs FS, prefix []byte, blockCache *BlockCache, smallTableRows int64) *mergeIterator {
	mi := &mergeIterator{
		fs:         fs,
		manifest:   manifest,
		dir:        dir,
		prefix:     append([]byte(nil), prefix...),
		blockCache: blockCache,
	}
	skipSST := smallTableRows > 0 && manifestAllSmall(manifest, smallTableRows)
	mi.init(memtables, skipSST)
	return mi
}

func (mi *mergeIterator) init(memtables []*memtable, skipSST bool) {
	activeMem := memtables[len(memtables)-1]

	mi.sources = append(mi.sources, &memtableIter{it: activeMem.Iterator()})
	for i := len(memtables) - 2; i >= 0; i-- {
		mt := memtables[i]
		mi.sources = append(mi.sources, &memtableIter{it: mt.Iterator()})
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
				data, err := mi.fs.ReadFile(sstPath)
				if err != nil {
					continue
				}
				reader, err := openSST(data)
				if err != nil {
					continue
				}
				reader.blockCache = mi.blockCache // REQ001242
				// Store the data in the source so it stays alive
				// The sstIter holds a reference to the reader which holds the data
				mi.sources = append(mi.sources, &sstIter{it: reader.Iterator(), data: data})
			}
		}
	}
	for i, src := range mi.sources {
		if src.Next() {
			// Push owned copies to heap (required because iterator values are invalidated on Next())
			heap.Push(&mi.h, iterHeapItem{
				key:   append([]byte(nil), src.Key()...),
				value: append([]byte(nil), src.Value()...),
				src:   i,
			})
		}
	}
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
	// Clear previous key/value (they were owned copies, no need to free)
	mi.curKey = nil
	mi.curVal = nil

	for mi.h.Len() > 0 {
		top := mi.h[0]
		heap.Pop(&mi.h)
		srcIdx := top.src
		src := mi.sources[srcIdx]

		// Advance the source and push next item if available
		if src.Next() {
			heap.Push(&mi.h, iterHeapItem{
				key:   append([]byte(nil), src.Key()...),
				value: append([]byte(nil), src.Value()...),
				src:   srcIdx,
			})
		}

		// Deduplicate: remove all other sources with the same key
		for mi.h.Len() > 0 && bytes.Equal(top.key, mi.h[0].key) {
			dup := heap.Pop(&mi.h).(iterHeapItem)
			// Advance the duplicate source
			dupSrc := mi.sources[dup.src]
			if dupSrc.Next() {
				heap.Push(&mi.h, iterHeapItem{
					key:   append([]byte(nil), dupSrc.Key()...),
					value: append([]byte(nil), dupSrc.Value()...),
					src:   dup.src,
				})
			}
		}

		// Check prefix and tombstone
		if !bytes.HasPrefix(top.key, mi.prefix) {
			continue
		}
		if isTombstone(top.value) {
			continue
		}

		// Keep the owned copies for this row
		mi.curKey = top.key
		mi.curVal = top.value
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

	// Clear heap (owned copies, no need to free)
	for mi.h.Len() > 0 {
		heap.Pop(&mi.h)
	}

	// Close all sources
	for _, src := range mi.sources {
		src.Close()
	}

	return nil
}

func (mi *mergeIterator) String() string {
	return fmt.Sprintf("mergeIterator(prefix=%x sources=%d)", mi.prefix, len(mi.sources))
}
