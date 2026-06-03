package EX

import "context"

type outerInjector struct {
	child Operator
	outer *Row
}

func newOuterInjector(child Operator, outer *Row) Operator {
	if outer == nil {
		return child
	}
	return &outerInjector{child: child, outer: outer}
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
// so that rows produced inside the subquery have Outer set
// before any Filter/Project sees them. Returns the (possibly
// new) root.
func injectOuter(op Operator, outer *Row) Operator {
	if outer == nil {
		return op
	}
	switch v := op.(type) {
	case *SeqScan:
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
	}
	return op
}

func runSubqueryPlan(pl *plan, outer *Row, params []interface{}) ([]Row, error) {
	if pl == nil || pl.root == nil {
		return nil, ErrSubquery
	}
	if outer != nil {
		pl.root = injectOuter(pl.root, outer)
	}
	defer pl.root.Close()
	var out []Row
	for {
		row, err := pl.root.Next(context.Background())
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
