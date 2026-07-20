package PF

import (
	"testing"

	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// mockSubPlanner returns a fixed operator for every PlanSubquery call.
type mockSubPlanner struct {
	planned pl.Operator
	err     error
}

func (m *mockSubPlanner) PlanSubquery(stmt PS.Stmt, outerAliases []string) (pl.Operator, error) {
	return m.planned, m.err
}

func TestSubqueryDecorrelation_ExistsToSemiJoin(t *testing.T) {
	factory := &mockFactory{}
	scan := &noopOp{name: "seqscan:t1"}
	sub := &noopOp{name: "seqscan:t2"}
	subq := &PS.ExistsExpr{Subquery: &PS.Select{From: "t2"}}
	filter := &noopOp{name: "filter", child: scan, pred: subq, hasPred: true}
	ctx := &OC.Context{
		Factory:    factory,
		SubPlanner: &mockSubPlanner{planned: sub},
	}
	pass := &SubqueryDecorrelationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: filter}, ctx)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if got, want := plan.Root.(*noopOp).name, "hashjoin"; got != want {
		t.Fatalf("root.name = %q, want %q (Filter should be replaced by HashJoin)", got, want)
	}
}

func TestSubqueryDecorrelation_Correlated_LeftUntouched(t *testing.T) {
	factory := &mockFactory{}
	scan := &noopOp{name: "seqscan:t1"}
	// Subquery references t1.col from outer scope.
	subq := &PS.ExistsExpr{Subquery: &PS.Select{
		From:  "t2",
		Where: &PS.QualifiedName{Table: "t1", Name: "col"},
	}}
	filter := &noopOp{name: "filter", child: scan, pred: subq, hasPred: true}
	ctx := &OC.Context{
		Factory:    factory,
		SubPlanner: &mockSubPlanner{planned: &noopOp{name: "seqscan:t2"}},
	}
	pass := &SubqueryDecorrelationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: filter}, ctx)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if got, want := plan.Root.(*noopOp).name, "filter"; got != want {
		t.Fatalf("root.name = %q, want %q (correlated EXISTS should stay as Filter)", got, want)
	}
}

func TestSubqueryDecorrelation_NoSubPlanner_NoOp(t *testing.T) {
	factory := &mockFactory{}
	subq := &PS.ExistsExpr{Subquery: &PS.Select{From: "t2"}}
	filter := &noopOp{name: "filter", pred: subq, hasPred: true}
	ctx := &OC.Context{Factory: factory}
	pass := &SubqueryDecorrelationPass{}
	plan, _ := pass.Apply(&OC.Plan{Root: filter}, ctx)
	if got, want := plan.Root.(*noopOp).name, "filter"; got != want {
		t.Fatalf("root.name = %q, want %q (no SubPlanner → keep filter)", got, want)
	}
}

func TestSubqueryDecorrelation_NoExists_NoOp(t *testing.T) {
	scan := &noopOp{name: "seqscan:t1"}
	filter := &noopOp{name: "filter", child: scan, pred: &PS.BinaryExpr{Left: &PS.Ident{Name: "a"}, Op: LX.T_EQ, Right: &PS.NumberLiteral{Val: 1}}, hasPred: true}
	ctx := &OC.Context{Factory: &mockFactory{}}
	pass := &SubqueryDecorrelationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: filter}, ctx)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan.Root != filter {
		t.Fatalf("expected no change for non-EXISTS predicate")
	}
}

// TestSubqueryDecorrelation_JoinContext verifies that EXISTS → semi-join
// decorrelation works when the EXISTS is on a join predicate (not just a
// single-table Filter). REQ001643.
func TestSubqueryDecorrelation_JoinContext(t *testing.T) {
	factory := &mockFactory{}
	join := &noopOp{name: "hashjoin"}
	sub := &noopOp{name: "seqscan:t2"}
	subq := &PS.ExistsExpr{Subquery: &PS.Select{From: "t2"}}
	// Filter on top of a join: Filter(HashJoin(SeqScan:t1, SeqScan:t3))
	filter := &noopOp{name: "filter", child: join, pred: subq, hasPred: true}
	ctx := &OC.Context{
		Factory:    factory,
		SubPlanner: &mockSubPlanner{planned: sub},
	}
	pass := &SubqueryDecorrelationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: filter}, ctx)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	// The Filter(HashJoin(...), EXISTS) should become HashJoin(HashJoin(...), SeqScan:t2, semi-join)
	if got, want := plan.Root.(*noopOp).name, "hashjoin"; got != want {
		t.Fatalf("root.name = %q, want %q (Filter on join should be replaced by HashJoin)", got, want)
	}
	t.Logf("subquery decorrelation works on join context: Filter(HashJoin, EXISTS) → HashJoin(..., semi-join)")
}
