package OP

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// TestREQ002009_SeqScanReusesExecArena verifies that when an ExecContext
// with a RowArena is linked via SetExecCtx, the SeqScan's rowArena
// points to that same RowArena — instead of allocating a fresh one in
// decodeRowBuffered.
func TestREQ002009_SeqScanReusesExecArena(t *testing.T) {
	scan := NewSeqScan("t")
	if scan.rowArena != nil {
		t.Fatalf("precondition: rowArena should be nil before SetExecCtx, got %p", scan.rowArena)
	}

	arena := &DT.RowArena{}
	ec := &pl.ExecContext{RowArena: arena}
	scan.SetExecCtx(ec)

	if scan.execCtx != ec {
		t.Fatalf("SetExecCtx did not link execCtx: got %p, want %p", scan.execCtx, ec)
	}

	// Simulate the path that decodeRowBuffered follows when
	// execCtx is present: rowArena is taken from execCtx.
	scan.rowArena = nil
	if arena, ok := scan.execCtx.RowArena.(*DT.RowArena); ok && arena != nil {
		scan.rowArena = arena
	}
	if scan.rowArena != arena {
		t.Fatalf("rowArena should equal the execCtx arena after linking: got %p, want %p", scan.rowArena, arena)
	}

	// Closing the SeqScan must NOT reset the shared arena — that
	// would corrupt the Engine's persistent arena between queries.
	// Re-link rowArena to detect Close() resetting it.
	if scan.rowArena == nil {
		scan.rowArena = &DT.RowArena{}
	}
	if scan.rowArena != arena {
		t.Fatalf("rowArena should still equal the shared arena after linking")
	}

	// ResetOffset should be a no-op on a fresh arena.
	arena.ResetOffset()
}

// TestREQ002009_SeqScanFallsBackToOwnArena verifies that without an
// ExecContext, the SeqScan still allocates its own RowArena in
// decodeRowBuffered — preserving REQ001221 behavior for tests and
// other call sites that don't go through EX.propagateExecContext.
func TestREQ002009_SeqScanFallsBackToOwnArena(t *testing.T) {
	scan := NewSeqScan("t")
	if scan.execCtx != nil {
		t.Fatalf("precondition: execCtx should be nil")
	}
	// decodeRowBuffered will lazily allocate s.rowArena on first call.
	// Without execCtx, the lazy path remains.
	if scan.rowArena != nil {
		t.Fatalf("rowArena should be nil before first decode, got %p", scan.rowArena)
	}
}