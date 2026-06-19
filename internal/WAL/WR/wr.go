// Package wr implements the WAL Writer cluster.
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

// SegSize is the maximum size of a single WAL segment.
const SegSize = int64(64 * 1024 * 1024) // 64 MB

// LSN is a Log Sequence Number.
type LSN = uint64

// LSNFor computes the LSN for a given segment and offset.
func LSNFor(segmentNumber, offset uint64) LSN {
	return segmentNumber*uint64(SegSize) + offset
}

type RecordType uint8

const (
	RTData       RecordType = 0
	RTCommit     RecordType = 1
	RTRollback   RecordType = 2
	RTCheckpoint RecordType = 3
	RTMerge      RecordType = 4
)

// LogRecord is a single WAL record.
type LogRecord struct {
	Type       RecordType
	TxnID      uint64
	Key        []byte
	Value      []byte
	BlockID    uint64
	PayCRCFail bool
}

type WriteBatch struct {
	TxnID uint64
	Recs  []LogRecord
}

// Checkpoint captures a snapshot of engine state.
type Checkpoint struct {
	LSN              uint64
	CatalogRootPtr   uint64
	ManifestChecksum uint32
	ActiveTXNs       []uint64
}

// AsyncSyncResult is the value delivered by SyncAsync (REQ000301).
type AsyncSyncResult struct {
	Err       error
	SyncedLSN uint64
}

// Writer appends records to the WAL.
type Writer interface {
	Append(batch *WriteBatch) (lsn uint64, err error)
	Sync() error
	SyncAsync() (<-chan AsyncSyncResult, error)
	Close() error
}

type logSegment struct {
	number   uint64
	fh       *lf.FileHandle // owned reference; Close() releases it
	writeOff int64          // total bytes logically written (incl. unflushed buf)
	buf      []byte         // pending writes, len ≤ cap = WALBufSize
}

type writer struct {
	dir      string
	sm       *lf.SegmentManager
	sp       sp.SyncPool
	log      lg.Logger
	readOnly bool
	compress bool // REQ000034: lz4 compression of record bodies

	mu                sync.Mutex
	seg               *logSegment
	closed            atomicBool
	synced            atomic.Uint64
	inflightFsyncs    sync.WaitGroup
	inflightFsyncsCnt atomic.Int64
	maxRecordSize     int64
	lsn               LSNCounter // REQ000541: optional batched LSN counter
}

// Options configures optional Writer behavior (REQ000034).
type Options struct {
	Compress   bool
	LSNCounter LSNCounter // REQ000541: optional batched LSN counter
}

// LSNCounter is the minimal interface for batched LSN allocation (REQ000541).
type LSNCounter interface {
	Reserve(n int) LSN
}

// New constructs a Writer rooted at dir.
func New(dir string, sm *lf.SegmentManager, spPool sp.SyncPool, log lg.Logger, readOnly bool) (Writer, error) {
	return NewWithOptions(dir, sm, spPool, log, readOnly, Options{})
}

// NewWithOptions is like New but applies the given Options.
func NewWithOptions(dir string, sm *lf.SegmentManager, spPool sp.SyncPool, log lg.Logger, readOnly bool, opts Options) (Writer, error) {
	if dir == "" {
		return nil, errors.New("wr: dir is required")
	}
	if sm == nil {
		return nil, errors.New("wr: SegmentManager is required")
	}
	if spPool == nil {
		return nil, errors.New("wr: SyncPool is required")
	}
	return &writer{dir: dir, sm: sm, sp: spPool, log: log, readOnly: readOnly, compress: opts.Compress, lsn: opts.LSNCounter}, nil
}

// Append encodes and appends every record in batch.
func (w *writer) Append(batch *WriteBatch) (uint64, error) {
	if batch == nil || len(batch.Recs) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed.isSet() {
		return 0, errors.New("wr: writer is closed")
	}

	if w.readOnly {
		return 0, errors.New("wr: read-only mode")
	}

	if w.seg == nil {
		if err := w.openSegmentLocked(0); err != nil {
			return 0, err
		}
	}

	if w.lsn != nil {
		w.lsn.Reserve(len(batch.Recs))
	}

	var lastLSN uint64
	for i := range batch.Recs {
		rec := &batch.Recs[i]
		rec.TxnID = batch.TxnID

		encoded := encodeRecordCompressed(rec, w.compress)
		recLen := int64(len(encoded))

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

		lsn := LSNFor(w.seg.number, uint64(w.seg.writeOff))

		if int64(cap(w.seg.buf))-int64(len(w.seg.buf)) < recLen {
			if err := w.flushBufferLocked(); err != nil {
				return lastLSN, err
			}
		}

		w.seg.buf = append(w.seg.buf, encoded...)
		w.seg.writeOff += recLen
		lastLSN = lsn
	}

	return lastLSN, nil
}

// Sync flushes the in-memory write buffer and fsyncs the segment.
func (w *writer) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed.isSet() {
		return nil
	}
	return w.syncLocked()
}

func (w *writer) syncLocked() error {
	if w.seg == nil {
		return nil
	}
	if len(w.seg.buf) == 0 {
		return nil
	}
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
	// Use CAS loop to avoid lost update from read-then-write race.
	for {
		old := w.synced.Load()
		if syncedLSN <= old {
			break
		}
		if w.synced.CompareAndSwap(old, syncedLSN) {
			break
		}
	}
	return nil
}

// SyncAsync issues the fsync on a background goroutine (REQ000301).
func (w *writer) SyncAsync() (<-chan AsyncSyncResult, error) {
	ch := make(chan AsyncSyncResult, 1)
	w.mu.Lock()
	if w.closed.isSet() {
		w.mu.Unlock()
		// Closed: deliver a no-op result synchronously.
		ch <- AsyncSyncResult{}
		close(ch)
		return ch, nil
	}
	if w.seg == nil {
		w.mu.Unlock()
		ch <- AsyncSyncResult{}
		close(ch)
		return ch, nil
	}
	if len(w.seg.buf) == 0 {
		ch <- AsyncSyncResult{SyncedLSN: w.synced.Load()}
		w.mu.Unlock()
		close(ch)
		return ch, nil
	}
	pendingEnd := w.seg.writeOff
	segNumber := w.seg.number
	fd := w.seg.fh.FD
	if err := w.flushBufferLocked(); err != nil {
		w.mu.Unlock()
		ch <- AsyncSyncResult{Err: err}
		close(ch)
		return ch, nil
	}
	w.inflightFsyncs.Add(1)
	w.inflightFsyncsCnt.Add(1)
	w.mu.Unlock()

	go func() {
		defer w.inflightFsyncs.Done()
		defer w.inflightFsyncsCnt.Add(-1)
		err := unix.Fsync(fd)
		if err != nil && w.log != nil {
			w.log.Error("wr.sync_async", "seg", segNumber, "err", err)
		}
		syncedLSN := uint64(0)
		if err == nil {
			syncedLSN = LSNFor(segNumber, uint64(pendingEnd))
			if syncedLSN > w.synced.Load() {
				w.synced.Store(syncedLSN)
			}
		}
		ch <- AsyncSyncResult{Err: err, SyncedLSN: syncedLSN}
		close(ch)
	}()
	return ch, nil
}

// Close flushes buffered writes, fsyncs, and releases resources.
func (w *writer) Close() error {
	if !w.closed.set() {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closeLocked()
}

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
	if w.inflightFsyncsCnt.Load() > 0 {
		w.mu.Unlock()
		w.inflightFsyncs.Wait()
		w.mu.Lock()
	}
	hadBuffer := len(w.seg.buf) > 0
	pendingEnd := w.seg.writeOff
	if hadBuffer {
		recordErr("flush", w.flushBufferLocked())
		recordErr("fsync", unix.Fsync(w.seg.fh.FD))
		syncedLSN := LSNFor(w.seg.number, uint64(pendingEnd))
		if syncedLSN > w.synced.Load() {
			w.synced.Store(syncedLSN)
		}
	}
	if w.seg.buf != nil {
		w.sp.Put(w.seg.buf)
		w.seg.buf = nil
	}
	if err := w.seg.fh.Close(); err != nil {
		recordErr("fd", err)
	}
	w.seg = nil
	return firstErr
}

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
	for i := range buf {
		buf[i] = 0
	}
	headerFlags := uint8(0)
	if w.compress {
		headerFlags = FlagCompressionLZ4
	}
	if err := writeSegmentHeaderWithFlags(fh.FD, headerFlags); err != nil {
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
	w.seg.buf = w.seg.buf[:0]
	return nil
}

func (w *writer) rotateLocked() error {
	if w.seg != nil {
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

func (w *writer) flushForTest() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushBufferLocked()
}
