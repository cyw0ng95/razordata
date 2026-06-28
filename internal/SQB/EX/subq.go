// Package EX's subq.go hosts the subquery planning and execution
// helpers (IN-subquery, EXISTS, scalar subquery). Subqueries are not
// part of the v1 MVP scope per docs/design/ARCH.md; the helpers are
// retained in v1.1 for upcoming releases.
package EX

import (
	"context"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

type outerInjector struct {
	child Operator
	outer *Row
}

func (o *outerInjector) Next(ctx context.Context) (Row, error) {
	row, err := o.child.Next(ctx)
	if err != nil {
		return Row{}, err
	}
	row.Outer = o.outer
	return row, nil
}

func (o *outerInjector) Close() error {
	return o.child.Close()
}

// injectOuter walks the operator tree and wraps every SeqScan
// and IndexScan so that rows produced inside the subquery have
// Outer set before any Filter/Project sees them. Returns the
// (possibly new) root.
func injectOuter(op Operator, outer *Row) Operator {
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
	case *AdaptiveOp:
		v.inner = injectOuter(v.inner, outer)
		return v
	case *SeqScan:
		return &outerInjector{child: v, outer: outer}
	case *IndexScan:
		return &outerInjector{child: v, outer: outer}
	case *Filter:
		v.child = injectOuter(v.child, outer)
		return v
	case *Project:
		v.child = injectOuter(v.child, outer)
		return v
	case *Sort:
		v.child = injectOuter(v.child, outer)
		return v
	case *Limit:
		v.child = injectOuter(v.child, outer)
		return v
	case *Distinct:
		v.child = injectOuter(v.child, outer)
		return v
	case *Aggregate:
		v.child = injectOuter(v.child, outer)
		return v
	case *NestedLoopJoin:
		v.left = injectOuter(v.left, outer)
		v.right = injectOuter(v.right, outer)
		return v
	case *HashJoin:
		v.left = injectOuter(v.left, outer)
		v.right = injectOuter(v.right, outer)
		return v
	}
	return op
}

func runSubqueryPlan(ctx context.Context, pl *pl.PlanResult, outer *Row, params []any) ([]Row, error) {
	if pl == nil || pl.Root == nil {
		return nil, ErrSubquery
	}
	if outer != nil {
		pl.Root = injectOuter(pl.Root, outer)
	}
	defer pl.Root.Close()
	var out []Row
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := pl.Root.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}
