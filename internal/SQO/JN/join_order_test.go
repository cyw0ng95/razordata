package JN

import (
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type mockStatsProvider struct {
	rowCounts map[string]float64
	indexes   map[string]bool
}

func (m *mockStatsProvider) TableRowCount(table string) float64 {
	if m.rowCounts != nil {
		if v, ok := m.rowCounts[table]; ok {
			return v
		}
	}
	return 100
}

func (m *mockStatsProvider) HasIndex(table string) bool {
	if m.indexes != nil {
		return m.indexes[table]
	}
	return false
}

func (m *mockStatsProvider) JoinPredSel(pred PS.Expr, rowCount float64) float64 {
	return 0.5
}

func (m *mockStatsProvider) JoinCost(leftRows, rightRows int, predicates []PS.Expr, hasIndex bool) float64 {
	return EstimateJoinCost(leftRows, rightRows, predicates, hasIndex)
}

func (m *mockStatsProvider) JoinResultRows(leftRows, rightRows float64, predicates []PS.Expr) float64 {
	return JoinResultRows(leftRows, rightRows, predicates)
}

func (m *mockStatsProvider) CanPushDown(pred PS.Expr, table string) bool {
	return false
}

func (m *mockStatsProvider) ExtractTablesFromExpr(e PS.Expr) map[string]bool {
	return map[string]bool{}
}

func (m *mockStatsProvider) FindTableForColumn(col string) string {
	return ""
}

func TestN3_ZeroJoinTables(t *testing.T) {
	sp := &mockStatsProvider{}
	order, cost := N3("t1", nil, nil, nil, sp)
	if len(order) != 1 || order[0] != "t1" {
		t.Fatalf("expected [t1], got %v", order)
	}
	if cost != 100 {
		t.Fatalf("expected 100, got %v", cost)
	}
}

func TestN3_OneJoinTable(t *testing.T) {
	sp := &mockStatsProvider{}
	jts := []JoinTableInfo{{Name: "t2"}}
	order, cost := N3("t1", jts, nil, nil, sp)
	if len(order) != 2 || order[0] != "t1" || order[1] != "t2" {
		t.Fatalf("expected [t1 t2], got %v", order)
	}
	if cost != 200 {
		t.Fatalf("expected 200, got %v", cost)
	}
}

func TestN3_TwoJoinTables(t *testing.T) {
	sp := &mockStatsProvider{}
	jts := []JoinTableInfo{{Name: "t2"}, {Name: "t3"}}
	order, cost := N3("t1", jts, nil, nil, sp)
	if len(order) != 3 {
		t.Fatalf("expected 3 tables in order, got %v", order)
	}
	if order[0] != "t1" {
		t.Fatalf("expected t1 first, got %v", order[0])
	}
	if cost > 0 {
		t.Logf("cost = %v", cost)
	}
}

func TestN3_FallbackOnEmptyHeap(t *testing.T) {
	sp := &mockStatsProvider{
		rowCounts: map[string]float64{"t1": 10, "t2": 10, "t3": 10},
	}
	jts := []JoinTableInfo{{Name: "t2"}, {Name: "t3"}}
	order, _ := N3("t1", jts, nil, nil, sp)
	if len(order) != 3 {
		t.Fatalf("expected 3 tables in fallback order, got %v", order)
	}
}

func TestMultiStart_ZeroJoinTables(t *testing.T) {
	sp := &mockStatsProvider{}
	order := MultiStart("t1", nil, nil, nil, sp)
	if len(order) != 1 || order[0] != "t1" {
		t.Fatalf("expected [t1], got %v", order)
	}
}

func TestMultiStart_OneJoinTable(t *testing.T) {
	sp := &mockStatsProvider{}
	order := MultiStart("t1", []JoinTableInfo{{Name: "t2"}}, nil, nil, sp)
	if len(order) != 2 {
		t.Fatalf("expected 2 tables, got %v", order)
	}
}

func TestMultiStart_TwoJoinTables(t *testing.T) {
	sp := &mockStatsProvider{}
	order := MultiStart("t1", []JoinTableInfo{{Name: "t2"}, {Name: "t3"}}, nil, nil, sp)
	if len(order) != 3 {
		t.Fatalf("expected 3 tables, got %v", order)
	}
}

func TestMultiStart_NormalizesOrder(t *testing.T) {
	sp := &mockStatsProvider{}
	order := MultiStart("t1", []JoinTableInfo{{Name: "t2"}, {Name: "t3"}, {Name: "t4"}}, nil, nil, sp)
	if len(order) < 4 {
		t.Fatalf("expected 4 tables, got %v", order)
	}
	if order[0] != "t1" {
		t.Fatalf("expected order[0]=t1, got %v", order[0])
	}
}

func TestGroupBushy_ThreeTablesReturnsOneGroup(t *testing.T) {
	groups := GroupBushy("t1", []string{"t1", "t2", "t3"}, nil)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group for 3 tables, got %v", groups)
	}
}

func TestGroupBushy_FourTablesNoCrossPreds(t *testing.T) {
	groups := GroupBushy("t1", []string{"t1", "t2", "t3", "t4"}, nil)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups for 4 independent tables, got %v", groups)
	}
}

func TestGroupBushy_ConnectedGraph(t *testing.T) {
	preds := []PS.Expr{
		&PS.BinaryExpr{
			Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
			Right: &PS.QualifiedName{Table: "t2", Name: "a"},
			Op:    1, // T_EQ
		},
		&PS.BinaryExpr{
			Left:  &PS.QualifiedName{Table: "t2", Name: "b"},
			Right: &PS.QualifiedName{Table: "t3", Name: "b"},
			Op:    1,
		},
	}
	groups := GroupBushy("t1", []string{"t1", "t2", "t3", "t4"}, preds)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups for 4 tables with connected subgraph via t1-t2-t3, got %v", groups)
	}
}

func TestEstimateJoinCost_NoIndex(t *testing.T) {
	cost := EstimateJoinCost(100, 200, nil, false)
	if cost != 20000 {
		t.Fatalf("expected 20000, got %v", cost)
	}
}

func TestEstimateJoinCost_WithIndex(t *testing.T) {
	cost := EstimateJoinCost(100, 200, nil, true)
	if cost != 300 {
		t.Fatalf("expected 300, got %v", cost)
	}
}

func TestEstimateJoinCost_ZeroRows(t *testing.T) {
	cost := EstimateJoinCost(0, 0, nil, false)
	if cost != 1 {
		t.Fatalf("expected 1 (min floor), got %v", cost)
	}
}

func TestJoinResultRows_NoPredicates(t *testing.T) {
	rows := JoinResultRows(100, 200, nil)
	if rows != 20000 {
		t.Fatalf("expected 20000, got %v", rows)
	}
}

func TestJoinResultRows_WithPredicates(t *testing.T) {
	preds := []PS.Expr{&PS.BinaryExpr{Left: &PS.Ident{Name: "a"}, Right: &PS.Ident{Name: "b"}, Op: 1}}
	rows := JoinResultRows(100, 200, preds)
	if rows < 1 {
		t.Fatalf("expected positive rows, got %v", rows)
	}
}

func TestJoinResultRows_MinFloor(t *testing.T) {
	rows := JoinResultRows(1, 1, []PS.Expr{&PS.Ident{Name: "a"}})
	if rows < 1 {
		t.Fatalf("expected min floor 1, got %v", rows)
	}
}

func TestN3PredCacheKey(t *testing.T) {
	joined := map[string]bool{"a": true, "b": true}
	key := n3PredCacheKey(joined, "c")
	expected := "a\x00b\x00c"
	if key != expected {
		t.Fatalf("expected %q, got %q", expected, key)
	}
}

func TestExtractTableColumn_QualifiedName(t *testing.T) {
	table, col := ExtractTableColumn(&PS.QualifiedName{Table: "t1", Name: "a"})
	if table != "t1" || col != "a" {
		t.Fatalf("expected t1.a, got %s.%s", table, col)
	}
}

func TestExtractTableColumn_Ident(t *testing.T) {
	table, col := ExtractTableColumn(&PS.Ident{Name: "a"})
	if table != "" || col != "a" {
		t.Fatalf("expected .a, got %s.%s", table, col)
	}
}

func TestIsConnectedGraph_Empty(t *testing.T) {
	if !IsConnectedGraph(nil, nil) {
		t.Fatal("expected true for empty")
	}
}

func TestIsConnectedGraph_Single(t *testing.T) {
	if !IsConnectedGraph([]string{"t1"}, nil) {
		t.Fatal("expected true for single table")
	}
}

func TestFilteredRowCount(t *testing.T) {
	sp := &mockStatsProvider{}
	rows := FilteredRowCount("t1", nil, sp)
	if rows != 100 {
		t.Fatalf("expected 100, got %v", rows)
	}
}

func TestEstimateJoinOrderCost_Empty(t *testing.T) {
	sp := &mockStatsProvider{}
	cost := EstimateJoinOrderCost(nil, nil, sp)
	if cost != 0 {
		t.Fatalf("expected 0, got %v", cost)
	}
}

func TestEstimateJoinOrderCost_SingleTable(t *testing.T) {
	sp := &mockStatsProvider{}
	cost := EstimateJoinOrderCost([]string{"t1"}, nil, sp)
	if cost != 100 {
		t.Fatalf("expected 100, got %v", cost)
	}
}

func TestExhaustiveJoinOrder_ZeroTables(t *testing.T) {
	sp := &mockStatsProvider{}
	order := ExhaustiveJoinOrder("t1", nil, nil, sp)
	if len(order) != 1 || order[0] != "t1" {
		t.Fatalf("expected [t1], got %v", order)
	}
}

func TestExhaustiveJoinOrder_TwoTables(t *testing.T) {
	sp := &mockStatsProvider{}
	order := ExhaustiveJoinOrder("t1", []JoinTableInfo{{Name: "t2"}, {Name: "t3"}}, nil, sp)
	if len(order) != 3 || order[0] != "t1" {
		t.Fatalf("expected 3 tables starting with t1, got %v", order)
	}
}