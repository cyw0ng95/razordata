package ls

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrNoActiveMemtable = errors.New("no active memtable")
)

type ReadStats struct {
	MemtableHits int
	SSTHits      int
	DiskReads    int
}

type engine struct {
	dir       string
	memtables []*memtable
	activeMem *memtable
	manifest  *manifest
	cm        *compactionManager
	fm        *flushManager
	stats     ReadStats
	statsMu   sync.RWMutex
	// REQ000574: protects the memtables slice and activeMem
	// pointer. Read takes RLock; Write takes no lock (it only
	// touches activeMem, which is set once at construction and
	// atomically swapped during flush under writeLock);
	// flushActiveMemtable takes the write lock for the
	// mutation. Without this guard a concurrent Read iterating
	// e.memtables while flushActiveMemtable slice-erases can
	// observe a half-applied state.
	mu sync.RWMutex
}

func newEngine(dir string) (*engine, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	sstDir := filepath.Join(dir, "sst")
	if err := os.MkdirAll(sstDir, 0755); err != nil {
		return nil, err
	}

	manifest, err := newManifest(dir)
	if err != nil {
		return nil, err
	}

	activeMem := newMemtable(64 * 1024 * 1024)

	e := &engine{
		dir:       dir,
		memtables: []*memtable{activeMem},
		activeMem: activeMem,
		manifest:  manifest,
	}

	e.cm = newCompactionManager(dir, manifest)
	e.fm = newFlushManager(dir, 64*1024*1024, manifest)

	return e, nil
}

func (e *engine) Write(key, value []byte) error {
	if e.activeMem == nil {
		return ErrNoActiveMemtable
	}

	if err := e.activeMem.Insert(key, value); err != nil {
		return err
	}

	if e.activeMem.Size() >= e.activeMem.maxSize {
		return e.flushActiveMemtable()
	}

	return nil
}

// flushActiveMemtable freezes the active memtable, enqueues
// it for flush, and installs a fresh memtable as the new
// active. The order of operations matters:
//
//  1. Capture the current active into `frozen` BEFORE
//     mutating any state. Step 5's `requestFlush` and step 7's
//     slice erase both refer to this pointer.
//  2. Freeze `frozen` (idempotent: `requestFlush` also calls
//     Freeze, but capturing the freeze here makes the data
//     flow obvious).
//  3. Append the new active memtable.
//  4. Swap the active pointer.
//  5. Enqueue `frozen` for flush.
//  6. Remove `frozen` from the slice at its known index
//     (`len-2` is the position of the freshly frozen one
//     after the append; do not pick `e.memtables[0]`, which
//     is a different, already-flushed memtable).
//
// REQ000347 (iter-26): the previous implementation selected
// `e.memtables[0]` for flush, which flushed an unrelated
// stale memtable. The freshly frozen memtable was then
// removed by `e.memtables[1:]`, never reaching the flush
// queue. The result was silent data loss for any row whose
// INSERT crossed a memtable boundary.
func (e *engine) flushActiveMemtable() error {
	// REQ000574: take the write lock for the mutation of
	// memtables/activeMem. Concurrent Read calls hold RLock.
	e.mu.Lock()
	defer e.mu.Unlock()

	frozen := e.activeMem
	frozen.Freeze()

	newMem := newMemtable(64 * 1024 * 1024)
	e.memtables = append(e.memtables, newMem)
	e.activeMem = newMem

	e.fm.requestFlush(frozen)

	// Remove the freshly frozen memtable from the reader-visible
	// list. After the append above, `frozen` is at
	// `len(e.memtables) - 2`. We do an in-place erase to keep
	// the backing array compact for the next flush.
	idx := len(e.memtables) - 2
	e.memtables = append(e.memtables[:idx], e.memtables[idx+1:]...)

	return nil
}

// Sync flushes the active memtable to SST and blocks until the flush
// job completes. Idempotent. Returns ErrNoActiveMemtable after Close.
func (e *engine) Sync() error {
	if e.activeMem == nil {
		return ErrNoActiveMemtable
	}
	if e.activeMem.Size() == 0 {
		return nil
	}
	if err := e.flushActiveMemtable(); err != nil {
		return err
	}
	e.fm.WaitForFlush()
	if p := e.fm.lastErr.Load(); p != nil {
		return *p
	}
	return nil
}

func (e *engine) Read(key []byte) ([]byte, error) {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()

	// REQ000574: hold the read lock while iterating e.memtables
	// so concurrent flushActiveMemtable cannot mutate the slice
	// out from under us.
	e.mu.RLock()
	defer e.mu.RUnlock()

	for i := len(e.memtables) - 1; i >= 0; i-- {
		mt := e.memtables[i]
		if mt.IsFrozen() {
			continue
		}
		if val, found := mt.Get(key); found {
			e.stats.MemtableHits++
			return val, nil
		}
	}

	for _, mt := range e.memtables {
		if !mt.IsFrozen() {
			continue
		}
		if val, found := mt.Get(key); found {
			e.stats.MemtableHits++
			return val, nil
		}
	}

	val, err := e.readFromSST(key)
	if err == nil {
		e.stats.SSTHits++
		return val, nil
	}
	if errors.Is(err, ErrKeyNotFound) {
		return nil, ErrKeyNotFound
	}
	return nil, err
}

func (e *engine) readFromSST(key []byte) ([]byte, error) {
	v := e.manifest.Current()

	for level := 0; level < len(v.levels); level++ {
		files := v.levels[level]

		for i := len(files) - 1; i >= 0; i-- {
			file := files[i]

			if bytes.Compare(key, file.MinKey) < 0 || bytes.Compare(key, file.MaxKey) > 0 {
				continue
			}

			e.stats.DiskReads++

			sstPath := filepath.Join(e.dir, fileName(&file))
			data, err := os.ReadFile(sstPath)
			if err != nil {
				continue
			}

			reader, err := openSST(data)
			if err != nil {
				continue
			}

			if !reader.mayContain(key) {
				continue
			}

			// REQ000602: use the existing Find() method
			// (bloom filter + block index binary search + linear
			// block scan) instead of opening a full iterator and
			// walking every key from the start. Find is O(log
			// blocks + block scan) vs O(file size).
			if val, found := reader.Find(key); found {
				return val, nil
			}
		}
	}

	return nil, ErrKeyNotFound
}

func (e *engine) MayContain(key []byte) bool {
	v := e.manifest.Current()

	for _, mt := range e.memtables {
		if _, found := mt.Get(key); found {
			return true
		}
	}

	for level := 0; level < len(v.levels); level++ {
		files := v.levels[level]
		for i := range files {
			file := &files[i]
			sstPath := filepath.Join(e.dir, fileName(file))
			data, err := os.ReadFile(sstPath)
			if err != nil {
				continue
			}
			reader, err := openSST(data)
			if err != nil {
				continue
			}
			if reader.mayContain(key) {
				return true
			}
		}
	}

	return false
}

func (e *engine) GetStats() ReadStats {
	e.statsMu.RLock()
	defer e.statsMu.RUnlock()
	return e.stats
}

func (e *engine) Close() error {
	if e.cm == nil {
		return nil
	}
	if err := e.cm.Close(); err != nil {
		return err
	}
	if err := e.fm.Close(); err != nil {
		return err
	}
	e.cm = nil
	e.fm = nil
	if err := e.manifest.Close(); err != nil {
		return err
	}
	return nil
}
