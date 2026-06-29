package OP

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// PragmaResult returns a single-row result for PRAGMA queries (REQ000242).
type PragmaResult struct {
	name  string
	value string
	done  bool
}

// NewPragmaResult creates a PragmaResult operator.
func NewPragmaResult(name, value string) *PragmaResult {
	return &PragmaResult{name: name, value: value}
}

func (p *PragmaResult) Next(_ context.Context) (Row, error) {
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
		Data: []Value{DT.NewTextValue(val)},
	}, nil
}

func (p *PragmaResult) Close() error { return nil }
