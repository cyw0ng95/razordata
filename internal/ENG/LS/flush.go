package ls

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrMemtableNotFrozen = errors.New("memtable is not frozen")
	ErrFlushInProgress   = errors.New("flush already in progress")
)

type flushJob struct {
	memtable   *memtable
	outputPath string
	manifest   *manifest
	fileID     uint64
	level      int
}

func (fj *flushJob) Run() error {
	if !fj.memtable.IsFrozen() {
		return ErrMemtableNotFrozen
	}

	// R16-7: flush writes a temp file inside the SST dir, then renames
	// it to the fileName(meta) path that compaction.fileName produces.
	// Before the fix, flush wrote <dir>/L0_<id>.sst (flat), so the
	// compaction reader (which uses sst/L<N>_<minkey>_<maxkey>_<id>.sst)
	// could not find the freshly flushed SST. The two-step write
	// (temp -> rename) is required because we need to scan the
	// memtable to compute MinKey/MaxKey before we can construct
	// fileName(meta).
	sstDir := fj.outputPath
	tmpPath := filepath.Join(sstDir, fmt.Sprintf(".tmp_%d_%d.sst", fj.fileID, time.Now().UnixNano()))
	if err := os.MkdirAll(sstDir, 0o755); err != nil {
		return err
	}
	sstData, err := fj.flushToSST()
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpPath, sstData, 0o644); err != nil {
		return err
	}

	if err := fj.updateManifest(tmpPath); err != nil {
		// Best-effort cleanup of the orphan temp file.
		_ = os.Remove(tmpPath)
		return err
	}

	return nil
}

func (fj *flushJob) flushToSST() ([]byte, error) {
	w := newSSTWriter()

	it := fj.memtable.Iterator()
	for it.Next() {
		w.Add(it.Key(), it.Value())
	}

	data, err := w.Finish()
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (fj *flushJob) updateManifest(tmpPath string) error {
	stat, err := os.Stat(tmpPath)
	if err != nil {
		return err
	}

	it := fj.memtable.Iterator()
	var minKey, maxKey []byte
	var keyCount int64
	for it.Next() {
		if minKey == nil {
			minKey = it.Key()
		}
		maxKey = it.Key()
		keyCount++
	}

	if keyCount == 0 {
		// Nothing to flush; drop the temp file and skip manifest update.
		_ = os.Remove(tmpPath)
		return nil
	}

	meta := SSTFileMeta{
		FileID:    fj.fileID,
		Level:     fj.level,
		MinKey:    minKey,
		MaxKey:    maxKey,
		Size:      stat.Size(),
		BloomBits: 10,
	}

	// R16-7: rename temp file to the fileName(meta) path so the
	// compaction reader (which uses fileName) can locate it. The
	// temp file lives in <engineDir>/sst/; the final path is
	// <engineDir>/<fileName(meta)> where fileName returns a relative
	// path that already starts with sst/.
	engineDir := filepath.Dir(filepath.Dir(tmpPath))
	finalPath := filepath.Join(engineDir, fileName(&meta))
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return err
	}

	files := []SSTFileMeta{meta}

	current := fj.manifest.Current()
	newLevels := make([][]SSTFileMeta, len(current.levels)+1)
	copy(newLevels, current.levels)
	newLevels[len(current.levels)] = files

	v := Version{
		num:     current.num + 1,
		levels:  newLevels,
		created: time.Now(),
	}

	return fj.manifest.Apply(v)
}

type flushManager struct {
	activeMemtable  atomic.Pointer[memtable]
	frozenMemtables []*memtable
	manifest        *manifest
	dir             string
	maxMemSize      int64
	flushQueue      chan *flushJob
	pendingWGs      sync.WaitGroup
	done            chan struct{}
	closed          atomic.Bool
	loopDone        chan struct{}
	stopOnce        sync.Once
	lastErr         atomic.Pointer[error]
}

func newFlushManager(dir string, maxMemSize int64, manifest *manifest) *flushManager {
	fm := &flushManager{
		dir:        dir,
		maxMemSize: maxMemSize,
		manifest:   manifest,
		flushQueue: make(chan *flushJob, 10),
		done:       make(chan struct{}),
		loopDone:   make(chan struct{}),
	}

	// R16-7: ensure the sst/ subdir exists. flush writes L0 SSTs to
	// <dir>/sst/L0_<id>.sst to align with compaction.fileName, which
	// also writes to <dir>/sst/... . MkdirAll is a no-op if the dir
	// already exists.
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0o755); err != nil {
		// Non-fatal: flushJob.Run will surface the failure on first
		// rename if mkdir actually failed.
		fmt.Fprintf(os.Stderr, "flush: mkdir sst: %v\n", err)
	}

	active := newMemtable(maxMemSize)
	fm.activeMemtable.Store(active)

	go fm.flushLoop()

	return fm
}

func (fm *flushManager) flushLoop() {
	defer close(fm.loopDone)
	for {
		select {
		case <-fm.done:
			return
		case job, ok := <-fm.flushQueue:
			if !ok {
				return
			}
			if err := job.Run(); err != nil {
				e := err
				fm.lastErr.Store(&e)
			}
			fm.pendingWGs.Done()
		}
	}
}

func (fm *flushManager) MaybeFlush() {
	active := fm.activeMemtable.Load()
	if active.ShouldFlush() {
		fm.requestFlush(active)
	}
}

func (fm *flushManager) requestFlush(m *memtable) {
	m.Freeze()

	id := nextFileID()
	fm.pendingWGs.Add(1)
	select {
	case fm.flushQueue <- &flushJob{
		memtable: m,
		// outputPath is the directory the flush writes temp files
		// to. The final SST path is <dir>/<fileName(meta)> and is
		// constructed by flushJob.Run via the manifest SSTFileMeta.
		// Before the iter-16 fix, outputPath was the final flat path
		// <dir>/L0_<id>.sst which the compaction reader could not
		// locate. (R16-7)
		outputPath: filepath.Join(fm.dir, "sst"),
		manifest:   fm.manifest,
		fileID:     id,
		level:      0,
	}:
	default:
		fm.pendingWGs.Done()
	}
}

// WaitForFlush blocks until every enqueued flush job has completed.
// Used by Engine.Sync to ensure active memtable data is durable in
// an SST before the caller proceeds.
func (fm *flushManager) WaitForFlush() {
	fm.pendingWGs.Wait()
}

// Stop signals the flush goroutine to exit and waits for it,
// bounded by ctx. Idempotent: a second call returns nil immediately
// if the loop has already exited.
//
// Stop is the graceful-shutdown entry point (Phase4.1 of
// SYS.md:245-251). It does NOT wait for in-flight flush jobs to
// finish — call WaitForFlush for that. It only waits for the
// dispatch loop to exit.
func (fm *flushManager) Stop(ctx context.Context) error {
	fm.stopOnce.Do(func() {
		close(fm.done)
	})
	select {
	case <-fm.loopDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (fm *flushManager) ActiveMemtable() *memtable {
	return fm.activeMemtable.Load()
}

func (fm *flushManager) Get(key []byte) ([]byte, bool) {
	active := fm.activeMemtable.Load()
	if val, found := active.Get(key); found {
		return val, found
	}

	for _, m := range fm.frozenMemtables {
		if val, found := m.Get(key); found {
			return val, found
		}
	}

	return nil, false
}

func (fm *flushManager) Insert(key, value []byte) error {
	active := fm.activeMemtable.Load()
	if err := active.Insert(key, value); err != nil {
		return err
	}

	if active.ShouldFlush() {
		fm.MaybeFlush()
	}

	return nil
}

func (fm *flushManager) Close() error {
	if !fm.closed.CompareAndSwap(false, true) {
		return nil
	}
	// Wait for the flushLoop goroutine to finish processing
	// pending jobs. Without this, the engine's manifest.Close
	// can race with a flush job's updateManifest call, causing a
	// "send on closed channel" panic.
	if err := fm.Stop(context.Background()); err != nil {
		return err
	}
	if p := fm.lastErr.Load(); p != nil {
		return *p
	}
	return nil
}

var fileIDCounter uint64

func nextFileID() uint64 {
	return atomic.AddUint64(&fileIDCounter, 1)
}
