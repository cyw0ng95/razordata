package CO

import (
	"reflect"

	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// SplitAnd splits a conjunction (AND tree) into its leaf conjuncts,
// using cache to memoize by expression pointer.
// Mirrors SQB/EX splitAnd with explicit cache injection.
// REQ001248, REQ001439.
func SplitAnd(expr PS.Expr, cache map[uintptr][]PS.Expr) []PS.Expr {
	if expr == nil {
		return nil
	}
	if b, ok := expr.(*PS.BinaryExpr); !ok || b.Op != LX.T_AND {
		return []PS.Expr{expr}
	}
	key := reflect.ValueOf(expr).Pointer()
	if v, ok := cache[key]; ok {
		return v
	}
	conjuncts := RE.SplitAnd(expr)
	cache[key] = conjuncts
	return conjuncts
}
