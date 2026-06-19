// Package rp implements the WAL Replayer cluster.
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
	ErrCorrupt         = errors.New("rp: WAL segment is corrupt")
)

// Stats exposes replay counters (R13-10).
type Stats struct {
	TruncatedSegments  uint64
	UnknownRecords     uint64
	CorruptionFailures uint64
}

const rpSegSize = int64(64 * 1024 * 1024)

// Callbacks groups hooks invoked during replay (R36).
type Callbacks struct {
	OnData     func(blockID uint64, data []byte) error
	OnCommit   func(txnID uint64, commitTS uint64) error
	OnRollback func(txnID uint64) error
}

type CheckpointResult = wr.Checkpoint

// Replayer reconstructs state from the WAL on startup (R11).
type Replayer interface {
	Replay() error
	Stats() Stats
	LastCheckpoint() (*CheckpointResult, error)
	Close() error
}

type replayer struct {
	dir        string
	sm         *lf.SegmentManager
	stats      Stats
	bp         bf.BufferPool
	cb         Callbacks
	log        lg.Logger
	compressed bool // REQ000034: lz4-compressed segment flag

	closed atomicBool
}

// New constructs a Replayer (R35).
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

// Replay implements Replayer.
func (r *replayer) Replay() error {
	if r.closed.isSet() {
		return errors.New("rp: replayer is closed")
	}
	r.stats = Stats{}

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

	headerBuf := make([]byte, wr.WALHeaderSize)
	hn, err := unix.Pread(fd, headerBuf, 0)
	if err != nil {
		return fmt.Errorf("rp: read header of segment %d: %w", segNum, err)
	}
	if hn < wr.WALHeaderSize {
		r.stats.CorruptionFailures++
		return fmt.Errorf("rp: segment %d shorter than header: %w",
			segNum, ErrCorrupt)
	}
	if err := wr.ValidateSegmentHeader(headerBuf); err != nil {
		r.stats.CorruptionFailures++
		if errors.Is(err, wr.ErrCorrupt) {
			return fmt.Errorf("rp: segment %d header: %w", segNum, ErrCorrupt)
		}
		return fmt.Errorf("rp: segment %d header: %w", segNum, err)
	}

	r.compressed = headerBuf[5]&wr.FlagCompressionLZ4 != 0
	offset := int64(wr.WALHeaderSize)
	remaining := fileSize - offset
	if remaining <= 0 {
		return nil
	}

	buf := make([]byte, remaining)
	n, err := unix.Pread(fd, buf, offset)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("rp: read segment %d offset %d: %w", segNum, offset, err)
	}
	if n == 0 {
		return nil
	}
	if int64(n) < remaining {
		// Short read: trim buf to the actual read length.
		buf = buf[:n]
	}

	off := 0
	for off < len(buf) {
		rec, consumed, err := wr.DecodeRecordCompressed(buf, off, r.compressed)
		if err != nil {
			if errors.Is(err, wr.ErrTruncatedRecord) {
				r.stats.TruncatedSegments++
				return nil
			}
			if errors.Is(err, wr.ErrUnknownRecord) {
				r.stats.TruncatedSegments++
				return nil
			}
			if errors.Is(err, wr.ErrCorrupt) {
				r.stats.CorruptionFailures++
				return fmt.Errorf("rp: segment %d offset %d: %w",
					segNum, offset+int64(off), ErrCorrupt)
			}
			if rec.PayCRCFail {
				r.stats.CorruptionFailures++
				return fmt.Errorf("rp: segment %d inner RTData CRC: %w",
					segNum, ErrCorrupt)
			}
			_ = rec
			resyncWindow := int64(wr.MaxRecordLen)
			if resyncWindow > int64(len(buf)-off) {
				resyncWindow = int64(len(buf) - off)
			}
			if off+int(resyncWindow) >= len(buf) {
				// No room to resync within this
				// segment; bail.
				return fmt.Errorf("rp: segment %d offset %d: %w",
					segNum, offset+int64(off), ErrCorrupt)
			}
			off++
			continue
		}
		if rec.PayCRCFail {
			r.stats.CorruptionFailures++
			return fmt.Errorf("rp: segment %d inner RTData CRC: %w",
				segNum, ErrCorrupt)
		}
		if rec.Type == 0xFF {
			r.stats.UnknownRecords++
			off += consumed
			continue
		}

		recLSN := segNum*uint64(rpSegSize) + uint64(offset) + uint64(off)
		if err := fn(rec, recLSN); err != nil {
			return err
		}

		off += consumed
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

// LastCheckpoint implements Replayer.
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

func (r *replayer) Close() error {
	if !r.closed.set() {
		return nil
	}
	return nil
}

func (r *replayer) Stats() Stats {
	return r.stats
}

var _ Replayer = (*replayer)(nil)
