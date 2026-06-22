package EX

import (
	"context"
	"fmt"
	"slices"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type Filter struct {
	child     Operator
	predicate PS.Expr
	params    []any
	execCtx   *ExecContext
	// REQ000757: curRow avoids heap-escape of local row variable
	// when passing &row to Eval. Filter is heap-allocated, so
	// &f.curRow is already a heap pointer — no escape needed.
	curRow Row
}

// Child returns the filter's child operator. Used by
// propagateParams to walk the operator tree.
func (f *Filter) Child() Operator { return f.child }

// Predicate returns the filter's predicate expression.
func (f *Filter) Predicate() PS.Expr { return f.predicate }

func NewFilter(child Operator, predicate PS.Expr) *Filter {
	return &Filter{
		child:     child,
		predicate: predicate,
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
		r, err := f.child.Next(ctx)
		if err != nil {
			return Row{}, err
		}
		if f.execCtx != nil {
			r.execCtx = f.execCtx
		}
		if f.predicate == nil {
			return r, nil
		}
		// REQ000757: copy into f.curRow instead of taking &r.
		// f.curRow is on the heap (Filter struct), so &f.curRow
		// is already a heap pointer — Eval won't force escape.
		f.curRow = r
		v, err := Eval(f.predicate, &f.curRow, f.params)
		if err != nil {
			return Row{}, err
		}
		if truthy(v) {
			return f.curRow, nil
		}
	}
}

func (f *Filter) Close() error {
	return f.child.Close()
}

type Project struct {
	child     Operator
	cols      []PS.Expr
	params    []any
	// REQ000756: pre-allocated column names (same for every row).
	prefixCols []string
}

// Child returns the project's child operator.
func (p *Project) Child() Operator { return p.child }

func NewProject(child Operator, cols []PS.Expr) *Project {
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
	return &Project{
		child:      child,
		cols:       cols,
		prefixCols: prefixCols,
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
	if isStar(p.cols) {
		return row, nil
	}
	// REQ000756: use pre-computed column names, allocate only data.
	out := Row{
		Cols: append([]string(nil), p.prefixCols...),
		Data: make([]any, len(p.cols)),
	}
	for i, c := range p.cols {
		var v any
		var err error
		if wf, ok := c.(*PS.WindowFunc); ok {
			v, err = findColumn(row, wf.Name)
			if err != nil {
				return Row{}, err
			}
		} else {
			v, err = Eval(c, &row, p.params)
			if err != nil {
				return Row{}, err
			}
		}
		out.Data[i] = v
	}
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
	return p.child.Close()
}

type Sort struct {
	child        Operator
	keys         []PS.OrderItem
	buf          []Row
	pos          int
	materialized bool
	params       []any
}

// Child returns the sort's child operator.
func (s *Sort) Child() Operator { return s.child }

func NewSort(child Operator, keys []PS.OrderItem) *Sort {
	return &Sort{child: child, keys: keys}
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
		n := len(s.buf)
		keyCache := make([][]any, n)
		for i, r := range s.buf {
			sk := make([]any, len(s.keys))
			for j, k := range s.keys {
				v, err := Eval(k.Expr, &r, s.params)
				if err != nil {
					return Row{}, err
				}
				sk[j] = v
			}
			keyCache[i] = sk
		}

		indices := make([]int, n)
		for i := range indices {
			indices[i] = i
		}
		slices.SortStableFunc(indices, func(ai, bi int) int {
			ka, kb := keyCache[ai], keyCache[bi]
			for ki := range ka {
				if s.keys[ki].NullsOrder != 0 {
					if ka[ki] == nil && kb[ki] != nil {
						return -int(s.keys[ki].NullsOrder)
					}
					if kb[ki] == nil && ka[ki] != nil {
						return int(s.keys[ki].NullsOrder)
					}
				}
				c := compare(ka[ki], kb[ki])
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

		// Reorder s.buf in-place using sorted indices.
		reordered := make([]Row, n)
		for i, idx := range indices {
			reordered[i] = s.buf[idx]
		}
		s.buf = reordered
		s.materialized = true
	}
	if s.pos >= len(s.buf) {
		return Row{}, ErrNoRows
	}
	r := s.buf[s.pos]
	s.pos++
	return r, nil
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
