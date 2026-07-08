package ls

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrMemtableNotFrozen = errors.New("ls: memtable is not frozen")
	ErrFlushInProgress   = errors.New("ls: flush already in progress")
)

type flushJob struct {
	fs         FS
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

	sstDir := fj.outputPath
	var tmpName strings.Builder
	tmpName.Grow(32)
	tmpName.WriteString(".tmp_")
	tmpName.WriteString(strconv.FormatUint(fj.fileID, 10))
	tmpName.WriteByte('_')
	tmpName.WriteString(strconv.FormatInt(time.Now().UnixNano(), 10))
	tmpName.WriteString(".sst")
	tmpPath := filepath.Join(sstDir, tmpName.String())
	if err := fj.fs.MkdirAll(sstDir, 0o755); err != nil {
		return err
	}
	sstData, err := fj.flushToSST()
	if err != nil {
		return err
	}
	if err := fj.fs.WriteFile(tmpPath, sstData, 0o644); err != nil {
		return err
	}

	if err := fj.updateManifest(tmpPath); err != nil {
		_ = fj.fs.Remove(tmpPath)
		return err
	}

	return nil
}

func (fj *flushJob) flushToSST() ([]byte, error) {
	w := acquireSSTWriter()
	defer releaseSSTWriter(w)
	w.SetLevel(0) // memtable flush always goes to L0

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
	fi, err := fj.fs.Stat(tmpPath)
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
		_ = fj.fs.Remove(tmpPath)
		return nil
	}

	meta := SSTFileMeta{
		FileID:    fj.fileID,
		Level:     fj.level,
		MinKey:    minKey,
		MaxKey:    maxKey,
		Size:      fi.Size(),
		BloomBits: 10,
		RowCount:  keyCount,
	}

	engineDir := filepath.Dir(filepath.Dir(tmpPath))
	finalPath := filepath.Join(engineDir, fileName(&meta))
	if err := fj.fs.Rename(tmpPath, finalPath); err != nil {
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
		_ = fj.fs.Remove(finalPath)
		return err
	}
	return nil
}

type flushManager struct {
	fs             FS
	activeMemtable atomic.Pointer[memtable]
	manifest       *manifest
	dir            string
	maxMemSize     int64
	targetSize     atomic.Int64 // REQ000552: adaptive memtable size (sampled under load)
	flushQueue     chan *flushJob
	pendingWGs     sync.WaitGroup
	done           chan struct{}
	closed         atomic.Bool
	loopDone       chan struct{}
	stopOnce       sync.Once
	lastErr        atomic.Pointer[error]
	enqueueMu      sync.Mutex
}

func newFlushManager(fs FS, dir string, maxMemSize int64, manifest *manifest) *flushManager {
	fm := &flushManager{
		fs:         fs,
		dir:        dir,
		maxMemSize: maxMemSize,
		manifest:   manifest,
		flushQueue: make(chan *flushJob, 10),
		done:       make(chan struct{}),
		loopDone:   make(chan struct{}),
	}
	fm.targetSize.Store(maxMemSize) // REQ000552: initialize adaptive target

	if err := fs.MkdirAll(filepath.Join(dir, "sst"), 0o755); err != nil {
		slog.Warn("flush: mkdir sst", "err", err)
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

const maxFlushRetries = 8

func (fm *flushManager) requestFlush(m *memtable) {
	// Hold enqueueMu so the enqueue + pendingWGs.Add pair is atomic
	// relative to Stop(). Without this, a concurrent Stop() could
	// close `done` between the channel send and Add(1), causing a
	// negative WaitGroup counter when the drain phase calls Done()
	// for an item that was never Add()ed. See REQ000364.
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
		fs:         fm.fs,
		memtable:   m,
		outputPath: filepath.Join(fm.dir, "sst"),
		manifest:   fm.manifest,
		fileID:     id,
		level:      0,
	}
	for range maxFlushRetries {
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
	select {
	case <-fm.done:
		return
	default:
	}
	fm.pendingWGs.Add(1)
	fm.flushQueue <- job
}

// WaitForFlush blocks until all enqueued flush jobs complete.
func (fm *flushManager) WaitForFlush() {
	fm.pendingWGs.Wait()
}

// Stop signals the flush goroutine to exit and waits for it (REQ000364).
func (fm *flushManager) Stop(ctx context.Context) error {
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

// SetTargetSize updates the adaptive memtable target size (REQ000552).
func (fm *flushManager) SetTargetSize(size int64) {
	if size < 0 {
		size = 0
	}
	fm.targetSize.Store(size)
}

// TargetSize returns the current adaptive memtable target size (REQ000552).
func (fm *flushManager) TargetSize() int64 {
	return fm.targetSize.Load()
}

func (fm *flushManager) Close() error {
	if !fm.closed.CompareAndSwap(false, true) {
		return nil
	}
	if err := fm.Stop(context.Background()); err != nil {
		return err
	}
	if p := fm.lastErr.Load(); p != nil {
		return *p
	}
	return nil
}

var fileIDCounter atomic.Uint64

func nextFileID() uint64 {
	return fileIDCounter.Add(1)
}
