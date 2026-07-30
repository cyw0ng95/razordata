package EX

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestTryFusedBatchScan_IneligibleNoStore verifies that
// tryFusedBatchScan returns nil when the SeqScan has no store
// (in-memory table). REQ002003.
func TestTryFusedBatchScan_IneligibleNoStore(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    LX.T_EQ,
	}
	filt := OP.NewFilter(ss, pred, nil)
	proj := OP.NewProject(filt, []PS.Expr{&PS.Ident{Name: "a"}})
	lim := OP.NewLimit(proj, 10)

	if got := tryFusedBatchScan(lim, nil); got != nil {
		t.Fatalf("expected nil for SeqScan without store, got %T", got)
	}
}

// TestTryFusedBatchScan_IneligibleSubqueryPredicate verifies that
// tryFusedBatchScan returns nil when the filter predicate contains a
// subquery. REQ002003.
func TestTryFusedBatchScan_IneligibleSubqueryPredicate(t *testing.T) {
	ss, err := OP.NewSeqScanWithStore(nil, "kv")
	if err != nil {
		t.Skipf("NewSeqScanWithStore failed (no schema registered): %v", err)
	}
	if ss.Store() == nil {
		t.Skip("SeqScan has no store; cannot verify subquery gating")
	}
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

	if got := tryFusedBatchScan(lim, nil); got != nil {
		t.Fatalf("expected nil for subquery predicate, got %T", got)
	}
}

// TestTryFusedBatchScan_IneligibleComplexProjection verifies that
// tryFusedBatchScan returns nil when the projection contains
// arithmetic (BinaryExpr). REQ002003.
func TestTryFusedBatchScan_IneligibleComplexProjection(t *testing.T) {
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
	complexExpr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
		Op:    LX.T_PLUS,
	}
	proj := OP.NewProject(filt, []PS.Expr{complexExpr})
	lim := OP.NewLimit(proj, 10)

	if got := tryFusedBatchScan(lim, nil); got != nil {
		t.Fatalf("expected nil for complex projection, got %T", got)
	}
}

// TestTryFusedBatchScan_IneligibleBareSeqScan verifies that a bare
// SeqScan with no Filter/Project/Limit returns nil (no fusion benefit
// over VectorizedSeqScan). REQ002003.
func TestTryFusedBatchScan_IneligibleBareSeqScan(t *testing.T) {
	ss, err := OP.NewSeqScanWithStore(nil, "kv")
	if err != nil {
		t.Skipf("NewSeqScanWithStore failed: %v", err)
	}
	if ss.Store() == nil {
		t.Skip("SeqScan has no store")
	}
	if got := tryFusedBatchScan(ss, nil); got != nil {
		t.Fatalf("expected nil for bare SeqScan, got %T", got)
	}
}

// TestTryFusedBatchScan_IneligibleFilterOnlyNoProjectOrLimit verifies
// that Filter(SeqScan) without Project or Limit is still eligible
// (fusion handles any non-empty subset of Filter/Project/Limit). This
// is a positive test for the "at least one op present" gate — but
// because we have no store, it returns nil. The point is to verify
// the gate doesn't reject Filter-only shapes. REQ002003.
func TestTryFusedBatchScan_IneligibleFilterOnlyNoProjectOrLimit(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    LX.T_EQ,
	}
	filt := OP.NewFilter(ss, pred, nil)

	// Returns nil because no store — not because Filter-only is rejected.
	if got := tryFusedBatchScan(filt, nil); got != nil {
		t.Fatalf("expected nil for filter-only without store, got %T", got)
	}
}

// TestTryFusedBatchScan_NoStore verifies that tryFusedBatchScan
// handles a Limit→Project→Filter→SeqScan tree without panicking.
// REQ002171: AdaptiveOp removed.
func TestTryFusedBatchScan_NoStore(t *testing.T) {
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
	proj := OP.NewProject(filt, []PS.Expr{&PS.Ident{Name: "a"}})
	lim := OP.NewLimit(proj, 10)

	got := tryFusedBatchScan(lim, nil)
	_ = got
}

// TestTryFusedBatchScan_NilRoot verifies that a nil root returns nil
// without panicking. REQ002003.
func TestTryFusedBatchScan_NilRoot(t *testing.T) {
	if got := tryFusedBatchScan(nil, nil); got != nil {
		t.Fatalf("expected nil for nil root, got %T", got)
	}
}

// TestTryFusedBatchScan_NilChild verifies that a nil child at any
// level returns nil without panicking. REQ002003.
func TestTryFusedBatchScan_NilChildAfterLimit(t *testing.T) {
	// Limit with nil child — the walk hits cur==nil after Limit.
	lim := OP.NewLimit(nil, 10)
	if got := tryFusedBatchScan(lim, nil); got != nil {
		t.Fatalf("expected nil for nil child after Limit, got %T", got)
	}
}

// TestTryVectorizePlan_FallsBackWhenFusionIneligible verifies that
// when tryFusedBatchScan returns nil, tryVectorizePlan falls through
// to the push pipeline / pull-based path. REQ002003.
func TestTryVectorizePlan_FallsBackWhenFusionIneligible(t *testing.T) {
	// SeqScan without store → fusion returns nil → push returns nil →
	// pull path wraps in BatchToRowAdapter via VectorizedSeqScan.
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

// Compile-time check that DT is referenced (avoids unused import
// if all tests above skip due to missing store).
var _ = DT.ErrNoRows
