package EX

import (
	"context"
	"sort"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type Filter struct {
	child     Operator
	predicate PS.Expr
	params    []interface{}
	cs        *CodegenState
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
func (f *Filter) WithParams(p []interface{}) Operator {
	f.params = p
	return f
}

func (f *Filter) Next(ctx context.Context) (Row, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		row, err := f.child.Next(ctx)
		if err != nil {
			return Row{}, err
		}
		if f.predicate == nil {
			return row, nil
		}
		v, err := Eval(f.predicate, &row, f.params)
		if err != nil {
			return Row{}, err
		}
		if truthy(v) {
			return row, nil
		}
	}
}

func (f *Filter) Close() error {
	return f.child.Close()
}

type Project struct {
	child  Operator
	cols   []PS.Expr
	params []interface{}
	cs     *CodegenState
}

// Child returns the project's child operator.
func (p *Project) Child() Operator { return p.child }

func NewProject(child Operator, cols []PS.Expr) *Project {
	return &Project{
		child: child,
		cols:  cols,
	}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (p *Project) WithParams(p2 []interface{}) Operator {
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
	out := Row{Cols: make([]string, 0, len(p.cols))}
	for _, c := range p.cols {
		var name string
		switch e := c.(type) {
		case *PS.Ident:
			name = e.Name
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
		}
		v, err := Eval(c, &row, p.params)
		if err != nil {
			return Row{}, err
		}
		if a, ok := c.(*PS.AliasedExpr); ok {
			name = a.Alias
		}
		out.Cols = append(out.Cols, name)
		out.Data = append(out.Data, v)
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

func (p *Project) Close() error {
	return p.child.Close()
}

type Sort struct {
	child        Operator
	keys         []PS.OrderItem
	buf          []Row
	pos          int
	materialized bool
	params       []interface{}
	cs           *CodegenState
}

// Child returns the sort's child operator.
func (s *Sort) Child() Operator { return s.child }

func NewSort(child Operator, keys []PS.OrderItem) *Sort {
	return &Sort{child: child, keys: keys}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (s *Sort) WithParams(p []interface{}) Operator {
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

		// REQ000580: pre-extract sort keys so each expression is
		// evaluated exactly once per row (O(N*K)) rather than
		// inside the comparator which does O(N log N * K).
		type sortRow struct {
			row  Row
			keys []interface{}
		}
		sorted := make([]sortRow, len(s.buf))
		for i, r := range s.buf {
			sk := make([]interface{}, len(s.keys))
			for j, k := range s.keys {
				v, err := Eval(k.Expr, &r, s.params)
				if err != nil {
					return Row{}, err
				}
				sk[j] = v
			}
			sorted[i] = sortRow{row: r, keys: sk}
		}

		sort.SliceStable(sorted, func(i, j int) bool {
			a := sorted[i].keys
			b := sorted[j].keys
			for ki := range a {
				c := compare(a[ki], b[ki])
				if c == 0 {
					continue
				}
				if s.keys[ki].Desc {
					return c > 0
				}
				return c < 0
			}
			return false
		})

		s.buf = make([]Row, len(sorted))
		for i, sr := range sorted {
			s.buf[i] = sr.row
		}
		sorted = nil // allow GC
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
	params []interface{}
	cs     *CodegenState
}

// Child returns the limit's child operator.
func (l *Limit) Child() Operator { return l.child }

// LimitValue returns the limit value.
func (l *Limit) LimitValue() int64 { return l.limit }

func NewLimit(child Operator, n int64) *Limit {
	return &Limit{child: child, limit: n}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (l *Limit) WithParams(p []interface{}) Operator {
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
	params  []interface{}
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
func (o *Offset) WithParams(p []interface{}) Operator {
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
