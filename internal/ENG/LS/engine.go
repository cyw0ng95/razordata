package ls

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrNoActiveMemtable    = errors.New("ls: no active memtable")
	// REQ001472: pageBufPool recycles 4KB buffers to eliminate
	// per-page make([]byte, PageSize) allocations during SST loading.
	pageBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, PageSize)
			return &b
		},
	}
	ErrClosed              = errors.New("ls: engine is closed")
	ErrNotFound            = errors.New("ls: key not found")
	ErrRetryExceeded       = errors.New("ls: retry limit exceeded")
	ErrBatchLengthMismatch = errors.New("ls: WriteBatch keys/values length mismatch")
)

const (
	DefaultMemTableShards = 1
	DefaultMemTableSize   = 128 * 1024 * 1024
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
	FS             FS    // REQ001172: virtual filesystem for testability
	MmapFiles      bool  // REQ001227: zero-copy reads via mmap
	BlockCacheSize int   // REQ001242: decompressed SST block cache, 0 = disabled
	SmallTableRows int64 // REQ001244: skip SST reads for tables with ≤N rows

	// REQ001304: LSM auto-compaction settings.
	AutoCompactMode    string  // "none" | "incremental" | "full", default "none"
	AutoCompactThreshold float64 // garbage ratio threshold, default 0.3
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
	dir        string
	memtables  []memtableIface
	activeMem  *shardedMemtable
	manifest   *manifest
	cm         *compactionManager
	fm         *flushManager
	pageCache  *PageCache
	blockCache *BlockCache
	opts       Options
	fs         FS // REQ001172: virtual filesystem
	stats      struct {
		MemtableHits atomic.Int64
		SSTHits      atomic.Int64
		DiskReads    atomic.Int64
	}
	mu     sync.RWMutex
	closed atomic.Bool
	log    *slog.Logger

	// REQ001227: mmap cache for zero-copy SST reads
	mmapCache map[string][]byte // file path → mmap'd []byte
	mmapMu    sync.RWMutex
}

func newEngine(dir string) (*engine, error) {
	return newEngineWithOptions(dir, DefaultOptions())
}

func newEngineWithOptions(dir string, opts Options) (*engine, error) {
	e := &engine{
		dir:       dir,
		memtables: make([]memtableIface, 0, 4),
		opts:      opts,
		fs:        opts.FS,
		log:       slog.Default(),
	}
	if e.fs == nil {
		e.fs = DefaultFS()
	}
	activeMem := newShardedMemtable(opts.MemTableSize, opts.MemTableShards)
	e.activeMem = activeMem
	e.pageCache = NewPageCache(DefaultPageCacheSize)

	// REQ001242: initialize block cache (default 1024 blocks)
	cacheSize := opts.BlockCacheSize
	if cacheSize < 0 {
		cacheSize = 1024
	}
	e.blockCache = NewBlockCache(cacheSize)

	if err := e.fs.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	sstDir := filepath.Join(dir, "sst")
	if err := e.fs.MkdirAll(sstDir, 0755); err != nil {
		return nil, err
	}
	manifest, err := newManifestWithFS(dir, e.fs)
	if err != nil {
		return nil, err
	}
	e.manifest = manifest
	e.cm = newCompactionManager(e.fs, dir, manifest, e.blockCache)
	e.cm.autoCompactMode = opts.AutoCompactMode
	e.cm.autoCompactThreshold = opts.AutoCompactThreshold
	e.fm = newFlushManager(e.fs, dir, opts.MemTableSize, manifest)
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

// WriteBatch inserts a contiguous list of key/value pairs into the
// active memtable, deferring the per-row flush threshold check
// until the end. Each pair keeps the same error semantics as
// Write: an empty pair is a no-op, and the first failed insert
// short-circuits the rest of the batch.
//
// REQ001421: per-call overhead is amortised over N rows for
// autocommit INSERT batches. The cost model:
//   - Single Write:  1× closed.Load + 1× shouldFlush.Load atomics
//                    per row.
//   - WriteBatch:   1× closed.Load + 1× shouldFlush.Load atomics
//                    per batch.
// For 100-row INSERT batches this drops the atomic-bound cost
// from O(N) to O(1).
func (e *engine) WriteBatch(keys, values [][]byte) error {
	if e.activeMem == nil {
		return ErrNoActiveMemtable
	}
	if e.closed.Load() {
		return ErrClosed
	}
	if len(keys) != len(values) {
		return ErrBatchLengthMismatch
	}
	for i := range keys {
		if err := e.activeMem.Insert(keys[i], values[i]); err != nil {
			return err
		}
	}
	if e.activeMem.ShouldFlush() {
		return e.flushActiveMemtable()
	}
	return nil
}

// DeleteBatch writes tombstones for a contiguous list of keys in a
// single amortised call. Symmetrical with WriteBatch — the closed
// state and ShouldFlush atomic checks are paid once per batch
// instead of per key. REQ001556.
//
// Empty input is a no-op. The first failed insert short-circuits
// the rest of the batch (matching per-row Write/Delete semantics).
func (e *engine) DeleteBatch(keys [][]byte) error {
	if e.activeMem == nil {
		return ErrNoActiveMemtable
	}
	if e.closed.Load() {
		return ErrClosed
	}
	if len(keys) == 0 {
		return nil
	}
	for i := range keys {
		if err := e.activeMem.Insert(keys[i], tombstoneValue); err != nil {
			return err
		}
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
	// REQ001304: auto_compact — trigger compaction after flush.
	e.cm.MaybeCompact()
	return nil
}

func (e *engine) Read(key []byte) ([]byte, error) {
	if e.closed.Load() {
		return nil, ErrClosed
	}
	e.mu.RLock()
	if e.activeMem != nil {
		if val, found := e.activeMem.Get(key); found {
			e.mu.RUnlock()
			e.stats.MemtableHits.Add(1)
			return val, nil
		}
	}
	for i := len(e.memtables) - 1; i >= 0; i-- {
		mt := e.memtables[i]
		if val, found := mt.Get(key); found {
			e.mu.RUnlock()
			e.stats.MemtableHits.Add(1)
			return val, nil
		}
	}
	// REQ001131: snapshot the manifest under RLock, then release
	// the lock before doing I/O-heavy SST reads. The Version
	// pointer is loaded atomically from the manifest, so it is
	// safe to use after releasing the lock.
	version := e.manifest.Current()
	e.mu.RUnlock()

	val, err := e.readFromSSTWithVersion(key, version)
	if err == nil {
		e.stats.SSTHits.Add(1)
		return val, nil
	}
	return nil, ErrNotFound
}

func (e *engine) readFromSST(key []byte) ([]byte, error) {
	return e.readFromSSTWithVersion(key, e.manifest.Current())
}

// readFromSSTWithVersion searches SST files using the provided version
// snapshot. This allows callers to release locks before I/O.
func (e *engine) readFromSSTWithVersion(key []byte, version *Version) ([]byte, error) {
	for level := 0; level < len(version.levels); level++ {
		files := version.levels[level]
		for i := len(files) - 1; i >= 0; i-- {
			// REQ001131: files slice is owned by the version snapshot,
			// which is immutable after creation. Safe to access without lock.
			file := files[i]
			if bytes.Compare(key, file.MinKey) < 0 || bytes.Compare(key, file.MaxKey) > 0 {
				continue
			}
			e.stats.DiskReads.Add(1)
			sstPath := filepath.Join(e.dir, fileName(&file))
			reader, err := e.getSSTReader(file.FileID, sstPath)
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

func (e *engine) getSSTReader(fileID uint64, path string) (*sstReader, error) {
	// REQ001227: try mmap cache first for zero-copy reads
	if e.opts.MmapFiles {
		if data, ok := e.getMmapData(path); ok {
			r, err := openSSTWithPath(data, path)
			if err != nil {
				return nil, err
			}
			r.mmap = data
			r.blockCache = e.blockCache // REQ001242
			return r, nil
		}
	}
	if cached, ok := e.pageCache.Get(fileID, 0); ok {
		r, err := openSSTWithPath(cached, path)
		if err != nil {
			return nil, err
		}
		r.blockCache = e.blockCache // REQ001242
		return r, nil
	}
	data, err := e.loadSSTMeta(fileID, path)
	if err != nil {
		return nil, err
	}
	r, err := openSSTWithPath(data, path)
	if err != nil {
		return nil, err
	}
	if e.opts.MmapFiles && len(r.mmap) == 0 {
		// loadSSTMeta already mmap'd and cached, set mmap on reader
		if mmapData, ok := e.getMmapData(path); ok {
			r.mmap = mmapData
		}
	}
	r.blockCache = e.blockCache // REQ001242
	return r, nil
}

// getMmapData returns mmap'd data for a file path, if available.
func (e *engine) getMmapData(path string) ([]byte, bool) {
	if e.mmapCache == nil {
		return nil, false
	}
	e.mmapMu.RLock()
	defer e.mmapMu.RUnlock()
	data, ok := e.mmapCache[path]
	return data, ok
}

// setMmapData stores mmap'd data for a file path.
func (e *engine) setMmapData(path string, data []byte) {
	if e.mmapCache == nil {
		e.mmapCache = make(map[string][]byte)
	}
	e.mmapMu.Lock()
	defer e.mmapMu.Unlock()
	e.mmapCache[path] = data
}

// delMmapData removes and unmmaps data for a file path.
func (e *engine) delMmapData(path string) {
	if e.mmapCache == nil {
		return
	}
	e.mmapMu.Lock()
	defer e.mmapMu.Unlock()
	if data, ok := e.mmapCache[path]; ok {
		delete(e.mmapCache, path)
		munmapFile(data)
	}
}

// unmapAll unmmaps all cached mmap'd SST files. Called on engine close.
func (e *engine) unmapAll() {
	if e.mmapCache == nil {
		return
	}
	e.mmapMu.Lock()
	defer e.mmapMu.Unlock()
	for path, data := range e.mmapCache {
		munmapFile(data)
		delete(e.mmapCache, path)
	}
	e.mmapCache = nil
}

func (e *engine) loadSSTMeta(fileID uint64, path string) ([]byte, error) {
	fi, err := e.fs.Stat(path)
	if err != nil {
		return nil, err
	}
	fileSize := int(fi.Size())
	if fileSize < 32 {
		return nil, ErrInvalidSSTFormat
	}

	// REQ001227: use mmap for zero-copy reads when enabled
	if e.opts.MmapFiles {
		if data := mmapFile(path, int64(fileSize)); data != nil {
			e.setMmapData(path, data)
			if len(data) < sstFooterSizeOld {
				munmapFile(data)
				e.delMmapData(path)
				return nil, ErrInvalidSSTFormat
			}
			return data, nil
		}
	}

	f, err := e.fs.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Read footer (last page containing the footer)
	footerPageOff := ((fileSize - 1) / PageSize) * PageSize
	footerReadLen := fileSize - footerPageOff
	footerBuf := make([]byte, footerReadLen)
	if _, err := f.ReadAt(footerBuf, int64(footerPageOff)); err != nil {
		return nil, err
	}
	e.pageCache.Put(fileID, uint32(footerPageOff), footerBuf)

	// Parse footer
	footerOff := footerReadLen - 28
	indexOffset := int(binary.LittleEndian.Uint64(footerBuf[footerOff:]))
	indexSize := int(binary.LittleEndian.Uint32(footerBuf[footerOff+8:]))
	bloomOffset := int(binary.LittleEndian.Uint64(footerBuf[footerOff+12:]))
	bloomSize := int(binary.LittleEndian.Uint32(footerBuf[footerOff+20:]))

	if indexOffset <= 0 || indexSize <= 0 || bloomOffset <= 0 {
		return nil, ErrInvalidSSTFormat
	}

	// Read index block + bloom data from file, page by page
	dataEnd := bloomOffset + bloomSize
	if dataEnd > fileSize {
		dataEnd = fileSize
	}
	dataStart := indexOffset
	totalSize := dataEnd - dataStart
	data := make([]byte, totalSize)

	for off := dataStart; off < dataEnd; off += PageSize {
		pageOff := (off / PageSize) * PageSize
		bufPtr := pageBufPool.Get().(*[]byte)
		pbuf := *bufPtr
		readStart := int64(pageOff)
		n, err := f.ReadAt(pbuf, readStart)
		if err != nil && err != io.EOF {
			pageBufPool.Put(bufPtr)
			return nil, err
		}
		pbuf = pbuf[:n]
		e.pageCache.Put(fileID, uint32(pageOff), pbuf)

		// Copy into result
		destOff := off - dataStart
		srcOff := off - pageOff
		copyLen := n - srcOff
		if destOff+copyLen > totalSize {
			copyLen = totalSize - destOff
		}
		if copyLen > 0 {
			copy(data[destOff:destOff+copyLen], pbuf[srcOff:srcOff+copyLen])
		}
		// Buffer copied — return to pool.
		pageBufPool.Put(bufPtr)
	}

	// Cache the assembled index+bloom data at sentinel offset 0
	e.pageCache.Put(fileID, 0, data)

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
			reader, err := e.getSSTReader(file.FileID, sstPath)
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
	e.unmapAll() // REQ001227: clean up mmap'd SST files
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

// REQ001497 follow-up: DropAll resets the engine to a freshly-opened
// state without closing it. Clears in-memory memtables, manifest
// state, page/block caches, mmap cache, and removes all SST files
// from disk so the next query sees an empty slate. This is the
// correct "drop everything between test files" primitive — the
// previous approach (clearing only DT.Tables + page cache) left
// SST data on disk and corrupted subsequent SLT runs that shared
// the same driver instance across files.
//
// Called from Engine.Reset between SLT corpus files (REQ001454).
// Idempotent.
func (e *engine) DropAll() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	// 1. Unmap all mmap'd SST files.
	e.unmapAll()

	// 2. Clear in-memory state.
	e.memtables = nil
	e.activeMem = newShardedMemtable(e.opts.MemTableSize, e.opts.MemTableShards)
	e.stats.MemtableHits.Store(0)
	e.stats.SSTHits.Store(0)
	e.stats.DiskReads.Store(0)

	// 3. Reset manifest to an empty version (no SST files, no levels).
	if e.manifest != nil {
		emptyVersion := &Version{
			num:     1,
			levels:  make([][]SSTFileMeta, 0),
			created: time.Now(),
		}
		e.manifest.current.Store(emptyVersion)
		e.manifest.version.Store(1)
		// Persist the empty manifest so reload-on-restart sees a clean slate.
		// Best-effort: ignore errors here — the in-memory state is what
		// matters for the current session.
		_ = e.manifest.Apply(*emptyVersion)
	}

	// 4. Reset page cache.
	if e.pageCache != nil {
		e.pageCache.Reset()
	}

	// 5. Remove all SST files from disk so a subsequent Open() reload
	//    starts from the same empty state.
	sstDir := filepath.Join(e.dir, "sst")
	if entries, err := os.ReadDir(sstDir); err == nil {
		for _, ent := range entries {
			if !ent.IsDir() {
				_ = os.Remove(filepath.Join(sstDir, ent.Name()))
			}
		}
	}

	return nil
}
