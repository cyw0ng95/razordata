package OP

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

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
	closed    atomic.Bool
}

func (p *Project) Child() Operator               { return p.child }
func (p *Project) SetChild(c Operator)           { p.child = c }
func (p *Project) Cols() []PS.Expr               { return p.cols }
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
	// REQ001288: trust the pool's default capacity (512 values).
	// This covers most queries without growth; larger result sets
	// grow by doubling via Next(). The old dataPerRow × 1024
	// prealloc wasted ~170MB on multi-column queries with small
	// result sets (select2).
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
	return p.child.Close()
}