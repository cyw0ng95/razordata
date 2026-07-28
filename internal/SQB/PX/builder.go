package PX

import (
	"context"
	"errors"
	"fmt"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// SpecializeFunc converts a row-based operator tree into a
// BatchProducer. It encapsulates the tryVectorizePlan logic
// from SQB/EX without creating an import cycle — the Executor
// provides the concrete implementation when constructing the
// PipelineBuilder.
type SpecializeFunc func(root DT.Operator, planner PL.QueryPlanner) UT.BatchProducer

// PipelineBuilder is the single entry point for the compile flow:
//
//	cache check → parse → rewrite → plan → specialize → cache
//
// It replaces the current scattered compilation across Executor
// entry points (Query, QueryAll, Exec, CompilePlan).
type PipelineBuilder struct {
	cache      *PipelineCache
	planner    PL.QueryPlanner
	specialize SpecializeFunc
}

// NewPipelineBuilder creates a builder with the given cache,
// planner interface, and specialization function.
func NewPipelineBuilder(cache *PipelineCache, planner PL.QueryPlanner, specialize SpecializeFunc) *PipelineBuilder {
	return &PipelineBuilder{
		cache:      cache,
		planner:    planner,
		specialize: specialize,
	}
}

// Build compiles SQL into a PipelineSpec. The flow:
//  1. Check cache by exact SQL text
//  2. Parse the SQL
//  3. Check cache by memo key (parameterized)
//  4. Rewrite (constant folding, boolean simplification)
//  5. Plan the query
//  6. Specialize: convert plan tree → PipelineSpec
//  7. Cache the result
func (b *PipelineBuilder) Build(sql string) (*PipelineSpec, error) {
	// 1. Check cache by exact SQL text
	if b.cache != nil {
		if spec, _ := b.cache.GetByText(sql); spec != nil {
			return spec, nil
		}
	}

	// 2. Parse
	parser := PS.NewParser(sql)
	defer parser.Close()
	stmt, err := parser.Parse()
	if err != nil {
		return nil, fmt.Errorf("px: parse: %w", err)
	}

	// 3. Check cache by memo key (parameterized)
	memoKey := encodeMemoKey(stmt)
	if b.cache != nil && memoKey != "" {
		if spec := b.cache.GetByMemo(memoKey); spec != nil {
			// Store in text cache for next exact-SQL hit
			b.cache.Put(spec, stmt)
			return spec, nil
		}
	}

	// 4. Rewrite (constant folding, boolean simplification)
	stmt, err = RE.Rewrite(stmt)
	if err != nil {
		return nil, fmt.Errorf("px: rewrite: %w", err)
	}

	// 5. Plan
	plan, err := b.planner.Plan(stmt)
	if err != nil {
		return nil, fmt.Errorf("px: plan: %w", err)
	}
	if plan == nil || plan.Root == nil {
		return nil, errors.New("px: plan produced no root")
	}

	// 6. Specialize: convert plan tree → PipelineSpec
	spec, err := b.specializePlan(plan, sql, memoKey)
	if err != nil {
		return nil, fmt.Errorf("px: specialize: %w", err)
	}

	// 7. Cache the result
	if b.cache != nil {
		b.cache.Put(spec, stmt)
	}

	return spec, nil
}

// specializePlan converts a row-based plan tree into a PipelineSpec.
// For REQ002123, this creates a single-stage PipelineSpec wrapping
// the current tryVectorizePlan result via LegacyBatchStageSpec.
// Later REQs (002124-002127) will add concrete StageSpec types.
func (b *PipelineBuilder) specializePlan(plan *PL.PlanResult, sql, memoKey string) (*PipelineSpec, error) {
	stageSpec := &LegacyBatchStageSpec{
		root:       plan.Root,
		planner:    b.planner,
		specialize: b.specialize,
	}

	cols, types := extractOutputSchema(plan.Root)

	return &PipelineSpec{
		Stages:      []StageSpec{stageSpec},
		Edges:       nil, // single stage, no edges
		RootIdx:     0,
		OutputCols:  cols,
		OutputTypes: types,
		Cost:        plan.Cost,
		MemoKey:     memoKey,
		SQLText:     sql,
	}, nil
}

// encodeMemoKey produces a parameterized cache key from a parsed
// statement. For REQ002123, this uses the PlanResult's MemoKey
// when available. A simplified key is produced from the statement
// type + table names when MemoKey is not yet available.
func encodeMemoKey(stmt PS.Stmt) string {
	if stmt == nil {
		return ""
	}
	// Use the parser's memo key if available.
	// For now, produce a simple key from statement type.
	switch s := stmt.(type) {
	case *PS.Select:
		return "sel:" + selectTables(s)
	default:
		return ""
	}
}

// selectTables extracts the table name from a Select.
func selectTables(s *PS.Select) string {
	if s == nil {
		return ""
	}
	return s.From
}

// extractOutputSchema returns the output column names and types
// from a row-based operator tree. For now, returns empty slices
// since schema extraction requires walking the operator tree.
// Full schema extraction is implemented in REQ002124 (specialize).
func extractOutputSchema(root DT.Operator) ([]string, []LX.TokenType) {
	// TODO(REQ002124): full schema extraction from plan tree
	return nil, nil
}

// --- LegacyBatchStageSpec (bridge) ---

// LegacyBatchStageSpec is the bridge StageSpec that wraps the
// current tryVectorizePlan result. It holds a row-based operator
// tree and a SpecializeFunc; NewRuntime() calls the specialize
// func to produce a BatchProducer, then wraps it in a
// LegacyBatchStage.
//
// This allows the PX package to work without modifying any existing
// operator. Concrete StageSpec types (AggregateStageSpec,
// SortStageSpec, etc.) replace this in later REQs.
type LegacyBatchStageSpec struct {
	root       DT.Operator
	planner    PL.QueryPlanner
	specialize SpecializeFunc
}

// NewRuntime creates a fresh LegacyBatchStage by calling the
// SpecializeFunc to convert the row-based plan tree into a
// BatchProducer, then wrapping it.
func (s *LegacyBatchStageSpec) NewRuntime() Stage {
	var producer UT.BatchProducer
	if s.specialize != nil {
		producer = s.specialize(s.root, s.planner)
	}
	if producer == nil {
		// Fallback: wrap the row operator in a BatchToRowAdapter
		// that produces one-row batches. This handles DML and other
		// non-vectorizable operators.
		producer = UT.NewBatchToRowAdapter(RowOperatorAsProducer{Op: s.root})
	}
	return &LegacyBatchStage{producer: producer}
}

// Category returns CatSource for the legacy single-stage pipeline.
func (s *LegacyBatchStageSpec) Category() StageCategory {
	return CatSource
}

// --- LegacyBatchStage ---

// LegacyBatchStage wraps a UT.BatchProducer as a Stage.
// Reset is not supported — returns ErrResetNotSupported.
type LegacyBatchStage struct {
	producer UT.BatchProducer
	closed   bool
}

// NextBatch delegates to the inner BatchProducer.
func (s *LegacyBatchStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if s.closed {
		return nil, errors.New("px: nextbatch on closed legacy stage")
	}
	return s.producer.NextBatch(ctx)
}

// Reset is not supported for legacy stages. The Pipeline handles
// this by recreating the stage from its spec.
func (s *LegacyBatchStage) Reset(_ context.Context) error {
	return ErrResetNotSupported
}

// Close releases the inner BatchProducer.
func (s *LegacyBatchStage) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.producer.Close()
}

// --- RowOperatorAsProducer ---

// RowOperatorAsProducer adapts a PL.Operator to produce
// single-row batches. This is the minimal adapter for
// non-vectorizable operators (DML, etc.). Exported for use
// by the Executor's PipelineBuilder specialize function. REQ002132.
type RowOperatorAsProducer struct {
	Op PL.Operator
}

func (r RowOperatorAsProducer) NextBatch(ctx context.Context) (*UT.Batch, error) {
	row, err := r.Op.Next(ctx)
	if err != nil {
		if err == DT.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	// Convert single row to batch
	batch := UT.GetBatch(len(row.Data))
	batch.Size = 1
	for i, v := range row.Data {
		batch.Cols[i].Name = ""
		switch v.Kind {
		case PL.KindInt:
			batch.Cols[i].Data.Ints = []int64{v.I64}
			batch.Cols[i].Type = LX.T_INT_KW
		case PL.KindFloat:
			batch.Cols[i].Data.Floats = []float64{v.F64}
			batch.Cols[i].Type = LX.T_FLOAT_KW
		case PL.KindText:
			batch.Cols[i].Data.Strs = []string{v.S}
			batch.Cols[i].Type = LX.T_TEXT
		default:
			batch.Cols[i].Nulls = []bool{true}
		}
	}
	return batch, nil
}

func (r RowOperatorAsProducer) Close() error {
	return r.Op.Close()
}
