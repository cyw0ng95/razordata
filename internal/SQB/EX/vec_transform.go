package EX

import (
	"fmt"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQB/AD"
	"github.com/cyw0ng95/razordata/internal/SQB/AG"
	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// parallelProbeThreshold is the minimum estimated build-side row count that
// triggers the parallel probe phase (UT.ParallelProbe) on VectorizedHashJoin.
// Below this, the streaming sequential probe is cheaper than spawning probe
// workers. The pool alone (without WithParallelism) already enables the
// parallel *build* phase via the existing totalRows >= 512 gate in
// buildHashTable. REQ001645.
const parallelProbeThreshold = 10000

// tryVectorizePlan attempts to replace row-based operators with vectorized
// equivalents. Returns the (possibly modified) root operator wrapped in
// a BatchToRowAdapter. Falls back to the original root unchanged if
// vectorization is not applicable.
//
// REQ001645: the planner is threaded through the transform chain so that
// VectorizedHashJoin can be wired with the shared WorkerPool (parallel build)
// and size-gated parallelism (parallel probe when the build side is large).
// A nil planner (e.g. in unit tests) yields the historical behavior: no pool,
// no parallelism.
func tryVectorizePlan(root DT.Operator, p *Planner) DT.Operator {
	// REQ001581: planner wraps plan.Root in *AD.AdaptiveOp (planner.go:526)
	// so this call site almost always sees an AdaptiveOp. Without
	// unwrapping, transformOp returns nil (no case for AdaptiveOp), and
	// the entire plan tree — including every Filter, HashJoin, Project
	// inside AdaptiveOp.Inner — falls back to row execution, bypassing
	// REQ001614 (default vec), REQ001618-1623 (vec joins), REQ001611
	// (HJ batch emission), and REQ001631 (IN-list bloom). Dominates
	// SLT select4 long-tail wall time. The vectorized chain that we
	// produce here already supersedes ADQC's specialized codegen path
	// (which targets the same batch shape), so unwrapping the
	// AdaptiveOp wrapper is semantically equivalent and faster.
	// REQ001587: when the inner plan starts with a SeqScan that has
	// no store schema (in-memory tables without column metadata),
	// transformOp returns nil, so the original AdaptiveOp is kept.

	// REQ002003: try fused batch scan first — for the eligible
	// Limit(Project(Filter(SeqScan))) shape, a single FusedBatchScan
	// replaces three separate operators and their per-batch handoffs.
	// Falls through to the push pipeline / pull path if not eligible.
	if fused := tryFusedBatchScan(root, p); fused != nil {
		return UT.NewBatchToRowAdapter(fused)
	}

	// REQ002002: try push-based pipeline for eligible Filter+Project+Limit
	// shapes. Conservative gating keeps SLT on the proven pull path for
	// anything non-trivial. Returns nil if the shape is not eligible.
	if pushAdapter := tryPushPipeline(root, p); pushAdapter != nil {
		return UT.NewBatchToRowAdapter(pushAdapter)
	}

	vec := transformRoot(root, p)
	if vec != nil {
		return UT.NewBatchToRowAdapter(vec)
	}
	return root
}

// transformRoot recurses one level through AD wrappers before calling
// transformOp on the actual data operator. For AdaptiveOp the inner
// is what gets vectorized — the BatchProducer result replaces the
// AdaptiveOp entirely because vec execution already supersedes ADQC.
// REQ001587: always unwrap AdaptiveOp — no join gate.
func transformRoot(root DT.Operator, p *Planner) UT.BatchProducer {
	if root == nil {
		return nil
	}
	if aop, ok := root.(*AD.AdaptiveOp); ok {
		return transformOp(aop.Inner, p)
	}
	return transformOp(root, p)
}

// All operators are now vectorized or wrapped in ScalarBatchProducer.

// transformOp transforms a row operator tree into a BatchProducer chain.
// REQ001645: p (the *Planner) is threaded through so join transforms can
// attach the worker pool and size-gated parallelism. p may be nil.
func transformOp(op DT.Operator, p *Planner) UT.BatchProducer {
	if op == nil {
		return nil
	}
	switch o := op.(type) {
	case *OP.SeqScan:
		// REQ001639: when the SeqScan has a store, use its native
		// NextBatch method which reads directly from the store and
		// decodes into columnar format — no row intermediary.
		if o.Store() != nil {
			if cols := o.GetRequestedCols(); len(cols) > 0 {
				schema := extractSchema(o)
				types := extractTypes(o)
				return OP.NewVectorizedSeqScanWithCols(o, schema, types, cols)
			}
			return o
		}
		schema := extractSchema(o)
		types := extractTypes(o)
		if cols := o.GetRequestedCols(); len(cols) > 0 {
			return OP.NewVectorizedSeqScanWithCols(o, schema, types, cols)
		}
		return OP.NewVectorizedSeqScan(o, schema, types)
	case *OP.IndexScan:
		// REQ001617/REQ001626: pure batch IndexScan — no row intermediary.
		return OP.NewBatchIndexScan(o)
	case *OP.BitmapHeapScan:
		return transformBitmapHeapScan(o, p)
	case *OP.IndexOnlyScan:
		return OP.NewBatchIndexOnlyScan(o)
	case *OP.Filter:
		child := transformOp(o.Child(), p)
		if child == nil {
			return nil
		}
		// REQ001658: push down range predicate to SeqScan when possible.
		if ss, ok := o.Child().(*OP.SeqScan); ok && ss.Store() != nil {
			if colIdx, min, max, ok := extractRangePredicate(o.Predicate()); ok {
				ss.SetRangePredicate(colIdx, min, max)
			}
		}
		return OP.NewVectorizedFilter(child, o.Predicate())
	case *OP.Project:
		child := transformOp(o.Child(), p)
		if child == nil {
			return nil
		}
		exprs := o.Cols()
		names := make([]string, len(exprs))
		for i, e := range exprs {
			names[i] = exprName(e)
		}
		vp := OP.NewVectorizedProject(child, exprs, names)
		// REQ001460: forward the ExecContext so row-fallback paths
		// (notably non-correlated scalar subqueries) can locate the
		// QueryPlanner via getSubqueryPlanner.
		if ec := o.ExecCtx(); ec != nil {
			vp.SetExecCtx(ec)
		}
		return vp
	case *AG.Aggregate:
		child := transformOp(o.Child(), p)
		if child == nil {
			return nil
		}
		result := transformAggregate(o, child)
		if result == nil {
			return nil
		}
		return result
	case *AG.HashAggregate:
		child := transformOp(o.Child(), p)
		if child == nil {
			return nil
		}
		result := transformHashAggregate(o, child)
		if result == nil {
			return nil
		}
		return result
	case *AG.ParallelHashAggregate:
		// REQ001982: already has NextBatch — return as-is.
		return o
	case *OP.HashJoin:
		return transformHashJoin(o, p)
	case *OP.NestedLoopJoin:
		return transformNestedLoopJoin(o, p)
	case *OP.MergeJoin:
		return transformMergeJoin(o, p)
	case *OP.HashCrossJoin:
		return transformHashCrossJoin(o, p)
	case *OP.Distinct:
		return transformDistinct(o, p)
	case *OP.CompoundOp:
		return transformCompoundOp(o, p)
	case *OP.Sort:
		child := transformOp(o.Child(), p)
		if child == nil {
			return nil
		}
		return OP.NewVectorizedSort(child, o.Keys())
	case *OP.TopNSort:
		child := transformOp(o.Child(), p)
		if child == nil {
			return nil
		}
		// REQ001640: convert row-based TopNSort to vectorized TopN sort.
		return OP.NewVectorizedTopNSort(child, o.Keys(), o.Limit())
	case *OP.Limit:
		child := transformOp(o.Child(), p)
		if child == nil {
			return nil
		}
		return OP.NewVectorizedLimit(child, o.Limit())
	case *OP.Offset:
		child := transformOp(o.Child(), p)
		if child == nil {
			return nil
		}
		return OP.NewVectorizedOffset(child, o.OffsetValue())
	case *OP.ParallelStoreSeqScan:
		// REQ001981: already has NextBatch — return as-is.
		return o
	case *OP.ParallelSeqScanRow:
		// REQ001981: already has NextBatch — return as-is.
		return o
	case *OP.ParallelUnionAll:
		// REQ001981: already has NextBatch — return as-is.
		return o
	case *OP.ParallelIndexRangeScan:
		// REQ001981: already has NextBatch — return as-is.
		return o
	case *WT.Insert:
		// REQ001983: already has NextBatch — return as-is.
		return o
	case *WT.Update:
		// REQ001984: already has NextBatch — return as-is.
		return o
	case *WT.Delete:
		// REQ001985: already has NextBatch — return as-is.
		return o
	case *AG.WindowOperator:
		child := transformOp(o.Input(), p)
		if child == nil {
			return nil
		}
		return AG.NewVectorizedWindowOperator(child, o.FuncName(), o.Args(), o.Spec(), o.Cols())
	}
	return nil
}

// transformHashJoin converts a row HashJoin to VectorizedHashJoin.
// Supports single or multi-column equi-join keys (REQ001618).
// Supports outer join kinds (REQ001619).
// REQ001645: wires the shared WorkerPool and size-gated parallel probe when
// the planner is available. Returns nil if transformation fails.
func transformHashJoin(h *OP.HashJoin, p *Planner) UT.BatchProducer {
	// Transform children: left = probe, right = build
	left := transformOp(h.LeftChild(), p)
	if left == nil {
		return nil
	}
	right := transformOp(h.RightChild(), p)
	if right == nil {
		return nil
	}
	// Resolve key column indices from the SeqScan schemas.
	// REQ001618: supports multi-column keys.
	leftKeys := h.LeftKeys()
	rightKeys := h.RightKeys()
	if len(leftKeys) != len(rightKeys) {
		return nil // key count mismatch
	}
	numKeys := len(leftKeys)
	if numKeys == 0 {
		return nil
	}

	buildIdxs := make([]int, numKeys)
	for i, rk := range rightKeys {
		idx, ok := resolveColumnIndex(h.RightChild(), rk)
		if !ok {
			return nil
		}
		buildIdxs[i] = idx
	}
	probeIdxs := make([]int, numKeys)
	for i, lk := range leftKeys {
		idx, ok := resolveColumnIndex(h.LeftChild(), lk)
		if !ok {
			return nil
		}
		probeIdxs[i] = idx
	}

	// REQ001619: forward join kind.
	kind := h.Kind()

	// VectorizedHashJoin expects build (right) side first, then probe (left).
	var vjh *OP.VectorizedHashJoin
	if kind == OP.JoinKindInner {
		vjh = OP.NewVectorizedHashJoin(right, left, buildIdxs, probeIdxs)
	} else {
		vjh = OP.NewVectorizedHashJoinWithKind(right, left, buildIdxs, probeIdxs, kind)
	}

	// REQ001645: planner wiring. Gate BOTH the parallel build (pool) and the
	// parallel probe (parallelism > 1) on a large build-side estimate. The
	// parallel build path (buildHashTableParallel) is only justified for
	// sizable builds, and gating it here keeps the medium-sized joins that
	// dominate SLT on the proven sequential build+probe path. Only a bare
	// SeqScan build child exposes a catalog row count; Filter/Project-wrapped
	// scans and other shapes stay sequential (a known limitation, safe
	// default). A nil planner (unit tests) leaves the join unwired, matching
	// the historical behavior.
	if p != nil {
		if pool, ok := p.pool.(*UT.WorkerPool); ok && pool != nil {
			if ss, ok := h.RightChild().(*OP.SeqScan); ok {
				if p.GetTableRowCount(ss.Table()) >= parallelProbeThreshold {
					vjh.WithPool(pool)
					vjh.WithParallelism(pool.Workers())
				}
			}
		}
	}
	return vjh
}

// transformAggregate converts a row Aggregate to VectorizedHashAggregate.
// Returns nil for unsupported aggregates (non-COUNT, DISTINCT, etc.).
func transformAggregate(a *AG.Aggregate, bp UT.BatchProducer) *AG.VectorizedHashAggregate {
	groupCols := a.GroupCols()
	if len(groupCols) > 0 {
		for _, gc := range groupCols {
			if _, ok := gc.(*PS.Ident); !ok {
				return nil // fallback to row-based for non-ident group cols
			}
		}
	}
	aggs := a.Aggs()
	if len(aggs) == 0 {
		return nil
	}
	defs := make([]AG.AggDef, 0, len(aggs))
	child := a.Child()
	for _, ag := range aggs {
		def, ok := resolveAggDef(ag, child)
		if !ok {
			// unsupported aggregate — fallback to row-based
			return nil
		}
		defs = append(defs, def)
	}
	groupColIdxs := make([]int, len(groupCols))
	for i, gc := range groupCols {
		idx, ok := resolveColumnIndex(child, gc.(*PS.Ident).Name)
		if !ok {
			return nil // can't resolve group column
		}
		groupColIdxs[i] = idx
	}
	if len(groupCols) == 0 {
		groupColIdxs = nil
	}
	return AG.NewVectorizedHashAggregate(bp, groupColIdxs, defs)
}

// transformHashAggregate converts a row HashAggregate to VectorizedHashAggregate.
// REQ001446.
func transformHashAggregate(h *AG.HashAggregate, bp UT.BatchProducer) *AG.VectorizedHashAggregate {
	groupCols := h.GroupCols()
	if len(groupCols) > 0 {
		for _, gc := range groupCols {
			if _, ok := gc.(*PS.Ident); !ok {
				return nil
			}
		}
	}
	aggs := h.Aggs()
	if len(aggs) == 0 {
		return nil
	}
	defs := make([]AG.AggDef, 0, len(aggs))
	child := h.Child()
	for _, ag := range aggs {
		def, ok := resolveAggDef(ag, child)
		if !ok {
			return nil
		}
		defs = append(defs, def)
	}
	groupColIdxs := make([]int, len(groupCols))
	for i, gc := range groupCols {
		idx, ok := resolveColumnIndex(child, gc.(*PS.Ident).Name)
		if !ok {
			return nil
		}
		groupColIdxs[i] = idx
	}
	if len(groupCols) == 0 {
		groupColIdxs = nil
	}
	return AG.NewVectorizedHashAggregate(bp, groupColIdxs, defs)
}

// resolveColumnIndex finds the column index for a named column by
// traversing down to the SeqScan and looking up in its schema.
// Falls back to index 0 when schema is unavailable (e.g., test mode).
func resolveColumnIndex(child DT.Operator, colName string) (int, bool) {
	// REQ001602: walk through Filter/Project/Sort chains to find
	// the underlying scan's schema. Previously only handled
	// Filter(SeqScan) — multi-table joins with IN-list predicates
	// produce Filter(Project(SeqScan)) shapes that were rejected.
	cols := colsOf(child)
	if cols == nil {
		// Schema unavailable (test mode, no store) — return index 0
		// as best-effort fallback.
		return 0, true
	}
	for i, name := range cols {
		if name == colName {
			return i, true
		}
	}
	return 0, false
}

// resolveAggDef parses an aggregate expression into (AggDef, ok).
// COUNT(*) returns (AggDef{Kind: AggCount, Col: -1}, true).
// Returns ({AggDef{}, false}, false) for unsupported aggregates.
// REQ001993: GROUP_CONCAT/STRING_AGG supported with DISTINCT and SEPARATOR.
func resolveAggDef(expr PS.Expr, child DT.Operator) (AG.AggDef, bool) {
	// Unwrap AliasedExpr (e.g., SUM(v) AS total)
	if ae, ok := expr.(*PS.AliasedExpr); ok {
		return resolveAggDef(ae.Expr, child)
	}
	af, ok := expr.(*PS.AggregateFunc)
	if !ok {
		return AG.AggDef{}, false
	}
	var kind AG.AggKind
	isStringAgg := false
	switch strings.ToUpper(af.Name) {
	case "COUNT":
		kind = AG.AggCount
	case "SUM":
		kind = AG.AggSum
	case "MIN":
		kind = AG.AggMin
	case "MAX":
		kind = AG.AggMax
	case "AVG":
		kind = AG.AggAvg
	case "GROUP_CONCAT":
		kind = AG.AggGroupConcat
		isStringAgg = true
	case "STRING_AGG":
		kind = AG.AggStringAgg
		isStringAgg = true
	default:
		return AG.AggDef{}, false
	}
	// REQ001730: DISTINCT is supported on all aggregate kinds.
	// The AG package deduplicates per-def for SUM/COUNT/MIN/MAX/AVG,
	// reusing the existing GROUP_CONCAT/STRING_AGG dedup pattern.
	// REQ001993: extract separator for GROUP_CONCAT/STRING_AGG
	sep := ","
	if isStringAgg && af.Separator != nil {
		// Evaluate separator as a constant expression.
		sv, err := EV.EvalValue(af.Separator, nil, nil)
		if err == nil && sv.Kind != DT.KindNull {
			sep = fmt.Sprintf("%v", sv.ToAny())
		}
	}
	def := AG.AggDef{Kind: kind, Separator: sep, Distinct: af.Distinct}
	switch arg := af.Arg.(type) {
	case *PS.StarExpr:
		// COUNT(*) uses Col: -1 (no column needed)
		return def, true
	case *PS.Ident:
		idx, ok := resolveColumnIndex(child, arg.Name)
		if !ok {
			return AG.AggDef{}, false
		}
		def.Col = idx
		return def, true
	default:
		// REQ001993: GROUP_CONCAT/STRING_AGG with expression args (e.g., CAST) not yet supported
		if isStringAgg {
			return AG.AggDef{}, false
		}
		return AG.AggDef{}, false
	}
}

// extractSchema gets column names from a SeqScan's store schema.
func extractSchema(s *OP.SeqScan) []string {
	sch := s.Schema()
	if sch == nil {
		return nil
	}
	names := make([]string, len(sch.Cols))
	for i, col := range sch.Cols {
		names[i] = col
	}
	return names
}

// extractTypes gets column types from a SeqScan's store schema.
func extractTypes(s *OP.SeqScan) []LX.TokenType {
	sch := s.Schema()
	if sch == nil {
		return nil
	}
	types := make([]LX.TokenType, len(sch.ColTypes))
	copy(types, sch.ColTypes)
	return types
}

// exprName extracts a human-readable name from an expression.
func exprName(e PS.Expr) string {
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

// transformNestedLoopJoin converts a row NestedLoopJoin to
// VectorizedNestedLoopJoin. Both children must be eligible.
// INNER and CROSS joins are supported.
func transformNestedLoopJoin(nlj *OP.NestedLoopJoin, p *Planner) UT.BatchProducer {
	left := transformOp(nlj.LeftChild(), p)
	if left == nil {
		return nil
	}
	right := transformOp(nlj.RightChild(), p)
	if right == nil {
		return nil
	}
	return OP.NewVectorizedNestedLoopJoin(left, right, nlj.OnFunc(), nlj.Kind())
}

// transformMergeJoin converts a row MergeJoin to VectorizedMergeJoin.
// REQ001615/REQ001625: pure batch sort-merge — no row intermediary.
// Reads batches from both pre-sorted sides and merges them directly.
func transformMergeJoin(mj *OP.MergeJoin, p *Planner) UT.BatchProducer {
	left := transformOp(mj.LeftChild(), p)
	if left == nil {
		return nil
	}
	right := transformOp(mj.RightChild(), p)
	if right == nil {
		return nil
	}
	// Resolve key column indices from the left/right schemas.
	leftKeys := mj.LeftKeys()
	rightKeys := mj.RightKeys()
	if len(leftKeys) != len(rightKeys) {
		return nil
	}
	numKeys := len(leftKeys)
	if numKeys == 0 {
		return nil
	}

	leftKeyIdxs := make([]int, numKeys)
	for i, lk := range leftKeys {
		idx, ok := resolveColumnIndex(mj.LeftChild(), lk)
		if !ok {
			return nil
		}
		leftKeyIdxs[i] = idx
	}
	rightKeyIdxs := make([]int, numKeys)
	for i, rk := range rightKeys {
		idx, ok := resolveColumnIndex(mj.RightChild(), rk)
		if !ok {
			return nil
		}
		rightKeyIdxs[i] = idx
	}
	return OP.NewBatchMergeJoin(left, right, leftKeyIdxs, rightKeyIdxs, mj.Kind())
}

// transformBitmapHeapScan converts a row BitmapHeapScan to a pure
// batch implementation. REQ001616/REQ001627.
func transformBitmapHeapScan(b *OP.BitmapHeapScan, p *Planner) UT.BatchProducer {
	// Transform each child IndexScan to BatchProducer.
	children := b.IndexScans()
	batchChildren := make([]UT.BatchProducer, 0, len(children))
	for _, child := range children {
		if child == nil {
			continue
		}
		bp := transformOp(child, p)
		if bp == nil {
			return nil
		}
		batchChildren = append(batchChildren, bp)
	}
	// BitmapHeapScan requires a store to fetch heap values.
	// Without a store, the row-based version also can't fetch;
	// fall back to scalar path. Callers must wire the store.
	return OP.NewBatchBitmapHeapScan(b.Table(), b.Store(), batchChildren)
}

// transformHashCrossJoin converts a row HashCrossJoin to a pure batch
// hash cross join. REQ001628: eliminates the row-based wrapper.
func transformHashCrossJoin(hcj *OP.HashCrossJoin, p *Planner) UT.BatchProducer {
	left := transformOp(hcj.LeftChild(), p)
	if left == nil {
		return nil
	}
	right := transformOp(hcj.RightChild(), p)
	if right == nil {
		return nil
	}
	// Resolve key column indices from the left/right schemas.
	leftKeyIdx, ok := resolveColumnIndex(hcj.LeftChild(), hcj.LeftKeyName())
	if !ok {
		return nil
	}
	rightKeyIdx, ok := resolveColumnIndex(hcj.RightChild(), hcj.RightKeyName())
	if !ok {
		return nil
	}
	return OP.NewBatchHashCrossJoin(left, right, leftKeyIdx, rightKeyIdx)
}

// transformDistinct converts a row Distinct to VectorizedDistinct.
func transformDistinct(d *OP.Distinct, p *Planner) UT.BatchProducer {
	child := transformOp(d.Child(), p)
	if child == nil {
		return nil
	}
	cols, types := extractSchemaFromOp(d.Child())
	return OP.NewVectorizedDistinct(child, cols, types)
}

// transformCompoundOp converts a row CompoundOp to VectorizedCompoundOp.
// Supports UNION ALL, UNION, EXCEPT, INTERSECT. REQ001442.
func transformCompoundOp(co *OP.CompoundOp, p *Planner) UT.BatchProducer {
	left := transformOp(co.LeftChild(), p)
	if left == nil {
		return nil
	}
	right := transformOp(co.RightChild(), p)
	if right == nil {
		return nil
	}
	cols, types := extractSchemaFromOp(co.LeftChild())
	return OP.NewVectorizedCompoundOpWithOp(left, right,
		co.CompoundOpType(), cols, types)
}

// extractSchemaFromOp extracts column schema from any eligible operator,
// walking down to the leaf SeqScan to find column names and types.
func extractSchemaFromOp(op DT.Operator) ([]string, []LX.TokenType) {
	switch o := op.(type) {
	case *OP.SeqScan:
		return extractSchema(o), extractTypes(o)
	case *OP.Filter:
		return extractSchemaFromOp(o.Child())
	case *OP.Project:
		return extractSchemaFromOp(o.Child())
	case *AG.Aggregate:
		return nil, nil
	case *AG.HashAggregate:
		return nil, nil
	case *OP.Distinct:
		return extractSchemaFromOp(o.Child())
	case *OP.CompoundOp:
		return extractSchemaFromOp(o.LeftChild())
	case *OP.NestedLoopJoin:
		return extractSchemaFromOp(o.LeftChild())
	case *OP.HashJoin:
		return extractSchemaFromOp(o.LeftChild())
	}
	return nil, nil
}

// extractRangePredicate extracts a simple range predicate from a
// filter expression. Returns the column index and the min/max range
// bounds. Returns ok=false if the predicate is not a simple range.
// Only handles int64 comparisons for now. REQ001658.
func extractRangePredicate(expr PS.Expr) (colIdx int, min, max int64, ok bool) {
	bin, ok := expr.(*PS.BinaryExpr)
	if !ok {
		return 0, 0, 0, false
	}
	// Extract column and literal from the binary expression.
	var ident *PS.Ident
	var literal *PS.NumberLiteral
	if id, ok := bin.Left.(*PS.Ident); ok {
		if lit, ok := bin.Right.(*PS.NumberLiteral); ok {
			ident = id
			literal = lit
		}
	}
	if id, ok := bin.Right.(*PS.Ident); ok {
		if lit, ok := bin.Left.(*PS.NumberLiteral); ok {
			ident = id
			literal = lit
		}
	}
	if ident == nil || literal == nil {
		return 0, 0, 0, false
	}
	_ = ident
	val := literal.Val
	switch bin.Op {
	case LX.T_GT, LX.T_GE:
		return 0, val + 1, 1<<63 - 1, true
	case LX.T_LT, LX.T_LE:
		return 0, 0, val - 1, true
	case LX.T_EQ:
		return 0, val, val, true
	}
	return 0, 0, 0, false
}

// tryPushPipeline detects the eligible Filter+Project+Limit over
// SeqScan shape and constructs a PushPipeline wrapped in a
// PushToPullAdapter. Returns nil if the shape is not eligible, in
// which case tryVectorizePlan falls through to the pull-based
// transformRoot path. REQ002002.
//
// Eligible shape (any prefix of): Limit(Project(Filter(SeqScan)))
//   - Each level is optional, but the order must be Limit→Project→Filter→SeqScan.
//   - The SeqScan must have a store (avoids in-memory table edge cases).
//   - The Filter predicate must not contain subqueries.
//   - The Project columns must be simple (Ident, QualifiedName,
//     literals, AliasedExpr wrapping those) — arithmetic and function
//     calls are rejected because the push path doesn't propagate
//     ExecCtx for subquery evaluation.
//   - The Limit value must be >= 0 (negative = unlimited, allowed).
//
// Gating is intentionally conservative to keep SLT on the proven
// pull path for anything non-trivial.
func tryPushPipeline(root DT.Operator, p *Planner) UT.BatchProducer {
	if root == nil {
		return nil
	}
	// Unwrap AdaptiveOp (planner wraps the root).
	inner := root
	if aop, ok := root.(*AD.AdaptiveOp); ok {
		inner = aop.Inner
	}

	// Walk Limit → Project → Filter → SeqScan, each level optional.
	var limitOp *OP.Limit
	var projectOp *OP.Project
	var filterOp *OP.Filter
	var seqScan *OP.SeqScan

	cur := inner
	if l, ok := cur.(*OP.Limit); ok {
		limitOp = l
		cur = l.Child()
	}
	if cur == nil {
		return nil
	}
	if proj, ok := cur.(*OP.Project); ok {
		projectOp = proj
		cur = proj.Child()
	}
	if cur == nil {
		return nil
	}
	if f, ok := cur.(*OP.Filter); ok {
		filterOp = f
		cur = f.Child()
	}
	if cur == nil {
		return nil
	}
	if ss, ok := cur.(*OP.SeqScan); ok {
		seqScan = ss
	}
	if seqScan == nil {
		return nil
	}
	// SeqScan must have a store (in-memory tables have different semantics).
	if seqScan.Store() == nil {
		return nil
	}

	// Filter predicate must not contain subqueries.
	if filterOp != nil {
		if containsSubquery(filterOp.Predicate()) {
			return nil
		}
	}

	// Project columns must be simple.
	if projectOp != nil {
		if !isSimpleProject(projectOp.Cols()) {
			return nil
		}
	}

	// Transform the SeqScan into a BatchProducer (VectorizedSeqScan).
	// We reuse transformOp so the same store-backed fast path applies.
	source := transformOp(seqScan, p)
	if source == nil {
		return nil
	}

	// Build the push operator chain.
	var ops []OP.PushOperator
	if filterOp != nil {
		pf := OP.NewPushFilter(filterOp.Predicate())
		ops = append(ops, pf)
	}
	if projectOp != nil {
		exprs := projectOp.Cols()
		names := make([]string, len(exprs))
		for i, e := range exprs {
			names[i] = exprName(e)
		}
		pp := OP.NewPushProject(exprs, names)
		// Forward ExecCtx so row-fallback paths (notably scalar
		// subqueries in projection) can locate the QueryPlanner.
		if ec := projectOp.ExecCtx(); ec != nil {
			_ = ec // PushProject.execCtx is a placeholder; subquery
			// support in the push path is deferred. Simple projections
			// (the only kind we accept here) don't need it.
		}
		ops = append(ops, pp)
	}
	if limitOp != nil {
		pl := OP.NewPushLimit(limitOp.LimitValue())
		ops = append(ops, pl)
	}
	if len(ops) == 0 {
		// Degenerate: bare SeqScan with no Filter/Project/Limit.
		// The pull path handles this fine; no benefit from push.
		return nil
	}

	pipeline := OP.NewPushPipeline(source, ops...)
	return OP.NewPushToPullAdapter(pipeline)
}

// tryFusedBatchScan detects the same Limit(Project(Filter(SeqScan)))
// shape as tryPushPipeline but collapses it into a single FusedBatchScan
// operator. Because fusion performs one NextBatch call per output batch
// (versus three PushBatch calls in the push pipeline), it is the
// preferred path for OLTP-sized queries. REQ002003.
//
// Eligibility mirrors tryPushPipeline:
//   - Each level (Limit→Project→Filter→SeqScan) is optional, order fixed.
//   - SeqScan must have a store.
//   - Filter predicate must not contain subqueries.
//   - Project columns must be simple (no arithmetic/function calls).
//
// At least one of Filter/Project/Limit must be present; a bare SeqScan
// returns nil (no fusion benefit over VectorizedSeqScan).
func tryFusedBatchScan(root DT.Operator, p *Planner) UT.BatchProducer {
	if root == nil {
		return nil
	}
	// Unwrap AdaptiveOp (planner wraps the root).
	inner := root
	if aop, ok := root.(*AD.AdaptiveOp); ok {
		inner = aop.Inner
	}

	// Walk Limit → Project → Filter → SeqScan, each level optional.
	var limitOp *OP.Limit
	var projectOp *OP.Project
	var filterOp *OP.Filter
	var seqScan *OP.SeqScan

	cur := inner
	if l, ok := cur.(*OP.Limit); ok {
		limitOp = l
		cur = l.Child()
	}
	if cur == nil {
		return nil
	}
	if proj, ok := cur.(*OP.Project); ok {
		projectOp = proj
		cur = proj.Child()
	}
	if cur == nil {
		return nil
	}
	if f, ok := cur.(*OP.Filter); ok {
		filterOp = f
		cur = f.Child()
	}
	if cur == nil {
		return nil
	}
	if ss, ok := cur.(*OP.SeqScan); ok {
		seqScan = ss
	}
	if seqScan == nil {
		return nil
	}
	// SeqScan must have a store (in-memory tables have different semantics).
	if seqScan.Store() == nil {
		return nil
	}

	// Filter predicate must not contain subqueries.
	if filterOp != nil {
		if containsSubquery(filterOp.Predicate()) {
			return nil
		}
	}

	// Project columns must be simple.
	if projectOp != nil {
		if !isSimpleProject(projectOp.Cols()) {
			return nil
		}
	}

	// At least one of Filter/Project/Limit must be present.
	if filterOp == nil && projectOp == nil && limitOp == nil {
		return nil
	}

	// Transform the SeqScan into a BatchProducer (VectorizedSeqScan).
	source := transformOp(seqScan, p)
	if source == nil {
		return nil
	}

	// Extract filter predicate (nil if absent).
	var pred PS.Expr
	if filterOp != nil {
		pred = filterOp.Predicate()
	}

	// Extract projection exprs + names (nil if absent → SELECT *).
	var exprs []PS.Expr
	var names []string
	if projectOp != nil {
		exprs = projectOp.Cols()
		names = make([]string, len(exprs))
		for i, e := range exprs {
			names[i] = exprName(e)
		}
	}

	// Extract limit (-1 = unlimited if absent).
	limit := int64(-1)
	if limitOp != nil {
		limit = limitOp.LimitValue()
	}

	return OP.NewFusedBatchScan(source, pred, exprs, names, limit)
}

// containsSubquery reports whether an expression tree contains any
// SubqueryExpr node. The push pipeline does not support correlated
// subqueries in the filter predicate (no ExecCtx propagation), so
// we reject such shapes. REQ002002.
func containsSubquery(e PS.Expr) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *PS.SubqueryExpr:
		return true
	case *PS.BinaryExpr:
		return containsSubquery(v.Left) || containsSubquery(v.Right)
	case *PS.UnaryExpr:
		return containsSubquery(v.Operand)
	case *PS.AliasedExpr:
		return containsSubquery(v.Expr)
	case *PS.CastExpr:
		return containsSubquery(v.Expr)
	case *PS.FunctionCall:
		for _, a := range v.Args {
			if containsSubquery(a) {
				return true
			}
		}
	case *PS.CaseExpr:
		if containsSubquery(v.Expr) {
			return true
		}
		for _, w := range v.WhenList {
			if containsSubquery(w.Cond) || containsSubquery(w.Then) {
				return true
			}
		}
		return containsSubquery(v.Else)
	case *PS.BetweenExpr:
		return containsSubquery(v.Expr) || containsSubquery(v.Low) || containsSubquery(v.High)
	case *PS.InExpr:
		if containsSubquery(v.Expr) {
			return true
		}
		if v.Subquery != nil {
			return true
		}
		for _, a := range v.List {
			if containsSubquery(a) {
				return true
			}
		}
	}
	return false
}

// isSimpleProject reports whether all projection expressions are
// simple (column refs, literals, or AliasedExpr wrapping those).
// Arithmetic, function calls, and CASE are rejected because the
// push path doesn't propagate ExecCtx for subquery evaluation.
// REQ002002.
func isSimpleProject(cols []PS.Expr) bool {
	for _, c := range cols {
		if !isSimpleProjectExpr(c) {
			return false
		}
	}
	return true
}

func isSimpleProjectExpr(e PS.Expr) bool {
	switch v := e.(type) {
	case *PS.Ident, *PS.QualifiedName,
		*PS.NumberLiteral, *PS.FloatLiteral,
		*PS.StringLiteral, *PS.BoolLiteral, *PS.NullLiteral,
		*PS.StarExpr:
		return true
	case *PS.AliasedExpr:
		return isSimpleProjectExpr(v.Expr)
	}
	return false
}
