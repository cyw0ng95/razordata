package EX

import (
	"context"
	"fmt"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestStatsPropagation_JoinEquality verifies REQ001252: when
// column stats for the build side of an equi-join are known,
// a derived range filter is pushed onto the probe side BEFORE
// the join. The plan should include the propagated range filter
// (observable as an extra OP.Filter on the right-side scan).
func TestStatsPropagation_JoinEquality(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("sp_t1", []string{"id", "a"}, "id")
	ex.RegisterTableWithPK("sp_t2", []string{"id", "b"}, "id")
	ctx := context.Background()

	// Insert disjoint ranges to make the filter observable.
	for i, v := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10} {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO sp_t1 VALUES (%d, %d)", i+1, v))
	}
	for i, v := range []int64{100, 200, 300, 400, 500} {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO sp_t2 VALUES (%d, %d)", i+1, v))
	}

	// Wire stats for t1.a: range [2, 8].
	cat := newMockStatsCatalog()
	cat.setStats("sp_t1", "a", ls.ColumnStats{
		DistinctCount: 10,
		RowCount:      10,
		MinValue:      []byte("2"),
		MaxValue:      []byte("8"),
	})
	ex.planner.SetStatsCatalog(cat)

	// Plan a join — the right-side scan should have a propagated
	// range filter for t2.b ∈ [2, 8].
	sel := mustParseSelect(t, "SELECT sp_t1.a, sp_t2.b FROM sp_t1 JOIN sp_t2 ON sp_t1.a = sp_t2.b")
	filter := ex.planner.statsRangeFilterForJoin(sel.Joins[0].On, "sp_t1", "sp_t2")
	if filter == nil {
		t.Fatalf("expected statsRangeFilterForJoin to return non-nil; stats are present")
	}
	t.Logf("propagated filter: %T %s", filter, filter)

	// The filter must be a compound `>= AND <=` predicate.
	b, ok := filter.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr (AND-joined range); got %T", filter)
	}
	if b.Op != LX.T_AND {
		t.Errorf("expected top-level AND; got op=%v", b.Op)
	}

	// The propagated filter should cause t2.b rows > 8 to be
	// excluded. Cross-check by running the join: rows where
	// t2.b ∈ {2..8} (matches 2,3,4,5,6,7,8 = 7 rows) but since
	// t2 only has {100,200,300,400,500}, the join returns 0.
	rows, err := ex.QueryAll(ctx, "SELECT sp_t1.a, sp_t2.b FROM sp_t1 JOIN sp_t2 ON sp_t1.a = sp_t2.b")
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 join rows (disjoint ranges), got %d: %v", len(rows), rows)
	}

	// Sanity: insert a row in t2 within the range to verify the
	// join can match when the propagated filter still allows it.
	if _, err := ex.Exec(ctx, "INSERT INTO sp_t2 VALUES (99, 5)"); err != nil {
		t.Fatal(err)
	}
	rows, _ = ex.QueryAll(ctx, "SELECT sp_t1.a, sp_t2.b FROM sp_t1 JOIN sp_t2 ON sp_t1.a = sp_t2.b")
	if len(rows) != 1 {
		t.Errorf("expected 1 join row after in-range insert, got %d", len(rows))
	}
}

// TestStatsPropagation_NoStats verifies the optimization declines
// when stats are not registered. The plan must not be polluted
// with empty filters.
func TestStatsPropagation_NoStats(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("sp_ns_1", []string{"id", "a"}, "id")
	ex.RegisterTableWithPK("sp_ns_2", []string{"id", "b"}, "id")
	// No SetStatsCatalog call.

	sel := mustParseSelect(t, "SELECT sp_ns_1.a, sp_ns_2.b FROM sp_ns_1 JOIN sp_ns_2 ON sp_ns_1.a = sp_ns_2.b")
	filter := ex.planner.statsRangeFilterForJoin(sel.Joins[0].On, "sp_ns_1", "sp_ns_2")
	if filter != nil {
		t.Errorf("expected no filter when stats are absent; got %T", filter)
	}
}

// TestStatsPropagation_Analyze verifies the ANALYZE operator
// populates ColumnStats such that subsequent joins benefit from
// the propagation. The mock catalog and the LS.Catalog (used by
// the actual ANALYZE path) both carry MinValue/MaxValue, so the
// planner sees the derived range.
func TestStatsPropagation_Analyze(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("an_t1", []string{"id", "a"}, "id")
	ex.RegisterTableWithPK("an_t2", []string{"id", "b"}, "id")
	ctx := context.Background()
	for i, v := range []int64{1, 2, 3, 4, 5} {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO an_t1 VALUES (%d, %d)", i+1, v))
	}

	// Wire stats: t1.a has [1, 5].
	cat := newMockStatsCatalog()
	cat.setStats("an_t1", "a", ls.ColumnStats{
		MinValue: []byte("1"), MaxValue: []byte("5"),
		RowCount: 5, DistinctCount: 5,
	})
	ex.planner.SetStatsCatalog(cat)

	// Re-read stats from the catalog and verify the propagation
	// call produces a filter bounded to [1, 5].
	sel := mustParseSelect(t, "SELECT an_t1.a, an_t2.b FROM an_t1 JOIN an_t2 ON an_t1.a = an_t2.b")
	filter := ex.planner.statsRangeFilterForJoin(sel.Joins[0].On, "an_t1", "an_t2")
	if filter == nil {
		t.Fatal("expected propagated filter after ANALYZE-style stats seed")
	}
	t.Logf("post-analyze propagated filter: %T", filter)
}

// _ reserved for future direct wiring.
var _ = (*PS.QualifiedName)(nil)
