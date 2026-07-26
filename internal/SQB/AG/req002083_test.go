package AG

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// ---------------------------------------------------------------------------
// Mock operators for streaming scalar aggregate tests
// ---------------------------------------------------------------------------

// batchOnlyChild implements Operator and BatchProducer for testing.
// It returns pre-built batches and rows from those batches for Next().
// REQ002083.
type batchOnlyChild struct {
	batches []*UT.Batch
	idx     int
	closed  bool
}

func (c *batchOnlyChild) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNoRows
}

func (c *batchOnlyChild) Close() error {
	c.closed = true
	return nil
}

func (c *batchOnlyChild) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if c.idx >= len(c.batches) {
		return nil, nil
	}
	b := c.batches[c.idx]
	c.idx++
	return b, nil
}

func (c *batchOnlyChild) BatchSupported() bool { return true }

// Verify interfaces at compile time.
var _ UT.BatchProducer = (*batchOnlyChild)(nil)
var _ UT.BatchSupportChecker = (*batchOnlyChild)(nil)

// ---------------------------------------------------------------------------
// Helper functions for building test batches
// ---------------------------------------------------------------------------

// makeIntColBatch creates a batch with a single INT column named "v".
func makeIntColBatch(values []int64) *UT.Batch {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(values))
	copy(b.Cols[0].Data.Ints, values)
	b.Size = len(values)
	return b
}

// makeFloatColBatch creates a batch with a single FLOAT column named "v".
func makeFloatColBatch(values []float64) *UT.Batch {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "v")
	b.Cols[0].Type = LX.T_FLOAT_KW
	b.Cols[0].Data.Floats = make([]float64, len(values))
	copy(b.Cols[0].Data.Floats, values)
	b.Size = len(values)
	return b
}

// makeIntColBatchWithNulls creates a batch with a single INT column "v"
// where nulls[i] indicates null values.
func makeIntColBatchWithNulls(values []int64, nulls []bool) *UT.Batch {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(values))
	copy(b.Cols[0].Data.Ints, values)
	if len(nulls) > 0 {
		b.Cols[0].Nulls = make([]bool, len(nulls))
		copy(b.Cols[0].Nulls, nulls)
	}
	b.Size = len(values)
	return b
}

// makeFloatColBatchWithNulls creates a batch with a single FLOAT column "v"
// where nulls[i] indicates null values.
func makeFloatColBatchWithNulls(values []float64, nulls []bool) *UT.Batch {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "v")
	b.Cols[0].Type = LX.T_FLOAT_KW
	b.Cols[0].Data.Floats = make([]float64, len(values))
	copy(b.Cols[0].Data.Floats, values)
	if len(nulls) > 0 {
		b.Cols[0].Nulls = make([]bool, len(nulls))
		copy(b.Cols[0].Nulls, nulls)
	}
	b.Size = len(values)
	return b
}

// ---------------------------------------------------------------------------
// REQ002083: Streaming scalar aggregate tests
// ---------------------------------------------------------------------------

// TestStreamingScalarAggregate_CountStar verifies COUNT(*) via streaming.
// REQ002083.
func TestStreamingScalarAggregate_CountStar(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{1, 2, 3}),
			makeIntColBatch([]int64{4, 5}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(row.Data) != 1 {
		t.Fatalf("expected 1 column, got %d", len(row.Data))
	}
	if row.Data[0].Kind != KindInt || row.Data[0].I64 != 5 {
		t.Errorf("COUNT(*) = %v, want 5", row.Data[0])
	}
}

// TestStreamingScalarAggregate_SUM verifies SUM(col) via streaming.
// REQ002083.
func TestStreamingScalarAggregate_SUM(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{10, 20, 30}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].Kind != KindInt || row.Data[0].I64 != 60 {
		t.Errorf("SUM(v) = %v, want 60", row.Data[0])
	}
}

// TestStreamingScalarAggregate_SUM_Float verifies SUM on float values.
// REQ002083.
func TestStreamingScalarAggregate_SUM_Float(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeFloatColBatch([]float64{1.5, 2.5, 3.0}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].Kind != KindFloat {
		t.Errorf("SUM(v) kind = %d, want KindFloat", row.Data[0].Kind)
	}
	if row.Data[0].F64 != 7.0 {
		t.Errorf("SUM(v) = %v, want 7.0", row.Data[0].F64)
	}
}

// TestStreamingScalarAggregate_AVG verifies AVG(col) via streaming.
// REQ002083.
func TestStreamingScalarAggregate_AVG(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{10, 20, 30}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "AVG", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].Kind != KindFloat {
		t.Errorf("AVG(v) kind = %d, want KindFloat", row.Data[0].Kind)
	}
	if row.Data[0].F64 != 20.0 {
		t.Errorf("AVG(v) = %v, want 20.0", row.Data[0].F64)
	}
}

// TestStreamingScalarAggregate_MIN verifies MIN(col) via streaming.
// REQ002083.
func TestStreamingScalarAggregate_MIN(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{30, 10, 20}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "MIN", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].Kind != KindInt || row.Data[0].I64 != 10 {
		t.Errorf("MIN(v) = %v, want 10", row.Data[0])
	}
}

// TestStreamingScalarAggregate_MAX verifies MAX(col) via streaming.
// REQ002083.
func TestStreamingScalarAggregate_MAX(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{30, 10, 20}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "MAX", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].Kind != KindInt || row.Data[0].I64 != 30 {
		t.Errorf("MAX(v) = %v, want 30", row.Data[0])
	}
}

// TestStreamingScalarAggregate_MultipleAggs verifies multiple aggregates
// in a single query. REQ002083.
func TestStreamingScalarAggregate_MultipleAggs(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{5, 10, 15}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
		&PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "MIN", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "MAX", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(row.Data) != 4 {
		t.Fatalf("expected 4 columns, got %d", len(row.Data))
	}
	if row.Data[0].I64 != 3 {
		t.Errorf("COUNT(*) = %d, want 3", row.Data[0].I64)
	}
	if row.Data[1].I64 != 30 {
		t.Errorf("SUM(v) = %d, want 30", row.Data[1].I64)
	}
	if row.Data[2].I64 != 5 {
		t.Errorf("MIN(v) = %d, want 5", row.Data[2].I64)
	}
	if row.Data[3].I64 != 15 {
		t.Errorf("MAX(v) = %d, want 15", row.Data[3].I64)
	}
}

// TestStreamingScalarAggregate_MultipleBatches verifies that
// streaming accumulation works across multiple batches.
// REQ002083.
func TestStreamingScalarAggregate_MultipleBatches(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{1, 2}),
			makeIntColBatch([]int64{3, 4}),
			makeIntColBatch([]int64{5}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
		&PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].I64 != 5 {
		t.Errorf("COUNT(*) = %d, want 5", row.Data[0].I64)
	}
	if row.Data[1].I64 != 15 {
		t.Errorf("SUM(v) = %d, want 15", row.Data[1].I64)
	}
}

// TestStreamingScalarAggregate_EmptyInput verifies that scalar
// aggregates on empty input return correct defaults (0 for COUNT,
// NULL for SUM/AVG/MIN/MAX). REQ002083.
func TestStreamingScalarAggregate_EmptyInput(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{}, // no batches
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
		&PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "AVG", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "MIN", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "MAX", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(row.Data) != 5 {
		t.Fatalf("expected 5 columns, got %d", len(row.Data))
	}
	// COUNT(*) returns 0 for empty input
	if row.Data[0].I64 != 0 {
		t.Errorf("COUNT(*) = %d, want 0", row.Data[0].I64)
	}
	// SUM, AVG, MIN, MAX return NULL for empty input
	if row.Data[1].Kind != KindNull {
		t.Errorf("SUM(v) = %v, want NULL", row.Data[1])
	}
	if row.Data[2].Kind != KindNull {
		t.Errorf("AVG(v) = %v, want NULL", row.Data[2])
	}
	if row.Data[3].Kind != KindNull {
		t.Errorf("MIN(v) = %v, want NULL", row.Data[3])
	}
	if row.Data[4].Kind != KindNull {
		t.Errorf("MAX(v) = %v, want NULL", row.Data[4])
	}
}

// TestStreamingScalarAggregate_NullValues verifies that NULL values
// are skipped for COUNT(col), SUM, AVG, MIN, MAX.
// REQ002083.
func TestStreamingScalarAggregate_NullValues(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatchWithNulls(
				[]int64{10, 0, 30, 0},
				[]bool{false, true, false, true},
			),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "AVG", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "MIN", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "MAX", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	// Only 2 non-null values: 10 and 30
	if row.Data[0].I64 != 2 {
		t.Errorf("COUNT(v) = %d, want 2", row.Data[0].I64)
	}
	if row.Data[1].I64 != 40 {
		t.Errorf("SUM(v) = %d, want 40", row.Data[1].I64)
	}
	if row.Data[2].F64 != 20.0 {
		t.Errorf("AVG(v) = %v, want 20.0", row.Data[2].F64)
	}
	if row.Data[3].I64 != 10 {
		t.Errorf("MIN(v) = %d, want 10", row.Data[3].I64)
	}
	if row.Data[4].I64 != 30 {
		t.Errorf("MAX(v) = %d, want 30", row.Data[4].I64)
	}
}

// TestStreamingScalarAggregate_FloatNulls verifies NULL handling
// with float columns. REQ002083.
func TestStreamingScalarAggregate_FloatNulls(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeFloatColBatchWithNulls(
				[]float64{1.5, 0, 3.5, 0},
				[]bool{false, true, false, true},
			),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "AVG", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	// Only 2 non-null values: 1.5 and 3.5
	if row.Data[0].F64 != 5.0 {
		t.Errorf("SUM(v) = %v, want 5.0", row.Data[0].F64)
	}
	if row.Data[1].F64 != 2.5 {
		t.Errorf("AVG(v) = %v, want 2.5", row.Data[1].F64)
	}
}

// TestStreamingScalarAggregate_AliasedExpr verifies that
// SUM(v) AS total works with the streaming path.
// REQ002083.
func TestStreamingScalarAggregate_AliasedExpr(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{10, 20}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AliasedExpr{
			Expr:  &PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
			Alias: "total",
		},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].I64 != 30 {
		t.Errorf("SUM(v) AS total = %d, want 30", row.Data[0].I64)
	}
	if row.Cols[0] != "total" {
		t.Errorf("column name = %q, want %q", row.Cols[0], "total")
	}
}

// TestStreamingScalarAggregate_ConstCol verifies that constant
// non-aggregate columns are correctly emitted alongside streaming
// aggregates. REQ002083.
func TestStreamingScalarAggregate_ConstCol(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{10, 20}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
	})
	agg.SetConstCols([]PS.Expr{&PS.NumberLiteral{Val: 42}})
	agg.SetFullCols([]PS.Expr{
		&PS.NumberLiteral{Val: 42},
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(row.Data) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(row.Data))
	}
	if row.Data[0].I64 != 42 {
		t.Errorf("const col = %d, want 42", row.Data[0].I64)
	}
	if row.Data[1].I64 != 2 {
		t.Errorf("COUNT(*) = %d, want 2", row.Data[1].I64)
	}
}

// TestStreamingScalarAggregate_DISTINCTFallback verifies that
// DISTINCT aggregates fall back to the ToRows path.
// REQ002083.
func TestStreamingScalarAggregate_DISTINCTFallback(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{1, 2, 2, 3}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.Ident{Name: "v"}, Distinct: true},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].I64 != 3 {
		t.Errorf("COUNT(DISTINCT v) = %d, want 3", row.Data[0].I64)
	}
}

// TestStreamingScalarAggregate_ComplexArgFallback verifies that
// complex aggregate arguments (e.g., SUM(col + 1)) fall back to
// the ToRows path. REQ002083.
func TestStreamingScalarAggregate_ComplexArgFallback(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{1, 2, 3}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{
			Name: "SUM",
			Arg: &PS.BinaryExpr{
				Left:  &PS.Ident{Name: "v"},
				Right: &PS.NumberLiteral{Val: 1},
				Op:    LX.T_PLUS,
			},
		},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	// SUM(v + 1) = (1+1) + (2+1) + (3+1) = 9
	if row.Data[0].I64 != 9 {
		t.Errorf("SUM(v+1) = %d, want 9", row.Data[0].I64)
	}
}

// TestStreamingScalarAggregate_NonBatchChild verifies that the
// streaming path falls back when the child does not support batches.
// REQ002083.
func TestStreamingScalarAggregate_NonBatchChild(t *testing.T) {
	// rowOnlyChild implements Operator but not BatchProducer.
	rowOnlyChild := &struct {
		Operator
		rows []Row
		idx  int
	}{
		rows: []Row{
			{Cols: []string{"v"}, Data: []Value{{Kind: KindInt, I64: 10}}},
			{Cols: []string{"v"}, Data: []Value{{Kind: KindInt, I64: 20}}},
		},
	}
	rowOnlyChild.Operator = &rowOp{rows: &rowOnlyChild.rows}

	agg := NewAggregate(rowOnlyChild.Operator, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].I64 != 2 {
		t.Errorf("COUNT(*) = %d, want 2", row.Data[0].I64)
	}
}

// rowOp is a minimal Operator for testing the non-batch fallback path.
type rowOp struct {
	rows *[]Row
	idx  int
}

func (r *rowOp) Next(ctx context.Context) (Row, error) {
	rows := *r.rows
	if r.idx >= len(rows) {
		return Row{}, ErrNoRows
	}
	row := rows[r.idx]
	r.idx++
	return row, nil
}

func (r *rowOp) Close() error { return nil }

// ---------------------------------------------------------------------------
// Unit tests for helper functions
// ---------------------------------------------------------------------------

// TestCanStreamScalarAgg verifies the canStreamScalarAgg checker.
// REQ002083.
func TestCanStreamScalarAgg(t *testing.T) {
	tests := []struct {
		name      string
		aggFunc   *PS.AggregateFunc
		wantKind  string
		wantOK    bool
	}{
		{
			name:      "COUNT(*)",
			aggFunc:   &PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
			wantKind:  "COUNT",
			wantOK:    true,
		},
		{
			name:      "COUNT(ident)",
			aggFunc:   &PS.AggregateFunc{Name: "COUNT", Arg: &PS.Ident{Name: "v"}},
			wantKind:  "COUNT",
			wantOK:    true,
		},
		{
			name:      "SUM(ident)",
			aggFunc:   &PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
			wantKind:  "SUM",
			wantOK:    true,
		},
		{
			name:      "AVG(ident)",
			aggFunc:   &PS.AggregateFunc{Name: "AVG", Arg: &PS.Ident{Name: "v"}},
			wantKind:  "AVG",
			wantOK:    true,
		},
		{
			name:      "MIN(ident)",
			aggFunc:   &PS.AggregateFunc{Name: "MIN", Arg: &PS.Ident{Name: "v"}},
			wantKind:  "MIN",
			wantOK:    true,
		},
		{
			name:      "MAX(ident)",
			aggFunc:   &PS.AggregateFunc{Name: "MAX", Arg: &PS.Ident{Name: "v"}},
			wantKind:  "MAX",
			wantOK:    true,
		},
		{
			name:      "COUNT(DISTINCT ident) - not streamable",
			aggFunc:   &PS.AggregateFunc{Name: "COUNT", Arg: &PS.Ident{Name: "v"}, Distinct: true},
			wantKind:  "",
			wantOK:    false,
		},
		{
			name: "SUM(binary_expr) - not streamable",
			aggFunc: &PS.AggregateFunc{
				Name: "SUM",
				Arg: &PS.BinaryExpr{
					Left:  &PS.Ident{Name: "v"},
					Right: &PS.NumberLiteral{Val: 1},
					Op:    LX.T_PLUS,
				},
			},
			wantKind: "",
			wantOK:   false,
		},
		{
			name:      "SUM(*) - not streamable",
			aggFunc:   &PS.AggregateFunc{Name: "SUM", Arg: &PS.StarExpr{}},
			wantKind:  "",
			wantOK:    false,
		},
		{
			name:      "GROUP_CONCAT - not streamable",
			aggFunc:   &PS.AggregateFunc{Name: "GROUP_CONCAT", Arg: &PS.Ident{Name: "v"}},
			wantKind:  "",
			wantOK:    false,
		},
		{
			name:      "QUALIFIED_NAME",
			aggFunc:   &PS.AggregateFunc{Name: "SUM", Arg: &PS.QualifiedName{Table: "t", Name: "v"}},
			wantKind:  "SUM",
			wantOK:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, ok := canStreamScalarAgg(tt.aggFunc)
			if ok != tt.wantOK {
				t.Errorf("canStreamScalarAgg() ok = %v, want %v", ok, tt.wantOK)
			}
			if kind != tt.wantKind {
				t.Errorf("canStreamScalarAgg() kind = %q, want %q", kind, tt.wantKind)
			}
		})
	}
}

// TestResolveAccumColIdx verifies column index resolution.
// REQ002083.
func TestResolveAccumColIdx(t *testing.T) {
	colMap := map[string]int{
		"v":     0,
		"t.v":   1,
		"name":  2,
	}

	tests := []struct {
		name string
		arg  PS.Expr
		want int
	}{
		{name: "Ident found", arg: &PS.Ident{Name: "v"}, want: 0},
		{name: "Ident not found", arg: &PS.Ident{Name: "x"}, want: -1},
		{name: "QualifiedName qualified form", arg: &PS.QualifiedName{Table: "t", Name: "v"}, want: 1},
		{name: "QualifiedName bare form", arg: &PS.QualifiedName{Table: "other", Name: "name"}, want: 2},
		{name: "QualifiedName not found", arg: &PS.QualifiedName{Table: "z", Name: "x"}, want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAccumColIdx(tt.arg, colMap)
			if got != tt.want {
				t.Errorf("resolveAccumColIdx() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestScalarAggAccum_ComputeResult verifies the accumulator result
// computation. REQ002083.
func TestScalarAggAccum_ComputeResult(t *testing.T) {
	t.Run("COUNT with rows", func(t *testing.T) {
		acc := scalarAggAccum{aggKind: "COUNT", count: 5}
		got := acc.computeResult()
		if got != int64(5) {
			t.Errorf("COUNT result = %v, want 5", got)
		}
	})

	t.Run("SUM int only", func(t *testing.T) {
		acc := scalarAggAccum{aggKind: "SUM", sumI: 100, seenI: true}
		got := acc.computeResult()
		if got != int64(100) {
			t.Errorf("SUM(int) result = %v, want 100", got)
		}
	})

	t.Run("SUM float only", func(t *testing.T) {
		acc := scalarAggAccum{aggKind: "SUM", sumF: 3.14, seenF: true}
		got := acc.computeResult()
		if got != 3.14 {
			t.Errorf("SUM(float) result = %v, want 3.14", got)
		}
	})

	t.Run("SUM empty", func(t *testing.T) {
		acc := scalarAggAccum{aggKind: "SUM"}
		got := acc.computeResult()
		if got != nil {
			t.Errorf("SUM(empty) result = %v, want nil", got)
		}
	})

	t.Run("AVG", func(t *testing.T) {
		acc := scalarAggAccum{aggKind: "AVG", sumI: 60, count: 3, seenI: true}
		got := acc.computeResult()
		if got != 20.0 {
			t.Errorf("AVG result = %v, want 20.0", got)
		}
	})

	t.Run("AVG empty", func(t *testing.T) {
		acc := scalarAggAccum{aggKind: "AVG"}
		got := acc.computeResult()
		if got != nil {
			t.Errorf("AVG(empty) result = %v, want nil", got)
		}
	})

	t.Run("MIN no value", func(t *testing.T) {
		acc := scalarAggAccum{aggKind: "MIN"}
		got := acc.computeResult()
		if got != nil {
			t.Errorf("MIN(empty) result = %v, want nil", got)
		}
	})

	t.Run("MAX no value", func(t *testing.T) {
		acc := scalarAggAccum{aggKind: "MAX"}
		got := acc.computeResult()
		if got != nil {
			t.Errorf("MAX(empty) result = %v, want nil", got)
		}
	})
}

// TestStreamingScalarAggregate_Close verifies that Close works
// after streaming. REQ002083.
func TestStreamingScalarAggregate_Close(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{1, 2, 3}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
	})
	// Read the row first
	_, _ = agg.Next(context.Background())
	if err := agg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !child.closed {
		t.Error("child not closed")
	}
}

// TestStreamingScalarAggregate_SecondNextReturnsNoRows verifies
// that after reading the single output row, subsequent Next calls
// return ErrNoRows. REQ002083.
func TestStreamingScalarAggregate_SecondNextReturnsNoRows(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{1, 2}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("First Next: %v", err)
	}
	if row.Data[0].I64 != 2 {
		t.Errorf("COUNT(*) = %d, want 2", row.Data[0].I64)
	}

	_, err = agg.Next(context.Background())
	if err != ErrNoRows {
		t.Errorf("Second Next: err = %v, want ErrNoRows", err)
	}
}

// TestStreamingScalarAggregate_AllNulls verifies that when all
// values in a column are NULL, SUM/AVG/MIN/MAX return NULL and
// COUNT(col) returns 0. REQ002083.
func TestStreamingScalarAggregate_AllNulls(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatchWithNulls(
				[]int64{0, 0, 0},
				[]bool{true, true, true},
			),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "AVG", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "MIN", Arg: &PS.Ident{Name: "v"}},
		&PS.AggregateFunc{Name: "MAX", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	// COUNT(col) returns 0 for all-NULL
	if row.Data[0].I64 != 0 {
		t.Errorf("COUNT(v) = %d, want 0", row.Data[0].I64)
	}
	// SUM, AVG, MIN, MAX return NULL for all-NULL
	if row.Data[1].Kind != KindNull {
		t.Errorf("SUM(v) = %v, want NULL", row.Data[1])
	}
	if row.Data[2].Kind != KindNull {
		t.Errorf("AVG(v) = %v, want NULL", row.Data[2])
	}
	if row.Data[3].Kind != KindNull {
		t.Errorf("MIN(v) = %v, want NULL", row.Data[3])
	}
	if row.Data[4].Kind != KindNull {
		t.Errorf("MAX(v) = %v, want NULL", row.Data[4])
	}
}

// TestStreamingScalarAggregate_SumMixedIntFloat verifies SUM
// with mixed int and float values across batches.
// REQ002083.
func TestStreamingScalarAggregate_SumMixedIntFloat(t *testing.T) {
	child := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{10, 20}),
			makeFloatColBatch([]float64{3.5, 4.5}),
		},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	// 10 + 20 = 30 (int), 3.5 + 4.5 = 8.0 (float)
	// Total = 30 + 8.0 = 38.0 (float because seenF is true)
	if row.Data[0].Kind != KindFloat {
		t.Errorf("SUM(v) kind = %d, want KindFloat", row.Data[0].Kind)
	}
	if row.Data[0].F64 != 38.0 {
		t.Errorf("SUM(v) = %v, want 38.0", row.Data[0].F64)
	}
}

// TestStreamingScalarAggregate_SelectionVector verifies that
// the streaming path respects the batch selection vector.
// REQ002083.
func TestStreamingScalarAggregate_SelectionVector(t *testing.T) {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = []int64{10, 20, 30, 40, 50}
	b.Size = 5
	// Select only rows 0 and 3 (values 10 and 40)
	b.Sel = []uint16{0, 3}
	b.Pooled = false

	child := &batchOnlyChild{
		batches: []*UT.Batch{b},
	}
	agg := NewAggregate(child, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
		&PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "v"}},
	})
	defer agg.Close()

	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].I64 != 2 {
		t.Errorf("COUNT(*) = %d, want 2", row.Data[0].I64)
	}
	if row.Data[1].I64 != 50 {
		t.Errorf("SUM(v) = %d, want 50", row.Data[1].I64)
	}
}

// TestStreamingScalarAggregate_CloseResetsState verifies that Close()
// resets internal state so the Aggregate can be reused without
// stale data from a previous execution. This is critical for
// correlated subqueries where the Aggregate is re-executed per
// outer row. REQ002083.
func TestStreamingScalarAggregate_CloseResetsState(t *testing.T) {
	// First execution: COUNT(*) on 3 rows
	child1 := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{1, 2, 3}),
		},
	}
	agg := NewAggregate(child1, nil, []PS.Expr{
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
	})
	row, err := agg.Next(context.Background())
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if row.Data[0].I64 != 3 {
		t.Errorf("first COUNT(*) = %d, want 3", row.Data[0].I64)
	}

	// Close should reset all internal state
	if err := agg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Second execution: COUNT(*) on 1 row — must not accumulate
	// with previous state
	child2 := &batchOnlyChild{
		batches: []*UT.Batch{
			makeIntColBatch([]int64{10}),
		},
	}
	// Replace the child to simulate correlated subquery re-execution
	// with a fresh data source.
	agg.child = child2
	agg.buf = nil // simulate Close reset

	row, err = agg.Next(context.Background())
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if row.Data[0].I64 != 1 {
		t.Errorf("second COUNT(*) = %d, want 1 (no accumulation from first execution)", row.Data[0].I64)
	}
	agg.Close()
}
