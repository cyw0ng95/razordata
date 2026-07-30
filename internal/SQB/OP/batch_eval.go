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
}// truncateBatchInPlace slices each column's Data slice to the first
// n logical rows. Handles both plain batches and batches with a
// selection vector (resolves physical indices via Sel). No new
// allocations — the underlying arrays remain owned by the batch
// and are released via Put. Mirrors the truncation logic in
// VectorizedLimit.NextBatch but operates in place.
func truncateBatchInPlace(batch *UT.Batch, n int) {
	if n < 0 {
		return
	}
	if n == 0 {
		batch.Size = 0
		batch.Sel = nil
		return
	}
	// When the batch has a selection vector, the logical rows are
	// Sel[0..Size); truncating to n logical rows means keeping only
	// the first n entries of Sel.
	if batch.Sel != nil {
		if n < len(batch.Sel) {
			batch.Sel = batch.Sel[:n]
		}
		batch.Size = n
		return
	}
	// Plain batch: slice each column's Data arrays in place.
	for i := range batch.Cols {
		col := &batch.Cols[i]
		if col.Type == 0 {
			break
		}
		if col.Nulls != nil && n < len(col.Nulls) {
			col.Nulls = col.Nulls[:n]
		}
		switch col.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if col.Data.Ints != nil && n < len(col.Data.Ints) {
				col.Data.Ints = col.Data.Ints[:n]
			}
		case LX.T_FLOAT_KW:
			if col.Data.Floats != nil && n < len(col.Data.Floats) {
				col.Data.Floats = col.Data.Floats[:n]
			}
		case LX.T_BOOL:
			if col.Data.Bools != nil && n < len(col.Data.Bools) {
				col.Data.Bools = col.Data.Bools[:n]
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if col.Data.Strs != nil && n < len(col.Data.Strs) {
				col.Data.Strs = col.Data.Strs[:n]
			}
		}
	}
	batch.Size = n
}
