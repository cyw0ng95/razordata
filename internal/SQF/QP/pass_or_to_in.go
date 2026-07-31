package QP

import (
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// OrToInExpansionPass converts OR chains of same-column equalities
// (e.g. `a = 1 OR a = 2 OR a = 3`) into IN expressions. REQ002263.
type OrToInExpansionPass struct{}

func (p *OrToInExpansionPass) Name() string { return "or_to_in_expansion" }

func (p *OrToInExpansionPass) Apply(qp *QueryPlan) error {
	if qp == nil || qp.RootIdx < 0 {
		return nil
	}
	qp.walkBottomUp(func(idx int, node *PlanNode) bool {
		if len(node.Exprs) != 1 {
			return true
		}
		expr, ok := node.Exprs[0].(PS.Expr)
		if !ok || expr == nil {
			return true
		}
		colName, values, ok := collectOrEqualityConjuncts(expr)
		if !ok || len(values) < 2 {
			return true
		}
		node.Exprs[0] = &PS.InExpr{
			Expr: &PS.Ident{Name: colName},
			List: values,
		}
		return true
	})
	return nil
}

func collectOrEqualityConjuncts(expr PS.Expr) (colName string, values []PS.Expr, ok bool) {
	disjuncts := RE.SplitOr(expr)
	if len(disjuncts) < 2 {
		return "", nil, false
	}
	var firstCol string
	vals := make([]PS.Expr, 0, len(disjuncts))
	for _, d := range disjuncts {
		bin, ok := d.(*PS.BinaryExpr)
		if !ok || bin.Op != LX.T_EQ {
			return "", nil, false
		}
		var col *PS.Ident
		var lit PS.Expr
		if c, ok := bin.Left.(*PS.Ident); ok && isLiteral(bin.Right) {
			col = c
			lit = bin.Right
		} else if c, ok := bin.Right.(*PS.Ident); ok && isLiteral(bin.Left) {
			col = c
			lit = bin.Left
		} else {
			return "", nil, false
		}
		if firstCol == "" {
			firstCol = col.Name
		} else if firstCol != col.Name {
			return "", nil, false
		}
		vals = append(vals, lit)
	}
	return firstCol, vals, true
}

func isLiteral(e PS.Expr) bool {
	switch e.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral, *PS.BoolLiteral, *PS.NullLiteral:
		return true
	}
	return false
}
