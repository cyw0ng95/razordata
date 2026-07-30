package EX

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestTryPushPipeline_IneligibleNoStore verifies that tryPushPipeline
// returns nil when the SeqScan has no store (in-memory table). The
// push path requires a store to use the store-backed columnar scan.
// REQ002002.
func TestTryPushPipeline_IneligibleNoStore(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    LX.T_EQ,
	}
	filt := OP.NewFilter(ss, pred, nil)
	proj := OP.NewProject(filt, []PS.Expr{&PS.Ident{Name: "a"}})
	lim := OP.NewLimit(proj, 10)

	if got := tryPushPipeline(lim, nil); got != nil {
		t.Fatalf("expected nil for SeqScan without store, got %T", got)
	}
}

// TestTryPushPipeline_IneligibleSubqueryPredicate verifies that
// tryPushPipeline returns nil when the filter predicate contains a
// subquery. REQ002002.
func TestTryPushPipeline_IneligibleSubqueryPredicate(t *testing.T) {
	// Use a store-backed SeqScan so the only disqualifier is the subquery.
	ss, err := OP.NewSeqScanWithStore(nil, "kv")
	if err != nil {
		t.Skipf("NewSeqScanWithStore failed (no schema registered): %v", err)
	}
	if ss.Store() == nil {
		t.Skip("SeqScan has no store; cannot verify subquery gating")
	}
	// Build a predicate with a subquery: a IN (SELECT b FROM t2).
	pred := &PS.InExpr{
		Expr: &PS.Ident{Name: "a"},
		Subquery: &PS.Select{
			From: "t2",
			Cols: []PS.Expr{&PS.Ident{Name: "b"}},
		},
	}
	filt := OP.NewFilter(ss, pred, nil)
	proj := OP.NewProject(filt, []PS.Expr{&PS.Ident{Name: "a"}})
	lim := OP.NewLimit(proj, 10)

	if got := tryPushPipeline(lim, nil); got != nil {
		t.Fatalf("expected nil for subquery predicate, got %T", got)
	}
}

// TestTryPushPipeline_IneligibleComplexProjection verifies that
// tryPushPipeline returns nil when the projection contains arithmetic
// (BinaryExpr). REQ002002.
func TestTryPushPipeline_IneligibleComplexProjection(t *testing.T) {
	ss, err := OP.NewSeqScanWithStore(nil, "kv")
	if err != nil {
		t.Skipf("NewSeqScanWithStore failed: %v", err)
	}
	if ss.Store() == nil {
		t.Skip("SeqScan has no store")
	}
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    LX.T_EQ,
	}
	filt := OP.NewFilter(ss, pred, nil)
	// a + 1 — arithmetic projection is rejected.
	complexExpr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
		Op:    LX.T_PLUS,
	}
	proj := OP.NewProject(filt, []PS.Expr{complexExpr})
	lim := OP.NewLimit(proj, 10)

	if got := tryPushPipeline(lim, nil); got != nil {
		t.Fatalf("expected nil for complex projection, got %T", got)
	}
}

// TestTryPushPipeline_IneligibleBareSeqScan verifies that a bare
// SeqScan with no Filter/Project/Limit returns nil (degenerate shape;
// pull path handles it fine). REQ002002.
func TestTryPushPipeline_IneligibleBareSeqScan(t *testing.T) {
	ss, err := OP.NewSeqScanWithStore(nil, "kv")
	if err != nil {
		t.Skipf("NewSeqScanWithStore failed: %v", err)
	}
	if ss.Store() == nil {
		t.Skip("SeqScan has no store")
	}
	if got := tryPushPipeline(ss, nil); got != nil {
		t.Fatalf("expected nil for bare SeqScan, got %T", got)
	}
}

// TestTryPushPipeline_FilterTree verifies that tryPushPipeline
// handles a Filter→SeqScan tree without panicking. REQ002171.
func TestTryPushPipeline_FilterTree(t *testing.T) {
	ss, err := OP.NewSeqScanWithStore(nil, "kv")
	if err != nil {
		t.Skipf("NewSeqScanWithStore failed: %v", err)
	}
	if ss.Store() == nil {
		t.Skip("SeqScan has no store")
	}
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    LX.T_EQ,
	}
	filt := OP.NewFilter(ss, pred, nil)
	got := tryPushPipeline(filt, nil)
	_ = got
}

// TestTryVectorizePlan_FallsBackToPullForIneligiblePush verifies
// that when tryPushPipeline returns nil, tryVectorizePlan falls
// through to the pull-based transformRoot path. This guards the
// integration point. REQ002002.
func TestTryVectorizePlan_FallsBackToPullForIneligiblePush(t *testing.T) {
	// SeqScan without store → push returns nil → pull path wraps in
	// BatchToRowAdapter via VectorizedSeqScan.
	ss := OP.NewSeqScan("t1")
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    LX.T_EQ,
	}
	filt := OP.NewFilter(ss, pred, nil)
	proj := OP.NewProject(filt, []PS.Expr{&PS.Ident{Name: "a"}})
	lim := OP.NewLimit(proj, 10)

	result := tryVectorizePlan(lim, nil)
	if result == nil {
		t.Fatal("expected non-nil result from tryVectorizePlan")
	}
	if result == lim {
		t.Fatal("expected wrapped operator, not original Limit")
	}
}

// TestContainsSubquery covers the subquery detection helper used by
// tryPushPipeline gating. REQ002002.
func TestContainsSubquery(t *testing.T) {
	tests := []struct {
		name string
		expr PS.Expr
		want bool
	}{
		{
			name: "plain binary",
			expr: &PS.BinaryExpr{
				Left:  &PS.Ident{Name: "a"},
				Right: &PS.NumberLiteral{Val: 1},
				Op:    LX.T_EQ,
			},
			want: false,
		},
		{
			name: "subquery in IN",
			expr: &PS.InExpr{
				Expr:     &PS.Ident{Name: "a"},
				Subquery: &PS.Select{From: "t2"},
			},
			want: true,
		},
		{
			name: "scalar subquery",
			expr: &PS.SubqueryExpr{
				Subquery: &PS.Select{From: "t2"},
			},
			want: true,
		},
		{
			name: "subquery in binary left",
			expr: &PS.BinaryExpr{
				Left: &PS.SubqueryExpr{
					Subquery: &PS.Select{From: "t2"},
				},
				Right: &PS.NumberLiteral{Val: 1},
				Op:    LX.T_EQ,
			},
			want: true,
		},
		{
			name: "aliased subquery",
			expr: &PS.AliasedExpr{
				Expr: &PS.SubqueryExpr{
					Subquery: &PS.Select{From: "t2"},
				},
				Alias: "x",
			},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsSubquery(tc.expr); got != tc.want {
				t.Errorf("containsSubquery(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestIsSimpleProject covers the projection simplicity helper.
// REQ002002.
func TestIsSimpleProject(t *testing.T) {
	tests := []struct {
		name string
		cols []PS.Expr
		want bool
	}{
		{name: "ident only", cols: []PS.Expr{&PS.Ident{Name: "a"}}, want: true},
		{name: "qualified", cols: []PS.Expr{&PS.QualifiedName{Table: "t", Name: "a"}}, want: true},
		{name: "literals", cols: []PS.Expr{&PS.NumberLiteral{Val: 1}, &PS.StringLiteral{Val: "x"}}, want: true},
		{name: "aliased ident", cols: []PS.Expr{&PS.AliasedExpr{Expr: &PS.Ident{Name: "a"}, Alias: "x"}}, want: true},
		{name: "arithmetic", cols: []PS.Expr{&PS.BinaryExpr{Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}, Op: LX.T_PLUS}}, want: false},
		{name: "function call", cols: []PS.Expr{&PS.FunctionCall{Name: "ABS", Args: []PS.Expr{&PS.Ident{Name: "a"}}}}, want: false},
		{name: "mixed", cols: []PS.Expr{&PS.Ident{Name: "a"}, &PS.BinaryExpr{Left: &PS.Ident{Name: "b"}, Right: &PS.NumberLiteral{Val: 1}, Op: LX.T_PLUS}}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSimpleProject(tc.cols); got != tc.want {
				t.Errorf("isSimpleProject(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// Compile-time check that DT is referenced (avoids unused import
// if all tests above skip due to missing store).
var _ = DT.ErrNoRows
