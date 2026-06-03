package EX

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

var ErrEval = errors.New("ex: eval error")
var ErrDivByZero = errors.New("ex: division by zero")
var ErrTypeMismatch = errors.New("ex: type mismatch")
var ErrSubquery = errors.New("ex: subquery not supported here")

func Eval(expr PS.Expr, row *Row, params []interface{}) (interface{}, error) {
	if expr == nil {
		return nil, nil
	}

	switch e := expr.(type) {
	case *PS.NumberLiteral:
		return e.Val, nil
	case *PS.FloatLiteral:
		return e.Val, nil
	case *PS.StringLiteral:
		return e.Val, nil
	case *PS.BoolLiteral:
		return e.Val, nil
	case *PS.NullLiteral:
		return nil, nil
	case *PS.Ident:
		if row != nil {
			if v, ok := row.Lookup(e.Name); ok {
				return v, nil
			}
		}
		return e.Name, nil
	case *PS.QualifiedName:
		if row != nil {
			key := e.Table + "." + e.Name
			for cur := row; cur != nil; cur = cur.Outer {
				for i, c := range cur.Cols {
					if c == key {
						if i < len(cur.Data) {
							return cur.Data[i], nil
						}
					}
				}
			}
		}
		return e.Table + "." + e.Name, nil
	case *PS.Param:
		if e.Index < len(params) {
			return params[e.Index], nil
		}
		return nil, nil
	case *PS.StarExpr:
		return "*", nil
	case *PS.UnaryExpr:
		return evalUnary(e, row, params)
	case *PS.BinaryExpr:
		return evalBinary(e, row, params)
	case *PS.ListExpr:
		return e.Items, nil
	case *PS.BetweenExpr:
		return evalBetween(e, row, params)
	case *PS.InExpr:
		return evalIn(e, row, params)
	case *PS.ExistsExpr:
		return evalExists(e, row, params)
	case *PS.SubqueryExpr:
		return evalScalarSubquery(e, row, params)
	case *PS.CaseExpr:
		return evalCase(e, row, params)
	case *PS.AggregateFunc:
		return evalAggregate(e, row, params)
	case *PS.FunctionCall:
		return evalFunction(e, row, params)
	case *PS.CastExpr:
		return evalCast(e, row, params)
	case *PS.AliasedExpr:
		return Eval(e.Expr, row, params)
	default:
		return nil, ErrEval
	}
}

func evalUnary(e *PS.UnaryExpr, row *Row, params []interface{}) (interface{}, error) {
	operand, err := Eval(e.Operand, row, params)
	if err != nil {
		return nil, err
	}

	switch e.Op {
	case int(LX.T_MINUS):
		switch v := operand.(type) {
		case int64:
			return -v, nil
		case float64:
			return -v, nil
		}
	case int(LX.T_PLUS):
		return operand, nil
	case int(LX.T_NOT):
		return !truthy(operand), nil
	}
	return nil, ErrEval
}

func evalBinary(e *PS.BinaryExpr, row *Row, params []interface{}) (interface{}, error) {
	left, err := Eval(e.Left, row, params)
	if err != nil {
		return nil, err
	}
	right, err := Eval(e.Right, row, params)
	if err != nil {
		return nil, err
	}

	switch e.Op {
	case int(LX.T_EQ):
		return equalValue(left, right), nil
	case int(LX.T_NE):
		return !equalValue(left, right), nil
	case int(LX.T_LT):
		return compare(left, right) < 0, nil
	case int(LX.T_LE):
		return compare(left, right) <= 0, nil
	case int(LX.T_GT):
		return compare(left, right) > 0, nil
	case int(LX.T_GE):
		return compare(left, right) >= 0, nil
	case int(LX.T_PLUS):
		return add(left, right)
	case int(LX.T_MINUS):
		return sub(left, right)
	case int(LX.T_STAR):
		return mul(left, right)
	case int(LX.T_SLASH):
		return div(left, right)
	case int(LX.T_AND):
		return band(left, right)
	case int(LX.T_OR):
		return bor(left, right)
	case int(LX.T_LIKE):
		return like(left, right)
	case int(LX.T_IS):
		return is(left, right)
	}
	return nil, ErrEval
}

func evalBetween(e *PS.BetweenExpr, row *Row, params []interface{}) (interface{}, error) {
	expr, err := Eval(e.Expr, row, params)
	if err != nil {
		return nil, err
	}
	low, err := Eval(e.Low, row, params)
	if err != nil {
		return nil, err
	}
	high, err := Eval(e.High, row, params)
	if err != nil {
		return nil, err
	}
	cmpLow := compare(expr, low)
	cmpHigh := compare(expr, high)
	return cmpLow >= 0 && cmpHigh <= 0, nil
}

func evalIn(e *PS.InExpr, row *Row, params []interface{}) (interface{}, error) {
	target, err := Eval(e.Expr, row, params)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return false, nil
	}
	if e.Subquery != nil {
		return evalInSubquery(target, e.Subquery, row, params)
	}
	for _, item := range e.List {
		v, err := Eval(item, row, params)
		if err != nil {
			return nil, err
		}
		if equalValue(target, v) {
			return true, nil
		}
	}
	return false, nil
}

func evalInSubquery(target interface{}, subq PS.Stmt, outer *Row, params []interface{}) (interface{}, error) {
	sel, ok := subq.(*PS.Select)
	if !ok {
		return nil, ErrSubquery
	}
	pl, err := NewPlanner().Plan(sel)
	if err != nil {
		return nil, err
	}
	rows, err := runSubqueryPlan(pl, outer, params)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if len(row.Cols) == 0 {
			continue
		}
		if equalValue(target, row.Data[0]) {
			return true, nil
		}
	}
	return false, nil
}

// EvalForTest exposes Eval for tests; do not use in production
// code paths where the row may not be valid.
func EvalForTest(e PS.Expr, row *Row, params []interface{}) (interface{}, error) {
	return Eval(e, row, params)
}

func evalExists(e *PS.ExistsExpr, outer *Row, params []interface{}) (interface{}, error) {
	sel, ok := e.Subquery.(*PS.Select)
	if !ok {
		return nil, ErrSubquery
	}
	pl, err := NewPlanner().Plan(sel)
	if err != nil {
		return nil, err
	}
	rows, err := runSubqueryPlan(pl, outer, params)
	if err != nil {
		return nil, err
	}
	return len(rows) > 0, nil
}

func evalScalarSubquery(e *PS.SubqueryExpr, outer *Row, params []interface{}) (interface{}, error) {
	sel, ok := e.Subquery.(*PS.Select)
	if !ok {
		return nil, ErrSubquery
	}
	pl, err := NewPlanner().Plan(sel)
	if err != nil {
		return nil, err
	}
	rows, err := runSubqueryPlan(pl, outer, params)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows[0].Data) == 0 {
		return nil, nil
	}
	return rows[0].Data[0], nil
}

func evalCast(e *PS.CastExpr, row *Row, params []interface{}) (interface{}, error) {
	v, err := Eval(e.Expr, row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	switch LX.TokenType(e.Type) {
	case LX.T_INT_KW, LX.T_BIGINT:
		switch x := v.(type) {
		case int64:
			return x, nil
		case float64:
			return int64(x), nil
		case string:
			n, err := strconv.ParseInt(x, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("ex: cast %q to int: %w", x, err)
			}
			return n, nil
		case bool:
			if x {
				return int64(1), nil
			}
			return int64(0), nil
		}
	case LX.T_FLOAT_KW:
		switch x := v.(type) {
		case int64:
			return float64(x), nil
		case float64:
			return x, nil
		case string:
			f, err := strconv.ParseFloat(x, 64)
			if err != nil {
				return nil, fmt.Errorf("ex: cast %q to float: %w", x, err)
			}
			return f, nil
		}
	case LX.T_TEXT:
		return fmt.Sprintf("%v", v), nil
	case LX.T_BOOL:
		return truthy(v), nil
	}
	return nil, ErrEval
}

func evalCase(e *PS.CaseExpr, row *Row, params []interface{}) (interface{}, error) {
	if e.Expr != nil {
		target, err := Eval(e.Expr, row, params)
		if err != nil {
			return nil, err
		}
		for _, w := range e.WhenList {
			v, err := Eval(w.Cond, row, params)
			if err != nil {
				return nil, err
			}
			if equalValue(target, v) {
				return Eval(w.Then, row, params)
			}
		}
	} else {
		for _, w := range e.WhenList {
			cond, err := Eval(w.Cond, row, params)
			if err != nil {
				return nil, err
			}
			if truthy(cond) {
				return Eval(w.Then, row, params)
			}
		}
	}
	if e.Else != nil {
		return Eval(e.Else, row, params)
	}
	return nil, nil
}

func truthy(v interface{}) bool {
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	if i, ok := v.(int64); ok {
		return i != 0
	}
	if f, ok := v.(float64); ok {
		return f != 0
	}
	if s, ok := v.(string); ok {
		return s != ""
	}
	return true
}

func evalAggregate(e *PS.AggregateFunc, row *Row, params []interface{}) (interface{}, error) {
	switch e.Name {
	case "COUNT":
		return int64(0), nil
	case "SUM":
		return int64(0), nil
	case "AVG":
		return float64(0), nil
	case "MIN":
		return nil, nil
	case "MAX":
		return nil, nil
	}
	return nil, ErrEval
}

func evalFunction(e *PS.FunctionCall, row *Row, params []interface{}) (interface{}, error) {
	switch e.Name {
	case "LENGTH":
		if len(e.Args) > 0 {
			if s, ok := e.Args[0].(*PS.StringLiteral); ok {
				return int64(len(s.Val)), nil
			}
		}
	case "IFNULL":
		if len(e.Args) == 2 {
			v1, _ := Eval(e.Args[0], row, params)
			if v1 == nil {
				return Eval(e.Args[1], row, params)
			}
			return v1, nil
		}
	case "COALESCE":
		for _, arg := range e.Args {
			v, _ := Eval(arg, row, params)
			if v != nil {
				return v, nil
			}
		}
	case "NOW":
		return "CURRENT_TIMESTAMP", nil
	case "SUBSTR":
		return "", nil
	}
	return nil, ErrEval
}

func compare(a, b interface{}) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	if af, aok := numericFloat(a); aok {
		if bf, bok := numericFloat(b); bok {
			return cmpFloat(af, bf)
		}
	}
	switch v := a.(type) {
	case string:
		if vb, ok := b.(string); ok {
			return cmpString(v, vb)
		}
	case bool:
		if vb, ok := b.(bool); ok {
			return cmpBool(v, vb)
		}
	}
	return 0
}

func cmpFloat(a, b float64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpBool(a, b bool) int {
	if a == b {
		return 0
	}
	if a {
		return 1
	}
	return -1
}

func add(a, b interface{}) (interface{}, error) {
	return numericArith(a, b, '+')
}

func sub(a, b interface{}) (interface{}, error) {
	return numericArith(a, b, '-')
}

func mul(a, b interface{}) (interface{}, error) {
	return numericArith(a, b, '*')
}

func div(a, b interface{}) (interface{}, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	if ai, ok := a.(int64); ok {
		if bi, ok := b.(int64); ok {
			if bi == 0 {
				return nil, ErrDivByZero
			}
			return ai / bi, nil
		}
	}
	af, aok := numericFloat(a)
	bf, bok := numericFloat(b)
	if !aok || !bok {
		return nil, nil
	}
	if bf == 0 {
		return nil, ErrDivByZero
	}
	return af / bf, nil
}

func numericArith(a, b interface{}, op rune) (interface{}, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	af, aok := numericFloat(a)
	bf, bok := numericFloat(b)
	if !aok || !bok {
		return nil, nil
	}
	var r float64
	switch op {
	case '+':
		r = af + bf
	case '-':
		r = af - bf
	case '*':
		r = af * bf
	}
	if _, aok := a.(int64); aok {
		if _, bok := b.(int64); bok {
			ai := int64(af)
			bi := int64(bf)
			var ri int64
			switch op {
			case '+':
				ri = ai + bi
			case '-':
				ri = ai - bi
			case '*':
				ri = ai * bi
			}
			return ri, nil
		}
	}
	return r, nil
}

func numericFloat(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func band(a, b interface{}) (interface{}, error) {
	if a == true && b == true {
		return true, nil
	}
	return false, nil
}

func bor(a, b interface{}) (interface{}, error) {
	if a == true || b == true {
		return true, nil
	}
	return false, nil
}

func like(a, b interface{}) (bool, error) {
	s, ok := a.(string)
	if !ok {
		return false, nil
	}
	pattern, ok := b.(string)
	if !ok {
		return false, nil
	}
	return matchLike(pattern, s), nil
}

func matchLike(pattern, s string) bool {
	pi, si := 0, 0
	starPI, starSI := -1, -1
	for si < len(s) {
		if pi < len(pattern) {
			c := pattern[pi]
			switch c {
			case '%':
				starPI = pi
				starSI = si
				pi++
				continue
			case '_':
				pi++
				si++
				continue
			}
			if c == s[si] {
				pi++
				si++
				continue
			}
		}
		if starPI >= 0 {
			pi = starPI + 1
			starSI++
			si = starSI
			continue
		}
		return false
	}
	for pi < len(pattern) && pattern[pi] == '%' {
		pi++
	}
	return pi == len(pattern)
}

func is(a, b interface{}) (bool, error) {
	if a == nil && b == nil {
		return true, nil
	}
	if a == nil || b == nil {
		return false, nil
	}
	return a == b, nil
}

func equalValue(a, b interface{}) bool {
	if a == nil || b == nil {
		return a == b
	}
	if ai, ok := a.(int64); ok {
		switch v := b.(type) {
		case int64:
			return ai == v
		case float64:
			return float64(ai) == v
		}
	}
	if af, ok := a.(float64); ok {
		switch v := b.(type) {
		case int64:
			return af == float64(v)
		case float64:
			return af == v
		}
	}
	return a == b
}
