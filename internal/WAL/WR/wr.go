// Package wr implements the WAL Writer cluster.
//
// The Writer owns the active WAL segment, an in-memory write buffer,
// and the LSN counter. Append is the sole write path: it assigns LSNs,
// encodes records, and either batches them in the 256 KB write buffer
// (R37) or flushes when the buffer overflows, when Sync is called, or
// when the active segment reaches SegSize (rotation).
package wr

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"github.com/cyw0ng95/razordata/internal/MEM/SP"
	"golang.org/x/sys/unix"
)

// SegSize is the maximum size of a single WAL segment (R01, R06).
// When writeOff reaches SegSize, the current segment is closed and a
// new one is created.
const SegSize = int64(64 * 1024 * 1024) // 64 MB

// LSN is a Log Sequence Number — the byte offset of a record within
// the entire WAL. The encoding is `segmentNumber * SegSize + offset`,
// so LSN order and segment-then-offset order are aligned (R04, R27).
type LSN = uint64

// LSNFor computes the LSN for a given segment and offset (R04):
//
//	lsn = segmentNumber * SegSize + offset
//
// `offset` is the byte position within the segment at which the record
// starts. Pure function — useful for the replayer to derive an LSN
// from a record it reads off disk.
func LSNFor(segmentNumber, offset uint64) LSN {
	return segmentNumber*uint64(SegSize) + offset
}

// RecordType identifies the type of a WAL record (R01).
type RecordType uint8

const (
	RTData       RecordType = 0
	RTCommit     RecordType = 1
	RTRollback   RecordType = 2
	RTCheckpoint RecordType = 3
	RTMerge      RecordType = 4 // compaction output: ENG writes SST directly, no WAL involvement
)

// LogRecord is a single WAL record (design WAL.md LogRecord Encoding).
//
//	┌──────────────┬──────────┬─────────────┬──────────────────┬───────┐
//	│ length:varint│ txnID:varint│ type:uint8 │ payload:blob     │ CRC32│
//	└──────────────┴──────────┴─────────────┴──────────────────┴───────┘
//
// `length` covers everything after the length field itself (body + CRC).
// As of iter-13 (R13-13) the per-RTData payload CRC is also verified.
type LogRecord struct {
	Type    RecordType
	TxnID   uint64
	Key     []byte
	Value   []byte
	BlockID uint64 // SST/manifest block this record modifies (RTData only).
	// For RTCheckpoint, BlockID is repurposed as the active-TXN count
	// (the field is otherwise unused for that record type).
	// PayCRCFail is set by decodePayload when the inner RTData
	// payload CRC does not match. The replayer reads this flag
	// to surface ErrCorrupt (the envelope CRC is verified inside
	// the decoder, but the inner CRC is checked after payload
	// slicing, which is when this flag is set).
	PayCRCFail bool
}

// WriteBatch is a batch of log records for one transaction.
type WriteBatch struct {
	TxnID uint64
	Recs  []LogRecord
}

// Checkpoint captures a snapshot of engine state (R14). The
// RTCheckpoint record payload format (per design) is:
//
//	[checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4]
//	[activeTXNCount:varint][activeTXNs:varint...]
type Checkpoint struct {
	LSN              uint64
	CatalogRootPtr   uint64
	ManifestChecksum uint32
	ActiveTXNs       []uint64
}

// Writer appends records to the WAL (R03).
type Writer interface {
	// Append encodes and writes a batch of records, returning the LSN
	// of the last record written. If the batch is empty, the returned
	// LSN is 0 and no I/O is performed.
	Append(batch *WriteBatch) (lsn uint64, err error)
	// Sync flushes the in-memory write buffer to the segment FD,
	// fsyncs the segment, and fsyncs the WAL directory. Idempotent
	// (R22) and safe to call concurrently from multiple goroutines
	// (R21).
	Sync() error
	// Close flushes pending writes, fsyncs, and releases resources.
	// Idempotent (R22).
	Close() error
}

// logSegment is the in-memory state for a single open WAL segment.
// One segment is active at a time; Append rotates to a new segment
// when writeOff would exceed SegSize (R06).
type logSegment struct {
	number   uint64
	fh       *lf.FileHandle // owned reference; Close() releases it
	writeOff int64          // total bytes logically written (incl. unflushed buf)
	buf      []byte         // pending writes, len ≤ cap = WALBufSize
}

// writer is the concrete Writer implementation.
type writer struct {
	dir string
	sm  *lf.SegmentManager
	sp  sp.SyncPool
	log lg.Logger

	mu     sync.Mutex // serializes Append/Sync/Close on the active segment
	seg    *logSegment
	closed atomicBool
	synced atomic.Uint64 // highest LSN that has been fsynced
	// maxRecordSize is the per-record-size cap used by Append. Zero
	// means "use SegSize" (the default for production writers). Tests
	// that need to exercise the "record too large" code path override
	// this to a small value to avoid allocating a 64 MB slice.
	maxRecordSize int64
}

// New constructs a Writer rooted at dir. The Writer owns its
// dependencies (sm, sp, log) and is safe to use from a single writer
// goroutine; concurrent Append/Sync is serialized by an internal mutex
// (R21).
func New(dir string, sm *lf.SegmentManager, spPool sp.SyncPool, log lg.Logger) (Writer, error) {
	if dir == "" {
		return nil, errors.New("wr: dir is required")
	}
	if sm == nil {
		return nil, errors.New("wr: SegmentManager is required")
	}
	if spPool == nil {
		return nil, errors.New("wr: SyncPool is required")
	}
	return &writer{dir: dir, sm: sm, sp: spPool, log: log}, nil
}

// Append encodes and appends every record in batch, returning the LSN
// of the last record. An empty batch returns (0, nil) without I/O
// (R07: no reads in the hot path; the writer is append-only).
//
// LSN assignment (R04): each record receives the LSN
// segmentNumber * SegSize + writeOff before encoding. The LSN is the
// byte offset within the WAL, so segment ordering and LSN ordering
// are aligned.
func (w *writer) Append(batch *WriteBatch) (uint64, error) {
	if batch == nil || len(batch.Recs) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Re-check the closed flag under the lock so a concurrent Close
	// cannot observe a not-yet-closed writer and start mutating
	// shared state after we have torn it down (R22).
	if w.closed.isSet() {
		return 0, errors.New("wr: writer is closed")
	}

	if w.seg == nil {
		if err := w.openSegmentLocked(0); err != nil {
			return 0, err
		}
	}

	var lastLSN uint64
	for i := range batch.Recs {
		rec := &batch.Recs[i]
		// Batch TxnID is the authoritative source; override the
		// per-record value in case the caller left it zero.
		rec.TxnID = batch.TxnID

		encoded := encodeRecord(rec)
		recLen := int64(len(encoded))

		// If a single record would not fit in the remaining segment
		// space, flush and rotate first. (If recLen > maxRec, the
		// record is malformed — fail loudly.)
		maxRec := w.maxRecordSize
		if maxRec <= 0 {
			maxRec = SegSize
		}
		if recLen > maxRec {
			return lastLSN, errors.New("wr: single record exceeds SegSize")
		}
		if w.seg.writeOff+recLen > SegSize {
			if err := w.flushBufferLocked(); err != nil {
				return lastLSN, err
			}
			if err := w.rotateLocked(); err != nil {
				return lastLSN, err
			}
		}

		// LSN for this record = segment * SegSize + current writeOff
		// (R04). Compute before appending so the LSN reflects where
		// the record will start, not where the buffer ends.
		lsn := LSNFor(w.seg.number, uint64(w.seg.writeOff))

		// Buffer overflow? Flush first. (R37: flush on overflow.)
		if int64(cap(w.seg.buf))-int64(len(w.seg.buf)) < recLen {
			if err := w.flushBufferLocked(); err != nil {
				return lastLSN, err
			}
		}

		// Append into the pre-allocated buffer — no allocation on
		// the hot path (R24). cap is fixed at WALBufSize so append
		// cannot grow the slice.
		w.seg.buf = append(w.seg.buf, encoded...)
		w.seg.writeOff += recLen
		lastLSN = lsn
	}

	return lastLSN, nil
}

// Sync flushes the in-memory write buffer to the segment FD,
// fsyncs the segment, and updates the synced LSN (R09). Returns
// the fsync error directly (R23) — the caller decides whether
// to retry or surface it.
//
// Idempotent: calling Sync on an empty buffer is a no-op. Calling
// Sync after Close is a no-op (R22). Safe for concurrent callers
// (R21) — serialized on w.mu.
func (w *writer) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Re-check the closed flag under the lock: a concurrent Close
	// could have torn down w.seg between any external observation
	// and the work below (R22).
	if w.closed.isSet() {
		return nil
	}
	return w.syncLocked()
}

// syncLocked is the inner Sync path. Caller must hold w.mu.
// Returns the highest LSN that was fsynced (0 if nothing to sync),
// or an error from flushBufferLocked / unix.Fsync.
func (w *writer) syncLocked() error {
	if w.seg == nil {
		return nil
	}
	// Snapshot the pre-flush state. If the buffer is empty, there
	// is nothing to flush and the previous fsync already covers
	// everything up to writeOff.
	if len(w.seg.buf) == 0 {
		return nil
	}
	// Snapshot the high-water mark of the buffer before flushing, so we
	// can update the synced LSN to the END of the just-fsynced range
	// (the highest LSN that is now durable) only after a successful fsync.
	pendingEnd := w.seg.writeOff
	if err := w.flushBufferLocked(); err != nil {
		return err
	}
	if err := unix.Fsync(w.seg.fh.FD); err != nil {
		if w.log != nil {
			w.log.Error("wr.sync", "seg", w.seg.number, "err", err)
		}
		return err
	}
	syncedLSN := LSNFor(w.seg.number, uint64(pendingEnd))
	if syncedLSN > w.synced.Load() {
		w.synced.Store(syncedLSN)
	}
	return nil
}

// Close flushes any buffered writes, fsyncs the active segment,
// returns the buffer to the pool, and releases the segment FD
// (R22). After Close returns, Append returns an error and Sync is
// a no-op.
//
// Close is idempotent — concurrent and repeat callers all return
// the cached first-call error (or nil). The atomic closed flag
// is the gate: only the first caller performs the teardown;
// others short-circuit.
//
// Errors during teardown are best-effort: a flush or fsync error
// does NOT prevent the FD from being released. The first such
// error is logged and returned to the caller; later Close calls
// receive the same cached error (R22).
func (w *writer) Close() error {
	// First-caller gate. The CAS returns true only on the 0→1
	// transition, so concurrent Close calls collapse to a no-op
	// (they read closed.isSet() as true after the lock is released).
	if !w.closed.set() {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closeLocked()
}

// closeLocked performs the actual teardown under w.mu. Caller
// must hold w.mu AND have already set w.closed. (Idempotency
// for the no-op short-circuit is handled by the caller.)
//
// Returns the first error encountered (or nil). All steps are
// best-effort: a failed flush or fsync is logged but does not
// prevent the FD from being closed.
func (w *writer) closeLocked() error {
	if w.seg == nil {
		// Writer was never used. Nothing to flush or close.
		return nil
	}
	var firstErr error
	recordErr := func(stage string, err error) {
		if err == nil {
			return
		}
		if w.log != nil {
			w.log.Error("wr.close."+stage, "seg", w.seg.number, "err", err)
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	// 1. Flush any in-memory buffer to the segment FD. If the
	// buffer is empty, this is a no-op (R37). Capture writeOff
	// before the flush so we can update the synced LSN to the
	// new durable high-water mark after fsync.
	hadBuffer := len(w.seg.buf) > 0
	pendingEnd := w.seg.writeOff
	if hadBuffer {
		recordErr("flush", w.flushBufferLocked())
		// 2. Fsync the segment FD so the last bytes are durable.
		// (R22: Close does a final fsync.)
		recordErr("fsync", unix.Fsync(w.seg.fh.FD))
		// 3. Update the synced LSN to the new high-water mark.
		syncedLSN := LSNFor(w.seg.number, uint64(pendingEnd))
		if syncedLSN > w.synced.Load() {
			w.synced.Store(syncedLSN)
		}
	}
	// 4. Return the buffer to the pool. The buffer may carry
	// whatever bytes were last in it — the pool's Get path
	// zero-fills (R25), so no information leaks across segments.
	if w.seg.buf != nil {
		w.sp.Put(w.seg.buf)
		w.seg.buf = nil
	}
	// 5. Release the segment FD back to the SegmentManager.
	// The manager may re-open the file on demand; the on-disk
	// bytes are preserved.
	if err := w.seg.fh.Close(); err != nil {
		recordErr("fd", err)
	}
	// 6. Drop the active segment so any future Append (which
	// would fail the closed check) does not try to touch it.
	w.seg = nil
	return firstErr
}

// openSegmentLocked creates and initializes a new active segment.
// Caller must hold w.mu.
func (w *writer) openSegmentLocked(n uint64) error {
	fh, err := w.sm.CreateSegment(n)
	if err != nil {
		if w.log != nil {
			w.log.Error("wr.open_segment", "n", n, "err", err)
		}
		return err
	}
	buf := w.sp.Get(int(sp.WALBufSize))
	if buf == nil {
		_ = fh.Close()
		return errors.New("wr: SyncPool returned nil buffer")
	}
	// R25: zero-fill on buffer reuse. Fresh make'd buffers are
	// already zero; this protects against pool reuse carrying stale
	// data from a prior segment. Cost: one 256 KB memset per
	// segment, paid at most once per 64 MB written.
	for i := range buf {
		buf[i] = 0
	}
	// R13-1 / R13-3: write the segment header immediately after
	// the file is created so the replayer can distinguish a
	// v0.10.0 segment from a v0.9.x segment. The header is
	// outside the buffered write path — a short pwrite with no
	// batching. writeOff starts at WALHeaderSize so the LSN
	// math (segNum * SegSize + writeOff) correctly accounts for
	// the header bytes.
	if err := writeSegmentHeader(fh.FD); err != nil {
		_ = fh.Close()
		return err
	}
	w.seg = &logSegment{
		number:   n,
		fh:       fh,
		writeOff: WALHeaderSize,
		buf:      buf[:0], // accumulate into pre-allocated backing array
	}
	return nil
}

// flushBufferLocked pwrites the current buffer to the active segment
// and resets it. Caller must hold w.mu. (R37: single pwrite of the
// full buffer contents.)
func (w *writer) flushBufferLocked() error {
	if w.seg == nil || len(w.seg.buf) == 0 {
		return nil
	}
	off := w.seg.writeOff - int64(len(w.seg.buf))
	n, err := unix.Pwrite(w.seg.fh.FD, w.seg.buf, off)
	if err != nil {
		if w.log != nil {
			w.log.Error("wr.flush", "seg", w.seg.number, "off", off, "err", err)
		}
		return err
	}
	if n != len(w.seg.buf) {
		return errors.New("wr: short pwrite")
	}
	// Reset slice to length 0, retain capacity (R24: no allocation
	// on the hot path).
	w.seg.buf = w.seg.buf[:0]
	return nil
}

// rotateLocked closes the current segment and opens the next one.
// Caller must hold w.mu. (R06: segment rotation.)
func (w *writer) rotateLocked() error {
	if w.seg != nil {
		// Return the buffer to the pool before closing the handle.
		// (Close happens unconditionally on rotation — even on a
		// partial write — to release the FD.)
		if w.seg.buf != nil {
			w.sp.Put(w.seg.buf)
		}
		if err := w.seg.fh.Close(); err != nil {
			if w.log != nil {
				w.log.Warn("wr.rotate_close", "n", w.seg.number, "err", err)
			}
		}
	}
	return w.openSegmentLocked(w.seg.number + 1)
}

var _ Writer = (*writer)(nil)

// flushForTest forces an immediate buffer flush. Test-only helper
// (in-package so it can access private state). Used by tests that
// want to verify on-disk bytes without depending on the Sync commit.
// NOT a public API — the production path is Append → Sync.
func (w *writer) flushForTest() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushBufferLocked()
}
