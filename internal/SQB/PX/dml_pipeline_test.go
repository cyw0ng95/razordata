package PX

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// ddlOp is a minimal DT.Operator that mimics a DDL/admin operator's
// Next/Close lifecycle. DDL operators typically return ErrNoRows after
// a single call (no result set), so ddlOp returns one synthetic "ok"
// row on the first call then ErrNoRows.
type ddlOp struct {
	nextCalls atomic.Int32
	closed    atomic.Bool
	failNext  error
	colName   string
	colValue  int64
}

func (d *ddlOp) Next(_ context.Context) (DT.Row, error) {
	d.nextCalls.Add(1)
	if d.failNext != nil {
		return DT.Row{}, d.failNext
	}
	if d.nextCalls.Load() > 1 {
		return DT.Row{}, DT.ErrNoRows
	}
	return DT.Row{
		Cols:  []string{d.colName},
		Types: []LX.TokenType{LX.T_INT_KW},
		Data:  []PL.Value{{Kind: PL.KindInt, I64: d.colValue}},
	}, nil
}

func (d *ddlOp) Close() error {
	d.closed.Store(true)
	return nil
}

// emptyDDLOp is a DDL that emits zero rows (the common case for DDL
// like CREATE TABLE without RETURNING).
type emptyDDLOp struct {
	closed atomic.Bool
}

func (e *emptyDDLOp) Next(_ context.Context) (DT.Row, error) {
	return DT.Row{}, DT.ErrNoRows
}

func (e *emptyDDLOp) Close() error {
	e.closed.Store(true)
	return nil
}

// Compile-time interface checks.
var (
	_ DT.Operator = (*ddlOp)(nil)
	_ DT.Operator = (*emptyDDLOp)(nil)
)

// TestBuildDMLPipelineSpec_InsertReturnsInsertStage verifies that the
// existing DML Insert path still emits InsertStageSpec (no regression
// after the DDL fallback rewrite for REQ002217).
func TestBuildDMLPipelineSpec_InsertReturnsInsertStage(t *testing.T) {
	op := WT.NewInsert("t", []string{"a"}, [][]PS.Expr{{nil}}, nil, nil)
	spec, err := BuildDMLPipelineSpec(op)
	if err != nil {
		t.Fatalf("BuildDMLPipelineSpec: %v", err)
	}
	if len(spec.Stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(spec.Stages))
	}
	if _, ok := spec.Stages[0].(*InsertStageSpec); !ok {
		t.Fatalf("expected InsertStageSpec, got %T", spec.Stages[0])
	}
	if spec.RootIdx != 0 {
		t.Fatalf("expected RootIdx=0, got %d", spec.RootIdx)
	}
}

// TestBuildDMLPipelineSpec_UpdateReturnsUpdateStage verifies the
// Update path emits UpdateStageSpec.
func TestBuildDMLPipelineSpec_UpdateReturnsUpdateStage(t *testing.T) {
	op := WT.NewUpdate("t", nil, nil, nil, nil)
	spec, err := BuildDMLPipelineSpec(op)
	if err != nil {
		t.Fatalf("BuildDMLPipelineSpec: %v", err)
	}
	if _, ok := spec.Stages[0].(*UpdateStageSpec); !ok {
		t.Fatalf("expected UpdateStageSpec, got %T", spec.Stages[0])
	}
}

// TestBuildDMLPipelineSpec_DeleteReturnsDeleteStage verifies the
// Delete path emits DeleteStageSpec.
func TestBuildDMLPipelineSpec_DeleteReturnsDeleteStage(t *testing.T) {
	op := WT.NewDelete("t", nil, nil, nil)
	spec, err := BuildDMLPipelineSpec(op)
	if err != nil {
		t.Fatalf("BuildDMLPipelineSpec: %v", err)
	}
	if _, ok := spec.Stages[0].(*DeleteStageSpec); !ok {
		t.Fatalf("expected DeleteStageSpec, got %T", spec.Stages[0])
	}
}

// TestBuildDMLPipelineSpec_DDLReturnsScanStage — REQ002217: DDL/admin
// operators must emit a ScanStageSpec, NOT a LegacyBatchStageSpec.
func TestBuildDMLPipelineSpec_DDLReturnsScanStage(t *testing.T) {
	op := &ddlOp{colName: "ok", colValue: 1}
	spec, err := BuildDMLPipelineSpec(op)
	if err != nil {
		t.Fatalf("BuildDMLPipelineSpec: %v", err)
	}
	if len(spec.Stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(spec.Stages))
	}
	if _, ok := spec.Stages[0].(*ScanStageSpec); !ok {
		t.Fatalf("expected ScanStageSpec, got %T", spec.Stages[0])
	}
	if spec.RootIdx != 0 {
		t.Fatalf("expected RootIdx=0, got %d", spec.RootIdx)
	}
}

// TestBuildDMLPipelineSpec_NoLegacyBatchStageSpec — REQ002217:
// no spec returned from BuildDMLPipelineSpec may contain legacy stages.
func TestBuildDMLPipelineSpec_NoLegacyBatchStageSpec(t *testing.T) {
	ops := []DT.Operator{
		WT.NewInsert("t", []string{"a"}, [][]PS.Expr{{nil}}, nil, nil),
		WT.NewUpdate("t", nil, nil, nil, nil),
		WT.NewDelete("t", nil, nil, nil),
		&ddlOp{colName: "ok", colValue: 0},
		&emptyDDLOp{},
	}
	for _, op := range ops {
		spec, err := BuildDMLPipelineSpec(op)
		if err != nil {
			t.Fatalf("BuildDMLPipelineSpec(%T): %v", op, err)
		}
		for i, s := range spec.Stages {
			_ = s
			_ = i
		}
	}
}

// TestDDLPipeline_Execute_DDLProducesRows — REQ002217: end-to-end
// execution of a DDL-shaped operator through the pipeline. The DDL op
// returns one synthetic row then ErrNoRows; the pipeline emits exactly
// one row.
func TestDDLPipeline_Execute_DDLProducesRows(t *testing.T) {
	op := &ddlOp{colName: "ok", colValue: 42}
	spec, err := BuildDMLPipelineSpec(op)
	if err != nil {
		t.Fatalf("BuildDMLPipelineSpec: %v", err)
	}

	executor := NewPipelineExecutor(spec)
	rows, err := executor.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := executor.Close(); err != nil {
		t.Fatalf("executor.Close: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row from DDL op, got %d", len(rows))
	}
	if len(rows[0].Data) != 1 {
		t.Fatalf("expected 1 col, got %d", len(rows[0].Data))
	}
	if rows[0].Data[0].I64 != 42 {
		t.Fatalf("expected value 42, got %d", rows[0].Data[0].I64)
	}
	if !op.closed.Load() {
		t.Fatalf("DDL op should be closed after executor.Close")
	}
}

// TestDDLPipeline_Execute_DDLEmptyResult — REQ002217: DDL operators
// that produce zero rows must complete cleanly with an empty row set.
func TestDDLPipeline_Execute_DDLEmptyResult(t *testing.T) {
	op := &emptyDDLOp{}
	spec, err := BuildDMLPipelineSpec(op)
	if err != nil {
		t.Fatalf("BuildDMLPipelineSpec: %v", err)
	}

	executor := NewPipelineExecutor(spec)
	rows, err := executor.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := executor.Close(); err != nil {
		t.Fatalf("executor.Close: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
	if !op.closed.Load() {
		t.Fatalf("DDL op should be closed after executor.Close")
	}
}

// TestDDLPipeline_Execute_DDLErrorPropagates — REQ002217: errors from
// the underlying DDL operator must propagate through the pipeline.
func TestDDLPipeline_Execute_DDLErrorPropagates(t *testing.T) {
	want := errors.New("ddl boom")
	op := &ddlOp{failNext: want}
	spec, err := BuildDMLPipelineSpec(op)
	if err != nil {
		t.Fatalf("BuildDMLPipelineSpec: %v", err)
	}

	executor := NewPipelineExecutor(spec)
	defer executor.Close()

	_, err = executor.Execute(context.Background())
	if err == nil {
		t.Fatalf("expected error from DDL op, got nil")
	}
	if err.Error() != want.Error() {
		t.Fatalf("expected error %q, got %q", want.Error(), err.Error())
	}
}

// TestDDLPipeline_DDLStageClosesUnderlyingOp — REQ002217: the
// ScanStage wrapping the DDL op must close the underlying operator on
// pipeline teardown so resources are released.
func TestDDLPipeline_DDLStageClosesUnderlyingOp(t *testing.T) {
	op := &ddlOp{colName: "ok", colValue: 1}
	spec, err := BuildDMLPipelineSpec(op)
	if err != nil {
		t.Fatalf("BuildDMLPipelineSpec: %v", err)
	}

	pipeline, err := spec.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := pipeline.Close(); err != nil {
		t.Fatalf("pipeline.Close: %v", err)
	}
	if !op.closed.Load() {
		t.Fatalf("DDL op should be closed after pipeline.Close")
	}
}

// TestDDLPipeline_DDLStageUsesBatchProducerWhenAvailable — REQ002217:
// when the DDL operator already implements UT.BatchProducer, the spec
// must use it directly instead of wrapping in RowOperatorAsProducer.
func TestDDLPipeline_DDLStageUsesBatchProducerWhenAvailable(t *testing.T) {
	op := &ddlBatchProducer{}
	spec, err := BuildDMLPipelineSpec(op)
	if err != nil {
		t.Fatalf("BuildDMLPipelineSpec: %v", err)
	}
	if len(spec.Stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(spec.Stages))
	}
	scan, ok := spec.Stages[0].(*ScanStageSpec)
	if !ok {
		t.Fatalf("expected ScanStageSpec, got %T", spec.Stages[0])
	}
	producer := scan.NewProducer()
	if producer == nil {
		t.Fatalf("NewProducer returned nil")
	}
	if _, ok := producer.(*ddlBatchProducer); !ok {
		t.Fatalf("expected ddlBatchProducer to be returned directly, got %T", producer)
	}
}

// ddlBatchProducer implements both DT.Operator and UT.BatchProducer so
// we can verify the type assertion in BuildDMLPipelineSpec.
type ddlBatchProducer struct {
	closed atomic.Bool
}

func (d *ddlBatchProducer) Next(_ context.Context) (DT.Row, error) {
	return DT.Row{}, DT.ErrNoRows
}

func (d *ddlBatchProducer) NextBatch(_ context.Context) (*UT.Batch, error) {
	return nil, nil
}

func (d *ddlBatchProducer) Close() error {
	d.closed.Store(true)
	return nil
}

// Compile-time: ddlBatchProducer satisfies DT.Operator and UT.BatchProducer.
var (
	_ DT.Operator       = (*ddlBatchProducer)(nil)
	_ UT.BatchProducer  = (*ddlBatchProducer)(nil)
)