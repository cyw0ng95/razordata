// Package rp implements the WAL Replayer cluster.
//
// The Replayer scans segments, finds the last checkpoint, and replays
// records in LSN order to rebuild in-memory state (memtable, active
// transaction set). Foundation declares the interface, the Callbacks
// struct, and the replayer internal state. The full implementation
// (segment iteration, record replay, checkpoint detection, truncation)
// lands in the Core RP commit.
package rp

import (
	"errors"

	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"github.com/cyw0ng95/razordata/internal/MEM/BF"
	"github.com/cyw0ng95/razordata/internal/WAL/WR"
)

// Callbacks groups the three hooks a Replayer invokes for each record
// type during replay (R36). Zero-value fields are no-ops (the replayer
// silently skips them). All hooks are called serially in the replay
// goroutine — callers that need concurrency should dispatch from
// inside the hook.
type Callbacks struct {
	// OnData is invoked for each RTData record. The data bytes are
	// the full recovered page image. The replayer injects the page
	// into the buffer pool via bp.Upsert before calling this hook,
	// so the hook's primary role is to update the memtable (in
	// iter-04/05). Returning an error aborts replay.
	OnData func(blockID uint64, data []byte) error
	// OnCommit is invoked for each RTCommit record.
	OnCommit func(txnID uint64, commitTS uint64) error
	// OnRollback is invoked for each RTRollback record.
	OnRollback func(txnID uint64) error
}

// CheckpointResult is what LastCheckpoint returns (R11).
type CheckpointResult = wr.Checkpoint

// Replayer reconstructs memtable and TXN state from the WAL on startup
// (R11).
type Replayer interface {
	// Replay walks the WAL segments in LSN order, starting from the
	// last checkpoint (or from segment 0 if no checkpoint exists),
	// and invokes the configured Callbacks for each record. After
	// replay, segments before the checkpoint are truncated. Returns
	// nil on success, including the no-segments case (R28).
	Replay() error
	// LastCheckpoint returns the most recent Checkpoint found in the
	// WAL, or (nil, nil) if no checkpoint exists.
	LastCheckpoint() (*CheckpointResult, error)
	// Close releases resources. Idempotent.
	Close() error
}

// replayer is the concrete Replayer implementation. Foundation holds
// the wiring; the actual scan/replay logic lands in the Core RP
// commit.
type replayer struct {
	dir string
	sm  *lf.SegmentManager
	bp  bf.BufferPool
	cb  Callbacks
	log lg.Logger

	closed atomicBool
}

// New constructs a Replayer (R35). cb's zero-value fields are no-ops
// (R36). The Foundation stub returns an error if any required
// dependency is missing.
func New(dir string, sm *lf.SegmentManager, bp bf.BufferPool, cb Callbacks, log lg.Logger) (Replayer, error) {
	if dir == "" {
		return nil, errors.New("rp: dir is required")
	}
	if sm == nil {
		return nil, errors.New("rp: SegmentManager is required")
	}
	if bp == nil {
		return nil, errors.New("rp: BufferPool is required")
	}
	return &replayer{dir: dir, sm: sm, bp: bp, cb: cb, log: log}, nil
}

// Replay stub.
func (r *replayer) Replay() error {
	if r.closed.isSet() {
		return errors.New("rp: replayer is closed")
	}
	return errors.New("rp: Replay not yet implemented")
}

// LastCheckpoint stub.
func (r *replayer) LastCheckpoint() (*CheckpointResult, error) {
	if r.closed.isSet() {
		return nil, errors.New("rp: replayer is closed")
	}
	return nil, errors.New("rp: LastCheckpoint not yet implemented")
}

// Close marks the replayer as closed. Idempotent.
func (r *replayer) Close() error {
	if !r.closed.set() {
		return nil
	}
	return nil
}

var _ Replayer = (*replayer)(nil)
