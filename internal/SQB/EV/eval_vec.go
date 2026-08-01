package EV

import (
	"context"
	"fmt"
	"hash"
	"hash/fnv"
	"log/slog"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// REQ001986: pool for batchToRow() output rows. Each pooled row has
// pre-allocated Cols and Data slices sized to a common column count.
// Rows are obtained via getRowPool(n) and returned via putRowPool(row).
// The pool reduces per-row allocation overhead in the batch→row fallback
// path, which is hit for complex expressions (CASE, subqueries, etc.).
var rowPool = sync.Pool{
	New: func() any {
		r := &Row{
			Cols: make([]string, 0, 16),
			Data: make([]Value, 0, 16),
		}
		return r
	},
}

// REQ002077: column data pools for arithmetic/projection chains.
var int64Pool = sync.Pool{New: func() any { s := make([]int64, 0, 1024); return &s }}
var float64Pool = sync.Pool{New: func() any { s := make([]float64, 0, 1024); return &s }}
var strPool = sync.Pool{New: func() any { s := make([]string, 0, 1024); return &s }}

func getInt64Col(n int) []int64 {
	ptr := int64Pool.Get().(*[]int64)
	s := *ptr
	if cap(s) < n {
		s = make([]int64, n)
	}
	return s[:n]
}

func getFloat64Col(n int) []float64 {
	ptr := float64Pool.Get().(*[]float64)
	s := *ptr
	if cap(s) < n {
		s = make([]float64, n)
	}
	return s[:n]
}

func getStrCol(n int) []string {
	ptr := strPool.Get().(*[]string)
	s := *ptr
	if cap(s) < n {
		s = make([]string, n)
	}
	return s[:n]
}

// REQ002075: selection vector pool for comparison kernels.
var selPool = sync.Pool{
	New: func() any {
		sel := make([]uint16, 0, 1024)
		return &sel
	},
}

func getSel(n int) []uint16 {
	ptr := selPool.Get().(*[]uint16)
	sel := *ptr
	if cap(sel) < n {
		sel = make([]uint16, 0, n)
	}
	return sel[:0]
}

func putSel(sel []uint16) {
	if cap(sel) > 0 && cap(sel) <= 4096 {
		selPool.Put(&sel)
	}
}

// REQ001994: session-wide counter of batch→row fallback hits. Incremented
// whenever batchToRow, evalRowFallback, or evalRowFallbackColumn is invoked.
// Read+reset by PRAGMA eval_fallback_stats. Atomic so concurrent statements
// (parallel probes, worker pools) don't lose counts.
var fallbackHitsGlobal atomic.Int64

// REQ001994: increment the fallback counter on the batch's ExecCtx
// (preferred — survives across statements via the executor) and the
// global counter (fallback when ExecCtx is missing, e.g. unit tests).
func incrementFallbackHits(batch *UT.Batch) {
	fallbackHitsGlobal.Add(1)
	if batch != nil && batch.ExecCtx != nil {
		batch.ExecCtx.FallbackHits.Add(1)
	}
}

// ReadAndResetFallbackHits atomically reads and resets the global fallback
// counter. REQ001994.
func ReadAndResetFallbackHits() int64 {
	return fallbackHitsGlobal.Swap(0)
}

func getRowPool(nCols int) *Row {
	r := rowPool.Get().(*Row)
	if cap(r.Cols) < nCols {
		r.Cols = make([]string, 0, nCols)
		r.Data = make([]Value, 0, nCols)
	} else {
		r.Cols = r.Cols[:0]
		r.Data = r.Data[:0]
	}
	r.ExecCtx = nil
	return r
}

func putRowPool(r *Row) {
	if r == nil {
		return
	}
	r.ExecCtx = nil
	rowPool.Put(r)
}

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
		col, ok := ExtractColumnRef(e.Expr, batch)
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
	case *PS.BetweenExpr:
		// REQ002031: BETWEEN batch evaluation.
		return evalBetweenBatch(e, batch, params)
	default:
		// Fallback: row-at-a-time
		return evalRowFallback(expr, batch, params)
	}
}

// isComparisonOp reports whether op is one of EQ/NE/LT/LE/GT/GE.
func isComparisonOp(op LX.TokenType) bool {
	switch op {
	case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
		return true
	}
	return false
}

// evalBinaryBatch handles binary expressions (comparisons, arithmetic).
// Fast paths:
//   - column op column: vectorized column-to-column comparison
//   - column op literal: vectorized column-to-literal comparison
//   - expression op column/literal/expression: evaluate both sides as
//     columns via EvalBatchExpr, then vectorized compare
//   - otherwise: row-at-a-time fallback
func evalBinaryBatch(e *PS.BinaryExpr, batch *UT.Batch, params []any) []uint16 {
	// REQ001010: AND/OR batch evaluation
	if e.Op == LX.T_AND {
		return evalAndBatch(e, batch, params)
	}
	if e.Op == LX.T_OR {
		return evalOrBatch(e, batch, params)
	}

	// REQ002031: IS NULL / IS NOT NULL batch evaluation.
	if e.Op == LX.T_IS {
		return evalIsBatch(e, batch, params)
	}

	// REQ002031: LIKE batch evaluation.
	if e.Op == LX.T_LIKE {
		return evalLikeBatch(e, batch, params)
	}

	// REQ002031: GLOB batch evaluation.
	if e.Op == LX.T_GLOB {
		return evalGlobBatch(e, batch, params)
	}

	// REQ002073: comparison expressions with non-column operands (e.g.
	// `a+b > 150`, `a*2 < 10`, `LENGTH(a) > 5`) previously fell back to
	// row-at-a-time evaluation. Evaluate both sides as columns and then
	// compare the resulting columns.
	if !isComparisonOp(e.Op) {
		return evalRowFallback(e, batch, params)
	}

	leftCol, leftIsCol := ExtractColumnRef(e.Left, batch)
	rightCol, rightIsCol := ExtractColumnRef(e.Right, batch)
	leftLit, leftIsLit := evalLiteral(e.Left, params)
	rightLit, rightIsLit := evalLiteral(e.Right, params)

	// Fast path: both sides are simple columns.
	if leftIsCol && rightIsCol {
		return compareColumns(leftCol, rightCol, e.Op, batch)
	}

	// Fast path: one side is a column, the other a literal.
	if leftIsCol && rightIsLit {
		return compareColLiteral(leftCol, rightLit, e.Op, batch)
	}
	if rightIsCol && leftIsLit {
		return compareColLiteral(rightCol, leftLit, SwapOp(e.Op), batch)
	}

	// At least one side is a non-trivial expression. Materialize both
	// sides as columns and compare them.
	lc := evalBinarySide(e.Left, leftIsCol, leftCol, leftIsLit, leftLit, batch, params)
	rc := evalBinarySide(e.Right, rightIsCol, rightCol, rightIsLit, rightLit, batch, params)
	if lc.Type == LX.T_NULL || rc.Type == LX.T_NULL {
		return evalRowFallback(e, batch, params)
	}
	return compareColumns(lc, rc, e.Op, batch)
}

// evalBinarySide materializes one side of a binary comparison as a
// column. Simple columns and literals are returned directly; everything
// else goes through EvalBatchExpr.
func evalBinarySide(expr PS.Expr, isCol bool, col UT.Column, isLit bool, lit any, batch *UT.Batch, params []any) UT.Column {
	if isCol {
		return col
	}
	if isLit {
		return evalAnyLiteral(lit, batch)
	}
	return EvalBatchExpr(expr, batch, params)
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
			// Check if the inner expression is a BETWEEN with NULL operands.
			// In that case, the empty result means "all rows are NULL" (unknown),
			// NOT "all rows are FALSE". NOT should also return unknown (empty).
			// REQ002317.
			if _, ok := e.Operand.(*PS.BetweenExpr); ok {
				return []uint16{}
			}
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
func ExtractColumnRef(expr PS.Expr, batch *UT.Batch) (UT.Column, bool) {
	// Handle both Ident and QualifiedName (which carries table-qualified names).
	var name string
	switch e := expr.(type) {
	case *PS.Ident:
		name = e.Name
	case *PS.QualifiedName:
		name = e.Name
	default:
		return UT.Column{}, false
	}
	// Use pre-computed index if available
	if batch.ColMap() != nil {
		if idx, found := batch.ColMap()[name]; found {
			// REQ001684: when column pruning is active the batch may have
			// fewer logical columns than the full-schema colMap indices.
			// Verify the resolved column actually carries the requested
			// name — pool batches retain MaxColumns slots, so the raw
			// index check alone is insufficient.
			if idx >= 0 && idx < len(batch.Cols) {
				if strings.EqualFold(batch.Cols[idx].Name, name) {
					return batch.Cols[idx], true
				}
				// Also try matching the suffix of qualified names (e.g., "bs1.name" matches "name").
				if parts := strings.Split(batch.Cols[idx].Name, "."); len(parts) > 0 {
					if strings.EqualFold(parts[len(parts)-1], name) {
						return batch.Cols[idx], true
					}
				}
			}
		}
		// Fallback: linear scan when colMap index is stale or mismatched
		// (e.g. column pruning reordered the batch layout).
		for i := range batch.Cols {
			if strings.EqualFold(batch.Cols[i].Name, name) {
				return batch.Cols[i], true
			}
			// Also match suffix of qualified names (e.g., "bs1.name" matches "name").
			if parts := strings.Split(batch.Cols[i].Name, "."); len(parts) > 0 {
				if strings.EqualFold(parts[len(parts)-1], name) {
					return batch.Cols[i], true
				}
			}
		}
		return UT.Column{}, false
	}
	// Fallback: linear scan by column name from "c0", "c1", etc.
	// (This handles synthetic batches in tests.)
	for i := range batch.Cols {
		if batch.Cols[i].Name == name {
			return batch.Cols[i], true
		}
		// Also match suffix of qualified names.
		if parts := strings.Split(batch.Cols[i].Name, "."); len(parts) > 0 {
			if parts[len(parts)-1] == name {
				return batch.Cols[i], true
			}
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
	sel := getSel(n)
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
// REQ001986: uses pooled rows from batchToRow to reduce per-row
// allocation overhead in the fallback path.
func evalRowFallback(expr PS.Expr, batch *UT.Batch, params []any) []uint16 {
	sel := make([]uint16, 0, batch.Size)
	for i := 0; i < batch.Size; i++ {
		// Build a synthetic row from batch data
		row := batchToRow(batch, i)
		val, err := evalFallbackEvalValue(expr, row, params)
		putRowPool(row)
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
// REQ001986: returns a pooled Row — caller must call putRowPool(row)
// when done to return it to the pool. Never retain the Row after
// returning it to the pool.
// REQ001460: forwards the batch's ExecContext so subquery
// evaluation in the row-fallback path can locate the planner.
func batchToRow(batch *UT.Batch, idx int) *Row {
	incrementFallbackHits(batch)
	row := getRowPool(len(batch.Cols))
	row.ExecCtx = batch.ExecCtx
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

// REQ001664: per-call expression memoization. Caches the result of
// EvalBatchExpr for each subexpression pointer within a single top-level
// call. Cleared automatically when the depth counter reaches 0.
var evalBatchExprMemo map[PS.Expr]UT.Column
var evalBatchExprDepth int

// EvalBatchExpr evaluates an expression over an entire batch, producing
// a column result. This is the batch-parallel equivalent of EvalValue.
// Dispatches by expression type to vectorized kernels where available.
// REQ001210.
//
// REQ001664: memoizes subexpression results for the duration of one
// top-level call. When the same *PS.Ident appears in multiple places
// in the expression tree (e.g. `a + b*2 + a*3`), it is evaluated
// once and the cached column is reused.
func EvalBatchExpr(expr PS.Expr, batch *UT.Batch, params []any) UT.Column {
	if expr == nil {
		return UT.Column{Type: LX.T_NULL}
	}

	// REQ001664: top-level call initializes/clears the memo.
	if evalBatchExprDepth == 0 {
		if evalBatchExprMemo == nil {
			evalBatchExprMemo = make(map[PS.Expr]UT.Column, 16)
		} else {
			clear(evalBatchExprMemo)
		}
	}
	evalBatchExprDepth++
	defer func() { evalBatchExprDepth-- }()

	// REQ001664: return cached result for already-evaluated subexpressions.
	if cached, ok := evalBatchExprMemo[expr]; ok {
		return cached
	}

	var result UT.Column
	switch e := expr.(type) {
	case *PS.Ident:
		// Column reference: shallow copy of source column.
		if col, ok := ExtractColumnRef(e, batch); ok {
			result = col
		} else {
			result = UT.Column{Type: LX.T_NULL}
		}

	case *PS.QualifiedName:
		// Column reference with table qualifier: shallow copy of source column.
		if col, ok := ExtractColumnRef(e, batch); ok {
			result = col
		} else {
			result = UT.Column{Type: LX.T_NULL}
		}

	case *PS.NumberLiteral:
		result = FillLiteralColumn(batch, LX.T_INT_KW, e.Val)

	case *PS.FloatLiteral:
		result = FillLiteralColumn(batch, LX.T_FLOAT_KW, e.Val)

	case *PS.StringLiteral:
		result = FillLiteralColumn(batch, LX.T_TEXT, e.Val)

	case *PS.BoolLiteral:
		result = FillLiteralColumn(batch, LX.T_BOOL, e.Val)

	case *PS.NullLiteral:
		result = FillNullColumn(batch)

	case *PS.Param:
		if e.Index < len(params) {
			result = evalAnyLiteral(params[e.Index], batch)
		} else {
			result = FillNullColumn(batch)
		}

	case *PS.BinaryExpr:
		result = evalBinaryBatchExpr(e, batch, params)

	case *PS.UnaryExpr:
		result = evalUnaryBatchExpr(e, batch, params)

	case *PS.AliasedExpr:
		result = EvalBatchExpr(e.Expr, batch, params)

	case *PS.CaseExpr:
		result = evalCaseBatchExpr(e, batch, params)

	case *PS.FunctionCall:
		result = evalFunctionBatchExpr(e, batch, params)

	case *PS.SubqueryExpr:
		result = evalSubqueryBatchExpr(e, batch, params)

	case *PS.InExpr:
		if e.Subquery != nil {
			result = evalBatchINSubquery(e, batch, params)
		} else {
			result = evalRowFallbackColumn(e, batch, params)
		}

	case *PS.CastExpr:
		result = evalCastBatchExpr(e, batch, params)

	default:
		result = evalRowFallbackColumn(expr, batch, params)
	}

	// REQ001664: cache for reuse within the same top-level call.
	evalBatchExprMemo[expr] = result
	return result
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
// REQ001652: adds uniform-correlated-value broadcast — when all rows in the
// batch share the same correlated values, evaluate once and broadcast.
func evalSubqueryBatchExpr(subq *PS.SubqueryExpr, batch *UT.Batch, params []any) UT.Column {
	if subq == nil {
		return FillNullColumn(batch)
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
			_ = cached
			_ = key
			if v, ok := cached.(Value); ok {
				return broadcastValueColumn(batch, v)
			}
			return evalAnyLiteral(cached, batch)
		}
		probeRow := &Row{ExecCtx: batch.ExecCtx}
		if batch.ExecCtx == nil || batch.ExecCtx.Planner == nil {
			slog.Warn("subq: ExecCtx.Planner is nil",
				"hasExecCtx", batch.ExecCtx != nil)
		}
		v, err := evalScalarSubquery(subq, probeRow, params)
		if err == nil {
			slog.Warn("subq: evalScalarSubquery succeeded", "v", v.I64)
			return broadcastValueColumn(batch, v)
		}
		slog.Warn("subq: evalScalarSubquery failed", "err", err)
		return evalRowFallbackColumn(subq, batch, params)
	}
	subqPtr := uintptr(reflect.ValueOf(subq).Pointer())

	// Resolve correlated column indices once from the first logical row
	// (shared schema across the batch).
	firstRow := batchToRow(batch, 0)
	corIdx := cachedCorrelatedColIndices(subq, firstRow)

	// REQ001652: quick uniformity check — if every row shares the same
	// correlated values, evaluate the subquery once and broadcast.
	allUniform := true
	for pos := 0; pos < n; pos++ {
		if !corrValuesEqual(batch, pos, corIdx) {
			allUniform = false
			break
		}
	}
	if allUniform {
		v, err := evalScalarSubquery(subq, firstRow, params)
		putRowPool(firstRow)
		if err == nil {
			return broadcastValueColumn(batch, v)
		}
		// On eval failure, fall through to per-row fallback.
	} else {
		putRowPool(firstRow)
	}

	// REQ001992: per-row path with group-by-key optimization.
	// Group rows by unique correlation key, evaluate subquery once per group,
	// then map results back. Avoids per-row batchToRow allocation.
	var out UT.Column
	allocated := false

	// Pass 1: compute hash for each row and group by hash.
	// REQ001992: hash directly from batch typed arrays (no batchToRow).
	type keyGroup struct {
		hash    uint64
		indices []int
	}
	groups := make(map[uint64]*keyGroup)
	groupOrder := make([]uint64, 0, n)

	for pos := 0; pos < n; pos++ {
		phys := pos
		if batch.Sel != nil && pos < len(batch.Sel) {
			phys = int(batch.Sel[pos])
		}
		if phys >= batch.Size {
			continue
		}
		h := fnv.New64a()
		hashBatchCorrelatedCols(h, batch, phys, corIdx)
		key := h.Sum64()
		g, ok := groups[key]
		if !ok {
			g = &keyGroup{hash: key, indices: make([]int, 0, 4)}
			groups[key] = g
			groupOrder = append(groupOrder, key)
		}
		g.indices = append(g.indices, pos)
	}

	// Pass 2: evaluate subquery once per unique group (with LRU cache).
	values := make(map[uint64]Value, len(groups))
	errGroups := make(map[uint64]bool)

	for _, key := range groupOrder {
		cacheKey := fmt.Sprintf("%x:%x", subqPtr, key)
		if cached, ok := correlatedSubqueryCache.Get(cacheKey); ok {
			values[key] = cached
			continue
		}
		g := groups[key]
		row := batchToRow(batch, g.indices[0])
		v, err := evalScalarSubquery(subq, row, params)
		putRowPool(row)
		if err != nil {
			errGroups[key] = true
			continue
		}
		correlatedSubqueryCache.Put(cacheKey, v)
		values[key] = v
	}

	// Pass 3: map results back to all rows.
	for _, key := range groupOrder {
		g := groups[key]
		if errGroups[key] {
			if !allocated {
				out.Type = LX.T_NULL
				out.Data = UT.ColumnData{}
				allocated = true
			}
			if out.Nulls == nil {
				out.Nulls = make([]bool, batch.Size)
			}
			for _, idx := range g.indices {
				out.Nulls[idx] = true
			}
			continue
		}
		v := values[key]
		if !allocated {
			out.Type = tokenTypeFromValue(v)
			allocateColumnData(&out, batch.Size)
			allocated = true
		}
		for _, idx := range g.indices {
			writeValueToColumnData(&out, idx, v)
		}
	}
	return out
}

// corrValuesEqual returns true when the correlated values at the given logical
// position equal those at position 0 in the same batch. Selection vectors are
// honored. Returns false for positions beyond the batch size.
func corrValuesEqual(batch *UT.Batch, logicalPos int, corIdx []int) bool {
	phys := logicalPos
	if batch.Sel != nil && logicalPos < len(batch.Sel) {
		phys = int(batch.Sel[logicalPos])
	}
	if phys >= batch.Size {
		return false
	}
	for _, idx := range corIdx {
		if idx < 0 || idx >= len(batch.Cols) {
			return false
		}
		col := &batch.Cols[idx]
		v0 := BatchValueAt(*col, 0)
		v1 := BatchValueAt(*col, phys)
		if !valuesEqual(v0, v1) {
			return false
		}
	}
	return true
}

// valuesEqual compares two any values for correlated-key equality. It treats
// integer types as compatible with float types by numeric value so that
// cache hits persist across type promotions in the batch path.
func valuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if reflect.DeepEqual(a, b) {
		return true
	}
	switch av := a.(type) {
	case int64:
		if bv, ok := b.(float64); ok {
			return float64(av) == bv
		}
	case uint64:
		if bv, ok := b.(float64); ok {
			return float64(av) == bv
		}
	case float64:
		if bv, ok := b.(int64); ok {
			return av == float64(bv)
		}
		if bv, ok := b.(uint64); ok {
			return av == float64(bv)
		}
	}
	return false
}

// broadcastValueColumn creates a constant column filled with v for
// every logical row in the batch. NULL values set the per-row null
// flag rather than producing zero-typed NULLs.
func broadcastValueColumn(batch *UT.Batch, v Value) UT.Column {
	if v.Kind == KindNull {
		return FillNullColumn(batch)
	}
	return FillLiteralColumn(batch, tokenTypeFromValue(v), v.ToAny())
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
	case LX.T_IS:
		return evalIsBatchExpr(e, batch, params)
	case LX.T_LIKE:
		return evalLikeBatchExpr(e, batch, params)
	case LX.T_GLOB:
		return evalGlobBatchExpr(e, batch, params)
	default:
		return evalRowFallbackColumn(e, batch, params)
	}
}

// evalUnaryBatchExpr evaluates a unary expression over a batch and returns
// a UT.Column result. REQ001991: previously complex CASE WHEN conditions
// like `WHEN NOT x` or `WHEN -y` fell back to row-at-a-time via
// evalRowFallbackColumn. This dispatch handles them without fallback
// for T_NOT, T_UMINUS, and other unary operators.
func evalUnaryBatchExpr(e *PS.UnaryExpr, batch *UT.Batch, params []any) UT.Column {
	if e == nil {
		return UT.Column{Type: LX.T_NULL}
	}
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_NULL}
	}
	switch e.Op {
	case LX.T_NOT:
		// Boolean NOT: invert predicate. Compute inner selection vector
		// then broadcast 0/1 to a UT.Column.
		inner := EvalBatch(e.Operand, batch, params)
		out := UT.Column{Type: LX.T_BOOL}
		allocateColumnData(&out, batch.Size)
		out.Nulls = make([]bool, batch.Size)
		// Build inverted selection: rows NOT in `inner`.
		isTrue := make([]bool, batch.Size)
		for _, idx := range inner {
			isTrue[idx] = true
		}
		for i := 0; i < n; i++ {
			phys := i
			if batch.Sel != nil && i < len(batch.Sel) {
				phys = int(batch.Sel[i])
			}
			out.Data.Bools[phys] = !isTrue[phys]
		}
		return out

	case LX.T_MINUS:
		// Negation: invert numeric sign.
		inner := EvalBatchExpr(e.Operand, batch, params)
		switch inner.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			out := UT.Column{Type: inner.Type}
			allocateColumnData(&out, batch.Size)
			if inner.Nulls != nil {
				out.Nulls = make([]bool, batch.Size)
				for i := 0; i < n; i++ {
					phys := i
					if batch.Sel != nil && i < len(batch.Sel) {
						phys = int(batch.Sel[i])
					}
					out.Nulls[phys] = inner.Nulls[phys]
					if !inner.Nulls[phys] {
						out.Data.Ints[phys] = -inner.Data.Ints[phys]
					}
				}
			} else {
				for i := 0; i < n; i++ {
					phys := i
					if batch.Sel != nil && i < len(batch.Sel) {
						phys = int(batch.Sel[i])
					}
					out.Data.Ints[phys] = -inner.Data.Ints[phys]
				}
			}
			return out
		case LX.T_FLOAT_KW:
			out := UT.Column{Type: inner.Type}
			allocateColumnData(&out, batch.Size)
			if inner.Nulls != nil {
				out.Nulls = make([]bool, batch.Size)
				for i := 0; i < n; i++ {
					phys := i
					if batch.Sel != nil && i < len(batch.Sel) {
						phys = int(batch.Sel[i])
					}
					out.Nulls[phys] = inner.Nulls[phys]
					if !inner.Nulls[phys] {
						out.Data.Floats[phys] = -inner.Data.Floats[phys]
					}
				}
			} else {
				for i := 0; i < n; i++ {
					phys := i
					if batch.Sel != nil && i < len(batch.Sel) {
						phys = int(batch.Sel[i])
					}
					out.Data.Floats[phys] = -inner.Data.Floats[phys]
				}
			}
			return out
		default:
			return evalRowFallbackColumn(e, batch, params)
		}
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
			switch e.Op {
			case LX.T_PLUS, LX.T_MINUS, LX.T_STAR, LX.T_SLASH, LX.T_DIV:
				isDiv := e.Op == LX.T_SLASH || e.Op == LX.T_DIV
				return evalArithIntBatch(leftCol, rightCol, batch, e.Op, isDiv)
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
// REQ002040: overflow-checked arithmetic — matches NumericArithValue logic.
// On overflow the output cell is set to NULL.
func evalArithIntBatch(left, right UT.Column, batch *UT.Batch, op LX.TokenType, isDiv bool) UT.Column {
	n := batch.LogicalSize()
	out := UT.Column{
		Name: "",
		Type: LX.T_INT_KW,
		Data: UT.ColumnData{Ints: getInt64Col(batch.Size)},
	}
	if n == 0 {
		return out
	}

	// overflowAdd checks a+b for int64 overflow.
	overflowAdd := func(a, b int64) bool {
		return (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b)
	}
	// overflowSub checks a-b for int64 overflow.
	overflowSub := func(a, b int64) bool {
		return (b < 0 && a > math.MaxInt64+b) || (b > 0 && a < math.MinInt64+b)
	}
	// overflowMul checks a*b for int64 overflow (matches NumericArithValue).
	overflowMul := func(a, b int64) bool {
		if a == 0 || b == 0 {
			return false
		}
		if a == -1 && b == math.MinInt64 {
			return true
		}
		if b == -1 && a == math.MinInt64 {
			return true
		}
		if a > 0 && b > 0 && a > math.MaxInt64/b {
			return true
		}
		if a < 0 && b < 0 && a < math.MaxInt64/b {
			return true
		}
		if (a > 0 && b < 0 && b < math.MinInt64/a) || (a < 0 && b > 0 && a < math.MinInt64/b) {
			return true
		}
		return false
	}

	setOutNull := func(i int) {
		if out.Nulls == nil {
			out.Nulls = make([]bool, batch.Size)
		}
		out.Nulls[i] = true
	}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			if isNull(left, i) || isNull(right, i) {
				setOutNull(i)
				continue
			}
			if isDiv && right.Data.Ints[i] == 0 {
				setOutNull(i)
				continue
			}
			a, b := left.Data.Ints[i], right.Data.Ints[i]
			switch op {
			case LX.T_PLUS:
				if overflowAdd(a, b) {
					setOutNull(i)
					continue
				}
				out.Data.Ints[i] = a + b
			case LX.T_MINUS:
				if overflowSub(a, b) {
					setOutNull(i)
					continue
				}
				out.Data.Ints[i] = a - b
			case LX.T_STAR:
				if overflowMul(a, b) {
					setOutNull(i)
					continue
				}
				out.Data.Ints[i] = a * b
			case LX.T_SLASH, LX.T_DIV:
				out.Data.Ints[i] = a / b
			}
		}
	} else {
		for i := 0; i < n; i++ {
			if isNull(left, i) || isNull(right, i) {
				setOutNull(i)
				continue
			}
			if isDiv && right.Data.Ints[i] == 0 {
				setOutNull(i)
				continue
			}
			a, b := left.Data.Ints[i], right.Data.Ints[i]
			switch op {
			case LX.T_PLUS:
				if overflowAdd(a, b) {
					setOutNull(i)
					continue
				}
				out.Data.Ints[i] = a + b
			case LX.T_MINUS:
				if overflowSub(a, b) {
					setOutNull(i)
					continue
				}
				out.Data.Ints[i] = a - b
			case LX.T_STAR:
				if overflowMul(a, b) {
					setOutNull(i)
					continue
				}
				out.Data.Ints[i] = a * b
			case LX.T_SLASH, LX.T_DIV:
				out.Data.Ints[i] = a / b
			}
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
		Data: UT.ColumnData{Floats: getFloat64Col(batch.Size)},
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
		Data: UT.ColumnData{Strs: getStrCol(batch.Size)},
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
	leftCol, leftIsCol := ExtractColumnRef(e.Left, batch)
	rightCol, rightIsCol := ExtractColumnRef(e.Right, batch)
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
	// REQ001669: track which WHEN clause matched each row (whenIdx),
	// then fill output column directly from the stored thenCols.
	// Previously we discarded condCol/thenCol and fell back to
	// evalRowFallbackColumn which re-evaluated the entire CASE
	// row-by-row — costing ~610KB B/op on select3 case_simple.
	matchedWhen := make([]int, n)
	for i := range matchedWhen {
		matchedWhen[i] = -1
	}
	thenCols := make([]UT.Column, 0, len(caseExpr.WhenList))

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
		thenCols = append(thenCols, thenCol)

		wi := len(thenCols) - 1
		for i := 0; i < n; i++ {
			if matchedWhen[i] >= 0 {
				continue
			}
			phys := i
			if batch.Sel != nil && i < len(batch.Sel) {
				phys = int(batch.Sel[i])
			}
			if phys < len(condCol.Data.Bools) && condCol.Data.Bools[phys] {
				matchedWhen[i] = wi
			}
		}
		condCol.Data = UT.ColumnData{}
	}

	// Evaluate ELSE once (if present) and hold its column.
	var elseCol UT.Column
	hasElse := caseExpr.Else != nil
	if hasElse {
		elseCol = EvalBatchExpr(caseExpr.Else, batch, params)
	}

	// Determine output type from the first matching row's thenCol,
	// or from elseCol, or default to T_NULL.
	var out UT.Column
	for i := 0; i < n; i++ {
		if wi := matchedWhen[i]; wi >= 0 && wi < len(thenCols) {
			out.Type = thenCols[wi].Type
			break
		}
	}
	if out.Type == 0 && hasElse {
		out.Type = elseCol.Type
	}
	if out.Type == 0 {
		out.Type = LX.T_NULL
	}
	allocateColumnData(&out, batch.Size)

	// Fill output column: for each row, copy from the matching
	// thenCol (or elseCol, or NULL).
	for i := 0; i < n; i++ {
		phys := i
		if batch.Sel != nil && i < len(batch.Sel) {
			phys = int(batch.Sel[i])
		}
		wi := matchedWhen[i]
		if wi >= 0 && wi < len(thenCols) {
			writeColumnValue(&out, &thenCols[wi], i, phys, batch.Size)
		} else if hasElse {
			writeColumnValue(&out, &elseCol, i, phys, batch.Size)
		} else {
			if out.Nulls == nil {
				out.Nulls = make([]bool, batch.Size)
			}
			out.Nulls[i] = true
		}
	}

	// Release thenCol/elseCol column data.
	for i := range thenCols {
		thenCols[i].Data = UT.ColumnData{}
	}
	if hasElse {
		elseCol.Data = UT.ColumnData{}
	}
	return out
}

// writeColumnValue copies a single value from src column at srcIdx
// into out column at outIdx. Handles NULL propagation and type
// dispatch. REQ001669. batchSize is the physical batch capacity
// for Nulls slice allocation.
func writeColumnValue(out, src *UT.Column, outIdx, srcIdx, batchSize int) {
	if src.Nulls != nil && srcIdx < len(src.Nulls) && src.Nulls[srcIdx] {
		if out.Nulls == nil {
			out.Nulls = make([]bool, batchSize)
		}
		out.Nulls[outIdx] = true
		return
	}
	switch out.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if src.Data.Ints != nil && srcIdx < len(src.Data.Ints) {
			out.Data.Ints[outIdx] = src.Data.Ints[srcIdx]
		}
	case LX.T_FLOAT_KW:
		if src.Data.Floats != nil && srcIdx < len(src.Data.Floats) {
			out.Data.Floats[outIdx] = src.Data.Floats[srcIdx]
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if src.Data.Strs != nil && srcIdx < len(src.Data.Strs) {
			out.Data.Strs[outIdx] = src.Data.Strs[srcIdx]
		}
	case LX.T_BOOL:
		if src.Data.Bools != nil && srcIdx < len(src.Data.Bools) {
			out.Data.Bools[outIdx] = src.Data.Bools[srcIdx]
		}
	}
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
	incrementFallbackHits(batch)
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

	// REQ001665: lift stable Cols names out of the per-row loop.
	for c := range batch.Cols {
		reusableRow.Cols[c] = batch.Cols[c].Name
	}

	processRow := func(i int, phys int) {
		// Fill reusable Row from batch column data.
		for c := range batch.Cols {
			col := &batch.Cols[c]
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
func FillLiteralColumn(batch *UT.Batch, typ LX.TokenType, val any) UT.Column {
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
func FillNullColumn(batch *UT.Batch) UT.Column {
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
		return FillLiteralColumn(batch, LX.T_INT_KW, x)
	case float64:
		return FillLiteralColumn(batch, LX.T_FLOAT_KW, x)
	case string:
		return FillLiteralColumn(batch, LX.T_TEXT, x)
	case bool:
		return FillLiteralColumn(batch, LX.T_BOOL, x)
	case nil:
		return FillNullColumn(batch)
	default:
		return FillLiteralColumn(batch, LX.T_TEXT, fmt.Sprint(x))
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
		col.Data = UT.ColumnData{Ints: getInt64Col(n)}
	case LX.T_FLOAT_KW:
		col.Data = UT.ColumnData{Floats: getFloat64Col(n)}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		col.Data = UT.ColumnData{Strs: getStrCol(n)}
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
		sel := getSel(n)
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
		sel := getSel(n)
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

// evalBatchINSubquery evaluates IN (SELECT ...) over a batch using a
// hash set built once from materialized subquery rows. REQ001990.
// Previously the dispatch fell back to evalRowFallbackColumn, which
// triggered per-row evalInSubquery — N×M round-trips through
// batchToRow. Here we materialize the subquery once via the planner,
// build a small int64 hash set (or string set for text columns), then
// probe the target column values per row using the selection vector.
//
// Falls back to evalRowFallbackColumn if the planner is unavailable
// or the subquery cannot be materialized in this batch.
func evalBatchINSubquery(e *PS.InExpr, batch *UT.Batch, params []any) UT.Column {
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_BOOL}
	}
	if e.Subquery == nil {
		return evalRowFallbackColumn(e, batch, params)
	}

	// Extract the target column once from the batch schema. If it's not
	// a plain column reference, fall back to row-at-a-time evaluation.
	targetCol, ok := ExtractColumnRef(e.Expr, batch)
	if !ok {
		return evalRowFallbackColumn(e, batch, params)
	}

	// Obtain planner from the batch's ExecCtx or first logical row.
	probeRow := batchToRow(batch, 0)
	defer putRowPool(probeRow)
	planner := getSubqueryPlanner(probeRow)
	if planner == nil {
		return evalRowFallbackColumn(e, batch, params)
	}

	// Materialize the subquery once (single materialization per batch).
	rows, err := planner.ExecuteSubquery(context.Background(), e.Subquery, probeRow, params)
	if err != nil {
		return evalRowFallbackColumn(e, batch, params)
	}

	// Allocate output bool column.
	out := UT.Column{Type: LX.T_BOOL}
	allocateColumnData(&out, batch.Size)

	// Build hash set(s) keyed by column type. Track whether ANY nulls
	// were seen in the RHS so we can propagate three-valued logic.
	hadNull := false
	intSet := make(map[int64]struct{}, len(rows))
	strSet := make(map[string]struct{}, len(rows))
	floatSet := make(map[float64]struct{}, len(rows))
	intSeen := false
	strSeen := false
	floatSeen := false

	for _, r := range rows {
		if len(r.Data) == 0 {
			continue
		}
		v := r.Data[0]
		if v.Kind == KindNull {
			hadNull = true
			continue
		}
		switch v.Kind {
		case KindInt:
			intSet[v.I64] = struct{}{}
			intSeen = true
		case KindFloat:
			floatSet[v.F64] = struct{}{}
			floatSeen = true
		case KindText:
			strSet[v.S] = struct{}{}
			strSeen = true
		}
	}

	// Probe each row in the batch using the physical index layout
	// (selection vector awareness).
	d := targetCol.Data
	for i := 0; i < n; i++ {
		phys := i
		if batch.Sel != nil && i < len(batch.Sel) {
			phys = int(batch.Sel[i])
		}
		// Three-valued NULL propagation: NULL IN (set) → NULL.
		if targetCol.Nulls != nil && phys < len(targetCol.Nulls) && targetCol.Nulls[phys] {
			if out.Nulls == nil {
				out.Nulls = make([]bool, batch.Size)
			}
			out.Nulls[phys] = true
			continue
		}
		matched := false
		if intSeen {
			if d.Ints != nil && phys < len(d.Ints) {
				if _, ok := intSet[d.Ints[phys]]; ok {
					matched = true
				}
			}
		}
		if !matched && strSeen {
			if d.Strs != nil && phys < len(d.Strs) {
				if _, ok := strSet[d.Strs[phys]]; ok {
					matched = true
				}
			}
		}
		if !matched && floatSeen {
			if d.Floats != nil && phys < len(d.Floats) {
				if _, ok := floatSet[d.Floats[phys]]; ok {
					matched = true
				}
			}
		}
		// Fallback cross-type scan (rare).
		if !matched && (intSeen && strSeen) {
			for _, r := range rows {
				if len(r.Data) == 0 {
					continue
				}
				v := r.Data[0]
				if v.Kind == KindNull {
					continue
				}
				if d.Ints != nil && phys < len(d.Ints) {
					if v.Kind == KindInt && v.I64 == d.Ints[phys] {
						matched = true
						break
					}
					if v.Kind == KindText {
						if d.Strs != nil && phys < len(d.Strs) && v.S == d.Strs[phys] {
							matched = true
							break
						}
					}
				} else if d.Strs != nil && phys < len(d.Strs) {
					if v.Kind == KindText && v.S == d.Strs[phys] {
						matched = true
						break
					}
				}
			}
		}
		if matched {
			out.Data.Bools[phys] = true
		} else if hadNull {
			// Per SQL: NULL IN (set with NULLs) → NULL.
			if out.Nulls == nil {
				out.Nulls = make([]bool, batch.Size)
			}
			out.Nulls[phys] = true
		} else {
			out.Data.Bools[phys] = false
		}
	}
	return out
}

// hashBatchCorrelatedCols computes an FNV hash of the correlated column values
// at the given physical row position in the batch, reading directly from the
// batch's typed data arrays. This avoids the batchToRow allocation in the
// per-row hash loop. REQ001992.
func hashBatchCorrelatedCols(h hash.Hash64, batch *UT.Batch, phys int, corIdx []int) {
	if h == nil {
		return
	}
	for _, idx := range corIdx {
		if idx < 0 || idx >= len(batch.Cols) {
			continue
		}
		col := &batch.Cols[idx]
		if col.Nulls != nil && phys < len(col.Nulls) && col.Nulls[phys] {
			h.Write([]byte{0xff})
			continue
		}
		switch col.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if phys < len(col.Data.Ints) {
				var b [8]byte
				u := uint64(col.Data.Ints[phys])
				b[0] = byte(u)
				b[1] = byte(u >> 8)
				b[2] = byte(u >> 16)
				b[3] = byte(u >> 24)
				b[4] = byte(u >> 32)
				b[5] = byte(u >> 40)
				b[6] = byte(u >> 48)
				b[7] = byte(u >> 56)
				h.Write(b[:])
			}
		case LX.T_FLOAT_KW:
			if phys < len(col.Data.Floats) {
				var b [8]byte
				u := math.Float64bits(col.Data.Floats[phys])
				b[0] = byte(u)
				b[1] = byte(u >> 8)
				b[2] = byte(u >> 16)
				b[3] = byte(u >> 24)
				b[4] = byte(u >> 32)
				b[5] = byte(u >> 40)
				b[6] = byte(u >> 48)
				b[7] = byte(u >> 56)
				h.Write(b[:])
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if phys < len(col.Data.Strs) {
				h.Write([]byte(col.Data.Strs[phys]))
			}
		case LX.T_BOOL:
			if phys < len(col.Data.Bools) {
				if col.Data.Bools[phys] {
					h.Write([]byte{1})
				} else {
					h.Write([]byte{0})
				}
			}
		}
	}
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

// ── REQ002031: IS NULL / IS NOT NULL / LIKE / GLOB / BETWEEN batch kernels ──

// evalIsBatch handles T_IS for predicate evaluation (selection vector).
// IS NULL: BinaryExpr{T_IS, col, NullLiteral}
// IS NOT NULL: BinaryExpr{T_IS, col, UnaryExpr{T_NOT, NullLiteral}}
func evalIsBatch(e *PS.BinaryExpr, batch *UT.Batch, params []any) []uint16 {
	// Check if right side is NullLiteral → IS NULL
	if _, ok := e.Right.(*PS.NullLiteral); ok {
		col, ok := ExtractColumnRef(e.Left, batch)
		if !ok {
			return evalRowFallback(e, batch, params)
		}
		return evalIsNullBatch(col, batch)
	}
	// Check if right side is UnaryExpr{T_NOT, NullLiteral} → IS NOT NULL
	if u, ok := e.Right.(*PS.UnaryExpr); ok && u.Op == LX.T_NOT {
		if _, ok := u.Operand.(*PS.NullLiteral); ok {
			col, ok := ExtractColumnRef(e.Left, batch)
			if !ok {
				return evalRowFallback(e, batch, params)
			}
			return evalIsNotNullBatch(col, batch)
		}
	}
	// Fallback for other IS expressions (e.g. IS TRUE, IS FALSE)
	return evalRowFallback(e, batch, params)
}

// evalIsNullBatch returns selection vector of rows where the column is NULL.
// If col.Nulls == nil, no rows are NULL → return empty []uint16{}.
func evalIsNullBatch(col UT.Column, batch *UT.Batch) []uint16 {
	n := batch.Size
	if n == 0 {
		return nil
	}
	if col.Nulls == nil {
		return []uint16{}
	}
	sel := getSel(n)
	for i := 0; i < n; i++ {
		if i < len(col.Nulls) && col.Nulls[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// evalIsNotNullBatch returns selection vector of rows where the column is NOT NULL.
// If col.Nulls == nil, all rows are non-null → return nil (all match).
func evalIsNotNullBatch(col UT.Column, batch *UT.Batch) []uint16 {
	n := batch.Size
	if n == 0 {
		return nil
	}
	if col.Nulls == nil {
		return nil // all rows match
	}
	sel := getSel(n)
	for i := 0; i < n; i++ {
		if i >= len(col.Nulls) || !col.Nulls[i] {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// evalLikeBatch evaluates col LIKE pattern per row.
// LIKE is case-insensitive (SQLite semantics): % matches any sequence, _ matches any single char.
// Fast paths:
//   - No % or _ → exact string comparison (case-insensitive)
//   - Pattern is 'prefix%' → strings.HasPrefix (case-insensitive)
//   - Otherwise → convert to regex
func evalLikeBatch(e *PS.BinaryExpr, batch *UT.Batch, params []any) []uint16 {
	// REQ002162: when an ESCAPE clause is present, the batch fast paths
	// (exact-match, prefix, regex) do not honor the escape character.
	// Fall back to the row-based evaluator, which calls MatchLike with
	// the escape char so `\%` matches a literal `%`, etc.
	if e.Escape != nil {
		return evalRowFallback(e, batch, params)
	}
	// Extract left column
	leftCol, leftIsCol := ExtractColumnRef(e.Left, batch)
	if !leftIsCol {
		return evalRowFallback(e, batch, params)
	}
	if !isTextColumn(leftCol) {
		return evalRowFallback(e, batch, params)
	}

	// Extract right pattern as a literal string
	patternLit, isLit := evalLiteral(e.Right, params)
	if !isLit {
		return evalRowFallback(e, batch, params)
	}
	pattern, ok := patternLit.(string)
	if !ok {
		return evalRowFallback(e, batch, params)
	}

	n := batch.Size
	if n == 0 {
		return nil
	}

	data := leftCol.Data.Strs

	// Fast path: no wildcards → exact match (case-insensitive)
	if !strings.Contains(pattern, "%") && !strings.Contains(pattern, "_") {
		sel := getSel(n)
		patternLower := strings.ToLower(pattern)
		for i := 0; i < n; i++ {
			if isNull(leftCol, i) {
				continue
			}
			if i < len(data) && strings.ToLower(data[i]) == patternLower {
				sel = append(sel, uint16(i))
			}
		}
		return sel
	}

	// Fast path: prefix pattern 'prefix%' → HasPrefix (case-insensitive)
	if strings.HasSuffix(pattern, "%") && !strings.Contains(pattern[:len(pattern)-1], "%") && !strings.Contains(pattern, "_") {
		prefix := pattern[:len(pattern)-1]
		prefixLower := strings.ToLower(prefix)
		sel := getSel(n)
		for i := 0; i < n; i++ {
			if isNull(leftCol, i) {
				continue
			}
			if i < len(data) && strings.HasPrefix(strings.ToLower(data[i]), prefixLower) {
				sel = append(sel, uint16(i))
			}
		}
		return sel
	}

	// General path: convert LIKE pattern to regex
	re := likeToRegex(pattern)
	sel := getSel(n)
	for i := 0; i < n; i++ {
		if isNull(leftCol, i) {
			continue
		}
		if i < len(data) && re.MatchString(strings.ToLower(data[i])) {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// likeToRegex converts a SQL LIKE pattern to a case-insensitive regexp.
// % → .*, _ → ., other regex chars are escaped.
func likeToRegex(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		default:
			// Escape regex meta-characters
			if strings.ContainsRune(`\.+*?()|[]{}^$`, rune(ch)) {
				b.WriteByte('\\')
			}
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// evalGlobBatch evaluates col GLOB pattern per row.
// GLOB is case-sensitive (Unix glob rules): * matches any sequence, ? matches any single char.
func evalGlobBatch(e *PS.BinaryExpr, batch *UT.Batch, params []any) []uint16 {
	// Extract left column
	leftCol, leftIsCol := ExtractColumnRef(e.Left, batch)
	if !leftIsCol {
		return evalRowFallback(e, batch, params)
	}
	if !isTextColumn(leftCol) {
		return evalRowFallback(e, batch, params)
	}

	// Extract right pattern as a literal string
	patternLit, isLit := evalLiteral(e.Right, params)
	if !isLit {
		return evalRowFallback(e, batch, params)
	}
	pattern, ok := patternLit.(string)
	if !ok {
		return evalRowFallback(e, batch, params)
	}

	n := batch.Size
	if n == 0 {
		return nil
	}

	data := leftCol.Data.Strs

	// Fast path: no wildcards → exact match (case-sensitive)
	if !strings.Contains(pattern, "*") && !strings.Contains(pattern, "?") {
		sel := getSel(n)
		for i := 0; i < n; i++ {
			if isNull(leftCol, i) {
				continue
			}
			if i < len(data) && data[i] == pattern {
				sel = append(sel, uint16(i))
			}
		}
		return sel
	}

	// Fast path: prefix pattern 'prefix*' → HasPrefix (case-sensitive)
	if strings.HasSuffix(pattern, "*") && !strings.Contains(pattern[:len(pattern)-1], "*") && !strings.Contains(pattern, "?") {
		prefix := pattern[:len(pattern)-1]
		sel := getSel(n)
		for i := 0; i < n; i++ {
			if isNull(leftCol, i) {
				continue
			}
			if i < len(data) && strings.HasPrefix(data[i], prefix) {
				sel = append(sel, uint16(i))
			}
		}
		return sel
	}

	// General path: convert GLOB pattern to regex (case-sensitive)
	re := globToRegex(pattern)
	sel := getSel(n)
	for i := 0; i < n; i++ {
		if isNull(leftCol, i) {
			continue
		}
		if i < len(data) && re.MatchString(data[i]) {
			sel = append(sel, uint16(i))
		}
	}
	return sel
}

// globToRegex converts a SQL GLOB pattern to a case-sensitive regexp.
// * → .*, ? → ., other regex chars are escaped.
func globToRegex(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			// Escape regex meta-characters
			if strings.ContainsRune(`\.+*?()|[]{}^$`, rune(ch)) {
				b.WriteByte('\\')
			}
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// evalBetweenBatch evaluates expr BETWEEN low AND high.
// Returns selection vector where expr >= low AND expr <= high.
// Rows where any of expr/low/high is NULL are excluded.
func evalBetweenBatch(e *PS.BetweenExpr, batch *UT.Batch, params []any) []uint16 {
	n := batch.Size
	if n == 0 {
		return nil
	}

	// Evaluate expr, low, high as columns via EvalBatchExpr
	exprCol := EvalBatchExpr(e.Expr, batch, params)
	lowCol := EvalBatchExpr(e.Low, batch, params)
	highCol := EvalBatchExpr(e.High, batch, params)

	// Determine the comparison type
	typ := exprCol.Type

	sel := getSel(n)
	switch typ {
	case LX.T_INT_KW, LX.T_BIGINT:
		exprInts := exprCol.Data.Ints
		lowInts := lowCol.Data.Ints
		highInts := highCol.Data.Ints
		// Convert float bounds to int if needed
		for i := 0; i < n; i++ {
			if isNull(exprCol, i) || isNull(lowCol, i) || isNull(highCol, i) {
				continue
			}
			var lo, hi int64
			if lowCol.Type == LX.T_FLOAT_KW && i < len(lowCol.Data.Floats) {
				lo = int64(lowCol.Data.Floats[i])
			} else if i < len(lowInts) {
				lo = lowInts[i]
			}
			if highCol.Type == LX.T_FLOAT_KW && i < len(highCol.Data.Floats) {
				hi = int64(highCol.Data.Floats[i])
			} else if i < len(highInts) {
				hi = highInts[i]
			}
			var val int64
			if i < len(exprInts) {
				val = exprInts[i]
			}
			if val >= lo && val <= hi {
				sel = append(sel, uint16(i))
			}
		}
	case LX.T_FLOAT_KW:
		exprFloats := exprCol.Data.Floats
		for i := 0; i < n; i++ {
			if isNull(exprCol, i) || isNull(lowCol, i) || isNull(highCol, i) {
				continue
			}
			var val, lo, hi float64
			if i < len(exprFloats) {
				val = exprFloats[i]
			}
			switch lowCol.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				if i < len(lowCol.Data.Ints) {
					lo = float64(lowCol.Data.Ints[i])
				}
			case LX.T_FLOAT_KW:
				if i < len(lowCol.Data.Floats) {
					lo = lowCol.Data.Floats[i]
				}
			}
			switch highCol.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				if i < len(highCol.Data.Ints) {
					hi = float64(highCol.Data.Ints[i])
				}
			case LX.T_FLOAT_KW:
				if i < len(highCol.Data.Floats) {
					hi = highCol.Data.Floats[i]
				}
			}
			if val >= lo && val <= hi {
				sel = append(sel, uint16(i))
			}
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		exprStrs := exprCol.Data.Strs
		for i := 0; i < n; i++ {
			if isNull(exprCol, i) || isNull(lowCol, i) || isNull(highCol, i) {
				continue
			}
			var val, lo, hi string
			if i < len(exprStrs) {
				val = exprStrs[i]
			}
			switch lowCol.Type {
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				if i < len(lowCol.Data.Strs) {
					lo = lowCol.Data.Strs[i]
				}
			}
			switch highCol.Type {
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				if i < len(highCol.Data.Strs) {
					hi = highCol.Data.Strs[i]
				}
			}
			if val >= lo && val <= hi {
				sel = append(sel, uint16(i))
			}
		}
	default:
		// Unsupported type: fallback
		return evalRowFallback(e, batch, params)
	}
	return sel
}

// ── REQ002031: IS/LIKE/GLOB batch expression (column-returning) kernels ──

// evalIsBatchExpr evaluates T_IS expressions over a batch, returning a
// boolean column (for EvalBatchExpr path). REQ002031.
func evalIsBatchExpr(e *PS.BinaryExpr, batch *UT.Batch, params []any) UT.Column {
	n := batch.Size
	if n == 0 {
		return UT.Column{Type: LX.T_BOOL}
	}
	out := UT.Column{
		Name: "",
		Type: LX.T_BOOL,
		Data: UT.ColumnData{Bools: make([]bool, n)},
	}

	// Check if right side is NullLiteral → IS NULL
	if _, ok := e.Right.(*PS.NullLiteral); ok {
		leftCol, leftIsCol := ExtractColumnRef(e.Left, batch)
		if !leftIsCol {
			return evalRowFallbackColumn(e, batch, params)
		}
		for i := 0; i < n; i++ {
			if isNull(leftCol, i) {
				out.Data.Bools[i] = true
			}
		}
		return out
	}

	// Check if right side is UnaryExpr{T_NOT, NullLiteral} → IS NOT NULL
	if u, ok := e.Right.(*PS.UnaryExpr); ok && u.Op == LX.T_NOT {
		if _, ok := u.Operand.(*PS.NullLiteral); ok {
			leftCol, leftIsCol := ExtractColumnRef(e.Left, batch)
			if !leftIsCol {
				return evalRowFallbackColumn(e, batch, params)
			}
			for i := 0; i < n; i++ {
				if !isNull(leftCol, i) {
					out.Data.Bools[i] = true
				}
			}
			return out
		}
	}

	// Fallback for other IS expressions
	return evalRowFallbackColumn(e, batch, params)
}

// evalLikeBatchExpr evaluates T_LIKE expressions over a batch, returning a
// boolean column (for EvalBatchExpr path). REQ002031.
func evalLikeBatchExpr(e *PS.BinaryExpr, batch *UT.Batch, params []any) UT.Column {
	sel := evalLikeBatch(e, batch, params)
	n := batch.Size
	out := UT.Column{
		Name: "",
		Type: LX.T_BOOL,
		Data: UT.ColumnData{Bools: make([]bool, n)},
	}
	if sel == nil {
		for i := 0; i < n; i++ {
			out.Data.Bools[i] = true
		}
	} else {
		for _, idx := range sel {
			out.Data.Bools[idx] = true
		}
	}

	// Mark NULL rows (left column is NULL) as NULL in output
	leftCol, leftIsCol := ExtractColumnRef(e.Left, batch)
	if leftIsCol {
		for i := 0; i < n; i++ {
			if isNull(leftCol, i) {
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

// evalGlobBatchExpr evaluates T_GLOB expressions over a batch, returning a
// boolean column (for EvalBatchExpr path). REQ002031.
func evalGlobBatchExpr(e *PS.BinaryExpr, batch *UT.Batch, params []any) UT.Column {
	sel := evalGlobBatch(e, batch, params)
	n := batch.Size
	out := UT.Column{
		Name: "",
		Type: LX.T_BOOL,
		Data: UT.ColumnData{Bools: make([]bool, n)},
	}
	if sel == nil {
		for i := 0; i < n; i++ {
			out.Data.Bools[i] = true
		}
	} else {
		for _, idx := range sel {
			out.Data.Bools[idx] = true
		}
	}

	// Mark NULL rows (left column is NULL) as NULL in output
	leftCol, leftIsCol := ExtractColumnRef(e.Left, batch)
	if leftIsCol {
		for i := 0; i < n; i++ {
			if isNull(leftCol, i) {
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
