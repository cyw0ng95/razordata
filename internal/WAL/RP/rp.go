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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"github.com/cyw0ng95/razordata/internal/MEM/BF"
	"github.com/cyw0ng95/razordata/internal/WAL/WR"
	"golang.org/x/sys/unix"
)

var (
	ErrNoCheckpoint    = errors.New("rp: no checkpoint found in WAL")
	ErrReplayAborted   = errors.New("rp: replay aborted by callback")
	ErrSegmentNotFound = errors.New("rp: segment not found during replay")
)

const rpSegSize = int64(64 * 1024 * 1024)

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

// Replay implements Replayer (R11, R12, R13, R16-R19, R27).
func (r *replayer) Replay() error {
	if r.closed.isSet() {
		return errors.New("rp: replayer is closed")
	}

	segments, err := r.sm.ListSegments()
	if err != nil {
		if r.log != nil {
			r.log.Error("rp.replay", "err", err)
		}
		return err
	}

	if len(segments) == 0 {
		return nil
	}

	cp, err := r.LastCheckpoint()
	if err != nil && !errors.Is(err, ErrNoCheckpoint) {
		if r.log != nil {
			r.log.Error("rp.replay", "err", err)
		}
		return err
	}

	var startLSN uint64
	if cp != nil {
		startLSN = cp.LSN
		if r.log != nil {
			r.log.Info("rp.replay", "checkpoint_lsn", startLSN)
		}
	}

	segsToScan := segments
	if cp != nil {
		segNum := startLSN / uint64(rpSegSize)
		found := false
		for i, s := range segments {
			if s >= segNum {
				segsToScan = segments[i:]
				found = true
				break
			}
		}
		if !found {
			segsToScan = nil
		}
	}

	for _, segNum := range segsToScan {
		if err := r.replaySegment(segNum, startLSN); err != nil {
			if r.log != nil {
				r.log.Error("rp.replay", "segment", segNum, "err", err)
			}
			return err
		}
	}

	if cp != nil {
		r.truncateBeforeCheckpoint(cp.LSN)
	}

	return nil
}

func (r *replayer) replaySegment(segNum uint64, minLSN uint64) error {
	return r.forEachRecord(segNum, func(rec *wr.LogRecord, recLSN uint64) error {
		if recLSN < minLSN {
			return nil
		}
		return r.applyRecord(rec)
	})
}

// forEachRecord reads every record in segment segNum and invokes fn
// for each successfully decoded one. The LSN passed to fn is the
// record's location in the segment (used by replaySegment to filter
// below minLSN). fn returning an error short-circuits the iteration
// and surfaces that error to the caller. Truncated or unknown
// records at the tail are skipped so a torn write or an unfamiliar
// record type does not abort replay.
func (r *replayer) forEachRecord(segNum uint64, fn func(rec *wr.LogRecord, recLSN uint64) error) error {
	fh, err := r.sm.GetSegment(segNum)
	if err != nil {
		return fmt.Errorf("rp: GetSegment(%d): %w", segNum, err)
	}
	defer fh.Close()

	fd, err := unix.Open(fh.Path, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("rp: open segment %d: %w", segNum, err)
	}
	defer unix.Close(fd)

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("rp: fstat segment %d: %w", segNum, err)
	}

	fileSize := stat.Size
	if fileSize == 0 {
		return nil
	}

	buf := make([]byte, 64*1024)
	offset := int64(0)

	for offset < fileSize {
		readN := int64(len(buf))
		if offset+readN > fileSize {
			readN = fileSize - offset
		}

		n, err := unix.Pread(fd, buf[:readN], offset)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("rp: read segment %d offset %d: %w", segNum, offset, err)
		}
		if n == 0 {
			break
		}

		off := 0
		for off < n {
			rec, consumed, err := wr.DecodeRecord(buf[:n], off)
			if err != nil {
				if errors.Is(err, wr.ErrTruncatedRecord) {
					break
				}
				off++
				continue
			}

			recLSN := segNum*uint64(rpSegSize) + uint64(offset) + uint64(consumed)
			if err := fn(rec, recLSN); err != nil {
				return err
			}

			off += consumed
			offset += int64(consumed)
		}

		if off > 0 {
			offset += int64(off)
		}
		if offset >= fileSize {
			break
		}
	}

	return nil
}

func (r *replayer) applyRecord(rec *wr.LogRecord) error {
	if rec == nil {
		return nil
	}

	switch rec.Type {
	case wr.RTData:
		if len(rec.Value) > 0 {
			if err := r.bp.Upsert(&bf.Page{ID: rec.BlockID, Data: rec.Value}); err != nil {
				if r.log != nil {
					r.log.Warn("rp.apply", "blockID", rec.BlockID, "err", err)
				}
			}
		}
		if r.cb.OnData != nil {
			return r.cb.OnData(rec.BlockID, rec.Value)
		}

	case wr.RTCommit:
		if r.cb.OnCommit != nil {
			return r.cb.OnCommit(rec.TxnID, rec.BlockID)
		}

	case wr.RTRollback:
		if r.cb.OnRollback != nil {
			return r.cb.OnRollback(rec.TxnID)
		}
	}

	return nil
}

// LastCheckpoint implements Replayer (R11, R13).
func (r *replayer) LastCheckpoint() (*CheckpointResult, error) {
	if r.closed.isSet() {
		return nil, errors.New("rp: replayer is closed")
	}

	segments, err := r.sm.ListSegments()
	if err != nil {
		return nil, err
	}

	if len(segments) == 0 {
		return nil, ErrNoCheckpoint
	}

	for i := len(segments) - 1; i >= 0; i-- {
		cp, err := r.findCheckpointInSegment(segments[i])
		if err == nil && cp != nil {
			return cp, nil
		}
		if errors.Is(err, ErrNoCheckpoint) {
			continue
		}
		if err != nil {
			return nil, err
		}
	}

	return nil, ErrNoCheckpoint
}

func (r *replayer) findCheckpointInSegment(segNum uint64) (*CheckpointResult, error) {
	var lastCheckpoint *wr.Checkpoint
	err := r.forEachRecord(segNum, func(rec *wr.LogRecord, _ uint64) error {
		if rec.Type == wr.RTCheckpoint {
			lastCheckpoint = r.decodeCheckpoint(rec)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if lastCheckpoint == nil {
		return nil, ErrNoCheckpoint
	}
	return lastCheckpoint, nil
}

func (r *replayer) decodeCheckpoint(rec *wr.LogRecord) *wr.Checkpoint {
	if rec == nil || rec.Type != wr.RTCheckpoint {
		return nil
	}

	cp := &wr.Checkpoint{}

	if len(rec.Key) >= 24 {
		cp.LSN = binary.LittleEndian.Uint64(rec.Key[0:8])
		cp.CatalogRootPtr = binary.LittleEndian.Uint64(rec.Key[8:16])
		cp.ManifestChecksum = binary.LittleEndian.Uint32(rec.Key[16:20])
	}

	txnCount := rec.BlockID

	if txnCount > 0 && len(rec.Value) > 0 {
		cp.ActiveTXNs = make([]uint64, 0, txnCount)
		off := 0
		for i := uint64(0); i < txnCount && off < len(rec.Value); i++ {
			v, n := wr.DecodeVarint(rec.Value, off)
			if n < 0 {
				break
			}
			cp.ActiveTXNs = append(cp.ActiveTXNs, v)
			off += n
		}
	}

	return cp
}

func (r *replayer) truncateBeforeCheckpoint(cpLSN uint64) {
	segNum := cpLSN / uint64(rpSegSize)

	segments, err := r.sm.ListSegments()
	if err != nil {
		if r.log != nil {
			r.log.Warn("rp.truncate", "list_err", err)
		}
		return
	}

	truncateSize := cpLSN % uint64(rpSegSize)
	if truncateSize == 0 && segNum > 0 {
		truncateSize = uint64(rpSegSize)
		segNum--
	}

	for _, s := range segments {
		if int64(s) < int64(segNum) {
			if err := r.sm.Truncate(s, 0); err != nil {
				if r.log != nil {
					r.log.Warn("rp.truncate", "segment", s, "err", err)
				}
			} else if r.log != nil {
				r.log.Info("rp.truncate", "segment", s, "size", 0)
			}
		} else if int64(s) == int64(segNum) && truncateSize > 0 {
			if err := r.sm.Truncate(s, int64(truncateSize)); err != nil {
				if r.log != nil {
					r.log.Warn("rp.truncate", "segment", s, "size", truncateSize, "err", err)
				}
			} else if r.log != nil {
				r.log.Info("rp.truncate", "segment", s, "size", truncateSize)
			}
		}
	}
}

// Close marks the replayer as closed. Idempotent.
func (r *replayer) Close() error {
	if !r.closed.set() {
		return nil
	}
	return nil
}

var _ Replayer = (*replayer)(nil)
