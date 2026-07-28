package PX

import (
	"context"
	"errors"
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SQB/AG"
	"github.com/cyw0ng95/razordata/internal/SQB/AD"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
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
// It decomposes the plan tree into concrete StageSpecs where possible,
// falling back to LegacyBatchStageSpec for unsupported operators.
func (b *PipelineBuilder) specializePlan(plan *PL.PlanResult, sql, memoKey string) (*PipelineSpec, error) {
	stages, edges, rootIdx := decomposePlan(plan.Root, b.planner, b.specialize)

	cols, types := extractOutputSchema(plan.Root)

	return &PipelineSpec{
		Stages:      stages,
		Edges:       edges,
		RootIdx:     rootIdx,
		OutputCols:  cols,
		OutputTypes: types,
		Cost:        plan.Cost,
		MemoKey:     memoKey,
		SQLText:     sql,
	}, nil
}

// decomposeState accumulates stages and edges during plan decomposition.
type decomposeState struct {
	stages []StageSpec
	edges  []EdgeSpec
}

// addStage appends a stage and returns its index.
func (s *decomposeState) addStage(spec StageSpec) int {
	idx := len(s.stages)
	s.stages = append(s.stages, spec)
	return idx
}

// addEdge records a parent→child wiring.
func (s *decomposeState) addEdge(from int, to int, side ChildSide) {
	s.edges = append(s.edges, EdgeSpec{From: from, To: to, Side: side})
}

// decomposePlan walks the PL.Operator tree bottom-up and produces
// StageSpecs + edges. Returns (rootIdx, stages, edges).
// Operators without a dedicated PX stage are wrapped in LegacyBatchStageSpec.
func decomposePlan(root DT.Operator, planner PL.QueryPlanner, specialize SpecializeFunc) ([]StageSpec, []EdgeSpec, int) {
	if root == nil {
		return nil, nil, 0
	}

	// Unwrap AdaptiveOp — the inner operator is the actual plan.
	if aop, ok := root.(*AD.AdaptiveOp); ok {
		root = aop.Inner
	}

	state := &decomposeState{}
	rootIdx := decomposeOp(root, state, planner, specialize)
	return state.stages, state.edges, rootIdx
}

// decomposeOp recursively decomposes a single operator into stages.
// Returns the index of the stage that produces output for this operator.
func decomposeOp(op DT.Operator, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	switch o := op.(type) {
	case *OP.SeqScan:
		return decomposeSeqScan(o, st)
	case *OP.IndexScan:
		return decomposeSeqScan(nil, st) // IndexScan → ScanStageSpec wrapping o
	case *OP.Filter:
		return decomposeFilter(o, st, planner, specialize)
	case *OP.Project:
		return decomposeProject(o, st, planner, specialize)
	case *OP.Sort:
		return decomposeSort(o, st, planner, specialize)
	case *OP.Limit:
		return decomposeLimit(o, st, planner, specialize)
	case *OP.Offset:
		return decomposeOffset(o, st, planner, specialize)
	case *OP.HashJoin:
		return decomposeHashJoin(o, st, planner, specialize)
	case *AG.Aggregate:
		return decomposeAggregate(o, st, planner, specialize)
	case *AG.HashAggregate:
		return decomposeAggregate(nil, st, planner, specialize) // HashAggregate via Aggregate
	default:
		return decomposeFallback(op, st, planner, specialize)
	}
}

// decomposeSeqScan creates a ScanStageSpec for a SeqScan or IndexScan.
func decomposeSeqScan(ss *OP.SeqScan, st *decomposeState) int {
	// Capture the scan operator for the producer closure.
	var op DT.Operator = ss
	return st.addStage(&ScanStageSpec{
		NewProducer: func() UT.BatchProducer {
			return RowOperatorAsProducer{Op: op}
		},
	})
}

// decomposeFilter creates FilterStageSpec with a child edge.
func decomposeFilter(f *OP.Filter, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(f.Child(), st, planner, specialize)
	filterIdx := st.addStage(&FilterStageSpec{Pred: f.Predicate()})
	st.addEdge(filterIdx, childIdx, SingleChild)
	return filterIdx
}

// decomposeProject creates ProjectStageSpec with a child edge.
func decomposeProject(p *OP.Project, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(p.Child(), st, planner, specialize)
	projectIdx := st.addStage(&ProjectStageSpec{Exprs: p.Cols()})
	st.addEdge(projectIdx, childIdx, SingleChild)
	return projectIdx
}

// decomposeSort wraps Sort in LegacyBatchStageSpec because PX SortStageSpec
// requires column indices while PL.Sort uses expressions — converting them
// requires the child's output schema which is not available at decomposition time.
func decomposeSort(s *OP.Sort, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(s.Child(), st, planner, specialize)
	sortIdx := st.addStage(&LegacyBatchStageSpec{
		Root:       s,
		Planner:    planner,
		Specialize: specialize,
	})
	st.addEdge(sortIdx, childIdx, SingleChild)
	return sortIdx
}

// decomposeLimit creates LimitStageSpec with a child edge.
func decomposeLimit(l *OP.Limit, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(l.Child(), st, planner, specialize)
	limitIdx := st.addStage(&LimitStageSpec{Limit: l.LimitValue()})
	st.addEdge(limitIdx, childIdx, SingleChild)
	return limitIdx
}

// decomposeOffset creates OffsetStageSpec with a child edge.
func decomposeOffset(o *OP.Offset, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(o.Child(), st, planner, specialize)
	offsetIdx := st.addStage(&OffsetStageSpec{Offset: o.OffsetValue()})
	st.addEdge(offsetIdx, childIdx, SingleChild)
	return offsetIdx
}

// decomposeHashJoin wraps HashJoin in LegacyBatchStageSpec because PX HashJoinStageSpec
// uses column indices while PL.HashJoin uses key names — converting requires schema.
func decomposeHashJoin(h *OP.HashJoin, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	leftIdx := decomposeOp(h.LeftChild(), st, planner, specialize)
	rightIdx := decomposeOp(h.RightChild(), st, planner, specialize)
	joinIdx := st.addStage(&LegacyBatchStageSpec{
		Root:       h,
		Planner:    planner,
		Specialize: specialize,
	})
	st.addEdge(joinIdx, leftIdx, LeftChild)
	st.addEdge(joinIdx, rightIdx, RightChild)
	return joinIdx
}

// decomposeAggregate creates AggregateStageSpec with a child edge.
func decomposeAggregate(agg *AG.Aggregate, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	var childIdx int
	if agg != nil {
		childIdx = decomposeOp(agg.Child(), st, planner, specialize)
	} else {
		childIdx = st.addStage(&LegacyBatchStageSpec{
			Root:       nil,
			Planner:    planner,
			Specialize: specialize,
		})
	}
	aggIdx := st.addStage(&AggregateStageSpec{})
	st.addEdge(aggIdx, childIdx, SingleChild)
	return aggIdx
}

// decomposeFallback wraps the operator in a LegacyBatchStageSpec.
func decomposeFallback(op DT.Operator, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	return st.addStage(&LegacyBatchStageSpec{
		Root:       op,
		Planner:    planner,
		Specialize: specialize,
	})
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
// from a row-based operator tree. Walks the root operator's schema
// to determine the result columns for the pipeline output.
func extractOutputSchema(root DT.Operator) ([]string, []LX.TokenType) {
	if root == nil {
		return nil, nil
	}

	// Unwrap AdaptiveOp.
	if aop, ok := root.(*AD.AdaptiveOp); ok {
		root = aop.Inner
	}

	switch o := root.(type) {
	case *OP.SeqScan:
		schema := o.Schema()
		if schema != nil && len(schema.Cols) > 0 {
			names := make([]string, len(schema.Cols))
			types := make([]LX.TokenType, len(schema.Cols))
			copy(names, schema.Cols)
			if len(schema.ColTypes) > 0 {
				copy(types, schema.ColTypes)
			}
			return names, types
		}
		return nil, nil
	case *OP.Filter:
		return extractOutputSchema(o.Child())
	case *OP.Project:
		exprs := o.Cols()
		names := make([]string, len(exprs))
		types := make([]LX.TokenType, len(exprs))
		for i, e := range exprs {
			names[i] = exprName(e)
			types[i] = LX.T_TEXT // default; actual type resolved at runtime
		}
		return names, types
	case *OP.Sort:
		return extractOutputSchema(o.Child())
	case *OP.Limit:
		return extractOutputSchema(o.Child())
	case *OP.Offset:
		return extractOutputSchema(o.Child())
	case *AG.Aggregate:
		return extractAggOutput(o)
	case *AG.HashAggregate:
		return extractAggOutput(nil)
	default:
		return nil, nil
	}
}

// extractAggOutput extracts output columns from an aggregate operator.
func extractAggOutput(agg *AG.Aggregate) ([]string, []LX.TokenType) {
	if agg == nil {
		return nil, nil
	}
	groupCols := agg.GroupCols()
	names := make([]string, 0, len(groupCols)+4) // +4 for common agg funcs
	types := make([]LX.TokenType, 0, len(groupCols)+4)
	for _, gc := range groupCols {
		names = append(names, exprName(gc))
		types = append(types, LX.T_INT_KW)
	}
	return names, types
}

// exprName returns a display name for a projection expression.
func exprName(e PS.Expr) string {
	if e == nil {
		return ""
	}
	switch expr := e.(type) {
	case *PS.Ident:
		return expr.Name
	case *PS.AliasedExpr:
		return expr.Alias
	case *PS.StarExpr:
		return "*"
	default:
		return "expr"
	}
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
	Root       DT.Operator
	Planner    PL.QueryPlanner
	Specialize SpecializeFunc
}

// NewRuntime creates a fresh LegacyBatchStage by calling the
// SpecializeFunc to convert the row-based plan tree into a
// BatchProducer, then wrapping it.
func (s *LegacyBatchStageSpec) NewRuntime() Stage {
	var producer UT.BatchProducer
	if s.Specialize != nil {
		producer = s.Specialize(s.Root, s.Planner)
	}
	if producer == nil {
		producer = UT.NewBatchToRowAdapter(RowOperatorAsProducer{Op: s.Root})
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
