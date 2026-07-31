package PX

import (
	"fmt"
	"testing"
)

func TestRewriter_NoOp(t *testing.T) {
	spec := &PipelineSpec{
		Stages:  []StageSpec{&ScanStageSpec{}},
		RootIdx: 0,
	}
	r := NewRewriter(spec)
	if err := r.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if len(spec.Stages) != 1 {
		t.Fatalf("stages: got %d, want 1", len(spec.Stages))
	}
	if spec.RootIdx != 0 {
		t.Fatalf("RootIdx: got %d, want 0", spec.RootIdx)
	}
}

func TestRewriter_Replace(t *testing.T) {
	spec := &PipelineSpec{
		Stages:  []StageSpec{&ScanStageSpec{}, &FilterStageSpec{}},
		RootIdx: 1,
	}
	r := NewRewriter(spec)
	if err := r.Replace(0, &ProjectStageSpec{}, false); err != nil {
		t.Fatalf("Replace(0): %v", err)
	}
	if err := r.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if len(spec.Stages) != 2 {
		t.Fatalf("stages: got %d, want 2", len(spec.Stages))
	}
	if _, ok := spec.Stages[0].(*ProjectStageSpec); !ok {
		t.Fatalf("Stages[0]: got %T, want *ProjectStageSpec", spec.Stages[0])
	}
	if spec.RootIdx != 1 {
		t.Fatalf("RootIdx: got %d, want 1", spec.RootIdx)
	}
}

func TestRewriter_ReplaceRoot(t *testing.T) {
	spec := &PipelineSpec{
		Stages:  []StageSpec{&ScanStageSpec{}, &FilterStageSpec{}},
		RootIdx: 1,
	}
	r := NewRewriter(spec)
	if err := r.Replace(0, &ProjectStageSpec{}, true); err != nil {
		t.Fatalf("Replace(0, root): %v", err)
	}
	if err := r.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if spec.RootIdx != 0 {
		t.Fatalf("RootIdx: got %d, want 0", spec.RootIdx)
	}
}

func TestRewriter_Remove(t *testing.T) {
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&ScanStageSpec{},
			&FilterStageSpec{},
			&LimitStageSpec{},
		},
		Edges: []EdgeSpec{
			{From: 1, To: 0, Side: SingleChild},
			{From: 2, To: 1, Side: SingleChild},
		},
		RootIdx: 2,
	}
	r := NewRewriter(spec)
	r.Remove(1)
	if err := r.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if len(spec.Stages) != 2 {
		t.Fatalf("stages: got %d, want 2", len(spec.Stages))
	}
	if len(spec.Edges) != 0 {
		t.Fatalf("edges: got %d, want 0 (both edges referenced removed stage)", len(spec.Edges))
	}
	if spec.RootIdx != 1 {
		t.Fatalf("RootIdx: got %d, want 1 (remapped from old idx 2)", spec.RootIdx)
	}
}

func TestRewriter_ReplaceThenCommit_Reset(t *testing.T) {
	spec := &PipelineSpec{
		Stages:  []StageSpec{&ScanStageSpec{}},
		RootIdx: 0,
	}
	r := NewRewriter(spec)
	if err := r.Replace(0, &FilterStageSpec{}, false); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if err := r.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// Second Commit is a no-op (sentinels reset).
	if err := r.Commit(); err != nil {
		t.Fatalf("Commit (second): %v", err)
	}
}

func TestRewriter_ReplaceOutOfRange(t *testing.T) {
	spec := &PipelineSpec{Stages: []StageSpec{&ScanStageSpec{}}}
	r := NewRewriter(spec)
	if err := r.Replace(5, &FilterStageSpec{}, false); err == nil {
		t.Fatal("Replace out of range: expected error")
	}
	if err := r.Replace(-1, &FilterStageSpec{}, false); err == nil {
		t.Fatal("Replace negative index: expected error")
	}
}

func TestOptimizer_NoPasses(t *testing.T) {
	spec := &PipelineSpec{
		Stages:  []StageSpec{&ScanStageSpec{}},
		RootIdx: 0,
	}
	optimizer := NewOptimizer()
	if err := optimizer.Optimize(spec); err != nil {
		t.Fatalf("Optimize(): %v", err)
	}
	if optimizer.PassCount() != 0 {
		t.Fatalf("PassCount: got %d, want 0", optimizer.PassCount())
	}
}

type testPass struct {
	name string
	fn   func(ctx *PassContext, spec *PipelineSpec) error
}

func (p *testPass) Name() string { return p.name }
func (p *testPass) Apply(ctx *PassContext, spec *PipelineSpec) error {
	return p.fn(ctx, spec)
}

func TestOptimizer_SinglePass(t *testing.T) {
	optimizer := NewOptimizer()
	applied := false
	optimizer.AddPass(&testPass{
		name: "test",
		fn: func(ctx *PassContext, spec *PipelineSpec) error {
			applied = true
			return nil
		},
	})
	spec := &PipelineSpec{Stages: []StageSpec{&ScanStageSpec{}}, RootIdx: 0}
	if err := optimizer.Optimize(spec); err != nil {
		t.Fatalf("Optimize(): %v", err)
	}
	if !applied {
		t.Fatal("pass was not applied")
	}
	if optimizer.PassCount() != 1 {
		t.Fatalf("PassCount: got %d, want 1", optimizer.PassCount())
	}
}

func TestOptimizer_MultiPassOrder(t *testing.T) {
	optimizer := NewOptimizer()
	var order []string
	optimizer.AddPass(&testPass{name: "A", fn: func(ctx *PassContext, spec *PipelineSpec) error {
		order = append(order, "A")
		return nil
	}})
	optimizer.AddPass(&testPass{name: "B", fn: func(ctx *PassContext, spec *PipelineSpec) error {
		order = append(order, "B")
		return nil
	}})
	if err := optimizer.Optimize(&PipelineSpec{Stages: []StageSpec{&ScanStageSpec{}}, RootIdx: 0}); err != nil {
		t.Fatalf("Optimize(): %v", err)
	}
	if len(order) != 2 || order[0] != "A" || order[1] != "B" {
		t.Fatalf("pass order: got %v, want [A B]", order)
	}
}

func TestOptimizer_PassErrorAborts(t *testing.T) {
	optimizer := NewOptimizer()
	optimizer.AddPass(&testPass{name: "ok", fn: func(ctx *PassContext, spec *PipelineSpec) error { return nil }})
	optimizer.AddPass(&testPass{name: "fail", fn: func(ctx *PassContext, spec *PipelineSpec) error { return errTest }})
	if err := optimizer.Optimize(&PipelineSpec{Stages: []StageSpec{&ScanStageSpec{}}, RootIdx: 0}); err != errTest {
		t.Fatalf("Optimize(): got %v, want errTest", err)
	}
}

var errTest = fmt.Errorf("test error")
