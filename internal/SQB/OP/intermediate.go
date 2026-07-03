package OP

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
)

// REQ001088: predicateCache is now best-effort. The Filter struct caches
// its own compiled predicate on first use (see Filter.compiledOnce),
// so the global cache is only consulted by legacy callers via
// lookupOrCompilePredicate. Per-Filter compilation avoids cross-query
// pollution when one query's compiled closure referenced a stale
// row.ColIndex.
var predicateCache sync.Map

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
}

// Child returns the filter's child operator. Used by
// propagateParams to walk the operator tree.
func (f *Filter) Child() Operator { return f.child }
func (f *Filter) SetChild(c Operator) { f.child = c }
func (f *Filter) SetExecCtx(ec *pl.ExecContext) { f.execCtx = ec }

// Predicate returns the filter's predicate expression.
func (f *Filter) Predicate() PS.Expr { return f.predicate }

func NewFilter(child Operator, predicate PS.Expr) *Filter {
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

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (f *Filter) WithParams(p []any) Operator {
	f.params = p
	return f
}

func (f *Filter) Next(ctx context.Context) (Row, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Row{}, err
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
const filterBatchSize = 1024

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

type Project struct {
	child  Operator
	cols   []PS.Expr
	params []any
	// REQ000756: pre-allocated column names (same for every row).
	prefixCols []string
	// REQ000816: pre-built colIndex map shared across all output
	// rows. Avoids per-row BuildColIndex in Lookup (pprof: 23.45%
	// cum, 1.06s in j3_mixed).
	colIndex map[string]int
	// REQ000802: compiled expression evaluators. On the first call
	// to Next(), each SELECT expression is compiled into a function
	// that reads directly from the input row's Data, bypassing
	// Eval dispatch and Value<->any boxing.
	compiledExprs []func(in *Row) Value
	// REQ000802+: pre-allocated data buffer for output rows.
	// Each row gets a non-overlapping sub-slice [off:off:off+dataPerRow]
	// from this shared buffer, eliminating per-row make([]Value) allocations.
	dataBuf    []Value
	dataPerRow int
	// execCtx carries per-execution state (planner, session ID,
	// tx writer, change counters) to eval functions. REQ000812.
	execCtx *pl.ExecContext
}

// Child returns the project's child operator.
func (p *Project) Child() Operator { return p.child }
func (p *Project) SetChild(c Operator) { p.child = c }
func (p *Project) Cols() []PS.Expr { return p.cols }
func (p *Project) SetExecCtx(ec *pl.ExecContext) { p.execCtx = ec }

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
	// Pre-compute column names once (they're the same for every row).
	prefixCols := make([]string, len(cols))
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
	}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (p *Project) WithParams(p2 []any) Operator {
	p.params = p2
	return p
}

func (p *Project) Next(ctx context.Context) (Row, error) {
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
		Cols:     p.prefixCols, // shared, no copy needed
		Data:     dataSlice,
		ColIndex: p.colIndex,
	}
	// Fill the data slice directly.
	for i, c := range p.cols {
		fn := p.compiledExprs[i]
		if fn != nil {
			out.Data[i] = fn(&row)
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
	return p.child.Close()
}

type Sort struct {
	child        Operator
	keys         []PS.OrderItem
	buf          []Row
	pos          int
	materialized bool
	params       []any
	pool         *UT.WorkerPool // REQ001050: parallel sort support
	// sortBufferSize caps the number of rows materialized before
	// sorting. 0 = unlimited. REQ001065.
	sortBufferSize int64
}

// Child returns the sort's child operator.
func (s *Sort) Child() Operator { return s.child }
func (s *Sort) SetChild(c Operator) { s.child = c }
func (s *Sort) Keys() []PS.OrderItem { return s.keys }

func NewSort(child Operator, keys []PS.OrderItem) *Sort {
	return &Sort{child: child, keys: keys}
}

// WithPool attaches a WorkerPool for parallel sort. REQ001050.
func (s *Sort) WithPool(pool *UT.WorkerPool) *Sort {
	s.pool = pool
	return s
}

// WithSortBufferSize caps the in-memory materialization for sorting.
// 0 = unlimited. REQ001065.
func (s *Sort) WithSortBufferSize(v int64) *Sort {
	s.sortBufferSize = v
	return s
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (s *Sort) WithParams(p []any) Operator {
	s.params = p
	return s
}

func (s *Sort) Next(ctx context.Context) (Row, error) {
	if !s.materialized {
		for {
			row, err := s.child.Next(ctx)
			if err != nil {
				if err == ErrNoRows {
					break
				}
				return Row{}, err
			}
			s.buf = append(s.buf, row)
		}

		// REQ000768+REQ000773: pre-extract sort keys into a parallel
		// keyCache slice, then sort an index array in-place.
		// Eliminates the sortRow allocation and double-buffering.
		// REQ001025: use a flat buffer to avoid N allocations.
		n := len(s.buf)
		numKeys := len(s.keys)
		keyCache := make([][]Value, n)
		flatKeys := make([]Value, n*numKeys)
		for i, r := range s.buf {
			sk := flatKeys[i*numKeys : (i+1)*numKeys]
			for j, k := range s.keys {
				v, err := EV.EvalValue(k.Expr, &r, s.params)
				if err != nil {
					return Row{}, err
				}
				sk[j] = v
			}
			keyCache[i] = sk
		}

		// REQ001050: parallel sort when pool is available and > threshold
		if s.pool != nil && n > 10000 {
			if err := s.parallelSort(ctx, keyCache); err != nil {
				return Row{}, err
			}
		} else {
			indices := make([]int, n)
			for i := range indices {
				indices[i] = i
			}
			slices.SortStableFunc(indices, func(ai, bi int) int {
				ka, kb := keyCache[ai], keyCache[bi]
				for ki := range ka {
					if s.keys[ki].NullsOrder != 0 {
						if ka[ki].IsNull() && !kb[ki].IsNull() {
							return -int(s.keys[ki].NullsOrder)
						}
						if kb[ki].IsNull() && !ka[ki].IsNull() {
							return int(s.keys[ki].NullsOrder)
						}
					}
					c := pl.CompareValue(ka[ki], kb[ki])
					if c == 0 {
						continue
					}
					if s.keys[ki].Desc {
						return -c
					}
					return c
				}
				return 0
			})

			reordered := make([]Row, n)
			for i, idx := range indices {
				reordered[i] = s.buf[idx]
			}
			s.buf = reordered
		}
		s.materialized = true
	}
	if s.pos >= len(s.buf) {
		return Row{}, ErrNoRows
	}
	r := s.buf[s.pos]
	s.pos++
	return r, nil
}

// ParallelSortThreshold is the minimum row count for parallel sort.
const ParallelSortThreshold = 10000

func (s *Sort) parallelSort(ctx context.Context, keyCache [][]Value) error {
	n := len(s.buf)
	workers := s.pool.Workers()
	if workers < 2 {
		workers = 2
	}

	// Sample-based partition: pick workers-1 splitters from keyCache
	sampleStep := n / (workers * 4)
	if sampleStep < 1 {
		sampleStep = 1
	}
	samples := make([]int, 0, workers*4)
	for i := 0; i < n && len(samples) < workers*4; i += sampleStep {
		samples = append(samples, i)
	}
	slices.SortStableFunc(samples, func(a, b int) int {
		ka, kb := keyCache[a], keyCache[b]
		for ki := range ka {
			c := DT.Compare(ka[ki], kb[ki])
			if c == 0 {
				continue
			}
			return c
		}
		return 0
	})

	// Pick every 4th sample as a splitter
	splitters := make([][]Value, 0, workers-1)
	for i := 4; i < len(samples) && len(splitters) < workers-1; i += 4 {
		splitters = append(splitters, keyCache[samples[i]])
	}
	if len(splitters) == 0 {
		// Single partition — sort sequentially
		indices := make([]int, n)
		for i := range indices {
			indices[i] = i
		}
		slices.SortStableFunc(indices, func(ai, bi int) int {
			return s.cmpKeys(keyCache[ai], keyCache[bi])
		})
		reordered := make([]Row, n)
		for i, idx := range indices {
			reordered[i] = s.buf[idx]
		}
		s.buf = reordered
		return nil
	}

	// Partition rows by splitters
	partitions := make([][]int, len(splitters)+1)
	for i := range partitions {
		partitions[i] = make([]int, 0, n/(len(splitters)+1))
	}
	for i := 0; i < n; i++ {
		key := keyCache[i]
		placed := false
		for pi, split := range splitters {
			if s.cmpKeys(key, split) < 0 {
				partitions[pi] = append(partitions[pi], i)
				placed = true
				break
			}
		}
		if !placed {
			partitions[len(partitions)-1] = append(partitions[len(partitions)-1], i)
		}
	}

	// Sort each partition in parallel
	type partResult struct {
		idx  int
		rows []Row
		err  error
	}
	resultCh := make(chan partResult, len(partitions))
	var wg sync.WaitGroup

	for pi, part := range partitions {
		if len(part) == 0 {
			continue
		}
		pi2, part2 := pi, part
		wg.Add(1)
		err := s.pool.Submit(ctx, func() error {
			defer wg.Done()
			slices.SortStableFunc(part2, func(a, b int) int {
				return s.cmpKeys(keyCache[a], keyCache[b])
			})
			out := make([]Row, len(part2))
			for j, idx := range part2 {
				out[j] = s.buf[idx]
			}
			resultCh <- partResult{idx: pi2, rows: out}
			return nil
		})
		if err != nil {
			wg.Done()
			resultCh <- partResult{idx: pi2, err: err}
			break
		}
	}

	wg.Wait()
	close(resultCh)

	ordered := make([][]Row, len(partitions))
	for res := range resultCh {
		if res.err != nil {
			return res.err
		}
		ordered[res.idx] = res.rows
	}
	var all []Row
	for _, part := range ordered {
		all = append(all, part...)
	}
	s.buf = all
	return nil
}

// cmpKeys compares two sort key Value slices, respecting DESC/NullsOrder.
func (s *Sort) cmpKeys(a, b []Value) int {
	for ki := range a {
		if s.keys[ki].NullsOrder != 0 {
			if a[ki].IsNull() && !b[ki].IsNull() {
				return -int(s.keys[ki].NullsOrder)
			}
			if b[ki].IsNull() && !a[ki].IsNull() {
				return int(s.keys[ki].NullsOrder)
			}
		}
		c := DT.Compare(a[ki], b[ki])
		if c == 0 {
			continue
		}
		if s.keys[ki].Desc {
			return -c
		}
		return c
	}
	return 0
}

func (s *Sort) Close() error {
	s.buf = nil
	s.pos = 0
	s.materialized = false
	return s.child.Close()
}

type Limit struct {
	child  Operator
	limit  int64
	seen   int64
	params []any
}

// Child returns the limit's child operator.
func (l *Limit) Child() Operator { return l.child }
func (l *Limit) SetChild(c Operator) { l.child = c }

// LimitValue returns the limit value.
func (l *Limit) LimitValue() int64 { return l.limit }

func NewLimit(child Operator, n int64) *Limit {
	return &Limit{child: child, limit: n}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (l *Limit) WithParams(p []any) Operator {
	l.params = p
	return l
}

func (l *Limit) Next(ctx context.Context) (Row, error) {
	if l.seen >= l.limit {
		return Row{}, ErrNoRows
	}
	row, err := l.child.Next(ctx)
	if err != nil {
		return Row{}, err
	}
	l.seen++
	return row, nil
}

func (l *Limit) Close() error {
	l.seen = 0
	return l.child.Close()
}

// Offset skips the first n rows from its child before yielding. It pairs
// with Limit to implement LIMIT/OFFSET pagination. A nil child or a
// negative n is treated as zero (no offset).
type Offset struct {
	child   Operator
	offset  int64
	skipped int64
	params  []any
}

// Child returns the offset's child operator.
func (o *Offset) Child() Operator { return o.child }
func (o *Offset) SetChild(c Operator) { o.child = c }

func NewOffset(child Operator, n int64) *Offset {
	if n < 0 {
		n = 0
	}
	return &Offset{child: child, offset: n}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (o *Offset) WithParams(p []any) Operator {
	o.params = p
	return o
}

func (o *Offset) Next(ctx context.Context) (Row, error) {
	for o.skipped < o.offset {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		if _, err := o.child.Next(ctx); err != nil {
			return Row{}, err
		}
		o.skipped++
	}
	return o.child.Next(ctx)
}

func (o *Offset) Close() error {
	o.skipped = 0
	if o.child == nil {
		return nil
	}
	return o.child.Close()
}

// REQ001088: predicate compilation is now per-Filter. The previous
// global `sync.Map` cache was keyed by `fmt.Sprintf("%v", e)` which
// serialized the entire AST on every call AND was shared across
// all Executors, creating cross-query pollution risk when one
// query's compiled closure referenced a stale row.ColIndex. Per-Filter
// compilation (via Filter.compiledOnce) is simpler and correct.
// `lookupOrCompilePredicate` is kept as a thin wrapper for backward
// compat with tests and any external callers.

// lookupOrCompilePredicate checks the global predicate cache for a
// compiled filter function. On cache miss it compiles and stores the
// result. REQ000802+.
//
// REQ001088: this cache is now best-effort. The Filter struct caches
// its own compiled predicate on first use, so the global cache only
// helps when many Filters share the same predicate text AND the
// compiled closure doesn't depend on per-Filter state. Most
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
			// REQ001127: NOT InExpr requires NULL-aware three-valued
			// logic that the compiled (bool,error) path cannot express
			// (NULL IN list is NULL, not FALSE, so NOT NULL is NULL,
			// not TRUE). Fall back to the per-row Eval path which
			// handles NULL correctly.
			if _, ok := v.Operand.(*PS.InExpr); ok {
				return nil
			}
			inner := compileFilterExpr(v.Operand)
			if inner == nil {
				return nil
			}
			return func(row *Row) (bool, error) {
				res, err := inner(row)
				if err != nil {
					return false, err
				}
				return !res, nil
			}
		}
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

	// Simple comparisons: col OP literal
	col, literal, ok := extractColLiteralPair(e)
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
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				return pl.CompareValue(a, b) > 0
			})
		case LX.T_GE:
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				return pl.CompareValue(a, b) >= 0
			})
		case LX.T_LT:
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				return pl.CompareValue(a, b) < 0
			})
		case LX.T_LE:
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				return pl.CompareValue(a, b) <= 0
			})
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

// extractColLiteralPair extracts (column_name, literal_value, ok) from a
// BinaryExpr where one side is a column reference and the other is a literal.
// REQ000802: supports both Ident (bare name) and QualifiedName (table.col).
func extractColLiteralPair(e *PS.BinaryExpr) (string, any, bool) {
	if col, ok := colRefName(e.Left); ok {
		if lit, ok := extractLiteral(e.Right); ok {
			return col, lit, true
		}
	}
	if col, ok := colRefName(e.Right); ok {
		if lit, ok := extractLiteral(e.Left); ok {
			return col, lit, true
		}
	}
	return "", nil, false
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
func makeCompiledColColCmp(leftCol, rightCol string, op LX.TokenType) func(*Row) (bool, error) {
	// Pre-resolve indices on the first call to avoid repeated linear scans.
	var leftIdx, rightIdx int = -1, -1

	return func(row *Row) (bool, error) {
		// Resolve column indices lazily.
		if leftIdx < 0 {
			leftIdx = findColIndex(row, leftCol)
		}
		if rightIdx < 0 {
			rightIdx = findColIndex(row, rightCol)
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
			return DT.EqualValueAny(a, b), nil
		case LX.T_NE:
			return !DT.EqualValueAny(a, b), nil
		case LX.T_GT:
			return DT.Compare(a, b) > 0, nil
		case LX.T_GE:
			return DT.Compare(a, b) >= 0, nil
		case LX.T_LT:
			return DT.Compare(a, b) < 0, nil
		case LX.T_LE:
			return DT.Compare(a, b) <= 0, nil
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

// compileProjectExprs compiles SELECT expressions into fast-path
// evaluators that read directly from row.Data, bypassing Eval
// dispatch and Value↔any boxing. REQ000802.
func (p *Project) compileProjectExprs() {
	p.compiledExprs = make([]func(*Row) Value, len(p.cols))
	for i, c := range p.cols {
		p.compiledExprs[i] = compileRowExpr(c)
	}
}

// compileRowExpr compiles a single SELECT expression into a function
// that reads directly from the input row and returns a Value.
// Returns nil for unrecognized patterns (caller falls back to Eval).
func compileRowExpr(e PS.Expr) func(*Row) Value {
	switch v := e.(type) {
	case *PS.QualifiedName:
		return compileColRef(v.Table + "." + v.Name)
	case *PS.Ident:
		return compileColRef(v.Name)
	case *PS.AliasedExpr:
		return compileRowExpr(v.Expr)
	case *PS.BinaryExpr:
		return compileBinaryArith(v)
	default:
		return nil
	}
}

// compileBinaryArith compiles a binary arithmetic expression (+-*/ and DIV)
// into a function that reads directly from the input row.
func compileBinaryArith(v *PS.BinaryExpr) func(*Row) Value {
	if v.Op != LX.T_PLUS && v.Op != LX.T_MINUS &&
		v.Op != LX.T_STAR && v.Op != LX.T_SLASH {
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
				// REQ000932: int / int = int (integer division), matching SQLite.
				return Value{Kind: KindInt, I64: a.I64 / b.I64}
			}
			return Value{Kind: KindFloat, F64: valueToFloat(a) / valueToFloat(b)}
		}
	}
	return nil
}

// compileColRef compiles a column reference (bare or qualified name)
// into a function that reads directly from row.Data.
// REQ000898: caches the column index on first lookup so repeated
// calls (across rows in a batch) use O(1) direct index access.
func compileColRef(name string) func(*Row) Value {
	lower := strings.ToLower(name)
	bareName := name
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		bareName = name[dot+1:]
	}
	bareLower := strings.ToLower(bareName)
	return func(row *Row) Value {
		// REQ001084: recompute idx per row. Cross-join output rows
		// may have different Cols; a cached idx from a previous row
		// would read from the wrong column. This is the same bug as
		// makeCompiledCmp (REQ001084) but in the Project column refs.
		// Fast path: use colIndex if available (avoids linear scan).
		if row.ColIndex != nil {
			if i, ok := row.ColIndex[lower]; ok && i < len(row.Data) {
				return row.Data[i]
			}
		}
		// Linear scan with suffix/prefix handling.
		for i, c := range row.Cols {
			cl := strings.ToLower(c)
			if cl == lower || cl == bareLower {
				if i < len(row.Data) {
					return row.Data[i]
				}
				return Value{Kind: KindNull}
			}
		}
		// Suffix match for bare names on prefixed rows.
		for i, c := range row.Cols {
			if strings.HasSuffix(strings.ToLower(c), "."+bareLower) && i < len(row.Data) {
				return row.Data[i]
			}
		}
		return Value{Kind: KindNull}
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
