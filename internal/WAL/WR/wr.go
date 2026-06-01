// Package wr implements the WAL Writer cluster.
//
// Foundation types and the Writer interface are declared here. The full
// implementation (sequential append, LSN allocation, segment rotation,
// record encoding) lands in subsequent commits.
package wr

import (
	"errors"

	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"github.com/cyw0ng95/razordata/internal/MEM/SP"
)

// SegSize is the maximum size of a single WAL segment (R01, R06).
// When writeOff reaches SegSize, the current segment is closed and a
// new one is created.
const SegSize = int64(64 * 1024 * 1024) // 64 MB

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
//	┌──────────────┬──────────┬─────────────┬──────────────────┐
//	│ length:varint│ txnID:varint│ type:uint8 │ payload:blob     │
//	└──────────────┴──────────┴─────────────┴──────────────────┘
//
// `length` covers everything after the length field itself.
type LogRecord struct {
	Type    RecordType
	TxnID   uint64
	Key     []byte
	Value   []byte
	BlockID uint64 // SST/manifest block this record modifies (RTData only)
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

// writer is the concrete Writer implementation. The struct is
// intentionally minimal in this Foundation commit — the full
// implementation lands in the Core WR commit.
type writer struct {
	dir string
	sm  *lf.SegmentManager
	sp  sp.SyncPool
	log lg.Logger

	closed atomicBool
}

// New constructs a Writer rooted at dir. The full implementation
// (segment handle, write buffer allocation, LSN counter wiring) lands
// in the Core WR commit. The Foundation stub returns an error so
// callers see a clear signal that the implementation is not yet wired.
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

// Append stub. Returns an error until the Core WR commit wires the
// in-memory buffer, segment write, and LSN allocation.
func (w *writer) Append(batch *WriteBatch) (uint64, error) {
	if w.closed.isSet() {
		return 0, errors.New("wr: writer is closed")
	}
	return 0, errors.New("wr: Append not yet implemented")
}

// Sync stub.
func (w *writer) Sync() error {
	if w.closed.isSet() {
		return nil
	}
	return errors.New("wr: Sync not yet implemented")
}

// Close stub. Marks the writer as closed so subsequent Append/Sync
// calls fail predictably.
func (w *writer) Close() error {
	if !w.closed.set() {
		return nil // already closed
	}
	return nil
}

var _ Writer = (*writer)(nil)
