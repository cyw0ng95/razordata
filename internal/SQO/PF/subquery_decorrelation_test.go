package PF

import (
	"testing"

	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
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
	factory := &mockFactory{}
	filter := &noopOp{name: "filter", pred: &PS.Ident{Name: "x"}, hasPred: true}
	ctx := &OC.Context{
		Factory:    factory,
		SubPlanner: &mockSubPlanner{planned: &noopOp{}},
	}
	pass := &SubqueryDecorrelationPass{}
	plan, _ := pass.Apply(&OC.Plan{Root: filter}, ctx)
	if got, want := plan.Root.(*noopOp).name, "filter"; got != want {
		t.Fatalf("root.name = %q, want %q", got, want)
	}
}
