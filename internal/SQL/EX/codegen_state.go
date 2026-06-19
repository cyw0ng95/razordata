package EX

import (
	"context"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// CodegenState holds the runtime state for a codegen-specialized operator.
// It wraps an existing operator and can swap from interpreted to compiled.
type CodegenState struct {
	inner   Operator
	fn      func(ctx context.Context, batch *Batch, params []any) (*Batch, error)
	swapped atomic.Bool
}

// NewCodegenState creates a codegen state wrapping an interpreted operator.
func NewCodegenState(inner Operator) *CodegenState {
	return &CodegenState{inner: inner}
}

// IsSwapped returns true if the operator has been specialized.
func (cs *CodegenState) IsSwapped() bool {
	return cs.swapped.Load()
}

// Swap installs a specialized batch function, switching from interpreted
// to compiled mode. Thread-safe: uses atomic store.
func (cs *CodegenState) Swap(fn func(ctx context.Context, batch *Batch, params []any) (*Batch, error)) {
	cs.fn = fn
	cs.swapped.Store(true)
}

// ExecBatch runs either the compiled or interpreted path.
func (cs *CodegenState) ExecBatch(ctx context.Context, batch *Batch, params []any) (*Batch, error) {
	if cs.swapped.Load() && cs.fn != nil {
		return cs.fn(ctx, batch, params)
	}
	return cs.interpretBatch(ctx, batch, params)
}

func (cs *CodegenState) interpretBatch(ctx context.Context, batch *Batch, params []any) (*Batch, error) {
	_ = params
	return batch, nil
}

// WithCodegen attaches a CodegenState to a Filter operator.
// Returns the same operator with codegen capability wired in.
func (f *Filter) WithCodegen() *Filter {
	f.cs = NewCodegenState(f)
	return f
}

// WithCodegen attaches a CodegenState to a Project operator.
func (p *Project) WithCodegen() *Project {
	p.cs = NewCodegenState(p)
	return p
}

// WithCodegen attaches a CodegenState to a Sort operator.
func (s *Sort) WithCodegen() *Sort {
	s.cs = NewCodegenState(s)
	return s
}

// WithCodegen attaches a CodegenState to a Limit operator.
func (l *Limit) WithCodegen() *Limit {
	l.cs = NewCodegenState(l)
	return l
}

// WithCodegenForOp attaches a CodegenState to any operator that supports it.
func WithCodegenForOp(op Operator) Operator {
	switch v := op.(type) {
	case *Filter:
		return v.WithCodegen()
	case *Project:
		return v.WithCodegen()
	case *Sort:
		v.WithCodegen()
		return v
	case *Limit:
		return v.WithCodegen()
	default:
		return op
	}
}

// Ensure CodegenState references are used.
var _ = PS.NumberLiteral{}
var _ context.Context
