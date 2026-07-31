package OP

import (
	"github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type batchEvalFunc func(batch *UT.Batch) UT.Column

func compileProjectExpr(expr PS.Expr) batchEvalFunc {
	if expr == nil {
		return func(batch *UT.Batch) UT.Column {
			return UT.Column{Type: LX.T_NULL}
		}
	}
	switch e := expr.(type) {
	case *PS.Ident:
		return func(batch *UT.Batch) UT.Column {
			if col, ok := EV.ExtractColumnRef(e, batch); ok {
				return col
			}
			return UT.Column{Type: LX.T_NULL}
		}
	case *PS.NumberLiteral:
		val := e.Val
		return func(batch *UT.Batch) UT.Column {
			return EV.FillLiteralColumn(batch, LX.T_INT_KW, val)
		}
	case *PS.FloatLiteral:
		val := e.Val
		return func(batch *UT.Batch) UT.Column {
			return EV.FillLiteralColumn(batch, LX.T_FLOAT_KW, val)
		}
	case *PS.StringLiteral:
		val := e.Val
		return func(batch *UT.Batch) UT.Column {
			return EV.FillLiteralColumn(batch, LX.T_TEXT, val)
		}
	case *PS.BoolLiteral:
		val := e.Val
		return func(batch *UT.Batch) UT.Column {
			return EV.FillLiteralColumn(batch, LX.T_BOOL, val)
		}
	case *PS.NullLiteral:
		return func(batch *UT.Batch) UT.Column {
			return EV.FillNullColumn(batch)
		}
	case *PS.AliasedExpr:
		inner := compileProjectExpr(e.Expr)
		if inner != nil {
			return inner
		}
	}
	return nil
}

