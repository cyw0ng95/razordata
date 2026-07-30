// REQ001435: AG-side adapters that expose the pl.* capability
// interfaces on Aggregate and HashAggregate.
//
// GroupCols() already exists on both types returning []PS.Expr,
// which matches pl.AggregateInfo.GroupCols. The adapters add
// SetChild and Aggregates (pl.AggregateSpec form).
package AG

import (
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// --- Aggregate adapters -----------------------------------------------

// SetChild sets the input. Aggregate has Child() but no SetChild.
func (a *Aggregate) SetChild(op pl.Operator) {
	if op == nil {
		a.child = nil
		return
	}
	if o, ok := op.(Operator); ok {
		a.child = o
	}
}

// Aggregates returns aggregate specs projected from the
// []PS.Expr Aggregate stores. pl.AggregateInfo.Aggregates is
// []pl.AggregateSpec.
func (a *Aggregate) Aggregates() []pl.AggregateSpec {
	if len(a.aggs) == 0 {
		return nil
	}
	out := make([]pl.AggregateSpec, 0, len(a.aggs))
	for _, e := range a.aggs {
		out = append(out, exprToSpec(e))
	}
	return out
}

// REQ002173: HashAggregate adapters removed — use Aggregate.

// exprToSpec converts a single aggregate PS.Expr into an
// AggregateSpec. The function-call form `count(x)` becomes
// {FuncName: "count", Arg: name(x)}; other forms become
// {FuncName: name(e)}.
func exprToSpec(e PS.Expr) pl.AggregateSpec {
	if fc, ok := e.(*PS.FunctionCall); ok {
		arg := ""
		if len(fc.Args) > 0 {
			arg = exprName(fc.Args[0])
		}
		return pl.AggregateSpec{FuncName: fc.Name, Arg: arg}
	}
	return pl.AggregateSpec{FuncName: exprName(e)}
}

// exprName extracts a column name from a simple PS.Expr.
// Returns "" for non-trivial expressions.
func exprName(e PS.Expr) string {
	switch n := e.(type) {
	case *PS.QualifiedName:
		return n.Name
	case *PS.Ident:
		return n.Name
	}
	return ""
}
