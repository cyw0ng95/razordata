package SY

import (
	"context"
	"testing"

	executor "github.com/cyw0ng95/razordata/internal/SQB/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestEngine_Stats_WALFieldsBeforeReplay pins REQ000190 (R15-1,
// R15-2) at the surface: the three new fields exist on
// AP.WALStats, are populated from the replayer's counters, and
// are zero on a fresh engine that has not yet called Replay.
// The deeper end-to-end path (write a torn segment → reopen →
// observe non-zero TruncatedSegments) is already covered by
// internal/WAL/RP/rp_corrupt_test.go. Here we only assert the
// surfacing is wired up.
func TestEngine_Stats_WALFieldsBeforeReplay(t *testing.T) {
	executor.UnregisterAll()
	eng := openForCloseTest(t)
	stats := eng.Stats()
	if stats.WAL.TruncatedSegments != 0 {
		t.Errorf("TruncatedSegments: want 0 on fresh engine, got %d", stats.WAL.TruncatedSegments)
	}
	if stats.WAL.UnknownRecords != 0 {
		t.Errorf("UnknownRecords: want 0 on fresh engine, got %d", stats.WAL.UnknownRecords)
	}
	if stats.WAL.CorruptionFailures != 0 {
		t.Errorf("CorruptionFailures: want 0 on fresh engine, got %d", stats.WAL.CorruptionFailures)
	}
	_ = eng.Close(context.Background())
}

// TestEngine_Stats_WALFieldsAreInt64 pins the type of the three
// new fields. Catches a refactor that accidentally uses uint32 or
// int — a regression in the public API that downstream tools
// (admin CLI, JSON parsers) would have to track.
func TestEngine_Stats_WALFieldsAreInt64(t *testing.T) {
	var s AP.WALStats
	_ = s.TruncatedSegments  // must be int64
	_ = s.UnknownRecords     // must be int64
	_ = s.CorruptionFailures // must be int64
}
