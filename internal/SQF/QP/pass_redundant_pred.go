package QP

import (
	"strings"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// RedundantPredicateEliminationPass removes conjuncts from AND
// predicates that are logically implied by another conjunct.
// Example: WHERE a > 10 AND a > 5 → WHERE a > 10. REQ002263.
type RedundantPredicateEliminationPass struct{}

func (p *RedundantPredicateEliminationPass) Name() string {
	return "redundant_predicate_elimination"
}

func (p *RedundantPredicateEliminationPass) Apply(qp *QueryPlan) error {
	if qp == nil || qp.RootIdx < 0 {
		return nil
	}
	qp.walkBottomUp(func(idx int, node *PlanNode) bool {
		if len(node.Exprs) != 1 {
			return true
		}
		pred, ok := node.Exprs[0].(PS.Expr)
		if !ok || pred == nil {
			return true
		}
		reduced := eliminateRedundant(pred)
		if reduced != pred {
			node.Exprs[0] = reduced
		}
		return true
	})
	return nil
}

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
		return conjuncts[0]
	}
	if len(keep) == 1 {
		return keep[0]
	}
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

func implies(a, b PS.Expr) bool {
	if a == nil || b == nil {
		return false
	}
	aCol := columnFromExpr(getLeftmostColumn(a))
	bCol := columnFromExpr(getLeftmostColumn(b))
	if aCol == "" || bCol == "" || aCol != bCol {
		return false
	}
	return impliesSameColumn(a, b)
}

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

func impliesSameColumn(a, b PS.Expr) bool {
	ca, okA := extractCmpInfo(a)
	cb, okB := extractCmpInfo(b)
	if okA && okB {
		return applyRule(ca, cb)
	}
	if okA != okB {
		return false
	}
	if eq, ok := isEquality(a); ok {
		return impliesEqualityRange(eq, b)
	}
	return false
}

type cmpInfo struct {
	op    LX.TokenType
	lower PS.Expr
	upper PS.Expr
}

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
		return cmpInfo{op: LX.T_BETWEEN, lower: &PS.NumberLiteral{Val: low}, upper: &PS.NumberLiteral{Val: high}}, true
	}
	return cmpInfo{}, false
}

func numberValue(e PS.Expr) (int64, bool) {
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val, true
	}
	if f, ok := e.(*PS.FloatLiteral); ok {
		return int64(f.Val), true
	}
	return 0, false
}

func isEquality(e PS.Expr) (int64, bool) {
	if be, ok := e.(*PS.BinaryExpr); ok && be.Op == LX.T_EQ {
		return numberValue(be.Right)
	}
	return 0, false
}

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
	default:
		return false
	}
}

func eqImplies(a, b cmpInfo) bool {
	av, _ := numberValue(a.lower)
	switch b.op {
	case LX.T_EQ:
		bv, _ := numberValue(b.lower)
		return bv == av
	case LX.T_GE:
		bv, _ := numberValue(b.lower)
		return bv <= av
	case LX.T_LE:
		bv, _ := numberValue(b.lower)
		return bv >= av
	case LX.T_GT:
		bv, _ := numberValue(b.lower)
		return bv < av
	case LX.T_LT:
		bv, _ := numberValue(b.lower)
		return bv > av
	default:
		return false
	}
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
		bv, _ := numberValue(b.lower)
		return bv < al
	case LX.T_LT:
		bv, _ := numberValue(b.lower)
		return bv > au
	case LX.T_EQ:
		bv, _ := numberValue(b.lower)
		return bv >= al && bv <= au
	case LX.T_BETWEEN:
		bl, _ := numberValue(b.lower)
		bu, _ := numberValue(b.upper)
		return bl >= al && bu <= au
	default:
		return false
	}
}

func gtImplies(a, b cmpInfo) bool {
	av, _ := numberValue(a.lower)
	switch b.op {
	case LX.T_GE, LX.T_GT:
		bv, _ := numberValue(b.lower)
		return bv <= av
	default:
		return false
	}
}

func ltImplies(a, b cmpInfo) bool {
	av, _ := numberValue(a.lower)
	switch b.op {
	case LX.T_LE, LX.T_LT:
		bv, _ := numberValue(b.lower)
		return bv >= av
	default:
		return false
	}
}

func geImplies(a, b cmpInfo) bool {
	av, _ := numberValue(a.lower)
	switch b.op {
	case LX.T_GE:
		bv, _ := numberValue(b.lower)
		return bv <= av
	case LX.T_GT:
		bv, _ := numberValue(b.lower)
		return bv < av
	default:
		return false
	}
}

func leImplies(a, b cmpInfo) bool {
	av, _ := numberValue(a.lower)
	switch b.op {
	case LX.T_LE:
		bv, _ := numberValue(b.lower)
		return bv >= av
	case LX.T_LT:
		bv, _ := numberValue(b.lower)
		return bv > av
	default:
		return false
	}
}

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
