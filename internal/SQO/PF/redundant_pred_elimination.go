package PF

import (
	"errors"
	"strings"

	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// RedundantPredicateEliminationPass removes conjuncts from an AND
// predicate that are logically implied by another conjunct in the
// same predicate.
//
// Example: WHERE a > 10 AND a > 5  ->  WHERE a > 10
//
// REQ001490.
type RedundantPredicateEliminationPass struct{}

func (p *RedundantPredicateEliminationPass) Name() string {
	return "redundant_predicate_elimination"
}

func (p *RedundantPredicateEliminationPass) Apply(plan *OC.Plan, ctx *OC.Context) (*OC.Plan, error) {
	if plan == nil || plan.Root == nil {
		return plan, nil
	}
	if ctx == nil || ctx.Factory == nil {
		return plan, errors.New("RedundantPredicateEliminationPass: ctx.Factory is nil")
	}

	plan.Root = WalkOp(plan.Root, func(op pl.Operator) pl.Operator {
		pc, ok := op.(pl.PredicateCarrier)
		if !ok || !pc.HasPredicate() {
			return op
		}
		pred, ok := pc.Predicate().(PS.Expr)
		if !ok || pred == nil {
			return op
	 }
		reduced := eliminateRedundant(pred)
		if reduced != pred {
			pc.SetPredicate(reduced)
		}
		return op
	})

	return plan, nil
}

// eliminateRedundant splits the predicate into conjuncts, removes
// those implied by another conjunct, and returns the simplified
// expression. Returns the original expression when nothing changed.
func eliminateRedundant(pred PS.Expr) PS.Expr {
	conjuncts := RE.SplitAnd(pred)
	if len(conjuncts) <= 1 {
		return pred
	}

	keep := make([]PS.Expr, 0, len(conjuncts))
	for i, p := range conjuncts {
		redundant := false
		for j, q := range conjuncts {
			if i == j {
				continue
			}
			if implies(q, p) {
				redundant = true
				break
			}
		}
		if !redundant {
			keep = append(keep, p)
		}
	}

	if len(keep) == 0 {
		// All conjuncts were mutually implied (e.g., a=5 AND a=5).
		// Keep just the first one.
		return conjuncts[0]
	}
	if len(keep) == 1 {
		return keep[0]
	}

	// Rebuild AND chain
	result := keep[len(keep)-1]
	for i := len(keep) - 2; i >= 0; i-- {
		result = &PS.BinaryExpr{
			Op:    LX.T_AND,
			Left:  keep[i],
			Right: result,
		}
	}
	return result
}

// columnFromExpr extracts a case-insensitive column name from an
// expression node. Handles Ident, QualifiedName, and wraps
// LiteralExpr types as non-column (empty string).
func columnFromExpr(e PS.Expr) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *PS.Ident:
		return strings.ToLower(n.Name)
	case *PS.QualifiedName:
		return strings.ToLower(n.Name)
	}
	return ""
}

// numberValue tries to extract an int64 value from a literal
// expression. Returns (val, true) on success.
func numberValue(e PS.Expr) (int64, bool) {
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val, true
	}
	if f, ok := e.(*PS.FloatLiteral); ok {
		return int64(f.Val), true
	}
	return 0, false
}

// implies reports whether predicate a logically implies predicate b.
// Both must reference the same column (case-insensitive). Only
// same-column comparison chains are supported.
func implies(a, b PS.Expr) bool {
	if a == nil || b == nil {
		return false
	}

	aCol := columnFromExpr(getLeftmostColumn(a))
	bCol := columnFromExpr(getLeftmostColumn(b))
	if aCol == "" || bCol == "" {
		return false
	}
	if aCol != bCol {
		return false
	}

	return impliesSameColumn(a, b)
}

// getLeftmostColumn extracts the column referenced on the left side
// of a comparison, or the single column of a BetweenExpr. For
// non-comparison expressions returns nil.
func getLeftmostColumn(e PS.Expr) PS.Expr {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case *PS.BinaryExpr:
		return v.Left
	case *PS.BetweenExpr:
		return v.Expr
	}
	return nil
}

// cmpInfo holds extracted comparison metadata.
type cmpInfo struct {
	op    LX.TokenType
	lower PS.Expr // for BETWEEN: low bound; for comparisons: right literal
	upper PS.Expr // for BETWEEN: high bound; unused for comparisons
}

// extractCmpInfo pulls comparison operator and literal bounds from an
// expression. Returns ok=false when the expression is not a supported
// comparison shape.
func extractCmpInfo(e PS.Expr) (cmpInfo, bool) {
	if e == nil {
		return cmpInfo{}, false
	}
	switch v := e.(type) {
	case *PS.BinaryExpr:
		right, ok := numberValue(v.Right)
		if !ok {
			return cmpInfo{}, false
		}
		return cmpInfo{op: v.Op, lower: &PS.NumberLiteral{Val: right}}, true
	case *PS.BetweenExpr:
		low, ok1 := numberValue(v.Low)
		high, ok2 := numberValue(v.High)
		if !ok1 || !ok2 {
			return cmpInfo{}, false
		}
		return cmpInfo{
			op:    LX.T_BETWEEN,
			lower: &PS.NumberLiteral{Val: low},
			upper: &PS.NumberLiteral{Val: high},
		}, true
	}
	return cmpInfo{}, false
}

// impliesSameColumn checks whether a implies b assuming both reference
// the same column.
func impliesSameColumn(a, b PS.Expr) bool {
	ca, okA := extractCmpInfo(a)
	cb, okB := extractCmpInfo(b)

	// If both are extractable as comparisons, use rule-based logic.
	if okA && okB {
		return applyRule(ca, cb)
	}

	// If only one is extractable, the other is a complex expression
	// we cannot reason about — conservative: do not eliminate.
	if okA != okB {
		return false
	}

	// Fallback: equality implies range. If a is equality and b is a
	// range on the same column with the same value, a implies b.
	if eq, ok := isEquality(a); ok {
		return impliesEqualityRange(eq, b)
	}
	return false
}

// isEquality checks if e is a binary equality (T_EQ) between a column
// and a numeric literal. Returns the literal value or (0, false).
func isEquality(e PS.Expr) (int64, bool) {
	if be, ok := e.(*PS.BinaryExpr); ok && be.Op == LX.T_EQ {
		return numberValue(be.Right)
	}
	return 0, false
}

// impliesEqualityRange reports whether a = val implies e. Supported
// shapes for e: >= val, <= val, > val-1, < val+1, >=, <=, =, >, <.
func impliesEqualityRange(val int64, e PS.Expr) bool {
	if be, ok := e.(*PS.BinaryExpr); ok {
		switch be.Op {
		case LX.T_EQ:
			v, ok := numberValue(be.Right)
			return ok && v == val
		case LX.T_GE:
			v, ok := numberValue(be.Right)
			return ok && v <= val
		case LX.T_LE:
			v, ok := numberValue(be.Right)
			return ok && v >= val
		case LX.T_GT:
			v, ok := numberValue(be.Right)
			return ok && v < val
		case LX.T_LT:
			v, ok := numberValue(be.Right)
			return ok && v > val
		}
	}
	if be, ok := e.(*PS.BetweenExpr); ok {
		low, ok1 := numberValue(be.Low)
		high, ok2 := numberValue(be.High)
		return ok1 && ok2 && low <= val && high >= val
	}
	return false
}

// applyRule applies the implication rules for two comparison infos.
//
// Rules:
//   - eq implies ge, le, gt(val-1), lt(val+1)
//   - between(lo,hi) implies ge(lo) and le(hi)
//   - gt implies ge (strict greater implies non-strict)
//   - lt implies le
//   - ge does NOT imply gt
//   - le does NOT imply lt
func applyRule(a, b cmpInfo) bool {
	switch a.op {
	case LX.T_EQ:
		return eqImplies(a, b)
	case LX.T_BETWEEN:
		return betweenImplies(a, b)
	case LX.T_GT:
		return gtImplies(a, b)
	case LX.T_LT:
		return ltImplies(a, b)
	case LX.T_GE:
		return geImplies(a, b)
	case LX.T_LE:
		return leImplies(a, b)
	case LX.T_NE:
		return false // != has no implications
	}
	return false
}

func eqImplies(a, b cmpInfo) bool {
	switch b.op {
	case LX.T_EQ:
		bv, _ := numberValue(b.lower)
		av, _ := numberValue(a.lower)
		return bv == av
	case LX.T_GE:
		bv, _ := numberValue(b.lower)
		av, _ := numberValue(a.lower)
		return bv <= av
	case LX.T_LE:
		bv, _ := numberValue(b.lower)
		av, _ := numberValue(a.lower)
		return bv >= av
	case LX.T_GT:
		bv, _ := numberValue(b.lower)
		av, _ := numberValue(a.lower)
		return bv < av
	case LX.T_LT:
		bv, _ := numberValue(b.lower)
		av, _ := numberValue(a.lower)
		return bv > av
	}
	return false
}

func betweenImplies(a, b cmpInfo) bool {
	al, _ := numberValue(a.lower)
	au, _ := numberValue(a.upper)
	switch b.op {
	case LX.T_GE:
		bv, _ := numberValue(b.lower)
		return bv >= al
	case LX.T_LE:
		bv, _ := numberValue(b.lower)
		return bv <= au
	case LX.T_GT:
		// a BETWEEN lo AND hi implies a > x iff x < lo.
		bv, _ := numberValue(b.lower)
		return bv < al
	case LX.T_LT:
		bv, _ := numberValue(b.lower)
		// a BETWEEN lo AND hi implies a < x iff x > hi, i.e. x >= hi+1.
		return bv > au
	case LX.T_EQ:
		bv, _ := numberValue(b.lower)
		return bv >= al && bv <= au
	case LX.T_BETWEEN:
		bl, _ := numberValue(b.lower)
		bu, _ := numberValue(b.upper)
		return bl >= al && bu <= au
	}
	return false
}

func gtImplies(a, b cmpInfo) bool {
	av, _ := numberValue(a.lower)
	switch b.op {
	case LX.T_GE:
		bv, _ := numberValue(b.lower)
		return bv <= av
	case LX.T_GT:
		bv, _ := numberValue(b.lower)
		return bv <= av
	case LX.T_LE:
		return false
	case LX.T_LT:
		return false
	case LX.T_EQ:
		return false
	}
	return false
}

func ltImplies(a, b cmpInfo) bool {
	av, _ := numberValue(a.lower)
	switch b.op {
	case LX.T_LE:
		bv, _ := numberValue(b.lower)
		return bv >= av
	case LX.T_LT:
		bv, _ := numberValue(b.lower)
		return bv >= av
	case LX.T_GE:
		return false
	case LX.T_GT:
		return false
	case LX.T_EQ:
		return false
	}
	return false
}

func geImplies(a, b cmpInfo) bool {
	// a >= x does NOT imply a > x.
	// a >= x implies a >= x (same) and a > x-1.
	av, _ := numberValue(a.lower)
	switch b.op {
	case LX.T_GE:
		bv, _ := numberValue(b.lower)
		return bv <= av
	case LX.T_GT:
		bv, _ := numberValue(b.lower)
		return bv < av
	case LX.T_LE:
		return false
	case LX.T_LT:
		return false
	case LX.T_EQ:
		return false
	}
	return false
}

func leImplies(a, b cmpInfo) bool {
	// a <= x does NOT imply a < x.
	// a <= x implies a <= x (same) and a < x+1.
	av, _ := numberValue(a.lower)
	switch b.op {
	case LX.T_LE:
		bv, _ := numberValue(b.lower)
		return bv >= av
	case LX.T_LT:
		bv, _ := numberValue(b.lower)
		return bv > av
	case LX.T_GE:
		return false
	case LX.T_GT:
		return false
	case LX.T_EQ:
		return false
	}
	return false
}
