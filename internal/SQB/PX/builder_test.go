package PX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/AD"
	"github.com/cyw0ng95/razordata/internal/SQB/AG"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestLegacyBatchStageSpec_NewRuntime(t *testing.T) {
	batches := []*UT.Batch{makeIntBatch([]int64{1, 2, 3})}
	spec := &LegacyBatchStageSpec{
		Specialize: func(_ DT.Operator, _ PL.QueryPlanner) UT.BatchProducer {
			return &simpleProducer{batches: batches}
		},
	}

	stage := spec.NewRuntime()
	if stage == nil {
		t.Fatal("expected non-nil stage")
	}
	if stage.(*LegacyBatchStage) == nil {
		t.Fatal("expected *LegacyBatchStage")
	}
}

func TestLegacyBatchStage_NextBatch(t *testing.T) {
	batches := []*UT.Batch{makeIntBatch([]int64{1, 2})}
	stage := &LegacyBatchStage{producer: &simpleProducer{batches: batches}}

	ctx := context.Background()
	batch, err := stage.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected batch")
	}
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	// EOF
	batch2, err := stage.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch2 != nil {
		t.Fatal("expected EOF")
	}
}

func TestLegacyBatchStage_Reset_NotSupported(t *testing.T) {
	stage := &LegacyBatchStage{producer: &simpleProducer{}}
	err := stage.Reset(context.Background())
	if err != ErrResetNotSupported {
		t.Fatalf("expected ErrResetNotSupported, got %v", err)
	}
}

func TestLegacyBatchStage_Close_Idempotent(t *testing.T) {
	stage := &LegacyBatchStage{producer: &simpleProducer{}}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyBatchStage_NextBatch_Closed(t *testing.T) {
	stage := &LegacyBatchStage{producer: &simpleProducer{}, closed: true}
	_, err := stage.NextBatch(context.Background())
	if err == nil {
		t.Fatal("expected error on closed stage")
	}
}

func TestLegacyBatchStageSpec_Category(t *testing.T) {
	spec := &LegacyBatchStageSpec{}
	if spec.Category() != CatSource {
		t.Fatalf("expected CatSource, got %v", spec.Category())
	}
}

// --- simpleProducer: minimal BatchProducer for testing ---

type simpleProducer struct {
	batches []*UT.Batch
	idx     int
}

func (p *simpleProducer) NextBatch(_ context.Context) (*UT.Batch, error) {
	if p.idx >= len(p.batches) {
		return nil, nil
	}
	b := p.batches[p.idx]
	p.idx++
	return b, nil
}

func (p *simpleProducer) Close() error { return nil }

// --- decomposePlan tests ---

func TestDecomposePlan_NilRoot(t *testing.T) {
	stages, edges, rootIdx := decomposePlan(nil, nil, nil)
	if len(stages) != 0 {
		t.Fatalf("expected 0 stages, got %d", len(stages))
	}
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges, got %d", len(edges))
	}
	if rootIdx != 0 {
		t.Fatalf("expected rootIdx 0, got %d", rootIdx)
	}
}

func TestDecomposePlan_SeqScan(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	stages, edges, rootIdx := decomposePlan(ss, nil, nil)

	if len(stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(stages))
	}
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges, got %d", len(edges))
	}
	if rootIdx != 0 {
		t.Fatalf("expected rootIdx 0, got %d", rootIdx)
	}
	if _, ok := stages[0].(*ScanStageSpec); !ok {
		t.Fatalf("expected *ScanStageSpec, got %T", stages[0])
	}
}

func TestDecomposePlan_FilterSeqScan(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
	}
	filter := OP.NewFilter(ss, pred, nil)

	stages, edges, rootIdx := decomposePlan(filter, nil, nil)

	if len(stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(stages))
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	// Bottom-up: stage 0 = scan, stage 1 = filter (root)
	if rootIdx != 1 {
		t.Fatalf("expected rootIdx 1, got %d", rootIdx)
	}

	// Stage 0 should be ScanStageSpec (child added first)
	if _, ok := stages[0].(*ScanStageSpec); !ok {
		t.Fatalf("stage 0: expected *ScanStageSpec, got %T", stages[0])
	}
	// Stage 1 should be FilterStageSpec (root added last)
	if _, ok := stages[1].(*FilterStageSpec); !ok {
		t.Fatalf("stage 1: expected *FilterStageSpec, got %T", stages[1])
	}
	// Edge: filter(1) → scan(0)
	if edges[0].From != 1 || edges[0].To != 0 || edges[0].Side != SingleChild {
		t.Fatalf("unexpected edge: %+v", edges[0])
	}
}

func TestDecomposePlan_ProjectFilterSeqScan(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
	}
	filter := OP.NewFilter(ss, pred, nil)
	proj := OP.NewProject(filter, []PS.Expr{
		&PS.Ident{Name: "a"},
		&PS.Ident{Name: "b"},
	})

	stages, edges, rootIdx := decomposePlan(proj, nil, nil)

	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}
	// Bottom-up: scan(0), filter(1), project(2=root)
	if rootIdx != 2 {
		t.Fatalf("expected rootIdx 2, got %d", rootIdx)
	}

	// Stage 0: ScanStageSpec, Stage 1: FilterStageSpec, Stage 2: ProjectStageSpec
	if _, ok := stages[0].(*ScanStageSpec); !ok {
		t.Fatalf("stage 0: expected *ScanStageSpec, got %T", stages[0])
	}
	if _, ok := stages[1].(*FilterStageSpec); !ok {
		t.Fatalf("stage 1: expected *FilterStageSpec, got %T", stages[1])
	}
	if _, ok := stages[2].(*ProjectStageSpec); !ok {
		t.Fatalf("stage 2: expected *ProjectStageSpec, got %T", stages[2])
	}

	// Edges: filter(1) → scan(0), project(2) → filter(1) (bottom-up order)
	if edges[0].From != 1 || edges[0].To != 0 {
		t.Fatalf("edge 0: unexpected: %+v", edges[0])
	}
	if edges[1].From != 2 || edges[1].To != 1 {
		t.Fatalf("edge 1: unexpected: %+v", edges[1])
	}
}

func TestDecomposePlan_LimitOffset(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	limit := OP.NewLimit(ss, 10)
	offset := OP.NewOffset(limit, 5)

	stages, edges, rootIdx := decomposePlan(offset, nil, nil)

	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}
	// Bottom-up: scan(0), limit(1), offset(2=root)
	if rootIdx != 2 {
		t.Fatalf("expected rootIdx 2, got %d", rootIdx)
	}

	// Stage 0: ScanStageSpec, Stage 1: LimitStageSpec, Stage 2: OffsetStageSpec
	if _, ok := stages[0].(*ScanStageSpec); !ok {
		t.Fatalf("stage 0: expected *ScanStageSpec, got %T", stages[0])
	}
	if _, ok := stages[1].(*LimitStageSpec); !ok {
		t.Fatalf("stage 1: expected *LimitStageSpec, got %T", stages[1])
	}
	if _, ok := stages[2].(*OffsetStageSpec); !ok {
		t.Fatalf("stage 2: expected *OffsetStageSpec, got %T", stages[2])
	}
}

func TestDecomposePlan_FallbackToLegacy(t *testing.T) {
	// Sort falls back to LegacyBatchStageSpec
	ss := OP.NewSeqScan("t1")
	sort := OP.NewSort(ss, nil)

	stages, _, rootIdx := decomposePlan(sort, nil, nil)

	if len(stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(stages))
	}
	// Bottom-up: scan(0), sort(1=root)
	if rootIdx != 1 {
		t.Fatalf("expected rootIdx 1, got %d", rootIdx)
	}
	// Stage 0 should be ScanStageSpec
	if _, ok := stages[0].(*ScanStageSpec); !ok {
		t.Fatalf("stage 0: expected *ScanStageSpec, got %T", stages[0])
	}
	// Stage 1 should be LegacyBatchStageSpec (Sort fallback)
	if _, ok := stages[1].(*LegacyBatchStageSpec); !ok {
		t.Fatalf("stage 1: expected *LegacyBatchStageSpec, got %T", stages[1])
	}
}

func TestDecomposePlan_UnwrapAdaptiveOp(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	aop := AD.NewAdaptiveOp(ss, "test-hash")

	stages, _, rootIdx := decomposePlan(aop, nil, nil)

	// AdaptiveOp should be unwrapped, producing ScanStageSpec
	if len(stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(stages))
	}
	if rootIdx != 0 {
		t.Fatalf("expected rootIdx 0, got %d", rootIdx)
	}
	if _, ok := stages[0].(*ScanStageSpec); !ok {
		t.Fatalf("stage 0: expected *ScanStageSpec, got %T", stages[0])
	}
}

func TestDecomposePlan_PipelineIntegration(t *testing.T) {
	// Build Filter(SeqScan) and verify it produces a working pipeline
	ss := OP.NewSeqScan("t1")
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
	}
	filter := OP.NewFilter(ss, pred, nil)

	stages, edges, rootIdx := decomposePlan(filter, nil, nil)
	spec := &PipelineSpec{
		Stages:  stages,
		Edges:   edges,
		RootIdx: rootIdx,
	}

	pipe, err := spec.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()

	// Pipeline should be valid (stages created, edges wired)
	if pipe == nil {
		t.Fatal("expected non-nil pipeline")
	}
}

func TestExtractOutputSchema_SeqScan(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	cols, types := extractOutputSchema(ss)
	// SeqScan without registered schema returns nil
	if cols != nil {
		t.Fatalf("expected nil cols, got %v", cols)
	}
	if types != nil {
		t.Fatalf("expected nil types, got %v", types)
	}
}

func TestExtractOutputSchema_Filter(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
	}
	filter := OP.NewFilter(ss, pred, nil)
	cols, types := extractOutputSchema(filter)
	// Filter passes through child schema
	if cols != nil {
		t.Fatalf("expected nil cols, got %v", cols)
	}
	_ = types
}

func TestExtractOutputSchema_Project(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	proj := OP.NewProject(ss, []PS.Expr{
		&PS.Ident{Name: "id"},
		&PS.AliasedExpr{Expr: &PS.Ident{Name: "name"}, Alias: "n"},
	})
	cols, types := extractOutputSchema(proj)

	if len(cols) != 2 {
		t.Fatalf("expected 2 cols, got %d", len(cols))
	}
	if cols[0] != "id" || cols[1] != "n" {
		t.Fatalf("unexpected cols: %v", cols)
	}
	if len(types) != 2 {
		t.Fatalf("expected 2 types, got %d", len(types))
	}
}

func TestExtractOutputSchema_Aggregate(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	agg := AG.NewAggregate(ss,
		[]PS.Expr{&PS.Ident{Name: "age"}},
		nil,
	)
	cols, types := extractOutputSchema(agg)

	if len(cols) != 1 {
		t.Fatalf("expected 1 col, got %d: %v", len(cols), cols)
	}
	if cols[0] != "age" {
		t.Fatalf("expected col 'age', got %q", cols[0])
	}
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
}

func TestExtractOutputSchema_Limit(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	limit := OP.NewLimit(ss, 10)
	cols, types := extractOutputSchema(limit)
	_ = cols
	_ = types
	// Limit passes through child schema
}
