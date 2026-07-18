package OP

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// PragmaResult returns a single-row result for PRAGMA queries (REQ000242).
type PragmaResult struct {
	name   string
	value  string
	intVal int64
	asInt  bool
	done   bool
}

// NewPragmaResult creates a PragmaResult operator.
func NewPragmaResult(name, value string) *PragmaResult {
	return &PragmaResult{name: name, value: value}
}

// NewPragmaIntResult creates a PragmaResult that returns an integer value.
func NewPragmaIntResult(name string, value int64) *PragmaResult {
	return &PragmaResult{name: name, intVal: value, asInt: true}
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
	if p.asInt {
		return Row{
			Cols: []string{p.name},
			Data: []Value{DT.NewIntValue(p.intVal)},
		}, nil
	}
	return Row{
		Cols: []string{p.name},
		Data: []Value{DT.NewTextValue(val)},
	}, nil
}

func (p *PragmaResult) Close() error { return nil }

// IncrementalVacuumResult is the PRAGMA incremental_vacuum(N) operator.
// REQ001306: triggers a manual compaction pass and returns the count
// of compaction steps enqueued. The async compaction loop will reclaim
// pages in the background; the returned int reflects the synchronous
// work (always 0 for now — async reclaim is observed via subsequent
// PRAGMA integrity_check).
type IncrementalVacuumResult struct {
	store DT.Store
	done  bool
}

// NewIncrementalVacuumResult creates an operator for incremental_vacuum.
func NewIncrementalVacuumResult(store DT.Store) *IncrementalVacuumResult {
	return &IncrementalVacuumResult{store: store}
}

func (v *IncrementalVacuumResult) Next(_ context.Context) (Row, error) {
	if v.done {
		return Row{}, ErrNoRows
	}
	v.done = true
	// ManualCompact is part of the DT.Store interface (REQ000257).
	// Best-effort kick-off; errors are swallowed because
	// incremental_vacuum is a hint, not a guarantee.
	if v.store != nil {
		_ = v.store.ManualCompact()
	}
	return Row{
		Cols: []string{"incremental_vacuum"},
		Data: []Value{DT.NewIntValue(0)},
	}, nil
}

func (v *IncrementalVacuumResult) Close() error { return nil }
