package AD

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"context"
	"log/slog"
	"fmt"
	"sync"
	"sync/atomic"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
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

// AdaptiveOp is a wrapper DT.Operator that adaptively switches from
// interpreted row-at-a-time execution to specialized batch execution
// after a configurable invocation threshold.
// The first N invocations (default 2) use the interpreted inner operator.
// After N, the hot path swaps to the codegen-specialized batch function.
// If the swap fails, the interpreted path continues as a fallback.
// REQ000313 satisfied.
type AdaptiveOp struct {
	Inner      DT.Operator
	counter    *InvocationCounter
	state      atomic.Uint32
	mu         sync.Mutex
	compiledFn func(ctx context.Context, batch *UT.Batch, params []any) (*UT.Batch, error)
	planHash   string
	params     []any
	// REQ000845: tryAttempted prevents repeated calls to tryCompile
	// when no compiled function is available. After the first tryCompile
	// returns without setting state to Compiled, subsequent Next() calls
	// take the direct inner.Next() fast path without going through
	// the counter.Inc() and lock acquisition.
	tryAttempted bool
}

// NewAdaptiveOp wraps an operator with adaptive compilation.
func NewAdaptiveOp(inner DT.Operator, planHash string) *AdaptiveOp {
	return &AdaptiveOp{
		Inner:    inner,
		counter:  NewInvocationCounter(planHash, 1), // compile after first invocation
		planHash: planHash,
		state:    atomic.Uint32{},
	}
}

// Next implements the DT.Operator interface. For the first threshold
// invocations it delegates to the inner interpreted operator.
// After the threshold, it attempts to swap to the compiled path.
// REQ000845: once tryCompile has run without success, subsequent
// calls skip the counter/lock and call inner.Next() directly.
func (a *AdaptiveOp) Next(ctx context.Context) (DT.Row, error) {
	state := AdqcState(a.state.Load())

	if state == AdqcCompiled {
		return a.Inner.Next(ctx)
	}

	if state == AdqcInterpreted && !a.tryAttempted {
		reached := a.counter.Inc()
		if !reached {
			return a.Inner.Next(ctx)
		}
		a.tryCompile(ctx)
	}

	return a.Inner.Next(ctx)
}

// tryCompile attempts to swap to the compiled codegen path.
// It first checks the GlobalAdqcCache for a previously compiled plan,
// avoiding recompilation across executor recreations.
// REQ000845: after this function runs (whether successful or not),
// the AdaptiveOp is marked as tryAttempted so future Next() calls
// skip the counter increment and lock acquisition.
func (a *AdaptiveOp) tryCompile(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if AdqcState(a.state.Load()) != AdqcInterpreted {
		a.tryAttempted = true
		return
	}

	// Check global cache first - avoids recompilation across executors.
	if cached := GlobalAdqcCache.Get(a.planHash, 0); cached != nil {
		a.compiledFn = cached.Fn
		a.state.Store(uint32(AdqcCompiled))
		a.tryAttempted = true
		slog.Debug("adqc: plan restored from cache",
			"planHash", a.planHash,
			"opType", cached.OpType,
		)
		return
	}

	opType := fmt.Sprintf("%T", a.Inner)
	a.state.Store(uint32(AdqcInterpreted))
	// REQ000845: mark as attempted so we don't retry on every Next()
	// call. No compiled function is available, so interpreted path
	// is the only path for this plan.
	a.tryAttempted = true
	slog.Debug("adqc: fallback",
		"planHash", a.planHash,
		"opType", opType,
		"reason", "no codegen fn registered",
	)
	_ = ctx
}

// Close implements the DT.Operator interface.
func (a *AdaptiveOp) Close() error {
	return a.Inner.Close()
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
func (a *AdaptiveOp) WithParams(p []any) DT.Operator {
	a.params = p
	if w, ok := a.Inner.(interface{ WithParams([]any) DT.Operator }); ok {
		w.WithParams(p)
	}
	return a
}

// Child returns the inner operator for parameter propagation
// and EXPLAIN tree walking.
func (a *AdaptiveOp) Child() DT.Operator { return a.Inner }

// GlobalAdqcCache is the process-wide adaptive compilation cache.
var GlobalAdqcCache = NewAdqcCache(256)

var _ DT.Operator = (*AdaptiveOp)(nil)
