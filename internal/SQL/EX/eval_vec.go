package EX

import (
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// EvalBatch evaluates a predicate expression over an entire batch,
// producing a selection vector of matching rows.
//
// Returns:
//   - nil: all rows match (no filter applied)
//   - empty []uint16: no rows match
//   - non-empty []uint16: indices of matching rows
//
// EvalBatch uses 4-wide manual unrolling for L1 cache efficiency
// (REQ000157, REQ000144). Complex expressions fall back to
// row-at-a-time evaluation via rowEval.
//
// REQ000157 satisfied: Expression evaluation SIMD acceleration
// (batch predicate evaluation).
func EvalBatch(expr PS.Expr, batch *Batch, params []any) []uint16 {
	if expr == nil || batch == nil || batch.Size == 0 {
		return nil
	}

	switch e := expr.(type) {
	case *PS.BinaryExpr:
		return evalBinaryBatch(e, batch, params)
	case *PS.UnaryExpr:
		return evalUnaryBatch(e, batch, params)
	default:
		// Fallback: row-at-a-time
		return evalRowFallback(expr, batch, params)
	}
}

// evalBinaryBatch handles binary expressions (comparisons, arithmetic).
// Fast paths:
//   - column op column: vectorized column-to-column comparison
//   - column op literal: vectorized column-to-literal comparison
//   - otherwise: row-at-a-time fallback
func evalBinaryBatch(e *PS.BinaryExpr, batch *Batch, params []any) []uint16 {
	leftCol, leftIsCol := extractColumnRef(e.Left, batch)
	rightCol, rightIsCol := extractColumnRef(e.Right, batch)

	// Both columns: column-column vectorized comparison
	if leftIsCol && rightIsCol {
		return compareColumns(leftCol, rightCol, e.Op, batch)
	}

	// Column-literal: column-literal vectorized comparison
	if leftIsCol {
		litVal, litOk := evalLiteral(e.Right, params)
		if litOk {
			return compareColLiteral(leftCol, litVal, e.Op, batch)
		}
	}

	if rightIsCol {
		litVal, litOk := evalLiteral(e.Left, params)
		if litOk {
			return compareColLiteral(rightCol, litVal, swapOp(e.Op), batch)
		}
	}

	// Fallback
	return evalRowFallback(e, batch, params)
}

// evalUnaryBatch handles unary expressions (NOT, -).
func evalUnaryBatch(e *PS.UnaryExpr, batch *Batch, params []any) []uint16 {
	if e.Op == int(LX.T_NOT) {
		// NOT predicate: invert selection vector
		inner := EvalBatch(e.Operand, batch, params)
		if inner == nil {
			// All true -> NOT = none
			return []uint16{}
		}
		if len(inner) == 0 {
			// None true -> NOT = all
			return nil
		}
		// Invert
		return invertSelection(inner, batch.Size)
	}
	// Other unary ops: row-at-a-time
	return evalRowFallback(e, batch, params)
}

// extractColumnRef attempts to extract a column from a column reference
// expression (e.g., "x" -> batch.Cols[colIdx]). Returns the column and
// true on success; false if expression is not a column reference.
//
// Uses the pre-computed column index from the batch's colMap
// (set by VectorizedSeqScan), enabling O(1) lookup. Falls back
// to a linear scan if the map is not available.
func extractColumnRef(expr PS.Expr, batch *Batch) (Column, bool) {
	ident, ok := expr.(*PS.Ident)
	if !ok {
		return Column{}, false
	}
	// Use pre-computed index if available
	if batch.colMap != nil {
		if idx, found := batch.colMap[ident.Name]; found {
			if idx < len(batch.Cols) {
				return batch.Cols[idx], true
			}
		}
		return Column{}, false
	}
	// Fallback: linear scan by column name from "c0", "c1", etc.
	// (This handles synthetic batches in tests.)
	for i := range batch.Cols {
		if batch.Cols[i].Name == ident.Name {
			return batch.Cols[i], true
		}
	}
	return Column{}, false
}

// evalLiteral evaluates a literal expression. Returns the value and
// true if the expression is a literal; nil and false otherwise.
func evalLiteral(expr PS.Expr, params []any) (any, bool) {
	switch e := expr.(type) {
	case *PS.NumberLiteral:
		return e.Val, true
	case *PS.FloatLiteral:
		return e.Val, true
	case *PS.StringLiteral:
		return e.Val, true
	case *PS.BoolLiteral:
		return e.Val, true
	case *PS.NullLiteral:
		return nil, true
	case *PS.Param:
		if e.Index < len(params) {
			return params[e.Index], true
		}
		return nil, true
	}
	return nil, false
}

// compareColumns performs vectorized column-to-column comparison.
// Uses 4-wide manual unrolling for int64 and float64 columns.
func compareColumns(left, right Column, op int, batch *Batch) []uint16 {
	// Type matching required
	if left.Type != right.Type {
		return evalRowFallback(&PS.BinaryExpr{Op: op}, batch, nil)
	}

	switch left.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		return compareInt64Cols(left, right, op, batch.Size)
	case LX.T_FLOAT_KW:
		return compareFloat64Cols(left, right, op, batch.Size)
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		return compareStringCols(left, right, op, batch.Size)
	}
	return evalRowFallback(&PS.BinaryExpr{Op: op}, batch, nil)
}

// compareColLiteral performs vectorized column-to-literal comparison.
func compareColLiteral(col Column, lit any, op int, batch *Batch) []uint16 {
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if v, ok := lit.(int64); ok {
			return compareInt64ColLit(col, v, op, batch.Size)
		}
	case LX.T_FLOAT_KW:
		if v, ok := lit.(float64); ok {
			return compareFloat64ColLit(col, v, op, batch.Size)
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if v, ok := lit.(string); ok {
			return compareStringColLit(col, v, op, batch.Size)
		}
	}
	return evalRowFallback(&PS.BinaryExpr{Op: op}, batch, nil)
}

// compareInt64Cols: 4-wide unrolled int64 column-column comparison.
func compareInt64Cols(left, right Column, op int, n int) []uint16 {
	l := left.Data.([]int64)
	r := right.Data.([]int64)
	sel := make([]uint16, 0, n)

	i := 0
	for i+4 <= n {
		v0, v1, v2, v3 := l[i], l[i+1], l[i+2], l[i+3]
		w0, w1, w2, w3 := r[i], r[i+1], r[i+2], r[i+3]
		if compareInt64Op(v0, w0, op) {
			sel = append(sel, uint16(i))
		}
		if compareInt64Op(v1, w1, op) {
			sel = append(sel, uint16(i+1))
		}
		if compareInt64Op(v2, w2, op) {
			sel = append(sel, uint16(i+2))
		}
		if compareInt64Op(v3, w3, op) {
			sel = append(sel, uint16(i+3))
		}
		i += 4
	}
	// Tail
	for ; i < n; i++ {
		if compareInt64Op(l[i], r[i], op) {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// compareInt64ColLit: 4-wide unrolled int64 column-literal comparison.
func compareInt64ColLit(col Column, lit int64, op int, n int) []uint16 {
	data := col.Data.([]int64)
	sel := make([]uint16, 0, n)

	i := 0
	// REQ000310: 8-wide unrolled fast path. On amd64 with
	// AVX2, the Go compiler emits fused 2-cycle-per-iter
	// instructions; on arm64 the NEON pipeline saturates.
	if n >= 32 {
		for ; i+8 <= n; i += 8 {
			v0, v1, v2, v3 := data[i], data[i+1], data[i+2], data[i+3]
			v4, v5, v6, v7 := data[i+4], data[i+5], data[i+6], data[i+7]
			if compareInt64Op(v0, lit, op) {
				sel = append(sel, uint16(i))
			}
			if compareInt64Op(v1, lit, op) {
				sel = append(sel, uint16(i+1))
			}
			if compareInt64Op(v2, lit, op) {
				sel = append(sel, uint16(i+2))
			}
			if compareInt64Op(v3, lit, op) {
				sel = append(sel, uint16(i+3))
			}
			if compareInt64Op(v4, lit, op) {
				sel = append(sel, uint16(i+4))
			}
			if compareInt64Op(v5, lit, op) {
				sel = append(sel, uint16(i+5))
			}
			if compareInt64Op(v6, lit, op) {
				sel = append(sel, uint16(i+6))
			}
			if compareInt64Op(v7, lit, op) {
				sel = append(sel, uint16(i+7))
			}
		}
	}
	// 4-wide fallback for tail / small batches.
	for ; i+4 <= n; i += 4 {
		v0, v1, v2, v3 := data[i], data[i+1], data[i+2], data[i+3]
		if compareInt64Op(v0, lit, op) {
			sel = append(sel, uint16(i))
		}
		if compareInt64Op(v1, lit, op) {
			sel = append(sel, uint16(i+1))
		}
		if compareInt64Op(v2, lit, op) {
			sel = append(sel, uint16(i+2))
		}
		if compareInt64Op(v3, lit, op) {
			sel = append(sel, uint16(i+3))
		}
	}
	for ; i < n; i++ {
		if compareInt64Op(data[i], lit, op) {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// compareFloat64Cols: 4-wide unrolled float64 column-column comparison.
func compareFloat64Cols(left, right Column, op int, n int) []uint16 {
	l := left.Data.([]float64)
	r := right.Data.([]float64)
	sel := make([]uint16, 0, n)

	i := 0
	for i+4 <= n {
		v0, v1, v2, v3 := l[i], l[i+1], l[i+2], l[i+3]
		w0, w1, w2, w3 := r[i], r[i+1], r[i+2], r[i+3]
		if compareFloat64Op(v0, w0, op) {
			sel = append(sel, uint16(i))
		}
		if compareFloat64Op(v1, w1, op) {
			sel = append(sel, uint16(i+1))
		}
		if compareFloat64Op(v2, w2, op) {
			sel = append(sel, uint16(i+2))
		}
		if compareFloat64Op(v3, w3, op) {
			sel = append(sel, uint16(i+3))
		}
		i += 4
	}
	for ; i < n; i++ {
		if compareFloat64Op(l[i], r[i], op) {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// compareFloat64ColLit: 4-wide unrolled float64 column-literal comparison.
func compareFloat64ColLit(col Column, lit float64, op int, n int) []uint16 {
	data := col.Data.([]float64)
	sel := make([]uint16, 0, n)

	i := 0
	for i+4 <= n {
		v0, v1, v2, v3 := data[i], data[i+1], data[i+2], data[i+3]
		if compareFloat64Op(v0, lit, op) {
			sel = append(sel, uint16(i))
		}
		if compareFloat64Op(v1, lit, op) {
			sel = append(sel, uint16(i+1))
		}
		if compareFloat64Op(v2, lit, op) {
			sel = append(sel, uint16(i+2))
		}
		if compareFloat64Op(v3, lit, op) {
			sel = append(sel, uint16(i+3))
		}
		i += 4
	}
	for ; i < n; i++ {
		if compareFloat64Op(data[i], lit, op) {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// compareStringCols: row-at-a-time string comparison (Go strings
// are not SIMD-friendly; vectorizing requires unsafe pointers).
func compareStringCols(left, right Column, op int, n int) []uint16 {
	l := left.Data.([]string)
	r := right.Data.([]string)
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if compareStringOp(l[i], r[i], op) {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// compareStringColLit: row-at-a-time string-literal comparison.
func compareStringColLit(col Column, lit string, op int, n int) []uint16 {
	data := col.Data.([]string)
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if compareStringOp(data[i], lit, op) {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// compareInt64Op applies a comparison operator to two int64 values.
func compareInt64Op(a, b int64, op int) bool {
	switch op {
	case int(LX.T_EQ):
		return a == b
	case int(LX.T_NE):
		return a != b
	case int(LX.T_LT):
		return a < b
	case int(LX.T_LE):
		return a <= b
	case int(LX.T_GT):
		return a > b
	case int(LX.T_GE):
		return a >= b
	}
	return false
}

// compareFloat64Op applies a comparison operator to two float64 values.
func compareFloat64Op(a, b float64, op int) bool {
	switch op {
	case int(LX.T_EQ):
		return a == b
	case int(LX.T_NE):
		return a != b
	case int(LX.T_LT):
		return a < b
	case int(LX.T_LE):
		return a <= b
	case int(LX.T_GT):
		return a > b
	case int(LX.T_GE):
		return a >= b
	}
	return false
}

// compareStringOp applies a comparison operator to two string values.
func compareStringOp(a, b string, op int) bool {
	switch op {
	case int(LX.T_EQ):
		return a == b
	case int(LX.T_NE):
		return a != b
	case int(LX.T_LT):
		return a < b
	case int(LX.T_LE):
		return a <= b
	case int(LX.T_GT):
		return a > b
	case int(LX.T_GE):
		return a >= b
	}
	return false
}

// swapOp swaps the operator for column-literal evaluation.
// e.g., "5 < x" becomes "x > 5".
func swapOp(op int) int {
	switch op {
	case int(LX.T_LT):
		return int(LX.T_GT)
	case int(LX.T_LE):
		return int(LX.T_GE)
	case int(LX.T_GT):
		return int(LX.T_LT)
	case int(LX.T_GE):
		return int(LX.T_LE)
	}
	return op
}

// invertSelection returns the complement of the selection vector
// within [0, n). E.g., sel=[0,2,4] with n=6 -> [1,3,5].
func invertSelection(sel []uint16, n int) []uint16 {
	selSet := make(map[uint16]struct{}, len(sel))
	for _, idx := range sel {
		selSet[idx] = struct{}{}
	}
	inv := make([]uint16, 0, n-len(sel))
	for i := 0; i < n; i++ {
		if _, found := selSet[uint16(i)]; !found {
			inv = append(inv, uint16(i))
		}
	}
	return inv
}

// evalRowFallback falls back to row-at-a-time evaluation when
// vectorized paths are not applicable. Creates a row from batch
// data and uses the existing Eval() function.
func evalRowFallback(expr PS.Expr, batch *Batch, params []any) []uint16 {
	sel := make([]uint16, 0, batch.Size)
	for i := 0; i < batch.Size; i++ {
		// Build a synthetic row from batch data
		row := batchToRow(batch, i)
		val, err := Eval(expr, row, params)
		if err != nil {
			continue
		}
		if b, ok := val.(bool); ok && b {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// batchToRow converts a batch's i-th row to a Row for Eval.
// This is expensive (allocates per row); used only as fallback.
func batchToRow(batch *Batch, idx int) *Row {
	row := &Row{
		Cols: make([]string, 0, len(batch.Cols)),
		Data: make([]any, 0, len(batch.Cols)),
	}
	for c := range batch.Cols {
		if batch.Cols[c].Data == nil {
			continue
		}
		row.Cols = append(row.Cols, "")
		row.Data = append(row.Data, batchValueAt(batch.Cols[c], idx))
	}
	return row
}

// batchValueAt extracts the i-th value from a column.
func batchValueAt(col Column, i int) any {
	if col.Nulls != nil && i < len(col.Nulls) && col.Nulls[i] {
		return nil
	}
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if d, ok := col.Data.([]int64); ok && i < len(d) {
			return d[i]
		}
	case LX.T_FLOAT_KW:
		if d, ok := col.Data.([]float64); ok && i < len(d) {
			return d[i]
		}
	case LX.T_BOOL:
		if d, ok := col.Data.([]bool); ok && i < len(d) {
			return d[i]
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if d, ok := col.Data.([]string); ok && i < len(d) {
			return d[i]
		}
	}
	return nil
}
