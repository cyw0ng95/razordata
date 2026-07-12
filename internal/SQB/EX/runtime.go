package EX

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// propagatePlanner walks the operator tree rooted at root and
// calls WithPlanner(p) on every node that supports it. See
// REQ000366.
func propagatePlanner(root DT.Operator, p *Planner) {
	if root == nil {
		return
	}
	if w, ok := root.(interface {
		WithPlanner(pl.QueryPlanner) pl.Operator
	}); ok {
		w.WithPlanner(p)
	}
	type childer interface {
		Child() DT.Operator
	}
	if c, ok := root.(childer); ok {
		propagatePlanner(c.Child(), p)
	}
	type leftRighter interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}
	if lr, ok := root.(leftRighter); ok {
		propagatePlanner(lr.LeftChild(), p)
		propagatePlanner(lr.RightChild(), p)
	}
}

// propagateExecContext walks the operator tree and sets execCtx
// on operators that evaluate expressions (Filter, Project, etc.)
// so that subquery eval can find the planner via DT.ExecContextFromRow.
// Also propagates execCtx to Insert/Update/Delete for change
// tracking (REQ000812).
func propagateExecContext(root DT.Operator, ec *DT.ExecContext) {
	if root == nil || ec == nil {
		return
	}
	// REQ001233: create RowArena once per query, shared across all
	// operators in the tree. REQ001260: Init is deferred to first
	// AllocRow call where nCols is known.
	if ec.RowArena == nil {
		ec.RowArena = &DT.RowArena{}
	}
	// REQ001527: use generic interface check so any operator with
	// SetExecCtx gets the exec context. Replaces per-type assertions.
	if setter, ok := root.(interface{ SetExecCtx(*DT.ExecContext) }); ok {
		setter.SetExecCtx(ec)
	}
	type childer interface {
		Child() DT.Operator
	}
	if c, ok := root.(childer); ok {
		propagateExecContext(c.Child(), ec)
	}
	type leftRighter interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}
	if lr, ok := root.(leftRighter); ok {
		propagateExecContext(lr.LeftChild(), ec)
		propagateExecContext(lr.RightChild(), ec)
	}
}

// resetRowArena resets the RowArena in the ExecContext. Called
// via defer at the end of each query execution. REQ001233.
func resetRowArena(ec *DT.ExecContext) {
	if ec == nil {
		return
	}
	if arena, ok := ec.RowArena.(*DT.RowArena); ok {
		arena.Reset()
	}
}

// propagateParams walks the operator tree rooted at root and
// calls WithParams(args) on every node that supports it
// (R16-1..2). The walk is depth-first, children-first so the
// args reach every leaf operator. Operators without a
// WithParams method are skipped silently.
func propagateParams(root DT.Operator, args []any) {
	if root == nil {
		return
	}
	if args == nil {
		return
	}
	p := asAnySlice(args)
	if w, ok := root.(interface{ WithParams([]any) DT.Operator }); ok {
		w.WithParams(p)
	}
	// Walk children via the Child() convention used elsewhere
	// in this package (explain.go).
	type childer interface {
		Child() DT.Operator
	}
	if c, ok := root.(childer); ok {
		propagateParams(c.Child(), args)
	}
	// Some operators expose children via a `child` field; we
	// rely on the explain.go walk for those via Child(). Operators
	// with multiple children (HashAggregate, Join) define
	// their own WithParams and walk internally.
}

// asAnySlice converts []any to []any for type-stability
// across the WithParams interface boundary. Avoids an allocation
// when the slice is already nil.
func asAnySlice(args []any) []any {
	if args == nil {
		return nil
	}
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a
	}
	return out
}