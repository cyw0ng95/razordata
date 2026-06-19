// Package fl implements the WAL Flusher cluster.
package fl

import (
	"errors"
	"sync"

	"github.com/cyw0ng95/razordata/internal/FIL/FS"
	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
)

const walDirName = "wal"

// Flusher batches and persists WAL records to disk (R08).
type Flusher interface {
	Sync() error
	BatchSync() error
	SyncDir() error
	Close() error
}

type flusher struct {
	sm  *lf.SegmentManager
	fm  *fs.FileManager
	lsn *lsnCounter
	log lg.Logger

	closed      atomicBool
	wbuf        *writeBuffer
	batchCommit sync.WaitGroup // REQ000176: group commit barrier
	mu          sync.Mutex
	syncErr     error
}

type writeBuffer struct {
	buf []byte
	off int
}

func newWriteBuffer() *writeBuffer {
	return &writeBuffer{
		buf: make([]byte, 256*1024),
		off: 0,
	}
}

func (wb *writeBuffer) Reset() {
	wb.off = 0
}

func (wb *writeBuffer) Available() int {
	return len(wb.buf) - wb.off
}

func (wb *writeBuffer) Write(p []byte) (int, error) {
	if len(p) > wb.Available() {
		return 0, errors.New("fl.WriteBuffer: buffer full")
	}
	n := copy(wb.buf[wb.off:], p)
	wb.off += n
	return n, nil
}

func (wb *writeBuffer) Bytes() []byte {
	return wb.buf[:wb.off]
}

// New constructs a Flusher.
func New(dir string, sm *lf.SegmentManager, fm *fs.FileManager, log lg.Logger) (Flusher, error) {
	if dir == "" {
		return nil, errors.New("fl: dir is required")
	}
	if sm == nil {
		return nil, errors.New("fl: SegmentManager is required")
	}
	if fm == nil {
		return nil, errors.New("fl: FileManager is required")
	}
	return &flusher{sm: sm, fm: fm, lsn: newLSNCounter(), log: log, wbuf: newWriteBuffer()}, nil
}

// Sync persists the write buffer to disk (REQ000594).
func (f *flusher) Sync() error {
	if f.closed.isSet() {
		return nil
	}
	f.batchCommit.Wait()
	return f.syncErr
}

// BatchSync coordinates group commit (REQ000176).
func (f *flusher) BatchSync() error {
	if f.closed.isSet() {
		return nil
	}
	f.batchCommit.Wait()

	f.mu.Lock()
	err := f.syncErr
	f.mu.Unlock()
	return err
}

// StartBatch begins a new group commit batch.
func (f *flusher) StartBatch() {
	f.batchCommit.Add(1)
}

// EndBatch signals that one writer in the batch is done.
func (f *flusher) EndBatch(err error) {
	f.mu.Lock()
	if err != nil && f.syncErr == nil {
		f.syncErr = err
	}
	f.mu.Unlock()
	f.batchCommit.Done()
}

// SyncDir fsyncs the WAL directory (R10).
func (f *flusher) SyncDir() error {
	if f.closed.isSet() {
		return nil
	}
	if err := f.fm.SyncDir(walDirName); err != nil {
		if f.log != nil {
			f.log.Error("fl.syncdir", "err", err)
		}
		return err
	}
	return nil
}

func (f *flusher) Close() error {
	if !f.closed.set() {
		return nil
	}
	return nil
}

func (f *flusher) LSN() LSN { return f.lsn.Current() }

var _ Flusher = (*flusher)(nil)
