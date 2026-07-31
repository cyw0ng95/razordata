// Package EX: Values operator for SELECT without FROM clause.
// REQ000357: `SELECT 1+1`, `SELECT 10 & 6` should return one row
// with the evaluated expression, not zero rows.

package OP

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
)

// ConstRow is a one-shot operator that returns a single row with constant
// values (REQ001420). Unlike Values, it has no mutable state — Close() is
// a no-op, so it is safe to call from any goroutine. Used by the COUNT(*)
// fast path where the planner returns a cached row count.
type ConstRow struct {
	cols   []string
	values []Value
	types  []LX.TokenType
	done   atomic.Bool
}

var _ Operator = (*ConstRow)(nil)

// NewConstRow creates a ConstRow with the given column names, values, and types.
func NewConstRow(cols []string, values []Value, types []LX.TokenType) *ConstRow {
	return &ConstRow{cols: cols, values: values, types: types}
}

func (c *ConstRow) Next(ctx context.Context) (Row, error) {
	if c.done.Load() {
		return Row{}, ErrNoRows
	}
	c.done.Store(true)
	// Return copies of slices to avoid aliasing.
	cols := make([]string, len(c.cols))
	copy(cols, c.cols)
	vals := make([]Value, len(c.values))
	copy(vals, c.values)
	typs := make([]LX.TokenType, len(c.types))
	copy(typs, c.types)
	return Row{Cols: cols, Types: typs, Data: vals}, nil
}

func (c *ConstRow) Close() error { return nil } // no-op: no mutable state; memo skips ConstRow
func (c *ConstRow) Reset(ctx context.Context) error { c.done.Store(false); return nil }
func (c *ConstRow) WithParams(p []any) Operator { return c }

// Cols returns the column names for this ConstRow.
func (c *ConstRow) Cols() []string { return c.cols }

// Types returns the column types for this ConstRow.
func (c *ConstRow) Types() []LX.TokenType { return c.types }

// Values implements a single-row operator that evaluates scalar
// expressions without a FROM source. Used for `SELECT expr[,expr...]`.
type Values struct {
	cols      []PS.Expr
	colNames  []string // REQ001571: pre-computed column names
	evaluated bool
	row       Row
	planner   pl.QueryPlanner
	params    []any
	execCtx   *pl.ExecContext // REQ000853: for CHANGES()/TOTAL_CHANGES() eval

	// REQ001670: pre-allocated scratch buffers reused across Reset cycles.
	// The Values operator is single-shot, but Reset() allows reuse across
	// queries without re-allocating data/types slices.
	scratchData  []Value
	scratchTypes []LX.TokenType
}

// newValuesOp creates a Values operator for the given column expressions.
func NewValuesOp(cols []PS.Expr) *Values {
	v := &Values{cols: cols}
	// REQ001571: pre-compute column names at construction time.
	v.colNames = make([]string, len(cols))
	for i, e := range cols {
		v.colNames[i] = exprString(e)
	}
	return v
}

var _ Operator = (*Values)(nil)

func (v *Values) Next(ctx context.Context) (Row, error) {
	if v.evaluated {
		return Row{}, ErrNoRows
	}
	v.evaluated = true

	// Evaluate each expression with a nil row (no table context).
	// If a planner is available, attach it to the row so subquery
	// evaluation can resolve DT.Tables against the store.
	// REQ000853: also attach execCtx so CHANGES()/TOTAL_CHANGES()
	// can read session-level change counters.
	var evalRow *Row
	if v.planner != nil || v.execCtx != nil {
		evalRow = &Row{Planner: v.planner, ExecCtx: v.execCtx}
	}
	// REQ001670: reuse scratch buffers across Reset cycles.
	n := len(v.cols)
	if cap(v.scratchData) < n {
		v.scratchData = make([]Value, n)
	} else {
		v.scratchData = v.scratchData[:n]
	}
	if cap(v.scratchTypes) < n {
		v.scratchTypes = make([]LX.TokenType, n)
	} else {
		v.scratchTypes = v.scratchTypes[:n]
	}
	data := v.scratchData
	types := v.scratchTypes

	for i, e := range v.cols {
		val, err := EV.EvalValue(e, evalRow, v.params)
		if err != nil {
			return Row{}, err
		}
		data[i] = val
		types[i] = inferTypeFromValue(val)
	}

	v.row = Row{Cols: v.colNames, Types: types, Data: data}
	return v.row, nil
}

func (v *Values) Close() error {
	v.evaluated = false
	return nil
}

func (v *Values) Reset(ctx context.Context) error { v.evaluated = false; return nil }

func (v *Values) WithParams(p []any) Operator {
	if v == nil {
		return nil
	}
	v.params = p
	return v
}

func (v *Values) SetExecCtx(ec *pl.ExecContext) { v.execCtx = ec }

// ColNames returns the pre-computed column names for this Values operator.
func (v *Values) ColNames() []string { return v.colNames }

func (v *Values) WithPlanner(p pl.QueryPlanner) pl.Operator {
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
	case *PS.AliasedExpr:
		return x.Alias
	case *PS.AggregateFunc:
		// REQ001420: render COUNT(*) as "COUNT(*)" for column naming.
		if x.Arg != nil {
			if _, ok := x.Arg.(*PS.StarExpr); ok {
				return strings.ToUpper(x.Name) + "(*)"
			}
		}
		return strings.ToUpper(x.Name) + "(?)"
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
	case LX.T_DIV:
		opStr = "DIV"
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

// REQ001696/REQ001706: inferTypeFromValue avoids boxing to any by
// switching on Value.Kind directly. Used by Values.Next and
// ValuesRows.Next to eliminate per-cell ToAny() allocation.
func inferTypeFromValue(v Value) LX.TokenType {
	switch v.Kind {
	case KindNull:
		return LX.TokenType(-1)
	case KindInt:
		return LX.T_INT_KW
	case KindFloat:
		return LX.T_FLOAT_KW
	case KindBool:
		return LX.T_BOOL
	case KindText:
		return LX.T_TEXT
	case KindBlob:
		return LX.T_BLOB
	default:
		return LX.T_TEXT
	}
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

func NewValuesRowsOp(rows [][]PS.Expr) *ValuesRows {
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
	data := make([]Value, len(rowExprs))
	types := make([]LX.TokenType, len(rowExprs))
	for i, e := range rowExprs {
		val, err := EV.EvalValue(e, nil, nil)
		if err != nil {
			return Row{}, err
		}
		data[i] = val
		types[i] = inferTypeFromValue(val)
	}
	// REQ001571: use pre-computed schema for column names (first row defines them).
	cols := v.schema
	return Row{Cols: cols, Types: types, Data: data}, nil
}

func (v *ValuesRows) Rows() [][]PS.Expr { return v.rows }

func (v *ValuesRows) Close() error {
	v.pos = 0
	return nil
}

func (v *ValuesRows) Reset(ctx context.Context) error { v.pos = 0; return nil }

func (v *ValuesRows) WithParams(p []any) Operator {
	return v
}
