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

func compactColumn(col UT.Column, sel []uint16, physicalSize int) UT.Column {
	n := len(sel)
	out := UT.Column{Name: col.Name, Type: col.Type}
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		out.Data.Ints = make([]int64, n)
		for j, idx := range sel {
			if int(idx) < len(col.Data.Ints) {
				out.Data.Ints[j] = col.Data.Ints[idx]
			}
		}
	case LX.T_FLOAT_KW:
		out.Data.Floats = make([]float64, n)
		for j, idx := range sel {
			if int(idx) < len(col.Data.Floats) {
				out.Data.Floats[j] = col.Data.Floats[idx]
			}
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		out.Data.Strs = make([]string, n)
		for j, idx := range sel {
			if int(idx) < len(col.Data.Strs) {
				out.Data.Strs[j] = col.Data.Strs[idx]
			}
		}
	case LX.T_BOOL:
		out.Data.Bools = make([]bool, n)
		for j, idx := range sel {
			if int(idx) < len(col.Data.Bools) {
				out.Data.Bools[j] = col.Data.Bools[idx]
			}
		}
	default:
		return col
	}
	if col.Nulls != nil {
		out.Nulls = make([]bool, n)
		for j, idx := range sel {
			if int(idx) < physicalSize && int(idx) < len(col.Nulls) {
				out.Nulls[j] = col.Nulls[idx]
			}
		}
	}
	return out
}