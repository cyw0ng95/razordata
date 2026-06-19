package ls

import (
	"bytes"
	"container/heap"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrClosed = errors.New("eng: engine closed")

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

func (eng *Engine) Stats() ReadStats {
	if eng == nil || eng.e == nil {
		return ReadStats{}
	}
	return eng.e.GetStats()
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
		return errors.New("engine: closed")
	}
	return eng.e.cm.ManualCompact()
}

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
	it *sstIterator
}

func (s *sstIter) Next() bool    { return s.it.Next() }
func (s *sstIter) Key() []byte   { return s.it.Key() }
func (s *sstIter) Value() []byte { return s.it.Value() }
func (s *sstIter) Err() error    { return s.it.Err() }
func (s *sstIter) Close() error  { return s.it.Close() }

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

// mergeIterator merges all sources filtered by prefix (REQ000598).
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

	mi.sources = append(mi.sources, &memtableIter{it: activeMem.Iterator()})
	for i := len(memtables) - 2; i >= 0; i-- {
		mt := memtables[i]
		mi.sources = append(mi.sources, &memtableIter{it: mt.Iterator()})
	}
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
	if mi.closed {
		return false
	}
	for mi.h.Len() > 0 {
		top := &mi.h[0]
		key := top.key
		val := top.value
		srcIdx := top.src
		heap.Pop(&mi.h)
		src := mi.sources[srcIdx]
		if src.Next() {
			heap.Push(&mi.h, iterHeapItem{
				key:   append([]byte(nil), src.Key()...),
				value: append([]byte(nil), src.Value()...),
				src:   srcIdx,
			})
		}
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
		if !bytes.HasPrefix(key, mi.prefix) {
			continue
		}
		if isTombstone(val) {
			continue
		}
		mi.curKey = key
		mi.curVal = val
		return true
	}
	return false
}

func (mi *mergeIterator) Key() []byte   { return mi.curKey }
func (mi *mergeIterator) Value() []byte { return mi.curVal }
func (mi *mergeIterator) Err() error    { return mi.err }

// Close releases iterator resources.
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

func (mi *mergeIterator) String() string {
	return fmt.Sprintf("mergeIterator(prefix=%x sources=%d)", mi.prefix, len(mi.sources))
}
