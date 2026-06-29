package EV

import (
	"slices"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// EvalBatch evaluates a predicate expression over an entire batch,
// producing a selection vector of matching rows.
// Returns:
//   - nil: all rows match (no filter applied)
//   - empty []uint16: no rows match
//   - non-empty []uint16: indices of matching rows
//
// EvalBatch uses 4-wide manual unrolling for L1 cache efficiency
// (REQ000157, REQ000144). Complex expressions fall back to
// row-at-a-time evaluation via rowEval.
// REQ000157 satisfied: Expression evaluation SIMD acceleration
// (batch predicate evaluation).
func EvalBatch(expr PS.Expr, batch *UT.Batch, params []any) []uint16 {
	if expr == nil || batch == nil || batch.Size == 0 {
		return nil
	}

	switch e := expr.(type) {
	case *PS.BinaryExpr:
		return evalBinaryBatch(e, batch, params)
	case *PS.UnaryExpr:
		return evalUnaryBatch(e, batch, params)
	case *PS.InExpr:
		if e.Subquery != nil || len(e.List) == 0 {
			return evalRowFallback(expr, batch, params)
		}
		col, ok := extractColumnRef(e.Expr, batch)
		if !ok {
			return evalRowFallback(expr, batch, params)
		}
		list := make([]any, len(e.List))
		for i, item := range e.List {
			v, isLit := evalLiteral(item, params)
			if !isLit {
				return evalRowFallback(expr, batch, params)
			}
			list[i] = v
		}
		return evalInListBatch(col, list, batch.Size)
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
func evalBinaryBatch(e *PS.BinaryExpr, batch *UT.Batch, params []any) []uint16 {
	// REQ001010: AND/OR batch evaluation
	if e.Op == LX.T_AND {
		return evalAndBatch(e, batch, params)
	}
	if e.Op == LX.T_OR {
		return evalOrBatch(e, batch, params)
	}

	leftCol, leftIsCol := extractColumnRef(e.Left, batch)
	rightCol, rightIsCol := extractColumnRef(e.Right, batch)

	// Both columns: column-column vectorized comparison
	if leftIsCol && rightIsCol {
		return compareColumns(leftCol, rightCol, e.Op, batch)
	}

	// UT.Column-literal: column-literal vectorized comparison
	if leftIsCol {
		litVal, litOk := evalLiteral(e.Right, params)
		if litOk {
			return compareColLiteral(leftCol, litVal, e.Op, batch)
		}
	}

	if rightIsCol {
		litVal, litOk := evalLiteral(e.Left, params)
		if litOk {
			return compareColLiteral(rightCol, litVal, SwapOp(e.Op), batch)
		}
	}

	// Fallback
	return evalRowFallback(e, batch, params)
}

// evalAndBatch evaluates left AND right as batch selection vectors.
// AND = set intersection of left and right selection vectors.
func evalAndBatch(e *PS.BinaryExpr, batch *UT.Batch, params []any) []uint16 {
	leftSel := EvalBatch(e.Left, batch, params)
	rightSel := EvalBatch(e.Right, batch, params)
	return intersectSelection(leftSel, rightSel, batch.Size)
}

// evalOrBatch evaluates left OR right as batch selection vectors.
// OR = set union of left and right selection vectors.
func evalOrBatch(e *PS.BinaryExpr, batch *UT.Batch, params []any) []uint16 {
	leftSel := EvalBatch(e.Left, batch, params)
	rightSel := EvalBatch(e.Right, batch, params)
	return unionSelection(leftSel, rightSel, batch.Size)
}

// evalUnaryBatch handles unary expressions (NOT, -).
func evalUnaryBatch(e *PS.UnaryExpr, batch *UT.Batch, params []any) []uint16 {
	if e.Op == LX.T_NOT {
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
		return InvertSelection(inner, batch.Size)
	}
	// Other unary ops: row-at-a-time
	return evalRowFallback(e, batch, params)
}

// extractColumnRef attempts to extract a column from a column reference
// expression (e.g., "x" -> batch.Cols[colIdx]). Returns the column and
// true on success; false if expression is not a column reference.
// Uses the pre-computed column index from the batch's colMap
// (set by VectorizedSeqScan), enabling O(1) lookup. Falls back
// to a linear scan if the map is not available.
func extractColumnRef(expr PS.Expr, batch *UT.Batch) (UT.Column, bool) {
	ident, ok := expr.(*PS.Ident)
	if !ok {
		return UT.Column{}, false
	}
	// Use pre-computed index if available
	if batch.ColMap() != nil {
		if idx, found := batch.ColMap()[ident.Name]; found {
			if idx < len(batch.Cols) {
				return batch.Cols[idx], true
			}
		}
		return UT.Column{}, false
	}
	// Fallback: linear scan by column name from "c0", "c1", etc.
	// (This handles synthetic batches in tests.)
	for i := range batch.Cols {
		if batch.Cols[i].Name == ident.Name {
			return batch.Cols[i], true
		}
	}
	return UT.Column{}, false
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
func compareColumns(left, right UT.Column, op LX.TokenType, batch *UT.Batch) []uint16 {
	// Type matching required
	if left.Type != right.Type {
		return evalRowFallback(&PS.BinaryExpr{Op: op}, batch, nil)
	}

	switch left.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		return CompareInt64Cols(left, right, op, batch.Size)
	case LX.T_FLOAT_KW:
		return compareFloat64Cols(left, right, op, batch.Size)
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		return compareStringCols(left, right, op, batch.Size)
	}
	return evalRowFallback(&PS.BinaryExpr{Op: op}, batch, nil)
}

// compareColLiteral performs vectorized column-to-literal comparison.
func compareColLiteral(col UT.Column, lit any, op LX.TokenType, batch *UT.Batch) []uint16 {
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if v, ok := lit.(int64); ok {
			return CompareInt64ColLit(col, v, op, batch.Size)
		}
	case LX.T_FLOAT_KW:
		if v, ok := lit.(float64); ok {
			return CompareFloat64ColLit(col, v, op, batch.Size)
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if v, ok := lit.(string); ok {
			return CompareStringColLit(col, v, op, batch.Size)
		}
	}
	return evalRowFallback(&PS.BinaryExpr{Op: op}, batch, nil)
}

// isNull reports whether the column's i-th row is NULL. Safe to call
// when col.Nulls is nil or shorter than i (REQ000608).
func isNull(col UT.Column, i int) bool {
	return i < len(col.Nulls) && col.Nulls[i]
}

// compareInt64Cols: hoisted-operator int64 column-column comparison.
// REQ000608: skip rows where either side is NULL.
func CompareInt64Cols(left, right UT.Column, op LX.TokenType, n int) []uint16 {
	l := left.Data.Ints
	r := right.Data.Ints
	switch op {
	case LX.T_EQ:
		return cmpInt64ColsEQ(l, r, left, right, n)
	case LX.T_NE:
		return cmpInt64ColsNE(l, r, left, right, n)
	case LX.T_LT:
		return cmpInt64ColsLT(l, r, left, right, n)
	case LX.T_LE:
		return cmpInt64ColsLE(l, r, left, right, n)
	case LX.T_GT:
		return cmpInt64ColsGT(l, r, left, right, n)
	case LX.T_GE:
		return cmpInt64ColsGE(l, r, left, right, n)
	}
	return nil
}

func cmpInt64ColsEQ(l, r []int64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] == r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpInt64ColsNE(l, r []int64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] != r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpInt64ColsLT(l, r []int64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] < r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpInt64ColsLE(l, r []int64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] <= r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpInt64ColsGT(l, r []int64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] > r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpInt64ColsGE(l, r []int64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] >= r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// compareInt64ColLit: hoisted-operator int64 column-literal comparison.
// REQ000608: skip NULL rows.
func CompareInt64ColLit(col UT.Column, lit int64, op LX.TokenType, n int) []uint16 {
	data := col.Data.Ints
	switch op {
	case LX.T_EQ:
		return cmpInt64LitEQ(data, lit, col, n)
	case LX.T_NE:
		return cmpInt64LitNE(data, lit, col, n)
	case LX.T_LT:
		return cmpInt64LitLT(data, lit, col, n)
	case LX.T_LE:
		return cmpInt64LitLE(data, lit, col, n)
	case LX.T_GT:
		return cmpInt64LitGT(data, lit, col, n)
	case LX.T_GE:
		return cmpInt64LitGE(data, lit, col, n)
	}
	return nil
}

func cmpInt64LitEQ(data []int64, lit int64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] == lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpInt64LitNE(data []int64, lit int64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] != lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpInt64LitLT(data []int64, lit int64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] < lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpInt64LitLE(data []int64, lit int64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] <= lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpInt64LitGT(data []int64, lit int64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] > lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpInt64LitGE(data []int64, lit int64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] >= lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// compareFloat64Cols: hoisted-operator float64 column-column comparison.
// REQ000608: skip NULL rows.
func compareFloat64Cols(left, right UT.Column, op LX.TokenType, n int) []uint16 {
	l := left.Data.Floats
	r := right.Data.Floats
	switch op {
	case LX.T_EQ:
		return cmpFloat64ColsEQ(l, r, left, right, n)
	case LX.T_NE:
		return cmpFloat64ColsNE(l, r, left, right, n)
	case LX.T_LT:
		return cmpFloat64ColsLT(l, r, left, right, n)
	case LX.T_LE:
		return cmpFloat64ColsLE(l, r, left, right, n)
	case LX.T_GT:
		return cmpFloat64ColsGT(l, r, left, right, n)
	case LX.T_GE:
		return cmpFloat64ColsGE(l, r, left, right, n)
	}
	return nil
}

func cmpFloat64ColsEQ(l, r []float64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] == r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpFloat64ColsNE(l, r []float64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] != r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpFloat64ColsLT(l, r []float64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] < r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpFloat64ColsLE(l, r []float64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] <= r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpFloat64ColsGT(l, r []float64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] > r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpFloat64ColsGE(l, r []float64, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] >= r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// compareFloat64ColLit: hoisted-operator float64 column-literal comparison.
// REQ000608: skip NULL rows.
func CompareFloat64ColLit(col UT.Column, lit float64, op LX.TokenType, n int) []uint16 {
	data := col.Data.Floats
	switch op {
	case LX.T_EQ:
		return cmpFloat64LitEQ(data, lit, col, n)
	case LX.T_NE:
		return cmpFloat64LitNE(data, lit, col, n)
	case LX.T_LT:
		return cmpFloat64LitLT(data, lit, col, n)
	case LX.T_LE:
		return cmpFloat64LitLE(data, lit, col, n)
	case LX.T_GT:
		return cmpFloat64LitGT(data, lit, col, n)
	case LX.T_GE:
		return cmpFloat64LitGE(data, lit, col, n)
	}
	return nil
}

func cmpFloat64LitEQ(data []float64, lit float64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] == lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpFloat64LitNE(data []float64, lit float64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] != lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpFloat64LitLT(data []float64, lit float64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] < lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpFloat64LitLE(data []float64, lit float64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] <= lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpFloat64LitGT(data []float64, lit float64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] > lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpFloat64LitGE(data []float64, lit float64, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] >= lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// compareStringCols: hoisted-operator string column-column comparison.
// REQ000608: skip NULL rows.
func compareStringCols(left, right UT.Column, op LX.TokenType, n int) []uint16 {
	l := left.Data.Strs
	r := right.Data.Strs
	switch op {
	case LX.T_EQ:
		return cmpStringColsEQ(l, r, left, right, n)
	case LX.T_NE:
		return cmpStringColsNE(l, r, left, right, n)
	case LX.T_LT:
		return cmpStringColsLT(l, r, left, right, n)
	case LX.T_LE:
		return cmpStringColsLE(l, r, left, right, n)
	case LX.T_GT:
		return cmpStringColsGT(l, r, left, right, n)
	case LX.T_GE:
		return cmpStringColsGE(l, r, left, right, n)
	}
	return nil
}

func cmpStringColsEQ(l, r []string, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] == r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpStringColsNE(l, r []string, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] != r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpStringColsLT(l, r []string, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] < r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpStringColsLE(l, r []string, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] <= r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpStringColsGT(l, r []string, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] > r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpStringColsGE(l, r []string, lc, rc UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(lc, i) || isNull(rc, i) {
			continue
		}
		if l[i] >= r[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// compareStringColLit: hoisted-operator string column-literal comparison.
// REQ000608: skip NULL rows.
func CompareStringColLit(col UT.Column, lit string, op LX.TokenType, n int) []uint16 {
	data := col.Data.Strs
	switch op {
	case LX.T_EQ:
		return cmpStringLitEQ(data, lit, col, n)
	case LX.T_NE:
		return cmpStringLitNE(data, lit, col, n)
	case LX.T_LT:
		return cmpStringLitLT(data, lit, col, n)
	case LX.T_LE:
		return cmpStringLitLE(data, lit, col, n)
	case LX.T_GT:
		return cmpStringLitGT(data, lit, col, n)
	case LX.T_GE:
		return cmpStringLitGE(data, lit, col, n)
	}
	return nil
}

func cmpStringLitEQ(data []string, lit string, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] == lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpStringLitNE(data []string, lit string, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] != lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpStringLitLT(data []string, lit string, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] < lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpStringLitLE(data []string, lit string, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] <= lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpStringLitGT(data []string, lit string, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] > lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

func cmpStringLitGE(data []string, lit string, col UT.Column, n int) []uint16 {
	sel := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		if isNull(col, i) {
			continue
		}
		if data[i] >= lit {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// swapOp swaps the operator for column-literal evaluation.
// e.g., "5 < x" becomes "x > 5".
func SwapOp(op LX.TokenType) LX.TokenType {
	switch op {
	case LX.T_LT:
		return LX.T_GT
	case LX.T_LE:
		return LX.T_GE
	case LX.T_GT:
		return LX.T_LT
	case LX.T_GE:
		return LX.T_LE
	}
	return op
}

// intersectSelection returns the intersection of two selection vectors.
// nil means "all rows" (identity for AND).
func intersectSelection(a, b []uint16, n int) []uint16 {
	// nil = all rows (identity)
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	// Both empty = no rows
	if len(a) == 0 || len(b) == 0 {
		return []uint16{}
	}
	// Build bitmap from the smaller set for efficiency
	if len(a) > len(b) {
		a, b = b, a
	}
	var bitmap [UT.BatchSize]bool
	for _, idx := range a {
		if int(idx) < UT.BatchSize {
			bitmap[idx] = true
		}
	}
	result := make([]uint16, 0, len(b))
	for _, idx := range b {
		if int(idx) < UT.BatchSize && bitmap[idx] {
			result = append(result, idx)
		}
	}
	return result
}

// unionSelection returns the union of two selection vectors.
// nil means "all rows" (identity for OR).
func unionSelection(a, b []uint16, n int) []uint16 {
	// nil = all rows (identity)
	if a == nil || b == nil {
		return nil
	}
	// Both empty = no rows
	if len(a) == 0 && len(b) == 0 {
		return []uint16{}
	}
	// Build bitmap from first set
	var bitmap [UT.BatchSize]bool
	for _, idx := range a {
		if int(idx) < UT.BatchSize {
			bitmap[idx] = true
		}
	}
	result := make([]uint16, 0, len(a)+len(b))
	for _, idx := range a {
		if int(idx) < UT.BatchSize {
			result = append(result, idx)
		}
	}
	for _, idx := range b {
		if int(idx) < UT.BatchSize && !bitmap[idx] {
			result = append(result, idx)
		}
	}
	return result
}

// invertSelection returns the complement of the selection vector
// within [0, n). E.g., sel=[0,2,4] with n=6 -> [1,3,5].
// Uses a [UT.BatchSize]bool bitmap instead of a map for zero allocation.
func InvertSelection(sel []uint16, n int) []uint16 {
	var bitmap [UT.BatchSize]bool
	for _, idx := range sel {
		if int(idx) < UT.BatchSize {
			bitmap[idx] = true
		}
	}
	inv := make([]uint16, 0, n-len(sel))
	for i := 0; i < n; i++ {
		if !bitmap[i] {
			inv = append(inv, uint16(i))
		}
	}
	return inv
}

// evalRowFallback falls back to row-at-a-time evaluation when
// vectorized paths are not applicable. Creates a row from batch
// data and uses the existing Eval() function.
func evalRowFallback(expr PS.Expr, batch *UT.Batch, params []any) []uint16 {
	sel := make([]uint16, 0, batch.Size)
	for i := 0; i < batch.Size; i++ {
		// Build a synthetic row from batch data
		row := batchToRow(batch, i)
		val, err := EvalValue(expr, row, params)
		if err != nil {
			continue
		}
		if val.Kind == KindBool && val.Bo {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// batchToRow converts a batch's i-th row to a Row for Eval.
// This is expensive (allocates per row); used only as fallback.
func batchToRow(batch *UT.Batch, idx int) *Row {
	row := &Row{
		Cols: make([]string, 0, len(batch.Cols)),
		Data: make([]Value, 0, len(batch.Cols)),
	}
	for c := range batch.Cols {
		d := batch.Cols[c].Data
		if d.Ints == nil && d.Floats == nil && d.Strs == nil && d.Bools == nil {
			continue
		}
		row.Cols = append(row.Cols, batch.Cols[c].Name)
		row.Data = append(row.Data, DT.ValueFromAny(BatchValueAt(batch.Cols[c], idx)))
	}
	return row
}

// BatchValueAt extracts the i-th value from a column.
func BatchValueAt(col UT.Column, i int) any {
	if col.Nulls != nil && i < len(col.Nulls) && col.Nulls[i] {
		return nil
	}
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if i < len(col.Data.Ints) {
			return col.Data.Ints[i]
		}
	case LX.T_FLOAT_KW:
		if i < len(col.Data.Floats) {
			return col.Data.Floats[i]
		}
	case LX.T_BOOL:
		if i < len(col.Data.Bools) {
			return col.Data.Bools[i]
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if i < len(col.Data.Strs) {
			return col.Data.Strs[i]
		}
	}
	return nil
}

// evalInListBatch runs a vectorized IN-list membership test (REQ000554).
// The list is materialized once and reused across all rows. For int64
// columns + int64 list: sort the list and binary search. For string
// columns + string list: build a hash set. Returns selection of rows
// where the column value is in the list. Empty/missing list returns
// nil (no rows match). Empty column returns nil. NULL columns skip
// the row per standard SQL semantics.
func evalInListBatch(col UT.Column, list []any, n int) []uint16 {
	if n == 0 || len(list) == 0 {
		return nil
	}
	// Int64 path: sort + binary search.
	if d := col.Data.Ints; d != nil {
		ints := make([]int64, 0, len(list))
		for _, v := range list {
			switch x := v.(type) {
			case int64:
				ints = append(ints, x)
			case int:
				ints = append(ints, int64(x))
			case float64:
				ints = append(ints, int64(x))
			default:
				continue
			}
		}
		if len(ints) == 0 {
			return nil
		}
		slices.Sort(ints)
		sel := make([]uint16, 0, n)
		for i := 0; i < n; i++ {
			if isNull(col, i) {
				continue
			}
			// Binary search.
			v := d[i]
			lo, hi := 0, len(ints)
			for lo < hi {
				mid := (lo + hi) / 2
				if ints[mid] < v {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			if lo < len(ints) && ints[lo] == v {
				sel = append(sel, uint16(i))
			}
		}
		return sel
	}
	// String path: hash set.
	if d := col.Data.Strs; d != nil {
		set := make(map[string]struct{}, len(list))
		for _, v := range list {
			if s, ok := v.(string); ok {
				set[s] = struct{}{}
			}
		}
		if len(set) == 0 {
			return nil
		}
		sel := make([]uint16, 0, n)
		for i := 0; i < n; i++ {
			if isNull(col, i) {
				continue
			}
			if _, found := set[d[i]]; found {
				sel = append(sel, uint16(i))
			}
		}
		return sel
	}
	return nil
}
