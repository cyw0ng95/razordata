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
	ErrRetryExceeded    = errors.New("ls: retry limit exceeded")
)

const (
	DefaultMemTableShards = 1
	DefaultMemTableSize   = 64 * 1024 * 1024
)

type memtableAdapter struct{ *memtable }

func (m memtableAdapter) Iterator() RangeIter {
	return &iteratorAdapter{it: m.memtable.Iterator()}
}

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

type Options struct {
	MemTableShards int
	MemTableSize   int64
}

func DefaultOptions() Options {
	return Options{MemTableShards: DefaultMemTableShards, MemTableSize: DefaultMemTableSize}
}

type ReadStats struct {
	MemtableHits int64
	SSTHits      int64
	DiskReads    int64
}

type engine struct {
	dir       string
	memtables []memtableIface
	activeMem *shardedMemtable
	manifest  *manifest
	cm        *compactionManager
	fm        *flushManager
	pageCache *PageCache
	opts      Options
	stats     struct {
		MemtableHits atomic.Int64
		SSTHits      atomic.Int64
		DiskReads    atomic.Int64
	}
	mu     sync.RWMutex
	closed atomic.Bool
	log    *slog.Logger
}

func newEngine(dir string) (*engine, error) {
	return newEngineWithOptions(dir, DefaultOptions())
}

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
	activeMem := newShardedMemtable(opts.MemTableSize, opts.MemTableShards)
	e := &engine{
		dir:       dir,
		memtables: make([]memtableIface, 0, 4),
		activeMem: activeMem,
		manifest:  manifest,
		pageCache: NewPageCache(DefaultPageCacheSize),
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
	if err := e.activeMem.Insert(key, value); err != nil {
		return err
	}
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
	for _, shard := range frozen.shards() {
		e.memtables = append(e.memtables, memtableAdapter{shard})
		e.fm.requestFlush(shard)
	}
	e.activeMem = newShardedMemtable(e.opts.MemTableSize, e.opts.MemTableShards)
	return nil
}

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
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.activeMem != nil {
		if val, found := e.activeMem.Get(key); found {
			e.stats.MemtableHits.Add(1)
			return val, nil
		}
	}
	for i := len(e.memtables) - 1; i >= 0; i-- {
		mt := e.memtables[i]
		if val, found := mt.Get(key); found {
			e.stats.MemtableHits.Add(1)
			return val, nil
		}
	}
	val, err := e.readFromSST(key)
	if err == nil {
		e.stats.SSTHits.Add(1)
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
			e.stats.DiskReads.Add(1)
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

func (e *engine) readSSTFile(fileID uint64, path string) ([]byte, error) {
	if cached, ok := e.pageCache.Get(fileID, 0); ok {
		return cached, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
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
	if e.activeMem != nil {
		if _, found := e.activeMem.Get(key); found {
			return true
		}
	}
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
	return ReadStats{
		MemtableHits: e.stats.MemtableHits.Load(),
		SSTHits:      e.stats.SSTHits.Load(),
		DiskReads:    e.stats.DiskReads.Load(),
	}
}

func (e *engine) Close() error {
	if e.closed.Swap(true) {
		return nil
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
