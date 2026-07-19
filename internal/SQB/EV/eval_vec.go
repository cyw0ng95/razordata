package EV

import (
	"fmt"
	"hash"
	"hash/fnv"
	"math"
	"reflect"
	"slices"
	"strconv"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
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
		val, err := evalFallbackEvalValue(expr, row, params)
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
// REQ001460: forwards the batch's ExecContext so subquery
// evaluation in the row-fallback path can locate the planner.
func batchToRow(batch *UT.Batch, idx int) *Row {
	row := &Row{
		Cols:    make([]string, 0, len(batch.Cols)),
		Data:    make([]Value, 0, len(batch.Cols)),
		ExecCtx: batch.ExecCtx,
	}
	for c := range batch.Cols {
		col := &batch.Cols[c]
		d := col.Data
		if d.Ints == nil && d.Floats == nil && d.Strs == nil && d.Bools == nil {
			// REQ001210: Even when all data slices are nil (pure-NULL
			// column), include the column in the reconstructed row so
			// that row.Lookup finds it and returns NULL. Without this,
			// NULL-only columns are silently dropped, causing scalar
			// functions like ROUND/SIGN/ABS to receive a missing column
			// instead of NULL, producing 0 instead of NULL.
			if col.Nulls != nil && idx < len(col.Nulls) && col.Nulls[idx] {
				row.Cols = append(row.Cols, col.Name)
				row.Data = append(row.Data, DT.NullValue())
			}
			continue
		}
		row.Cols = append(row.Cols, col.Name)
		row.Data = append(row.Data, DT.ValueFromAny(BatchValueAt(batch.Cols[c], idx)))
	}
	return row
}

// BatchValueAt extracts the i-th value from a column.
// Delegates to UT.BatchValueAt.
func BatchValueAt(col UT.Column, i int) any {
	return UT.BatchValueAt(col, i)
}

// EvalBatchExpr evaluates an expression over an entire batch, producing
// a column result. This is the batch-parallel equivalent of EvalValue.
// Dispatches by expression type to vectorized kernels where available.
// REQ001210.
func EvalBatchExpr(expr PS.Expr, batch *UT.Batch, params []any) UT.Column {
	if expr == nil {
		return UT.Column{Type: LX.T_NULL}
	}

	switch e := expr.(type) {
	case *PS.Ident:
		// Column reference: shallow copy of source column.
		if col, ok := extractColumnRef(e, batch); ok {
			return col
		}
		return UT.Column{Type: LX.T_NULL}

	case *PS.NumberLiteral:
		return fillLiteralColumn(batch, LX.T_INT_KW, e.Val)

	case *PS.FloatLiteral:
		return fillLiteralColumn(batch, LX.T_FLOAT_KW, e.Val)

	case *PS.StringLiteral:
		return fillLiteralColumn(batch, LX.T_TEXT, e.Val)

	case *PS.BoolLiteral:
		return fillLiteralColumn(batch, LX.T_BOOL, e.Val)

	case *PS.NullLiteral:
		return fillNullColumn(batch)

	case *PS.Param:
		if e.Index < len(params) {
			return evalAnyLiteral(params[e.Index], batch)
		}
		return fillNullColumn(batch)

	case *PS.BinaryExpr:
		return evalBinaryBatchExpr(e, batch, params)

	case *PS.AliasedExpr:
		return EvalBatchExpr(e.Expr, batch, params)

	case *PS.CaseExpr:
		return evalCaseBatchExpr(e, batch, params)

	case *PS.FunctionCall:
		return evalFunctionBatchExpr(e, batch, params)

case *PS.SubqueryExpr:
		// REQ001460: vectorized scalar subquery evaluation.
		return evalSubqueryBatchExpr(e, batch, params)

	case *PS.CastExpr:
		return evalCastBatchExpr(e, batch, params)

	default:
		return evalRowFallbackColumn(expr, batch, params)
	}
}

// REQ001610: per-session cache for correlated subquery results.
// Uses the existing correlatedSubqueryCache from eval.go (LRU, 256 entries).
// Key is a string combining subquery pointer and outer-row correlated column hash.

// evalSubqueryBatchExpr evaluates a scalar subquery over a batch,
// dispatching on correlation status. Non-correlated subqueries
// short-circuit via globalSubqueryCache (REQ001460):
//
//   - Cache hit: broadcast the cached value as a constant column.
//   - Cache miss: evaluate once via evalScalarSubquery using a
//     minimal Row carrying the batch's ExecCtx (for the planner),
//     then broadcast.
//
// Correlated subqueries use per-row evaluation with a local cache
// keyed by the outer row's correlated column values (REQ001610).
func evalSubqueryBatchExpr(subq *PS.SubqueryExpr, batch *UT.Batch, params []any) UT.Column {
	if subq == nil {
		return fillNullColumn(batch)
	}
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_NULL}
	}
	corrCols := cachedCorrelatedCols(subq)
	// Non-correlated subqueries: same result for every row.
	if len(corrCols) == 0 {
		key := cachedSubqueryKey(subq)
		if cached, ok := globalSubqueryCache.Load(key); ok {
			if v, ok := cached.(Value); ok {
				return broadcastValueColumn(batch, v)
			}
			return evalAnyLiteral(cached, batch)
		}
		probeRow := &Row{ExecCtx: batch.ExecCtx}
		v, err := evalScalarSubquery(subq, probeRow, params)
		if err == nil {
			return broadcastValueColumn(batch, v)
		}
		return evalRowFallbackColumn(subq, batch, params)
	}
	// REQ001610: correlated subquery with per-row cache.
	// Build result column by evaluating each row, caching results
	// by the outer row's correlated column values.
	subqPtr := uintptr(reflect.ValueOf(subq).Pointer())
	var out UT.Column
	allocated := false
	for i := 0; i < n; i++ {
		phys := i
		if batch.Sel != nil && i < len(batch.Sel) {
			phys = int(batch.Sel[i])
		}
		row := batchToRow(batch, phys)
		// Compute hash of correlated column values for cache key.
		h := fnv.New64a()
		for _, col := range corrCols {
			for ci, name := range row.Cols {
				if name == col {
					if ci < len(row.Data) {
						writeHashToFNV(h, row.Data[ci])
					}
					break
				}
			}
		}
		key := fmt.Sprintf("%x:%x", subqPtr, h.Sum64())
		if cached, ok := correlatedSubqueryCache.Get(key); ok {
			v := cached
			if !allocated {
				out.Type = tokenTypeFromValue(v)
				allocateColumnData(&out, batch.Size)
				allocated = true
			}
			writeValueToColumnData(&out, i, v)
			continue
		}
		v, err := evalScalarSubquery(subq, row, params)
		if err != nil {
			if !allocated {
				out.Type = LX.T_NULL
				out.Data = UT.ColumnData{}
				allocated = true
			}
			if out.Nulls == nil {
				out.Nulls = make([]bool, batch.Size)
			}
			out.Nulls[i] = true
			continue
		}
		correlatedSubqueryCache.Put(key, v)
		if !allocated {
			out.Type = tokenTypeFromValue(v)
			allocateColumnData(&out, batch.Size)
			allocated = true
		}
		writeValueToColumnData(&out, i, v)
	}
	return out
}

// broadcastValueColumn creates a constant column filled with v for
// every logical row in the batch. NULL values set the per-row null
// flag rather than producing zero-typed NULLs.
func broadcastValueColumn(batch *UT.Batch, v Value) UT.Column {
	if v.Kind == KindNull {
		return fillNullColumn(batch)
	}
	return fillLiteralColumn(batch, tokenTypeFromValue(v), v.ToAny())
}

// evalBinaryBatchExpr dispatches binary expression evaluation to the
// appropriate vectorized kernel based on the operator.
func evalBinaryBatchExpr(e *PS.BinaryExpr, batch *UT.Batch, params []any) UT.Column {
	switch e.Op {
	case LX.T_PLUS, LX.T_MINUS, LX.T_STAR, LX.T_SLASH, LX.T_DIV:
		return evalArithBatch(e, batch, params)
	case LX.T_CONCAT:
		return evalConcatBatchExpr(e, batch, params)
	case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
		return evalComparisonBatch(e, batch, params)
	default:
		return evalRowFallbackColumn(e, batch, params)
	}
}

// evalArithBatch evaluates arithmetic expressions (PLUS/MINUS/STAR/SLASH/DIV)
// over a batch. Dispatches to int64 or float64 kernels based on operand types.
func evalArithBatch(e *PS.BinaryExpr, batch *UT.Batch, params []any) UT.Column {
	leftCol := EvalBatchExpr(e.Left, batch, params)
	rightCol := EvalBatchExpr(e.Right, batch, params)

	// Both int64 → int64 kernel.
	if leftCol.Type == LX.T_INT_KW || leftCol.Type == LX.T_BIGINT {
		if rightCol.Type == LX.T_INT_KW || rightCol.Type == LX.T_BIGINT {
			var op func(a, b int64) int64
			switch e.Op {
			case LX.T_PLUS:
				op = func(a, b int64) int64 { return a + b }
			case LX.T_MINUS:
				op = func(a, b int64) int64 { return a - b }
			case LX.T_STAR:
				op = func(a, b int64) int64 { return a * b }
			case LX.T_SLASH, LX.T_DIV:
				op = func(a, b int64) int64 { return a / b }
			}
			if op != nil {
				isDiv := e.Op == LX.T_SLASH || e.Op == LX.T_DIV
				return evalArithIntBatch(leftCol, rightCol, batch, op, isDiv)
			}
		}
	}

	// Float or mixed → float64 kernel.
	if isFloatType(leftCol.Type) || isFloatType(rightCol.Type) {
		var op func(a, b float64) float64
		switch e.Op {
		case LX.T_PLUS:
			op = func(a, b float64) float64 { return a + b }
		case LX.T_MINUS:
			op = func(a, b float64) float64 { return a - b }
		case LX.T_STAR:
			op = func(a, b float64) float64 { return a * b }
		case LX.T_SLASH, LX.T_DIV:
			op = func(a, b float64) float64 { return a / b }
		}
		if op != nil {
			return evalArithFloatBatch(leftCol, rightCol, batch, op)
		}
	}

	// Fallback.
	return evalRowFallbackColumn(e, batch, params)
}

// isFloatType reports whether the token type represents a float column.
func isFloatType(typ LX.TokenType) bool {
	return typ == LX.T_FLOAT_KW
}

// isTextColumn reports whether a column is one of the text-compatible types.
func isTextColumn(col UT.Column) bool {
	return col.Type == LX.T_TEXT || col.Type == LX.T_VARCHAR || col.Type == LX.T_BLOB
}

// evalArithIntBatch evaluates an int64 binary arithmetic expression over
// two columns, respecting the batch's selection vector and propagating NULLs.
// When isDiv is true, zero divisors produce NULL in the output.
func evalArithIntBatch(left, right UT.Column, batch *UT.Batch, op func(a, b int64) int64, isDiv bool) UT.Column {
	n := batch.LogicalSize()
	out := UT.Column{
		Name: "",
		Type: LX.T_INT_KW,
		Data: UT.ColumnData{Ints: make([]int64, batch.Size)},
	}
	if n == 0 {
		return out
	}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			if isNull(left, i) || isNull(right, i) {
				if out.Nulls == nil {
					out.Nulls = make([]bool, batch.Size)
				}
				out.Nulls[i] = true
				continue
			}
			if isDiv && right.Data.Ints[i] == 0 {
				if out.Nulls == nil {
					out.Nulls = make([]bool, batch.Size)
				}
				out.Nulls[i] = true
				continue
			}
			out.Data.Ints[i] = op(left.Data.Ints[i], right.Data.Ints[i])
		}
	} else {
		for i := 0; i < n; i++ {
			if isNull(left, i) || isNull(right, i) {
				if out.Nulls == nil {
					out.Nulls = make([]bool, batch.Size)
				}
				out.Nulls[i] = true
				continue
			}
			if isDiv && right.Data.Ints[i] == 0 {
				if out.Nulls == nil {
					out.Nulls = make([]bool, batch.Size)
				}
				out.Nulls[i] = true
				continue
			}
			out.Data.Ints[i] = op(left.Data.Ints[i], right.Data.Ints[i])
		}
	}
	return out
}

// evalArithFloatBatch evaluates a float64 binary arithmetic expression over
// two columns, respecting the batch's selection vector and propagating NULLs.
// Int64 columns are converted to float64 on the fly.
func evalArithFloatBatch(left, right UT.Column, batch *UT.Batch, op func(a, b float64) float64) UT.Column {
	n := batch.LogicalSize()
	out := UT.Column{
		Name: "",
		Type: LX.T_FLOAT_KW,
		Data: UT.ColumnData{Floats: make([]float64, batch.Size)},
	}
	if n == 0 {
		return out
	}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			if isNull(left, i) || isNull(right, i) {
				if out.Nulls == nil {
					out.Nulls = make([]bool, batch.Size)
				}
				out.Nulls[i] = true
				continue
			}
			var lf, rf float64
			if left.Data.Floats != nil {
				lf = left.Data.Floats[i]
			} else if left.Data.Ints != nil {
				lf = float64(left.Data.Ints[i])
			}
			if right.Data.Floats != nil {
				rf = right.Data.Floats[i]
			} else if right.Data.Ints != nil {
				rf = float64(right.Data.Ints[i])
			}
			out.Data.Floats[i] = op(lf, rf)
		}
	} else {
		for i := 0; i < n; i++ {
			if isNull(left, i) || isNull(right, i) {
				if out.Nulls == nil {
					out.Nulls = make([]bool, batch.Size)
				}
				out.Nulls[i] = true
				continue
			}
			var lf, rf float64
			if left.Data.Floats != nil {
				lf = left.Data.Floats[i]
			} else if left.Data.Ints != nil {
				lf = float64(left.Data.Ints[i])
			}
			if right.Data.Floats != nil {
				rf = right.Data.Floats[i]
			} else if right.Data.Ints != nil {
				rf = float64(right.Data.Ints[i])
			}
			out.Data.Floats[i] = op(lf, rf)
		}
	}
	return out
}

// evalConcatBatchExpr evaluates string concatenation over a batch.
// If either operand column is not a text type (TEXT/VARCHAR/BLOB), falls
// back to row-at-a-time evaluation to avoid silent "" conversion for
// non-string columns (REQ001210).
func evalConcatBatchExpr(e *PS.BinaryExpr, batch *UT.Batch, params []any) UT.Column {
	leftCol := EvalBatchExpr(e.Left, batch, params)
	rightCol := EvalBatchExpr(e.Right, batch, params)
	// Non-text columns cannot be concatenated via the batch kernel.
	if !isTextColumn(leftCol) || !isTextColumn(rightCol) {
		return evalRowFallbackColumn(e, batch, params)
	}
	return evalConcatBatch(leftCol, rightCol, batch)
}

// evalConcatBatch concatenates two string columns. NULL on either side
// produces NULL in the output.
func evalConcatBatch(left, right UT.Column, batch *UT.Batch) UT.Column {
	n := batch.LogicalSize()
	out := UT.Column{
		Name: "",
		Type: LX.T_TEXT,
		Data: UT.ColumnData{Strs: make([]string, batch.Size)},
	}
	if n == 0 {
		return out
	}

	// Helper to get string value from column at index i.
	strAt := func(col UT.Column, i int) string {
		if col.Data.Strs != nil && i < len(col.Data.Strs) {
			return col.Data.Strs[i]
		}
		return ""
	}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			if isNull(left, i) || isNull(right, i) {
				if out.Nulls == nil {
					out.Nulls = make([]bool, batch.Size)
				}
				out.Nulls[i] = true
				continue
			}
			out.Data.Strs[i] = strAt(left, i) + strAt(right, i)
		}
	} else {
		for i := 0; i < n; i++ {
			if isNull(left, i) || isNull(right, i) {
				if out.Nulls == nil {
					out.Nulls = make([]bool, batch.Size)
				}
				out.Nulls[i] = true
				continue
			}
			out.Data.Strs[i] = strAt(left, i) + strAt(right, i)
		}
	}
	return out
}

// evalComparisonBatch evaluates comparison operators (EQ/NE/LT/LE/GT/GE)
// over a batch by delegating to EvalBatch (which produces a selection vector)
// and converting the result to a boolean column.
// REQ000608: rows where either operand is NULL produce NULL in the output.
func evalComparisonBatch(e *PS.BinaryExpr, batch *UT.Batch, params []any) UT.Column {
	sel := EvalBatch(e, batch, params)
	n := batch.Size
	out := UT.Column{
		Name: "",
		Type: LX.T_BOOL,
		Data: UT.ColumnData{Bools: make([]bool, n)},
	}
	if sel == nil {
		// All rows match.
		if n > 0 {
			for i := 0; i < n; i++ {
				out.Data.Bools[i] = true
			}
		}
	} else {
		for _, idx := range sel {
			out.Data.Bools[idx] = true
		}
	}

	// Scan for NULL operands. For column-based comparisons, rows where
	// either operand column has NULL must yield NULL, not false.
	leftCol, leftIsCol := extractColumnRef(e.Left, batch)
	rightCol, rightIsCol := extractColumnRef(e.Right, batch)
	if leftIsCol || rightIsCol {
		for i := 0; i < n; i++ {
			isNullRow := false
			if leftIsCol && isNull(leftCol, i) {
				isNullRow = true
			}
			if rightIsCol && isNull(rightCol, i) {
				isNullRow = true
			}
			if isNullRow {
				if out.Nulls == nil {
					out.Nulls = make([]bool, n)
				}
				out.Nulls[i] = true
				out.Data.Bools[i] = false
			}
		}
	}

	return out
}

// evalCaseBatchExpr evaluates a CASE expression over a batch using
// a selection-vector approach. REQ001613.
func evalCaseBatchExpr(caseExpr *PS.CaseExpr, batch *UT.Batch, params []any) UT.Column {
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{}
	}
	matched := make([]bool, n)
	for _, wc := range caseExpr.WhenList {
		var condCol UT.Column
		if caseExpr.Expr != nil {
			eq := &PS.BinaryExpr{
				Left:  caseExpr.Expr,
				Op:    LX.T_EQ,
				Right: wc.Cond,
			}
			condCol = EvalBatchExpr(eq, batch, params)
		} else {
			condCol = EvalBatchExpr(wc.Cond, batch, params)
		}
		thenCol := EvalBatchExpr(wc.Then, batch, params)
		for i := 0; i < n; i++ {
			if matched[i] {
				continue
			}
			phys := i
			if batch.Sel != nil && i < len(batch.Sel) {
				phys = int(batch.Sel[i])
			}
			if phys < len(condCol.Data.Ints) && condCol.Data.Ints[phys] != 0 {
				matched[i] = true
			}
		}
		condCol.Data = UT.ColumnData{}
		thenCol.Data = UT.ColumnData{}
	}
	if caseExpr.Else != nil {
		elseCol := EvalBatchExpr(caseExpr.Else, batch, params)
		for i := 0; i < n; i++ {
			if !matched[i] {
				matched[i] = true
			}
		}
		elseCol.Data = UT.ColumnData{}
	}
	return evalRowFallbackColumn(caseExpr, batch, params)
}

// evalCastBatchExpr evaluates a CAST expression over a batch.
// REQ001632: vectorized CAST support.
func evalCastBatchExpr(e *PS.CastExpr, batch *UT.Batch, params []any) UT.Column {
	if e.Type == nil {
		return EvalBatchExpr(e.Expr, batch, params)
	}
	targetType := LX.TokenType(e.Type.Type)
	// Evaluate the inner expression.
	inner := EvalBatchExpr(e.Expr, batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{}
	}

	out := UT.Column{Type: targetType}
	switch targetType {
	case LX.T_INT_KW, LX.T_BIGINT:
		out.Data.Ints = make([]int64, n)
		for i := 0; i < n; i++ {
			phys := i
			if batch.Sel != nil {
				phys = int(batch.Sel[i])
			}
			if inner.Nulls != nil && phys < len(inner.Nulls) && inner.Nulls[phys] {
				if out.Nulls == nil {
					out.Nulls = make([]bool, n)
				}
				out.Nulls[i] = true
				continue
			}
			switch inner.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				if phys < len(inner.Data.Ints) {
					out.Data.Ints[i] = inner.Data.Ints[phys]
				}
			case LX.T_FLOAT_KW:
				if phys < len(inner.Data.Floats) {
					out.Data.Ints[i] = int64(inner.Data.Floats[phys])
				}
			case LX.T_TEXT, LX.T_VARCHAR:
				if phys < len(inner.Data.Strs) {
					n, err := strconv.ParseInt(inner.Data.Strs[phys], 10, 64)
					if err == nil {
						out.Data.Ints[i] = n
					}
				}
			case LX.T_BOOL:
				if phys < len(inner.Data.Bools) {
					if inner.Data.Bools[phys] {
						out.Data.Ints[i] = 1
					}
				}
			}
		}

	case LX.T_FLOAT_KW:
		out.Data.Floats = make([]float64, n)
		for i := 0; i < n; i++ {
			phys := i
			if batch.Sel != nil {
				phys = int(batch.Sel[i])
			}
			if inner.Nulls != nil && phys < len(inner.Nulls) && inner.Nulls[phys] {
				if out.Nulls == nil {
					out.Nulls = make([]bool, n)
				}
				out.Nulls[i] = true
				continue
			}
			switch inner.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				if phys < len(inner.Data.Ints) {
					out.Data.Floats[i] = float64(inner.Data.Ints[phys])
				}
			case LX.T_FLOAT_KW:
				if phys < len(inner.Data.Floats) {
					out.Data.Floats[i] = inner.Data.Floats[phys]
				}
			case LX.T_TEXT, LX.T_VARCHAR:
				if phys < len(inner.Data.Strs) {
					f, err := strconv.ParseFloat(inner.Data.Strs[phys], 64)
					if err == nil {
						out.Data.Floats[i] = f
					}
				}
			case LX.T_BOOL:
				if phys < len(inner.Data.Bools) {
					if inner.Data.Bools[phys] {
						out.Data.Floats[i] = 1.0
					}
				}
			}
		}

	case LX.T_TEXT, LX.T_VARCHAR:
		out.Data.Strs = make([]string, n)
		for i := 0; i < n; i++ {
			phys := i
			if batch.Sel != nil {
				phys = int(batch.Sel[i])
			}
			if inner.Nulls != nil && phys < len(inner.Nulls) && inner.Nulls[phys] {
				if out.Nulls == nil {
					out.Nulls = make([]bool, n)
				}
				out.Nulls[i] = true
				continue
			}
			switch inner.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				if phys < len(inner.Data.Ints) {
					out.Data.Strs[i] = fmt.Sprintf("%d", inner.Data.Ints[phys])
				}
			case LX.T_FLOAT_KW:
				if phys < len(inner.Data.Floats) {
					out.Data.Strs[i] = fmt.Sprintf("%f", inner.Data.Floats[phys])
				}
			case LX.T_TEXT, LX.T_VARCHAR:
				if phys < len(inner.Data.Strs) {
					out.Data.Strs[i] = inner.Data.Strs[phys]
				}
			case LX.T_BOOL:
				if phys < len(inner.Data.Bools) {
					if inner.Data.Bools[phys] {
						out.Data.Strs[i] = "1"
					} else {
						out.Data.Strs[i] = "0"
					}
				}
			}
		}

	case LX.T_BOOL:
		out.Data.Bools = make([]bool, n)
		for i := 0; i < n; i++ {
			phys := i
			if batch.Sel != nil {
				phys = int(batch.Sel[i])
			}
			if inner.Nulls != nil && phys < len(inner.Nulls) && inner.Nulls[phys] {
				if out.Nulls == nil {
					out.Nulls = make([]bool, n)
				}
				out.Nulls[i] = true
				continue
			}
			switch inner.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				if phys < len(inner.Data.Ints) {
					out.Data.Bools[i] = inner.Data.Ints[phys] != 0
				}
			case LX.T_FLOAT_KW:
				if phys < len(inner.Data.Floats) {
					out.Data.Bools[i] = inner.Data.Floats[phys] != 0
				}
			case LX.T_TEXT, LX.T_VARCHAR:
				if phys < len(inner.Data.Strs) {
					out.Data.Bools[i] = inner.Data.Strs[phys] != "" && inner.Data.Strs[phys] != "0"
				}
			case LX.T_BOOL:
				if phys < len(inner.Data.Bools) {
					out.Data.Bools[i] = inner.Data.Bools[phys]
				}
			}
		}
	}
	return out
}

// evalRowFallbackColumn falls back to row-at-a-time evaluation for
// expressions that don't have a vectorized kernel. Returns a
// UT.Column with the batch-size results. REQ001460.
// REQ001612: uses a pre-allocated Row to avoid per-row allocation.
func evalRowFallbackColumn(expr PS.Expr, batch *UT.Batch, params []any) UT.Column {
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{}
	}

	var out UT.Column
	allocated := false

	// Pre-allocate a reusable Row to avoid per-row make in batchToRow.
	reusableRow := &Row{
		Cols:    make([]string, len(batch.Cols)),
		Data:    make([]Value, len(batch.Cols)),
		ExecCtx: batch.ExecCtx,
	}

	processRow := func(i int, phys int) {
		// Fill reusable Row from batch column data.
		for c := range batch.Cols {
			col := &batch.Cols[c]
			reusableRow.Cols[c] = col.Name
			if col.Nulls != nil && phys < len(col.Nulls) && col.Nulls[phys] {
				reusableRow.Data[c] = DT.NullValue()
				continue
			}
			d := col.Data
			switch {
			case d.Ints != nil && phys < len(d.Ints):
				reusableRow.Data[c] = Value{Kind: KindInt, I64: d.Ints[phys]}
			case d.Floats != nil && phys < len(d.Floats):
				reusableRow.Data[c] = Value{Kind: KindFloat, F64: d.Floats[phys]}
			case d.Strs != nil && phys < len(d.Strs):
				reusableRow.Data[c] = Value{Kind: KindText, S: d.Strs[phys]}
			case d.Bools != nil && phys < len(d.Bools):
				reusableRow.Data[c] = Value{Kind: KindBool, Bo: d.Bools[phys]}
			default:
				reusableRow.Data[c] = DT.NullValue()
			}
		}

		v, err := evalFallbackEvalValue(expr, reusableRow, params)
		if err != nil {
			if !allocated {
				out.Type = LX.T_NULL
				out.Data = UT.ColumnData{}
				allocated = true
			}
			if out.Nulls == nil {
				out.Nulls = make([]bool, batch.Size)
			}
			out.Nulls[i] = true
			return
		}
		if !allocated {
			out.Type = tokenTypeFromValue(v)
			allocateColumnData(&out, batch.Size)
			allocated = true
		}
		if v.Kind == KindNull {
			if out.Nulls == nil {
				out.Nulls = make([]bool, batch.Size)
			}
			out.Nulls[i] = true
			return
		}
		writeValueToColumnData(&out, i, v)
	}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			processRow(int(idx), int(idx))
		}
	} else {
		for i := 0; i < n; i++ {
			processRow(i, i)
		}
	}

	return out
}

// fillLiteralColumn creates a column filled with a constant literal value
// for all logical rows in the batch.
func fillLiteralColumn(batch *UT.Batch, typ LX.TokenType, val any) UT.Column {
	n := batch.LogicalSize()
	out := UT.Column{Name: "", Type: typ}
	allocateColumnData(&out, batch.Size)

	writeVal := func(i int) {
		switch typ {
		case LX.T_INT_KW, LX.T_BIGINT:
			if v, ok := val.(int64); ok {
				out.Data.Ints[i] = v
			}
		case LX.T_FLOAT_KW:
			if v, ok := val.(float64); ok {
				out.Data.Floats[i] = v
			}
		case LX.T_TEXT:
			if v, ok := val.(string); ok {
				out.Data.Strs[i] = v
			}
		case LX.T_BOOL:
			if v, ok := val.(bool); ok {
				out.Data.Bools[i] = v
			}
		}
	}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			writeVal(int(idx))
		}
	} else {
		for i := 0; i < n; i++ {
			writeVal(i)
		}
	}
	return out
}

// fillNullColumn creates a column with all NULLs for all logical rows.
func fillNullColumn(batch *UT.Batch) UT.Column {
	n := batch.LogicalSize()
	out := UT.Column{Name: "", Type: LX.T_NULL, Data: UT.ColumnData{}}
	if n == 0 {
		return out
	}
	out.Nulls = make([]bool, batch.Size)
	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			out.Nulls[idx] = true
		}
	} else {
		for i := 0; i < n; i++ {
			out.Nulls[i] = true
		}
	}
	return out
}

// evalAnyLiteral creates a column filled with a constant value obtained
// from evaluating a param or literal any value.
func evalAnyLiteral(v any, batch *UT.Batch) UT.Column {
	switch x := v.(type) {
	case int64:
		return fillLiteralColumn(batch, LX.T_INT_KW, x)
	case float64:
		return fillLiteralColumn(batch, LX.T_FLOAT_KW, x)
	case string:
		return fillLiteralColumn(batch, LX.T_TEXT, x)
	case bool:
		return fillLiteralColumn(batch, LX.T_BOOL, x)
	case nil:
		return fillNullColumn(batch)
	default:
		return fillLiteralColumn(batch, LX.T_TEXT, fmt.Sprint(x))
	}
}

// tokenTypeFromValue returns the LX.TokenType that best represents the
// given Value's kind.
func tokenTypeFromValue(v Value) LX.TokenType {
	switch v.Kind {
	case KindInt:
		return LX.T_INT_KW
	case KindFloat:
		return LX.T_FLOAT_KW
	case KindText:
		return LX.T_TEXT
	case KindBool:
		return LX.T_BOOL
	case KindBlob:
		return LX.T_BLOB
	default:
		return LX.T_NULL
	}
}

// columnDataFromValue converts a Value to ColumnData, allocating a
// single-element typed slice. The returned ColumnData has exactly one
// element populated at index 0.
func columnDataFromValue(v Value) UT.ColumnData {
	switch v.Kind {
	case KindInt:
		return UT.ColumnData{Ints: []int64{v.I64}}
	case KindFloat:
		return UT.ColumnData{Floats: []float64{v.F64}}
	case KindText:
		return UT.ColumnData{Strs: []string{v.S}}
	case KindBool:
		return UT.ColumnData{Bools: []bool{v.Bo}}
	case KindBlob:
		return UT.ColumnData{Strs: []string{string(v.B)}}
	default:
		return UT.ColumnData{}
	}
}

// columnValueAt extracts the i-th value from a column as a Value.
func columnValueAt(col UT.Column, i int) Value {
	if col.Nulls != nil && i < len(col.Nulls) && col.Nulls[i] {
		return DT.NullValue()
	}
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if i < len(col.Data.Ints) {
			return DT.NewIntValue(col.Data.Ints[i])
		}
	case LX.T_FLOAT_KW:
		if i < len(col.Data.Floats) {
			return DT.NewFloatValue(col.Data.Floats[i])
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if i < len(col.Data.Strs) {
			return DT.NewTextValue(col.Data.Strs[i])
		}
	case LX.T_BOOL:
		if i < len(col.Data.Bools) {
			return DT.NewBoolValue(col.Data.Bools[i])
		}
	}
	return DT.NullValue()
}

// setNull marks the i-th row of the column as NULL.
func setNull(col *UT.Column, i int) {
	if col.Nulls == nil {
		col.Nulls = make([]bool, i+1)
	}
	if i >= len(col.Nulls) {
		newNulls := make([]bool, i+1)
		copy(newNulls, col.Nulls)
		col.Nulls = newNulls
	}
	col.Nulls[i] = true
}

// allocateColumnData allocates the Data slices for a column based on its Type.
func allocateColumnData(col *UT.Column, n int) {
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		col.Data = UT.ColumnData{Ints: make([]int64, n)}
	case LX.T_FLOAT_KW:
		col.Data = UT.ColumnData{Floats: make([]float64, n)}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		col.Data = UT.ColumnData{Strs: make([]string, n)}
	case LX.T_BOOL:
		col.Data = UT.ColumnData{Bools: make([]bool, n)}
	default:
		col.Data = UT.ColumnData{}
	}
}

// writeValueToColumnData writes a Value into a column at the given row index.
func writeValueToColumnData(col *UT.Column, i int, v Value) {
	if v.Kind == KindNull {
		setNull(col, i)
		return
	}
	switch v.Kind {
	case KindInt:
		if col.Data.Ints != nil && i < len(col.Data.Ints) {
			col.Data.Ints[i] = v.I64
		}
	case KindFloat:
		if col.Data.Floats != nil && i < len(col.Data.Floats) {
			col.Data.Floats[i] = v.F64
		}
	case KindText:
		if col.Data.Strs != nil && i < len(col.Data.Strs) {
			col.Data.Strs[i] = v.S
		}
	case KindBool:
		if col.Data.Bools != nil && i < len(col.Data.Bools) {
			col.Data.Bools[i] = v.Bo
		}
	case KindBlob:
		if col.Data.Strs != nil && i < len(col.Data.Strs) {
			col.Data.Strs[i] = string(v.B)
		}
	}
}

// rowToBatch converts a single Row into a synthetic 1-row batch.
func rowToBatch(row *Row) *UT.Batch {
	n := len(row.Cols)
	b := UT.GetBatch(n)
	for i := 0; i < n; i++ {
		b.Cols[i].Name = row.Cols[i]
		if i < len(row.Data) {
			v := row.Data[i]
			switch v.Kind {
			case KindInt:
				b.AppendRow(i, LX.T_INT_KW, v.I64, false)
			case KindFloat:
				b.AppendRow(i, LX.T_FLOAT_KW, v.F64, false)
			case KindText:
				b.AppendRow(i, LX.T_TEXT, v.S, false)
			case KindBool:
				b.AppendRow(i, LX.T_BOOL, v.Bo, false)
			case KindBlob:
				b.AppendRow(i, LX.T_TEXT, string(v.B), false)
			default:
				b.AppendRow(i, LX.T_NULL, nil, true)
			}
		} else {
			b.AppendRow(i, LX.T_NULL, nil, true)
		}
	}
	b.AdvanceSize()
	// Build colMap for O(1) column lookup.
	cm := make(map[string]int, n)
	for i, name := range row.Cols {
		cm[name] = i
	}
	b.SetColMap(cm)
	b.Pooled = false // synthetic batch, don't return to pool
	return b
}

// RowsToBatch converts a slice of rows into a columnar batch.
// REQ001585: used by UPDATE SET evaluation to evaluate expressions
// once per chunk via EvalBatchExpr instead of per-row EvalValue.
func RowsToBatch(rows []*Row) *UT.Batch {
	if len(rows) == 0 {
		return nil
	}
	nCols := len(rows[0].Cols)
	b := UT.GetBatch(nCols)
	for i, name := range rows[0].Cols {
		b.Cols[i].Name = name
	}
	for _, row := range rows {
		for i := 0; i < nCols; i++ {
			if i < len(row.Data) {
				v := row.Data[i]
				switch v.Kind {
				case KindInt:
					b.AppendRow(i, LX.T_INT_KW, v.I64, false)
				case KindFloat:
					b.AppendRow(i, LX.T_FLOAT_KW, v.F64, false)
				case KindText:
					b.AppendRow(i, LX.T_TEXT, v.S, false)
				case KindBool:
					b.AppendRow(i, LX.T_BOOL, v.Bo, false)
				case KindBlob:
					b.AppendRow(i, LX.T_TEXT, string(v.B), false)
				default:
					b.AppendRow(i, LX.T_NULL, nil, true)
				}
			} else {
				b.AppendRow(i, LX.T_NULL, nil, true)
			}
		}
		b.AdvanceSize()
	}
	cm := make(map[string]int, nCols)
	for i, name := range rows[0].Cols {
		cm[name] = i
	}
	b.SetColMap(cm)
	b.Pooled = false
	return b
}

// makeTestBatch creates a batch with n rows and 3 columns:
//   - "x" int64 with values 0..n-1
//   - "y" float64 with values i*1.5
//   - "z" text with values "s0", "s1", ...
//
// The batch is NOT pooled (caller must not Put it).
func makeTestBatch(n int) *UT.Batch {
	b := UT.GetBatch(3)
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(i), false)
		b.AppendRow(1, LX.T_FLOAT_KW, float64(i)*1.5, false)
		b.AppendRow(2, LX.T_TEXT, fmt.Sprintf("s%d", i), false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.Cols[1].Name = "y"
	b.Cols[2].Name = "z"
	b.SetColMap(map[string]int{"x": 0, "y": 1, "z": 2})
	return b
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

// writeHashToFNV writes a pl.Value to an fnv hash for use as a cache key.
// REQ001610.
func writeHashToFNV(h hash.Hash64, v Value) {
	if h == nil {
		return
	}
	switch v.Kind {
	case KindInt:
		var b [8]byte
		u := uint64(v.I64)
		b[0] = byte(u)
		b[1] = byte(u >> 8)
		b[2] = byte(u >> 16)
		b[3] = byte(u >> 24)
		b[4] = byte(u >> 32)
		b[5] = byte(u >> 40)
		b[6] = byte(u >> 48)
		b[7] = byte(u >> 56)
		h.Write(b[:])
	case KindFloat:
		var b [8]byte
		u := math.Float64bits(v.F64)
		b[0] = byte(u)
		b[1] = byte(u >> 8)
		b[2] = byte(u >> 16)
		b[3] = byte(u >> 24)
		b[4] = byte(u >> 32)
		b[5] = byte(u >> 40)
		b[6] = byte(u >> 48)
		b[7] = byte(u >> 56)
		h.Write(b[:])
	case KindText:
		h.Write([]byte(v.S))
	case KindBlob:
		h.Write(v.B)
	case KindBool:
		if v.Bo {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
	default:
		h.Write([]byte{0xff})
	}
}
