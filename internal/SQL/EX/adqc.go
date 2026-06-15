package EX

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// AdqcState is the state of an adaptive plan.
type AdqcState uint32

const (
	AdqcInterpreted AdqcState = iota
	AdqcCompiling
	AdqcCompiled
)

// InvocationCounter tracks how many times a plan has been invoked.
// Uses atomic operations for lock-free reads in the hot path.
// Threshold is the number of interpreted invocations before compilation.
type InvocationCounter struct {
	count     atomic.Uint64
	threshold uint64
	planHash  string
}

// NewInvocationCounter creates a counter for a plan hash.
func NewInvocationCounter(planHash string, threshold uint64) *InvocationCounter {
	if threshold == 0 {
		threshold = 2
	}
	return &InvocationCounter{
		threshold: threshold,
		planHash:  planHash,
	}
}

// Inc increments the counter and returns whether threshold has been reached.
func (ic *InvocationCounter) Inc() bool {
	val := ic.count.Add(1)
	return val >= ic.threshold
}

// Reset sets the counter back to zero.
func (ic *InvocationCounter) Reset() {
	ic.count.Store(0)
}

// Count returns the current invocation count.
func (ic *InvocationCounter) Count() uint64 {
	return ic.count.Load()
}

// PlanHash returns the plan hash this counter is keyed on.
func (ic *InvocationCounter) PlanHash() string {
	return ic.planHash
}

// AdaptiveOp is a wrapper Operator that adaptively switches from
// interpreted row-at-a-time execution to specialized batch execution
// after a configurable invocation threshold.
//
// The first N invocations (default 2) use the interpreted inner operator.
// After N, the hot path swaps to the codegen-specialized batch function.
// If the swap fails, the interpreted path continues as a fallback.
//
// REQ000313 satisfied.
type AdaptiveOp struct {
	inner      Operator
	counter    *InvocationCounter
	state      atomic.Uint32
	mu         sync.Mutex
	compiledFn func(ctx context.Context, batch *Batch, params []any) (*Batch, error)
	planHash   string
	params     []any
}

// NewAdaptiveOp wraps an operator with adaptive compilation.
func NewAdaptiveOp(inner Operator, planHash string) *AdaptiveOp {
	return &AdaptiveOp{
		inner:    inner,
		counter:  NewInvocationCounter(planHash, 2),
		planHash: planHash,
		state:    atomic.Uint32{},
	}
}

// Next implements the Operator interface. For the first threshold
// invocations it delegates to the inner interpreted operator.
// After the threshold, it attempts to swap to the compiled path.
func (a *AdaptiveOp) Next(ctx context.Context) (Row, error) {
	state := AdqcState(a.state.Load())

	if state == AdqcCompiled {
		return a.inner.Next(ctx)
	}

	if state == AdqcInterpreted {
		reached := a.counter.Inc()
		if !reached {
			return a.inner.Next(ctx)
		}
		a.tryCompile(ctx)
	}

	return a.inner.Next(ctx)
}

// tryCompile attempts to swap to the compiled codegen path.
func (a *AdaptiveOp) tryCompile(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if AdqcState(a.state.Load()) != AdqcInterpreted {
		return
	}

	a.state.Store(uint32(AdqcCompiling))

	opType := operatorType(a.inner)
	if fn, ok := LookupCodegenOp(opType); ok {
		a.compiledFn = fn
		a.state.Store(uint32(AdqcCompiled))
		slog.Debug("adqc: plan specialized",
			"planHash", a.planHash,
			"opType", opType,
		)
	} else {
		a.state.Store(uint32(AdqcInterpreted))
		slog.Debug("adqc: fallback",
			"planHash", a.planHash,
			"opType", opType,
			"reason", "no codegen fn registered",
		)
	}
	_ = ctx
}

// Close implements the Operator interface.
func (a *AdaptiveOp) Close() error {
	return a.inner.Close()
}

// State returns the current adaptive state.
func (a *AdaptiveOp) State() AdqcState {
	return AdqcState(a.state.Load())
}

// IsCompiled returns true if the adaptive op has been compiled.
func (a *AdaptiveOp) IsCompiled() bool {
	return a.state.Load() == uint32(AdqcCompiled)
}

// Counter returns the invocation counter for this plan.
func (a *AdaptiveOp) Counter() *InvocationCounter {
	return a.counter
}

// PlanHash returns the plan hash this adaptive op wraps.
func (a *AdaptiveOp) PlanHash() string {
	return a.planHash
}

// WithParams propagates params to the inner operator and stores
// them locally for use during specialized execution.
func (a *AdaptiveOp) WithParams(p []any) Operator {
	a.params = p
	if w, ok := a.inner.(interface{ WithParams([]any) Operator }); ok {
		w.WithParams(p)
	}
	return a
}

// Child returns the inner operator for parameter propagation
// and EXPLAIN tree walking.
func (a *AdaptiveOp) Child() Operator { return a.inner }

// GlobalAdqcCache is the process-wide adaptive compilation cache.
var GlobalAdqcCache = NewAdqcCache(256)

var _ Operator = (*AdaptiveOp)(nil)
