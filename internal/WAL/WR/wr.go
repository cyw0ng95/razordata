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

const SegSize = int64(64 * 1024 * 1024)

var (
	ErrWriterClosed      = errors.New("wr: writer is closed")
	ErrReadOnly          = errors.New("wr: read-only mode")
	ErrRecordExceedsSeg  = errors.New("wr: single record exceeds SegSize")
	ErrSyncPoolNilBuffer = errors.New("wr: SyncPool returned nil buffer")
	ErrShortPwrite       = errors.New("wr: short pwrite")
)

type LSN = uint64

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

type Checkpoint struct {
	LSN              uint64
	CatalogRootPtr   uint64
	ManifestChecksum uint32
	ActiveTXNs       []uint64
}

type WALMode uint8

const (
	FSYNC_EVERY WALMode = iota
	FSYNC_HEADER_ONLY
	FSYNC_BATCH
)

func (m WALMode) String() string {
	switch m {
	case FSYNC_EVERY:
		return "every"
	case FSYNC_HEADER_ONLY:
		return "header-only"
	case FSYNC_BATCH:
		return "batch"
	default:
		return "unknown"
	}
}

type AsyncSyncResult struct {
	Err       error
	SyncedLSN uint64
}

type Writer interface {
	Append(batch *WriteBatch) (lsn uint64, err error)
	Sync() error
	SyncAsync() (<-chan AsyncSyncResult, error)
	Close() error
}

type logSegment struct {
	number   uint64
	fh       *lf.FileHandle
	writeOff int64
	buf      []byte
}

// cmdType enumerates worker command kinds.
type cmdType uint8

const (
	cmdAppend cmdType = iota
	cmdSync
	cmdSyncAsync
	cmdStop
)

// cmd is a message sent to the background writer goroutine.
type cmd struct {
	typ   cmdType
	batch *WriteBatch
	seq   [][]byte // pre-encoded records for cmdAppend
	res   chan cmdResult
}

type cmdResult struct {
	lsn    uint64
	err    error
	asyncC <-chan AsyncSyncResult
}

type writer struct {
	dir      string
	sm       *lf.SegmentManager
	sp       sp.SyncPool
	log      lg.Logger
	readOnly bool

	closed     atomicBool
	synced     atomic.Uint64
	mode       WALMode
	batchLimit int
	compress   bool

	ch   chan cmd
	wg   sync.WaitGroup
	once sync.Once

	seg *logSegment

	maxRecordSize int64
	lsn           LSNCounter
	batchCount    int

	inflightFsyncs    sync.WaitGroup
	inflightFsyncsCnt atomic.Int64
}

type Options struct {
	Compress   bool
	LSNCounter LSNCounter
	Mode       WALMode
	BatchLimit int
}

type LSNCounter interface {
	Reserve(n int) LSN
}

func New(dir string, sm *lf.SegmentManager, spPool sp.SyncPool, log lg.Logger, readOnly bool) (Writer, error) {
	return NewWithOptions(dir, sm, spPool, log, readOnly, Options{})
}

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
	bl := opts.BatchLimit
	if bl <= 0 {
		bl = 100
	}
	w := &writer{
		dir:        dir,
		sm:         sm,
		sp:         spPool,
		log:        log,
		readOnly:   readOnly,
		compress:   opts.Compress,
		lsn:        opts.LSNCounter,
		mode:       opts.Mode,
		batchLimit: bl,
	}
	w.ch = make(chan cmd, 256)
	w.wg.Add(1)
	go w.loop()
	return w, nil
}

func (w *writer) loop() {
	defer w.wg.Done()
	for c := range w.ch {
		switch c.typ {
		case cmdAppend:
			c.res <- w.handleAppend(c)
		case cmdSync:
			c.res <- w.handleSync(c)
		case cmdSyncAsync:
			c.res <- w.handleSyncAsync(c)
		case cmdStop:
			w.handleStop()
			close(c.res)
			return
		}
	}
}

func (w *writer) send(typ cmdType) cmdResult {
	res := make(chan cmdResult, 1)
	w.ch <- cmd{typ: typ, res: res}
	return <-res
}

func (w *writer) sendAppend(batch *WriteBatch, seq [][]byte) cmdResult {
	res := make(chan cmdResult, 1)
	w.ch <- cmd{typ: cmdAppend, batch: batch, seq: seq, res: res}
	return <-res
}

func (w *writer) Append(batch *WriteBatch) (uint64, error) {
	if batch == nil || len(batch.Recs) == 0 {
		return 0, nil
	}
	if w.closed.isSet() {
		return 0, ErrWriterClosed
	}
	if w.readOnly {
		return 0, ErrReadOnly
	}

	seq := make([][]byte, len(batch.Recs))
	for i := range batch.Recs {
		rec := &batch.Recs[i]
		rec.TxnID = batch.TxnID
		seq[i] = encodeRecordCompressed(rec, w.compress)
	}

	res := w.sendAppend(batch, seq)
	return res.lsn, res.err
}

func (w *writer) Sync() error {
	if w.closed.isSet() {
		return nil
	}
	res := w.send(cmdSync)
	return res.err
}

func (w *writer) SyncAsync() (<-chan AsyncSyncResult, error) {
	if w.closed.isSet() {
		ch := make(chan AsyncSyncResult, 1)
		ch <- AsyncSyncResult{}
		close(ch)
		return ch, nil
	}
	res := make(chan cmdResult, 1)
	w.ch <- cmd{typ: cmdSyncAsync, res: res}
	r := <-res
	return r.asyncC, r.err
}

func (w *writer) Close() error {
	if !w.closed.set() {
		return nil
	}
	res := make(chan cmdResult, 1)
	w.ch <- cmd{typ: cmdStop, res: res}
	<-res
	w.wg.Wait()
	close(w.ch)
	return nil
}

func (w *writer) handleAppend(c cmd) cmdResult {
	if w.seg == nil {
		if err := w.openSegment(); err != nil {
			return cmdResult{err: err}
		}
	}

	if w.lsn != nil {
		w.lsn.Reserve(len(c.seq))
	}

	var lastLSN uint64
	for i, encoded := range c.seq {
		recLen := int64(len(encoded))
		maxRec := w.maxRecordSize
		if maxRec <= 0 {
			maxRec = SegSize
		}
		if recLen > maxRec {
			return cmdResult{lsn: lastLSN, err: ErrRecordExceedsSeg}
		}
		if w.seg.writeOff+recLen > SegSize {
			if err := w.flushBuffer(); err != nil {
				return cmdResult{lsn: lastLSN, err: err}
			}
			if err := w.rotate(); err != nil {
				return cmdResult{lsn: lastLSN, err: err}
			}
		}

		lsn := LSNFor(w.seg.number, uint64(w.seg.writeOff))

		if int64(cap(w.seg.buf))-int64(len(w.seg.buf)) < recLen {
			if err := w.flushBuffer(); err != nil {
				return cmdResult{lsn: lastLSN, err: err}
			}
		}

		w.seg.buf = append(w.seg.buf, encoded...)
		w.seg.writeOff += recLen
		lastLSN = lsn
		_ = i
	}

	return cmdResult{lsn: lastLSN}
}

func (w *writer) handleSync(c cmd) cmdResult {
	return cmdResult{err: w.syncInternal()}
}

func (w *writer) handleSyncAsync(c cmd) cmdResult {
	if w.seg == nil || len(w.seg.buf) == 0 {
		ch := make(chan AsyncSyncResult, 1)
		ch <- AsyncSyncResult{SyncedLSN: w.synced.Load()}
		close(ch)
		return cmdResult{asyncC: ch}
	}

	pendingEnd := w.seg.writeOff
	segNumber := w.seg.number
	fd := w.seg.fh.FD
	mode := w.mode
	bl := w.batchLimit

	if err := w.flushBuffer(); err != nil {
		ch := make(chan AsyncSyncResult, 1)
		ch <- AsyncSyncResult{Err: err}
		close(ch)
		return cmdResult{asyncC: ch}
	}

	switch mode {
	case FSYNC_HEADER_ONLY:
		ch := make(chan AsyncSyncResult, 1)
		syncedLSN := LSNFor(segNumber, uint64(pendingEnd))
		for {
			old := w.synced.Load()
			if syncedLSN <= old || w.synced.CompareAndSwap(old, syncedLSN) {
				break
			}
		}
		ch <- AsyncSyncResult{SyncedLSN: syncedLSN}
		close(ch)
		return cmdResult{asyncC: ch}

	case FSYNC_BATCH:
		w.batchCount++
		if w.batchCount < bl {
			ch := make(chan AsyncSyncResult, 1)
			syncedLSN := LSNFor(segNumber, uint64(pendingEnd))
			for {
				old := w.synced.Load()
				if syncedLSN <= old || w.synced.CompareAndSwap(old, syncedLSN) {
					break
				}
			}
			ch <- AsyncSyncResult{SyncedLSN: syncedLSN}
			close(ch)
			return cmdResult{asyncC: ch}
		}
		w.batchCount = 0
	}

	ch := make(chan AsyncSyncResult, 1)
	w.inflightFsyncs.Add(1)
	w.inflightFsyncsCnt.Add(1)

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
			for {
				old := w.synced.Load()
				if syncedLSN <= old || w.synced.CompareAndSwap(old, syncedLSN) {
					break
				}
			}
		}
		ch <- AsyncSyncResult{Err: err, SyncedLSN: syncedLSN}
		close(ch)
	}()

	return cmdResult{asyncC: ch}
}

func (w *writer) handleStop() {
	if w.seg == nil {
		return
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
		w.inflightFsyncs.Wait()
	}
	hadBuffer := len(w.seg.buf) > 0
	pendingEnd := w.seg.writeOff
	if hadBuffer {
		recordErr("flush", w.flushBuffer())
	}
	if pendingEnd > int64(WALHeaderSize) {
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
	_ = firstErr
}

func (w *writer) syncInternal() error {
	if w.seg == nil {
		return nil
	}
	if len(w.seg.buf) == 0 {
		return nil
	}
	pendingEnd := w.seg.writeOff
	if err := w.flushBuffer(); err != nil {
		return err
	}

	switch w.mode {
	case FSYNC_HEADER_ONLY:
	case FSYNC_BATCH:
		w.batchCount++
		if w.batchCount >= w.batchLimit {
			w.batchCount = 0
			if err := unix.Fsync(w.seg.fh.FD); err != nil {
				if w.log != nil {
					w.log.Error("wr.sync", "seg", w.seg.number, "err", err)
				}
				return err
			}
		}
	default:
		if err := unix.Fsync(w.seg.fh.FD); err != nil {
			if w.log != nil {
				w.log.Error("wr.sync", "seg", w.seg.number, "err", err)
			}
			return err
		}
	}

	syncedLSN := LSNFor(w.seg.number, uint64(pendingEnd))
	for {
		old := w.synced.Load()
		if syncedLSN <= old || w.synced.CompareAndSwap(old, syncedLSN) {
			break
		}
	}
	return nil
}

func (w *writer) openSegment() error {
	n := uint64(0)
	if w.seg != nil {
		n = w.seg.number + 1
	}
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
		return ErrSyncPoolNilBuffer
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
		buf:      buf[:0],
	}
	return nil
}

func (w *writer) flushBuffer() error {
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
		return ErrShortPwrite
	}
	w.seg.buf = w.seg.buf[:0]
	return nil
}

func (w *writer) rotate() error {
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
	return w.openSegment()
}

var _ Writer = (*writer)(nil)

func (w *writer) flushForTest() error {
	if w.closed.isSet() {
		return nil
	}
	res := w.send(cmdSync)
	return res.err
}
