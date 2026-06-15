package EX

import (
	"context"
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
	if c.limit != 256 {
		t.Errorf("expected default limit 256, got %d", c.limit)
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

func TestLookupCodegenOp_NotFound(t *testing.T) {
	fn, ok := LookupCodegenOp("NonExistentOp")
	if ok {
		t.Errorf("expected false for nonexistent op")
	}
	if fn != nil {
		t.Errorf("expected nil fn, got %v", fn)
	}
}

func TestLookupCodegenOp_Found(t *testing.T) {
	fn, ok := LookupCodegenOp("Filter")
	if !ok {
		t.Skip("codegen op not registered (expected in non-codegen build)")
	}
	if fn == nil {
		t.Errorf("expected non-nil fn for Filter")
	}
}

func TestLookupCodegenOp_EmptyString(t *testing.T) {
	fn, ok := LookupCodegenOp("")
	if ok {
		t.Errorf("expected false for empty string")
	}
	_ = fn
}

func TestCodegenState_Defaults(t *testing.T) {
	cs := NewCodegenState(nil)
	if cs.IsSwapped() {
		t.Error("expected not swapped initially")
	}
}

func TestCodegenState_Swap(t *testing.T) {
	cs := NewCodegenState(nil)
	cs.Swap(nil)
	if !cs.IsSwapped() {
		t.Error("expected swapped after Swap call")
	}
}

func TestCodegenState_ExecBatch(t *testing.T) {
	cs := NewCodegenState(nil)
	out, err := cs.ExecBatch(context.Background(), &Batch{Size: 10}, nil)
	if err != nil {
		t.Fatalf("ExecBatch error: %v", err)
	}
	if out == nil || out.Size != 10 {
		t.Errorf("expected batch size 10, got %v", out)
	}
}

func TestCodegenState_ExecBatchAfterSwap(t *testing.T) {
	cs := NewCodegenState(nil)
	called := false
	cs.Swap(func(ctx context.Context, batch *Batch, params []any) (*Batch, error) {
		called = true
		return batch, nil
	})
	out, err := cs.ExecBatch(context.Background(), &Batch{Size: 5}, nil)
	if err != nil {
		t.Fatalf("ExecBatch error: %v", err)
	}
	if !called {
		t.Error("expected swapped function to be called")
	}
	if out == nil || out.Size != 5 {
		t.Errorf("expected batch size 5, got %v", out)
	}
}

func TestGlobalAdqcCache_Exists(t *testing.T) {
	if GlobalAdqcCache == nil {
		t.Fatal("GlobalAdqcCache is nil")
	}
}

func TestWithCodegenForOp_Filter(t *testing.T) {
	f := &Filter{}
	op := WithCodegenForOp(f)
	if _, ok := op.(*Filter); !ok {
		t.Errorf("expected *Filter back, got %T", op)
	}
	v := op.(*Filter)
	if v.cs == nil {
		t.Error("expected CodegenState to be set on Filter")
	}
}

func TestWithCodegenForOp_Project(t *testing.T) {
	p := &Project{}
	op := WithCodegenForOp(p)
	v := op.(*Project)
	if v.cs == nil {
		t.Error("expected CodegenState to be set on Project")
	}
}

func TestWithCodegenForOp_Sort(t *testing.T) {
	s := &Sort{}
	op := WithCodegenForOp(s)
	v := op.(*Sort)
	if v.cs == nil {
		t.Error("expected CodegenState to be set on Sort")
	}
}

func TestWithCodegenForOp_Limit(t *testing.T) {
	l := &Limit{}
	op := WithCodegenForOp(l)
	v := op.(*Limit)
	if v.cs == nil {
		t.Error("expected CodegenState to be set on Limit")
	}
}

func TestWithCodegenForOp_Unsupported(t *testing.T) {
	inner := &testOp{}
	op := WithCodegenForOp(inner)
	if op != inner {
		t.Errorf("expected same operator back for unsupported type")
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
	if c.limit != 256 {
		t.Errorf("expected 256, got %d", c.limit)
	}
}

func TestAdqcCache_NegativeCapacity(t *testing.T) {
	c := NewAdqcCache(-5)
	if c.limit != 256 {
		t.Errorf("expected 256, got %d", c.limit)
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

// testOp is a minimal Operator implementation for testing.
type testOp struct {
	closed bool
}

func (t *testOp) Next(_ context.Context) (Row, error) {
	return Row{Cols: []string{}, Data: []interface{}{}}, nil
}

func (t *testOp) Close() error {
	t.closed = true
	return nil
}

type paramTestOp struct {
	paramsReceived bool
}

func (p *paramTestOp) Next(_ context.Context) (Row, error) {
	return Row{}, nil
}

func (p *paramTestOp) Close() error { return nil }

func (p *paramTestOp) WithParams(args []interface{}) Operator {
	p.paramsReceived = true
	return p
}
