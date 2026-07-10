package OP

import (
	"context"
	"sync/atomic"

	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

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