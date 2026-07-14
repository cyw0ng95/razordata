package OC

import (
	"context"
	"errors"
	"testing"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// noopOperator is the smallest possible pl.Operator implementation
// for tests. It always returns ErrNoRows on the first Next call.
type noopOperator struct{}

func (noopOperator) Next(_ context.Context) (pl.Row, error) {
	return pl.Row{}, pl.ErrNoRows
}
func (noopOperator) Close() error { return nil }

func TestNew_EmptyOptimizer(t *testing.T) {
	opt := New()
	if opt == nil {
		t.Fatal("New() returned nil")
	}
	if got := opt.PassCount(); got != 0 {
		t.Errorf("PassCount = %d, want 0", got)
	}
}

func TestOptimizer_Optimize_Empty_PassesThrough(t *testing.T) {
	opt := New()
	root := noopOperator{}
	plan := &Plan{Root: root}
	ctx := &Context{}

	out, err := opt.Optimize(plan, ctx)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if out == nil {
		t.Fatal("Optimize returned nil plan")
	}
	if out.Root != pl.Operator(root) {
		t.Errorf("Root changed: got %T, want %T", out.Root, root)
	}
}

func TestOptimizer_AddPass_ChainsPasses(t *testing.T) {
	opt := New()
	order := []string{}
	opt.AddPass(testPass{name: "a", order: &order})
	opt.AddPass(testPass{name: "b", order: &order})
	opt.AddPass(testPass{name: "c", order: &order})

	if got := opt.PassCount(); got != 3 {
		t.Errorf("PassCount = %d, want 3", got)
	}

	_, err := opt.Optimize(&Plan{Root: noopOperator{}}, &Context{})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	want := "a,b,c,"
	if got := joinOrder(order); got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

func TestOptimizer_Passes_ReturnsCopy(t *testing.T) {
	opt := New()
	opt.AddPass(testPass{name: "a"})

	got := opt.Passes()
	if len(got) != 1 {
		t.Fatalf("Passes len = %d, want 1", len(got))
	}
	got[0] = testPass{name: "mutated"}
	again := opt.Passes()
	if again[0].Name() != "a" {
		t.Errorf("internal slice was mutated: got %q", again[0].Name())
	}
}

func TestOptimizer_Optimize_PropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	opt := New()
	opt.AddPass(errorPass{err: wantErr})
	_, err := opt.Optimize(&Plan{Root: noopOperator{}}, &Context{})
	if err == nil || err.Error() != wantErr.Error() {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestOptimizer_StopsOnError(t *testing.T) {
	calls := []string{}
	opt := New()
	opt.AddPass(testPass{name: "first", order: &calls})
	opt.AddPass(errorPass{err: errors.New("stop")})
	opt.AddPass(testPass{name: "third", order: &calls})
	_, _ = opt.Optimize(&Plan{Root: noopOperator{}}, &Context{})
	if got := joinOrder(calls); got != "first," {
		t.Errorf("order = %q, want only first to run", got)
	}
}

func TestContext_FieldsAccessible(t *testing.T) {
	ctx := &Context{
		Catalog: nil,
		Stats:   nil,
		Tables: map[string]TableSchema{
			"t1": {Columns: []string{"a", "b"}, PK: "a"},
		},
	}
	if got := ctx.Tables["t1"].PK; got != "a" {
		t.Errorf("PK = %q, want a", got)
	}
}

func TestPlan_StmtAssignable(t *testing.T) {
	// Use a typed-nil interface assignment to verify the field
	// holds a PS.Stmt without depending on a concrete subtype.
	var stmt PS.Stmt
	plan := &Plan{Root: noopOperator{}, Stmt: stmt}
	_ = plan.Stmt
}

// testPass is a Pass that records its name into a shared slice
// and returns the plan unchanged.
type testPass struct {
	name  string
	order *[]string
}

func (p testPass) Name() string { return p.name }
func (p testPass) Apply(plan *Plan, _ *Context) (*Plan, error) {
	if p.order != nil {
		*p.order = append(*p.order, p.name)
	}
	return plan, nil
}

// errorPass always returns a fixed error.
type errorPass struct{ err error }

func (p errorPass) Name() string                       { return "error" }
func (p errorPass) Apply(_ *Plan, _ *Context) (*Plan, error) { return nil, p.err }

func joinOrder(s []string) string {
	out := ""
	for _, v := range s {
		out += v + ","
	}
	return out
}
