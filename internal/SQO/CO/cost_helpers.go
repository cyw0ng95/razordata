// Package CO hosts cost-model and predicate-analysis logic for the
// optimizer. Functions are moved from SQB/EX (REQ001439, REQ001440)
// to break the SQF → SQO → SQB dependency direction: SQO/CO imports
// SQF only; never SQB.
//
// REQ001439: predicate cost helpers. The original definitions live
// in SQB/EX/predicate.go and are wrapped in this commit; SQB/EX
// callers continue to call them through thin wrappers until
// later commits switch call sites to CO.* directly.
package CO

import (
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// Cost returns a heuristic cost for evaluating a predicate
// expression. Lower cost = evaluated first.
//
// Cost scale (matches SQB/EX/predicate.go:35):
//   - Ident/Literal = 1 (trivial column read / constant)
//   - Comparison (=, !=, <, <=, >, >=) = 2 + children
//   - Arithmetic (+, -, *, /, etc.) = 3 + children
//   - Unary NOT = 1 + child; unary arithmetic = 2 + child
//   - Function call = 5 + children
//   - InExpr = 5 + children
//   - BETWEEN = 4 + children
//   - Cast = 3 + child
//   - CASE = 3 + branches
//   - LIKE/GLOB = 10 + children (expensive string matching)
//   - EXISTS/Subquery = 100 (expensive)
//   - AND = sum of children (reordered for short-circuit)
//   - OR = sum of children
func Cost(e PS.Expr) int {
	if e == nil {
		return 0
	}
	switch v := e.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral,
		*PS.BoolLiteral, *PS.NullLiteral, *PS.StarExpr, *PS.Param:
		return 1
	case *PS.Ident, *PS.QualifiedName:
		return 1
	case *PS.BinaryExpr:
		switch v.Op {
		case LX.T_AND, LX.T_OR:
			return Cost(v.Left) + Cost(v.Right)
		case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
			return 2 + Cost(v.Left) + Cost(v.Right)
		case LX.T_LIKE, LX.T_GLOB:
			return 10 + Cost(v.Left) + Cost(v.Right)
		case LX.T_IN:
			return 5 + Cost(v.Left) + Cost(v.Right)
		case LX.T_CONCAT:
			return 4 + Cost(v.Left) + Cost(v.Right)
		default:
			return 3 + Cost(v.Left) + Cost(v.Right)
		}
	case *PS.UnaryExpr:
		switch v.Op {
		case LX.T_NOT:
			return 1 + Cost(v.Operand)
		default:
			return 2 + Cost(v.Operand)
		}
	case *PS.FunctionCall:
		return 5 + FuncArgCost(v.Args)
	case *PS.CaseExpr:
		return 3 + CaseExprCost(v)
	case *PS.CastExpr:
		return 3 + Cost(v.Expr)
	case *PS.BetweenExpr:
		return 4 + Cost(v.Expr) + Cost(v.Low) + Cost(v.High)
	case *PS.InExpr:
		return 5 + Cost(v.Expr) + FuncArgCost(v.List)
	case *PS.ExistsExpr, *PS.SubqueryExpr:
		return 100
	case *PS.AggregateFunc:
		return 5
	case *PS.WindowFunc:
		return 10
	case *PS.ListExpr:
		return FuncArgCost(v.Items)
	case *PS.IntervalLiteral:
		return 2
	case *PS.AliasedExpr:
		return Cost(v.Expr)
	case *PS.RaiseFunc:
		return 1
	default:
		return 5
	}
}

// FuncArgCost sums the cost of each argument in a function
// call or list. Mirrors SQB/EX/predicate.go:100.
func FuncArgCost(args []PS.Expr) int {
	sum := 0
	for _, a := range args {
		sum += Cost(a)
	}
	return sum
}

// CaseExprCost computes the cost of a CASE expression. Mirrors
// SQB/EX/predicate.go:108.
func CaseExprCost(e *PS.CaseExpr) int {
	cost := 0
	if e.Expr != nil {
		cost += Cost(e.Expr)
	}
	for _, w := range e.WhenList {
		cost += Cost(w.Cond) + Cost(w.Then)
	}
	if e.Else != nil {
		cost += Cost(e.Else)
	}
	return cost
}
