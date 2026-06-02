package EX

import (
	"context"

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
	return Row{}, ErrNotImplemented
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
	return Row{}, ErrNotImplemented
}

func (p *Project) Close() error {
	return p.child.Close()
}

type Sort struct {
	child Operator
	keys  []PS.OrderItem
}

func NewSort(child Operator, keys []PS.OrderItem) *Sort {
	return &Sort{
		child: child,
		keys:  keys,
	}
}

func (s *Sort) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNotImplemented
}

func (s *Sort) Close() error {
	return s.child.Close()
}

type Limit struct {
	child Operator
	n     PS.Expr
}

func NewLimit(child Operator, n PS.Expr) *Limit {
	return &Limit{
		child: child,
		n:     n,
	}
}

func (l *Limit) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNotImplemented
}

func (l *Limit) Close() error {
	return l.child.Close()
}
