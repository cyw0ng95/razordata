package PX

import (
	"context"
	"errors"
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SQB/AD"
	"github.com/cyw0ng95/razordata/internal/SQB/AG"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	MR "github.com/cyw0ng95/razordata/internal/SQB/MR"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
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

// ClearCache clears any entries in the builder's PipelineCache. Safe to
// call on a nil builder or builder with no cache (no-op). REQ002132.
func (b *PipelineBuilder) ClearCache() {
	if b == nil || b.cache == nil {
		return
	}
	b.cache.Clear()
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
// NOTE: The presence of LegacyBatchStageSpec in the returned spec means
// execution will fall back to plan-tree evaluation via RowOperatorAsProducer.
// Callers that want a pure-PX pipeline (no LegacyBatch wrappers) must check
// the returned stages themselves — e.g. in EX's queryAllBuildPipeline.
//
// The root stage's output schema is taken from decomposePlan itself (which
// tracks per-stage outputSchema bottom-up for col-index resolution). This
// covers Join/Aggregate/Sort/FusedScan/Filter/Project/Limit/Offset/Distinct
// and DML shapes that extractOutputSchema(plan.Root) used to miss. When the
// decomposition returned empty schema (unsupported op → LegacyBatchStageSpec),
// we fall back to extractOutputSchema on the root to derive at least the
// column identities for the driver's Columns() method.
func (b *PipelineBuilder) specializePlan(plan *PL.PlanResult, sql, memoKey string) (*PipelineSpec, error) {
	stages, edges, rootIdx, rootSchema := decomposePlan(plan.Root, b.planner, b.specialize)

	cols, types := rootSchema.names, rootSchema.types
	if len(cols) == 0 {
		// Fallback for LegacyBatchStageSpec roots (decomposeFallback → empty
		// outputSchema). Extract from root op via the legacy walker so the
		// driver's Columns() method still returns names for unsupported
		// operator shapes (Window, Compound, non-planner native Op types).
		cols, types = extractOutputSchema(plan.Root)
	}

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

// outputSchema describes a stage's output columns — names and rough type.
// Used during decomposition to resolve expression names to column indices
// for Sort/Aggregate/HashJoin. Slots are populated bottom-up; a nil slice
// means the child schema is unknown (we'll fall back to LegacyBatchStageSpec).
type outputSchema struct {
	names []string
	types []LX.TokenType
}

// resolved returns true if schema names are populated.
func (s outputSchema) resolved() bool { return len(s.names) > 0 }

// findCol returns the index of column name, or -1 if not present.
// Case-insensitive (matches SQL identifier semantics, consistent with
// how the planner populates Ident.Name from the FROM list).
func (s outputSchema) findCol(name string) int {
	for i, n := range s.names {
		if equalFold(n, name) {
			return i
		}
	}
	return -1
}

// equalFold is a local case-insensitive string match to avoid pulling
// in strings package.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// decomposeState accumulates stages, edges, and per-stage output
// schemas during plan decomposition. REQ002124: per-stage outputSchema
// lets us resolve Ident expressions to integer column indices for
// native StageSpec construction.
type decomposeState struct {
	stages      []StageSpec
	edges       []EdgeSpec
	stageOutput []outputSchema // parallel with stages
}

// addStage appends a stage and records its output schema (known or empty).
// Returns the stage index.
func (s *decomposeState) addStage(spec StageSpec, out outputSchema) int {
	idx := len(s.stages)
	s.stages = append(s.stages, spec)
	s.stageOutput = append(s.stageOutput, out)
	return idx
}

// addEdge records a parent→child wiring.
func (s *decomposeState) addEdge(from int, to int, side ChildSide) {
	s.edges = append(s.edges, EdgeSpec{From: from, To: to, Side: side})
}

// childOutput returns the output schema for a stage's child (by index).
func (s *decomposeState) childOutput(childIdx int) outputSchema {
	if childIdx < 0 || childIdx >= len(s.stageOutput) {
		return outputSchema{}
	}
	return s.stageOutput[childIdx]
}

// decomposePlan walks the PL.Operator tree bottom-up and produces
// StageSpecs + edges. Returns (stages, edges, rootIdx, rootSchema) where
// rootSchema is the root stage's output column names and types (populated
// by the per-type decompose step). Returns empty schema for unsupported
// operators (callers must fall back to extractOutputSchema(plan.Root)
// or legacy path).
func decomposePlan(root DT.Operator, planner PL.QueryPlanner, specialize SpecializeFunc) ([]StageSpec, []EdgeSpec, int, outputSchema) {
	if root == nil {
		return nil, nil, 0, outputSchema{}
	}

	// Unwrap AdaptiveOp — the inner operator is the actual plan.
	if aop, ok := root.(*AD.AdaptiveOp); ok {
		root = aop.Inner
	}

	state := &decomposeState{}
	rootIdx := decomposeOp(root, state, planner, specialize)
	schema := outputSchema{}
	if rootIdx >= 0 && rootIdx < len(state.stageOutput) {
		schema = state.stageOutput[rootIdx]
	}
	return state.stages, state.edges, rootIdx, schema
}

// decomposeOp recursively decomposes a single operator into stages.
// Returns the index of the stage that produces output for this operator.
// First, it checks for the FusedScan candidate pattern (Limit → Project → Filter → SeqScan)
// to emit a single FusedScanStageSpec (REQ002124).
func decomposeOp(op DT.Operator, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	// FusedScan detection: try Limit→Project→Filter→SeqScan before the
	// default per-type dispatch. If the pattern matches we create a single stage
	// and skip the nested child decomposition.
	if idx, ok := tryDecomposeFusedScan(op, st, planner, specialize); ok {
		return idx
	}
	switch o := op.(type) {
	case *OP.SeqScan:
		return decomposeSeqScan(o, st)
	case *OP.IndexScan:
		// IndexScan has different op wiring; can't extract the
		// row-operator for a native ScanStageSpec without a schema.
		// Fall back to LegacyBatchStageSpec → specializePlan errors
		// → EX falls back to legacy plan-tree execution, which is
		// still correct (planner produces IndexScan → legacy OP loop).
		return decomposeFallback(o, st, planner, specialize)
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
	case *OP.Distinct:
		return decomposeDistinct(o, st, planner, specialize)
	case *AG.Aggregate:
		return decomposeAggregate(o, st, planner, specialize)
	case *AG.HashAggregate:
		return decomposeHashAggregate(o, st, planner, specialize)
	case *AG.WindowOperator:
		// WindowStageSpec isn't implemented yet (REQ002128); fall back to
		// LegacyBatchStageSpec but propagate the window's output column
		// names (WindowOperator.Cols()) so the pipeline still carries
		// OutputCols metadata → fast path can use it instead of bailing.
		cols := o.Cols()
		schema := outputSchema{}
		if len(cols) > 0 {
			schema.names = append([]string(nil), cols...)
			schema.types = make([]LX.TokenType, len(cols))
			for i := range cols {
				schema.types[i] = LX.T_TEXT
			}
		}
		return st.addStage(&LegacyBatchStageSpec{
			Root:       o,
			Planner:    planner,
			Specialize: specialize,
		}, schema)
	case *OP.CompoundOp:
		// CompoundStageSpec not yet implemented (REQ002128); fall back but
		// propagate left-child's output schema (UNION/EXCEPT/INTERSECT all
		// have shape compatible with left child per planner).
		childIdx := decomposeOp(o.LeftChild(), st, planner, specialize)
		leftSchema := st.childOutput(childIdx)
		if !leftSchema.resolved() {
			leftSchema.names, leftSchema.types = extractOutputSchema(o.LeftChild())
		}
		return st.addStage(&LegacyBatchStageSpec{
			Root:       o,
			Planner:    planner,
			Specialize: specialize,
		}, leftSchema)
	case *WT.Insert:
		return decomposeDML(&InsertStageSpec{Insert: o}, st)
	case *WT.Update:
		return decomposeDML(&UpdateStageSpec{Update: o}, st)
	case *WT.Delete:
		return decomposeDML(&DeleteStageSpec{Delete: o}, st)
	default:
		return decomposeFallback(op, st, planner, specialize)
	}
}

// decomposeSeqScan creates a ScanStageSpec for a SeqScan or IndexScan.
// Output schema comes from the scan's StoreSchema (Cols + ColTypes) if known,
// populated by the planner via Schema() method on OP.SeqScan.
func decomposeSeqScan(ss *OP.SeqScan, st *decomposeState) int {
	var op DT.Operator = ss
	var out outputSchema
	if ss != nil {
		if sch := ss.Schema(); sch != nil && len(sch.Cols) > 0 {
			n := len(sch.Cols)
			names := make([]string, n)
			types := make([]LX.TokenType, n)
			copy(names, sch.Cols)
			if len(sch.ColTypes) > 0 {
				copy(types, sch.ColTypes)
			}
			out = outputSchema{names: names, types: types}
		}
	}
	return st.addStage(&ScanStageSpec{
		NewProducer: func() UT.BatchProducer {
			return NewRowOperatorAsProducer(op)
		},
	}, out)
}

// decomposeFilter creates FilterStageSpec with a child edge. Output
// Filter preserves its child's schema unchanged.
func decomposeFilter(f *OP.Filter, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(f.Child(), st, planner, specialize)
	childOut := st.childOutput(childIdx)
	filterIdx := st.addStage(&FilterStageSpec{Pred: f.Predicate()}, childOut)
	st.addEdge(filterIdx, childIdx, SingleChild)
	return filterIdx
}

// decomposeProject creates ProjectStageSpec with a child edge. Output
// schema is derived from the projected expressions' display names.
func decomposeProject(p *OP.Project, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(p.Child(), st, planner, specialize)
	exprs := p.Cols()
	n := len(exprs)
	names := make([]string, n)
	types := make([]LX.TokenType, n)
	for i, e := range exprs {
		names[i] = exprName(e)
		types[i] = LX.T_TEXT
	}
	projectOut := outputSchema{names: names, types: types}
	projectIdx := st.addStage(&ProjectStageSpec{Exprs: exprs}, projectOut)
	st.addEdge(projectIdx, childIdx, SingleChild)
	return projectIdx
}

// decomposeSort native SortStageSpec — emits SortCols / Desc arrays
// populated from the child's output schema. REQ002124.
//
// When the child schema is available (each OrderItem.Expr resolve to all Ident
// whose names exist in the child's output columns) we build the sort column
// indices directly. Otherwise fall back to LegacyBatchStageSpec (rare and
// the legacy vectorized path, which is still correct but can be slow.
func decomposeSort(s *OP.Sort, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(s.Child(), st, planner, specialize)
	childOut := st.childOutput(childIdx)
	keys := s.Keys()
	if childOut.resolved() {
		sortCols := make([]int, 0, len(keys))
		desc := make([]bool, 0, len(keys))
		allResolved := true
		for _, k := range keys {
			idx := -1
			switch e := k.Expr.(type) {
			case *PS.Ident:
				idx = childOut.findCol(e.Name)
				if idx == -1 && e.SlotIdx >= 0 {
					idx = e.SlotIdx
				}
			}
			if idx == -1 {
				allResolved = false
				break
			}
			sortCols = append(sortCols, idx)
			desc = append(desc, k.Desc)
		}
		if allResolved && len(sortCols) > 0 {
			sortIdx := st.addStage(&SortStageSpec{
				SortCols: sortCols,
				Desc:     desc,
			}, childOut)
			st.addEdge(sortIdx, childIdx, SingleChild)
			return sortIdx
		}
	}
	// Fallback: couldn't resolve all keys. Wrap in legacy.
	sortIdx := st.addStage(&LegacyBatchStageSpec{
		Root:       s,
		Planner:    planner,
		Specialize: specialize,
	}, childOut)
	st.addEdge(sortIdx, childIdx, SingleChild)
	return sortIdx
}

// decomposeLimit creates LimitStageSpec. Child schema passes through unchanged.
func decomposeLimit(l *OP.Limit, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(l.Child(), st, planner, specialize)
	childOut := st.childOutput(childIdx)
	limitIdx := st.addStage(&LimitStageSpec{Limit: l.LimitValue()}, childOut)
	st.addEdge(limitIdx, childIdx, SingleChild)
	return limitIdx
}

// decomposeOffset creates OffsetStageSpec. Child schema passes through unchanged.
func decomposeOffset(o *OP.Offset, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(o.Child(), st, planner, specialize)
	childOut := st.childOutput(childIdx)
	offsetIdx := st.addStage(&OffsetStageSpec{Offset: o.OffsetValue()}, childOut)
	st.addEdge(offsetIdx, childIdx, SingleChild)
	return offsetIdx
}

// decomposeHashJoin native HashJoinStageSpec. Resolves left/right key
// names against left/right child schemas. Falls back to LegacyBatchStageSpec
// if names not resolvable. REQ002124.
func decomposeHashJoin(h *OP.HashJoin, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	leftIdx := decomposeOp(h.LeftChild(), st, planner, specialize)
	rightIdx := decomposeOp(h.RightChild(), st, planner, specialize)
	leftOut := st.childOutput(leftIdx)
	rightOut := st.childOutput(rightIdx)
	leftKeys := h.LeftKeys()
	rightKeys := h.RightKeys()

	probeKeys := make([]int, 0, len(leftKeys))
	buildKeys := make([]int, 0, len(rightKeys))
	allResolved := len(leftKeys) == len(rightKeys) && len(leftKeys) > 0
	if allResolved {
		for i := range leftKeys {
			li := leftOut.findCol(leftKeys[i])
			ri := rightOut.findCol(rightKeys[i])
			if li == -1 || ri == -1 {
				allResolved = false
				break
			}
			probeKeys = append(probeKeys, li)
			buildKeys = append(buildKeys, ri)
		}
	}

	var joinOut outputSchema
	if leftOut.resolved() && rightOut.resolved() {
		n1, n2 := len(leftOut.names), len(rightOut.names)
		names := make([]string, 0, n1+n2)
		types := make([]LX.TokenType, 0, n1+n2)
		names = append(names, leftOut.names...)
		types = append(types, leftOut.types...)
		names = append(names, rightOut.names...)
		types = append(types, rightOut.types...)
		joinOut = outputSchema{names: names, types: types}
	}
	if allResolved {
		// Map OP.JoinKind (string) → PX.JoinKind (uint8 enum).
		var jk JoinKind
		switch h.Kind() {
		case OP.JoinKindLeft:
			jk = JoinKindLeft
		case OP.JoinKindRight:
			jk = JoinKindRight
		case OP.JoinKindFull:
			jk = JoinKindFull
		default:
			jk = JoinKindInner
		}
		joinIdx := st.addStage(&HashJoinStageSpec{
			BuildKeys: buildKeys,
			ProbeKeys: probeKeys,
			Kind:      jk,
		}, joinOut)
		st.addEdge(joinIdx, leftIdx, LeftChild)
		st.addEdge(joinIdx, rightIdx, RightChild)
		return joinIdx
	}
	// Fallback: couldn't resolve all keys
	joinIdx := st.addStage(&LegacyBatchStageSpec{
		Root:       h,
		Planner:    planner,
		Specialize: specialize,
	}, joinOut)
	st.addEdge(joinIdx, leftIdx, LeftChild)
	st.addEdge(joinIdx, rightIdx, RightChild)
	return joinIdx
}

// decomposeAggregate native AggregateStageSpec. Resolves group column
// groupCols) and extracts aggregate func specs (col indices from agg
// expressions. REQ002124.
//
// When all group columns and aggregate arguments resolve to child output
// indices, builds native AggregateStageSpec with the UnifiedAccumulatorSpec
// array. Otherwise falls back to LegacyBatchStageSpec.
// decomposeAggregate emits a native AggregateStageSpec when the child's
// output schema can resolve all group-by column indices AND all aggregate
// function arguments. Falls back to LegacyBatchStageSpec otherwise, but
// ALWAYS populates the output schema (GroupCols + Aggs names) so the
// pipeline carries OutputCols metadata — this is essential for the
// BuildPipeline fast-path gate (len(OutputCols) > 0) to trip.
func decomposeAggregate(agg *AG.Aggregate, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	var childIdx int
	var childOut outputSchema
	if agg != nil {
		childIdx = decomposeOp(agg.Child(), st, planner, specialize)
		childOut = st.childOutput(childIdx)
	} else {
		// nil agg shouldn't happen post-refactor (caller uses
		// decomposeHashAggregate instead). Defensive: empty stage.
		childIdx = st.addStage(&LegacyBatchStageSpec{
			Root:       nil,
			Planner:    planner,
			Specialize: specialize,
		}, outputSchema{})
	}

	// If agg = HashAggregate fallback to legacy via empty spec.
	if agg == nil {
		aggIdx := st.addStage(&AggregateStageSpec{}, outputSchema{})
		st.addEdge(aggIdx, childIdx, SingleChild)
		return aggIdx
	}

	groupExprs := agg.GroupCols()
	aggExprs := agg.Aggs()

	groupCols := make([]int, 0, len(groupExprs))
	specs := make([]MR.AccumulatorSpec, 0, len(aggExprs))
	allResolved := true

	// Resolve group cols
	if childOut.resolved() {
		for _, g := range groupExprs {
			idx := -1
			switch e := g.(type) {
			case *PS.Ident:
				idx = childOut.findCol(e.Name)
				if idx == -1 && e.SlotIdx >= 0 {
					idx = e.SlotIdx
				}
			}
			if idx == -1 {
				allResolved = false
				break
			}
			groupCols = append(groupCols, idx)
		}
	} else if len(groupExprs) > 0 {
		allResolved = false
	}

	// Resolve aggregate functions
	if allResolved && childOut.resolved() {
		for _, ae := range aggExprs {
			spec, ok := resolveAggFunc(ae, childOut)
			if !ok {
				allResolved = false
				break
			}
			// DISTINCT aggregates require a dedup stage that isn't yet
			// implemented in the pure-PX pipeline; bail to legacy path.
			if spec.Distinct {
				allResolved = false
				break
			}
			// GROUP_CONCAT / STRING_AGG have sep arg + concat semantics
			// not yet fully implemented in the native AggregateStage
			// accumulators; bail.
			switch spec.Kind {
			case MR.AggGroupConcat, MR.AggStringAgg:
				allResolved = false
				break
			}
			if !allResolved {
				break
			}
			specs = append(specs, spec)
		}
	} else {
		allResolved = false
	}

	// Compute the aggregate's output schema: groupExprs (by name) + aggExprs
	// (by name, either resolved kind display name or raw exprName). Even
	// when allResolved=false and we fall back to LegacyBatchStageSpec, the
	// caller still needs OutputCols metadata (so the fast-path len check in
	// EX doesn't bail to the legacy parse→plan loop). Hence we build
	// aggOut unconditionally from expressions, not just resolved specs.
	aggNames := make([]string, 0, len(groupExprs)+len(aggExprs))
	aggTypes := make([]LX.TokenType, 0, len(groupExprs)+len(aggExprs))
	for _, g := range groupExprs {
		aggNames = append(aggNames, exprName(g))
		aggTypes = append(aggTypes, LX.T_INT_KW)
	}
	// For unresolved expressions (no matching PS.AggregateFunc, or child
	// schema unknown) fall back to exprName. For resolved ones prefer the
	// accumulator's known display/kind name.
	resolvedIdx := 0
	for i, ae := range aggExprs {
		_ = i
		var name string
		var typ LX.TokenType
		if allResolved && resolvedIdx < len(specs) {
			name = aggFuncDisplayName(specs[resolvedIdx].Kind)
			typ = aggFuncReturnType(specs[resolvedIdx].Kind)
			resolvedIdx++
		} else {
			name = exprName(ae)
			typ = LX.T_TEXT
			// Only advance resolvedIdx when allResolved — otherwise specs
			// may be empty due to early bail in resolve loop above.
		}
		aggNames = append(aggNames, name)
		aggTypes = append(aggTypes, typ)
	}
	aggOut := outputSchema{names: aggNames, types: aggTypes}

	if allResolved {
		keyCols := groupCols
		if len(keyCols) == 0 {
			keyCols = nil
		}
		aggIdx := st.addStage(&AggregateStageSpec{
			Specs:     specs,
			GroupCols: groupCols,
			KeyCols:   keyCols,
		}, aggOut)
		st.addEdge(aggIdx, childIdx, SingleChild)
		return aggIdx
	}
	// Fallback. Even when the native StageSpec can't execute, we attach
	// aggOut so the root's output schema is known → BuildPipeline fast
	// path's len(OutputCols)>0 check still passes for this shape, and
	// pipeline execution falls back through LegacyBatchStageSpec.
	aggIdx := st.addStage(&LegacyBatchStageSpec{
		Root:       agg,
		Planner:    planner,
		Specialize: specialize,
	}, aggOut)
	st.addEdge(aggIdx, childIdx, SingleChild)
	return aggIdx
}

// decomposeHashAggregate mirrors decomposeAggregate but for HashAggregate.
// Output schema derivation (GroupCols + Aggs) is identical; the native
// HashJoinStageSpec isn't implemented yet (REQ002143) so it always falls
// through to LegacyBatchStageSpec with a populated output schema.
func decomposeHashAggregate(agg *AG.HashAggregate, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(agg.Child(), st, planner, specialize)

	groupExprs := agg.GroupCols()
	aggExprs := agg.Aggs()

	names := make([]string, 0, len(groupExprs)+len(aggExprs))
	types := make([]LX.TokenType, 0, len(groupExprs)+len(aggExprs))
	for _, g := range groupExprs {
		names = append(names, exprName(g))
		types = append(types, LX.T_INT_KW)
	}
	for _, ae := range aggExprs {
		names = append(names, exprName(ae))
		types = append(types, LX.T_TEXT)
	}
	out := outputSchema{names: names, types: types}

	// Native HashAggregateStageSpec is REQ002143 scope (decomposePlan
	// native stages). For now fall back to LegacyBatchStageSpec so
	// the operator tree still runs correctly — but the output schema
	// is populated, so PipelineSpec.OutputCols is non-empty →
	// BuildPipeline fast path stays active.
	aggIdx := st.addStage(&LegacyBatchStageSpec{
		Root:       agg,
		Planner:    planner,
		Specialize: specialize,
	}, out)
	st.addEdge(aggIdx, childIdx, SingleChild)
	return aggIdx
}

// decomposeDistinct wraps a Distinct operator in LegacyBatchStageSpec for
// now (DistinctStageSpec exists but requires column-index resolution against
// the child schema — REQ002143 for the native emit). It always propagates
// the child's output schema unchanged so OutputCols metadata survives the
// round-trip through the pipeline.
func decomposeDistinct(d *OP.Distinct, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(d.Child(), st, planner, specialize)
	schema := st.childOutput(childIdx)
	if !schema.resolved() {
		// Child's decomposition didn't know its schema (e.g. SeqScan with
		// an unknown StoreSchema); fall back to extractOutputSchema via
		// the legacy walker.
		schema.names, schema.types = extractOutputSchema(d.Child())
	}
	return st.addStage(&LegacyBatchStageSpec{
		Root:       d,
		Planner:    planner,
		Specialize: specialize,
	}, schema)
}

// resolveAggFunc parses an aggregate function expression (e.g. SUM(col),
// COUNT(DISTINCT col), GROUP_CONCAT(x, ',')) and returns an
// MR.AccumulatorSpec with child column index + flags. Child's output
// schema is used to resolve argument column names. Second return is ok=false
// if child column couldn't be resolved or function is unsupported.
func resolveAggFunc(expr PS.Expr, childOut outputSchema) (MR.AccumulatorSpec, bool) {
	fn, ok := expr.(*PS.AggregateFunc)
	if !ok {
		return MR.AccumulatorSpec{}, false
	}
	var col int = -1
	if fn.Arg != nil {
		switch a := fn.Arg.(type) {
		case *PS.Ident:
			col = childOut.findCol(a.Name)
			if col == -1 && a.SlotIdx >= 0 {
				col = a.SlotIdx
			}
		case *PS.StarExpr:
			// COUNT(*) — col remains -1
		}
	}
	kind, ok := aggKindByName(fn.Name)
	if !ok {
		return MR.AccumulatorSpec{}, false
	}
	// col = -1 is valid only for COUNT (counts rows regardless of arg)
	if col == -1 && kind != MR.AggCount {
		return MR.AccumulatorSpec{}, false
	}
	var separator string
	if fn.Separator != nil {
		if s, ok := fn.Separator.(*PS.StringLiteral); ok {
			separator = s.Val
		}
	}
	return MR.AccumulatorSpec{
		Kind:      kind,
		Col:       col,
		Separator: separator,
		Distinct:  fn.Distinct,
	}, true
}

// aggKindByName maps a SQL aggregate function name (case-insensitive)
// to an MR.AccumKind. The second return is false if unrecognized.
func aggKindByName(name string) (MR.AccumKind, bool) {
	switch {
	case equalFold(name, "count"):
		return MR.AggCount, true
	case equalFold(name, "sum"):
		return MR.AggSum, true
	case equalFold(name, "min"):
		return MR.AggMin, true
	case equalFold(name, "max"):
		return MR.AggMax, true
	case equalFold(name, "avg"):
		return MR.AggAvg, true
	case equalFold(name, "group_concat"):
		return MR.AggGroupConcat, true
	case equalFold(name, "string_agg"):
		return MR.AggStringAgg, true
	}
	return 0, false
}

// aggFuncDisplayName returns the column display name for an aggregate
// function kind used in aggregate output schema.
func aggFuncDisplayName(k MR.AccumKind) string {
	switch k {
	case MR.AggCount:
		return "count(*)"
	case MR.AggSum:
		return "sum(expr)"
	case MR.AggMin:
		return "min(expr)"
	case MR.AggMax:
		return "max(expr)"
	case MR.AggAvg:
		return "avg(expr)"
	case MR.AggGroupConcat:
		return "group_concat(expr)"
	case MR.AggStringAgg:
		return "string_agg(expr)"
	}
	return "agg"
}

// aggFuncReturnType returns a rough result column type.
func aggFuncReturnType(k MR.AccumKind) LX.TokenType {
	switch k {
	case MR.AggCount:
		return LX.T_INT_KW
	case MR.AggMin, MR.AggMax:
		return LX.T_NULL // actual type determined at runtime
	case MR.AggAvg, MR.AggSum:
		return LX.T_FLOAT_KW // conservative guess
	case MR.AggGroupConcat, MR.AggStringAgg:
		return LX.T_TEXT
	}
	return LX.T_TEXT
}

// decomposeDML creates a DML StageSpec (Insert/Update/Delete). Output
// schema is empty (DML outputs no result cols unless RETURNING — caller
// handles that at the executor layer.
func decomposeDML(spec StageSpec, st *decomposeState) int {
	return st.addStage(spec, outputSchema{})
}

// decomposeFallback wraps the operator in a LegacyBatchStageSpec.
// Output schema empty (will fallbacks treat it unknown).
func decomposeFallback(op DT.Operator, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	return st.addStage(&LegacyBatchStageSpec{
		Root:       op,
		Planner:    planner,
		Specialize: specialize,
	}, outputSchema{})
}

// tryDecomposeFusedScan is the fusedScan candidate detection.
// Pattern: Limit? → Project? → Filter? → (SeqScan / IndexScan)
// Emits single FusedScanStageSpec.
// Returns (stageIdx, true) if fused, else (0, false).
// REQ002124 — same eligibility as vec_transform.go:tryFusedBatchScan.
func tryDecomposeFusedScan(op DT.Operator, st *decomposeState, _ PL.QueryPlanner, _ SpecializeFunc) (int, bool) {
	var limitOp *OP.Limit
	var projectOp *OP.Project
	var filterOp *OP.Filter
	var seqScan *OP.SeqScan
	cur := op
	if l, ok := cur.(*OP.Limit); ok {
		limitOp = l
		cur = l.Child()
	}
	if cur == nil {
		return 0, false
	}
	if proj, ok := cur.(*OP.Project); ok {
		projectOp = proj
		cur = proj.Child()
	}
	if cur == nil {
		return 0, false
	}
	if filt, ok := cur.(*OP.Filter); ok {
		filterOp = filt
		cur = filt.Child()
	}
	if cur == nil {
		return 0, false
	}
	ss, ok := cur.(*OP.SeqScan)
	if !ok {
		return 0, false
	}
	seqScan = ss
	// FusedScan requires a populated scan schema. vec_transform.go's
	// tryFusedBatchScan runs only on planner-built trees where SeqScan
	// schemas are always populated (Cols, ColTypes, store-backed). This
	// guard also prevents fusion on shallow test-only scans
	// (OP.NewSeqScan("t1") with sch==nil), keeping test expectations for
	// separate stages intact.
	sch := ss.Schema()
	if sch == nil || len(sch.Cols) == 0 {
		return 0, false
	}
	// At least Filter, Project, or Limit must be present.
	if projectOp == nil && filterOp == nil && limitOp == nil {
		return 0, false
	}
	// Build scan output schema from SeqScan's StoreSchema.
	n := len(sch.Cols)
	names := make([]string, n)
	types := make([]LX.TokenType, n)
	copy(names, sch.Cols)
	if len(sch.ColTypes) > 0 {
		copy(types, sch.ColTypes)
	}
	scanOut := outputSchema{names: names, types: types}

	var pred PS.Expr
	if filterOp != nil {
		pred = filterOp.Predicate()
	}
	var exprs []PS.Expr
	var fusedNames []string
	if projectOp != nil {
		exprs = projectOp.Cols()
		fusedNames = make([]string, len(exprs))
		for i, e := range exprs {
			fusedNames[i] = exprName(e)
		}
	} else if scanOut.resolved() {
		fusedNames = scanOut.names
	}

	var limit int64 = -1
	if limitOp != nil {
		limit = limitOp.LimitValue()
	}

	// Build output schema for the fused stage: project names if project,
	// else scan names.
	var fusedOut outputSchema
	if len(fusedNames) > 0 {
		outNames := make([]string, len(fusedNames))
		copy(outNames, fusedNames)
		fusedTypes := make([]LX.TokenType, len(outNames))
		if projectOp != nil {
			for i := range outNames {
				fusedTypes[i] = LX.T_TEXT
			}
		} else if scanOut.resolved() {
			copy(fusedTypes, scanOut.types)
		}
		fusedOut = outputSchema{names: outNames, types: fusedTypes}
	}

	scanOp := seqScan
	fusedIdx := st.addStage(&FusedScanStageSpec{
		SourceFactory: func() UT.BatchProducer {
			return NewRowOperatorAsProducer(scanOp)
		},
		Pred:  pred,
		Exprs: exprs,
		Names: fusedNames,
		Limit: limit,
	}, fusedOut)
	return fusedIdx, true
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
//
// NOTE: This function is the fallback when decomposePlan couldn't
// resolve the output schema (i.e. the root is an unsupported op
// wrapped in LegacyBatchStageSpec). Prefer the decomposition's own
// rootSchema return value — it's populated bottom-up for every
// native stage shape and covers cases the legacy walker misses.
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
	case *OP.IndexScan:
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
	case *OP.Distinct:
		return extractOutputSchema(o.Child())
	case *OP.HashJoin:
		// HashJoin merges left + right output schemas. When sharedCols
		// is non-empty (equi-join column deduplication set by the
		// planner), we use that instead of a naive concat because the
		// runtime uses sharedCols (plus sharedTypes) as the output
		// shape for deduplicated projections (matches USING-col semantics
		// in SQL).
		if len(o.SharedCols()) > 0 {
			names := append([]string(nil), o.SharedCols()...)
			types := append([]LX.TokenType(nil), o.SharedTypes()...)
			// Append remaining non-shared cols from each side in order.
			lNames, lTypes := extractOutputSchema(o.LeftChild())
			rNames, rTypes := extractOutputSchema(o.RightChild())
			shared := make(map[string]struct{}, len(names))
			for _, n := range names {
				shared[n] = struct{}{}
			}
			for i, n := range lNames {
				if _, dup := shared[n]; dup {
					continue
				}
				shared[n] = struct{}{}
				names = append(names, n)
				if i < len(lTypes) {
					types = append(types, lTypes[i])
				} else {
					types = append(types, LX.T_TEXT)
				}
			}
			for i, n := range rNames {
				if _, dup := shared[n]; dup {
					continue
				}
				shared[n] = struct{}{}
				names = append(names, n)
				if i < len(rTypes) {
					types = append(types, rTypes[i])
				} else {
					types = append(types, LX.T_TEXT)
				}
			}
			return names, types
		}
		lNames, lTypes := extractOutputSchema(o.LeftChild())
		rNames, rTypes := extractOutputSchema(o.RightChild())
		if len(lNames) == 0 && len(rNames) == 0 {
			return nil, nil
		}
		names := make([]string, 0, len(lNames)+len(rNames))
		names = append(names, lNames...)
		names = append(names, rNames...)
		types := make([]LX.TokenType, 0, len(lTypes)+len(rTypes))
		types = append(types, lTypes...)
		types = append(types, rTypes...)
		// Defensive: pad types so len matches names (one of the sides
		// may have returned names but unknown types, common for
		// projections with runtime-discovered types).
		for len(types) < len(names) {
			types = append(types, LX.T_TEXT)
		}
		return names, types
	case *OP.CompoundOp:
		// UNION ALL / UNION / EXCEPT / INTERSECT — the compound
		// operator produces a result with the same shape as the
		// left child (both sides must be compatible per planner).
		return extractOutputSchema(o.LeftChild())
	case *AG.Aggregate:
		return extractAggOutput(o.GroupCols(), o.Aggs())
	case *AG.HashAggregate:
		return extractAggOutput(o.GroupCols(), o.Aggs())
	case *AG.WindowOperator:
		cols := o.Cols()
		if len(cols) == 0 {
			return nil, nil
		}
		names := make([]string, len(cols))
		types := make([]LX.TokenType, len(cols))
		copy(names, cols)
		for i := range names {
			types[i] = LX.T_TEXT // window funcs have runtime types
		}
		return names, types
	default:
		return nil, nil
	}
}

// extractAggOutput extracts output columns from an aggregate operator.
// Aggregation output is always: [GroupCols in order] + [Agg funcs in order].
// Group names come from the expression (Ident.Name for bare columns, Alias
// for aliased exprs, "expr" for complex exprs). Agg func names come from
// exprName too (typically "agg0", "agg1" if planner assigned aliases).
// Types are placeholder INT_KW for groups; AGG() runtime determines true
// types via ValueKind inspection.
func extractAggOutput(groupCols []PS.Expr, aggFns []PS.Expr) ([]string, []LX.TokenType) {
	total := len(groupCols) + len(aggFns)
	if total == 0 {
		return nil, nil
	}
	names := make([]string, 0, total)
	types := make([]LX.TokenType, 0, total)
	for _, gc := range groupCols {
		names = append(names, exprName(gc))
		types = append(types, LX.T_INT_KW)
	}
	for _, af := range aggFns {
		names = append(names, exprName(af))
		types = append(types, LX.T_TEXT)
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
		producer = UT.NewBatchToRowAdapter(NewRowOperatorAsProducer(s.Root))
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
//
// Calls op.Next() at most once: row-based operators are typically
// stateful and not safe to call Next() multiple times (e.g., ALTER TABLE
// re-executes the schema mutation on every call). Pipeline draining
// must not drive Next() in a loop.
type RowOperatorAsProducer struct {
	Op PL.Operator
}

// NewRowOperatorAsProducer creates a RowOperatorAsProducer.
func NewRowOperatorAsProducer(op PL.Operator) *RowOperatorAsProducer {
	return &RowOperatorAsProducer{Op: op}
}

func (r *RowOperatorAsProducer) NextBatch(ctx context.Context) (*UT.Batch, error) {
	row, err := r.Op.Next(ctx)
	if err != nil {
		if err == DT.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	// Convert single row to batch.
	// Preserve row.Cols (column names) so ToRowsShared downstream
	// can reconstruct rows with correct column metadata. REQ002133:
	// otherwise RowOperatorAsProducer→ToRowsShared returns nil rows
	// because ToRowsShared short-circuits on empty ColNames.
	n := len(row.Data)
	batch := UT.GetBatch(n)
	batch.Size = 1
	if len(row.Cols) >= n {
		for i := 0; i < n; i++ {
			batch.Cols[i].Name = row.Cols[i]
		}
	} else {
		// Some operators (e.g. expression-only SELECTs, subquery
		// wrappers) may not populate row.Cols even when Data has
		// values. ToRowsShared requires non-empty ColNames, so
		// synthesize unique placeholder names.
		for i := 0; i < n; i++ {
			batch.Cols[i].Name = fmt.Sprintf("col%d", i)
		}
	}
	for i, v := range row.Data {
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
			batch.Cols[i].Type = LX.T_NULL
		}
	}
	return batch, nil
}

func (r *RowOperatorAsProducer) Close() error {
	return r.Op.Close()
}
