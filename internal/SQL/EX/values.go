// Package EX: Values operator for SELECT without FROM clause.
// REQ000357: `SELECT 1+1`, `SELECT 10 & 6` should return one row
// with the evaluated expression, not zero rows.

package EX

import (
	"context"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// Values implements a single-row operator that evaluates scalar
// expressions without a FROM source. Used for `SELECT expr[,expr...]`.
type Values struct {
	cols     []PS.Expr
	evaluated bool
	row      Row
}

// newValuesOp creates a Values operator for the given column expressions.
func newValuesOp(cols []PS.Expr) *Values {
	return &Values{cols: cols}
}

var _ Operator = (*Values)(nil)

func (v *Values) Next(ctx context.Context) (Row, error) {
	if v.evaluated {
		return Row{}, ErrNoRows
	}
	v.evaluated = true

	// Evaluate each expression with a nil row (no table context)
	cols := make([]string, len(v.cols))
	data := make([]interface{}, len(v.cols))
	types := make([]int, len(v.cols))

	for i, e := range v.cols {
		val, err := Eval(e, nil, nil)
		if err != nil {
			return Row{}, err
		}
		cols[i] = exprString(e)
		data[i] = val
		types[i] = inferType(val)
	}

	v.row = Row{Cols: cols, Types: types, Data: data}
	return v.row, nil
}

func (v *Values) Close() error {
	return nil
}

func (v *Values) WithParams(p []interface{}) Operator {
	if v == nil {
		return nil
	}
	// No child operator, nothing to propagate to
	return v
}

// exprString returns a string representation of an expression for column naming.
func exprString(e PS.Expr) string {
	switch x := e.(type) {
	case *PS.Ident:
		return x.Name
	case *PS.NumberLiteral:
		return "num"
	case *PS.FloatLiteral:
		return "num"
	case *PS.StringLiteral:
		return x.Val
	case *PS.BinaryExpr:
		return binaryOpString(x)
	case *PS.UnaryExpr:
		return unaryOpString(x)
	case *PS.FunctionCall:
		return x.Name + "(...)"
	default:
		return "?"
	}
}

func binaryOpString(b *PS.BinaryExpr) string {
	var opStr string
	switch LX.TokenType(b.Op) {
	case LX.T_PLUS:
		opStr = "+"
	case LX.T_MINUS:
		opStr = "-"
	case LX.T_STAR:
		opStr = "*"
	case LX.T_SLASH:
		opStr = "/"
	case LX.T_BITAND:
		opStr = "&"
	case LX.T_BITOR:
		opStr = "|"
	case LX.T_BITXOR:
		opStr = "^"
	case LX.T_MOD:
		opStr = "%"
	case LX.T_CONCAT:
		opStr = "||"
	default:
		return "?"
	}
	return "?" + opStr + "?"
}

func unaryOpString(u *PS.UnaryExpr) string {
	if LX.TokenType(u.Op) == LX.T_BITNOT {
		return "~?"
	}
	return "?"
}

// inferType maps a Go value to an LS column type.
func inferType(v interface{}) int {
	if v == nil {
		return -1
	}
	switch v.(type) {
	case int64, int, int32:
		return int(ls.CTInt)
	case float64, float32:
		return int(ls.CTFloat)
	case bool:
		return int(ls.CTBool)
	case string:
		return int(ls.CTText)
	case []byte:
		return int(ls.CTBlob)
	default:
		return int(ls.CTText)
	}
}
