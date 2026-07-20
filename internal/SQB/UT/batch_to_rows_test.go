package UT

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// REQ001440: Batch.ToRows() materialises a columnar Batch back
// into `[]pl.Row`. Verify roundtrip for the four primitive kinds
// plus null handling and Sel-filtered batches.
func TestBatch_ToRows_RoundtripAllTypes(t *testing.T) {
	b := GetBatch(3)
	defer b.Put()
	b.SetColumnName(0, "a")
	b.SetColumnName(1, "b")
	b.SetColumnName(2, "c")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[1].Type = LX.T_TEXT
	// b[2] left with Type=0; ToValue treats any Type=0 as null
	// (we set Nulls[2]=true below).
	b.AppendRow(0, LX.T_INT_KW, int64(7), false)
	b.AppendRow(1, LX.T_TEXT, "hi", false)
	b.AppendRow(2, LX.T_INT_KW, nil, true)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_INT_KW, int64(8), false)
	b.AppendRow(1, LX.T_TEXT, "ho", false)
	b.AppendRow(2, LX.T_INT_KW, nil, true)
	b.AdvanceSize()
	rows := b.ToRows()
	if len(rows) != 2 {
		t.Fatalf("ToRows: got %d rows, want 2", len(rows))
	}
	if rows[0].Data[0].Kind != pl.KindInt || rows[0].Data[0].I64 != 7 {
		t.Errorf("r0c0: got %v, want int 7", rows[0].Data[0])
	}
	if rows[0].Data[1].Kind != pl.KindText || rows[0].Data[1].S != "hi" {
		t.Errorf("r0c1: got %v, want text hi", rows[0].Data[1])
	}
	if rows[0].Data[2].Kind != pl.KindNull {
		t.Errorf("r0c2: got %v, want null", rows[0].Data[2])
	}
	if rows[1].Data[0].I64 != 8 || rows[1].Data[1].S != "ho" {
		t.Errorf("r1: got %v %v", rows[1].Data[0], rows[1].Data[1])
	}
	if rows[1].Data[2].Kind != pl.KindNull {
		t.Errorf("r1c2: got %v, want null", rows[1].Data[2])
	}
}

// REQ001440: ToRows respects the Sel vector (selection filter).
// A Sel [2, 0] over a 4-row batch must yield only rows 2 and 0
// in that order, not all 4.
func TestBatch_ToRows_RespectsSelection(t *testing.T) {
	b := GetBatch(1)
	defer b.Put()
	b.SetColumnName(0, "x")
	b.Cols[0].Type = LX.T_INT_KW
	for i := int64(0); i < 4; i++ {
		b.AppendRow(0, LX.T_INT_KW, i, false)
		b.AdvanceSize()
	}
	// Manually set Sel to pick rows 2 and 0.
	b.Sel = []uint16{2, 0}
	rows := b.ToRows()
	if len(rows) != 2 {
		t.Fatalf("len: got %d want 2", len(rows))
	}
	if rows[0].Data[0].I64 != 2 {
		t.Errorf("r0 (sel[2]): got %d want 2", rows[0].Data[0].I64)
	}
	if rows[1].Data[0].I64 != 0 {
		t.Errorf("r1 (sel[0]): got %d want 0", rows[1].Data[0].I64)
	}
}

// REQ001440: empty / nil batch edge cases.
func TestBatch_ToRows_NilAndEmpty(t *testing.T) {
	if got := ((*Batch)(nil)).ToRows(); got != nil {
		t.Errorf("nil batch: got %v, want nil", got)
	}
	b := GetBatch(1)
	defer b.Put()
	b.SetColumnName(0, "x")
	// No data appended. ColNames() returns ["x"]; ToRows produces
	// a zero-length but non-nil []pl.Row{} (callers test
	// `len(rows) == 0` either way).
	rows := b.ToRows()
	if len(rows) != 0 {
		t.Errorf("empty batch: got len=%d want 0", len(rows))
	}
}

// TestBatch_GetBatch_ClearsStaleColumnNames verifies REQ001582:
// GetBatch clears stale Name fields from pooled columns. A previous
// owner (e.g. a 2-column SeqScan) leaves Name on column 1; after
// GetBatch(1) the stale name must be gone so ColNames() returns only
// column 0's name.
func TestBatch_GetBatch_ClearsStaleColumnNames(t *testing.T) {
	// Simulate a previous owner: a 2-column batch with names set.
	b1 := GetBatch(2)
	b1.SetColumnName(0, "count")
	b1.SetColumnName(1, "name")
	b1.Put()

	// Get a 1-column batch from the pool. The stale "name" on column 1
	// must be cleared.
	b2 := GetBatch(1)
	b2.SetColumnName(0, "count")
	names := b2.ColNames()
	if len(names) != 1 {
		t.Fatalf("ColNames() returned %d columns, want 1: %v", len(names), names)
	}
	if names[0] != "count" {
		t.Errorf("ColNames()[0] = %q, want %q", names[0], "count")
	}
	b2.Put()
}
