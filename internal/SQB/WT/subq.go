// Package EX's subq.go hosts the subquery planning and execution
// helpers (IN-subquery, EXISTS, scalar subquery). Subqueries are not
// part of the v1 MVP scope per docs/design/ARCH.md; the helpers are
// retained in v1.1 for upcoming releases.
package WT

import (
	"context"
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"

	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

type outerInjector struct {
	child DT.Operator
	outer *DT.Row
}

func (o *outerInjector) Next(ctx context.Context) (DT.Row, error) {
	row, err := o.child.Next(ctx)
	if err != nil {
		return DT.Row{}, err
	}
	row.Outer = o.outer
	return row, nil
}

func (o *outerInjector) Close() error {
	return o.child.Close()
}

// injectOuter walks the operator tree and wraps every OP.SeqScan
// and OP.IndexScan so that rows produced inside the subquery have
// Outer set before any OP.Filter/OP.Project sees them. Returns the
// (possibly new) root.
func injectOuter(op DT.Operator, outer *DT.Row) DT.Operator {
	if outer == nil {
		return op
	}
	// REQ000366: the planner memoizes subquery plans. The same
	// plan tree can be re-walked by injectOuter on each
	// subquery eval call (one per outer row). If we left a
	// previous outerInjector in place, the second call would
	// see the stale outer and the correlated WHERE would
	// evaluate against the first outer row.
	// Update an existing outerInjector's outer field in place
	// so the memoized plan is reused correctly.
	if inj, ok := op.(*outerInjector); ok {
		inj.outer = outer
		return inj
	}
	switch v := op.(type) {
	case *OP.SeqScan:
		return &outerInjector{child: v, outer: outer}
	case *OP.IndexScan:
		return &outerInjector{child: v, outer: outer}
	case *OP.Filter:
		v.SetChild(injectOuter(v.Child(), outer))
		return v
	case *OP.Project:
		v.SetChild(injectOuter(v.Child(), outer))
		return v
	case *OP.Sort:
		v.SetChild(injectOuter(v.Child(), outer))
		return v
	case *OP.Limit:
		v.SetChild(injectOuter(v.Child(), outer))
		return v
	case *OP.Distinct:
		injectOuter(v.Child(), outer)
		return v
	case *AG.Aggregate:
		injectOuter(v.Child(), outer)
		return v
	case *OP.NestedLoopJoin:
		v.SetLeft(injectOuter(v.LeftChild(), outer))
		v.SetRight(injectOuter(v.RightChild(), outer))
		return v
	case *OP.HashJoin:
		// HashJoin children are read-only via accessors. Return as-is;
		// correlated subqueries over HashJoin fall back to literal.
		return op
	default:
		if c, ok := op.(interface {
			Child() DT.Operator
			SetChild(DT.Operator)
		}); ok {
			c.SetChild(injectOuter(c.Child(), outer))
			return op
		}
		return op
	}
	return op
}

func RunSubqueryPlan(ctx context.Context, pl *pl.PlanResult, outer *DT.Row, params []any) ([]DT.Row, error) {
	if pl == nil || pl.Root == nil {
		return nil, EV.ErrSubquery
	}
	if outer != nil {
		pl.Root = injectOuter(pl.Root, outer)
	}
	defer pl.Root.Close()
	var out []DT.Row
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := pl.Root.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// RunSubqueryFirstMatch is the REQ001073 short-circuit variant of
// RunSubqueryPlan. Instead of materializing all rows, it stops after
// the first row and returns (true, nil). If the subquery produces
// zero rows it returns (false, nil). This is the "semi-join stops
// scanning after the first match" optimization applied to the
// existential subquery path.
//
// Callers MUST close pl.Root independently — this function does not
// take ownership of pl.
func RunSubqueryFirstMatch(ctx context.Context, pl *pl.PlanResult, outer *DT.Row, params []any) (bool, error) {
	if pl == nil || pl.Root == nil {
		return false, EV.ErrSubquery
	}
	if outer != nil {
		pl.Root = injectOuter(pl.Root, outer)
	}
	// Drain until the first row — that's all we need for an
	// existential check. Returns immediately on the first hit.
	for {
		if err := ctx.Err(); err != nil {
			// Close on context error so the caller doesn't leak.
			pl.Root.Close()
			return false, err
		}
		_, err := pl.Root.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				return false, nil
			}
			return false, err
		}
		// Found at least one matching row — short-circuit.
		return true, nil
	}
}
