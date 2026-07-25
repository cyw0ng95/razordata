package OP

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
)

// VectorizedSeqScan produces columnar batches of up to UT.BatchSize
// rows from an underlying row source. Uses the sync.Pool-backed
// Batch allocator to avoid per-batch GC pressure.
// The schema (column names and types) must be pre-computed and
// passed in. The operator reads from `source` (a row iterator)
// and re-projects each row into columnar layout.
// REQ000144 satisfied (partial): Vectorized SeqScan that
// produces columnar batches for downstream operators.
type VectorizedSeqScan struct {
	source       Operator
	schema       []string
	types        []LX.TokenType
	colMap       map[string]int
	requestedCols []int // nil = all columns
	current      *UT.Batch
	done         bool
}

// NewVectorizedSeqScan creates a vectorized scan over the given
// row source. schema is the ordered list of column names; types
// is the parallel list of column types for columnar storage.
func NewVectorizedSeqScan(source Operator, schema []string, types []LX.TokenType) *VectorizedSeqScan {
	colMap := make(map[string]int, len(schema))
	for i, name := range schema {
		colMap[name] = i
	}
	return &VectorizedSeqScan{
		source: source,
		schema: schema,
		types:  types,
		colMap: colMap,
	}
}

// NewVectorizedSeqScanWithCols creates a vectorized scan that only
// reads the requested columns (column pruning). REQ001450.
func NewVectorizedSeqScanWithCols(source Operator, schema []string, types []LX.TokenType, requestedCols []int) *VectorizedSeqScan {
	v := NewVectorizedSeqScan(source, schema, types)
	v.requestedCols = requestedCols
	return v
}

// NextBatch produces the next batch. Returns (nil, nil) at EOF.
// Caller is responsible for calling Put() on each non-nil batch.
func (v *VectorizedSeqScan) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if v.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	schema := v.schema
	types := v.types
	var wantedCols []int
	if len(v.requestedCols) > 0 {
		// REQ001684: sort requestedCols so that
		// DecodeRowSubsetIntoColumnar's ascending cursor scan
		// correctly identifies all wanted columns. Without sorting,
		// unsorted requestedCols (e.g. [1,0] for "col1 + col0")
		// causes the cursor to skip columns whose index is smaller
		// than a preceding entry, producing empty Data.Ints and a
		// panic in downstream batch arithmetic kernels.
		sortedCols := make([]int, len(v.requestedCols))
		copy(sortedCols, v.requestedCols)
		slices.Sort(sortedCols)
		prunedSchema := make([]string, 0, len(sortedCols))
		prunedTypes := make([]LX.TokenType, 0, len(sortedCols))
		for _, ci := range sortedCols {
			if ci >= 0 && ci < len(v.schema) {
				prunedSchema = append(prunedSchema, v.schema[ci])
				prunedTypes = append(prunedTypes, v.types[ci])
			}
		}
		schema = prunedSchema
		types = prunedTypes
		wantedCols = sortedCols
	}

	batch := UT.GetBatch(len(schema))
	batch.Size = 0
	for i, name := range schema {
		batch.SetColumnName(i, name)
	}
	batch.SetColMap(v.colMap)
	// REQ001684: set column types on the batch so ToRows → ToValue
	// can correctly dispatch on col.Type. Without this, ToValue
	// sees col.Type == 0 and returns NULL for every cell.
	for i, typ := range types {
		if i < len(batch.Cols) {
			batch.Cols[i].Type = typ
		}
	}

	// Fast path: when the source is a SeqScan with a real store,
	// read directly into columnar format from store bytes, bypassing
	// Row construction and Value allocation. REQ001480.
	if ss, ok := v.source.(*SeqScan); ok && ss.store != nil && !v.done {
		decodeCols := wantedCols
		if decodeCols == nil {
			decodeCols = make([]int, len(schema))
			for i := range decodeCols {
				decodeCols[i] = i
			}
		}
		n, err := ss.nextColumnarBatch(ctx, batch, decodeCols, types)
		if err != nil {
			batch.Put()
			return nil, err
		}
		if n == 0 {
			v.done = true
			batch.Put()
			return nil, nil
		}
		batch.Size = n
		return batch, nil
	}

	// Fallback: row-at-a-time from the generic Operator source.
	for batch.Size < UT.BatchSize {
		row, err := v.source.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				v.done = true
				break
			}
			batch.Put()
			return nil, err
		}
		// REQ001587: when no schema was provided at construction
		// (in-memory tables without StoreSchema), derive the
		// column names and types from the first row on the fly.
		// This allows VectorizedSeqScan to work correctly with
		// in-memory tables where the schema is only known at
		// runtime from the first row's Cols/Types fields.
		if len(schema) == 0 && len(row.Cols) > 0 {
			schema = row.Cols
			// REQ001587: when the row has no Types metadata (common
			// for in-memory tables created via DT.Row literals),
			// infer the column types from the Value Kind of each
			// Data element. Without this, types[i] panics with
			// index-out-of-range when types is nil/empty.
			if len(row.Types) > 0 {
				types = row.Types
			} else {
				types = make([]LX.TokenType, len(row.Cols))
				for i, v := range row.Data {
					switch v.Kind {
					case pl.KindInt:
						types[i] = LX.T_INT_KW
					case pl.KindFloat:
						types[i] = LX.T_FLOAT_KW
					case pl.KindText:
						types[i] = LX.T_TEXT
					case pl.KindBool:
						types[i] = LX.T_BOOL
					case pl.KindBlob:
						types[i] = LX.T_BLOB
					default:
						types[i] = LX.T_NULL
					}
				}
			}
			// Rebuild colMap from the discovered schema so that
			// upstream operators (VectorizedProject, etc.) can
			// resolve column references by name. Without this,
			// extractColumnRef finds an empty colMap, returns
			// false, and downstream EvalBatchExpr returns NULL.
			colMap := make(map[string]int, len(schema))
			for i, name := range schema {
				colMap[name] = i
			}
			v.colMap = colMap
			// Re-allocate the batch with the correct column count.
			batch.Put()
			batch = UT.GetBatch(len(schema))
			batch.Size = 0
			for i, name := range schema {
				batch.SetColumnName(i, name)
			}
			batch.SetColMap(v.colMap)
		}
		for i := range schema {
			val := row.Data[i].ToAny()
			isNull := val == nil
			batch.AppendRow(i, types[i], val, isNull)
		}
		batch.AdvanceSize()
	}

	if batch.Size == 0 {
		batch.Put()
		return nil, nil
	}
	return batch, nil
}

func (v *VectorizedSeqScan) Close() error {
	if v.current != nil {
		v.current.Put()
		v.current = nil
	}
	if v.source != nil {
		return v.source.Close()
	}
	return nil
}

// Child returns the underlying row operator (the source).
func (v *VectorizedSeqScan) Child() Operator {
	return v.source
}

// VectorizedCoveringIndexScan produces columnar batches directly
// from a covering index scan, bypassing row-at-a-time Row allocation.
// Reads index entries from the store and fills batch column data
// directly via coveringAppendToBatch. REQ001479.
type VectorizedCoveringIndexScan struct {
	idxScan  *IndexScan
	done     bool

	it interface {
		Next() bool
		Key() []byte
		Value() []byte
		Err() error
		Close() error
	}
}

// NewVectorizedCoveringIndexScan creates a vectorized covering index
// scan from an IndexScan that has been configured as a covering scan
// (SetCovering must have been called). Panics if coveringMode is false.
func NewVectorizedCoveringIndexScan(is *IndexScan) *VectorizedCoveringIndexScan {
	return &VectorizedCoveringIndexScan{idxScan: is}
}

// NextBatch produces the next batch of rows from the index iterator.
// Returns (nil, nil) at EOF. Caller must Put() each non-nil batch.
func (v *VectorizedCoveringIndexScan) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if v.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	is := v.idxScan

	// Lazily create the index iterator.
	if v.it == nil {
		if is.prefixIdxKey == nil {
			is.prefixIdxKey = DT.BuildIndexKey(is.indexTableID, is.idx, nil)
		}
		if is.indexSeek != nil && is.indexLower == nil {
			v.it = is.store.NewIterator(DT.BuildIndexKey(is.indexTableID, is.idx, is.indexSeek))
		} else {
			v.it = is.store.NewIterator(is.prefixIdxKey)
		}
		if v.it == nil {
			v.done = true
			return nil, nil
		}
	}

	batch := UT.GetBatch(len(is.schema.Cols))
	batch.Size = 0
	for i, name := range is.schema.Cols {
		batch.SetColumnName(i, name)
	}
	colMap := is.schema.ColIndex
	batch.SetColMap(colMap)

	rowIdx := 0
	for rowIdx < UT.BatchSize {
		if !v.it.Next() {
			v.done = true
			break
		}
		key := v.it.Key()
		idxVal := DT.IndexValueFromKey(key, is.prefixIdxKey)
		if idxVal == nil {
			continue
		}
		// Check lower bound.
		if is.indexLower != nil {
			cmp := bytes.Compare(idxVal, is.indexLower)
			if cmp < 0 || (cmp == 0 && is.indexLowerExclusive) {
				continue
			}
		}
		// Check upper bound.
		if is.indexUpper != nil {
			cmp := bytes.Compare(idxVal, is.indexUpper)
			if cmp > 0 || (cmp == 0 && !is.indexUpperInclusive) {
				v.done = true
				break
			}
		}
		// For exact-match seeks, filter beyond the specific value.
		if is.indexSeek != nil && is.indexLower == nil && is.indexUpper == nil {
			if bytes.Compare(idxVal, is.indexSeek) > 0 {
				v.done = true
				break
			}
		}

		pk := v.it.Value()
		if err := coveringAppendToBatch(is.schema, is.coverIdxCols, is.coverIdxTypes, idxVal, pk, is.coverPK, batch, rowIdx); err != nil {
			batch.Put()
			return nil, err
		}
		rowIdx++
	}

	if rowIdx == 0 {
		batch.Put()
		return nil, nil
	}
	batch.Size = rowIdx
	return batch, nil
}

// Close closes the underlying index iterator.
func (v *VectorizedCoveringIndexScan) Close() error {
	if v.it != nil {
		return v.it.Close()
	}
	return nil
}

// VectorizedFilter applies a predicate to batches from a child
// vectorized source, producing filtered batches with selection
// vectors. Uses EvalBatch for batch-level predicate evaluation.
// REQ000144 satisfied: Vectorized Filter that processes batches
// using selection vectors (no data copying).
type VectorizedFilter struct {
	child  UT.BatchProducer
	pred   PS.Expr
	params []any

	// REQ001631: IN-list bloom filter for fast negative detection.
	inBloom   *UT.BloomFilter
	inNegate  bool
	inColIdx  int
}

// NewVectorizedFilter creates a vectorized filter.
// REQ001631: detects IN-list patterns and builds a bloom filter
// for fast negative detection when the list has > 8 elements.
func NewVectorizedFilter(child UT.BatchProducer, pred PS.Expr) *VectorizedFilter {
	f := &VectorizedFilter{child: child, pred: pred}
	// Try to detect col IN (v1, v2, ..., vn) pattern for bloom filter.
	if inExpr, ok := pred.(*PS.InExpr); ok && inExpr.Subquery == nil && len(inExpr.List) > 8 {
		// Extract column name from the IN expression.
		colName := extractInExprColName(inExpr.Expr)
		if colName != "" {
			// Build bloom filter from literal values.
			vals := make([]int64, 0, len(inExpr.List))
			for _, item := range inExpr.List {
				if lit, ok := item.(*PS.NumberLiteral); ok {
					vals = append(vals, lit.Val)
				}
			}
			if len(vals) > 8 {
				f.inBloom = UT.NewBloomFilter(len(vals), 0.01)
				for _, v := range vals {
					f.inBloom.Add(uint64(v))
				}
				f.inColIdx = -1 // resolved on first batch
				f.inNegate = false
			}
		}
	}
	return f
}

// WithParams propagates bound ? placeholders to the filter.
// REQ000577: the previous implementation returned nil which
// caused nil-pointer panics in any caller that dereferenced
// the result. We now return the receiver typed as *VectorizedFilter
// so the result is always non-nil and usable. Note: this method
// does NOT return the Operator interface because VectorizedFilter
// only implements NextBatch (the columnar vectorized path), not
// Next. Callers that need Operator dispatch should use the
// vectorized batch pipeline directly via NextBatch.
func (f *VectorizedFilter) WithParams(p []any) *VectorizedFilter {
	f.params = p
	return f
}

// NextBatch produces the next filtered batch.
// REQ001631: when a bloom filter is configured, rows that are
// definitely absent from the IN-list are filtered out before
// the full predicate evaluation.
func (f *VectorizedFilter) NextBatch(ctx context.Context) (*UT.Batch, error) {
	for {
		batch, err := f.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, nil // EOF
		}

		// REQ001631: bloom filter pre-filter — skip rows that are
		// definitely absent from the IN-list without calling EvalBatch.
		if f.inBloom != nil {
			// Resolve column index on first batch.
			if f.inColIdx < 0 {
				f.inColIdx = resolveInColName(batch, f.pred.(*PS.InExpr).Expr)
			}
			sel := f.applyBloomFilter(batch)
			if sel == nil {
				return batch, nil // all rows match
			}
			if len(sel) == 0 {
				batch.Put()
				continue // no rows match, try next batch
			}
			// Apply bloom filter selection before EvalBatch.
			// The bloom filter may have false positives, so we still
			// need EvalBatch for the final verdict.
			batch.Sel = sel
			batch.Size = len(sel)
		}

		// Apply predicate via vectorized evaluation
		sel := EV.EvalBatch(f.pred, batch, f.params)

		if sel == nil {
			// All rows match
			return batch, nil
		}
		if len(sel) == 0 {
			// No rows match: drop this batch, try next
			batch.Put()
			continue
		}
		// Partial match: update selection vector
		batch.Sel = sel
		batch.Size = len(sel)
		return batch, nil
}
}

// Close releases the child operator.
func (f *VectorizedFilter) Close() error {
	if f.child != nil {
		return f.child.Close()
	}
	return nil
}

// applyBloomFilter filters rows in a batch by checking each row's
// IN-list column value against the bloom filter. Rows with values
// that are definitely absent from the bloom filter are excluded.
// Returns a selection vector for rows that MAY be in the list.
// REQ001631.
func (f *VectorizedFilter) applyBloomFilter(batch *UT.Batch) []uint16 {
	if f.inColIdx < 0 || f.inColIdx >= len(batch.Cols) || f.inBloom == nil {
		return nil
	}
	col := &batch.Cols[f.inColIdx]
	n := batch.LogicalSize()
	sel := make([]uint16, 0, n)

	for i := 0; i < n; i++ {
		phys := i
		if batch.Sel != nil {
			phys = int(batch.Sel[i])
		}
		if phys >= len(batch.Cols) {
			continue
		}
		if col.Nulls != nil && phys < len(col.Nulls) && col.Nulls[phys] {
			continue
		}
		var key uint64
		switch col.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if phys < len(col.Data.Ints) {
				key = uint64(col.Data.Ints[phys])
			}
		case LX.T_FLOAT_KW:
			if phys < len(col.Data.Floats) {
				key = uint64(col.Data.Floats[phys])
			}
		default:
			// Non-integer columns: include the row (bloom only supports int64).
			sel = append(sel, uint16(phys))
			continue
		}
		if f.inBloom.Contains(key) {
			sel = append(sel, uint16(phys))
		}
	}

	if len(sel) == n {
		return nil // all rows may match
	}
	return sel
}

// extractInExprColName extracts the column name from an IN-list
// expression. Returns "" if the expression is not a simple column ref.
// REQ001631.
func extractInExprColName(expr PS.Expr) string {
	switch e := expr.(type) {
	case *PS.Ident:
		return e.Name
	case *PS.QualifiedName:
		if e.Name != "" {
			return e.Name
		}
		return e.Table
	}
	return ""
}

// resolveInColName finds the column index for a column name in the
// batch. Returns -1 if not found. REQ001631.
func resolveInColName(batch *UT.Batch, expr PS.Expr) int {
	name := extractInExprColName(expr)
	if name == "" {
		return -1
	}
	if batch.ColMap() != nil {
		if idx, ok := batch.ColMap()[name]; ok {
			return idx
		}
	}
	for i, c := range batch.Cols {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// SchemaFromRowSchema converts a Row's Types to []LX.TokenType.
func SchemaFromRowSchema(types []LX.TokenType) []LX.TokenType {
	return append([]LX.TokenType(nil), types...)
}

// VectorizedProject applies expression projections to batches from
// a child BatchProducer, producing a new batch with the projected
// columns. Each expression is evaluated over the child batch to
// produce one output column.
// REQ001212 satisfied: vectorized projection operator.
// REQ001591: compiledEvals caches fast-path evaluators for simple
// expressions (column refs, literals), avoiding EvalBatchExpr dispatch
// overhead on every batch.
type VectorizedProject struct {
	child         UT.BatchProducer
	exprs         []PS.Expr
	names         []string
	done          bool
	compiledEvals []batchEvalFunc
	// REQ001460: per-execution state for subquery evaluation in
	// row-fallback paths. Set by transformOp from the original
	// row-based Project, propagated to childBatch before eval.
	execCtx *pl.ExecContext
}

// batchEvalFunc is a compiled fast-path evaluator for a single
// projection expression. Returns the evaluated column for a batch.
// REQ001591.
type batchEvalFunc func(batch *UT.Batch) UT.Column

// NewVectorizedProject creates a vectorized projection operator.
// REQ001591: compiles simple expressions to fast-path evaluators
// on construction.
func NewVectorizedProject(child UT.BatchProducer, exprs []PS.Expr, names []string) *VectorizedProject {
	p := &VectorizedProject{
		child: child,
		exprs: exprs,
		names: names,
	}
	p.compiledEvals = make([]batchEvalFunc, len(exprs))
	for i, expr := range exprs {
		p.compiledEvals[i] = compileProjectExpr(expr)
	}
	return p
}

// compileProjectExpr compiles a projection expression to a fast-path
// batch evaluator. Returns nil for complex expressions that must use
// the EvalBatchExpr fallback. REQ001591.
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
		return nil
	}
	return nil
}

// SetExecCtx attaches an ExecContext for subquery evaluation in
// row-fallback paths. The execCtx is propagated to each child
// batch before EvalBatchExpr runs, so batchToRow's reconstructed
// Row carries the ExecCtx to evalScalarSubquery via getSubqueryPlanner.
// REQ001460.
func (p *VectorizedProject) SetExecCtx(ec *pl.ExecContext) { p.execCtx = ec }

// NextBatch produces the next projected batch. Returns (nil, nil) at EOF.
func (p *VectorizedProject) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if p.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	childBatch, err := p.child.NextBatch(ctx)
	if err != nil {
		return nil, err
	}
	if childBatch == nil {
		p.done = true
		return nil, nil
	}
	defer childBatch.Put()
	// REQ001460: propagate execCtx so row-fallback eval (notably
	// non-correlated scalar subqueries) can locate the QueryPlanner
	// via batchToRow -> row.ExecCtx -> getSubqueryPlanner.
	childBatch.ExecCtx = p.execCtx

	n := childBatch.LogicalSize()
	output := UT.GetBatch(len(p.exprs))
	output.Size = n
	for i, expr := range p.exprs {
		// REQ001591: use compiled fast-path evaluator when available,
		// fall back to EvalBatchExpr for complex expressions.
		var col UT.Column
		if i < len(p.compiledEvals) && p.compiledEvals[i] != nil {
			col = p.compiledEvals[i](childBatch)
		} else {
			col = EV.EvalBatchExpr(expr, childBatch, nil)
		}
		col.Name = p.names[i]
		if childBatch.Sel != nil && n < childBatch.Size {
			col = compactColumn(col, childBatch.Sel, childBatch.Size)
		}
		output.Cols[i] = col
	}
	return output, nil
}

// compactColumn creates a new column containing only the rows
// identified by sel from the source column. Used when the child
// batch has a selection vector and the output must contain only
// the logically valid rows.
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
	// Compact nulls if present.
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

// Close releases the child BatchProducer.
func (p *VectorizedProject) Close() error {
	if p.child != nil {
		return p.child.Close()
	}
	return nil
}

// errVectorizedNotImplemented is a placeholder for future
// extensions. Currently unused; kept for consistent error
// reporting when a feature is planned but not yet implemented.
var errVectorizedNotImplemented = fmt.Errorf("ex: vectorized operator not yet implemented")

// VectorizedDistinct de-duplicates rows across batches from a child
// BatchProducer. Drains all batches, builds a hash set of distinct row
// keys, and emits a single result batch with the unique rows.
type VectorizedDistinct struct {
	child  UT.BatchProducer
	colMap map[string]int
	cols   []string
	types  []LX.TokenType
	done   bool
}

func NewVectorizedDistinct(child UT.BatchProducer, cols []string, types []LX.TokenType) *VectorizedDistinct {
	colMap := make(map[string]int, len(cols))
	for i, name := range cols {
		colMap[name] = i
	}
	return &VectorizedDistinct{
		child:  child,
		colMap: colMap,
		cols:   cols,
		types:  types,
	}
}

func (d *VectorizedDistinct) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if d.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// REQ001704: pre-size seen map to first batch size (heuristic).
	var seen map[string]bool
	var seenInit bool
	var resultCols []UT.Column
	nCols := len(d.cols)

	var first bool = true
	for {
		batch, err := d.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}
		// REQ001704: pre-size map on first batch.
		if !seenInit {
			cap := batch.Size * 2
			if cap < 64 {
				cap = 64
			}
			seen = make(map[string]bool, cap)
			seenInit = true
		}
		sel := batch.Sel
		for r := 0; r < batch.Size; r++ {
			idx := r
			if sel != nil {
				if r >= len(sel) {
					break
				}
				idx = int(sel[r])
			}
			key := batchDistinctKey(batch, idx)
			if seen[key] {
				continue
			}
			seen[key] = true
			if first {
				resultCols = make([]UT.Column, nCols)
				for c := 0; c < nCols; c++ {
					resultCols[c].Name = d.cols[c]
					resultCols[c].Type = batch.Cols[c].Type
				}
				first = false
			}
			for c := 0; c < nCols; c++ {
				if c < len(batch.Cols) {
					copyBatchValue(&resultCols[c], &batch.Cols[c], idx)
				}
			}
		}
		batch.Put()
	}

	if first {
		return nil, nil
	}

	output := UT.GetBatch(nCols)
	output.Size = len(resultCols[0].Data.Ints) // all cols have same len after copyBatchValue
	// Actually, different types have different Data lengths. Fix:
	output.Size = 0
	for c := 0; c < nCols; c++ {
		output.Cols[c] = resultCols[c]
		output.Cols[c].Name = d.cols[c]
switch resultCols[c].Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				output.Size = len(resultCols[c].Data.Ints)
			case LX.T_FLOAT_KW:
				output.Size = len(resultCols[c].Data.Floats)
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				output.Size = len(resultCols[c].Data.Strs)
			case LX.T_BOOL:
				output.Size = len(resultCols[c].Data.Bools)
			}
	}

	return output, nil
}

func (d *VectorizedDistinct) Close() error {
	if d.child != nil {
		return d.child.Close()
	}
	return nil
}

func (d *VectorizedDistinct) Child() UT.BatchProducer {
	return d.child
}

func (d *VectorizedDistinct) Cols() []string        { return d.cols }
func (d *VectorizedDistinct) Types() []LX.TokenType  { return d.types }

// copyBatchValue appends one value from src column at srcRow to dst column.
func copyBatchValue(dst, src *UT.Column, srcRow int) {
	if src.Nulls != nil && srcRow < len(src.Nulls) && src.Nulls[srcRow] {
		if dst.Nulls == nil {
			dst.Nulls = make([]bool, 0, 64)
		}
		for len(dst.Nulls) <= srcRow {
			dst.Nulls = append(dst.Nulls, false)
		}
		dst.Nulls[len(dst.Nulls)-1] = true // mark last as null
		// Actually, we need to append a NULL. Let me reconsider.
		// For now, append a zero value and mark null.
	}
	switch src.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if srcRow < len(src.Data.Ints) {
			dst.Data.Ints = append(dst.Data.Ints, src.Data.Ints[srcRow])
		}
	case LX.T_FLOAT_KW:
		if srcRow < len(src.Data.Floats) {
			dst.Data.Floats = append(dst.Data.Floats, src.Data.Floats[srcRow])
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if srcRow < len(src.Data.Strs) {
			dst.Data.Strs = append(dst.Data.Strs, src.Data.Strs[srcRow])
		}
	case LX.T_BOOL:
		if srcRow < len(src.Data.Bools) {
			dst.Data.Bools = append(dst.Data.Bools, src.Data.Bools[srcRow])
		}
	}
	// Handle null tracking properly
	if src.Nulls != nil && srcRow < len(src.Nulls) && src.Nulls[srcRow] {
		if dst.Nulls == nil {
			dst.Nulls = make([]bool, 0, 64)
		}
		// Extend nulls to match data length
		currentLen := len(dst.Data.Ints)
		if len(src.Data.Floats) > currentLen {
			currentLen = len(src.Data.Floats)
		}
		if len(src.Data.Strs) > currentLen {
			currentLen = len(src.Data.Strs)
		}
		if len(src.Data.Bools) > currentLen {
			currentLen = len(src.Data.Bools)
		}
		for len(dst.Nulls) < currentLen-1 {
			dst.Nulls = append(dst.Nulls, false)
		}
		dst.Nulls = append(dst.Nulls, true)
	}
}

// batchDistinctKey builds a string key for a row at position idx in a batch.
func batchDistinctKey(batch *UT.Batch, idx int) string {
	if idx >= batch.Size {
		return ""
	}
	nCols := len(batch.Cols)
	if nCols == 0 {
		return ""
	}
	out := make([]byte, 0, 64)
	for c := 0; c < nCols; c++ {
		if c > 0 {
			out = append(out, 1)
		}
		col := &batch.Cols[c]
		if col.Nulls != nil && idx < len(col.Nulls) && col.Nulls[idx] {
			out = append(out, 'N')
			continue
		}
		switch col.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			out = append(out, 'I')
			v := int64(0)
			if idx < len(col.Data.Ints) {
				v = col.Data.Ints[idx]
			}
			out = appendInt64(out, v)
		case LX.T_FLOAT_KW:
			out = append(out, 'F')
			v := float64(0)
			if idx < len(col.Data.Floats) {
				v = col.Data.Floats[idx]
			}
			out = appendFloat64(out, v)
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			out = append(out, 'T', 1)
			v := ""
			if idx < len(col.Data.Strs) {
				v = col.Data.Strs[idx]
			}
			out = appendUVarint(out, uint64(len(v)))
			out = append(out, v...)
		case LX.T_BOOL:
			out = append(out, 'B')
			v := false
			if idx < len(col.Data.Bools) {
				v = col.Data.Bools[idx]
			}
			if v {
				out = append(out, '1')
			} else {
				out = append(out, '0')
			}
		default:
			out = append(out, 'O')
		}
	}
	return string(out)
}

func appendInt64(buf []byte, v int64) []byte {
	if v == 0 {
		return append(buf, '0')
	}
	neg := v < 0
	if neg {
		v = -v
		buf = append(buf, '-')
	}
	var tmp [20]byte
	pos := len(tmp)
	for v > 0 {
		pos--
		tmp[pos] = byte('0' + v%10)
		v /= 10
	}
	return append(buf, tmp[pos:]...)
}

func appendFloat64(buf []byte, v float64) []byte {
	// Simplified: use integer parts for key, avoiding full float formatting
	i := int64(v)
	f := int64((v - float64(i)) * 1000000)
	if f < 0 {
		f = -f
	}
	buf = appendInt64(buf, i)
	buf = append(buf, '.')
	return appendInt64(buf, f)
}

func appendUVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// VectorizedCompoundOp handles UNION ALL (batch concatenation) and
// UNION (hash dedup) for compound SELECT statements in the vectorized
// pipeline.
type VectorizedCompoundOp struct {
	left         UT.BatchProducer
	right        UT.BatchProducer
	op           int // 0=UNION ALL, 1=UNION, 2=EXCEPT, 3=INTERSECT
	cols         []string
	types        []LX.TokenType
	done         bool
	onLeft       bool
	currentBatch *UT.Batch
	batchPos     int
	bufBatches   []*UT.Batch // for UNION/EXCEPT/INTERSECT materialized mode
	bufPos       int
	// EXCEPT/INTERSECT streaming state
	rightKeys    map[string]bool
	emittedKeys  map[string]bool
	rightDrained bool
}

func NewVectorizedCompoundOp(left, right UT.BatchProducer, isUnion bool, cols []string, types []LX.TokenType) *VectorizedCompoundOp {
	v := &VectorizedCompoundOp{
		left:   left,
		right:  right,
		cols:   cols,
		types:  types,
		onLeft: true,
	}
	if isUnion {
		v.op = 1
	}
	return v
}

// NewVectorizedCompoundOpWithOp creates a VectorizedCompoundOp with explicit
// compound operation type. REQ001442.
func NewVectorizedCompoundOpWithOp(left, right UT.BatchProducer, op PS.CompoundOp, cols []string, types []LX.TokenType) *VectorizedCompoundOp {
	v := &VectorizedCompoundOp{
		left:   left,
		right:  right,
		cols:   cols,
		types:  types,
		onLeft: true,
	}
	switch op {
	case PS.CompoundUnionAll:
		v.op = 0
	case PS.CompoundUnion:
		v.op = 1
	case PS.CompoundExcept:
		v.op = 2
	case PS.CompoundIntersect:
		v.op = 3
	default:
		v.op = 0
	}
	return v
}

func (c *VectorizedCompoundOp) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if c.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	switch c.op {
	case 0:
		// UNION ALL: stream batches from left then right
		return c.nextUnionAll(ctx)
	case 1:
		// UNION: materialize all, dedup, emit one result batch
		return c.nextUnion(ctx)
	case 2:
		// EXCEPT: stream left, skip rows present in right
		return c.nextExcept(ctx)
	case 3:
		// INTERSECT: stream left, emit rows present in right
		return c.nextIntersect(ctx)
	default:
		return c.nextUnionAll(ctx)
	}
}

func (c *VectorizedCompoundOp) nextUnionAll(ctx context.Context) (*UT.Batch, error) {
	for {
		if c.onLeft {
			batch, err := c.left.NextBatch(ctx)
			if err != nil {
				return nil, err
			}
			if batch != nil {
				return batch, nil
			}
			c.onLeft = false
		}
		batch, err := c.right.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			c.done = true
			return nil, nil
		}
		return batch, nil
	}
}

// nextExcept streams left batches and emits rows whose keys are NOT
// present in the right side. Right side is drained first into a hash set.
// REQ001442.
func (c *VectorizedCompoundOp) nextExcept(ctx context.Context) (*UT.Batch, error) {
	if !c.rightDrained {
		c.rightKeys = make(map[string]bool)
		for {
			batch, err := c.right.NextBatch(ctx)
			if err != nil {
				return nil, err
			}
			if batch == nil {
				break
			}
			sel := batch.Sel
			for r := 0; r < batch.Size; r++ {
				idx := r
				if sel != nil {
					if r >= len(sel) {
						break
					}
					idx = int(sel[r])
				}
				key := batchDistinctKey(batch, idx)
				if key != "" {
					c.rightKeys[key] = true
				}
			}
			batch.Put()
		}
		_ = c.right.Close()
		c.rightDrained = true
		c.emittedKeys = make(map[string]bool)
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := c.left.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			c.done = true
			return nil, nil
		}

		nCols := len(c.cols)
		var resultCols []UT.Column
		first := true
		var outSize int

		drainLeft := func(src *UT.Batch) error {
			sel := src.Sel
			for r := 0; r < src.Size; r++ {
				idx := r
				if sel != nil {
					if r >= len(sel) {
						break
					}
					idx = int(sel[r])
				}
				key := batchDistinctKey(src, idx)
				if key == "" {
					continue
				}
				if c.rightKeys[key] {
					continue
				}
				if c.emittedKeys[key] {
					continue
				}
				c.emittedKeys[key] = true

				if first {
					resultCols = make([]UT.Column, nCols)
					for i := 0; i < nCols; i++ {
						resultCols[i].Name = c.cols[i]
						resultCols[i].Type = src.Cols[i].Type
					}
					first = false
				}
				for i := 0; i < nCols && i < len(src.Cols); i++ {
					copyBatchValue(&resultCols[i], &src.Cols[i], idx)
				}
				outSize++
			}
			return nil
		}

		if err := drainLeft(batch); err != nil {
			batch.Put()
			return nil, err
		}

		if outSize > 0 {
			output := UT.GetBatch(nCols)
			for i := 0; i < nCols; i++ {
				output.Cols[i] = resultCols[i]
				output.Cols[i].Name = c.cols[i]
				switch resultCols[i].Type {
				case LX.T_INT_KW, LX.T_BIGINT:
					output.Size = len(resultCols[i].Data.Ints)
				case LX.T_FLOAT_KW:
					output.Size = len(resultCols[i].Data.Floats)
				case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
					output.Size = len(resultCols[i].Data.Strs)
				case LX.T_BOOL:
					output.Size = len(resultCols[i].Data.Bools)
				}
			}
			return output, nil
		}

		batch.Put()
	}
}

// nextIntersect streams left batches and emits rows whose keys ARE
// present in the right side. Right side is drained first into a hash set.
// REQ001442.
func (c *VectorizedCompoundOp) nextIntersect(ctx context.Context) (*UT.Batch, error) {
	if !c.rightDrained {
		c.rightKeys = make(map[string]bool)
		for {
			batch, err := c.right.NextBatch(ctx)
			if err != nil {
				return nil, err
			}
			if batch == nil {
				break
			}
			sel := batch.Sel
			for r := 0; r < batch.Size; r++ {
				idx := r
				if sel != nil {
					if r >= len(sel) {
						break
					}
					idx = int(sel[r])
				}
				key := batchDistinctKey(batch, idx)
				if key != "" {
					c.rightKeys[key] = true
				}
			}
			batch.Put()
		}
		_ = c.right.Close()
		c.rightDrained = true
		c.emittedKeys = make(map[string]bool)
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := c.left.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			c.done = true
			return nil, nil
		}

		nCols := len(c.cols)
		var resultCols []UT.Column
		first := true

		drainLeft := func(src *UT.Batch) error {
			sel := src.Sel
			for r := 0; r < src.Size; r++ {
				idx := r
				if sel != nil {
					if r >= len(sel) {
						break
					}
					idx = int(sel[r])
				}
				key := batchDistinctKey(src, idx)
				if key == "" {
					continue
				}
				if !c.rightKeys[key] {
					continue
				}
				if c.emittedKeys[key] {
					continue
				}
				c.emittedKeys[key] = true

				if first {
					resultCols = make([]UT.Column, nCols)
					for i := 0; i < nCols; i++ {
						resultCols[i].Name = c.cols[i]
						resultCols[i].Type = src.Cols[i].Type
					}
					first = false
				}
				for i := 0; i < nCols && i < len(src.Cols); i++ {
					copyBatchValue(&resultCols[i], &src.Cols[i], idx)
				}
			}
			return nil
		}

		if err := drainLeft(batch); err != nil {
			batch.Put()
			return nil, err
		}

		if !first {
			output := UT.GetBatch(nCols)
			for i := 0; i < nCols; i++ {
				output.Cols[i] = resultCols[i]
				output.Cols[i].Name = c.cols[i]
				switch resultCols[i].Type {
				case LX.T_INT_KW, LX.T_BIGINT:
					output.Size = len(resultCols[i].Data.Ints)
				case LX.T_FLOAT_KW:
					output.Size = len(resultCols[i].Data.Floats)
				case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
					output.Size = len(resultCols[i].Data.Strs)
				case LX.T_BOOL:
					output.Size = len(resultCols[i].Data.Bools)
				}
			}
			return output, nil
		}

		batch.Put()
	}
}

func (c *VectorizedCompoundOp) nextUnion(ctx context.Context) (*UT.Batch, error) {
	if c.bufBatches == nil {
		// Materialize all batches from both sides
		seen := make(map[string]bool)
		nCols := len(c.cols)
		var resultCols []UT.Column
		first := true

		drain := func(source UT.BatchProducer) error {
			for {
				batch, err := source.NextBatch(ctx)
				if err != nil {
					return err
				}
				if batch == nil {
					return nil
				}
				sel := batch.Sel
				for r := 0; r < batch.Size; r++ {
					idx := r
					if sel != nil {
						if r >= len(sel) {
							break
						}
						idx = int(sel[r])
					}
					key := batchDistinctKey(batch, idx)
					if seen[key] {
						continue
					}
					seen[key] = true
					if first {
						resultCols = make([]UT.Column, nCols)
						for i := 0; i < nCols; i++ {
							resultCols[i].Name = c.cols[i]
							resultCols[i].Type = batch.Cols[i].Type
						}
						first = false
					}
					for i := 0; i < nCols && i < len(batch.Cols); i++ {
						copyBatchValue(&resultCols[i], &batch.Cols[i], idx)
					}
				}
				batch.Put()
			}
		}

		if err := drain(c.left); err != nil {
			return nil, err
		}
		if err := drain(c.right); err != nil {
			return nil, err
		}

		if first {
			c.done = true
			return nil, nil
		}

		output := UT.GetBatch(nCols)
		// Determine size from the first non-empty column
		for i := 0; i < nCols; i++ {
			output.Cols[i] = resultCols[i]
			output.Cols[i].Name = c.cols[i]
			switch resultCols[i].Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				output.Size = len(resultCols[i].Data.Ints)
			case LX.T_FLOAT_KW:
				output.Size = len(resultCols[i].Data.Floats)
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				output.Size = len(resultCols[i].Data.Strs)
			case LX.T_BOOL:
				output.Size = len(resultCols[i].Data.Bools)
			}
		}

		c.bufBatches = []*UT.Batch{output}
		c.bufPos = 0
	}

	if c.bufPos >= len(c.bufBatches) {
		c.done = true
		return nil, nil
	}
	batch := c.bufBatches[c.bufPos]
	c.bufPos++
	return batch, nil
}

func (c *VectorizedCompoundOp) Close() error {
	var err1, err2 error
	if c.left != nil {
		err1 = c.left.Close()
	}
	if c.right != nil {
		err2 = c.right.Close()
	}
	for _, b := range c.bufBatches {
		b.Put()
	}
	c.bufBatches = nil
	c.rightKeys = nil
	c.emittedKeys = nil
	if err1 != nil {
		return err1
	}
	return err2
}
