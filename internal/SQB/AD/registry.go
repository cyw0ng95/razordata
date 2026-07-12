package AD

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"context"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"fmt"
	"sync"
)

// CompileFn builds a closure (CompiledFn) from a concrete operator. The
// returned closure takes a columnar Batch and emits a columnar Batch,
// bypassing the per-row volcano DT.Operator.Next() dispatch path.
//
// opType is the registered type key (typically fmt.Sprintf("%T", op)).
// inner is the operator being specialized — the compile fn is expected to
// type-assert it to a concrete type it knows how to compile.
//
// REQ001536: registry wire-up so tryCompile can actually swap to a
// specialized path instead of always falling back to interpreted.
type CompileFn func(op any) (CompiledFn, error)

// CompiledFn is a batch-in / batch-out specialization. It mirrors the
// shape declared by SpecializedPlan.Fn so the cache and the runtime
// stay consistent.
type CompiledFn = func(ctx context.Context, batch *UT.Batch, params []any) (*UT.Batch, error)

// CompileFunc is the registry-facing signature. Compiles a concrete
// DT.Operator to a CompiledFn and reports the OpType that should be
// cached alongside the result.
type CompileFunc func(op DT.Operator) (CompiledFn, string, error)

// registry holds compile functions keyed by operator type name.
type registry struct {
	mu      sync.RWMutex
	entries map[string]CompileFn
}

func newRegistry() *registry {
	return &registry{entries: make(map[string]CompileFn)}
}

// Register adds a compile function for the given operator type key.
// Safe to call from package init() or main; thread-safe.
func (r *registry) Register(opType string, fn CompileFn) {
	r.mu.Lock()
	r.entries[opType] = fn
	r.mu.Unlock()
}

// Get returns the registered compile function for opType, or (nil, false)
// if no specialization is registered.
func (r *registry) Get(opType string) (CompileFn, bool) {
	r.mu.RLock()
	fn, ok := r.entries[opType]
	r.mu.RUnlock()
	return fn, ok
}

// Len returns the number of registered specializations.
func (r *registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// Compile looks up the compile function for op's type, invokes it, and
// returns (CompiledFn, opType, error). On registry miss returns
// (nil, "", nil) — caller should fall back to interpreted.
func (r *registry) Compile(op DT.Operator) (CompiledFn, string, error) {
	opType := fmt.Sprintf("%T", op)
	r.mu.RLock()
	fn, ok := r.entries[opType]
	r.mu.RUnlock()
	if !ok {
		return nil, opType, nil
	}
	compiled, err := fn(op)
	if err != nil {
		return nil, opType, fmt.Errorf("compile %s: %w", opType, err)
	}
	return compiled, opType, nil
}

// GlobalRegistry is the process-wide compile-fn registry. Specializations
// are registered via init() or main(); the registry is consulted by
// AdaptiveOp.tryCompile when a plan first crosses the invocation
// threshold. REQ001536.
var GlobalRegistry = newRegistry()

// Register is a convenience wrapper around GlobalRegistry.Register.
// Intended to be called from init() or main() to wire up specializations:
//
//	func init() {
//	    AD.Register("*SQB.OP.SeqScan", compileScanFilterProject)
//	}
func Register(opType string, fn CompileFn) {
	GlobalRegistry.Register(opType, fn)
}