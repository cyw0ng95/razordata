package PF

import (
	"errors"

	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// OrToInExpansionPass converts OR chains of same-column equalities
// (e.g. `a = 1 OR a = 2 OR a = 3`) into IN expressions
// (`a IN (1, 2, 3)`).
type OrToInExpansionPass struct{}

func (p *OrToInExpansionPass) Name() string {
	return "or_to_in_expansion"
}

func (p *OrToInExpansionPass) Apply(plan *OC.Plan, ctx *OC.Context) (*OC.Plan, error) {
	if plan == nil || plan.Root == nil {
		return plan, nil
	}
	if ctx == nil || ctx.Factory == nil {
		return plan, errors.New("OrToInExpansionPass: ctx.Factory is nil")
	}

	plan.Root = WalkOp(plan.Root, func(op pl.Operator) pl.Operator {
		filter, ok := op.(pl.PredicateCarrier)
		if !ok || !filter.HasPredicate() {
			return op
		}
		pred := filter.Predicate()
		expr, ok := pred.(PS.Expr)
		if !ok {
			return op
		}

		colName, values, ok := collectOrEqualityConjuncts(expr)
		if !ok || len(values) < 2 {
			return op
		}

		literals := make([]PS.Expr, len(values))
		for i, lit := range values {
			literals[i] = lit
		}

		var colExpr PS.Expr
		if colName != "" {
			colExpr = &PS.Ident{Name: colName}
		}
		filter.SetPredicate(&PS.InExpr{
			Expr: colExpr,
			List: literals,
		})
		return op
	})

	return plan, nil
}

// collectOrEqualityConjuncts checks whether expr is an OR chain of
// `col = literal` disjuncts all referencing the same column.
// Returns (colName, literalValues, true) when successful, or
// ("", nil, false) when the pattern doesn't match.
//
// At least 2 disjuncts are required — a single equality is left as-is.
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

// isLiteral reports whether e is a supported literal type.
func isLiteral(e PS.Expr) bool {
	switch e.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral, *PS.BoolLiteral, *PS.NullLiteral:
		return true
	}
	return false
}
