package EX

import (
	"errors"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

var ErrEval = errors.New("ex: eval error")

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
		return e.Name, nil
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
	case *PS.CaseExpr:
		return evalCase(e, row, params)
	case *PS.AggregateFunc:
		return evalAggregate(e, row, params)
	case *PS.FunctionCall:
		return evalFunction(e, row, params)
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
		if b, ok := operand.(bool); ok {
			return !b, nil
		}
		if operand == nil {
			return true, nil
		}
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
		return left == right, nil
	case int(LX.T_NE):
		return left != right, nil
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
	case int(LX.T_IN):
		return in(left, right)
	case int(LX.T_BETWEEN):
		return between(left, right)
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

func evalCase(e *PS.CaseExpr, row *Row, params []interface{}) (interface{}, error) {
	for _, w := range e.WhenList {
		cond, err := Eval(w.Cond, row, params)
		if err != nil {
			return nil, err
		}
		if cond == true {
			return Eval(w.Then, row, params)
		}
	}
	if e.Else != nil {
		return Eval(e.Else, row, params)
	}
	return nil, nil
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
	switch v := a.(type) {
	case int64:
		if vb, ok := b.(int64); ok {
			if v < vb {
				return -1
			}
			if v > vb {
				return 1
			}
			return 0
		}
	case float64:
		if vb, ok := b.(float64); ok {
			if v < vb {
				return -1
			}
			if v > vb {
				return 1
			}
			return 0
		}
	case string:
		if vb, ok := b.(string); ok {
			if v < vb {
				return -1
			}
			if v > vb {
				return 1
			}
			return 0
		}
	case bool:
		if vb, ok := b.(bool); ok {
			if v == vb {
				return 0
			}
			if v {
				return 1
			}
			return -1
		}
	}
	return 0
}

func add(a, b interface{}) (interface{}, error) {
	switch a.(type) {
	case int64:
		if b, ok := b.(int64); ok {
			return a.(int64) + b, nil
		}
	case float64:
		if b, ok := b.(float64); ok {
			return a.(float64) + b, nil
		}
	}
	return nil, ErrEval
}

func sub(a, b interface{}) (interface{}, error) {
	switch a.(type) {
	case int64:
		if b, ok := b.(int64); ok {
			return a.(int64) - b, nil
		}
	case float64:
		if b, ok := b.(float64); ok {
			return a.(float64) - b, nil
		}
	}
	return nil, ErrEval
}

func mul(a, b interface{}) (interface{}, error) {
	switch a.(type) {
	case int64:
		if b, ok := b.(int64); ok {
			return a.(int64) * b, nil
		}
	case float64:
		if b, ok := b.(float64); ok {
			return a.(float64) * b, nil
		}
	}
	return nil, ErrEval
}

func div(a, b interface{}) (interface{}, error) {
	switch a.(type) {
	case int64:
		if b, ok := b.(int64); ok {
			if b == 0 {
				return nil, ErrEval
			}
			return a.(int64) / b, nil
		}
	case float64:
		if b, ok := b.(float64); ok {
			if b == 0 {
				return nil, ErrEval
			}
			return a.(float64) / b, nil
		}
	}
	return nil, ErrEval
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
	return len(pattern) > 0 && len(s) > 0, nil
}

func in(a, b interface{}) (bool, error) {
	return false, nil
}

func between(a, b interface{}) (bool, error) {
	return false, nil
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
