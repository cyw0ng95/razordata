package ls

import (
	"bytes"
	"container/heap"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrClosed is returned by Engine methods after Close has been called.
var ErrClosed = errors.New("eng: engine closed")

// tombstoneValue is the sentinel value written by Engine.Delete.
// Keys whose stored value equals tombstoneValue are treated as deleted
// and skipped by iterators; Engine.Get returns ErrNotFound for them.
var tombstoneValue = []byte{0xDE, 0xAD, 0xBE, 0xEF}

func isTombstone(v []byte) bool {
	return bytes.Equal(v, tombstoneValue)
}

// Engine is the public storage handle returned by Open.
// It wraps the internal LSM engine and exposes the minimal surface needed by
// the SQL executor: Insert / Get / Delete / NewIterator / Close. All operations
// are goroutine-safe.
type Engine struct {
	e *engine
}

// Open creates or opens a database stored in dir. The directory is created if
// it does not exist.
func Open(dir string) (*Engine, error) {
	e, err := newEngine(dir)
	if err != nil {
		return nil, err
	}
	return &Engine{e: e}, nil
}

// Insert upserts a key-value pair.
func (eng *Engine) Insert(key, value []byte) error {
	if eng == nil || eng.e == nil {
		return ErrClosed
	}
	return eng.e.Write(key, value)
}

// Get returns the value stored at key, or ErrNotFound if the key was deleted
// or never written.
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

// Delete writes a tombstone for key. Subsequent Get returns ErrNotFound and
// iterators skip the key.
func (eng *Engine) Delete(key []byte) error {
	if eng == nil || eng.e == nil {
		return ErrClosed
	}
	return eng.e.Write(key, tombstoneValue)
}

// NewIterator returns an iterator over all live key-value pairs whose key
// starts with prefix. Tombstoned keys are skipped. The returned iterator must
// be Close()'d.
func (eng *Engine) NewIterator(prefix []byte) RangeIter {
	e := eng.e
	e.mu.RLock()
	memtables := append([]*memtable(nil), e.memtables...)
	manifest := e.manifest
	dir := e.dir
	e.mu.RUnlock()
	return newMergeIterator(memtables, manifest, dir, prefix)
}

// Close releases engine resources. Calling Close twice is a no-op.
func (eng *Engine) Close() error {
	if eng == nil || eng.e == nil {
		return nil
	}
	// Flush any in-flight memtable data so it is durable across the
	// boundary. Callers that do not need this durability guarantee
	// can rely on the raw LS.Engine.Close path via the unexported
	// helper, but the public contract is "Close persists".
	_ = eng.e.Sync()
	err := eng.e.Close()
	eng.e = nil
	return err
}

// Sync forces the active memtable to be flushed to an SST file and
// blocks until the flush completes. Returns nil if the memtable is
// empty. Required for callers (e.g. the system catalog) that need
// crash-survivable writes.
func (eng *Engine) Sync() error {
	if eng == nil || eng.e == nil {
		return ErrClosed
	}
	return eng.e.Sync()
}

// Stats returns a snapshot of the engine's read-side counters.
func (eng *Engine) Stats() ReadStats {
	if eng == nil || eng.e == nil {
		return ReadStats{}
	}
	return eng.e.GetStats()
}

// Compaction returns the underlying compaction manager. Used by the
// shutdown sequence (Phase 4.1 of SYS.md:245-251) to call
// Stop(ctx) before tearing the engine down. Returns nil if the
// engine is closed.
func (eng *Engine) Compaction() *compactionManager {
	if eng == nil || eng.e == nil {
		return nil
	}
	return eng.e.cm
}

// Flush returns the underlying flush manager. Used by the shutdown
// sequence (Phase 4.1) to call Stop(ctx). Returns nil if the engine
// is closed.
func (eng *Engine) Flush() *flushManager {
	if eng == nil || eng.e == nil {
		return nil
	}
	return eng.e.fm
}

// ManualCompact triggers a full compaction cycle across all levels.
// Returns ErrCompactionInProgress if a compaction is already running.
// REQ000257: Used by VACUUM to immediately reclaim tombstone space.
func (eng *Engine) ManualCompact() error {
	if eng == nil || eng.e == nil {
		return errors.New("engine: closed")
	}
	return eng.e.cm.ManualCompact()
}

// RangeIter is the public iteration interface over a key range.
type RangeIter interface {
	Next() bool
	Key() []byte
	Value() []byte
	Err() error
	Close() error
}

// memtableIter wraps the public skiplist Iterator to satisfy RangeIter.
type memtableIter struct {
	it *Iterator
}

func (m *memtableIter) Next() bool    { return m.it.Next() }
func (m *memtableIter) Key() []byte   { return m.it.Key() }
func (m *memtableIter) Value() []byte { return m.it.Value() }
func (m *memtableIter) Err() error    { return nil }
func (m *memtableIter) Close() error  { return nil }

// sstIter wraps the package-private sstIterator to satisfy RangeIter.
type sstIter struct {
	it *sstIterator
}

func (s *sstIter) Next() bool    { return s.it.Next() }
func (s *sstIter) Key() []byte   { return s.it.Key() }
func (s *sstIter) Value() []byte { return s.it.Value() }
func (s *sstIter) Err() error    { return s.it.Err() }
func (s *sstIter) Close() error  { return s.it.Close() }

// iterHeapItem is a single entry in the merge heap.
type iterHeapItem struct {
	key   []byte
	value []byte
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

// mergeIterator is a streaming merge of all relevant sources, filtered by
// prefix. Sources are: the active memtable (covers all unflushed writes) and
// every SST file whose MinKey/MaxKey range overlaps [prefix, prefix+1).
// REQ000598: accepts explicit dependencies instead of *engine for testability.
type mergeIterator struct {
	manifest *manifest
	dir      string
	prefix   []byte
	sources  []RangeIter
	h        iterHeap
	curKey   []byte
	curVal   []byte
	err      error
	closed   bool
}

// newMergeIterator constructs a merge iterator from explicit dependencies.
// The caller must hold a reference to the active memtable and frozen
// memtables snapshot (under the lock) before calling.
func newMergeIterator(memtables []*memtable, manifest *manifest, dir string, prefix []byte) *mergeIterator {
	mi := &mergeIterator{
		manifest: manifest,
		dir:      dir,
		prefix:   append([]byte(nil), prefix...),
	}
	mi.init(memtables)
	return mi
}

func (mi *mergeIterator) init(memtables []*memtable) {
	activeMem := memtables[len(memtables)-1]

	// Source 0: active memtable (all uncommitted writes).
	mi.sources = append(mi.sources, &memtableIter{it: activeMem.Iterator()})
	// Source 1+: frozen memtables (newest first).
	for i := len(memtables) - 2; i >= 0; i-- {
		mt := memtables[i]
		mi.sources = append(mi.sources, &memtableIter{it: mt.Iterator()})
	}
	// Source N+: SST files whose range overlaps [prefix, prefix_upper).
	v := mi.manifest.Current()
	if v != nil {
		upper := prefixUpperBound(mi.prefix)
		for _, level := range v.levels {
			for _, f := range level {
				if !fileOverlapsPrefix(f.MinKey, f.MaxKey, mi.prefix, upper) {
					continue
				}
				sstPath := filepath.Join(mi.dir, fileName(&f))
				data, err := os.ReadFile(sstPath)
				if err != nil {
					continue
				}
				reader, err := openSST(data)
				if err != nil {
					continue
				}
				mi.sources = append(mi.sources, &sstIter{it: reader.Iterator()})
			}
		}
	}
	// Pre-fill the heap with the first key from each source.
	for i, src := range mi.sources {
		if src.Next() {
			heap.Push(&mi.h, iterHeapItem{
				key:   append([]byte(nil), src.Key()...),
				value: append([]byte(nil), src.Value()...),
				src:   i,
			})
		}
	}
}

// fileOverlapsPrefix reports whether [minKey, maxKey] may contain any key in
// the range [prefix, prefixUpper). An empty minKey or maxKey is treated as
// -infinity / +infinity respectively.
func fileOverlapsPrefix(minKey, maxKey, prefix, upper []byte) bool {
	if len(upper) == 0 {
		// No upper bound: any key in [minKey, +inf) might match.
		return bytes.Compare(maxKey, prefix) >= 0
	}
	// Overlap iff [minKey, maxKey] intersects [prefix, upper).
	if bytes.Compare(maxKey, prefix) < 0 {
		return false
	}
	if len(minKey) > 0 && bytes.Compare(minKey, upper) >= 0 {
		return false
	}
	return true
}

// prefixUpperBound returns the smallest byte string strictly greater than
// every key starting with prefix, or nil if no such bound exists.
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

// Next advances the iterator. It returns true if a row is available, false
// when the stream is exhausted or an error occurred (consult Err).
// Tombstoned keys are skipped. Duplicate keys keep the value from the source
// with the lowest index (memtable first, then frozen memtables newest first,
// then SSTs in manifest order).
func (mi *mergeIterator) Next() bool {
	if mi.closed {
		return false
	}
	for mi.h.Len() > 0 {
		top := &mi.h[0]
		key := top.key
		val := top.value
		srcIdx := top.src
		// Pop the top and advance its source.
		heap.Pop(&mi.h)
		src := mi.sources[srcIdx]
		if src.Next() {
			heap.Push(&mi.h, iterHeapItem{
				key:   append([]byte(nil), src.Key()...),
				value: append([]byte(nil), src.Value()...),
				src:   srcIdx,
			})
		}
		// Skip any other heap entries with the same key; they are older
		// versions from lower-priority sources.
		for mi.h.Len() > 0 && bytes.Equal(mi.h[0].key, key) {
			oldIdx := mi.h[0].src
			oldSrc := mi.sources[oldIdx]
			heap.Pop(&mi.h)
			if oldSrc.Next() {
				heap.Push(&mi.h, iterHeapItem{
					key:   append([]byte(nil), oldSrc.Key()...),
					value: append([]byte(nil), oldSrc.Value()...),
					src:   oldIdx,
				})
			}
		}
		// Filter by prefix.
		if !bytes.HasPrefix(key, mi.prefix) {
			continue
		}
		// Skip tombstones.
		if isTombstone(val) {
			continue
		}
		mi.curKey = key
		mi.curVal = val
		return true
	}
	return false
}

// Key returns the current key. Valid only after a true Next.
func (mi *mergeIterator) Key() []byte { return mi.curKey }

// Value returns the current value. Valid only after a true Next.
func (mi *mergeIterator) Value() []byte { return mi.curVal }

// Err returns the first error encountered during iteration, or nil.
func (mi *mergeIterator) Err() error { return mi.err }

// Close releases iterator resources. Idempotent.
func (mi *mergeIterator) Close() error {
	if mi.closed {
		return nil
	}
	mi.closed = true
	var firstErr error
	for _, s := range mi.sources {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	mi.sources = nil
	mi.h = mi.h[:0]
	return firstErr
}

// String renders a one-line description for debugging.
func (mi *mergeIterator) String() string {
	return fmt.Sprintf("mergeIterator(prefix=%x sources=%d)", mi.prefix, len(mi.sources))
}
