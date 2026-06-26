package EX

import (
	"context"
	"sync"
)

// PragmaListener is notified when a PRAGMA value changes (REQ000734).
// Subsystems register listeners to react to configuration changes.
type PragmaListener interface {
	OnPragmaChange(name, value string)
}

var (
	pragmaListeners []PragmaListener
	pragmaMu        sync.RWMutex
)

// RegisterPragmaListener adds a listener for PRAGMA changes.
func RegisterPragmaListener(l PragmaListener) {
	pragmaMu.Lock()
	defer pragmaMu.Unlock()
	pragmaListeners = append(pragmaListeners, l)
}

// UnregisterAllPragmaListeners clears all listeners (for testing).
func UnregisterAllPragmaListeners() {
	pragmaMu.Lock()
	defer pragmaMu.Unlock()
	pragmaListeners = nil
}

// notifyPragmaChange calls all registered listeners.
func notifyPragmaChange(name, value string) {
	pragmaMu.RLock()
	defer pragmaMu.RUnlock()
	for _, l := range pragmaListeners {
		l.OnPragmaChange(name, value)
	}
}

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
		Data: []Value{NewTextValue(val)},
	}, nil
}

func (p *PrAGMAResult) Close() error { return nil }
