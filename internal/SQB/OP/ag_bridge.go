// Package OP exposes AG's aggregate constructors as wrapped
// DT.Operator-returning helpers. The factory in factory.go
// delegates to these so SQB/OP/AG does not need to import the
// full AG package (and AG does not import SQB/OP/AG).
package OP

import (
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"

	"github.com/cyw0ng95/razordata/internal/SQB/AG"
)

// AGNewAggregate wraps AG.NewAggregate. The returned operator
// implements the AG-side aggregate semantics; the executor wires
// input on Open. groupCols is supplied as []string (the public
// DT.OperatorFactory shape) and converted to []PS.Expr here.
func AGNewAggregate(child DT.Operator, groupCols []string, aggs []PS.Expr) DT.Operator {
	gc := make([]PS.Expr, 0, len(groupCols))
	for _, c := range groupCols {
		gc = append(gc, &PS.QualifiedName{Name: c})
	}
	return AG.NewAggregate(child, gc, aggs)
}


