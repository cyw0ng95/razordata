package EX

import (
	"context"
	"testing"

	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// ---------------------------------------------------------------------------
// resolveAggDef unit tests
// ---------------------------------------------------------------------------

// testScanForResolve returns a DT.Operator for resolveAggDef tests.
// When colsOf returns nil for the operator, resolveColumnIndex falls back
// to index 0 — sufficient for unit-testing the UnaryExpr/CastExpr unwrap logic.
func testScanForResolve() DT.Operator {
	// Use a bare SeqScan without store — Schema() returns nil,
	// so resolveColumnIndex falls back to index 0 for all columns.
	return OP.NewSeqScan("test_table")
}

func TestREQ002030_ResolveAggDef_UnaryMinus(t *testing.T) {
	scan := testScanForResolve()

	// SUM(-col0) should resolve with Negate=true, Col=0
	expr := &PS.AggregateFunc{
		Name: "SUM",
		Arg: &PS.UnaryExpr{
			Op:      LX.T_MINUS,
			Operand: &PS.Ident{Name: "col0"},
		},
	}
	def, ok := resolveAggDef(expr, scan)
	if !ok {
		t.Fatal("resolveAggDef(SUM(-col0)) returned false")
	}
	if def.Kind != AG.AggSum {
		t.Errorf("Kind = %d, want AggSum", def.Kind)
	}
	if def.Col != 0 {
		t.Errorf("Col = %d, want 0", def.Col)
	}
	if !def.Negate {
		t.Error("Negate = false, want true")
	}
}

func TestREQ002030_ResolveAggDef_UnaryPlus(t *testing.T) {
	scan := testScanForResolve()

	// MIN(+col1) should resolve with Negate=false.
	// Note: bare SeqScan has no schema, so Col falls back to 0.
	expr := &PS.AggregateFunc{
		Name: "MIN",
		Arg: &PS.UnaryExpr{
			Op:      LX.T_PLUS,
			Operand: &PS.Ident{Name: "col1"},
		},
	}
	def, ok := resolveAggDef(expr, scan)
	if !ok {
		t.Fatal("resolveAggDef(MIN(+col1)) returned false")
	}
	if def.Kind != AG.AggMin {
		t.Errorf("Kind = %d, want AggMin", def.Kind)
	}
	if def.Negate {
		t.Error("Negate = true, want false")
	}
}

func TestREQ002030_ResolveAggDef_DoubleNegate(t *testing.T) {
	scan := testScanForResolve()

	// SUM(-(-col0)) should resolve with Negate=false (double negative cancels)
	expr := &PS.AggregateFunc{
		Name: "SUM",
		Arg: &PS.UnaryExpr{
			Op: LX.T_MINUS,
			Operand: &PS.UnaryExpr{
				Op:      LX.T_MINUS,
				Operand: &PS.Ident{Name: "col0"},
			},
		},
	}
	def, ok := resolveAggDef(expr, scan)
	if !ok {
		t.Fatal("resolveAggDef(SUM(-(-col0))) returned false")
	}
	if def.Negate {
		t.Error("Negate = true for double negate, want false")
	}
}

func TestREQ002030_ResolveAggDef_TripleNegate(t *testing.T) {
	scan := testScanForResolve()

	// SUM(-(-(-col0))) = SUM(-col0), Negate=true
	expr := &PS.AggregateFunc{
		Name: "SUM",
		Arg: &PS.UnaryExpr{
			Op: LX.T_MINUS,
			Operand: &PS.UnaryExpr{
				Op: LX.T_MINUS,
				Operand: &PS.UnaryExpr{
					Op:      LX.T_MINUS,
					Operand: &PS.Ident{Name: "col0"},
				},
			},
		},
	}
	def, ok := resolveAggDef(expr, scan)
	if !ok {
		t.Fatal("resolveAggDef(SUM(-(-(-col0)))) returned false")
	}
	if !def.Negate {
		t.Error("Negate = false for triple negate, want true")
	}
}

func TestREQ002030_ResolveAggDef_CastExpr(t *testing.T) {
	scan := testScanForResolve()

	// SUM(CAST(col0 AS SIGNED)) should resolve with Negate=false, Col=0
	expr := &PS.AggregateFunc{
		Name: "SUM",
		Arg: &PS.CastExpr{
			Expr: &PS.Ident{Name: "col0"},
			Type: &PS.TypeInfo{Type: LX.T_INT_KW},
		},
	}
	def, ok := resolveAggDef(expr, scan)
	if !ok {
		t.Fatal("resolveAggDef(SUM(CAST(col0 AS SIGNED))) returned false")
	}
	if def.Col != 0 {
		t.Errorf("Col = %d, want 0", def.Col)
	}
	if def.Negate {
		t.Error("Negate = true, want false")
	}
}

func TestREQ002030_ResolveAggDef_CastExprWithUnary(t *testing.T) {
	scan := testScanForResolve()

	// SUM(CAST(-col0 AS SIGNED)) should resolve with Negate=true, Col=0
	expr := &PS.AggregateFunc{
		Name: "SUM",
		Arg: &PS.CastExpr{
			Expr: &PS.UnaryExpr{
				Op:      LX.T_MINUS,
				Operand: &PS.Ident{Name: "col0"},
			},
			Type: &PS.TypeInfo{Type: LX.T_INT_KW},
		},
	}
	def, ok := resolveAggDef(expr, scan)
	if !ok {
		t.Fatal("resolveAggDef(SUM(CAST(-col0 AS SIGNED))) returned false")
	}
	if !def.Negate {
		t.Error("Negate = false, want true")
	}
	if def.Col != 0 {
		t.Errorf("Col = %d, want 0", def.Col)
	}
}

func TestREQ002030_ResolveAggDef_PlusPlusIdent(t *testing.T) {
	scan := testScanForResolve()

	// COUNT(+ + col1) should resolve with Negate=false.
	// Note: bare SeqScan has no schema, so Col falls back to 0.
	expr := &PS.AggregateFunc{
		Name: "COUNT",
		Arg: &PS.UnaryExpr{
			Op: LX.T_PLUS,
			Operand: &PS.UnaryExpr{
				Op:      LX.T_PLUS,
				Operand: &PS.Ident{Name: "col1"},
			},
		},
	}
	def, ok := resolveAggDef(expr, scan)
	if !ok {
		t.Fatal("resolveAggDef(COUNT(+ + col1)) returned false")
	}
	if def.Negate {
		t.Error("Negate = true, want false")
	}
}

func TestREQ002030_ResolveAggDef_DistinctNegate(t *testing.T) {
	scan := testScanForResolve()

	// COUNT(DISTINCT -col0) should resolve with Distinct=true, Negate=true
	expr := &PS.AggregateFunc{
		Name:     "COUNT",
		Distinct: true,
		Arg: &PS.UnaryExpr{
			Op:      LX.T_MINUS,
			Operand: &PS.Ident{Name: "col0"},
		},
	}
	def, ok := resolveAggDef(expr, scan)
	if !ok {
		t.Fatal("resolveAggDef(COUNT(DISTINCT -col0)) returned false")
	}
	if !def.Distinct {
		t.Error("Distinct = false, want true")
	}
	if !def.Negate {
		t.Error("Negate = false, want true")
	}
}

func TestREQ002030_ResolveAggDef_BinaryExprFallsBack(t *testing.T) {
	scan := testScanForResolve()

	// SUM(col0 + 1) should return false (BinaryExpr not supported)
	expr := &PS.AggregateFunc{
		Name: "SUM",
		Arg: &PS.BinaryExpr{
			Op:    LX.T_PLUS,
			Left:  &PS.Ident{Name: "col0"},
			Right: &PS.NumberLiteral{Val: 1},
		},
	}
	_, ok := resolveAggDef(expr, scan)
	if ok {
		t.Error("resolveAggDef(SUM(col0 + 1)) should return false for BinaryExpr")
	}
}

func TestREQ002030_ResolveAggDef_StringAggRejectsNegate(t *testing.T) {
	scan := testScanForResolve()

	// GROUP_CONCAT(-col0) should return false (negate meaningless for string agg)
	expr := &PS.AggregateFunc{
		Name: "GROUP_CONCAT",
		Arg: &PS.UnaryExpr{
			Op:      LX.T_MINUS,
			Operand: &PS.Ident{Name: "col0"},
		},
	}
	_, ok := resolveAggDef(expr, scan)
	if ok {
		t.Error("resolveAggDef(GROUP_CONCAT(-col0)) should return false")
	}
}

func TestREQ002030_ResolveAggDef_PlusIdent(t *testing.T) {
	scan := testScanForResolve()

	// SUM(+col0) should resolve with Negate=false
	expr := &PS.AggregateFunc{
		Name: "SUM",
		Arg: &PS.UnaryExpr{
			Op:      LX.T_PLUS,
			Operand: &PS.Ident{Name: "col0"},
		},
	}
	def, ok := resolveAggDef(expr, scan)
	if !ok {
		t.Fatal("resolveAggDef(SUM(+col0)) returned false")
	}
	if def.Negate {
		t.Error("Negate = true for +col0, want false")
	}
}

// ---------------------------------------------------------------------------
// End-to-end VectorizedHashAggregate tests with Negate
// ---------------------------------------------------------------------------

// testAggSource wraps testBatchSource for use in EX package tests.
type testAggSource struct {
	batches []*UT.Batch
	idx     int
}

func (s *testAggSource) NextBatch(_ context.Context) (*UT.Batch, error) {
	if s.idx >= len(s.batches) {
		return nil, nil
	}
	b := s.batches[s.idx]
	s.idx++
	return b, nil
}

func (s *testAggSource) Close() error { return nil }

// makeAggIntBatch creates a 1-column INT batch with given values.
func makeAggIntBatch(values []int64) *UT.Batch {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(values))
	copy(b.Cols[0].Data.Ints, values)
	b.Size = len(values)
	return b
}

// makeAggGroupBatch creates a 2-column batch: col 0 = group key, col 1 = value.
func makeAggGroupBatch(keys, values []int64) *UT.Batch {
	b := UT.GetBatch(2)
	b.SetColumnName(0, "g")
	b.SetColumnName(1, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(keys))
	copy(b.Cols[0].Data.Ints, keys)
	b.Cols[1].Type = LX.T_INT_KW
	b.Cols[1].Data.Ints = make([]int64, len(values))
	copy(b.Cols[1].Data.Ints, values)
	b.Size = len(keys)
	return b
}

func TestREQ002030_SumNegate(t *testing.T) {
	// SUM(-v) with values [1, 2, 3] => -1 + -2 + -3 = -6
	src := &testAggSource{
		batches: []*UT.Batch{makeAggIntBatch([]int64{1, 2, 3})},
	}
	agg := AG.NewVectorizedHashAggregate(src, nil, []AG.AggDef{
		{Kind: AG.AggSum, Col: 0, Negate: true},
	})

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	defer batch.Put()

	if batch.LogicalSize() != 1 {
		t.Fatalf("expected 1 row, got %d", batch.LogicalSize())
	}
	got := batch.Cols[0].Data.Ints[0]
	if got != -6 {
		t.Errorf("SUM(-v) = %d, want -6", got)
	}
}

func TestREQ002030_MinNegate(t *testing.T) {
	// MIN(-v) with values [1, 2, 3] => min(-1, -2, -3) = -3
	src := &testAggSource{
		batches: []*UT.Batch{makeAggIntBatch([]int64{1, 2, 3})},
	}
	agg := AG.NewVectorizedHashAggregate(src, nil, []AG.AggDef{
		{Kind: AG.AggMin, Col: 0, Negate: true},
	})

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	defer batch.Put()

	got := batch.Cols[0].Data.Ints[0]
	if got != -3 {
		t.Errorf("MIN(-v) = %d, want -3", got)
	}
}

func TestREQ002030_MaxNegate(t *testing.T) {
	// MAX(-v) with values [1, 2, 3] => max(-1, -2, -3) = -1
	src := &testAggSource{
		batches: []*UT.Batch{makeAggIntBatch([]int64{1, 2, 3})},
	}
	agg := AG.NewVectorizedHashAggregate(src, nil, []AG.AggDef{
		{Kind: AG.AggMax, Col: 0, Negate: true},
	})

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	defer batch.Put()

	got := batch.Cols[0].Data.Ints[0]
	if got != -1 {
		t.Errorf("MAX(-v) = %d, want -1", got)
	}
}

func TestREQ002030_AvgNegate(t *testing.T) {
	// AVG(-v) with values [10, 20, 30] => (-10 + -20 + -30) / 3 = -20
	src := &testAggSource{
		batches: []*UT.Batch{makeAggIntBatch([]int64{10, 20, 30})},
	}
	agg := AG.NewVectorizedHashAggregate(src, nil, []AG.AggDef{
		{Kind: AG.AggAvg, Col: 0, Negate: true},
	})

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	defer batch.Put()

	got := batch.Cols[0].Data.Ints[0]
	if got != -20 {
		t.Errorf("AVG(-v) = %d, want -20", got)
	}
}

func TestREQ002030_CountNegate(t *testing.T) {
	// COUNT(-v) should count non-null rows regardless of Negate.
	// With values [1, 2, 3] => count = 3
	src := &testAggSource{
		batches: []*UT.Batch{makeAggIntBatch([]int64{1, 2, 3})},
	}
	agg := AG.NewVectorizedHashAggregate(src, nil, []AG.AggDef{
		{Kind: AG.AggCount, Col: 0, Negate: true},
	})

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	defer batch.Put()

	got := batch.Cols[0].Data.Ints[0]
	if got != 3 {
		t.Errorf("COUNT(-v) = %d, want 3", got)
	}
}

func TestREQ002030_SumNoNegate(t *testing.T) {
	// SUM(v) without Negate should work as before.
	src := &testAggSource{
		batches: []*UT.Batch{makeAggIntBatch([]int64{1, 2, 3})},
	}
	agg := AG.NewVectorizedHashAggregate(src, nil, []AG.AggDef{
		{Kind: AG.AggSum, Col: 0, Negate: false},
	})

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	defer batch.Put()

	got := batch.Cols[0].Data.Ints[0]
	if got != 6 {
		t.Errorf("SUM(v) = %d, want 6", got)
	}
}

func TestREQ002030_GroupBySumNegate(t *testing.T) {
	// GROUP BY g, SUM(-v) with groups:
	// g=1, v=10,20 => SUM(-v) = -30
	// g=2, v=30    => SUM(-v) = -30
	src := &testAggSource{
		batches: []*UT.Batch{makeAggGroupBatch(
			[]int64{1, 1, 2},
			[]int64{10, 20, 30},
		)},
	}
	agg := AG.NewVectorizedHashAggregate(src, []int{0}, []AG.AggDef{
		{Kind: AG.AggSum, Col: 1, Negate: true},
	})

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	defer batch.Put()

	// Collect results by group key
	results := map[int64]int64{}
	for i := 0; i < batch.LogicalSize(); i++ {
		g := batch.Cols[0].Data.Ints[i]
		s := batch.Cols[1].Data.Ints[i]
		results[g] = s
	}
	if results[1] != -30 {
		t.Errorf("group 1 SUM(-v) = %d, want -30", results[1])
	}
	if results[2] != -30 {
		t.Errorf("group 2 SUM(-v) = %d, want -30", results[2])
	}
}

func TestREQ002030_DistinctNegate(t *testing.T) {
	// COUNT(DISTINCT -v) with values [1, 2, 1, 3, 2]
	// negated: [-1, -2, -1, -3, -2]
	// distinct: {-1, -2, -3} => count = 3
	src := &testAggSource{
		batches: []*UT.Batch{makeAggIntBatch([]int64{1, 2, 1, 3, 2})},
	}
	agg := AG.NewVectorizedHashAggregate(src, nil, []AG.AggDef{
		{Kind: AG.AggCount, Col: 0, Negate: true, Distinct: true},
	})

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	defer batch.Put()

	got := batch.Cols[0].Data.Ints[0]
	if got != 3 {
		t.Errorf("COUNT(DISTINCT -v) = %d, want 3", got)
	}
}
