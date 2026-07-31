package PX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

func makeMultiColBatch(intVals []int64, floatVals []float64) *UT.Batch {
	n := len(intVals)
	if len(floatVals) > n {
		n = len(floatVals)
	}
	b := UT.GetBatch(2)
	b.Pooled = false
	b.Cols[0].Type = LX.T_INT_KW
	b.SetColumnName(0, "a")
	b.Cols[0].Data.Ints = make([]int64, len(intVals))
	copy(b.Cols[0].Data.Ints, intVals)
	b.Cols[0].Nulls = make([]bool, len(intVals))
	b.Cols[1].Type = LX.T_FLOAT_KW
	b.SetColumnName(1, "b")
	b.Cols[1].Data.Floats = make([]float64, len(floatVals))
	copy(b.Cols[1].Data.Floats, floatVals)
	b.Cols[1].Nulls = make([]bool, len(floatVals))
	b.Size = n
	return b
}

func TestProjectStageSpec_Category(t *testing.T) {
	spec := &ProjectStageSpec{Exprs: []PS.Expr{}, Names: []string{}}
	if spec.Category() != CatTransform {
		t.Errorf("expected CatTransform, got %v", spec.Category())
	}
}

func TestProjectStage_NextBatch(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeMultiColBatch([]int64{1, 2, 3}, []float64{1.1, 2.2, 3.3}),
	}}
	project := &ProjectStage{
		child: child,
		exprs: []PS.Expr{
			&PS.Ident{Name: "a", SlotIdx: 0},
			&PS.Ident{Name: "b", SlotIdx: 1},
		},
		names: []string{"x", "y"},
	}
	defer project.Close()

	batch, err := project.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}
	if len(batch.Cols) < 2 {
		t.Fatalf("expected at least 2 columns, got %d", len(batch.Cols))
	}
	if batch.Cols[0].Name != "x" {
		t.Fatalf("expected col 0 name 'x', got %q", batch.Cols[0].Name)
	}
	if batch.Cols[1].Name != "y" {
		t.Fatalf("expected col 1 name 'y', got %q", batch.Cols[1].Name)
	}
}

func TestProjectStage_NextBatch_EOF(t *testing.T) {
	child := &mockStage{batches: nil}
	project := &ProjectStage{
		child: child,
		exprs: []PS.Expr{&PS.Ident{Name: "v", SlotIdx: 0}},
		names: []string{"v"},
	}
	defer project.Close()

	batch, err := project.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch at EOF")
	}
}

func TestProjectStage_NextBatch_DoneAfterEOF(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1}),
	}}
	project := &ProjectStage{
		child: child,
		exprs: []PS.Expr{&PS.Ident{Name: "v", SlotIdx: 0}},
		names: []string{"v"},
	}
	defer project.Close()

	// First call gets the batch.
	_, err := project.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Second call hits EOF.
	_, err = project.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Third call returns nil immediately because done=true.
	batch, err := project.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil after done")
	}
}

func TestProjectStage_Reset(t *testing.T) {
	project := &ProjectStage{done: true}
	err := project.Reset(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if project.done {
		t.Fatal("expected done=false after reset")
	}
}

func TestProjectStage_SetChild(t *testing.T) {
	project := &ProjectStage{}
	child := &mockStage{}
	project.SetChild(SingleChild, child)
	if project.child != child {
		t.Fatal("expected child to be set")
	}
}

func TestProjectStage_Close(t *testing.T) {
	child := &mockStage{}
	project := &ProjectStage{child: child}
	if err := project.Close(); err != nil {
		t.Fatal(err)
	}
	if !child.closed {
		t.Fatal("expected child to be closed")
	}
}

func TestProjectStage_WithCompiledEvals(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{10, 20, 30}),
	}}
	project := &ProjectStage{
		child: child,
		exprs: []PS.Expr{&PS.Ident{Name: "v", SlotIdx: 0}},
		names: []string{"doubled"},
		compiledEvals: []batchEvalFunc{
			func(batch *UT.Batch) UT.Column {
				n := batch.LogicalSize()
				col := UT.Column{Name: "doubled", Type: LX.T_INT_KW}
				col.Data.Ints = make([]int64, n)
				for i := range n {
					col.Data.Ints[i] = batch.Cols[0].Data.Ints[i] * 2
				}
				return col
			},
		},
	}
	defer project.Close()

	batch, err := project.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Cols[0].Data.Ints[0] != 20 || batch.Cols[0].Data.Ints[1] != 40 || batch.Cols[0].Data.Ints[2] != 60 {
		t.Fatalf("expected [20,40,60], got %v", batch.Cols[0].Data.Ints[:3])
	}
}

func TestCompactColumn_Int(t *testing.T) {
	col := UT.Column{Name: "v", Type: LX.T_INT_KW}
	col.Data.Ints = []int64{10, 20, 30, 40, 50}
	col.Nulls = []bool{false, false, true, false, false}
	sel := []uint16{1, 3}
	out := UT.CompactColumn(col, sel, 5)
	if len(out.Data.Ints) != 2 {
		t.Fatalf("expected 2 ints, got %d", len(out.Data.Ints))
	}
	if out.Data.Ints[0] != 20 || out.Data.Ints[1] != 40 {
		t.Fatalf("expected [20,40], got %v", out.Data.Ints)
	}
	if out.Nulls[1] != false {
		t.Fatalf("expected nulls[1]=false, got %v", out.Nulls[1])
	}
}

func TestCompactColumn_Float(t *testing.T) {
	col := UT.Column{Name: "f", Type: LX.T_FLOAT_KW}
	col.Data.Floats = []float64{1.1, 2.2, 3.3}
	sel := []uint16{0, 2}
	out := UT.CompactColumn(col, sel, 3)
	if len(out.Data.Floats) != 2 {
		t.Fatalf("expected 2 floats, got %d", len(out.Data.Floats))
	}
	if out.Data.Floats[0] != 1.1 || out.Data.Floats[1] != 3.3 {
		t.Fatalf("expected [1.1,3.3], got %v", out.Data.Floats)
	}
}

func TestCompactColumn_EmptySel(t *testing.T) {
	col := UT.Column{Name: "v", Type: LX.T_INT_KW}
	col.Data.Ints = []int64{1, 2, 3}
	out := UT.CompactColumn(col, nil, 3)
	// Empty sel returns the column as-is.
	if out.Data.Ints[0] != 1 {
		t.Fatalf("expected identity compact, got %v", out.Data.Ints)
	}
}
