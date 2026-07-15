package OP

import (
	"context"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// fusedRowThreshold is the maximum table row count at which FusedScan
// is preferred over the standard SeqScan→Filter→Project chain. For
// tables larger than this, the overhead of reading the entire table
// into memory outweighs the benefit of virtual dispatch elimination.
// REQ001463.
const fusedRowThreshold = 1000

// FusedScan is a fast-path operator that inlines SeqScan+Filter+Project
// into a single tight loop for small tables (<1000 rows). It holds:
//   - table: the table name to scan from DT.Tables
//   - filterFn: compiled predicate (nil if no filter)
//   - projectFn: compiled projection (nil for `SELECT *`)
//   - cols/types: pre-computed output column metadata
//   - dataBuf: reused []Value scratch buffer for projections
//
// On Next(), FusedScan reads each table row in order, calls filterFn,
// and if passing, emits a row via projectFn (or passthrough). This
// eliminates 3 virtual dispatches (SeqScan.Next, Filter.Next,
// Project.Next) per row, reducing per-row overhead from ~500ns to
// ~50ns for tables with 20 rows.
//
// Trade-offs: FusedScan materializes no batches; Sort/Limit above it
// work the same. FusedScan only applies to in-memory SeqScan (not
// IndexScan, not store-backed). For larger tables or store-backed
// scans, the planner falls through to Filter+Project. REQ001463.
type FusedScan struct {
	table        string
	src          []DT.Row
	pos          int
	filterExpr   PS.Expr // raw predicate expression (nil if no filter)
	projectExprs []PS.Expr // raw projection expressions (nil for SELECT *)
	filterFn     func(*Row) (bool, error)
	projectFn    func(*DT.Row) (DT.Row, error)
	cols         []string
	types        []LX.TokenType
	colIndex     map[string]int
	dataBuf      []DT.Value
	srcCols      []string
	srcTypes     []LX.TokenType
	srcColIdx    map[string]int
	ctxCheckCounter uint64
	execCtx      *pl.ExecContext
	closed       atomic.Bool
}

// Child returns nil — FusedScan has no child.
func (f *FusedScan) Child() Operator               { return nil }
func (f *FusedScan) SetChild(c Operator)           {}
func (f *FusedScan) Cols() []PS.Expr              { return nil }
func (f *FusedScan) SetExecCtx(ec *pl.ExecContext) { f.execCtx = ec }

// Table returns the table name for EXPLAIN. REQ001463.
func (f *FusedScan) Table() string { return f.table }

// HasFilter reports whether this FusedScan has a compiled predicate.
func (f *FusedScan) HasFilter() bool { return f.filterFn != nil }

// HasProjection reports whether this FusedScan has a compiled projection.
func (f *FusedScan) HasProjection() bool { return f.projectFn != nil }

// CountComparisonLiterals returns how many comparison-literal values
// this FusedScan's filter would consume when normalized. REQ001463.
func (f *FusedScan) CountComparisonLiterals() int {
	var count int
	countComparisonLiteralsWalk(f.filterExpr, &count)
	return count
}

// ReplaceLiterals replaces comparison-literal values in the filter
// expression with values from vals. REQ001463.
func (f *FusedScan) ReplaceLiterals(vals []any) {
	n := f.CountComparisonLiterals()
	if n > len(vals) {
		n = len(vals)
	}
	remaining := vals[:n]
	f.filterExpr = replaceComparisonLiterals(f.filterExpr, &remaining)
}

// NewFusedScan constructs a FusedScan for the given table and optional
// predicate/projection. Pass nil for filter to skip filtering, nil
// for project to emit all columns. Compilation is deferred to Open()
// so that replaceLiteralsOnTree can update the PS.Expr nodes before
// the compiled functions are created. REQ001463.
func NewFusedScan(table string, filter PS.Expr, project []PS.Expr) *FusedScan {
	f := &FusedScan{
		table:        table,
		filterExpr:   filter,
		projectExprs: project,
		cols:         []string{table + ".*"},
		types:        []LX.TokenType{},
	}
	// Validate that filter expression can be compiled (defer actual
	// compilation to Open()).
	if filter != nil {
		if compileFilterExpr(filter) == nil {
			return nil
		}
	}
	if project != nil {
		hasStar := false
		for _, c := range project {
			if _, ok := c.(*PS.StarExpr); ok {
				hasStar = true
				break
			}
		}
		if !hasStar && len(project) > 0 {
			if compileProjectExprs(project) == nil {
				return nil
			}
		}
	}
	return f
}

// compileProjectExprs compiles a list of project expressions into a
// single function. Returns nil if any expression is not supported.
// Handles column references (Ident/QualifiedName) by runtime
// lookup via the row's ColIndex, and arithmetic (BinaryExpr) by
// delegating to compileRowExpr which uses Ident.SlotIdx.
// Falls back to nil on unsupported expressions.
func compileProjectExprs(cols []PS.Expr) func(*DT.Row) (DT.Row, error) {
	if len(cols) == 0 {
		return nil
	}
	type resolved struct {
		colName string
		fallback bool // if true, use compileRowExpr with SlotIdx
		expr    PS.Expr
	}
	items := make([]resolved, len(cols))
	hasArithmetic := false
	for i, c := range cols {
		switch v := c.(type) {
		case *PS.Ident:
			items[i] = resolved{colName: v.Name}
		case *PS.QualifiedName:
			items[i] = resolved{colName: v.Table + "." + v.Name}
		case *PS.AliasedExpr:
			items[i] = resolved{fallback: true, expr: v.Expr}
			hasArithmetic = true
		case *PS.BinaryExpr:
			items[i] = resolved{fallback: true, expr: v}
			hasArithmetic = true
		default:
			return nil
		}
	}

	// Pre-compile arithmetic expressions if any.
	compiledFn := func(in *DT.Row) DT.Value { return DT.Value{Kind: AP.KindNull} }
	if hasArithmetic {
		if cf := compileRowExpr(items[0].expr); cf != nil && len(items) == 1 {
			compiledFn = cf
			items = items[:0]
		} else {
			// Mixed: must fall back to plain project path. Return nil
			// so the planner uses SeqScan → Project.
			return nil
		}
	}
	if hasArithmetic && len(items) == 0 {
		// Single arithmetic expression, whole-output.
		return func(in *DT.Row) (DT.Row, error) {
			out := DT.Row{
				Cols:     []string{in.Cols[0]},
				Types:    in.Types,
				ColIndex: in.ColIndex,
			}
			out.Data = []DT.Value{compiledFn(in)}
			return out, nil
		}
	}

	return func(in *DT.Row) (DT.Row, error) {
		out := DT.Row{
			Cols:     make([]string, len(items)),
			Types:    in.Types,
			ColIndex: in.ColIndex,
		}
		out.Data = make([]DT.Value, len(items))
		for i, it := range items {
			out.Cols[i] = it.colName
			// Resolve column index: prefer ColIndex, fall back
			// to linear scan of Cols for rows without ColIndex
			// (e.g. rows stored in DT.Tables have empty ColIndex).
			var val DT.Value
			if in.ColIndex != nil && len(in.ColIndex) > 0 {
				if idx, ok := in.ColIndex[it.colName]; ok && idx >= 0 && idx < len(in.Data) {
					val = in.Data[idx]
				}
			} else {
				for ci, cn := range in.Cols {
					if cn == it.colName {
						val = in.Data[ci]
						break
					}
				}
			}
			out.Data[i] = val
		}
		return out, nil
	}
}

// snapshotTable captures a fresh copy of the table data and column
// metadata. Called on first Next() after construction or Close().
// REQ001463.
func (f *FusedScan) snapshotTable() {
	DT.TablesMu.RLock()
	defer DT.TablesMu.RUnlock()
	src := DT.Tables[f.table]
	if len(src) == 0 {
		f.src = nil
		return
	}
	if len(src) > fusedRowThreshold {
		f.src = nil
		return
	}
	f.src = make([]DT.Row, len(src))
	copy(f.src, src)
	if len(src) > 0 {
		first := f.src[0]
		if len(first.Cols) > 0 {
			f.srcCols = first.Cols
			f.srcTypes = first.Types
			f.srcColIdx = first.ColIndex
		}
	}
}

// ensureCompiled lazily compiles filter and projection from the
// stored PS.Expr on first Next() each execution cycle. REQ001463.
func (f *FusedScan) ensureCompiled() {
	if f.filterFn == nil && f.filterExpr != nil {
		f.filterFn = compileFilterExpr(f.filterExpr)
	}
	if f.projectFn == nil && f.projectExprs != nil {
		hasStar := false
		for _, c := range f.projectExprs {
			if _, ok := c.(*PS.StarExpr); ok {
				hasStar = true
				break
			}
		}
		if !hasStar && len(f.projectExprs) > 0 {
			f.projectFn = compileProjectExprs(f.projectExprs)
		}
	}
}

// Next implements the fused scan loop. REQ001463.
func (f *FusedScan) Next(ctx context.Context) (DT.Row, error) {
	if f.closed.Load() {
		return DT.Row{}, ErrNoRows
	}
	f.ensureCompiled()
	if f.src == nil {
		f.snapshotTable()
	}
	for {
		// REQ001277: batched ctx.Err() check.
		f.ctxCheckCounter++
		if f.ctxCheckCounter >= 1024 {
			f.ctxCheckCounter = 0
			if err := ctx.Err(); err != nil {
				return DT.Row{}, err
			}
		}
		if f.src == nil || f.pos >= len(f.src) {
			return DT.Row{}, ErrNoRows
		}
		// Copy row by value so downstream operators hold a
		// stable copy even if the source []DT.Row is mutated.
		row := f.src[f.pos]
		f.pos++
		if f.filterFn != nil {
			ok, err := f.filterFn(&row)
			if err != nil {
				return DT.Row{}, err
			}
			if !ok {
				continue
			}
		}
		if f.execCtx != nil {
			row.ExecCtx = f.execCtx
		}
		if f.projectFn == nil {
			// No projection: emit the row as-is.
			return row, nil
		}
		out, err := f.projectFn(&row)
		if err != nil {
			return DT.Row{}, err
		}
		return out, nil
	}
}

// Open is a no-op — initialization is lazy in Next(). REQ001463.
func (f *FusedScan) Open() error { return nil }

// Close resets the scan cursor, clears the snapshot, and invalidates
// the compiled functions so the next Next() call recompiles from the
// (possibly updated) PS.Expr and re-snapshots. REQ001463.
func (f *FusedScan) Close() error {
	f.closed.Store(false)
	f.pos = 0
	f.src = nil
	f.filterFn = nil
	f.projectFn = nil
	return nil
}
