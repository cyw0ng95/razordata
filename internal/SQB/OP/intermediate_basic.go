package OP

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// pollution when one query's compiled closure referenced a stale
// row.ColIndex.
var predicateCache sync.Map

// REQ001231: FilterProject fuses Filter + Project into a single operator
// so every row goes through one virtual call (Next) instead of two
// (Filter.Next + Project.Next). The predicate is evaluated first; if it
// passes, the projection expressions are evaluated on the same input row.
type FilterProject struct {
	child     Operator
	predicate PS.Expr
	cols      []PS.Expr
	params    []any
	execCtx   *pl.ExecContext

	// Compiled predicate function (from Filter)
	compiledFilterFn func(*Row) (bool, error)
	compiledOnce     bool

	// Compiled projection expressions (from Project)
	compiledExprs []func(in *Row) Value
	prefixCols    []string
	prefixTypes   []LX.TokenType // REQ001184: output row type metadata
	colIndex      map[string]int
	dataBuf       []Value
	dataPerRow    int

	closed atomic.Bool
}

func (fp *FilterProject) Child() Operator               { return fp.child }
func (fp *FilterProject) SetChild(c Operator)           { fp.child = c }
func (fp *FilterProject) SetExecCtx(ec *pl.ExecContext) { fp.execCtx = ec }
func (fp *FilterProject) Predicate() PS.Expr            { return fp.predicate }
func (fp *FilterProject) Cols() []PS.Expr               { return fp.cols }

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (fp *FilterProject) WithParams(p []any) Operator {
	fp.params = p
	return fp
}

// NewFilterProject creates a fused Filter-Project operator.
func NewFilterProject(child Operator, predicate PS.Expr, cols []PS.Expr) *FilterProject {
	// Pre-compute column names once (same for every row).
	prefixCols := make([]string, len(cols))
	for i, c := range cols {
		var name string
		switch e := c.(type) {
		case *PS.Ident:
			name = e.Name
		case *PS.QualifiedName:
			name = e.Table + "." + e.Name
		case *PS.AliasedExpr:
			if inner, ok := e.Expr.(*PS.Ident); ok {
				name = inner.Name
			}
			if name == "" {
				name = e.Alias
			}
		case *PS.UnaryExpr:
			if inner, ok := e.Operand.(*PS.Ident); ok {
				name = inner.Name
			}
		case *PS.WindowFunc:
			name = e.Name
		}
		if a, ok := c.(*PS.AliasedExpr); ok {
			name = a.Alias
		}
		prefixCols[i] = name
	}
	colIndex := make(map[string]int, len(prefixCols))
	for i, c := range prefixCols {
		colIndex[c] = i
	}
	return &FilterProject{
		child:      child,
		predicate:  predicate,
		cols:       cols,
		prefixCols: prefixCols,
		colIndex:   colIndex,
		dataPerRow: len(cols),
	}
}

func (fp *FilterProject) Next(ctx context.Context) (Row, error) {
	ec.BUG_ON(fp.closed.Load(), "FilterProject.Next() after Close()")
	for {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		row, err := fp.child.Next(ctx)
		if err != nil {
			return Row{}, err
		}
		if fp.execCtx != nil {
			row.ExecCtx = fp.execCtx
		}

		// Evaluate predicate.
		if fp.predicate != nil {
			if !fp.compiledOnce {
				fp.compiledFilterFn = compileFilterExpr(fp.predicate)
				fp.compiledOnce = true
			}
			if fp.compiledFilterFn != nil {
				ok, err := fp.compiledFilterFn(&row)
				if err != nil {
					return Row{}, err
				}
				if !ok {
					continue
				}
			} else {
				// Fallback to Eval-based predicate.
				v, err := EV.EvalValue(fp.predicate, &row, fp.params)
				if err != nil {
					return Row{}, err
				}
				if !DT.IsValueTruthy(v) {
					continue
				}
			}
		}

		// Evaluate projection expressions.
		if fp.compiledExprs == nil {
			fp.compiledExprs = make([]func(*Row) Value, len(fp.cols))
			for i, c := range fp.cols {
				fp.compiledExprs[i] = compileRowExpr(c)
			}
		}
		if fp.dataBuf == nil {
			fp.dataPerRow = len(fp.cols)
			if fp.dataPerRow == 0 {
				fp.dataPerRow = 1
			}
			fp.dataBuf = make([]Value, 0, 64*fp.dataPerRow)
		}
		off := len(fp.dataBuf)
		required := off + fp.dataPerRow
		if cap(fp.dataBuf) < required {
			newCap := cap(fp.dataBuf) * 2
			if newCap < required {
				newCap = required
			}
			fp.dataBuf = append(fp.dataBuf, make([]Value, required-off)...)
		} else {
			fp.dataBuf = fp.dataBuf[:required]
		}
	dataSlice := fp.dataBuf[off : off+fp.dataPerRow : off+fp.dataPerRow]
		out := Row{
			Cols:     fp.prefixCols,
			Data:     dataSlice,
			ColIndex: fp.colIndex,
		}
		for i, c := range fp.cols {
			fn := fp.compiledExprs[i]
			if fn != nil {
				out.Data[i] = fn(&row)
				continue
			}
			var v any
			var err error
			if wf, ok := c.(*PS.WindowFunc); ok {
				v, err = findColumn(row, wf.Name)
				if err != nil {
					return Row{}, err
				}
			} else {
				v, err = EV.EvalValue(c, &row, fp.params)
				if err != nil {
					return Row{}, err
				}
			}
			out.Data[i] = v.(Value)
		}
		// REQ001184: infer and cache types from evaluated data.
		if fp.prefixTypes == nil {
			fp.prefixTypes = make([]LX.TokenType, len(fp.cols))
		}
		for i := range out.Data {
			if fp.prefixTypes[i] == 0 {
				fp.prefixTypes[i] = inferProjectType(out.Data[i].ToAny())
			}
		}
		out.Types = fp.prefixTypes
		return out, nil
	}
}

func (fp *FilterProject) Close() error {
	fp.closed.Store(true)
	fp.dataBuf = nil
	fp.dataPerRow = 0
	return fp.child.Close()
}

// Reset reinitializes FilterProject cursor state. Preserves compiled
// filter and expression fns. Does NOT close the child. REQ001464.
func (fp *FilterProject) Reset(ctx context.Context) error {
	fp.dataBuf = fp.dataBuf[:0]
	fp.dataPerRow = 0
	return nil
}

// CountComparisonLiterals returns how many comparison-literal values
// this FilterProject's predicate would consume when normalized. REQ001231.
func (fp *FilterProject) CountComparisonLiterals() int {
	var count int
	countComparisonLiteralsWalk(fp.predicate, &count)
	return count
}

// ReplaceLiterals replaces comparison-literal values in the predicate
// with values from vals. Forces recompilation of the predicate on the
// next Next() call. REQ001231.
func (fp *FilterProject) ReplaceLiterals(vals []any) {
	n := fp.CountComparisonLiterals()
	if n > len(vals) {
		n = len(vals)
	}
	remaining := vals[:n]
	fp.predicate = replaceComparisonLiterals(fp.predicate, &remaining)
	fp.compiledOnce = false
	fp.compiledFilterFn = nil
}

// REQ000869: batchBufPool reuses []Row backing arrays across Filter
// instances. Filters are created per query and hold batchBuf/batchEmit
// slices; returning them to this pool in Close() allows the next query's
// Filter to reuse the capacity instead of re-allocating.
var batchBufPool = sync.Pool{
	New: func() any {
		b := make([]Row, 0, filterBatchSize)
		return &b
	},
}

// REQ001091: projectDataBufPool reuses Project.dataBuf slices across
// queries. Each Project carves a non-overlapping sub-slice [off:off:off+dataPerRow]
// from dataBuf for every output row. Allocating a fresh 64*dataPerRow
// buffer per query dominates allocations in workloads like select4
// (~3857 allocs per run); pooling cuts the per-query allocation to
// zero when the pool is warm. Chunk size 64*8=512 values matches the
// common 8-column output of REQ000802's Project fast-path.
var projectDataBufPool = sync.Pool{
	New: func() any {
		b := make([]Value, 0, projectDataBufChunkSize)
		return &b
	},
}

// projectDataBufChunkSize is the initial capacity (in values) of a
// pooled Project.dataBuf. 512 values covers 64 rows × 8 cols which is
// the modal Project output shape. REQ001091.
const projectDataBufChunkSize = 512

// REQ001707: pooled backing arrays for prefixCols, compiledExprs, and
// fnArgBuf so that repeated Project lifecycles (especially for
// parameterized queries that miss memo cache) avoid repeated make()
// allocations on the heap.  The pool capacity is sized to handle
// typical wide projections (up to 512 columns); narrower Projects
// still benefit from zero-allocation reuse within their current cap.
var projectPrefixColsPool = sync.Pool{
	New: func() any {
		s := make([]string, 0, 256)
		return &s
	},
}

var projectCompiledExprsPool = sync.Pool{
	New: func() any {
		s := make([]func(*Row) (Value, error), 0, 32)
		return &s
	},
}

var projectFnArgBufPool = sync.Pool{
	New: func() any {
		s := make([]any, 0, 32)
		return &s
	},
}

// isNullValue checks if a value represents SQL NULL.
// Handles both raw nil and Value{Kind: KindNull}.
func isNullValue(v any) bool {
	if v == nil {
		return true
	}
	// Check if it's a Value type with KindNull.
	if val, ok := v.(Value); ok {
		return val.IsNull()
	}
	return false
}

// isNullValueValue is a Value-typed variant that avoids interface boxing.
// REQ000871: used in hot paths where v is known to be Value.
func isNullValueValue(v Value) bool { return v.IsNull() }

type Filter struct {
	child     Operator
	predicate PS.Expr
	params    []any
	execCtx   *pl.ExecContext
	// REQ000757: curRow avoids heap-escape of local row variable
	// when passing &row to Eval. Filter is heap-allocated, so
	// &f.curRow is already a heap pointer — no escape needed.
	curRow Row
	// REQ000802: compiledFilterFn is a specialized predicate function
	// compiled on first use. It bypasses Eval dispatch overhead.
	compiledFilterFn func(*Row) (bool, error)
	compiledOnce     bool
	// REQ000822: batch mode buffers a slab of input rows, evaluates
	// the compiled predicate in a tight loop, and emits matching rows
	// on subsequent Next() calls. This reduces per-row function-call
	// overhead and improves cache locality for selective predicates
	// on cross products (select4 multi-table joins). Falls back to
	// the original per-row loop when the predicate is uncompiled
	// (falls back to Eval).
	batchBuf      []Row
	batchEmit     []Row
	batchEmitPos  int
	batchRefilled bool
	rowID         uint64 // debug: tracks rows through filter
	// REQ001277: only check ctx.Err() every 64 iterations to reduce
	// overhead on the per-row fallback path. Mirrors the pattern in
	// SeqScan (REQ001042) which batches at 1024. The fallback path
	// is already slow (no compiled predicate), so a coarser batch
	// is safe and avoids ~10K goroutine-scheduled checks per select1.
	ctxCheckCounter uint64
	// REQ001567: reusable buffer for function call argument allocation.
	fnArgBuf []any

	closed atomic.Bool
}

// Child returns the filter's child operator. Used by
// propagateParams to walk the operator tree.
func (f *Filter) Child() Operator               { return f.child }
func (f *Filter) SetChild(c Operator)           { f.child = c }
func (f *Filter) SetExecCtx(ec *pl.ExecContext) { f.execCtx = ec }

// Predicate returns the filter's predicate expression.
func (f *Filter) Predicate() PS.Expr { return f.predicate }

func NewFilter(child Operator, predicate PS.Expr, schema *DT.StoreSchema) *Filter {
	// REQ001225: if schema is nil, try to get it from the child operator.
	if schema == nil {
		schema = operatorSchema(child)
	}
	if schema != nil {
		rawFilter := CompileRawByteFilter(predicate, schema)
		if rawFilter != nil {
			switch c := child.(type) {
			case *SeqScan:
				c.SetRawByteFilter(rawFilter)
			case *IndexScan:
				c.SetRawByteFilter(rawFilter)
			}
		}
	}

	// REQ000869: try to reuse batch buffers from the pool.
	buf, _ := batchBufPool.Get().(*[]Row)
	emit, _ := batchBufPool.Get().(*[]Row)
	if buf == nil {
		buf = new([]Row)
		*buf = make([]Row, 0, filterBatchSize)
	}
	if emit == nil {
		emit = new([]Row)
		*emit = make([]Row, 0, filterBatchSize)
	}
	return &Filter{
		child:     child,
		predicate: predicate,
		// REQ000868: pre-allocate batch buffers to avoid first-call
		// allocation in refillBatch.
		batchBuf:  *buf,
		batchEmit: *emit,
		// REQ001567: pre-allocate function call argument buffer.
		fnArgBuf: make([]any, 0, 10),
	}
}

// operatorSchema extracts the *DT.StoreSchema from an operator,
// walking through Filter/Project/Sort wrappers to find the underlying scan.
func operatorSchema(op Operator) *DT.StoreSchema {
	switch o := op.(type) {
	case *SeqScan:
		return o.Schema()
	case *IndexScan:
		return o.Schema()
	case *Filter:
		return operatorSchema(o.Child())
	case *FilterProject:
		return operatorSchema(o.Child())
	case *Project:
		return operatorSchema(o.Child())
	case *Sort:
		return operatorSchema(o.Child())
	case *Limit:
		return operatorSchema(o.Child())
	}
	return nil
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (f *Filter) WithParams(p []any) Operator {
	f.params = p
	return f
}

// isComparisonOp reports whether op is a comparison operator. REQ001195.
func isComparisonOp(op LX.TokenType) bool {
	return op == LX.T_EQ || op == LX.T_NE || op == LX.T_LT || op == LX.T_LE || op == LX.T_GT || op == LX.T_GE
}

// isColumnRef reports whether expr is a column reference. REQ001195.
func isColumnRef(expr PS.Expr) bool {
	_, ok := expr.(*PS.Ident)
	if ok {
		return true
	}
	_, ok = expr.(*PS.QualifiedName)
	return ok
}

// isLiteralNode reports whether expr is a literal value. REQ001195.
func isLiteralNode(expr PS.Expr) bool {
	switch expr.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral, *PS.BoolLiteral:
		return true
	}
	return false
}

// anyToLiteral converts a Go value back to a PS literal AST node. REQ001195.
func anyToLiteral(val any) PS.Expr {
	switch v := val.(type) {
	case int64:
		return &PS.NumberLiteral{Val: v}
	case float64:
		return &PS.FloatLiteral{Val: v}
	case string:
		return &PS.StringLiteral{Val: v}
	case bool:
		return &PS.BoolLiteral{Val: v}
	case nil:
		return &PS.NullLiteral{}
	}
	return &PS.NullLiteral{}
}

// replaceComparisonLiterals walks expr and replaces literal values at
// comparison positions (column OP literal) with values from vals in
// tree-walk order. Must match the walk order of cloneExprForMemo in
// PL/memo.go. REQ001195.
func replaceComparisonLiterals(expr PS.Expr, vals *[]any) PS.Expr {
	if expr == nil || len(*vals) == 0 {
		return expr
	}
	switch v := expr.(type) {
	case *PS.BinaryExpr:
		if isComparisonOp(v.Op) {
			if isColumnRef(v.Left) && isLiteralNode(v.Right) && len(*vals) > 0 {
				val := (*vals)[0]
				*vals = (*vals)[1:]
				return &PS.BinaryExpr{Op: v.Op, Left: v.Left, Right: anyToLiteral(val)}
			}
			if isLiteralNode(v.Left) && isColumnRef(v.Right) && len(*vals) > 0 {
				val := (*vals)[0]
				*vals = (*vals)[1:]
				return &PS.BinaryExpr{Op: v.Op, Left: anyToLiteral(val), Right: v.Right}
			}
		}
		return &PS.BinaryExpr{Op: v.Op, Left: replaceComparisonLiterals(v.Left, vals), Right: replaceComparisonLiterals(v.Right, vals)}
	case *PS.InExpr:
		items := make([]PS.Expr, len(v.List))
		for i, item := range v.List {
			items[i] = replaceComparisonLiterals(item, vals)
		}
		return &PS.InExpr{Expr: replaceComparisonLiterals(v.Expr, vals), List: items, Subquery: v.Subquery}
	case *PS.BetweenExpr:
		return &PS.BetweenExpr{Expr: replaceComparisonLiterals(v.Expr, vals), Low: replaceComparisonLiterals(v.Low, vals), High: replaceComparisonLiterals(v.High, vals)}
	case *PS.UnaryExpr:
		return &PS.UnaryExpr{Op: v.Op, Operand: replaceComparisonLiterals(v.Operand, vals)}
	case *PS.FunctionCall:
		args := make([]PS.Expr, len(v.Args))
		for i, a := range v.Args {
			args[i] = replaceComparisonLiterals(a, vals)
		}
		return &PS.FunctionCall{Name: v.Name, Args: args}
	case *PS.CaseExpr:
		wl := make([]PS.WhenClause, len(v.WhenList))
		for i, w := range v.WhenList {
			wl[i] = PS.WhenClause{Cond: replaceComparisonLiterals(w.Cond, vals), Then: replaceComparisonLiterals(w.Then, vals)}
		}
		return &PS.CaseExpr{Expr: replaceComparisonLiterals(v.Expr, vals), WhenList: wl, Else: replaceComparisonLiterals(v.Else, vals)}
	case *PS.CastExpr:
		return &PS.CastExpr{Expr: replaceComparisonLiterals(v.Expr, vals), Type: v.Type}
	case *PS.ListExpr:
		items := make([]PS.Expr, len(v.Items))
		for i, item := range v.Items {
			items[i] = replaceComparisonLiterals(item, vals)
		}
		return &PS.ListExpr{Items: items}
	case *PS.AliasedExpr:
		return &PS.AliasedExpr{Expr: replaceComparisonLiterals(v.Expr, vals), Alias: v.Alias}
	}
	return expr
}

// countComparisonLiteralsWalk counts comparison-literal positions in expr. REQ001195.
func countComparisonLiteralsWalk(expr PS.Expr, count *int) {
	if expr == nil {
		return
	}
	switch v := expr.(type) {
	case *PS.BinaryExpr:
		if isComparisonOp(v.Op) {
			if (isColumnRef(v.Left) && isLiteralNode(v.Right)) || (isLiteralNode(v.Left) && isColumnRef(v.Right)) {
				*count++
				return
			}
		}
		countComparisonLiteralsWalk(v.Left, count)
		countComparisonLiteralsWalk(v.Right, count)
	case *PS.InExpr:
		countComparisonLiteralsWalk(v.Expr, count)
		for _, item := range v.List {
			countComparisonLiteralsWalk(item, count)
		}
	case *PS.BetweenExpr:
		countComparisonLiteralsWalk(v.Expr, count)
		countComparisonLiteralsWalk(v.Low, count)
		countComparisonLiteralsWalk(v.High, count)
	case *PS.UnaryExpr:
		countComparisonLiteralsWalk(v.Operand, count)
	case *PS.FunctionCall:
		for _, a := range v.Args {
			countComparisonLiteralsWalk(a, count)
		}
	case *PS.CaseExpr:
		countComparisonLiteralsWalk(v.Expr, count)
		for _, w := range v.WhenList {
			countComparisonLiteralsWalk(w.Cond, count)
			countComparisonLiteralsWalk(w.Then, count)
		}
		countComparisonLiteralsWalk(v.Else, count)
	}
}

// CountComparisonLiterals returns how many comparison-literal values
// this Filter's predicate would consume when normalized. REQ001195.
func (f *Filter) CountComparisonLiterals() int {
	var count int
	countComparisonLiteralsWalk(f.predicate, &count)
	return count
}

// ReplaceLiterals replaces comparison-literal values in the predicate
// with values from vals. Only the first n values are consumed where
// n = CountComparisonLiterals(). REQ001195.
func (f *Filter) ReplaceLiterals(vals []any) {
	n := f.CountComparisonLiterals()
	if n > len(vals) {
		n = len(vals)
	}
	remaining := vals[:n]
	f.predicate = replaceComparisonLiterals(f.predicate, &remaining)
	// Force recompile on next Next() call
	f.compiledOnce = false
	f.compiledFilterFn = nil
	// REQ000822: clear batch buffer so stale compiled rows are not reused
	f.batchEmit = f.batchEmit[:0]
	f.batchEmitPos = 0
	f.batchRefilled = false
}

func (f *Filter) Next(ctx context.Context) (Row, error) {
	ec.BUG_ON(f.closed.Load(), "Filter.Next() after Close()")
	for {
		// REQ001277: batch ctx.Err() check — skip on most iterations.
		f.ctxCheckCounter++
		if f.ctxCheckCounter >= 64 {
			f.ctxCheckCounter = 0
			if err := ctx.Err(); err != nil {
				return Row{}, err
			}
		}
		if f.predicate == nil {
			r, err := f.child.Next(ctx)
			if err != nil {
				return Row{}, err
			}
			if f.execCtx != nil {
				r.ExecCtx = f.execCtx
			}
			return r, nil
		}
		// REQ000802: use compiled predicate if available.
		// REQ000802+: check global predicate cache for reuse.
		// REQ001088: compile per-Filter instead. Compiled closures
		// may reference row.ColIndex which is per-row, so a global
		// cache is only safe for compileFilterExpr results that
		// are pure (no row state). For simplicity, all compilation
		// is now per-Filter via compiledOnce.
		if !f.compiledOnce {
			f.compiledFilterFn = compileFilterExpr(f.predicate)
			f.compiledOnce = true
		}
		// REQ000822: batch path — drain up to filterBatchSize rows
		// from the child, run the compiled predicate in a tight
		// loop, and buffer the matches. Subsequent Next() calls
		// emit from the buffer until exhausted, then refill. This
		// is the dominant path for selective predicates on cross
		// products where child.Next() is expensive (e.g. NLJ
		// inner scan). Only enabled when a compiled predicate is
		// available — uncompiled predicates fall through to the
		// per-row Eval path below.
		if f.compiledFilterFn != nil {
			if f.batchEmitPos >= len(f.batchEmit) {
				if err := f.refillBatch(ctx); err != nil {
					return Row{}, err
				}
			}
			if len(f.batchEmit) == 0 {
				// Child exhausted and nothing matched.
				return Row{}, ErrNoRows
			}
			r := f.batchEmit[f.batchEmitPos]
			f.batchEmitPos++
			if f.execCtx != nil {
				r.ExecCtx = f.execCtx
			}
			return r, nil
		}
		// Fallback to Eval-based path (per-row).
		r, err := f.child.Next(ctx)
		if err != nil {
			return Row{}, err
		}
		if f.execCtx != nil {
			r.ExecCtx = f.execCtx
		}
		f.curRow = r
		v, err := EV.EvalValue(f.predicate, &f.curRow, f.params)
		if err != nil {
			return Row{}, err
		}
		passed := DT.IsValueTruthy(v)
		f.rowID++
		if passed {
			return f.curRow, nil
		}
	}
}

// filterBatchSize is the slab size used by REQ000822's batch Filter.
// 1024 rows is a sweet spot: large enough to amortize child.Next()
// overhead and to keep the compiled-predicate loop cache-friendly,
// small enough to avoid excess memory pressure on selective
// predicates that return only a handful of matches. Tuned against
// the select4 6-table cross-product profile where a 100×100×100
// join is the typical workload.
const filterBatchSize = 2048

// refillBatch pulls up to filterBatchSize rows from the child,
// evaluates the compiled predicate in a tight loop, and stages the
// matches in f.batchEmit. Returns ErrNoRows when the child is
// exhausted and nothing matched.
func (f *Filter) refillBatch(ctx context.Context) error {
	f.batchEmit = f.batchEmit[:0]
	f.batchEmitPos = 0
	if cap(f.batchBuf) < filterBatchSize {
		f.batchBuf = make([]Row, 0, filterBatchSize)
	} else {
		f.batchBuf = f.batchBuf[:0]
	}
	for len(f.batchBuf) < filterBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, err := f.child.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return err
		}
		if f.execCtx != nil {
			r.ExecCtx = f.execCtx
		}
		// REQ001408: clone Data eagerly before appending to
		// batchBuf. Some child operators (IndexScan, See
		// SQB/DT/storage.go PoolValueSlice) return a row whose
		// Data slice is reused across Next() calls via the
		// valueSlicePool. Without this clone, the next
		// child.Next() would overwrite the underlying array
		// and corrupt every entry currently in batchBuf,
		// causing the predicate evaluation below to see the
		// same (last) row's values for every slot. SLT
		// select4.test L39784 reproduces this: 14 rows returned
		// instead of 21, hash b752b9c6... vs expected
		// 34325f84dd0efa600c0be4e8e0770bc3.
		if r.Data != nil {
			r.Data = append([]DT.Value(nil), r.Data...)
		}
		// REQ001583: deep-copy StoreKey — it aliases the SeqScan's
		// internal iterator buffer which is only valid until the
		// next SeqScan.Next() call. Without the copy, every row
		// in batchBuf shares the same StoreKey backing array and
		// gets silently overwritten on the next Next().
		if r.StoreKey != nil {
			sk := make([]byte, len(r.StoreKey))
			copy(sk, r.StoreKey)
			r.StoreKey = sk
		}
		f.batchBuf = append(f.batchBuf, r)
	}
	// Tight loop: predicate evaluation, no per-row Eval dispatch.
	// REQ000822: emit row must be a stable copy — refillBatch()
	// reuses f.batchBuf's backing array, so storing raw Row values
	// into batchEmit would alias memory the next refill will
	// overwrite. cloneRow produces an independent Row whose Data
	// slice does not share storage with the batch buffer.
	//
	// REQ000869: share Cols/Types across cloned rows. All rows in
	// batchBuf come from the same child operator and have identical
	// Cols and Types. Copying them per-row wastes ~66% of cloneRow's
	// allocation budget. Instead, capture the shared slices from the
	// first row and reuse them for all subsequent clones.
	var sharedCols []string
	var sharedTypes []LX.TokenType
	for i := range f.batchBuf {
		ok, err := f.compiledFilterFn(&f.batchBuf[i])
		if err != nil {
			return err
		}
		if ok {
			r := f.batchBuf[i]
			if sharedCols == nil {
				// First match — capture the shared Cols/Types.
				sharedCols = append([]string(nil), r.Cols...)
				sharedTypes = append([]LX.TokenType(nil), r.Types...)
			}
			r.Cols = sharedCols
			r.Types = sharedTypes
			if f.execCtx != nil {
				if arena, ok := f.execCtx.RowArena.(*DT.RowArena); ok && arena != nil {
					// REQ001233: batch clone row via arena instead of per-row alloc
					cloned := arena.CloneRowsBatch([]DT.Row{r})
					// REQ001583: CloneRowsBatch does not preserve StoreKey.
					// Copy it from the source row so ExtractPKForUpdate can
					// use it for hidden-PK UPDATE key generation.
					for i := range cloned {
						cloned[i].StoreKey = r.StoreKey
					}
					f.batchEmit = append(f.batchEmit, cloned...)
					continue
				}
			}
			if r.Data != nil {
			r.Data = append([]Value(nil), r.Data...)
		}
		// REQ001583: deep-copy StoreKey — it aliases the SeqScan's
		// internal iterator buffer which is only valid until the
		// next Next() call. Without the copy, ExtractPKForUpdate
		// sees an empty StoreKey and allocates a new synthetic
		// rowid for every UPDATE, creating new rows instead of
		// overwriting old ones.
		if r.StoreKey != nil {
			sk := make([]byte, len(r.StoreKey))
			copy(sk, r.StoreKey)
			r.StoreKey = sk
		}
		f.batchEmit = append(f.batchEmit, r)
		}
	}
	return nil
}

func (f *Filter) Close() error {
	f.closed.Store(true)
	// REQ000869: return batch buffers to the pool for reuse.
	if cap(f.batchBuf) >= filterBatchSize {
		f.batchBuf = f.batchBuf[:0]
		batchBufPool.Put(&f.batchBuf)
	}
	if cap(f.batchEmit) >= filterBatchSize {
		f.batchEmit = f.batchEmit[:0]
		batchBufPool.Put(&f.batchEmit)
	}
	return f.child.Close()
}

// Reset reinitializes Filter cursor state. Reuses batch buffers in-place
// instead of returning them to the pool. Does NOT close the child.
// REQ001464.
func (f *Filter) Reset(ctx context.Context) error {
	f.batchBuf = f.batchBuf[:0]
	f.batchEmit = f.batchEmit[:0]
	f.batchEmitPos = 0
	f.batchRefilled = false
	f.ctxCheckCounter = 0
	f.curRow = Row{}
	return nil
}

type Project struct {
	child     Operator
	cols      []PS.Expr
	params    []any
	prefixCols  []string
	prefixTypes []LX.TokenType // REQ001184: output row type metadata
	colIndex    map[string]int
	compiledExprs  []func(in *Row) (Value, error)
	dataBuf         []Value
	dataPerRow      int
	execCtx    *pl.ExecContext
	// REQ001567: reusable buffer for function call argument allocation.
	fnArgBuf []any
	closed    atomic.Bool
}

func (p *Project) Child() Operator               { return p.child }
func (p *Project) SetChild(c Operator)           { p.child = c }
func (p *Project) Cols() []PS.Expr               { return p.cols }
func (p *Project) SetExecCtx(ec *pl.ExecContext) { p.execCtx = ec }

// ExecCtx returns the ExecContext attached to this operator, used by
// transformOp to forward execution state to the vectorized equivalent
// (REQ001460).
func (p *Project) ExecCtx() *pl.ExecContext { return p.execCtx }

func NewProject(child Operator, cols []PS.Expr) *Project {
	// REQ001091: acquire the data buffer from the pool so concurrent
	// Projects share a backing array across queries. If the pool is
	// empty or returns the wrong type, fall back to a fresh allocation.
	dataBufPtr, _ := projectDataBufPool.Get().(*[]Value)
	var dataBuf []Value
	if dataBufPtr != nil {
		dataBuf = (*dataBufPtr)[:0]
	} else {
		dataBufPtr = new([]Value)
	}
	// REQ001288: pre-allocate to hold at least 1024 rows worth of data
	// to avoid growth allocations for typical queries. The pool provides
	// capacity 512, which may be insufficient for larger result sets.
	dataPerRow := len(cols)
	preallocSize := dataPerRow * 1024
	if preallocSize < projectDataBufChunkSize {
		preallocSize = projectDataBufChunkSize
	}
	if cap(dataBuf) < preallocSize {
		dataBuf = make([]Value, 0, preallocSize)
	}
	// REQ001707: acquire prefixCols from pool to avoid repeated
	// make([]string, N) heap allocations across queries. Resize the
	// pooled backing array in-place so its cap >= len(cols).
	prefixColsRaw := projectPrefixColsPool.Get().(*[]string)
	if cap(*prefixColsRaw) < len(cols) {
		*prefixColsRaw = make([]string, 0, len(cols))
	}
	prefixCols := (*prefixColsRaw)[:len(cols):len(cols)]
	for i, c := range cols {
		var name string
		switch e := c.(type) {
		case *PS.Ident:
			name = e.Name
		case *PS.QualifiedName:
			// REQ000720: render qualified name as "table.col"
			// so the projected column matches what callers
			// expect when the query uses a table alias.
			name = e.Table + "." + e.Name
		case *PS.AliasedExpr:
			if inner, ok := e.Expr.(*PS.Ident); ok {
				name = inner.Name
			}
			if name == "" {
				name = e.Alias
			}
		case *PS.UnaryExpr:
			if inner, ok := e.Operand.(*PS.Ident); ok {
				name = inner.Name
			}
		case *PS.WindowFunc:
			name = e.Name
		}
		if a, ok := c.(*PS.AliasedExpr); ok {
			name = a.Alias
		}
		prefixCols[i] = name
	}
	// REQ000816: build colIndex once. All prefixCols are
	// already lowercase (parser lowercases at parse time per
	// REQ000770).
	colIndex := make(map[string]int, len(prefixCols))
	for i, c := range prefixCols {
		colIndex[c] = i
	}
	return &Project{
		child:      child,
		cols:       cols,
		prefixCols: prefixCols,
		colIndex:   colIndex,
		dataBuf:    dataBuf,
		// REQ001091: dataPerRow is now set in NewProject so the lazy-init
		// branch in Next is a no-op for the pool-acquired case.
		dataPerRow: len(cols),
		// REQ001707: acquire fnArgBuf from pool to avoid repeated
		// make([]any, 0, 10) allocations across queries.
		fnArgBuf: func() []any {
			buf := projectFnArgBufPool.Get().(*[]any)
			return (*buf)[:0:cap(*buf)]
		}(),
	}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (p *Project) WithParams(p2 []any) Operator {
	p.params = p2
	return p
}

func (p *Project) Next(ctx context.Context) (Row, error) {
	ec.BUG_ON(p.closed.Load(), "Project.Next() after Close()")
	row, err := p.child.Next(ctx)
	if err != nil {
		return Row{}, err
	}
	if p.execCtx != nil {
		row.ExecCtx = p.execCtx
	}
	if isStar(p.cols) {
		return row, nil
	}
	// REQ000802: compile expressions on first use, then use
	// fast-path evaluators that bypass Eval dispatch.
	if p.compiledExprs == nil {
		p.compileProjectExprs()
	}
	// REQ000802+: use pre-allocated data buffer to eliminate
	// per-row make([]Value) allocations. Each row gets a
	// non-overlapping sub-slice from the shared buffer.
	// REQ001091: dataBuf is acquired from projectDataBufPool in
	// NewProject, so the lazy-init branch is no longer needed for
	// the common path. Keep a defensive fallback in case the buffer
	// is nil (e.g. Project constructed directly without NewProject).
	if p.dataBuf == nil {
		p.dataPerRow = len(p.cols)
		// REQ000872: start with smaller initial capacity (64 rows)
		// instead of 256 to reduce wasted allocation for small
		// result sets. Grows by doubling if needed.
		p.dataBuf = make([]Value, 0, 64*p.dataPerRow)
	}
	off := len(p.dataBuf)
	// Ensure buffer has enough capacity for this row.
	required := off + p.dataPerRow
	if cap(p.dataBuf) < required {
		// Grow by doubling capacity.
		newCap := cap(p.dataBuf) * 2
		if newCap < required {
			newCap = required
		}
		// Grow the slice length to accommodate the new row.
		p.dataBuf = append(p.dataBuf, make([]Value, required-off)...)
	} else {
		// Extend the slice length by exactly dataPerRow.
		p.dataBuf = p.dataBuf[:required]
	}
	// Carve sub-slice pointing to the newly added space.
	dataSlice := p.dataBuf[off : off+p.dataPerRow : off+p.dataPerRow]
	out := Row{
		Cols:     p.prefixCols,
		Data:     dataSlice,
		ColIndex: p.colIndex,
	}
	// REQ001184: infer and cache types from evaluated data.
	if p.prefixTypes == nil {
		p.prefixTypes = make([]LX.TokenType, len(p.cols))
	}
	for i := range out.Data {
		if p.prefixTypes[i] == 0 {
			p.prefixTypes[i] = inferProjectType(out.Data[i].ToAny())
		}
	}
	out.Types = p.prefixTypes
	// Fill the data slice directly.
	for i, c := range p.cols {
		fn := p.compiledExprs[i]
		if fn != nil {
			var err error
			out.Data[i], err = fn(&row)
			if err != nil {
				return Row{}, err
			}
			continue
		}
		// Fallback to Eval for complex or unrecognized expressions.
		var v any
		var err error
		if wf, ok := c.(*PS.WindowFunc); ok {
			v, err = findColumn(row, wf.Name)
			if err != nil {
				return Row{}, err
			}
		} else {
			v, err = EV.EvalValue(c, &row, p.params)
			if err != nil {
				return Row{}, err
			}
		}
		out.Data[i] = v.(Value)
	}
	// Debug: validate column count matches expectation
	projectDebugOffset("Project", len(p.cols), len(out.Data), "")
	return out, nil
}

func isStar(cols []PS.Expr) bool {
	if len(cols) == 1 {
		_, ok := cols[0].(*PS.StarExpr)
		return ok
	}
	return false
}

func findColumn(row Row, name string) (any, error) {
	for i, c := range row.Cols {
		if c == name {
			return row.Data[i], nil
		}
	}
	return nil, fmt.Errorf("ex: column %q not found in row", name)
}

func (p *Project) Close() error {
	p.closed.Store(true)
	// REQ001091: return the data buffer to the pool so the next
	// Project can reuse the backing array. Only return buffers
	// that grew to a meaningful size to avoid wasting pool slots
	// on degenerate empty Projects. Reset length to 0 so the
	// next acquirer starts from a clean slate.
	if cap(p.dataBuf) >= projectDataBufChunkSize {
		buf := p.dataBuf[:0]
		projectDataBufPool.Put(&buf)
	}
	p.dataBuf = nil
	p.dataPerRow = 0
	// REQ001707: return pooled slices to their pools so the next
	// NewProject call can reuse the backing arrays without fresh
	// allocations.  Reset len to 0 first so pooled capacity is not
	// wasted by returning partially-filled slices.
	var prefixCols []string
	if p.prefixCols != nil && len(p.prefixCols) > 0 {
		prefixCols = make([]string, 0, cap(p.prefixCols))
		prefixCols = append(prefixCols, p.prefixCols...)
		projectPrefixColsPool.Put(&prefixCols)
	}
	p.prefixCols = nil
	var fnArgBuf []any
	if p.fnArgBuf != nil && len(p.fnArgBuf) > 0 {
		fnArgBuf = make([]any, 0, cap(p.fnArgBuf))
		fnArgBuf = append(fnArgBuf, p.fnArgBuf...)
		projectFnArgBufPool.Put(&fnArgBuf)
	}
	p.fnArgBuf = nil
	// compiledExprs are function closures but we CAN pool the backing
	// slice since the functions themselves are just pointers.  Reset len
	// to 0 so capacity is preserved for reuse.
	var compiledExprs []func(*Row) (Value, error)
	if p.compiledExprs != nil && len(p.compiledExprs) > 0 {
		compiledExprs = make([]func(*Row) (Value, error), 0, cap(p.compiledExprs))
		projectCompiledExprsPool.Put(&compiledExprs)
	}
	p.compiledExprs = nil
	return p.child.Close()
}

// Reset reinitializes Project cursor state. Reuses dataBuf in-place.
// Does NOT close the child. REQ001464.
func (p *Project) Reset(ctx context.Context) error {
	p.dataBuf = p.dataBuf[:0]
	p.dataPerRow = 0
	return nil
}

// compiled predicates today are column-literal comparisons which
// ARE state-dependent (row.ColIndex), so the global cache is
// bypassed by per-Filter caching entirely.
func lookupOrCompilePredicate(e PS.Expr) func(*Row) (bool, error) {
	key := fmt.Sprintf("%v", e)
	if cached, ok := predicateCache.Load(key); ok {
		return cached.(func(*Row) (bool, error))
	}
	fn := compileFilterExpr(e)
	if fn != nil {
		predicateCache.Store(key, fn)
	}
	return fn
}

// inferProjectType infers the token type from a Go value.
// REQ001184: mirrors Values.inferType for Project output rows.
func inferProjectType(v any) LX.TokenType {
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

// compileFilterExpr compiles a simple Filter predicate into a
// specialized function that reads directly from row.Data, bypassing
// the Eval dispatch tree. Returns nil for complex predicates that
// cannot be compiled. REQ000802.
func compileFilterExpr(e PS.Expr) func(*Row) (bool, error) {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case *PS.BinaryExpr:
		return compileBinary(v)
	case *PS.InExpr:
		return compileInExpr(v)
	case *PS.UnaryExpr:
		if v.Op == LX.T_NOT {
			// REQ001979: NOT requires NULL-aware three-valued logic.
			// The compiled (bool,error) path cannot distinguish NULL
			// from FALSE — makeCompiledCmp returns (false, nil) for
			// NULL data, and compiled NOT flips it to (true, nil),
			// which is wrong (NOT NULL = NULL, not TRUE). Always
			// fall back to the per-row Eval path which handles NULL
			// correctly (eval.go:842 returns NULL for unary NOT).
			return nil
		}
		return nil
	case *PS.BetweenExpr:
		// REQ001979: BETWEEN has three-valued NULL semantics
		// (NULL BETWEEN x AND y = NULL). The compiled (bool,error)
		// path cannot express NULL, so always fall back to the
		// per-row Eval path (evalBetween in eval.go:998) which
		// handles NULL correctly via lowOk/highOk tracking.
		return nil
	default:
		return nil
	}
}

// compileInExpr compiles a `col IN (lit1, lit2, ...)` predicate into
// a map lookup. Pre-computes the lookup map once at Filter creation
// so per-row evaluation is O(1) instead of O(N) comparisons.
// REQ001087. Only works when the IN-list contains only literal
// values (no subqueries or computed expressions).
func compileInExpr(e *PS.InExpr) func(*Row) (bool, error) {
	if e.Subquery != nil || len(e.List) == 0 {
		return nil
	}
	colName, ok := colRefName(e.Expr)
	if !ok {
		return nil
	}
	lookup := make(map[string]bool, len(e.List))
	for _, item := range e.List {
		lit, ok := extractLiteral(item)
		if !ok {
			return nil // non-constant expression; fall back to Eval
		}
		lookup[apValueKey(DT.ValueFromAny(lit))] = true
	}
	bareName := colName
	if dot := strings.LastIndexByte(colName, '.'); dot >= 0 {
		bareName = colName[dot+1:]
	}
	return func(row *Row) (bool, error) {
		idx := -1
		for i, c := range row.Cols {
			if strings.EqualFold(c, colName) {
				idx = i
				break
			}
		}
		if idx < 0 {
			for i, c := range row.Cols {
				if strings.EqualFold(c, bareName) && i < len(row.Data) {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			lk := strings.ToLower(bareName)
			for i, c := range row.Cols {
				if strings.HasSuffix(strings.ToLower(c), "."+lk) && i < len(row.Data) {
					idx = i
					break
				}
			}
		}
		if idx < 0 || idx >= len(row.Data) {
			return false, nil
		}
		return lookup[apValueKey(row.Data[idx])], nil
	}
}

// apValueKey returns a string key for a Value suitable for map lookup.
// The key includes the Kind prefix so different types never collide
// (e.g. int 1 != text "1"), and avoids []byte incomparability.
// REQ001087.
func apValueKey(v Value) string {
	switch v.Kind {
	case KindNull:
		return "NULL"
	case KindInt:
		return "I:" + strconv.FormatInt(v.I64, 10)
	case KindFloat:
		return "F:" + strconv.FormatFloat(v.F64, 'g', -1, 64)
	case KindText:
		return "T:" + v.S
	case KindBool:
		if v.Bo {
			return "B:true"
		}
		return "B:false"
	case KindBlob:
		return "BL:" + string(v.B)
	default:
		return ""
	}
}

func compileBinary(e *PS.BinaryExpr) func(*Row) (bool, error) {
	// Handle AND/OR by compiling both sides.
	if e.Op == LX.T_AND {
		left := compileFilterExpr(e.Left)
		right := compileFilterExpr(e.Right)
		if left == nil || right == nil {
			return nil
		}
		return func(row *Row) (bool, error) {
			lr, err := left(row)
			if err != nil || !lr {
				return false, err
			}
			return right(row)
		}
	}
	if e.Op == LX.T_OR {
		left := compileFilterExpr(e.Left)
		right := compileFilterExpr(e.Right)
		if left == nil || right == nil {
			return nil
		}
		return func(row *Row) (bool, error) {
			lr, err := left(row)
			if err != nil || lr {
				return lr, err
			}
			return right(row)
		}
	}

	// Simple comparisons: col OP literal. REQ001711: when literal is
	// on the LEFT (e.g. `-30 >= col0`), extractColLiteralPair returns
	// reversed=true and we must flip the ordering operator.
	col, literal, reversed, ok := extractColLiteralPair(e)
	if ok && literal != nil {
		colName := col
		litVal := literal

		switch e.Op {
		case LX.T_EQ:
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				return pl.EqualValueValue(a, b)
			})
		case LX.T_NE:
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				// SQL semantics: NULL compared with anything = UNKNOWN (drop row)
				// The NULL guard in makeCompiledCmp handles this for col and lit.
				// We just need to negate the equality result for non-NULL values.
				if a.IsNull() || b.IsNull() {
					return false
				}
				return !pl.EqualValueValue(a, b)
			})
		case LX.T_GT:
			cmpFn := func(a, b Value) bool { return pl.CompareValue(a, b) > 0 }
			if reversed {
				cmpFn = func(a, b Value) bool { return pl.CompareValue(b, a) > 0 }
			}
			return makeCompiledCmp(colName, litVal, cmpFn)
		case LX.T_GE:
			cmpFn := func(a, b Value) bool { return pl.CompareValue(a, b) >= 0 }
			if reversed {
				cmpFn = func(a, b Value) bool { return pl.CompareValue(b, a) >= 0 }
			}
			return makeCompiledCmp(colName, litVal, cmpFn)
		case LX.T_LT:
			cmpFn := func(a, b Value) bool { return pl.CompareValue(a, b) < 0 }
			if reversed {
				cmpFn = func(a, b Value) bool { return pl.CompareValue(b, a) < 0 }
			}
			return makeCompiledCmp(colName, litVal, cmpFn)
		case LX.T_LE:
			cmpFn := func(a, b Value) bool { return pl.CompareValue(a, b) <= 0 }
			if reversed {
				cmpFn = func(a, b Value) bool { return pl.CompareValue(b, a) <= 0 }
			}
			return makeCompiledCmp(colName, litVal, cmpFn)
		}
	}

	// Equi-join: col OP col (both sides are column references).
	// This is the common pattern in multi-table joins like
	// "WHERE t1.a = t2.b AND t2.b = t3.c". Compiling these
	// eliminates Eval dispatch overhead in the hot join path.
	// REQ000802+: only compile when both columns are from the
	// same table or are bare names — cross-table qualified names
	// require the Eval path to resolve table aliases correctly.
	// Even with Outer-chain walking in findColIndex, correlated
	// subqueries (e.g., x.b<t1.b where x is an inner alias and t1
	// is the outer table) need the Eval path because the compiled
	// function caches column indices from the first row, which
	// might not have the Outer chain set up yet.
	leftCol, rightCol, ok := extractColColPair(e)
	if ok {
		leftDot := strings.LastIndexByte(leftCol, '.')
		rightDot := strings.LastIndexByte(rightCol, '.')
		if leftDot < 0 && rightDot < 0 {
			// Both are bare names — only compile if both columns
			// exist in the current row. Correlated subqueries where
			// one bare name resolves via the outer chain (e.g.
			// WHERE user_id = id where id is in the outer users row)
			// must NOT be compiled because makeCompiledColColCmp
			// reads from row.Data which belongs to the inner row
			// only. REQ000846.
			return nil
		}
		if leftDot >= 0 && rightDot >= 0 {
			// Both are qualified names. Only compile if they
			// reference the same table prefix. Cross-table
			// qualified names (including correlated subqueries
			// like x.b<t1.b) fall back to Eval.
			leftTbl := leftCol[:leftDot]
			rightTbl := rightCol[:rightDot]
			if leftTbl == rightTbl {
				return makeCompiledColColCmp(leftCol, rightCol, e.Op)
			}
			return nil
		}
		// Mixed (one qualified, one bare) — compile is safe
		// since the bare name resolves via the row's columns.
		if leftDot >= 0 || rightDot >= 0 {
			return makeCompiledColColCmp(leftCol, rightCol, e.Op)
		}
	}

	return nil
}

func makeCompiledCmp(colName string, litVal any, cmp func(a, b Value) bool) func(*Row) (bool, error) {
	bareName := colName
	if dot := strings.LastIndexByte(colName, '.'); dot >= 0 {
		bareName = colName[dot+1:]
	}
	// Pre-convert literal to Value to avoid boxing in hot path.
	litValue := DT.ValueFromAny(litVal)
	// REQ001084: idx must NOT be captured in closure — rows in
	// a batch may have different Cols (e.g. cross join produces
	// rows with varying prefixed columns between left/right sides).
	// A cached idx from row N can be wrong for row M in the same
	// batch. Recompute idx per row by linear scan — slower per row
	// but correct across heterogeneous row layouts.
	return func(row *Row) (bool, error) {
		// REQ001084: recompute idx every call. Cross-join batches
		// can have rows with different Cols between left/right
		// sides; a cached idx from a previous row would be wrong.
		idx := -1
		// Try direct match first (qualified name like "t1.a").
		for i, c := range row.Cols {
			if strings.EqualFold(c, colName) {
				idx = i
				break
			}
		}
		// Fallback: try bare column name (works for SeqScan rows).
		if idx < 0 {
			for i, c := range row.Cols {
				if strings.EqualFold(c, bareName) && i < len(row.Data) {
					idx = i
					break
				}
			}
		}
		// Fallback: suffix match for bare names on prefixed rows.
		if idx < 0 {
			lk := strings.ToLower(bareName)
			for i, c := range row.Cols {
				if strings.HasSuffix(strings.ToLower(c), "."+lk) && i < len(row.Data) {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			return false, nil
		}
		if idx >= len(row.Data) {
			return false, nil
		}
		// SQL three-valued logic: NULL compared with anything = UNKNOWN.
		// Without this guard, DT.Compare() returns a non-zero ordering for
		// NULL values, causing the comparison to incorrectly evaluate
		// as true/false instead of NULL (filtered out by Filter).
		if isNullValueValue(row.Data[idx]) || isNullValueValue(litValue) {
			return false, nil
		}
		// Direct Value comparison — no boxing.
		return cmp(row.Data[idx], litValue), nil
	}
}

// extractColLiteralPair extracts (column_name, literal_value, reversed, ok)
// from a BinaryExpr where one side is a column reference and the other is
// a literal. REQ000802: supports both Ident (bare name) and QualifiedName
// (table.col). When the literal appears on the LEFT side of the comparison
// (e.g. `-30 >= col0`), the pair is canonicalized to (col, lit) with
// `reversed = true` so callers know to flip the comparison direction
// (`col OP lit` becomes `lit OP col`, which for ordering operators means
// swapping `>` ↔ `<` and `>=` ↔ `<=`). REQ001711.
func extractColLiteralPair(e *PS.BinaryExpr) (string, any, bool, bool) {
	if col, ok := colRefName(e.Left); ok {
		if lit, ok := extractLiteral(e.Right); ok {
			return col, lit, false, true
		}
	}
	if col, ok := colRefName(e.Right); ok {
		if lit, ok := extractLiteral(e.Left); ok {
			return col, lit, true, true
		}
	}
	return "", nil, false, false
}

// colRefName returns the column name from an expression that is
// either an Ident or a QualifiedName, plus whether it succeeded.
func colRefName(e PS.Expr) (string, bool) {
	switch v := e.(type) {
	case *PS.Ident:
		return v.Name, true
	case *PS.QualifiedName:
		return v.Table + "." + v.Name, true
	}
	return "", false
}

// extractLiteral returns the Go value from a literal expression node.
func extractLiteral(e PS.Expr) (any, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		return v.Val, true
	case *PS.FloatLiteral:
		return v.Val, true
	case *PS.StringLiteral:
		return v.Val, true
	case *PS.BoolLiteral:
		return v.Val, true
	case *PS.NullLiteral:
		return nil, true
	default:
		return nil, false
	}
}

// extractColColPair extracts (leftColName, rightColName, ok) from a
// BinaryExpr where both sides are column references (Ident or QualifiedName).
// This enables compilation of equi-join predicates like "t1.a = t2.b".
func extractColColPair(e *PS.BinaryExpr) (string, string, bool) {
	leftCol, leftOK := colRefName(e.Left)
	rightCol, rightOK := colRefName(e.Right)
	if leftOK && rightOK {
		return leftCol, rightCol, true
	}
	return "", "", false
}

// makeCompiledColColCmp builds a compiled comparison function for
// two column references. It handles both bare names ("a") and
// qualified names ("t1.a"), with fallbacks for prefixed rows.
// REQ001273: revalidates cached column indices on each call to
// handle rows with varying column layouts (cross-join output).
func makeCompiledColColCmp(leftCol, rightCol string, op LX.TokenType) func(*Row) (bool, error) {
	var leftIdx, rightIdx int = -1, -1
	var lastLeftRowLen, lastRightRowLen int

	return func(row *Row) (bool, error) {
		// Revalidate cached indices if row layout changed.
		if leftIdx >= 0 && (leftIdx >= len(row.Data) || len(row.Data) != lastLeftRowLen) {
			leftIdx = findColIndex(row, leftCol)
		}
		if rightIdx >= 0 && (rightIdx >= len(row.Data) || len(row.Data) != lastRightRowLen) {
			rightIdx = findColIndex(row, rightCol)
		}
		if leftIdx < 0 {
			leftIdx = findColIndex(row, leftCol)
		}
		if rightIdx < 0 {
			rightIdx = findColIndex(row, rightCol)
		}
		if leftIdx >= 0 {
			lastLeftRowLen = len(row.Data)
		}
		if rightIdx >= 0 {
			lastRightRowLen = len(row.Data)
		}

		// If either column is not found, the comparison yields false.
		if leftIdx < 0 || rightIdx < 0 || leftIdx >= len(row.Data) || rightIdx >= len(row.Data) {
			return false, nil
		}

		a, b := row.Data[leftIdx], row.Data[rightIdx]

		// SQL three-valued logic: NULL compared with anything = UNKNOWN.
		if isNullValue(a) || isNullValue(b) {
			return false, nil
		}

		switch op {
		case LX.T_EQ:
			eq, _ := DT.EqualValue(a, b)
			return eq, nil
		case LX.T_NE:
			eq, _ := DT.EqualValue(a, b)
			return !eq, nil
		case LX.T_GT:
			return DT.CompareValue(a, b) > 0, nil
		case LX.T_GE:
			return DT.CompareValue(a, b) >= 0, nil
		case LX.T_LT:
			return DT.CompareValue(a, b) < 0, nil
		case LX.T_LE:
			return DT.CompareValue(a, b) <= 0, nil
		}
		return false, nil
	}
}

// findColIndex finds the column index for a name in a row, handling
// bare names, qualified names, suffix matches for prefixed rows,
// and correlated subquery outer-row resolution.
func findColIndex(row *Row, name string) int {
	idx := findColIndexInRow(row, name)
	if idx >= 0 {
		return idx
	}
	// REQ000700: walk the outer chain for correlated subquery
	// resolution. The subquery's inner row may not contain the
	// outer-referenced column (e.g., t1.b when inner row is aliased
	// as x with columns [x.a, x.b, ...]).
	for cur := row.Outer; cur != nil; cur = cur.Outer {
		if nameHasTable(name) && cur.TableName != "" && !strings.EqualFold(cur.TableName, tableOfName(name)) {
			continue
		}
		idx = findColIndexInRow(cur, name)
		if idx >= 0 {
			return idx
		}
	}
	return -1
}

// nameHasTable reports whether name contains a table prefix.
func nameHasTable(name string) bool {
	return strings.IndexByte(name, '.') >= 0
}

// tableOfName returns the table prefix of "t.col" (returns "t");
// returns "" if name has no table prefix.
func tableOfName(name string) string {
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		return name[:dot]
	}
	return ""
}

// findColIndexInRow searches a single row for the column name.
// Does not walk the Outer chain — use findColIndex for that.
// REQ000816: skip strings.ToLower when name is already lowercase.
func findColIndexInRow(row *Row, name string) int {
	if row == nil {
		return -1
	}
	// Fast path: skip ToLower when name is already lowercase.
	lower := name
	for _, c := range name {
		if c >= 'A' && c <= 'Z' {
			lower = strings.ToLower(name)
			break
		}
	}
	bareName := name
	hasDot := false
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		bareName = name[dot+1:]
		hasDot = true
	}
	bareLower := bareName
	if lower != name {
		// If name had uppercase, we need ToLower for bareLower too.
		bareLower = strings.ToLower(bareName)
	}

	// For qualified names (containing dot), prefer exact linear scan
	// to avoid colIndex's duplicate-key issue in self-joins where
	// both sides produce columns with the same qualified prefix.
	if hasDot {
		for i, c := range row.Cols {
			if strings.EqualFold(c, name) && i < len(row.Data) {
				return i
			}
		}
	}

	// Fast path: use colIndex map (safe for bare names or when
	// the qualified name wasn't found via linear scan).
	if row.ColIndex != nil {
		if idx, ok := row.ColIndex[lower]; ok && idx < len(row.Data) {
			return idx
		}
		if idx, ok := row.ColIndex[bareLower]; ok && idx < len(row.Data) {
			return idx
		}
	}

	// Linear scan for exact match (bare or qualified).
	for i, c := range row.Cols {
		cl := strings.ToLower(c)
		if cl == lower || cl == bareLower {
			if i < len(row.Data) {
				return i
			}
			return -1
		}
	}

	// Suffix match for bare names on prefixed rows (e.g., "a" matches "t1.a").
	for i, c := range row.Cols {
		if strings.HasSuffix(strings.ToLower(c), "."+bareLower) && i < len(row.Data) {
			return i
		}
	}

	return -1
}

// compileRowExpr compiles a single SELECT expression into a function
// that reads directly from the input row and returns a Value.
// Returns nil for unrecognized patterns (caller falls back to Eval).
func compileRowExpr(e PS.Expr) func(*Row) Value {
	switch v := e.(type) {
	case *PS.QualifiedName:
		return compileColRef(v.Table+"."+v.Name, v.SlotIdx)
	case *PS.Ident:
		return compileColRef(v.Name, v.SlotIdx)
	case *PS.AliasedExpr:
		return compileRowExpr(v.Expr)
	case *PS.BinaryExpr:
		return compileBinaryArith(v)
	default:
		return nil
	}
}

// compileProjectExprs compiles SELECT expressions into fast-path
// evaluators that read directly from row.Data, bypassing Eval
// dispatch and Value↔any boxing. REQ000802.
func (p *Project) compileProjectExprs() {
	exprRaw := projectCompiledExprsPool.Get().(*[]func(*Row) (Value, error))
	if cap(*exprRaw) < len(p.cols) {
		*exprRaw = make([]func(*Row) (Value, error), 0, len(p.cols))
	}
	p.compiledExprs = (*exprRaw)[:len(p.cols):len(p.cols)]
	for i, c := range p.cols {
		switch e := c.(type) {
		case *PS.FunctionCall:
			p.compiledExprs[i] = compileProjectFuncCall(e, p.fnArgBuf)
		case *PS.CaseExpr:
			p.compiledExprs[i] = compileProjectCaseExpr(e)
		default:
			if fn := compileRowExpr(c); fn != nil {
				fn2 := fn
				p.compiledExprs[i] = func(row *Row) (Value, error) {
					return fn2(row), nil
				}
			}
		}
	}
	// REQ001282: pre-allocate dataBuf to child's estimated row count
	// to avoid repeated doubling reallocations for large result sets.
	if est := estimateChildRowCount(p.child); est > 0 {
		needed := int(est) * p.dataPerRow
		if cap(p.dataBuf) < needed {
			newBuf := make([]Value, len(p.dataBuf), needed)
			copy(newBuf, p.dataBuf)
			p.dataBuf = newBuf
		}
	}
}

// estimateChildRowCount returns an approximate row count from the
// child operator, or -1 if unknown. For SeqScan with a store, it
// counts keys by iterating the store once. REQ001282.
func estimateChildRowCount(child Operator) int {
	ss, ok := child.(*SeqScan)
	if !ok || ss == nil {
		return -1
	}
	store := ss.Store()
	if store == nil {
		return -1
	}
	iter := store.NewIterator(nil)
	if iter == nil {
		return -1
	}
	defer iter.Close()
	count := 0
	for iter.Next() {
		count++
	}
	if err := iter.Err(); err != nil {
		return -1
	}
	return count
}

// compileBinaryArith compiles a binary arithmetic expression (+-*/ and DIV)
// into a function that reads directly from the input row.
func compileBinaryArith(v *PS.BinaryExpr) func(*Row) Value {
	if v.Op != LX.T_PLUS && v.Op != LX.T_MINUS &&
		v.Op != LX.T_STAR && v.Op != LX.T_SLASH && v.Op != LX.T_DIV {
		return nil
	}
	left := compileRowExpr(v.Left)
	right := compileRowExpr(v.Right)
	if left == nil || right == nil {
		return nil
	}
switch v.Op {
	case LX.T_PLUS:
		return func(row *Row) Value {
			a, b := left(row), right(row)
			if a.IsNull() || b.IsNull() {
				return Value{Kind: KindNull}
			}
			if a.Kind == KindInt && b.Kind == KindInt {
				return Value{Kind: KindInt, I64: a.I64 + b.I64}
			}
			return Value{Kind: KindFloat, F64: valueToFloat(a) + valueToFloat(b)}
		}
	case LX.T_MINUS:
		return func(row *Row) Value {
			a, b := left(row), right(row)
			if a.IsNull() || b.IsNull() {
				return Value{Kind: KindNull}
			}
			if a.Kind == KindInt && b.Kind == KindInt {
				return Value{Kind: KindInt, I64: a.I64 - b.I64}
			}
			return Value{Kind: KindFloat, F64: valueToFloat(a) - valueToFloat(b)}
		}
	case LX.T_STAR:
		return func(row *Row) Value {
			a, b := left(row), right(row)
			if a.IsNull() || b.IsNull() {
				return Value{Kind: KindNull}
			}
			if a.Kind == KindInt && b.Kind == KindInt {
				return Value{Kind: KindInt, I64: a.I64 * b.I64}
			}
			return Value{Kind: KindFloat, F64: valueToFloat(a) * valueToFloat(b)}
		}
	case LX.T_SLASH:
		return func(row *Row) Value {
			a, b := left(row), right(row)
			if a.IsNull() || b.IsNull() {
				return Value{Kind: KindNull}
			}
			if b.Kind == KindInt && b.I64 == 0 {
				return Value{Kind: KindNull}
			}
			if b.Kind == KindFloat && b.F64 == 0 {
				return Value{Kind: KindNull}
			}
			if a.Kind == KindInt && b.Kind == KindInt {
				return Value{Kind: KindInt, I64: a.I64 / b.I64}
			}
			return Value{Kind: KindFloat, F64: valueToFloat(a) / valueToFloat(b)}
		}
	case LX.T_DIV:
		return func(row *Row) Value {
			a, b := left(row), right(row)
			if a.IsNull() || b.IsNull() {
				return Value{Kind: KindNull}
			}
			if b.Kind == KindInt && b.I64 == 0 {
				return Value{Kind: KindNull}
			}
			if b.Kind == KindFloat && b.F64 == 0 {
				return Value{Kind: KindNull}
			}
			var ai, bi int64
			switch a.Kind {
			case KindInt:
				ai = a.I64
			case KindFloat:
				ai = int64(a.F64)
			default:
				return Value{Kind: KindNull}
			}
			switch b.Kind {
			case KindInt:
				bi = b.I64
			case KindFloat:
				bi = int64(b.F64)
			default:
				return Value{Kind: KindNull}
			}
			if bi == 0 {
				return Value{Kind: KindNull}
			}
			return Value{Kind: KindInt, I64: ai / bi}
		}
	}
	return nil
}

// compileColRef compiles a column reference (bare or qualified name)
// into a function that reads directly from row.Data.
// REQ000898: caches the column index on first lookup so repeated
// calls (across rows in a batch) use O(1) direct index access.
func compileColRef(name string, slotIdx int) func(*Row) Value {
	lower := strings.ToLower(name)
	bareName := name
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		bareName = name[dot+1:]
	}
	bareLower := strings.ToLower(bareName)
	// REQ001432: pre-compute the qualified-name suffix once at
	// compile time. The original code rebuilt "."+bareLower inside
	// the per-row loop via capture-via-escape, which forced a
	// runtime.concatstring2 allocation each iteration. Note: we
	// always build the suffix (even for unqualified refs) because
	// multi-table join rows may carry qualified columns like
	// "t4.b4" while the projection references bare "b4".
	dotBareLower := "." + bareLower
	return func(row *Row) Value {
		if slotIdx >= 0 && slotIdx < len(row.Data) && slotIdx < len(row.Cols) {
			cl := row.Cols[slotIdx]
			if len(cl) > 0 {
				if strings.EqualFold(cl, lower) || strings.EqualFold(cl, bareLower) {
					return row.Data[slotIdx]
				}
			}
		}
		if row.ColIndex != nil {
			if i, ok := row.ColIndex[lower]; ok && i < len(row.Data) {
				return row.Data[i]
			}
			// REQ001432: also probe the bare name. Rows from
			// single-table scans populate ColIndex with the
			// unqualified column name; rows from multi-table joins
			// use the qualified form. Querying both removes a full
			// O(n) Cols scan when the bare form matches.
			if lower != bareLower {
				if i, ok := row.ColIndex[bareLower]; ok && i < len(row.Data) {
					return row.Data[i]
				}
			}
		}
		for i, c := range row.Cols {
			cl := strings.ToLower(c)
			if cl == lower || cl == bareLower {
				if i < len(row.Data) {
					return row.Data[i]
				}
				return Value{Kind: KindNull}
			}
		}
		// REQ001432: qualified-suffix match. Hoisted dotBareLower
		// avoids the per-iter "." + bareLower concat allocation.
		for i, c := range row.Cols {
			if strings.HasSuffix(strings.ToLower(c), dotBareLower) && i < len(row.Data) {
				return row.Data[i]
			}
		}
		return Value{Kind: KindNull}
	}
}

// compileProjectFuncCall compiles a function call expression into a
// closure that calls EvalFunction directly, bypassing the top-level
// EvalValue type-switch. REQ001291. The buf argument is reused for
// argument storage to avoid per-call allocation (REQ001567).
func compileProjectFuncCall(e *PS.FunctionCall, buf []any) func(*Row) (Value, error) {
	return func(row *Row) (Value, error) {
		return EV.EvalFunction(e, row, nil, buf)
	}
}

// compileProjectCaseExpr compiles a CASE expression into a closure
// that evaluates it directly. REQ001291.
func compileProjectCaseExpr(e *PS.CaseExpr) func(*Row) (Value, error) {
	return func(row *Row) (Value, error) {
		return EV.EvalValue(e, row, nil)
	}
}

// valueToFloat converts a Value to float64 for mixed-type arithmetic.
func valueToFloat(v Value) float64 {
	switch v.Kind {
	case KindInt:
		return float64(v.I64)
	case KindFloat:
		return v.F64
	default:
		return 0
	}
}
