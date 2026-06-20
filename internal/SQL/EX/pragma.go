package EX

import (
	"context"
)

// PrAGMAResult returns a single-row result for PRAGMA queries (REQ000242).
type PrAGMAResult struct {
	name  string
	value string
	done  bool
}

// NewPragmaResult creates a PragmaResult operator.
func NewPragmaResult(name, value string) *PrAGMAResult {
	return &PrAGMAResult{name: name, value: value}
}

func (p *PrAGMAResult) Next(_ context.Context) (Row, error) {
	if p.done {
		return Row{}, ErrNoRows
	}
	p.done = true
	val := p.value
	if val == "" {
		val = "(default)"
	}
	return Row{
		Cols: []string{p.name},
		Data: []any{val},
	}, nil
}

func (p *PrAGMAResult) Close() error { return nil }
