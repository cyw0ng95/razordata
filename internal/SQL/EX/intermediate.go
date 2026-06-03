package EX

import (
	"context"
	"sort"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type Filter struct {
	child     Operator
	predicate PS.Expr
}

func NewFilter(child Operator, predicate PS.Expr) *Filter {
	return &Filter{
		child:     child,
		predicate: predicate,
	}
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
		v, err := Eval(f.predicate, &row, nil)
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
	child Operator
	cols  []PS.Expr
}

func NewProject(child Operator, cols []PS.Expr) *Project {
	return &Project{
		child: child,
		cols:  cols,
	}
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
		}
		v, err := Eval(c, &row, nil)
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
}

func NewSort(child Operator, keys []PS.OrderItem) *Sort {
	return &Sort{child: child, keys: keys}
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
		sort.SliceStable(s.buf, func(i, j int) bool {
			a := s.buf[i]
			b := s.buf[j]
			for _, k := range s.keys {
				av, _ := Eval(k.Expr, &a, nil)
				bv, _ := Eval(k.Expr, &b, nil)
				c := compare(av, bv)
				if c == 0 {
					continue
				}
				if k.Desc {
					return c > 0
				}
				return c < 0
			}
			return false
		})
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
	child Operator
	limit int64
	seen  int64
}

func NewLimit(child Operator, n int64) *Limit {
	return &Limit{child: child, limit: n}
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
