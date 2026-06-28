package PL

import (
	"errors"
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// Cost model tests

func TestPlan_CostModel_BasicSelect(t *testing.T) {
	// Simulate cost estimation: scan(100) + filter(0.1 * 100) = 110
	pl := NewPlannerWith(PlanOptions{
		BuildTree: func(s PS.Stmt) (float64, error) {
			switch v := s.(type) {
			case *PS.Select:
				cost := 100.0 // base scan cost
				if v.Where != nil {
					cost *= 1.1 // filter overhead
				}
				return cost, nil
			}
			return 0, nil
		},
	})
	stmt := &PS.Select{From: "t", Where: &PS.NumberLiteral{Val: 1}}
	plan, err := pl.Plan(stmt)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cost < 109.0 || plan.Cost > 111.0 {
		t.Errorf("Cost = %v, want ~110", plan.Cost)
	}
}

func TestPlan_CostModel_OrderBy(t *testing.T) {
	pl := NewPlannerWith(PlanOptions{
		BuildTree: func(s PS.Stmt) (float64, error) {
			if v, ok := s.(*PS.Select); ok {
				cost := 50.0
				if len(v.OrderBy) > 0 {
					cost += 200.0 // sort overhead
				}
				return cost, nil
			}
			return 0, nil
		},
	})
	withSort := &PS.Select{From: "t", OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "x"}}}}
	noSort := &PS.Select{From: "t"}

	p1, _ := pl.Plan(withSort)
	p2, _ := pl.Plan(noSort)

	if p1.Cost <= p2.Cost {
		t.Errorf("expected ORDER BY cost (%v) > no-ORDER BY cost (%v)", p1.Cost, p2.Cost)
	}
}

func TestPlan_CostModel_Limit(t *testing.T) {
	pl := NewPlannerWith(PlanOptions{
		BuildTree: func(s PS.Stmt) (float64, error) {
			if v, ok := s.(*PS.Select); ok {
				if v.Limit != nil {
					return 10.0, nil // LIMIT reduces cost
				}
				return 1000.0, nil
			}
			return 0, nil
		},
	})
	limited := &PS.Select{From: "t", Limit: &PS.NumberLiteral{Val: 10}}
	full := &PS.Select{From: "t"}

	p1, _ := pl.Plan(limited)
	p2, _ := pl.Plan(full)

	if p1.Cost >= p2.Cost {
		t.Errorf("LIMIT cost (%v) should be < full scan cost (%v)", p1.Cost, p2.Cost)
	}
}

// Index selection tests

func TestPlan_IndexSelection_EqualityPredicate(t *testing.T) {
	// Simulate: equality on indexed col -> cost 5 (index seek)
	//           no predicate -> cost 100 (full scan)
	pl := NewPlannerWith(PlanOptions{
		BuildTree: func(s PS.Stmt) (float64, error) {
			if v, ok := s.(*PS.Select); ok {
				if isEqualityOnIndexedCol(v.Where) {
					return 5.0, nil
				}
				return 100.0, nil
			}
			return 0, nil
		},
	})
	indexed := &PS.Select{From: "users", Where: &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    LX.T_EQ,
		Right: &PS.NumberLiteral{Val: 42},
	}}
	plan, _ := pl.Plan(indexed)
	if plan.Cost != 5.0 {
		t.Errorf("equality on indexed col should pick index seek (cost 5), got %v", plan.Cost)
	}
}

func TestPlan_IndexSelection_RangePredicate(t *testing.T) {
	pl := NewPlannerWith(PlanOptions{
		BuildTree: func(s PS.Stmt) (float64, error) {
			if v, ok := s.(*PS.Select); ok {
				if isRangeOnIndexedCol(v.Where) {
					return 20.0, nil // range scan
				}
				return 100.0, nil
			}
			return 0, nil
		},
	})
	rangeQ := &PS.Select{From: "users", Where: &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    LX.T_GT,
		Right: &PS.NumberLiteral{Val: 10},
	}}
	plan, _ := pl.Plan(rangeQ)
	if plan.Cost != 20.0 {
		t.Errorf("range on indexed col should pick range scan (cost 20), got %v", plan.Cost)
	}
}

func TestPlan_IndexSelection_Composite(t *testing.T) {
	pl := NewPlannerWith(PlanOptions{
		BuildTree: func(s PS.Stmt) (float64, error) {
			if v, ok := s.(*PS.Select); ok {
				if hasCompositeIndexMatch(v.Where, []string{"a", "b"}) {
					return 5.0, nil
				}
				return 100.0, nil
			}
			return 0, nil
		},
	})
	composite := &PS.Select{From: "t", Where: &PS.BinaryExpr{
		Left: &PS.BinaryExpr{
			Left:  &PS.Ident{Name: "a"},
			Op:    LX.T_EQ,
			Right: &PS.NumberLiteral{Val: 1},
		},
		Op:    LX.T_AND,
		Right: &PS.BinaryExpr{Left: &PS.Ident{Name: "b"}, Op: LX.T_EQ, Right: &PS.NumberLiteral{Val: 2}},
	}}
	plan, _ := pl.Plan(composite)
	if plan.Cost != 5.0 {
		t.Errorf("composite index match should pick index (cost 5), got %v", plan.Cost)
	}
}

// Plan caching tests

func TestPlan_Cache_HitReturnsSame(t *testing.T) {
	pl := NewPlannerWith(PlanOptions{
		BuildTree: func(PS.Stmt) (float64, error) { return 1.0, nil },
	})
	stmt := &PS.Select{From: "t"}
	p1, _ := pl.Plan(stmt)
	p2, _ := pl.Plan(stmt)
	if p1.MemoKey != p2.MemoKey {
		t.Errorf("cached plans should share MemoKey, got %q vs %q", p1.MemoKey, p2.MemoKey)
	}
}

func TestPlan_Cache_DifferentASTsDifferentKeys(t *testing.T) {
	pl := NewPlanner()
	a := &PS.Select{From: "t"}
	b := &PS.Select{From: "u"}
	pa, _ := pl.Plan(a)
	pb, _ := pl.Plan(b)
	if pa.MemoKey == pb.MemoKey {
		t.Error("different ASTs should produce different MemoKeys")
	}
	if pl.Memo().Len() != 2 {
		t.Errorf("memo should hold 2 plans, got %d", pl.Memo().Len())
	}
}

func TestPlan_Cache_BuildError(t *testing.T) {
	wantErr := errors.New("build failed")
	pl := NewPlannerWith(PlanOptions{
		BuildTree: func(PS.Stmt) (float64, error) { return 0, wantErr },
	})
	_, err := pl.Plan(&PS.Select{From: "t"})
	if !errors.Is(err, wantErr) {
		t.Errorf("expected build error, got %v", err)
	}
	if pl.Memo().Len() != 0 {
		t.Errorf("failed plan should not be cached, got %d entries", pl.Memo().Len())
	}
}

func TestPlan_Cache_NotStaleOnReinsert(t *testing.T) {
	pl := NewPlannerWith(PlanOptions{
		BuildTree: func(PS.Stmt) (float64, error) { return 1.0, nil },
	})
	stmt := &PS.Select{From: "t"}
	pl.Plan(stmt)
	pl.Plan(stmt)
	if pl.Memo().Len() != 1 {
		t.Errorf("duplicate plans should not inflate memo, got %d", pl.Memo().Len())
	}
}

// Memo edge cases

func TestMemo_OverwriteValue(t *testing.T) {
	m := NewMemo()
	m.Put("k", Plan{Cost: 1.0, MemoKey: "k"})
	m.Put("k", Plan{Cost: 2.0, MemoKey: "k"})
	got, _ := m.Get("k")
	if got.Cost != 2.0 {
		t.Errorf("overwritten cost = %v, want 2.0", got.Cost)
	}
}

func TestMemo_EmptyLen(t *testing.T) {
	m := NewMemo()
	if m.Len() != 0 {
		t.Errorf("empty memo Len = %d, want 0", m.Len())
	}
}

func TestMemo_DistinctKeys(t *testing.T) {
	m := NewMemo()
	m.Put("a", Plan{Cost: 1.0, MemoKey: "a"})
	m.Put("b", Plan{Cost: 2.0, MemoKey: "b"})
	m.Put("c", Plan{Cost: 3.0, MemoKey: "c"})
	if m.Len() != 3 {
		t.Errorf("Len = %d, want 3", m.Len())
	}
	if _, ok := m.Get("a"); !ok {
		t.Error("expected key 'a'")
	}
	if _, ok := m.Get("missing"); ok {
		t.Error("missing key should not be found")
	}
}

// Serialization coverage for all AST node types

func TestSerializeKey_AllExprNodes(t *testing.T) {
	stmts := []struct {
		name string
		stmt PS.Stmt
	}{
		{"select", &PS.Select{From: "t"}},
		{"insert", &PS.Insert{Table: "t"}},
		{"update", &PS.Update{Table: "t"}},
		{"delete", &PS.Delete{Table: "t"}},
		{"create", &PS.CreateTable{Name: "t"}},
		{"drop", &PS.DropTable{Name: "t"}},
	}
	for _, tt := range stmts {
		t.Run(tt.name, func(t *testing.T) {
			k1 := SerializeKey(tt.stmt)
			k2 := SerializeKey(tt.stmt)
			if k1 != k2 {
				t.Errorf("non-deterministic key for %s", tt.name)
			}
			if len(k1) == 0 {
				t.Errorf("empty key for %s", tt.name)
			}
		})
	}
}

func TestSerializeKey_AllExprTypes(t *testing.T) {
	// Exercise every expression encoding branch.
	exprs := []PS.Expr{
		&PS.NumberLiteral{Val: 42},
		&PS.FloatLiteral{Val: 3.14},
		&PS.StringLiteral{Val: "hello"},
		&PS.BoolLiteral{Val: true},
		&PS.NullLiteral{},
		&PS.Ident{Name: "x"},
		&PS.QualifiedName{Table: "t", Name: "c"},
		&PS.AliasedExpr{Expr: &PS.Ident{Name: "x"}, Alias: "y"},
		&PS.Param{Index: 0},
		&PS.StarExpr{},
		&PS.UnaryExpr{Op: LX.T_MINUS, Operand: &PS.NumberLiteral{Val: 1}},
		&PS.BinaryExpr{Left: &PS.NumberLiteral{Val: 1}, Op: LX.T_PLUS, Right: &PS.NumberLiteral{Val: 2}},
		&PS.FunctionCall{Name: "ABS", Args: []PS.Expr{&PS.NumberLiteral{Val: 1}}},
		&PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}},
		&PS.CastExpr{Expr: &PS.Ident{Name: "x"}, Type: &PS.TypeInfo{Type: LX.T_INT_KW}},
		&PS.ListExpr{Items: []PS.Expr{&PS.NumberLiteral{Val: 1}}},
		&PS.BetweenExpr{Expr: &PS.Ident{Name: "x"}, Low: &PS.NumberLiteral{Val: 1}, High: &PS.NumberLiteral{Val: 10}},
		&PS.CaseExpr{WhenList: []PS.WhenClause{{Cond: &PS.NumberLiteral{Val: 1}, Then: &PS.StringLiteral{Val: "a"}}}},
		&PS.InExpr{Expr: &PS.Ident{Name: "x"}, List: []PS.Expr{&PS.NumberLiteral{Val: 1}}},
		&PS.ExistsExpr{Subquery: &PS.Select{From: "t"}},
		&PS.SubqueryExpr{Subquery: &PS.Select{From: "t"}},
	}
	for i, e := range exprs {
		t.Run(strings.TrimPrefix(string(rune('A'+i)), ""), func(t *testing.T) {
			stmt := &PS.Select{Cols: []PS.Expr{e}, From: "t"}
			k := SerializeKey(stmt)
			if len(k) == 0 {
				t.Error("empty key")
			}
		})
	}
}

func TestSerializeKey_NilStmt(t *testing.T) {
	k := SerializeKey(nil)
	if len(k) == 0 {
		t.Error("expected non-empty key for nil stmt")
	}
}

func TestSerializeKey_AllSelectFields(t *testing.T) {
	stmt := &PS.Select{
		Distinct:  true,
		Cols:      []PS.Expr{&PS.Ident{Name: "x"}},
		From:      "t",
		FromAlias: "a",
		Where:     &PS.NumberLiteral{Val: 1},
		OrderBy:   []PS.OrderItem{{Expr: &PS.Ident{Name: "y"}, Desc: true}},
		Limit:     &PS.NumberLiteral{Val: 10},
		Offset:    &PS.NumberLiteral{Val: 5},
	}
	k := SerializeKey(stmt)
	if len(k) == 0 {
		t.Error("expected non-empty key for full Select")
	}
}

func TestSerializeKey_AllInsertFields(t *testing.T) {
	stmt := &PS.Insert{
		Table:  "t",
		Cols:   []string{"a", "b"},
		Values: [][]PS.Expr{{&PS.NumberLiteral{Val: 1}, &PS.NumberLiteral{Val: 2}}},
	}
	k := SerializeKey(stmt)
	if len(k) == 0 {
		t.Error("expected non-empty key for full Insert")
	}
}

func TestSerializeKey_AllUpdateFields(t *testing.T) {
	stmt := &PS.Update{
		Table: "t",
		Set:   []PS.Pair{{Col: "x", Val: &PS.NumberLiteral{Val: 1}}},
		Where: &PS.Ident{Name: "y"},
	}
	k := SerializeKey(stmt)
	if len(k) == 0 {
		t.Error("expected non-empty key for full Update")
	}
}

func TestSerializeKey_AllCreateTableFields(t *testing.T) {
	pk := "id"
	stmt := &PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: LX.T_INT_KW, Nullable: false, PK: true, Unique: true},
		},
		PK: &pk,
	}
	k := SerializeKey(stmt)
	if len(k) == 0 {
		t.Error("expected non-empty key for full CreateTable")
	}
}

func TestSerializeKey_ColDefWithDefault(t *testing.T) {
	stmt := &PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "x", Type: LX.T_INT_KW, Default: &PS.NumberLiteral{Val: 0}},
		},
	}
	k := SerializeKey(stmt)
	if len(k) == 0 {
		t.Error("expected non-empty key for ColDef with default")
	}
}

func TestSerializeKey_ExistsNoSubquery(t *testing.T) {
	stmt := &PS.Select{From: "t", Where: &PS.ExistsExpr{}}
	k := SerializeKey(stmt)
	if len(k) == 0 {
		t.Error("expected non-empty key for EXISTS without subquery")
	}
}

func TestSerializeKey_SubqueryNilSubquery(t *testing.T) {
	stmt := &PS.Select{From: "t", Where: &PS.SubqueryExpr{}}
	k := SerializeKey(stmt)
	if len(k) == 0 {
		t.Error("expected non-empty key for SubqueryExpr without subquery")
	}
}

func TestSerializeKey_InExprNoSubquery(t *testing.T) {
	stmt := &PS.Select{From: "t", Where: &PS.InExpr{Expr: &PS.Ident{Name: "x"}}}
	k := SerializeKey(stmt)
	if len(k) == 0 {
		t.Error("expected non-empty key for InExpr without subquery")
	}
}

// Planner lifecycle

func TestPlanner_NewPlanner_DefaultNoop(t *testing.T) {
	pl := NewPlanner()
	plan, err := pl.Plan(&PS.Select{From: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cost != 0 {
		t.Errorf("default BuildTree should return 0, got %v", plan.Cost)
	}
}

func TestPlanner_NewPlannerWith_NilBuildTreeDefaultsToNoop(t *testing.T) {
	pl := NewPlannerWith(PlanOptions{BuildTree: nil})
	_, err := pl.Plan(&PS.Select{From: "t"})
	if err != nil {
		t.Errorf("nil BuildTree should default to no-op, got %v", err)
	}
}

func TestPlanner_MemoReturnsUnderlying(t *testing.T) {
	pl := NewPlanner()
	if pl.Memo() == nil {
		t.Error("Memo() should return non-nil")
	}
	if pl.Memo() != pl.memo {
		t.Error("Memo() should return the underlying memo pointer")
	}
}

// Helper predicates simulating index/cost decisions

func isEqualityOnIndexedCol(e PS.Expr) bool {
	b, ok := e.(*PS.BinaryExpr)
	if !ok {
		return false
	}
	if b.Op != LX.T_EQ {
		return false
	}
	id, ok := b.Left.(*PS.Ident)
	if !ok {
		return false
	}
	return id.Name == "id"
}

func isRangeOnIndexedCol(e PS.Expr) bool {
	b, ok := e.(*PS.BinaryExpr)
	if !ok {
		return false
	}
	if b.Op != LX.T_GT && b.Op != LX.T_LT && b.Op != LX.T_GE && b.Op != LX.T_LE {
		return false
	}
	id, ok := b.Left.(*PS.Ident)
	if !ok {
		return false
	}
	return id.Name == "id"
}

func hasCompositeIndexMatch(e PS.Expr, cols []string) bool {
	b, ok := e.(*PS.BinaryExpr)
	if !ok {
		return false
	}
	if b.Op != LX.T_AND {
		return false
	}
	left, lok := b.Left.(*PS.BinaryExpr)
	right, rok := b.Right.(*PS.BinaryExpr)
	if !lok || !rok {
		return false
	}
	leftId, _ := left.Left.(*PS.Ident)
	rightId, _ := right.Left.(*PS.Ident)
	return leftId.Name == cols[0] && rightId.Name == cols[1]
}
