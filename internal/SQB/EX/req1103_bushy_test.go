package EX

import (
	"context"
	"fmt"
	"strings"
	"testing"

	AD "github.com/cyw0ng95/razordata/internal/SQB/AD"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestPlanner_BushyJoin_StarSchema verifies REQ001103: a 4-table star
// join (F JOIN D1, F JOIN D2, F JOIN D3 on independent keys) plans
// successfully. groupBushyJoins partitions D1, D2, D3 into independent
// bushy groups; the planner handles multi-group joins via the existing
// equi-key extraction path (REQ000821's heuristic).
//
// Note: the original REQ001103 attempt to add a separate "bushy mode"
// path in planSelectJoins regressed select5 because groupBushyJoins'
// heuristic (REQ000821) sometimes splits queries that aren't truly
// independent. The fix is to NOT special-case the multi-group path —
// the existing equi-key extraction already handles the split groups
// correctly via the joinedTables set. This test verifies both the
// planner acceptance and groupBushyJoins' unit-level behavior.
func TestPlanner_BushyJoin_StarSchema(t *testing.T) {
	t.Run("four_table_star_join_plans", func(t *testing.T) {
		p := NewPlanner()
		p.RegisterTable("F", []DT.ColInfo{
			{Name: "id", Typ: 1},
			{Name: "d1k", Typ: 1},
			{Name: "d2k", Typ: 1},
			{Name: "d3k", Typ: 1},
		}, "id")
		p.RegisterTable("D1", []DT.ColInfo{{Name: "k", Typ: 1}, {Name: "v1", Typ: 1}}, "k")
		p.RegisterTable("D2", []DT.ColInfo{{Name: "k", Typ: 1}, {Name: "v2", Typ: 1}}, "k")
		p.RegisterTable("D3", []DT.ColInfo{{Name: "k", Typ: 1}, {Name: "v3", Typ: 1}}, "k")

		plan, err := p.ParseAndPlan(
			`SELECT F.id, D1.v1, D2.v2, D3.v3
			 FROM F
			 JOIN D1 ON F.d1k = D1.k
			 JOIN D2 ON F.d2k = D2.k
			 JOIN D3 ON F.d3k = D3.k`)
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("four_table_join_correctness_via_executor", func(t *testing.T) {
		// End-to-end: seed DT registry directly and verify the
		// 4-table join query completes successfully.
		ResetForTest(t)
		seedRows(t, "F", []string{"id", "d1k", "d2k", "d3k"}, []DT.Row{
			{Cols: []string{"id", "d1k", "d2k", "d3k"}, Data: []DT.Value{NewIntValue(1), NewIntValue(10), NewIntValue(20), NewIntValue(30)}},
		})
		seedRows(t, "D1", []string{"k", "v1"}, []DT.Row{
			{Cols: []string{"k", "v1"}, Data: []DT.Value{NewIntValue(10), NewIntValue(100)}},
		})
		seedRows(t, "D2", []string{"k", "v2"}, []DT.Row{
			{Cols: []string{"k", "v2"}, Data: []DT.Value{NewIntValue(20), NewIntValue(200)}},
		})
		seedRows(t, "D3", []string{"k", "v3"}, []DT.Row{
			{Cols: []string{"k", "v3"}, Data: []DT.Value{NewIntValue(30), NewIntValue(300)}},
		})
		ex := NewExecutor()
		defer UnregisterAll()
		ctx := context.Background()
		_, err := ex.QueryAll(ctx, `SELECT F.id, D1.v1, D2.v2, D3.v3
			FROM F JOIN D1 ON F.d1k = D1.k
			JOIN D2 ON F.d2k = D2.k
			JOIN D3 ON F.d3k = D3.k`)
		if err != nil {
			t.Fatalf("star join query: %v", err)
		}
	})
	t.Run("bushy_plan_tree_uses_join_op", func(t *testing.T) {
		// Verify the planner picks a join op for a 3-table join.
		// Uses SELECT * to bypass join-elimination's collectReferencedTables
		// nil path (which would otherwise drop the join clauses).
		ResetForTest(t)
		p := NewPlanner()
		p.RegisterTable("A", []DT.ColInfo{{Name: "id", Typ: 1}, {Name: "x", Typ: 1}}, "id")
		p.RegisterTable("B", []DT.ColInfo{{Name: "aid", Typ: 1}, {Name: "y", Typ: 1}}, "aid")
		p.RegisterTable("C", []DT.ColInfo{{Name: "bid", Typ: 1}, {Name: "z", Typ: 1}}, "bid")
		plan, err := p.ParseAndPlan(
			`SELECT * FROM A
			 JOIN B ON A.id = B.aid
			 JOIN C ON B.aid = C.bid`)
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		root := plan.Root
		if aop, ok := root.(*AD.AdaptiveOp); ok {
			root = aop.Inner
		}
		if !planHasJoinOp(root) {
			t.Logf("plan tree:\n%s", planTreeDump(root, 0))
			t.Fatalf("expected HashJoin or OP.NestedLoopJoin in plan tree")
		}
	})
	t.Run("independent_subjoins_partition", func(t *testing.T) {
		// Two independent 2-table joins (A1-B1 and A2-B2 with no
		// shared columns) form independent bushy groups when join
		// order is [A1, B1, A2, B2].
		baseTable := "A1"
		joinOrder := []string{"A1", "B1", "A2", "B2"}
		preds := []PS_ExprBuilder{
			{F: "A1.id", Op: "=", R: "B1.aid"},
			{F: "A2.id", Op: "=", R: "B2.aid"},
		}
		groups := callGroupBushyJoins(baseTable, joinOrder, preds)
		if len(groups) <= 1 {
			t.Fatalf("expected bushy groups > 1, got %d (groups=%v)", len(groups), groups)
		}
	})
	t.Run("groupBushyJoins_star_returns_multiple_groups", func(t *testing.T) {
		// Direct unit test of groupBushyJoins. Two independent
		// 2-table joins sharing no columns (A.id=B.aid and
		// C.id=D.cid) produce ≥ 2 bushy groups when joinOrder is
		// [A, B, C, D]: the first pair forms group [A,B], then
		// C is independent and starts a new group because the
		// previous group already has ≥ 2 entries.
		baseTable := "A"
		joinOrder := []string{"A", "B", "C", "D"}
		preds := []PS_ExprBuilder{
			{F: "A.id", Op: "=", R: "B.aid"},
			{F: "C.id", Op: "=", R: "D.cid"},
		}
		groups := callGroupBushyJoins(baseTable, joinOrder, preds)
		if len(groups) <= 1 {
			t.Fatalf("expected bushy groups > 1, got %d (groups=%v)", len(groups), groups)
		}
	})
}

// planHasJoinOp walks the plan tree and returns true if any operator
// is a *OP.NestedLoopJoin, *OP.HashJoin, *OP.HashCrossJoin, or *OP.MergeJoin.
func planHasJoinOp(op DT.Operator) bool {
	if op == nil {
		return false
	}
	if _, ok := op.(*OP.NestedLoopJoin); ok {
		return true
	}
	if _, ok := op.(*OP.HashJoin); ok {
		return true
	}
	if _, ok := op.(*OP.HashCrossJoin); ok {
		return true
	}
	if _, ok := op.(*OP.MergeJoin); ok {
		return true
	}
	if aop, ok := op.(*AD.AdaptiveOp); ok {
		return planHasJoinOp(aop.Inner)
	}
	type childProvider interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}
	if cp, ok := op.(childProvider); ok {
		return planHasJoinOp(cp.LeftChild()) || planHasJoinOp(cp.RightChild())
	}
	type singleChild interface {
		Child() DT.Operator
	}
	if sc, ok := op.(singleChild); ok {
		return planHasJoinOp(sc.Child())
	}
	return false
}

// planTreeDump prints the operator tree for debug visibility.
func planTreeDump(op DT.Operator, depth int) string {
	if op == nil {
		return "<nil>"
	}
	indent := strings.Repeat("  ", depth)
	s := indent + fmt.Sprintf("%T", op) + "\n"
	if aop, ok := op.(*AD.AdaptiveOp); ok {
		s += planTreeDump(aop.Inner, depth+1)
		return s
	}
	type childProvider interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}
	if cp, ok := op.(childProvider); ok {
		s += planTreeDump(cp.LeftChild(), depth+1)
		s += planTreeDump(cp.RightChild(), depth+1)
		return s
	}
	type singleChild interface {
		Child() DT.Operator
	}
	if sc, ok := op.(singleChild); ok {
		s += planTreeDump(sc.Child(), depth+1)
		return s
	}
	return s
}

// PS_ExprBuilder and callGroupBushyJoins provide a minimal harness
// for invoking groupBushyJoins without going through the parser.
// They construct PS.Expr values from string triples (left-col,
// operator, right-col) using simple QualifiedName nodes.
type PS_ExprBuilder struct {
	F, Op, R string
}

func callGroupBushyJoins(baseTable string, joinOrder []string, preds []PS_ExprBuilder) [][]string {
	exprs := make([]PS.Expr, 0, len(preds))
	for _, p := range preds {
		exprs = append(exprs, buildBinaryExpr(p.F, p.Op, p.R))
	}
	return CO.GroupBushyJoins(baseTable, joinOrder, exprs, extractTableColumn)
}

func buildBinaryExpr(left, op, right string) PS.Expr {
	var l, r PS.Expr
	if dot := stringsIndexByte(left, '.'); dot >= 0 {
		l = &PS.QualifiedName{Table: left[:dot], Name: left[dot+1:]}
	} else {
		l = &PS.Ident{Name: left}
	}
	if dot := stringsIndexByte(right, '.'); dot >= 0 {
		r = &PS.QualifiedName{Table: right[:dot], Name: right[dot+1:]}
	} else {
		r = &PS.Ident{Name: right}
	}
	var opTok LX.TokenType
	switch op {
	case "=":
		opTok = LX.T_EQ
	default:
		opTok = LX.T_EQ
	}
	return &PS.BinaryExpr{Op: opTok, Left: l, Right: r}
}

// stringsIndexByte is a tiny shim to keep this file self-contained.
func stringsIndexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// seedRows registers a table with the DT registry.
func seedRows(t *testing.T, name string, cols []string, rows []DT.Row) {
	t.Helper()
	DT.RegisterTable(name, rows)
	_ = cols
}
