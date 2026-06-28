// Package EX: Values operator for SELECT without FROM clause.
// REQ000357: `SELECT 1+1`, `SELECT 10 & 6` should return one row
// with the evaluated expression, not zero rows.

package EX

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// Values implements a single-row operator that evaluates scalar
// expressions without a FROM source. Used for `SELECT expr[,expr...]`.
type Values struct {
	cols      []PS.Expr
	evaluated bool
	row       Row
	planner   *Planner
	params    []any
	execCtx   *ExecContext // REQ000853: for CHANGES()/TOTAL_CHANGES() eval
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

	// Evaluate each expression with a nil row (no table context).
	// If a planner is available, attach it to the row so subquery
	// evaluation can resolve tables against the store.
	// REQ000853: also attach execCtx so CHANGES()/TOTAL_CHANGES()
	// can read session-level change counters.
	var evalRow *Row
	if v.planner != nil || v.execCtx != nil {
		evalRow = &Row{planner: v.planner, execCtx: v.execCtx}
	}
	cols := make([]string, len(v.cols))
	data := make([]Value, len(v.cols))
	types := make([]LX.TokenType, len(v.cols))

	for i, e := range v.cols {
		val, err := EvalValue(e, evalRow, v.params)
		if err != nil {
			return Row{}, err
		}
		cols[i] = exprString(e)
		data[i] = val
		types[i] = inferType(val.ToAny())
	}

	v.row = Row{Cols: cols, Types: types, Data: data}
	return v.row, nil
}

func (v *Values) Close() error {
	v.evaluated = false
	return nil
}

func (v *Values) WithParams(p []any) Operator {
	if v == nil {
		return nil
	}
	v.params = p
	return v
}

func (v *Values) WithPlanner(p *Planner) Operator {
	if v == nil {
		return nil
	}
	v.planner = p
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
func inferType(v any) LX.TokenType {
	if v == nil {
		return LX.TokenType(-1)
	}
	switch v.(type) {
	case int64, int, int32:
		return LX.T_INT_KW
	case float64, float32:
		return LX.T_FLOAT_KW
	case bool:
		return LX.T_BOOL
	case string:
		return LX.T_TEXT
	case []byte:
		return LX.T_BLOB
	}
	return LX.T_TEXT
}

// ValuesRows implements a multi-row operator for standalone VALUES
// statements (REQ000564). Each row is a list of scalar expressions.
type ValuesRows struct {
	rows    [][]PS.Expr
	pos     int
	colExpr []PS.Expr // cached from first row for column metadata
	schema  []string
}

var _ Operator = (*ValuesRows)(nil)

func newValuesRowsOp(rows [][]PS.Expr) *ValuesRows {
	colExpr := rows[0]
	names := make([]string, len(colExpr))
	for i := range colExpr {
		names[i] = exprString(colExpr[i])
	}
	return &ValuesRows{rows: rows, colExpr: colExpr, schema: names}
}

func (v *ValuesRows) Next(ctx context.Context) (Row, error) {
	if v.pos >= len(v.rows) {
		return Row{}, ErrNoRows
	}
	rowExprs := v.rows[v.pos]
	v.pos++
	cols := make([]string, len(rowExprs))
	data := make([]Value, len(rowExprs))
	types := make([]LX.TokenType, len(rowExprs))
	for i, e := range rowExprs {
		val, err := EvalValue(e, nil, nil)
		if err != nil {
			return Row{}, err
		}
		if v.pos == 1 && i < len(v.schema) {
			cols[i] = v.schema[i]
		} else {
			cols[i] = exprString(e)
		}
		data[i] = val
		types[i] = inferType(val.ToAny())
	}
	return Row{Cols: cols, Types: types, Data: data}, nil
}

func (v *ValuesRows) Close() error {
	v.pos = 0
	return nil
}

func (v *ValuesRows) WithParams(p []any) Operator {
	return v
}
