package ls

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	newLevels := make([][]SSTFileMeta, len(current.levels))
	if len(newLevels) == 0 {
		newLevels = [][]SSTFileMeta{{}}
	}
	for i := range current.levels {
		newLevels[i] = append([]SSTFileMeta(nil), current.levels[i]...)
	}
	newLevels[0] = append(files, newLevels[0]...)

	v := Version{
		num:     current.num + 1,
		levels:  newLevels,
		created: time.Now(),
	}

	if err := fj.manifest.Apply(v); err != nil {
		_ = os.Remove(finalPath)
		return err
	}
	return nil
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
	enqueueMu       sync.Mutex
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
			// Stop requested. Drain any queued jobs so every
			// Add(1) has a matching Done(). Use a simple
			// non-blocking drain: if the queue is still being
			// written to, those jobs will see done and not
			// Add(), so this one-pass drain is safe.
			for {
				select {
				case job, ok := <-fm.flushQueue:
					if !ok {
						return
					}
					if err := job.Run(); err != nil {
						e := err
						fm.lastErr.Store(&e)
					}
					fm.pendingWGs.Done()
				default:
					return
				}
			}
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

// requestFlush enqueues a memtable for background flushing.
// If the flush queue is full, we retry with a small backoff
// rather than silently dropping the job (which would lose the
// memtable's data without any operator-visible signal). The
// previous `default` branch called `pendingWGs.Done()` and
// returned, leaving the memtable enqueued for the next
// `requestFlush` to pick up — but in practice the next
// `requestFlush` was for a *different* memtable, so the
// dropped memtable was effectively orphaned. See REQ000347.
//
// The retry path uses a non-blocking send to avoid stalling
// the writer goroutine on a stalled flush worker. After
// `maxFlushRetries` attempts we fall through to a blocking
// send, which the flush worker must service before any
// further writes can complete. This is preferable to silent
// loss: the worst case is a write stall under sustained
// flush-queue saturation, not data loss.
const maxFlushRetries = 8

func (fm *flushManager) requestFlush(m *memtable) {
	// Hold enqueueMu so the enqueue + pendingWGs.Add pair is atomic
	// relative to Stop(). Without this, a concurrent Stop() could
	// close `done` between the channel send and Add(1), causing a
	// negative WaitGroup counter when the drain phase calls Done()
	// for an item that was never Add()ed. See REQ000364.
	//
	// We call Add(1) BEFORE the send (and undo with Add(-1) on
	// failure) so that the flushLoop's Done() can never observe a
	// pendingWGs count of zero for a job that was already delivered
	// to the channel. The previous ordering — Add after send — was
	// racy: the receive side could run Done before requestFlush's
	// goroutine reached Add(1).
	fm.enqueueMu.Lock()
	defer fm.enqueueMu.Unlock()

	select {
	case <-fm.done:
		return
	default:
	}

	m.Freeze()

	id := nextFileID()
	job := &flushJob{
		memtable:   m,
		outputPath: filepath.Join(fm.dir, "sst"),
		manifest:   fm.manifest,
		fileID:     id,
		level:      0,
	}
	for attempt := 0; attempt < maxFlushRetries; attempt++ {
		select {
		case <-fm.done:
			return
		default:
		}
		fm.pendingWGs.Add(1)
		select {
		case fm.flushQueue <- job:
			return
		default:
			fm.pendingWGs.Add(-1)
			runtime.Gosched()
		}
	}
	// Retries exhausted. Double-check done before blocking.
	select {
	case <-fm.done:
		return
	default:
	}
	fm.pendingWGs.Add(1)
	fm.flushQueue <- job
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
	// Hold enqueueMu so we serialize against any in-flight
	// requestFlush that is about to call pendingWGs.Add(1).
	// Without this, Stop() could close `done` between the
	// channel send and Add(1), and the drain phase would then
	// call Done() for a job that was never Add()ed, panicking
	// with "negative WaitGroup counter". See REQ000364.
	fm.enqueueMu.Lock()
	fm.stopOnce.Do(func() {
		close(fm.done)
	})
	fm.enqueueMu.Unlock()
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
