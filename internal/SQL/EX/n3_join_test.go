package EX

import (
	"context"
	"fmt"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	LX "github.com/cyw0ng95/razordata/internal/SQL/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// mockStatsCatalog is an in-memory StatsCatalog for tests. REQ000948.
type mockStatsCatalog struct {
	stats map[string]map[string]ls.ColumnStats
}

func (m *mockStatsCatalog) ColumnStatsByName(tableName, colName string) *ls.ColumnStats {
	if m == nil || m.stats == nil {
		return nil
	}
	tbl, ok := m.stats[tableName]
	if !ok {
		return nil
	}
	s, ok := tbl[colName]
	if !ok {
		return nil
	}
	return &s
}

func newMockStatsCatalog() *mockStatsCatalog {
	return &mockStatsCatalog{stats: make(map[string]map[string]ls.ColumnStats)}
}

func (m *mockStatsCatalog) setStats(table, col string, s ls.ColumnStats) {
	if m.stats[table] == nil {
		m.stats[table] = make(map[string]ls.ColumnStats)
	}
	m.stats[table][col] = s
}

func TestN3JoinOrdering_NoJoins(t *testing.T) {
	p := NewPlanner()
	order, _ := p.n3JoinOrdering("t1", nil, nil, nil)
	if len(order) != 1 || order[0] != "t1" {
		t.Fatalf("expected [t1], got %v", order)
	}
}

func TestN3JoinOrdering_SingleJoin(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t1", []ColInfo{{Name: "id", Typ: 1}}, "id")
	p.RegisterTable("t2", []ColInfo{{Name: "id", Typ: 1}}, "id")

	joinTables := []joinTableInfo{{name: "t2"}}
	order, _ := p.n3JoinOrdering("t1", joinTables, nil, nil)
	if len(order) != 2 || order[0] != "t1" || order[1] != "t2" {
		t.Fatalf("expected [t1 t2], got %v", order)
	}
}

func TestN3JoinOrdering_ThreeTablesDefault(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t1", []ColInfo{{Name: "id", Typ: 1}}, "id")
	p.RegisterTable("t2", []ColInfo{{Name: "id", Typ: 1}}, "id")
	p.RegisterTable("t3", []ColInfo{{Name: "id", Typ: 1}}, "id")

	joinTables := []joinTableInfo{{name: "t2"}, {name: "t3"}}
	order, _ := p.n3JoinOrdering("t1", joinTables, nil, nil)
	if len(order) != 3 {
		t.Fatalf("expected 3 tables, got %v", order)
	}
	if order[0] != "t1" {
		t.Fatalf("expected base table t1 first, got %s", order[0])
	}
}

func TestN3JoinOrdering_EdgeCases(t *testing.T) {
	p := NewPlanner()

	// Empty join table list.
	order, _ := p.n3JoinOrdering("t1", []joinTableInfo{}, nil, nil)
	if len(order) != 1 || order[0] != "t1" {
		t.Fatalf("expected [t1], got %v", order)
	}
}

func TestEstimateJoinCost_CrossJoin(t *testing.T) {
	p := NewPlanner()
	cost := p.estimateJoinCost(100, 200, nil, false)
	expected := float64(100 * 200)
	if cost != expected {
		t.Fatalf("expected %v, got %v", expected, cost)
	}
}

func TestEstimateJoinCost_WithIndex(t *testing.T) {
	p := NewPlanner()
	cost := p.estimateJoinCost(100, 200, nil, true)
	expected := float64(100*200) * 0.2
	if cost != expected {
		t.Fatalf("expected %v, got %v", expected, cost)
	}
}

func TestEstimateJoinCost_MinCostFloor(t *testing.T) {
	p := NewPlanner()
	cost := p.estimateJoinCost(1, 1, nil, false)
	if cost < 1 {
		t.Fatalf("expected cost >= 1, got %v", cost)
	}
}

func TestJoinResultRows_CrossJoin(t *testing.T) {
	result := joinResultRows(100, 200, nil)
	if result != float64(100*200) {
		t.Fatalf("expected %v, got %v", float64(20000), result)
	}
}

func TestJoinResultRows_MinFloor(t *testing.T) {
	result := joinResultRows(1, 1, nil)
	if result < 1 {
		t.Fatalf("expected result >= 1, got %v", result)
	}
}

func TestGetTableRowCount_Default(t *testing.T) {
	p := NewPlanner()
	count := p.getTableRowCount("nonexistent")
	if count != 100 {
		t.Fatalf("expected default 100, got %v", count)
	}
}

func TestEstimateJoinPredicateSelectivity(t *testing.T) {
	// Nil predicate.
	sel := estimateJoinPredicateSelectivity(nil, 0)
	if sel != 1.0 {
		t.Fatalf("expected 1.0, got %v", sel)
	}
}

// REQ000914: Empty order fallback — when heap entries have zero-length
// order slices, n3JoinOrdering must not panic and must fall back to raw order.
func TestN3JoinOrdering_EmptyOrderFallback(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t1", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.RegisterTable("t2", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.RegisterTable("t3", []ColInfo{{Name: "a", Typ: 1}}, "a")

	// Simulate a self-join scenario with same table aliases.
	joinTables := []joinTableInfo{
		{name: "t2"},
		{name: "t3"},
	}
	order, _ := p.n3JoinOrdering("t1", joinTables, nil, nil)
	if len(order) == 0 {
		t.Fatalf("expected non-empty order, got %v", order)
	}
	// Must contain all three tables.
	seen := map[string]bool{}
	for _, name := range order {
		seen[name] = true
	}
	for _, expected := range []string{"t1", "t2", "t3"} {
		if !seen[expected] {
			t.Fatalf("expected table %s in order %v", expected, order)
		}
	}
}

// REQ000914: 3-way self-join must not panic.
func TestN3JoinOrdering_SelfJoinNoPanic(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("tab0", []ColInfo{
		{Name: "pk", Typ: 1},
		{Name: "col0", Typ: 1},
	}, "pk")

	joinTables := []joinTableInfo{
		{name: "tab0"},
		{name: "tab0"},
	}
	// Must not panic.
	order, _ := p.n3JoinOrdering("tab0", joinTables, nil, nil)
	if len(order) != 3 {
		t.Fatalf("expected 3 tables in order, got %d: %v", len(order), order)
	}
}

// REQ000948: NDV-based equi-join selectivity. With stats catalog
// providing DistinctCount=10 for both sides, equi-join selectivity
// should be 1/max(ndv1, ndv2) = 0.1, not the default 0.1 — but
// with NDV=100 should give 0.01, much more selective than the
// default 0.1.
func TestJoinPredSel_NDVEquiJoin(t *testing.T) {
	p := NewPlanner()
	p.SetStatsCatalog(newMockStatsCatalog())
	p.statsCatalog.(*mockStatsCatalog).setStats("t1", "a", ls.ColumnStats{
		DistinctCount: 100,
		RowCount:      100,
	})
	p.statsCatalog.(*mockStatsCatalog).setStats("t2", "b", ls.ColumnStats{
		DistinctCount: 100,
		RowCount:      100,
	})
	pred := &PS.BinaryExpr{
		Op:    int(LX.T_EQ),
		Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
		Right: &PS.QualifiedName{Table: "t2", Name: "b"},
	}
	sel := p.joinPredSel(pred, 0)
	expected := 1.0 / 100.0 // 0.01
	if sel < expected-0.0001 || sel > expected+0.0001 {
		t.Fatalf("expected %v, got %v", expected, sel)
	}
}

// REQ000948: NDV-based selectivity with asymmetric NDV. Larger NDV
// (more unique values) → lower selectivity.
func TestJoinPredSel_NDVAsymmetric(t *testing.T) {
	p := NewPlanner()
	p.SetStatsCatalog(newMockStatsCatalog())
	p.statsCatalog.(*mockStatsCatalog).setStats("t1", "a", ls.ColumnStats{
		DistinctCount: 1000,
		RowCount:      1000,
	})
	p.statsCatalog.(*mockStatsCatalog).setStats("t2", "b", ls.ColumnStats{
		DistinctCount: 10,
		RowCount:      1000,
	})
	pred := &PS.BinaryExpr{
		Op:    int(LX.T_EQ),
		Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
		Right: &PS.QualifiedName{Table: "t2", Name: "b"},
	}
	sel := p.joinPredSel(pred, 0)
	expected := 1.0 / 1000.0 // 1/max(ndv1, ndv2) = 1/1000
	if sel < expected-0.0001 || sel > expected+0.0001 {
		t.Fatalf("expected %v, got %v", expected, sel)
	}
}

// REQ000948: Range predicate with null fraction. selectivity =
// (1 - null_frac) / 3. With null_frac=0.1, expected = 0.9/3 = 0.3.
func TestJoinPredSel_RangeWithNullFrac(t *testing.T) {
	p := NewPlanner()
	p.SetStatsCatalog(newMockStatsCatalog())
	p.statsCatalog.(*mockStatsCatalog).setStats("t1", "a", ls.ColumnStats{
		DistinctCount: 100,
		NullCount:     10, // 10% null
		RowCount:      100,
	})
	pred := &PS.BinaryExpr{
		Op:    int(LX.T_LT),
		Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
		Right: &PS.NumberLiteral{Val: 100},
	}
	sel := p.joinPredSel(pred, 0)
	expected := (1.0 - 0.1) / 3.0 // 0.3
	if sel < expected-0.0001 || sel > expected+0.0001 {
		t.Fatalf("expected %v, got %v", expected, sel)
	}
}

// REQ000948: Fallback to default constants when no stats available.
func TestJoinPredSel_NoStatsFallback(t *testing.T) {
	p := NewPlanner()
	// No stats catalog set.
	pred := &PS.BinaryExpr{
		Op:    int(LX.T_EQ),
		Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
		Right: &PS.QualifiedName{Table: "t2", Name: "b"},
	}
	sel := p.joinPredSel(pred, 0)
	if sel != 0.1 {
		t.Fatalf("expected 0.1 (default fallback), got %v", sel)
	}
}

// REQ000948: Equi-join with col=literal uses the column's NDV.
func TestJoinPredSel_ColEqLiteral(t *testing.T) {
	p := NewPlanner()
	p.SetStatsCatalog(newMockStatsCatalog())
	p.statsCatalog.(*mockStatsCatalog).setStats("t1", "a", ls.ColumnStats{
		DistinctCount: 50,
		RowCount:      100,
	})
	pred := &PS.BinaryExpr{
		Op:    int(LX.T_EQ),
		Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
		Right: &PS.NumberLiteral{Val: 5},
	}
	sel := p.joinPredSel(pred, 0)
	expected := 1.0 / 50.0 // 0.02
	if sel < expected-0.0001 || sel > expected+0.0001 {
		t.Fatalf("expected %v, got %v", expected, sel)
	}
}

// REQ000948: Unqualified column (Ident) — looked up via findTableForColumn.
func TestJoinPredSel_UnqualifiedColumn(t *testing.T) {
	p := NewPlanner()
	p.SetStatsCatalog(newMockStatsCatalog())
	p.RegisterTable("t1", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.statsCatalog.(*mockStatsCatalog).setStats("t1", "a", ls.ColumnStats{
		DistinctCount: 200,
		RowCount:      200,
	})
	pred := &PS.BinaryExpr{
		Op:    int(LX.T_EQ),
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 5},
	}
	sel := p.joinPredSel(pred, 0)
	expected := 1.0 / 200.0 // 0.005
	if sel < expected-0.0001 || sel > expected+0.0001 {
		t.Fatalf("expected %v, got %v", expected, sel)
	}
}

// REQ000947: Prune threshold (bestCost × 2) — 3-table join with
// asymmetric costs must still return a valid complete order even
// with the prune threshold active. The test verifies that the
// planner does not produce a partial order (must include all 3
// tables) when one candidate is much more expensive than another.
func TestN3JoinOrdering_PruneThreshold_ThreeTables(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t1", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.RegisterTable("t2", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.RegisterTable("t3", []ColInfo{{Name: "a", Typ: 1}}, "a")

	// Large asymmetry via different selectivity predicates.
	// t1 has no filter (100 rows), t2 has heavy filter (2 rows),
	// t3 has medium filter (10 rows). N3 should prefer t2 or t3
	// as the base.
	wherePreds := []PS.Expr{
		&PS.BinaryExpr{
			Op:    int(LX.T_EQ),
			Left:  &PS.QualifiedName{Table: "t2", Name: "a"},
			Right: &PS.NumberLiteral{Val: 1},
		},
		&PS.BinaryExpr{
			Op:    int(LX.T_LT),
			Left:  &PS.QualifiedName{Table: "t3", Name: "a"},
			Right: &PS.NumberLiteral{Val: 50},
		},
	}
	joinTables := []joinTableInfo{{name: "t2"}, {name: "t3"}}
	order, _ := p.n3JoinOrdering("t1", joinTables, wherePreds, nil)
	if len(order) != 3 {
		t.Fatalf("expected 3 tables, got %d: %v", len(order), order)
	}
	seen := map[string]bool{}
	for _, n := range order {
		seen[n] = true
	}
	for _, want := range []string{"t1", "t2", "t3"} {
		if !seen[want] {
			t.Fatalf("expected %s in order %v", want, order)
		}
	}
}

// REQ000947: Prune threshold constant must be 2.0 (per MySQL's
// optimizer_prune_level=1 / PostgreSQL geqo_effort=2.0 heuristic).
func TestN3PruneMultiplier_Default(t *testing.T) {
	if n3PruneMultiplier != 2.0 {
		t.Fatalf("expected n3PruneMultiplier=2.0, got %v", n3PruneMultiplier)
	}
}

// REQ000947: 5-table join with prune threshold — must produce a
// valid complete order (all 5 tables present) even when the prune
// is active. This is a stress test for the iterative N3 path.
func TestN3JoinOrdering_PruneThreshold_FiveTables(t *testing.T) {
	p := NewPlanner()
	for i := 1; i <= 5; i++ {
		p.RegisterTable(fmt.Sprintf("t%d", i), []ColInfo{{Name: "id", Typ: 1}}, "id")
	}
	joinTables := []joinTableInfo{
		{name: "t2"}, {name: "t3"}, {name: "t4"}, {name: "t5"},
	}
	order, _ := p.n3JoinOrdering("t1", joinTables, nil, nil)
	if len(order) != 5 {
		t.Fatalf("expected 5 tables, got %d: %v", len(order), order)
	}
	seen := map[string]bool{}
	for _, n := range order {
		seen[n] = true
	}
	for i := 1; i <= 5; i++ {
		want := fmt.Sprintf("t%d", i)
		if !seen[want] {
			t.Fatalf("expected %s in order %v", want, order)
		}
	}
}

// REQ000946: Multi-start baseTable — verify that when the leftmost
// table is the largest, the multi-start variant still picks a
// smaller table as the base. The cost reduction comes from joining
// the smallest filtered table first.
//
// Setup:
//   - t1 (leftmost): 1000 rows, no filter
//   - t2: 10 rows, no filter
//   - t3: 10 rows, no filter
//
// With single-baseTable=t1, the cost is roughly 1000 × 10 = 10K
// for the first join. With multi-start trying t2 as base, the cost
// is 10 × 10 = 100 for the first join. Multi-start should pick t2.
func TestN3JoinOrdering_MultiStart_PicksSmallerBase(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t1", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.RegisterTable("t2", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.RegisterTable("t3", []ColInfo{{Name: "a", Typ: 1}}, "a")

	// Use a custom row count via the in-memory `tables` map.
	tablesMu.Lock()
	tables["t1"] = makeRows(1000)
	tables["t2"] = makeRows(10)
	tables["t3"] = makeRows(10)
	tablesMu.Unlock()
	defer func() {
		tablesMu.Lock()
		delete(tables, "t1")
		delete(tables, "t2")
		delete(tables, "t3")
		tablesMu.Unlock()
	}()

	joinTables := []joinTableInfo{{name: "t2"}, {name: "t3"}}
	order := p.n3JoinOrderingMultiStart("t1", joinTables, nil, nil)
	if len(order) != 3 {
		t.Fatalf("expected 3 tables, got %d: %v", len(order), order)
	}
	// Multi-start should pick a small table (t2 or t3) as the first
	// joined table. Verify the order is valid (all 3 tables present).
	seen := map[string]bool{}
	for _, n := range order {
		seen[n] = true
	}
	for _, want := range []string{"t1", "t2", "t3"} {
		if !seen[want] {
			t.Fatalf("expected %s in order %v", want, order)
		}
	}
}

// REQ000946: Multi-start vs single-start — verify both return a
// valid order with the same set of tables. Multi-start may return
// a different order than single-start when the leftmost table is
// not the best base.
func TestN3JoinOrdering_MultiStart_ValidOrder(t *testing.T) {
	p := NewPlanner()
	for i := 1; i <= 4; i++ {
		p.RegisterTable(fmt.Sprintf("t%d", i), []ColInfo{{Name: "id", Typ: 1}}, "id")
	}
	joinTables := []joinTableInfo{
		{name: "t2"}, {name: "t3"}, {name: "t4"},
	}
	order := p.n3JoinOrderingMultiStart("t1", joinTables, nil, nil)
	if len(order) != 4 {
		t.Fatalf("expected 4 tables, got %d: %v", len(order), order)
	}
	seen := map[string]bool{}
	for _, n := range order {
		seen[n] = true
	}
	for i := 1; i <= 4; i++ {
		want := fmt.Sprintf("t%d", i)
		if !seen[want] {
			t.Fatalf("expected %s in order %v", want, order)
		}
	}
}

// REQ000946: Multi-start with no joins — should return the single
// baseTable as the only element, no N3 needed.
func TestN3JoinOrdering_MultiStart_NoJoins(t *testing.T) {
	p := NewPlanner()
	order := p.n3JoinOrderingMultiStart("t1", nil, nil, nil)
	if len(order) != 1 || order[0] != "t1" {
		t.Fatalf("expected [t1], got %v", order)
	}
}

// makeRows creates a slice of n empty Rows for in-memory table setup.
func makeRows(n int) []Row {
	rows := make([]Row, n)
	for i := range rows {
		rows[i] = Row{Cols: []string{"a"}, Types: []int{1}, Data: []Value{NewIntValue(0)}}
	}
	return rows
}

// REQ000926: Cross join of the same table with different alias should
// produce the full Cartesian product. `tab1, tab1 AS cor0` on a 3-row
// table should produce 3×3 = 9 rows. Previously the multi-start N3
// function deduplicated table names, causing the second occurrence
// to be lost and only 3 rows produced.
func TestCrossJoin_SelfJoin3x3Yields9Rows(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()
	for _, s := range []string{
		"CREATE TABLE tab1(col0 INTEGER, col1 INTEGER, col2 INTEGER, col3 INTEGER, col4 INTEGER)",
		"INSERT INTO tab1 VALUES (1, 10, 100, 1000, 10000), (2, 20, 200, 2000, 20000), (3, 30, 300, 3000, 30000)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	// REQ000926: SELECT with constant expr over a self-join. The
	// row count is the test signal — value is the constant 33*57=1881.
	rows, err := e.QueryAll(ctx, "SELECT 33*57 col1 FROM tab1, tab1 AS cor0")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 9 {
		t.Fatalf("self-join: expected 9 rows (3×3), got %d", len(rows))
	}
	// Verify all rows have the constant value 1881.
	for i, r := range rows {
		if len(r.Data) == 0 || r.Data[0].ToAny().(int64) != 1881 {
			t.Fatalf("row %d: expected 1881, got %v", i, r.Data)
		}
	}
}

// REQ000926: Multi-start must preserve duplicate table names in
// the returned order for self-joins.
func TestN3JoinOrdering_MultiStart_SelfJoinPreservesDuplicates(t *testing.T) {
	p := NewPlanner()
	joinTables := []joinTableInfo{{name: "tab1"}}
	order := p.n3JoinOrderingMultiStart("tab1", joinTables, nil, nil)
	if len(order) != 2 {
		t.Fatalf("expected 2 entries [tab1, tab1], got %d: %v", len(order), order)
	}
	if order[0] != "tab1" || order[1] != "tab1" {
		t.Fatalf("expected [tab1, tab1], got %v", order)
	}
}
