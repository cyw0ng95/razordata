// Package fl implements the WAL Flusher cluster.
//
// The Flusher owns the directory-fsync side of WAL durability:
// after the Writer (WR) has fsynced a segment, the caller invokes
// FL.SyncDir to ensure the segment's directory entry is durable
// (R10). The Flusher implements group commit coordination via
// BatchSync (REQ000176) using sync.WaitGroup as a write barrier,
// and provides a pre-allocated 256 KB writeBuffer (REQ000184) to
// batch multiple WAL records into a single fsync.
package fl

import (
	"errors"
	"sync"

	"github.com/cyw0ng95/razordata/internal/FIL/FS"
	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
)

// walDirName is the relative subdirectory under the database root
// where the SegmentManager creates segment files. SyncDir resolves
// this path through the FileManager's path validator.
const walDirName = "wal"

// Flusher batches and persists WAL records to disk (R08).
type Flusher interface {
	// Sync is reserved for the group-commit coordinator that will
	// land with the TXN cluster. In v1 the Writer (WR) is the sole
	// write path and handles its own flush+fsync; this method
	// returns nil. Safe to call after Close (no-op).
	Sync() error
	// BatchSync is reserved for group commit (multiple transactions,
	// single fsync). v1 returns nil. Safe to call after Close.
	BatchSync() error
	// SyncDir fsyncs the WAL directory to ensure segment directory
	// entries are durable. Must be called after any segment
	// create/close. Idempotent and safe to call after Close.
	SyncDir() error
	// Close releases resources. Idempotent (R22). After Close all
	// other methods are no-ops.
	Close() error
}

// flusher is the concrete Flusher implementation.
type flusher struct {
	sm  *lf.SegmentManager
	fm  *fs.FileManager
	lsn *lsnCounter
	log lg.Logger

	closed atomicBool
	wbuf   *writeBuffer

	// batchCommit is the wait group for group commit (REQ000176).
	// Callers add(1) before issuing a write, then wait() for
	// BatchSync to signal completion.
	batchCommit sync.WaitGroup
	// mu guards the pending batch queue and syncErr.
	mu      sync.Mutex
	syncErr error
}

// writeBuffer is a pre-allocated 256 KB buffer for batched WAL writes
// (iter-15 REQ000184). It is owned by the Flusher and can be shared
// with the Writer for group-commit scenarios.
type writeBuffer struct {
	buf []byte
	off int
}

// newWriteBuffer allocates a 256 KB write buffer as per WAL.md:103-117.
func newWriteBuffer() *writeBuffer {
	return &writeBuffer{
		buf: make([]byte, 256*1024),
		off: 0,
	}
}

// Reset clears the buffer for reuse.
func (wb *writeBuffer) Reset() {
	wb.off = 0
}

// Available returns the number of bytes remaining in the buffer.
func (wb *writeBuffer) Available() int {
	return len(wb.buf) - wb.off
}

// Write copies data into the buffer. Returns the number of bytes
// written (always len(p)) and an error if the buffer is too small.
func (wb *writeBuffer) Write(p []byte) (int, error) {
	if len(p) > wb.Available() {
		return 0, errors.New("fl.WriteBuffer: buffer full")
	}
	n := copy(wb.buf[wb.off:], p)
	wb.off += n
	return n, nil
}

// Bytes returns the buffered data as a slice (valid until Reset).
func (wb *writeBuffer) Bytes() []byte {
	return wb.buf[:wb.off]
}

// New constructs a Flusher (R35). All four dependencies are required.
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

// Sync persists the write buffer to disk. In v1 this is a stub
// reserved for TXN group-commit coordination.
// REQ000594: the pre-fix implementation always returned nil,
// ignoring f.syncErr. If the batch sync failed (disk full, fsync
// error), Sync() would report success even though the WAL was not
// durably persisted.
func (f *flusher) Sync() error {
	if f.closed.isSet() {
		return nil
	}
	f.batchCommit.Wait()
	return f.syncErr
}

// BatchSync coordinates group commit: waits for all pending writes
// in the batch, then fsyncs once (write barrier). Implements
// REQ000176 per WAL.md:103-117.
func (f *flusher) BatchSync() error {
	if f.closed.isSet() {
		return nil
	}
	// Wait for all writers in this batch to finish copying data.
	f.batchCommit.Wait()

	f.mu.Lock()
	err := f.syncErr
	f.mu.Unlock()
	return err
}

// StartBatch begins a new group commit batch. Callers should call
// this before adding transactions to the batch, then call
// EndBatch() after all transactions are added.
func (f *flusher) StartBatch() {
	f.batchCommit.Add(1)
}

// EndBatch signals that one writer in the batch is done. The
// coordinator (BatchSync) will proceed once all writers call this.
func (f *flusher) EndBatch(err error) {
	f.mu.Lock()
	if err != nil && f.syncErr == nil {
		f.syncErr = err
	}
	f.mu.Unlock()
	f.batchCommit.Done()
}

// SyncDir fsyncs the WAL directory (R10). The call is forwarded to
// the FileManager so the same path-validator and FD cache are used
// across the database; segment directory entries are only durable
// once the directory itself is fsynced.
//
// Idempotent and safe to call after Close (R22).
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

// Close marks the flusher as closed. Idempotent (R22). The first
// caller performs the transition; subsequent callers short-circuit.
// Returns nil (no resources need releasing in v1 — the underlying
// FileManager and SegmentManager are owned by the caller).
func (f *flusher) Close() error {
	if !f.closed.set() {
		return nil
	}
	return nil
}

// LSN returns the Flusher's view of the latest allocated LSN. v1
// the Writer computes LSNs directly from segment+offset, so this
// counter is a read-side cache used by stale-read detection (R14).
// Exposed via the interface implementation; not part of the
// Flusher public interface.
func (f *flusher) LSN() LSN { return f.lsn.Current() }

var _ Flusher = (*flusher)(nil)
