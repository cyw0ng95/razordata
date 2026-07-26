package EX

import (
	"context"
	"fmt"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestMinMax_Indexed_Correctness verifies REQ001247: MIN(col) and
// MAX(col) on a column with an index return the correct value.
// The optimization is deferred (see planner_minmax.go file-level
// comment) so this test exercises the SeqScan+Aggregate path
// that the planner still produces today. Once the index encoding
// is fixed and the optimization is enabled, this test should
// keep passing.
func TestMinMax_Indexed_Correctness(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t_mm", []string{"id", "a"}, "id")
	ex.RegisterIndex("t_mm", "idx_a", []string{"a"})

	ctx := context.Background()
	seed := []int64{50, 10, 30, 90, 20, 70, 40, 80, 60}
	for i, v := range seed {
		if _, err := ex.Exec(ctx, fmt.Sprintf("INSERT INTO t_mm VALUES (%d, %d)", i+1, v)); err != nil {
			t.Fatal(err)
		}
	}

	// MIN(a) — must return 10 regardless of optimization status.
	rows, err := ex.QueryAll(ctx, "SELECT MIN(a) FROM t_mm")
	if err != nil {
		t.Fatalf("MIN: %v", err)
	}
	if len(rows) != 1 || len(rows[0].Data) == 0 {
		t.Fatalf("MIN(a) returned unexpected shape: %v", rows)
	}
	got := rows[0].Data[0].ToAny().(int64)
	if got != 10 {
		t.Errorf("MIN(a) = %d, want 10", got)
	}

	rows, err = ex.QueryAll(ctx, "SELECT MAX(a) FROM t_mm")
	if err != nil {
		t.Fatalf("MAX: %v", err)
	}
	got = rows[0].Data[0].ToAny().(int64)
	if got != 90 {
		t.Errorf("MAX(a) = %d, want 90", got)
	}
}

// TestMinMax_Indexed_PlanUsesIndex verifies the plan shape: MIN(a)
// is NOT routed through IndexScan + Limit today because the
// underlying secondary-index encoding (currently duplicated
// value/pk) would produce wrong row fetches. The optimization is
// guarded by `tryMinMaxIndexScan` returning nil; the planner falls
// back to SeqScan + Aggregate. Once the index encoding is
// corrected, this test should be inverted to require Limit+IndexScan.
func TestMinMax_Indexed_PlanUsesIndex(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t_mm_p", []string{"id", "a"}, "id")
	id, _ := DT.TableIDFor("t_mm_p")
	ex.RegisterIndex("t_mm_p", "idx_a", []string{"a"})
	DT.RegisterIndexWithID("t_mm_p", DT.RegisteredIndex{
		Name: "idx_a", Columns: []string{"a"},
	})
	ctx := context.Background()
	for i, v := range []int64{50, 10, 30} {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t_mm_p VALUES (%d, %d)", i+1, v))
	}
	idxStore := ls.NewIndexStore(eng, id, "idx_a")
	for _, v := range []int64{50, 10, 30} {
		idxStore.Insert([]byte(fmt.Sprintf("%d", v)), []byte(fmt.Sprintf("%d", v)))
	}

	s := mustParseSelect(t, "SELECT MIN(a) FROM t_mm_p")
	// Plan-level hook must decline to rewrite today.
	op := ex.planner.tryMinMaxIndexScan(s)
	if op != nil {
		t.Errorf("tryMinMaxIndexScan should return nil until the index-encoding fix lands; got %T", op)
	}

	// EXPLAIN should NOT contain a Limit-wrapper for MIN today.
	result, err := ex.planner.ParseAndPlan("SELECT MIN(a) FROM t_mm_p")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if containsLimit(result.Root) {
		t.Errorf("MIN plan should NOT include LIMIT 1 today (optimization is deferred); got tree that contains Limit")
	}
}

// TestMinMax_NoIndexFallback: without an index on the column, the
// planner must fall back to SeqScan + Aggregate. This ensures the
// optimization does NOT break when no index is available.
func TestMinMax_NoIndexFallback(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t_mm_ni", []string{"id", "a"}, "id")
	// No index!
	ctx := context.Background()
	for i, v := range []int64{50, 10, 30} {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t_mm_ni VALUES (%d, %d)", i+1, v))
	}
	rows, err := ex.QueryAll(ctx, "SELECT MIN(a) FROM t_mm_ni")
	if err != nil {
		t.Fatalf("MIN: %v", err)
	}
	if got := rows[0].Data[0].ToAny().(int64); got != 10 {
		t.Errorf("MIN(a) = %d, want 10", got)
	}
}

// TestMinMax_DetectEligibility unit-tests the eligibility rules of
// the REQ001247 rewrite. Cases that should NOT trigger the
// optimization (returning ok=false) confirm correctness of the
// detection helper.
func TestMinMax_DetectEligibility(t *testing.T) {
	cases := []struct {
		name    string
		sql     string
		wantOK  bool
		wantMin bool // when wantOK=true, whether the result is MIN
	}{
		{"MIN col", "SELECT MIN(a) FROM t", true, true},
		{"MAX col", "SELECT MAX(a) FROM t", true, false},
		{"WITH where", "SELECT MIN(a) FROM t WHERE a > 5", false, false},
		{"WITH groupby", "SELECT MIN(a) FROM t GROUP BY a", false, false},
		{"WITH orderby", "SELECT MIN(a) FROM t ORDER BY a", false, false},
		{"WITH distinct", "SELECT MIN(DISTINCT a) FROM t", false, false},
		{"non-min-max", "SELECT SUM(a) FROM t", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := mustParseSelect(t, c.sql)
			_, isMin, ok := detectMinMaxAggregate(s)
			if ok != c.wantOK {
				t.Errorf("detectMinMaxAggregate ok = %v, want %v (isMin=%v)", ok, c.wantOK, isMin)
			}
			if c.wantOK && isMin != c.wantMin {
				t.Errorf("isMin = %v, want %v", isMin, c.wantMin)
			}
		})
	}
}

// mustParseSelect parses the SQL into a *PS.Select via the
// package-level Parser. Used by unit tests that exercise the
// detection helpers without spinning up a full executor.
func mustParseSelect(t *testing.T, sql string) *PS.Select {
	t.Helper()
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel, ok := stmt.(*PS.Select)
	if !ok {
		t.Fatalf("not a SELECT: %T", stmt)
	}
	return sel
}

// containsIndexScan walks the operator tree and reports whether
// any OP.IndexScan (or OP.IndexOnlyScan) is present. Used to
// verify the MIN/MAX optimization rewrote the plan.
func containsIndexScan(op DT.Operator) bool {
	if op == nil {
		return false
	}
	switch op.(type) {
	case *OP.IndexScan, *OP.IndexOnlyScan, *OP.IndexSeekScan:
		return true
	}
	if c, ok := op.(interface{ Child() DT.Operator }); ok {
		if containsIndexScan(c.Child()) {
			return true
		}
	}
	if lr, ok := op.(interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}); ok {
		if containsIndexScan(lr.LeftChild()) || containsIndexScan(lr.RightChild()) {
			return true
		}
	}
	return false
}

// containsLimit reports whether any *OP.Limit is in the tree.
func containsLimit(op DT.Operator) bool {
	if op == nil {
		return false
	}
	if _, ok := op.(*OP.Limit); ok {
		return true
	}
	if c, ok := op.(interface{ Child() DT.Operator }); ok {
		if containsLimit(c.Child()) {
			return true
		}
	}
	if lr, ok := op.(interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}); ok {
		if containsLimit(lr.LeftChild()) || containsLimit(lr.RightChild()) {
			return true
		}
	}
	return false
}
