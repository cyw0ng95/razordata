package ls

import (
	"log/slog"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

var (
	ErrNoActiveMemtable = errors.New("ls: no active memtable")
	ErrClosed           = errors.New("ls: engine is closed")
	ErrNotFound         = errors.New("ls: key not found")
)

// memtableAdapter wraps *memtable to satisfy memtableIface.
type memtableAdapter struct {
	*memtable
}

func (m memtableAdapter) Iterator() RangeIter {
	return &iteratorAdapter{it: m.memtable.Iterator()}
}

// memtableIface abstracts both single memtable and sharded memtable.
// This allows the engine to treat them uniformly for read/iteration.
type memtableIface interface {
	Get(key []byte) ([]byte, bool)
	Size() int64
	Len() int64
	ShouldFlush() bool
	Freeze()
	IsFrozen() bool
	IncRef()
	DecRef()
	RefCount() int64
	Iterator() RangeIter
}

// Options configures the LSM engine. REQ000537.
type Options struct {
	// MemTableShards is the number of shards for the active memtable.
	// Default is 1 (single-shard mode, equivalent to legacy behavior).
	// Use powers of 2 (e.g., 4, 8, 16) for efficient modulo via bit masking.
	MemTableShards int
	// MemTableSize is the total size threshold that triggers a flush.
	// Default is 64MB.
	MemTableSize int64
}

// DefaultOptions returns the default configuration.
func DefaultOptions() Options {
	return Options{
		MemTableShards: 1, // legacy single-shard mode by default
		MemTableSize:   64 * 1024 * 1024,
	}
}

type ReadStats struct {
	MemtableHits int
	SSTHits      int
	DiskReads    int
}

type engine struct {
	dir       string
	memtables []memtableIface // frozen memtables + active shardedMemtable
	activeMem *shardedMemtable
	manifest  *manifest
	cm        *compactionManager
	fm        *flushManager
	pageCache *PageCache // REQ000571: block-level SST cache
	opts      Options
	stats     ReadStats
	statsMu   sync.RWMutex
	mu        sync.RWMutex // REQ000574: guards memtables/activeMem
	closed    atomic.Bool
	log       *slog.Logger
}

func newEngine(dir string) (*engine, error) {
	return newEngineWithOptions(dir, DefaultOptions())
}

// newEngineWithOptions creates an engine with the given options. REQ000537.
func newEngineWithOptions(dir string, opts Options) (*engine, error) {
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

	// Create sharded memtable (or single-shard for legacy mode)
	activeMem := newShardedMemtable(opts.MemTableSize, opts.MemTableShards)

	e := &engine{
		dir:       dir,
		memtables: make([]memtableIface, 0),
		activeMem: activeMem,
		manifest:  manifest,
		pageCache: NewPageCache(DefaultPageCacheSize), // REQ000571
		opts:      opts,
		log:       slog.Default(),
	}

	e.cm = newCompactionManager(dir, manifest)
	e.fm = newFlushManager(dir, opts.MemTableSize, manifest)

	return e, nil
}

func (e *engine) Write(key, value []byte) error {
	if e.activeMem == nil {
		return ErrNoActiveMemtable
	}
	if e.closed.Load() {
		return ErrClosed
	}

	e.activeMem.Insert(key, value)

	if e.activeMem.ShouldFlush() {
		return e.flushActiveMemtable()
	}

	return nil
}

func (e *engine) flushActiveMemtable() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.activeMem == nil {
		return ErrNoActiveMemtable
	}

	frozen := e.activeMem
	frozen.Freeze()

	// Add frozen shards to memtables list for reading
	for _, shard := range frozen.shards() {
		e.memtables = append(e.memtables, memtableAdapter{shard})
		// Request flush for each shard individually
		e.fm.requestFlush(shard)
	}

	// Create new active sharded memtable
	e.activeMem = newShardedMemtable(e.opts.MemTableSize, e.opts.MemTableShards)

	// Request async flush for each shard
	for _, shard := range frozen.shards() {
		e.fm.requestFlush(shard)
	}

	return nil
}

// Sync flushes the active memtable to SST and blocks until complete.
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
	if e.closed.Load() {
		return nil, ErrClosed
	}

	e.statsMu.Lock()
	defer e.statsMu.Unlock()

	e.mu.RLock()
	defer e.mu.RUnlock()

	// Check active memtable shards first
	if e.activeMem != nil {
		if val, found := e.activeMem.Get(key); found {
			e.stats.MemtableHits++
			return val, nil
		}
	}

	// Check frozen memtables
	for i := len(e.memtables) - 1; i >= 0; i-- {
		mt := e.memtables[i]
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
	return nil, ErrNotFound
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
			data, err := e.readSSTFile(file.FileID, sstPath)
			if err != nil {
				e.log.Warn("readFromSST: failed to read SST file", "path", sstPath, "err", err)
				continue
			}

			reader, err := openSST(data)
			if err != nil {
				e.log.Warn("readFromSST: failed to open SST file", "path", sstPath, "err", err)
				continue
			}

			if !reader.mayContain(key) {
				continue
			}

			if val, found := reader.Find(key); found {
				return val, nil
			}
		}
	}

	return nil, ErrNotFound
}

// readSSTFile reads an SST file, consulting the page cache first (REQ000571).
func (e *engine) readSSTFile(fileID uint64, path string) ([]byte, error) {
	// Try cache first (block-level).
	if cached, ok := e.pageCache.Get(fileID, 0); ok {
		return cached, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Cache in 4KB blocks.
	for off := 0; off < len(data); off += PageSize {
		end := off + PageSize
		if end > len(data) {
			end = len(data)
		}
		e.pageCache.Put(fileID, uint32(off), data[off:end])
	}
	return data, nil
}

func (e *engine) MayContain(key []byte) bool {
	if e.closed.Load() {
		return false
	}

	// Check active memtable shards
	if e.activeMem != nil {
		if _, found := e.activeMem.Get(key); found {
			return true
		}
	}

	// Check frozen memtables
	for _, mt := range e.memtables {
		if _, found := mt.Get(key); found {
			return true
		}
	}

	v := e.manifest.Current()

	for level := 0; level < len(v.levels); level++ {
		files := v.levels[level]
		for i := range files {
			file := &files[i]
			sstPath := filepath.Join(e.dir, fileName(file))
			data, err := e.readSSTFile(file.FileID, sstPath)
			if err != nil {
				e.log.Warn("MayContain: failed to read SST file", "path", sstPath, "err", err)
				continue
			}
			reader, err := openSST(data)
			if err != nil {
				e.log.Warn("MayContain: failed to open SST file", "path", sstPath, "err", err)
				continue
			}
			if reader.mayContain(key) {
				return true
			}
		}
	}

	return false
}

func (e *engine) Stats() ReadStats {
	e.statsMu.RLock()
	defer e.statsMu.RUnlock()
	return e.stats
}

func (e *engine) Close() error {
	if e.closed.Swap(true) {
		return nil // already closed
	}

	if e.cm != nil {
		if err := e.cm.Close(); err != nil {
			return err
		}
	}
	if e.fm != nil {
		if err := e.fm.Close(); err != nil {
			return err
		}
	}
	e.cm = nil
	e.fm = nil
	if e.manifest != nil {
		if err := e.manifest.Close(); err != nil {
			return err
		}
	}
	e.manifest = nil
	return nil
}
