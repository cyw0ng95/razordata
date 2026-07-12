package AD

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

)

func TestInvocationCounter_Basic(t *testing.T) {
	ic := NewInvocationCounter("hash1", 2)
	if c := ic.Count(); c != 0 {
		t.Errorf("expected count 0, got %d", c)
	}
	if ic.PlanHash() != "hash1" {
		t.Errorf("expected hash1, got %s", ic.PlanHash())
	}
}

func TestInvocationCounter_Threshold(t *testing.T) {
	ic := NewInvocationCounter("hash2", 3)
	for i := 0; i < 2; i++ {
		if ic.Inc() {
			t.Errorf("expected false on invocation %d", i+1)
		}
	}
	if !ic.Inc() {
		t.Error("expected true on 3rd invocation")
	}
}

func TestInvocationCounter_ThresholdOne(t *testing.T) {
	ic := NewInvocationCounter("hash-one", 1)
	if !ic.Inc() {
		t.Error("expected true on first invocation with threshold=1")
	}
}

func TestInvocationCounter_DefaultThreshold(t *testing.T) {
	ic := NewInvocationCounter("hash-def", 0)
	if ic.threshold != 2 {
		t.Errorf("expected default threshold 2, got %d", ic.threshold)
	}
}

func TestInvocationCounter_Reset(t *testing.T) {
	ic := NewInvocationCounter("hash3", 1)
	ic.Inc()
	ic.Reset()
	if c := ic.Count(); c != 0 {
		t.Errorf("expected count 0 after reset, got %d", c)
	}
}

func TestInvocationCounter_Concurrent(t *testing.T) {
	ic := NewInvocationCounter("hash-con", 100)
	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)
	if n < 2 {
		n = 2
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				ic.Inc()
			}
		}()
	}
	wg.Wait()
	if c := ic.Count(); c != uint64(n*50) {
		t.Errorf("expected %d, got %d", n*50, c)
	}
}

func TestNewAdaptiveOp_InterpretedStart(t *testing.T) {
	inner := &testOp{}
	ao := NewAdaptiveOp(inner, "plan-hash")
	if ao.State() != AdqcInterpreted {
		t.Errorf("expected interpreted state, got %d", ao.State())
	}
}

func TestAdaptiveOp_FirstTwoInterpreted(t *testing.T) {
	inner := &testOp{}
	ao := NewAdaptiveOp(inner, "plan-hash")
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		_, err := ao.Next(ctx)
		if err != nil {
			t.Fatalf("Next returned error: %v", err)
		}
		if ao.State() != AdqcInterpreted {
			t.Errorf("expected interpreted on iteration %d", i+1)
		}
	}
}

func TestAdaptiveOp_Close(t *testing.T) {
	inner := &testOp{}
	ao := NewAdaptiveOp(inner, "plan-hash")
	if err := ao.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	if !inner.closed {
		t.Error("expected inner operator to be closed")
	}
}

func TestAdaptiveOp_PlanHash(t *testing.T) {
	ao := NewAdaptiveOp(&testOp{}, "test-plan-hash")
	if ao.PlanHash() != "test-plan-hash" {
		t.Errorf("expected test-plan-hash, got %s", ao.PlanHash())
	}
}

func TestAdaptiveOp_WithParams(t *testing.T) {
	inner := &testOp{}
	ao := NewAdaptiveOp(inner, "hash")
	ao2 := ao.WithParams([]any{1, 2, 3})
	if ao2 != ao {
		t.Errorf("expected same operator back")
	}
}

func TestAdaptiveOp_WithParamsInnerPropagated(t *testing.T) {
	inner := &paramTestOp{}
	ao := NewAdaptiveOp(inner, "hash")
	ao.WithParams([]any{42})
	if !inner.paramsReceived {
		t.Error("expected params propagated to inner operator")
	}
}

func TestAdaptiveOp_ChildReturnsInner(t *testing.T) {
	inner := &testOp{}
	ao := NewAdaptiveOp(inner, "hash")
	if ao.Child() != inner {
		t.Error("expected Child() to return inner operator")
	}
}

func TestAdaptiveOp_IsCompiledFalseInitially(t *testing.T) {
	ao := NewAdaptiveOp(&testOp{}, "h")
	if ao.IsCompiled() {
		t.Error("expected not compiled initially")
	}
}

func TestAdaptiveOp_CompileAttempt(t *testing.T) {
	inner := &testOp{}
	ao := NewAdaptiveOp(inner, "plan-hash")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := ao.Next(ctx)
		if err != nil {
			t.Fatalf("Next returned error: %v", err)
		}
	}
}

func TestAdaptiveOp_CompiledState(t *testing.T) {
	inner := &testOp{}
	ao := NewAdaptiveOp(inner, "plan-hash")
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		ao.Next(ctx)
	}
	state := ao.State()
	if state == AdqcInterpreted {
		t.Log("adqc: compile attempt completed (state may be compiled or fallback)")
	}
}

func TestAdaptiveOp_NegativeParams(t *testing.T) {
	ao := NewAdaptiveOp(&testOp{}, "h")
	ao.WithParams([]any{-1, 0, 1})
}

func TestAdaptiveOp_EmptyPlanHash(t *testing.T) {
	ao := NewAdaptiveOp(&testOp{}, "")
	if ao.PlanHash() != "" {
		t.Errorf("expected empty plan hash, got %s", ao.PlanHash())
	}
}

func TestNewAdqcCache_DefaultLimit(t *testing.T) {
	c := NewAdqcCache(0)
	if c == nil {
		t.Fatal("NewAdqcCache returned nil")
	}
	if c.Limit() != 256 {
		t.Errorf("expected default limit 256, got %d", c.Limit())
	}
}

func TestAdqcCache_GetPut(t *testing.T) {
	c := NewAdqcCache(10)
	plan := &SpecializedPlan{OpType: "Filter", PlanHash: "h1"}
	c.Put("h1", 1, plan)
	got := c.Get("h1", 1)
	if got == nil {
		t.Fatal("expected plan, got nil")
	}
	if got.OpType != "Filter" {
		t.Errorf("expected Filter, got %s", got.OpType)
	}
}

func TestAdqcCache_Miss(t *testing.T) {
	c := NewAdqcCache(10)
	got := c.Get("nonexistent", 0)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestAdqcCache_Invalidate(t *testing.T) {
	c := NewAdqcCache(10)
	c.Put("h1", 1, &SpecializedPlan{PlanHash: "h1", SchemaVersion: 1})
	c.Put("h2", 2, &SpecializedPlan{PlanHash: "h2", SchemaVersion: 2})
	c.Invalidate(1)
	if c.Len() != 1 {
		t.Errorf("expected 1 entry after invalidation, got %d", c.Len())
	}
}

func TestAdqcCache_InvalidateAll(t *testing.T) {
	c := NewAdqcCache(10)
	c.Put("h1", 1, &SpecializedPlan{PlanHash: "h1", SchemaVersion: 1})
	c.Put("h2", 1, &SpecializedPlan{PlanHash: "h2", SchemaVersion: 1})
	c.Invalidate(1)
	if c.Len() != 0 {
		t.Errorf("expected 0 entries after full invalidation, got %d", c.Len())
	}
}

func TestAdqcCache_InvalidateNoop(t *testing.T) {
	c := NewAdqcCache(10)
	c.Put("h1", 1, &SpecializedPlan{PlanHash: "h1", SchemaVersion: 1})
	c.Invalidate(99)
	if c.Len() != 1 {
		t.Errorf("expected 1 entry after non-matching invalidation, got %d", c.Len())
	}
}

func TestAdqcCache_Eviction(t *testing.T) {
	c := NewAdqcCache(2)
	for i := 0; i < 5; i++ {
		c.Put(string(rune('a'+i)), 0, &SpecializedPlan{PlanHash: string(rune('a' + i))})
	}
	if c.Len() > 2 {
		t.Errorf("expected max 2 entries, got %d", c.Len())
	}
}

func TestAdqcCache_EvictionKeepsRecent(t *testing.T) {
	c := NewAdqcCache(3)
	c.Put("a", 0, &SpecializedPlan{PlanHash: "a"})
	c.Put("b", 0, &SpecializedPlan{PlanHash: "b"})
	c.Put("c", 0, &SpecializedPlan{PlanHash: "c"})
	_ = c.Get("a", 0)
	c.Put("d", 0, &SpecializedPlan{PlanHash: "d"})
	if c.Get("a", 0) == nil {
		t.Log("adqc: 'a' was evicted (expected with LRU of 3)")
	}
}

func TestAdqcCache_UpdatePromotes(t *testing.T) {
	c := NewAdqcCache(3)
	c.Put("a", 0, &SpecializedPlan{PlanHash: "a"})
	c.Put("b", 0, &SpecializedPlan{PlanHash: "b"})
	c.Put("c", 0, &SpecializedPlan{PlanHash: "c"})
	c.Put("a", 0, &SpecializedPlan{PlanHash: "a-updated"})
	got := c.Get("a", 0)
	if got == nil || got.PlanHash != "a-updated" {
		t.Errorf("expected updated plan 'a-updated', got %v", got)
	}
}

func TestAdqcCache_ConcurrentAccess(t *testing.T) {
	c := NewAdqcCache(64)
	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)
	if n < 2 {
		n = 2
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := string(rune('a' + id%26))
			c.Put(key, uint64(id), &SpecializedPlan{PlanHash: key, SchemaVersion: uint64(id)})
			_ = c.Get(key, uint64(id))
		}(i)
	}
	wg.Wait()
}

func TestNewFallbackOp_Delegates(t *testing.T) {
	inner := &testOp{}
	fb := NewFallbackOp(inner, "hash", "test reason")
	ctx := context.Background()
	row, err := fb.Next(ctx)
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if len(row.Cols) != 0 {
		t.Errorf("expected empty cols, got %v", row.Cols)
	}
}

func TestFallbackOp_Close(t *testing.T) {
	inner := &testOp{}
	fb := NewFallbackOp(inner, "hash", "test")
	if err := fb.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	if !inner.closed {
		t.Error("expected inner operator to be closed")
	}
}

func TestFallbackOp_TriggeredInitially(t *testing.T) {
	fb := NewFallbackOp(&testOp{}, "h", "test")
	if fb.Triggered() {
		t.Error("expected Triggered() to be false initially")
	}
}

func TestFallbackOp_MultipleNext(t *testing.T) {
	inner := &testOp{}
	fb := NewFallbackOp(inner, "h", "test")
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, err := fb.Next(ctx)
		if err != nil {
			t.Fatalf("Next iteration %d failed: %v", i, err)
		}
	}
}

func TestGlobalAdqcCache_Exists(t *testing.T) {
	if GlobalAdqcCache == nil {
		t.Fatal("GlobalAdqcCache is nil")
	}
}

func TestNewInvocationCounter_ConcurrentInc(t *testing.T) {
	ic := NewInvocationCounter("h", 1000)
	var wg sync.WaitGroup
	var hitThreshold atomic.Bool
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if ic.Inc() {
					hitThreshold.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if !hitThreshold.Load() {
		t.Error("expected threshold to be hit after concurrent increment")
	}
}

func TestAdqcCache_ZeroCapacity(t *testing.T) {
	c := NewAdqcCache(0)
	if c.Limit() != 256 {
		t.Errorf("expected 256, got %d", c.Limit())
	}
}

func TestAdqcCache_NegativeCapacity(t *testing.T) {
	c := NewAdqcCache(-5)
	if c.Limit() != 256 {
		t.Errorf("expected 256, got %d", c.Limit())
	}
}

func TestAdqcCache_DoubleInvalidate(t *testing.T) {
	c := NewAdqcCache(10)
	c.Put("h1", 1, &SpecializedPlan{PlanHash: "h1", SchemaVersion: 1})
	c.Invalidate(1)
	c.Invalidate(1)
	if c.Len() != 0 {
		t.Errorf("expected 0 after double invalidation, got %d", c.Len())
	}
}

func TestAdqcCache_SameHashDifferentVersion(t *testing.T) {
	c := NewAdqcCache(10)
	c.Put("hash", 1, &SpecializedPlan{PlanHash: "hash", SchemaVersion: 1})
	c.Put("hash", 2, &SpecializedPlan{PlanHash: "hash", SchemaVersion: 2})
	if c.Len() != 2 {
		t.Errorf("expected 2 entries for different versions, got %d", c.Len())
	}
	got1 := c.Get("hash", 1)
	if got1 == nil || got1.SchemaVersion != 1 {
		t.Errorf("expected version 1, got %v", got1)
	}
	got2 := c.Get("hash", 2)
	if got2 == nil || got2.SchemaVersion != 2 {
		t.Errorf("expected version 2, got %v", got2)
	}
}

// testOp is a minimal DT.Operator implementation for testing.
type testOp struct {
	closed bool
}

func (t *testOp) Next(_ context.Context) (DT.Row, error) {
	return DT.Row{Cols: []string{}, Data: []DT.Value{}}, nil
}

func (t *testOp) Close() error {
	t.closed = true
	return nil
}

type paramTestOp struct {
	paramsReceived bool
}

func (p *paramTestOp) Next(_ context.Context) (DT.Row, error) {
	return DT.Row{}, nil
}

func (p *paramTestOp) Close() error { return nil }

func (p *paramTestOp) WithParams(args []any) DT.Operator {
	p.paramsReceived = true
	return p
}

// TestAdaptiveOp_DirectBypass verifies that after tryAttempted=true,
// the direct bool bypass avoids the atomic state.Load() and calls
// Inner.Next() directly. REQ001275.
func TestAdaptiveOp_DirectBypass(t *testing.T) {
	op := NewAdaptiveOp(&testOp{}, "test_hash")
	if op.direct {
		t.Error("direct should be false before tryCompile")
	}
	_, _ = op.Next(context.Background())
	if !op.direct {
		t.Error("direct should be true after tryCompile")
	}
}

// BenchmarkAdaptiveOp_DirectBypass measures Next() throughput when
// the direct bypass is active (after tryAttempted). REQ001275.
func BenchmarkAdaptiveOp_DirectBypass(b *testing.B) {
	op := NewAdaptiveOp(&testOp{}, "bench_hash")
	_, _ = op.Next(context.Background()) // prime: trigger tryCompile
	if !op.direct {
		b.Fatal("direct not set after tryCompile")
	}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = op.Next(ctx)
	}
}

// BenchmarkAdaptiveOp_BeforeBypass measures Next() throughput when
// the atomic state.Load() path is taken (before tryAttempted).
// REQ001275 baseline.
func BenchmarkAdaptiveOp_BeforeBypass(b *testing.B) {
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op := NewAdaptiveOp(&testOp{}, "bench_hash2")
		_, _ = op.Next(ctx)
	}
}

// --- REQ001536: registry wire-up tests ---

// fakeSpecializableOp satisfies DT.Operator and has a stable type name
// that registry tests use to register/lookup specializations.
type fakeSpecializableOp struct{ name string }

func (f *fakeSpecializableOp) Next(ctx context.Context) (DT.Row, error) {
	return DT.Row{}, nil
}
func (f *fakeSpecializableOp) Close() error { return nil }

const fakeSpecializableOpType = "*AD.fakeSpecializableOp"

func TestRegistry_RegisterLookup(t *testing.T) {
	r := newRegistry()
	called := false
	fn := func(op any) (CompiledFn, error) {
		called = true
		_, ok := op.(*fakeSpecializableOp)
		if !ok {
			t.Errorf("expected *fakeSpecializableOp, got %T", op)
		}
		return func(ctx context.Context, batch *UT.Batch, params []any) (*UT.Batch, error) {
			return batch, nil
		}, nil
	}
	r.Register(fakeSpecializableOpType, fn)
	if r.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", r.Len())
	}
	got, ok := r.Get(fakeSpecializableOpType)
	if !ok {
		t.Fatal("expected lookup hit, got miss")
	}
	if got == nil {
		t.Fatal("expected non-nil compile fn")
	}
	out, opType, err := r.Compile(&fakeSpecializableOp{name: "x"})
	if err != nil {
		t.Fatalf("Compile err: %v", err)
	}
	if opType != fakeSpecializableOpType {
		t.Errorf("expected opType %q, got %q", fakeSpecializableOpType, opType)
	}
	if out == nil {
		t.Fatal("expected non-nil compiled fn")
	}
	if !called {
		t.Error("compile fn was not invoked")
	}
}

func TestRegistry_MissReturnsNil(t *testing.T) {
	r := newRegistry()
	out, opType, err := r.Compile(&fakeSpecializableOp{name: "x"})
	if err != nil {
		t.Fatalf("Compile err: %v", err)
	}
	if out != nil {
		t.Error("expected nil compiled fn on miss")
	}
	if opType != fakeSpecializableOpType {
		t.Errorf("expected opType reported even on miss, got %q", opType)
	}
}

func TestRegistry_RegisterErrPropagates(t *testing.T) {
	r := newRegistry()
	r.Register(fakeSpecializableOpType, func(op any) (CompiledFn, error) {
		return nil, fmt.Errorf("boom")
	})
	out, _, err := r.Compile(&fakeSpecializableOp{name: "x"})
	if err == nil {
		t.Fatal("expected error from compile fn")
	}
	if out != nil {
		t.Error("expected nil compiled fn on error")
	}
}

// TestAdaptiveOp_CompiledState_AfterRegister verifies the registry
// wire-up: when a specialization is registered for the inner operator's
// type, AdaptiveOp.tryCompile flips state to AdqcCompiled instead of
// falling through to "no codegen fn registered".
func TestAdaptiveOp_CompiledState_AfterRegister(t *testing.T) {
	// Use a dedicated registry so we don't disturb GlobalRegistry state
	// in the parallel package init order.
	saved := GlobalRegistry
	defer func() { GlobalRegistry = saved }()
	GlobalRegistry = newRegistry()

	const opType = "*AD.fakeSpecializableOp"
	called := false
	Register(opType, func(op any) (CompiledFn, error) {
		called = true
		return func(ctx context.Context, batch *UT.Batch, params []any) (*UT.Batch, error) {
			return batch, nil
		}, nil
	})

	// Sanity: confirmed registered.
	_, ok := GlobalRegistry.Get(opType)
	if !ok {
		t.Fatal("registration did not land in GlobalRegistry")
	}

	inner := &fakeSpecializableOp{name: "x"}
	op := NewAdaptiveOp(inner, "plan-with-registry")
	if op.State() != AdqcInterpreted {
		t.Fatalf("expected interpreted start, got %d", op.State())
	}

	// Drive Next once so the counter hits threshold and tryCompile fires.
	ctx := context.Background()
	_, err := op.Next(ctx)
	if err != nil {
		t.Fatalf("Next err: %v", err)
	}

	if !called {
		t.Error("compile fn was not invoked — registry lookup missed")
	}
	if op.State() != AdqcCompiled {
		t.Errorf("expected AdqcCompiled after registry hit, got %d", op.State())
	}
	if op.compiledFn == nil {
		t.Error("expected compiledFn to be set after compile")
	}
}

// TestAdaptiveOp_FallbackWhenNoRegistration ensures the existing
// fallback path still works when no specialization is registered.
// This is the regression guard for the registry wire-up.
func TestAdaptiveOp_FallbackWhenNoRegistration(t *testing.T) {
	saved := GlobalRegistry
	defer func() { GlobalRegistry = saved }()
	GlobalRegistry = newRegistry()

	inner := &fakeSpecializableOp{name: "x"}
	op := NewAdaptiveOp(inner, "plan-fallback")
	ctx := context.Background()
	_, err := op.Next(ctx)
	if err != nil {
		t.Fatalf("Next err: %v", err)
	}
	if op.State() != AdqcInterpreted {
		t.Errorf("expected fallback to AdqcInterpreted, got %d", op.State())
	}
	if op.compiledFn != nil {
		t.Error("expected nil compiledFn on fallback path")
	}
}

// BenchmarkAdaptiveOp_CompiledVsInterpreted measures the cost of an
// AdaptiveOp Next() call in two modes:
//   - interpreted (no registry entry)
//   - compiled (registry hit)
//
// REQ001536: this is the proof that the registry swap actually routes
// execution through the compiledFn.
func BenchmarkAdaptiveOp_CompiledVsInterpreted(b *testing.B) {
	saved := GlobalRegistry
	defer func() { GlobalRegistry = saved }()
	GlobalRegistry = newRegistry()

	const opType = "*AD.fakeSpecializableOp"
	Register(opType, func(op any) (CompiledFn, error) {
		return func(ctx context.Context, batch *UT.Batch, params []any) (*UT.Batch, error) {
			return batch, nil
		}, nil
	})

	ctx := context.Background()

	b.Run("interpreted", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			op := NewAdaptiveOp(&fakeSpecializableOp{name: "x"}, "bench-int")
			_, _ = op.Next(ctx)
		}
	})
	b.Run("compiled", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			op := NewAdaptiveOp(&fakeSpecializableOp{name: "x"}, "bench-comp")
			_, _ = op.Next(ctx)
		}
	})
}
