package OP

import (
	"context"
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
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
	source  Operator
	schema  []string
	types   []LX.TokenType
	colMap  map[string]int
	current *UT.Batch
	done    bool
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

// NextBatch produces the next batch. Returns (nil, nil) at EOF.
// Caller is responsible for calling Put() on each non-nil batch.
func (v *VectorizedSeqScan) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if v.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	batch := UT.GetBatch(len(v.schema))
	batch.Size = 0
	// Install column names and colMap for O(1) filter lookup
	for i, name := range v.schema {
		batch.SetColumnName(i, name)
	}
	batch.SetColMap(v.colMap)

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
		// Project row into columnar layout
		for i, colName := range v.schema {
			val, ok := row.Lookup(colName)
			if !ok {
				val = nil
			}
			isNull := val == nil
			batch.AppendRow(i, v.types[i], val, isNull)
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

// VectorizedFilter applies a predicate to batches from a child
// vectorized source, producing filtered batches with selection
// vectors. Uses EvalBatch for batch-level predicate evaluation.
// REQ000144 satisfied: Vectorized Filter that processes batches
// using selection vectors (no data copying).
type VectorizedFilter struct {
	child  *VectorizedSeqScan
	pred   PS.Expr
	params []any
}

// NewVectorizedFilter creates a vectorized filter.
func NewVectorizedFilter(child *VectorizedSeqScan, pred PS.Expr) *VectorizedFilter {
	return &VectorizedFilter{child: child, pred: pred}
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
func (f *VectorizedFilter) NextBatch(ctx context.Context) (*UT.Batch, error) {
	for {
		batch, err := f.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, nil // EOF
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
		batch.Size = len(sel) // logical size = selection count
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

// SchemaFromRowSchema converts a Row's Types to []LX.TokenType.
func SchemaFromRowSchema(types []LX.TokenType) []LX.TokenType {
	return append([]LX.TokenType(nil), types...)
}

// VectorizedProject applies expression projections to batches from
// a child BatchProducer, producing a new batch with the projected
// columns. Each expression is evaluated over the child batch to
// produce one output column.
// REQ001212 satisfied: vectorized projection operator.
type VectorizedProject struct {
	child UT.BatchProducer
	exprs []PS.Expr
	names []string
	done  bool
}

// NewVectorizedProject creates a vectorized projection operator.
func NewVectorizedProject(child UT.BatchProducer, exprs []PS.Expr, names []string) *VectorizedProject {
	return &VectorizedProject{
		child: child,
		exprs: exprs,
		names: names,
	}
}

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

	n := childBatch.LogicalSize()
	output := UT.GetBatch(len(p.exprs))
	output.Size = n
	for i, expr := range p.exprs {
		col := EV.EvalBatchExpr(expr, childBatch, nil)
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
