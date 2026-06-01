// Package fl implements the WAL Flusher cluster.
//
// The Flusher owns fsync semantics: per-segment fdatasync, batch
// coordination, and the WAL directory fsync. Foundation declares the
// interface and the LSN counter. The full Sync/BatchSync/SyncDir
// implementation lands in the Core FL commit.
package fl

import (
	"errors"

	"github.com/cyw0ng95/razordata/internal/FIL/FS"
	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
)

// Flusher batches and persists WAL records to disk (R08).
type Flusher interface {
	// Sync flushes the in-memory write buffer to the segment FD,
	// fsyncs the segment, and updates the synced LSN. After Sync
	// returns, all prior Append calls are durable on disk.
	Sync() error
	// BatchSync waits for any in-flight Append calls to complete and
	// performs a single fsync across all pending writes. Useful for
	// group commit at the transaction boundary.
	BatchSync() error
	// SyncDir fsyncs the WAL directory to ensure segment directory
	// entries are durable. Must be called after any segment
	// create/close. Idempotent.
	SyncDir() error
	// Close releases resources. Idempotent.
	Close() error
}

// flusher is the concrete Flusher implementation. The Foundation
// version holds the wiring; the actual fsync calls land in the Core
// FL commit.
type flusher struct {
	sm  *lf.SegmentManager
	fm  *fs.FileManager
	lsn *lsnCounter
	log lg.Logger

	closed atomicBool
}

// New constructs a Flusher (R35). The Foundation stub returns an
// error if any required dependency is missing.
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
	return &flusher{sm: sm, fm: fm, lsn: newLSNCounter(), log: log}, nil
}

// Sync stub.
func (f *flusher) Sync() error {
	if f.closed.isSet() {
		return nil
	}
	return errors.New("fl: Sync not yet implemented")
}

// BatchSync stub.
func (f *flusher) BatchSync() error {
	if f.closed.isSet() {
		return nil
	}
	return errors.New("fl: BatchSync not yet implemented")
}

// SyncDir stub.
func (f *flusher) SyncDir() error {
	if f.closed.isSet() {
		return nil
	}
	return errors.New("fl: SyncDir not yet implemented")
}

// Close marks the flusher as closed. Idempotent (R22).
func (f *flusher) Close() error {
	if !f.closed.set() {
		return nil
	}
	return nil
}

var _ Flusher = (*flusher)(nil)
