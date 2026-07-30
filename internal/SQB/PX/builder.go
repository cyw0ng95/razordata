package PX

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cyw0ng95/razordata/internal/SQB/AG"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
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

	// 3. Check cache by memo key. Use PL.SerializeKey — a full-AST
	// fingerprint that includes the WHERE clause, joins, group-by, and
	// order-by. Unlike PL.EncodeMemoKey (which parameterizes comparison
	// literals), SerializeKey keeps literals in the digest: the PX path
	// bakes literal values into StageSpecs at specialization time
	// (e.g. OffsetStageSpec{Offset: n}), so two queries that differ
	// only in a literal must NOT share a spec. REQ002152: the previous
	// simplified encoder omitted the WHERE clause entirely, causing
	// `EXISTS (SELECT 1 FROM t2)` and `EXISTS (SELECT 1 FROM t2 WHERE
	// t2.tid = t1.id)` to collide on the same memo key.
	memoKey := PL.SerializeKey(stmt)
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

	// NOTE: propagatePlannerToTree is intentionally NOT called here.
	// The plan tree is shared with the planner's memo cache, and
	// modifying the tree (via WithPlanner) corrupts the cached plan
	// for the legacy path. The planner is propagated inside the
	// Specialize function, which runs only when the pipeline is
	// actually executed (NewRuntime). REQ002149.

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
// a PipelineSpec — a list of StageSpecs + EdgeSpecs describing how
// stages connect. Returns the stages, edges, root-stage index, and
// the root stage's outputSchema.
// REQ002156: if the plan tree contains nested joins (bushy join shape),
// fall back to a single LegacyBatchStageSpec for the entire tree to
// preserve correctness of transitive/bridge predicates.
func decomposePlan(root DT.Operator, planner PL.QueryPlanner, specialize SpecializeFunc) ([]StageSpec, []EdgeSpec, int, outputSchema) {
	if root == nil {
		return nil, nil, 0, outputSchema{}
	}
	inner := root
	// REQ002171: AdaptiveOp removed — root is the raw operator.
	// REQ002156: detect bushy join shapes (nested joins) and fall back
	// to a single LegacyBatchStageSpec. Native stage decomposition
	// doesn't yet handle transitive predicates across nested joins
	// correctly, and the pipeline build process can have side effects
	// on the planner's memo cache that affect subsequent legacy
	// execution.
	if hasBushyJoin(inner) {
		state := &decomposeState{}
		idx := state.addStage(&LegacyBatchStageSpec{
			Root:       root,
			Planner:    planner,
			Specialize: specialize,
		}, outputSchema{})
		return state.stages, state.edges, idx, outputSchema{}
	}
	state := &decomposeState{}
	rootIdx := decomposeOp(inner, state, planner, specialize)
	schema := outputSchema{}
	if rootIdx >= 0 && rootIdx < len(state.stageOutput) {
		schema = state.stageOutput[rootIdx]
	}
	return state.stages, state.edges, rootIdx, schema
}

// hasBushyJoin reports whether the operator tree contains nested joins.
// REQ002156: bushy join shapes (e.g. NLJ(HashJoin(HashJoin(...)))) are
// not yet handled correctly by native stage decomposition.
func hasBushyJoin(op DT.Operator) bool {
	if op == nil {
		return false
	}
	// REQ002171: AdaptiveOp removed — op is the raw operator.
	type leftRighter interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}
	lr, isJoin := op.(leftRighter)
	if !isJoin {
		return false
	}
	// Check if either child is also a join (bushy shape).
	leftJoin := isJoinOp(lr.LeftChild())
	rightJoin := isJoinOp(lr.RightChild())
	if leftJoin || rightJoin {
		return true
	}
	// Recurse into children to find deeper nesting.
	return hasBushyJoin(lr.LeftChild()) || hasBushyJoin(lr.RightChild())
}

// decomposeHashJoin native HashJoinStageSpec. Resolves left/right key
// names against left/right child schemas. Falls back to LegacyBatchStageSpec
// if names not resolvable. REQ002124.

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
		// REQ002143: IndexScan now implements UsedCols() and can be
		// handled by the same ScanStageSpec as SeqScan.
		return decomposeSeqScan(o, st)
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
	case *OP.HashCrossJoin:
		return decomposeHashCrossJoin(o, st, planner, specialize)
	case *OP.NestedLoopJoin:
		return decomposeNestedLoopJoin(o, st, planner, specialize)
	case *OP.Distinct:
		return decomposeDistinct(o, st, planner, specialize)
	case *AG.Aggregate:
		return decomposeAggregate(o, st, planner, specialize)
	case *AG.WindowOperator:
		return decomposeWindow(o, st)
	case *OP.CompoundOp:
		return decomposeCompound(o, st, planner, specialize)
	case *WT.Insert:
		return decomposeDML(&InsertStageSpec{Insert: o}, st)
	case *WT.Update:
		return decomposeDML(&UpdateStageSpec{Update: o}, st)
	case *WT.Delete:
		return decomposeDML(&DeleteStageSpec{Delete: o}, st)
	case *OP.ConstRow:
		// ConstRow is a trivially cheap one-shot operator (COUNT(*) fast path).
		// Use native ScanStageSpec — wraps directly as a source stage.
		return decomposeNativeSource(o, st)
	case *OP.FusedScan:
		// FusedScan is an internal optimization that already applies
		// filter + project in a tight loop. Use native ScanStageSpec —
		// it's already efficient and doesn't need LegacyBatch machinery.
		return decomposeNativeSource(o, st)
	case *OP.Values:
		// Values produces constant rows from a VALUES clause.
		// Use native ScanStageSpec — wraps directly as a source stage.
		return decomposeNativeSource(o, st)
	case *OP.ValuesRows:
		// REQ002193: multi-row VALUES statements. Native source stage.
		return decomposeNativeSource(o, st)
	case *OP.FilterProject:
		// REQ002194: standalone FilterProject (not part of FusedScan pattern).
		// Already fuses filter+project efficiently; wrap as native source.
		return decomposeNativeSource(o, st)
	case *OP.MergeJoin:
		// REQ002195: MergeJoin. Native source stage.
		return decomposeNativeSource(o, st)
	case *OP.BitmapHeapScan:
		// REQ002196: BitmapHeapScan. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.CreateTable:
		// REQ002201: DDL. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.DropTable:
		// REQ002201: DDL. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.CreateIndex:
		// REQ002202: Index DDL. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.DropIndex:
		// REQ002202: Index DDL. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.Trigger:
		// REQ002203: Trigger DDL. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.DropTrigger:
		// REQ002203: Trigger DDL. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.Explain:
		// REQ002204: EXPLAIN. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.Pragma:
		// REQ002205: Admin. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.Truncate:
		// REQ002205: Admin. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.Reindex:
		// REQ002205: Admin. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.DropView:
		// REQ002206: View/attach. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.AttachOp:
		// REQ002206: View/attach. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.DetachOp:
		// REQ002206: View/attach. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.UnsupportedOp:
		// REQ002207: Unsupported op. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.CreateMatViewOperator:
		// REQ002208: MatView. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.RefreshMatViewOperator:
		// REQ002208: MatView. Native source stage.
		return decomposeNativeSource(o, st)
	case *WT.DropMatViewOperator:
		// REQ002208: MatView. Native source stage.
		return decomposeNativeSource(o, st)
	default:
		return decomposeFallback(op, st, planner, specialize)
	}
}

// scanOp is the common interface for both OP.SeqScan and OP.IndexScan.
// REQ002143: IndexScan now also implements UsedCols(), making it eligible
// for the same ScanStageSpec-based decomposition as SeqScan.
type scanOp interface {
	DT.Operator
	Schema() *DT.StoreSchema
	UsedCols() []string
}

// decomposeSeqScan creates a ScanStageSpec for a SeqScan or IndexScan.
// Output schema comes from the scan's StoreSchema (Cols + ColTypes) if known,
// populated by the planner via Schema() method on OP.SeqScan/OP.IndexScan.
// Accepts both via the scanOp interface (REQ002143).
func decomposeSeqScan(ss scanOp, st *decomposeState) int {
	var op DT.Operator = ss
	var out outputSchema
	sch := ss.Schema()
	if used := ss.UsedCols(); len(used) > 0 && sch != nil {
		names := make([]string, len(used))
		types := make([]LX.TokenType, len(used))
		for i, col := range used {
			names[i] = col
			if idx := sch.ColIndex[col]; idx >= 0 && idx < len(sch.ColTypes) {
				types[i] = sch.ColTypes[idx]
			}
		}
		out = outputSchema{names: names, types: types}
	} else if sch != nil && len(sch.Cols) > 0 {
		n := len(sch.Cols)
		names := make([]string, n)
		types := make([]LX.TokenType, n)
		copy(names, sch.Cols)
		if len(sch.ColTypes) > 0 {
			copy(types, sch.ColTypes)
		}
		out = outputSchema{names: names, types: types}
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
	// REQ002186: predicates containing subquery expressions use a native
	// SubqueryFilterStage that evaluates the predicate row-by-row with
	// ExecCtx for subquery execution. ToRows() now propagates ExecCtx.
	// Falls back to LegacyBatchStageSpec for now because the NOT EXISTS
	// test case (correlated subquery) does not receive ExecCtx correctly.
	if pred := f.Predicate(); pred != nil && exprContainsSubquery(pred) {
		filterIdx := st.addStage(&LegacyBatchStageSpec{
			Root:       f,
			Planner:    planner,
			Specialize: specialize,
		}, childOut)
		st.addEdge(filterIdx, childIdx, SingleChild)
		return filterIdx
	}
	filterIdx := st.addStage(&FilterStageSpec{Pred: f.Predicate()}, childOut)
	st.addEdge(filterIdx, childIdx, SingleChild)
	return filterIdx
}

// subqueryDetector embeds PS.BaseVisitor and overrides the subquery
// visit methods to short-circuit the walk when an EXISTS or scalar
// subquery expression is found. REQ002152.
type subqueryDetector struct {
	PS.BaseVisitor
	found bool
}

func (d *subqueryDetector) VisitExistsExpr(*PS.ExistsExpr) bool {
	d.found = true
	return false // stop walking
}

func (d *subqueryDetector) VisitSubqueryExpr(*PS.SubqueryExpr) bool {
	d.found = true
	return false // stop walking
}

// exprContainsSubquery reports whether e contains an EXISTS or scalar
// subquery expression anywhere in its subtree. REQ002152.
func exprContainsSubquery(e PS.Expr) bool {
	if e == nil {
		return false
	}
	d := &subqueryDetector{}
	PS.AcceptExpr(e, d)
	return d.found
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
	projectIdx := st.addStage(&ProjectStageSpec{Exprs: exprs, Names: names}, projectOut)
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
	sortCols := make([]int, 0, len(keys))
	desc := make([]bool, 0, len(keys))
	collations := make([]string, 0, len(keys))
	nullsOrder := make([]int8, 0, len(keys))
	allResolved := false
	sortKeyNames := make([]string, 0, len(keys))
	if childOut.resolved() {
		allResolved = true
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
			collations = append(collations, k.Collation)
			nullsOrder = append(nullsOrder, k.NullsOrder)
			sortKeyNames = append(sortKeyNames, exprName(k.Expr))
		}
	} else {
		for _, k := range keys {
			sortKeyNames = append(sortKeyNames, exprName(k.Expr))
		}
	}
	if allResolved && len(sortCols) > 0 {
		sortIdx := st.addStage(&SortStageSpec{
			SortCols:   sortCols,
			Desc:       desc,
			Collations: collations,
			NullsOrder: nullsOrder,
		}, childOut)
		st.addEdge(sortIdx, childIdx, SingleChild)
		return sortIdx
	}
	// REQ002181: keys not resolvable statically — use runtime resolution.
	sortIdx := st.addStage(&SortStageSpec{
		SortCols:             sortCols,
		Desc:                 desc,
		Collations:           collations,
		NullsOrder:           nullsOrder,
		ResolveKeysAtRuntime: true,
		SortKeyNames:         sortKeyNames,
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
// REQ002156: if either child is itself a join (bushy join shape), fall
// back to LegacyBatchStageSpec for the entire subtree — native stage
// decomposition doesn't yet handle transitive/bridge predicates across
// nested joins correctly.
func decomposeHashJoin(h *OP.HashJoin, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	leftIdx := decomposeOp(h.LeftChild(), st, planner, specialize)
	rightIdx := decomposeOp(h.RightChild(), st, planner, specialize)
	leftOut := st.childOutput(leftIdx)
	rightOut := st.childOutput(rightIdx)

	// REQ002156: bushy join shape — fall back to LegacyBatchStageSpec
	// for the entire subtree so the legacy execution path handles
	// transitive predicates correctly.
	if isJoinOp(h.LeftChild()) || isJoinOp(h.RightChild()) {
		leftSchema := leftOut
		rightSchema := rightOut
		var joinOut outputSchema
		if leftSchema.resolved() && rightSchema.resolved() {
			n1, n2 := len(leftSchema.names), len(rightSchema.names)
			names := make([]string, 0, n1+n2)
			types := make([]LX.TokenType, 0, n1+n2)
			names = append(names, leftSchema.names...)
			types = append(types, leftSchema.types...)
			names = append(names, rightSchema.names...)
			types = append(types, rightSchema.types...)
			joinOut = outputSchema{names: names, types: types}
		}
		joinIdx := st.addStage(&LegacyBatchStageSpec{
			Root:       h,
			Planner:    planner,
			Specialize: specialize,
		}, joinOut)
		st.addEdge(joinIdx, leftIdx, LeftChild)
		st.addEdge(joinIdx, rightIdx, RightChild)
		return joinIdx
	}

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
	if allResolved {
		joinIdx := st.addStage(&HashJoinStageSpec{
			BuildKeys: buildKeys,
			ProbeKeys: probeKeys,
			Kind:      jk,
		}, joinOut)
		st.addEdge(joinIdx, leftIdx, LeftChild)
		st.addEdge(joinIdx, rightIdx, RightChild)
		return joinIdx
	}
	// REQ002180: keys not resolvable statically — use runtime resolution.
	keyName := ""
	if len(leftKeys) > 0 {
		keyName = leftKeys[0]
	}
	joinIdx := st.addStage(&HashJoinStageSpec{
		BuildKeys:            buildKeys,
		ProbeKeys:            probeKeys,
		Kind:                 jk,
		ResolveKeysAtRuntime: true,
		LeftKeyName:          keyName,
		RightKeyName:         keyName,
	}, joinOut)
	st.addEdge(joinIdx, leftIdx, LeftChild)
	st.addEdge(joinIdx, rightIdx, RightChild)
	return joinIdx
}

// decomposeHashCrossJoin creates a native HashJoinStageSpec for HashCrossJoin.
// HashCrossJoin has single-column equi-keys (LeftKeyName/RightKeyName) that
// we resolve against left/right child schemas. REQ002151.
func decomposeHashCrossJoin(h *OP.HashCrossJoin, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	leftIdx := decomposeOp(h.LeftChild(), st, planner, specialize)
	rightIdx := decomposeOp(h.RightChild(), st, planner, specialize)
	leftOut := st.childOutput(leftIdx)
	rightOut := st.childOutput(rightIdx)

	leftKey := h.LeftKeyName()
	rightKey := h.RightKeyName()

	var probeKeys, buildKeys []int
	allResolved := leftKey != "" && rightKey != ""
	if allResolved {
		li := leftOut.findCol(leftKey)
		ri := rightOut.findCol(rightKey)
		if li == -1 || ri == -1 {
			allResolved = false
		} else {
			probeKeys = []int{li}
			buildKeys = []int{ri}
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
		joinIdx := st.addStage(&HashJoinStageSpec{
			BuildKeys: buildKeys,
			ProbeKeys: probeKeys,
			Kind:      JoinKindInner,
		}, joinOut)
		st.addEdge(joinIdx, leftIdx, LeftChild)
		st.addEdge(joinIdx, rightIdx, RightChild)
		return joinIdx
	}
	// REQ002180: key names not resolvable statically — use runtime resolution.
	joinIdx := st.addStage(&HashJoinStageSpec{
		BuildKeys:            buildKeys,
		ProbeKeys:            probeKeys,
		Kind:                 JoinKindInner,
		ResolveKeysAtRuntime: true,
		LeftKeyName:          leftKey,
		RightKeyName:         rightKey,
	}, joinOut)
	st.addEdge(joinIdx, leftIdx, LeftChild)
	st.addEdge(joinIdx, rightIdx, RightChild)
	return joinIdx
}

// decomposeNestedLoopJoin creates native decomposition for NestedLoopJoin.
// For equi-joins without outer modifiers and simple SEMI patterns, uses native stages.
// For complex cases, falls back to LegacyBatchStageSpec which uses existing NLJ implementation.
// REQ002151, REQ002152.
func decomposeNestedLoopJoin(n *OP.NestedLoopJoin, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	leftIdx := decomposeOp(n.LeftChild(), st, planner, specialize)
	rightIdx := decomposeOp(n.RightChild(), st, planner, specialize)
	leftOut := st.childOutput(leftIdx)
	rightOut := st.childOutput(rightIdx)

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

	kind := n.Kind()

	// REQ002152: SEMI joins (correlated EXISTS / IN subqueries
	// decorrelated by the planner) use a native SemiJoinStage. The match
	// predicate is a runtime closure (NestedLoopJoin.OnFunc) built by
	// decorrelateExists — it resolves columns by name per call, so we
	// carry it via SemiJoinStageSpec.OnFunc and nested-loop probe the
	// materialized build side. SEMI emits only probe (left) columns.
	if kind == OP.JoinKindSemi {
		semiIdx := st.addStage(&SemiJoinStageSpec{OnFunc: n.OnFunc()}, joinOut)
		st.addEdge(semiIdx, leftIdx, LeftChild)
		st.addEdge(semiIdx, rightIdx, RightChild)
		return semiIdx
	}

	// REQ002182: for other join types (INNER, LEFT, RIGHT, FULL, CROSS),
	// use NLJStage which wraps the row-based NLJ via SpecializeFunc.
	// Currently falls back to LegacyBatchStageSpec because the NLJStage
	// does not yet handle the RegisterTable test setup correctly.
	joinIdx := st.addStage(&LegacyBatchStageSpec{
		Root:       n,
		Planner:    planner,
		Specialize: specialize,
	}, joinOut)
	st.addEdge(joinIdx, leftIdx, LeftChild)
	st.addEdge(joinIdx, rightIdx, RightChild)
	return joinIdx
}

// aggPlan is the common interface for both AG.Aggregate and AG.HashAggregate.
// REQ002143: HashAggregate is a legacy subset of Aggregate; both share the
// same GroupCols/Aggs signatures and can be handled by the same native
// AggregateStageSpec.
type aggPlan interface {
	DT.Operator
	Child() DT.Operator
	GroupCols() []PS.Expr
	Aggs() []PS.Expr
}

// decomposeAggregate native AggregateStageSpec. Resolves group column
// groupCols) and extracts aggregate func specs (col indices from agg
// expressions. REQ002124.
//
// Accepts both AG.Aggregate and AG.HashAggregate via the aggPlan interface.
// REQ002143: HashAggregate is now routed through this same function.
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
func decomposeAggregate(agg aggPlan, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(agg.Child(), st, planner, specialize)
	childOut := st.childOutput(childIdx)

	groupExprs := agg.GroupCols()
	aggExprs := agg.Aggs()

	groupCols := make([]int, 0, len(groupExprs))
	specs := make([]AccumulatorSpec, 0, len(aggExprs))
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

	// REQ002192: check if any group key is a string type — HashShuffler
	// only supports int64 keys. Fall back to LegacyBatchStageSpec for
	// string group keys.
	if allResolved && childOut.resolved() {
		for _, idx := range groupCols {
			if idx >= 0 && idx < len(childOut.types) {
				switch childOut.types[idx] {
				case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
					allResolved = false
					break
				}
			}
			if !allResolved {
				break
			}
		}
	}

	// Resolve aggregate functions
	if allResolved && childOut.resolved() {
		for _, ae := range aggExprs {
			spec, ok := resolveAggFunc(ae, childOut)
			if !ok {
				allResolved = false
				break
			}
			// REQ002188: DISTINCT, GROUP_CONCAT, and STRING_AGG are natively
			// supported by UnifiedAccum. Falls back to LegacyBatchStageSpec
			// because the AggregateStage HashShuffler only supports int64
			// group keys (string group keys cause 0-row results).
			if spec.Distinct {
				allResolved = false
				break
			}
			switch spec.Kind {
			case AggGroupConcat, AggStringAgg:
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

// decomposeDistinct wraps a Distinct operator in LegacyBatchStageSpec for
// now (DistinctStageSpec exists but requires column-index resolution against
// the child schema — REQ002143 for the native emit). It always propagates
// the child's output schema unchanged so OutputCols metadata survives the
// round-trip through the pipeline.
// decomposeDistinct creates a native DistinctStageSpec when the child's
// output schema is known. REQ002143: uses all columns as distinct keys.
func decomposeDistinct(d *OP.Distinct, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	childIdx := decomposeOp(d.Child(), st, planner, specialize)
	schema := st.childOutput(childIdx)
	if !schema.resolved() {
		schema.names, schema.types = extractOutputSchema(d.Child())
	}
	if schema.resolved() {
		// Use all columns as distinct keys.
		keyCols := make([]int, len(schema.names))
		for i := range schema.names {
			keyCols[i] = i
		}
		idx := st.addStage(&DistinctStageSpec{KeyCols: keyCols}, schema)
		st.addEdge(idx, childIdx, SingleChild)
		return idx
	}
	// REQ002184: child schema unknown — use all columns as distinct keys.
	// The DistinctStage will determine column count from the first batch.
	idx := st.addStage(&DistinctStageSpec{KeyCols: nil}, schema)
	st.addEdge(idx, childIdx, SingleChild)
	return idx
}

// decomposeWindow creates a native WindowStageSpec. Output schema
// comes from the child stage's output plus the window function name
// (the function's result is appended as a new column). REQ002128.
func decomposeWindow(w *AG.WindowOperator, st *decomposeState) int {
	childIdx := decomposeOp(w.Input(), st, nil, nil)
	childOut := st.childOutput(childIdx)
	out := childOut
	out.names = append([]string(nil), childOut.names...)
	out.types = append([]LX.TokenType(nil), childOut.types...)
	out.names = append(out.names, w.FuncName())
	out.types = append(out.types, LX.T_INT_KW)
	idx := st.addStage(&WindowStageSpec{
		Spec:     w.Spec(),
		FuncName: w.FuncName(),
		Args:     w.Args(),
		Cols:     w.Cols(),
	}, out)
	st.addEdge(idx, childIdx, SingleChild)
	return idx
}

// decomposeCompound creates a native CompoundStageSpec for set operations
// (UNION, UNION ALL, INTERSECT, EXCEPT). Output schema is taken from the
// left child. REQ002128.
// compoundOp is the interface for operators that can be decomposed
// into a CompoundStageSpec. REQ002178: accept DT.Operator interface.
type compoundOp interface {
	LeftChild() DT.Operator
	RightChild() DT.Operator
	CompoundOpType() PS.CompoundOp
}

func decomposeCompound(c DT.Operator, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	comp, ok := c.(compoundOp)
	if !ok {
		return decomposeFallback(c, st, planner, specialize)
	}
	leftIdx := decomposeOp(comp.LeftChild(), st, planner, specialize)
	rightIdx := decomposeOp(comp.RightChild(), st, planner, specialize)
	leftOut := st.childOutput(leftIdx)
	idx := st.addStage(&CompoundStageSpec{Op: comp.CompoundOpType()}, leftOut)
	st.addEdge(idx, leftIdx, LeftChild)
	st.addEdge(idx, rightIdx, RightChild)
	return idx
}

// resolveAggFunc parses an aggregate function expression (e.g. SUM(col),
// COUNT(DISTINCT col), GROUP_CONCAT(x, ',')) and returns an
// AccumulatorSpec with child column index + flags. Child's output
// schema is used to resolve argument column names. Second return is ok=false
// if child column couldn't be resolved or function is unsupported.
func resolveAggFunc(expr PS.Expr, childOut outputSchema) (AccumulatorSpec, bool) {
	fn, ok := expr.(*PS.AggregateFunc)
	if !ok {
		return AccumulatorSpec{}, false
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
		return AccumulatorSpec{}, false
	}
	// col = -1 is valid only for COUNT (counts rows regardless of arg)
	if col == -1 && kind != AggCount {
		return AccumulatorSpec{}, false
	}
	var separator string
	if fn.Separator != nil {
		if s, ok := fn.Separator.(*PS.StringLiteral); ok {
			separator = s.Val
		}
	}
	return AccumulatorSpec{
		Kind:      kind,
		Col:       col,
		Separator: separator,
		Distinct:  fn.Distinct,
	}, true
}

// aggKindByName maps a SQL aggregate function name (case-insensitive)
// to an AccumKind. The second return is false if unrecognized.
func aggKindByName(name string) (AccumKind, bool) {
	switch {
	case equalFold(name, "count"):
		return AggCount, true
	case equalFold(name, "sum"):
		return AggSum, true
	case equalFold(name, "min"):
		return AggMin, true
	case equalFold(name, "max"):
		return AggMax, true
	case equalFold(name, "avg"):
		return AggAvg, true
	case equalFold(name, "group_concat"):
		return AggGroupConcat, true
	case equalFold(name, "string_agg"):
		return AggStringAgg, true
	}
	return 0, false
}

// aggFuncDisplayName returns the column display name for an aggregate
// function kind used in aggregate output schema.
func aggFuncDisplayName(k AccumKind) string {
	switch k {
	case AggCount:
		return "count(*)"
	case AggSum:
		return "sum(expr)"
	case AggMin:
		return "min(expr)"
	case AggMax:
		return "max(expr)"
	case AggAvg:
		return "avg(expr)"
	case AggGroupConcat:
		return "group_concat(expr)"
	case AggStringAgg:
		return "string_agg(expr)"
	}
	return "agg"
}

// aggFuncReturnType returns a rough result column type.
func aggFuncReturnType(k AccumKind) LX.TokenType {
	switch k {
	case AggCount:
		return LX.T_INT_KW
	case AggMin, AggMax:
		return LX.T_NULL // actual type determined at runtime
	case AggAvg, AggSum:
		return LX.T_FLOAT_KW // conservative guess
	case AggGroupConcat, AggStringAgg:
		return LX.T_TEXT
	}
	return LX.T_TEXT
}

// isJoinOp reports whether op is a join operator (HashJoin, HashCrossJoin,
// or NestedLoopJoin). REQ002156: used to detect bushy join shapes that
// should fall back to LegacyBatchStageSpec for correct handling of
// transitive/bridge predicates across nested joins.
func isJoinOp(op DT.Operator) bool {
	switch op.(type) {
	case *OP.HashJoin, *OP.HashCrossJoin, *OP.NestedLoopJoin:
		return true
	}
	return false
}

// decomposeDML creates a DML StageSpec (Insert/Update/Delete). Output
// schema is empty (DML outputs no result cols unless RETURNING — caller
// handles that at the executor layer.
func decomposeDML(spec StageSpec, st *decomposeState) int {
	return st.addStage(spec, outputSchema{})
}

// decomposeNativeSource wraps a simple source operator (ConstRow,
// FusedScan, Values) in a native ScanStageSpec. Unlike decomposeFallback,
// this avoids the LegacyBatchStageSpec overhead (Specialize call, slog.Warn)
// and produces a proper CatSource stage that the pipeline can optimize.
func decomposeNativeSource(op DT.Operator, st *decomposeState) int {
	return st.addStage(&ScanStageSpec{
		NewProducer: func() UT.BatchProducer {
			return NewRowOperatorAsProducer(op)
		},
	}, outputSchema{})
}

// decomposeFallback wraps the operator in a LegacyBatchStageSpec.
// Output schema empty (will fallbacks treat it unknown).
func decomposeFallback(op DT.Operator, st *decomposeState, planner PL.QueryPlanner, specialize SpecializeFunc) int {
	// REQ002185: log unknown operator types to surface missing native stages.
	slog.Warn("px.decomposeFallback: unknown operator type, using LegacyBatchStageSpec",
		"type", fmt.Sprintf("%T", op))
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

	// REQ002150: handle FilterProject as fused Filter + Project.
	// The planner emits FilterProject instead of separate Filter + Project
	// for simple WHERE+SELECT queries. There may be multiple FilterProject
	// layers (e.g., FilterProject(FilterProject(Filter(Scan)))). Walk through
	// all FilterProject layers and collect the last non-nil pred and exprs.
	var fpPred PS.Expr
	var fpExprs []PS.Expr
	for {
		fp, ok := cur.(*OP.FilterProject)
		if !ok {
			break
		}
		if p := fp.Predicate(); p != nil {
			fpPred = p
		}
		if c := fp.Cols(); len(c) > 0 {
			fpExprs = c
		}
		cur = fp.Child()
		if cur == nil {
			return 0, false
		}
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
		// REQ002143: also try IndexScan for FusedScan.
		if idx, ok2 := cur.(*OP.IndexScan); ok2 {
			return tryDecomposeFusedIndexScan(limitOp, projectOp, filterOp, idx, st)
		}
		// REQ002150: the planner emits FusedScan for small in-memory
		// tables (fused.go). FusedScan already applies filter + project
		// internally, so we skip the FusedScanStageSpec and let it
		// fall through to decomposeFallback which creates a
		// LegacyBatchStageSpec. The pipeline path will fall back to
		// legacy execution, which uses the FusedScan directly.
		if _, ok2 := cur.(*OP.FusedScan); ok2 {
			return 0, false
		}
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
	if projectOp == nil && filterOp == nil && fpPred == nil && fpExprs == nil && limitOp == nil {
		return 0, false
	}
	// REQ002152: do not fuse when the filter predicate contains a
	// subquery expression (EXISTS / NOT EXISTS / scalar subquery). The
	// FusedScanStage evaluates predicates via the batch-native EV
	// evaluator, which cannot execute correlated subqueries (REQ002153).
	// Returning false here lets the query fall through to decomposeFilter,
	// which guards the same condition and falls back to LegacyBatchStageSpec.
	candidatePred := fpPred
	if filterOp != nil {
		candidatePred = filterOp.Predicate()
	}
	if candidatePred != nil && exprContainsSubquery(candidatePred) {
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
	} else if fpPred != nil {
		pred = fpPred
	}
	var exprs []PS.Expr
	var fusedNames []string
	if projectOp != nil {
		exprs = projectOp.Cols()
		fusedNames = make([]string, len(exprs))
		for i, e := range exprs {
			fusedNames[i] = exprName(e)
		}
	} else if fpExprs != nil {
		exprs = fpExprs
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
		if projectOp != nil || fpExprs != nil {
			for i := range outNames {
				fusedTypes[i] = LX.T_TEXT
			}
		} else if scanOut.resolved() {
			copy(fusedTypes, scanOut.types)
		}
		fusedOut = outputSchema{names: outNames, types: fusedTypes}
	}

	var scanOp DT.Operator = seqScan
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

// tryDecomposeFusedIndexScan mirrors tryDecomposeFusedScan for IndexScan.
// REQ002143: IndexScan supports the same FusedScan pattern (filter/project/limit).
func tryDecomposeFusedIndexScan(limitOp *OP.Limit, projectOp *OP.Project, filterOp *OP.Filter, idxScan *OP.IndexScan, st *decomposeState) (int, bool) {
	sch := idxScan.Schema()
	if sch == nil || len(sch.Cols) == 0 {
		return 0, false
	}
	if projectOp == nil && filterOp == nil && limitOp == nil {
		return 0, false
	}
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
	scanOp := idxScan
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
	// REQ002173: HashAggregate removed — handled by Aggregate case.
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
	return &LegacyBatchStage{producer: producer, root: s.Root}
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
	root     DT.Operator // underlying operator tree for param propagation
	execCtx  *DT.ExecContext
	params   []any
	closed   bool
}

// NextBatch delegates to the inner BatchProducer. Sets batch.ExecCtx
// when the exec context has been propagated (REQ002148).
func (s *LegacyBatchStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closed {
		return nil, errors.New("px: nextbatch on closed legacy stage")
	}
	batch, err := s.producer.NextBatch(ctx)
	if err != nil {
		return nil, err
	}
	if batch != nil && s.execCtx != nil {
		batch.ExecCtx = s.execCtx
	}
	return batch, nil
}

// PropagateExecContext stores the per-execution context so it can be
// embedded into batches produced by the LegacyBatchStage. REQ002148.
func (s *LegacyBatchStage) PropagateExecContext(ec *DT.ExecContext) {
	s.execCtx = ec
}

// PropagateParams stores parameter values and propagates them to the
// underlying operator tree (root). REQ002143.
// REQ002161: copy into own buffer — the incoming buf is shared
// across all stages and subsequent stages would overwrite it.
func (s *LegacyBatchStage) PropagateParams(args []any, buf *[]any) {
	s.params = append(s.params[:0], args...)
	if s.root != nil {
		if w, ok := s.root.(interface {
			WithParams([]any) DT.Operator
		}); ok {
			w.WithParams(s.params)
		}
	}
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

// RowOperatorAsProducer adapts a DT.Operator to produce
// batches of 1 row each. Used by LegacyBatchStageSpec to convert
// row-based operators (DML, legacy ops) into the BatchProducer
// interface expected by the PipelineExecutor.
// REQ002177: uses DT.Operator (alias for PL.Operator).
type RowOperatorAsProducer struct {
	Op DT.Operator
}

// NewRowOperatorAsProducer creates a RowOperatorAsProducer.
func NewRowOperatorAsProducer(op DT.Operator) *RowOperatorAsProducer {
	return &RowOperatorAsProducer{Op: op}
}

func (r *RowOperatorAsProducer) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

// propagatePlannerToTree walks the plan tree and calls WithPlanner
// on every operator that supports it. This is required so that SeqScan
// (and other store-backed operators) receive the planner reference they
// need to access the store/transaction during execution.
// Mirrors the same pattern in EX's propagatePlanner.
func propagatePlannerToTree(root DT.Operator, planner PL.QueryPlanner) {
	if root == nil {
		return
	}
	if w, ok := root.(interface {
		WithPlanner(PL.QueryPlanner) DT.Operator
	}); ok {
		w.WithPlanner(planner)
	}
	type childer interface {
		Child() DT.Operator
	}
	if c, ok := root.(childer); ok {
		propagatePlannerToTree(c.Child(), planner)
	}
	type leftRighter interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}
	if lr, ok := root.(leftRighter); ok {
		propagatePlannerToTree(lr.LeftChild(), planner)
		propagatePlannerToTree(lr.RightChild(), planner)
	}
	// REQ002149: handle multi-child operators (BitmapHeapScan, etc.)
	// that store children in a slice rather than a single Child().
	type multiChilder interface {
		Children() []DT.Operator
	}
	if mc, ok := root.(multiChilder); ok {
		for _, child := range mc.Children() {
			propagatePlannerToTree(child, planner)
		}
	}
}
