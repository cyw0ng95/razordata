package OP

import (
	"context"
	"sync"
	"sync/atomic"

	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

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
const filterBatchSize = 256

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
					f.batchEmit = append(f.batchEmit, cloned...)
					continue
				}
			}
			// Only Data needs a per-row deep copy (it varies per row).
			if r.Data != nil {
				r.Data = append([]Value(nil), r.Data...)
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