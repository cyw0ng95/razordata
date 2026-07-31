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

// REQ002235: RowsToBatch converts a []pl.Row back into a columnar
// Batch — the inverse of ToRows. Roundtrip all four primitive kinds,
// nulls, and type inference from value Kind when Row.Types is empty.
func TestRowsToBatch_RoundtripAllTypes(t *testing.T) {
	rows := []pl.Row{
		{
			Cols: []string{"a", "b", "c", "d"},
			Data: []pl.Value{
				{Kind: pl.KindInt, I64: 7},
				{Kind: pl.KindText, S: "hi"},
				{Kind: pl.KindFloat, F64: 1.5},
				{Kind: pl.KindNull},
			},
		},
		{
			Cols: []string{"a", "b", "c", "d"},
			Data: []pl.Value{
				{Kind: pl.KindInt, I64: 8},
				{Kind: pl.KindText, S: "ho"},
				{Kind: pl.KindFloat, F64: 2.5},
				{Kind: pl.KindNull},
			},
		},
	}
	b := RowsToBatch(rows)
	if b == nil {
		t.Fatal("RowsToBatch returned nil batch")
	}
	defer b.Put()
	if b.Size != 2 {
		t.Fatalf("Size: got %d want 2", b.Size)
	}
	if b.Cols[0].Name != "a" || b.Cols[1].Name != "b" || b.Cols[2].Name != "c" || b.Cols[3].Name != "d" {
		t.Errorf("column names: got %q %q %q %q, want a b c d",
			b.Cols[0].Name, b.Cols[1].Name, b.Cols[2].Name, b.Cols[3].Name)
	}
	if got := b.Value(0, 0); got != int64(7) {
		t.Errorf("r0c0: got %v (%T) want int64(7)", got, got)
	}
	if got := b.Value(1, 0); got != "hi" {
		t.Errorf("r0c1: got %v want hi", got)
	}
	if got := b.Value(2, 0); got != 1.5 {
		t.Errorf("r0c2: got %v want 1.5", got)
	}
	if got := b.Value(3, 0); got != nil {
		t.Errorf("r0c3: got %v want nil", got)
	}
	if got := b.Value(0, 1); got != int64(8) {
		t.Errorf("r1c0: got %v want 8", got)
	}
	if got := b.Value(1, 1); got != "ho" {
		t.Errorf("r1c1: got %v want ho", got)
	}
	if got := b.Value(2, 1); got != 2.5 {
		t.Errorf("r1c2: got %v want 2.5", got)
	}
	if got := b.Value(3, 1); got != nil {
		t.Errorf("r1c3: got %v want nil", got)
	}
	// Type inference: int→BIGINT, text→TEXT, float→FLOAT_KW.
	if b.Cols[0].Type != LX.T_BIGINT {
		t.Errorf("col0 type: got %v want BIGINT", b.Cols[0].Type)
	}
	if b.Cols[1].Type != LX.T_TEXT {
		t.Errorf("col1 type: got %v want TEXT", b.Cols[1].Type)
	}
	if b.Cols[2].Type != LX.T_FLOAT_KW {
		t.Errorf("col2 type: got %v want FLOAT_KW", b.Cols[2].Type)
	}
}

// REQ002235: RowsToBatch roundtrip — batch → ToRows → RowsToBatch
// preserves values for all primitive kinds (compositional inverse).
func TestRowsToBatch_RoundtripToRows(t *testing.T) {
	orig := GetBatch(2)
	orig.SetColumnName(0, "x")
	orig.SetColumnName(1, "y")
	orig.AppendRow(0, LX.T_INT_KW, int64(11), false)
	orig.AppendRow(1, LX.T_TEXT, "zz", false)
	orig.AdvanceSize()
	orig.AppendRow(0, LX.T_INT_KW, int64(12), false)
	orig.AppendRow(1, LX.T_TEXT, "yy", false)
	orig.AdvanceSize()
	rows := orig.ToRows()
	orig.Put()

	back := RowsToBatch(rows)
	if back == nil {
		t.Fatal("RowsToBatch returned nil batch")
	}
	defer back.Put()
	if back.Size != 2 {
		t.Fatalf("Size: got %d want 2", back.Size)
	}
	if got := back.Value(0, 0); got != int64(11) {
		t.Errorf("b0x: got %v want 11", got)
	}
	if got := back.Value(1, 0); got != "zz" {
		t.Errorf("b0y: got %v want zz", got)
	}
	if got := back.Value(0, 1); got != int64(12) {
		t.Errorf("b1x: got %v want 12", got)
	}
	if got := back.Value(1, 1); got != "yy" {
		t.Errorf("b1y: got %v want yy", got)
	}
}

// REQ002235: RowsToBatch honors explicit Row.Types over inference.
func TestRowsToBatch_RespectsExplicitTypes(t *testing.T) {
	rows := []pl.Row{
		{
			Cols:  []string{"x"},
			Types: []LX.TokenType{LX.T_FLOAT_KW},
			Data:  []pl.Value{{Kind: pl.KindInt, I64: 3}},
		},
	}
	b := RowsToBatch(rows)
	if b == nil {
		t.Fatal("RowsToBatch returned nil batch")
	}
	defer b.Put()
	if b.Cols[0].Type != LX.T_FLOAT_KW {
		t.Errorf("col0 type: got %v want FLOAT (explicit overrides int kind)", b.Cols[0].Type)
	}
}

// REQ002235: empty / nil input edge cases.
func TestRowsToBatch_NilAndEmpty(t *testing.T) {
	if got := RowsToBatch(nil); got != nil {
		t.Errorf("nil input: got %v want nil", got)
	}
	if got := RowsToBatch([]pl.Row{}); got != nil {
		t.Errorf("empty input: got %v want nil", got)
	}
}

// REQ002235: RowsToBatch must survive rows whose Data slice is shorter
// than Cols (defensive bounds check in the value loop).
func TestRowsToBatch_ShortData(t *testing.T) {
	rows := []pl.Row{
		{Cols: []string{"a", "b"}, Data: []pl.Value{{Kind: pl.KindInt, I64: 1}}},
	}
	b := RowsToBatch(rows)
	if b == nil {
		t.Fatal("RowsToBatch returned nil batch")
	}
	defer b.Put()
	if b.Size != 1 {
		t.Fatalf("Size: got %d want 1", b.Size)
	}
	if got := b.Value(0, 0); got != int64(1) {
		t.Errorf("r0c0: got %v want 1", got)
	}
	// c1 has no data → null.
	if got := b.Value(1, 0); got != nil {
		t.Errorf("r0c1: got %v want nil", got)
	}
}
