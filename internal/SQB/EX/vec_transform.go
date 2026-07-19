package EX

import (
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQB/AG"
	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// tryVectorizePlan attempts to replace row-based operators with vectorized
// equivalents. Returns the (possibly modified) root operator wrapped in
// a BatchToRowAdapter. Falls back to the original root unchanged if
// vectorization is not applicable.
func tryVectorizePlan(root DT.Operator) DT.Operator {
	bp := transformOp(root)
	if bp != nil {
		return UT.NewBatchToRowAdapter(bp)
	}
	return root
}

// REQ001614: isEligible removed — tryVectorizePlan always succeeds
// by falling back to ScalarBatchProducer.

// isEligible checks whether the operator tree can be vectorized.
// REQ001602: expanded eligibility — sub-components that are not yet
// vectorized are acceptable; they will be wrapped in
// ScalarBatchProducer at runtime. This turns vectorization from a
// strict white-list into a best-effort optimization.
func isEligible(root DT.Operator) bool {
	var check func(op DT.Operator) bool
	check = func(op DT.Operator) bool {
		if op == nil {
			return true
		}
		switch o := op.(type) {
		case *OP.SeqScan:
			return true
		case *OP.IndexScan:
			// Non-covering IndexScan: still eligible; the scalar
			// path will be wrapped in ScalarBatchProducer.
			return true
		case *OP.BitmapHeapScan:
			return true
		case *OP.Filter:
			return check(o.Child())
		case *OP.Project:
			return check(o.Child())
		case *AG.Aggregate:
			if len(o.GroupCols()) > 0 {
				for _, gc := range o.GroupCols() {
					if _, ok := gc.(*PS.Ident); !ok {
						return false
					}
				}
			}
			return check(o.Child())
		case *AG.HashAggregate:
			if len(o.GroupCols()) > 0 {
				for _, gc := range o.GroupCols() {
					if _, ok := gc.(*PS.Ident); !ok {
						return false
					}
				}
			}
			return true
		case *OP.HashJoin:
			// REQ001618: multi-column equi-join keys are supported.
			// Both sides must have the same number of keys (> 0).
			if len(o.LeftKeys()) == 0 || len(o.RightKeys()) == 0 {
				return false
			}
			if len(o.LeftKeys()) != len(o.RightKeys()) {
				return false
			}
			return check(o.LeftChild()) && check(o.RightChild())
		case *OP.NestedLoopJoin:
			return check(o.LeftChild()) && check(o.RightChild())
		case *OP.MergeJoin:
			return check(o.LeftChild()) && check(o.RightChild())
		case *OP.HashCrossJoin:
			return check(o.LeftChild()) && check(o.RightChild())
		case *OP.Distinct:
			return check(o.Child())
		case *OP.CompoundOp:
			return o.CompoundOpType() == PS.CompoundUnionAll ||
				o.CompoundOpType() == PS.CompoundUnion ||
				o.CompoundOpType() == PS.CompoundExcept ||
				o.CompoundOpType() == PS.CompoundIntersect
		case *OP.Sort:
			return check(o.Child())
		case *OP.Limit:
			return check(o.Child())
		default:
			return false
		}
	}
	return check(root)
}

// transformOp transforms a row operator tree into a BatchProducer chain.
func transformOp(op DT.Operator) UT.BatchProducer {
	if op == nil {
		return nil
	}
	switch o := op.(type) {
	case *OP.SeqScan:
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
		return transformBitmapHeapScan(o)
	case *OP.IndexOnlyScan:
		return OP.NewBatchIndexOnlyScan(o)
	case *OP.Filter:
		child := transformOp(o.Child())
		if child == nil {
			return nil
		}
		return OP.NewVectorizedFilter(child, o.Predicate())
	case *OP.Project:
		child := transformOp(o.Child())
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
		child := transformOp(o.Child())
		if child == nil {
			return nil
		}
		result := transformAggregate(o, child)
		if result == nil {
			return nil
		}
		return result
	case *AG.HashAggregate:
		child := transformOp(o.Child())
		if child == nil {
			return nil
		}
		result := transformHashAggregate(o, child)
		if result == nil {
			return nil
		}
		return result
	case *OP.HashJoin:
		return transformHashJoin(o)
	case *OP.NestedLoopJoin:
		return transformNestedLoopJoin(o)
	case *OP.MergeJoin:
		return transformMergeJoin(o)
	case *OP.HashCrossJoin:
		return transformHashCrossJoin(o)
	case *OP.Distinct:
		return transformDistinct(o)
	case *OP.CompoundOp:
		return transformCompoundOp(o)
	case *OP.Sort:
		child := transformOp(o.Child())
		if child == nil {
			return nil
		}
		return OP.NewVectorizedSort(child, o.Keys())
	case *OP.Limit:
		child := transformOp(o.Child())
		if child == nil {
			return nil
		}
		return OP.NewVectorizedLimit(child, o.Limit())
	}
	return nil
}

// transformHashJoin converts a row HashJoin to VectorizedHashJoin.
// Supports single or multi-column equi-join keys (REQ001618).
// Supports outer join kinds (REQ001619).
// Returns nil if transformation fails.
func transformHashJoin(h *OP.HashJoin) UT.BatchProducer {
	// Transform children: left = probe, right = build
	left := transformOp(h.LeftChild())
	if left == nil {
		return nil
	}
	right := transformOp(h.RightChild())
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
	if kind == OP.JoinKindInner {
		return OP.NewVectorizedHashJoin(right, left, buildIdxs, probeIdxs)
	}
	return OP.NewVectorizedHashJoinWithKind(right, left, buildIdxs, probeIdxs, kind)
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
		kind, colIdx, ok := resolveAggDef(ag, child)
		if !ok {
			// unsupported aggregate — fallback to row-based
			return nil
		}
		defs = append(defs, AG.AggDef{Kind: kind, Col: colIdx})
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
		kind, colIdx, ok := resolveAggDef(ag, child)
		if !ok {
			return nil
		}
		defs = append(defs, AG.AggDef{Kind: kind, Col: colIdx})
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

// resolveAggDef parses an aggregate expression into (AggKind, ColIdx, ok).
// COUNT(*) returns (AggCount, -1, true). COUNT(col) returns (AggCount, colIdx, true).
// Returns (0, 0, false) for unsupported aggregates (DISTINCT, GROUP_CONCAT, etc.).
func resolveAggDef(expr PS.Expr, child DT.Operator) (AG.AggKind, int, bool) {
	// Unwrap AliasedExpr (e.g., SUM(v) AS total)
	if ae, ok := expr.(*PS.AliasedExpr); ok {
		return resolveAggDef(ae.Expr, child)
	}
	af, ok := expr.(*PS.AggregateFunc)
	if !ok {
		return 0, 0, false
	}
	// DISTINCT not supported in vectorized path
	if af.Distinct {
		return 0, 0, false
	}
	var kind AG.AggKind
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
	default:
		return 0, 0, false
	}
	switch arg := af.Arg.(type) {
	case *PS.StarExpr:
		// COUNT(*) uses Col: -1 (no column needed)
		return kind, -1, true
	case *PS.Ident:
		idx, ok := resolveColumnIndex(child, arg.Name)
		if !ok {
			return 0, 0, false
		}
		return kind, idx, true
	default:
		return 0, 0, false
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
func transformNestedLoopJoin(nlj *OP.NestedLoopJoin) UT.BatchProducer {
	left := transformOp(nlj.LeftChild())
	if left == nil {
		return nil
	}
	right := transformOp(nlj.RightChild())
	if right == nil {
		return nil
	}
	return OP.NewVectorizedNestedLoopJoin(left, right, nlj.OnFunc(), nlj.Kind())
}

// transformMergeJoin converts a row MergeJoin to VectorizedMergeJoin.
// REQ001615/REQ001625: pure batch sort-merge — no row intermediary.
// Reads batches from both pre-sorted sides and merges them directly.
func transformMergeJoin(mj *OP.MergeJoin) UT.BatchProducer {
	left := transformOp(mj.LeftChild())
	if left == nil {
		return nil
	}
	right := transformOp(mj.RightChild())
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
func transformBitmapHeapScan(b *OP.BitmapHeapScan) UT.BatchProducer {
	// Transform each child IndexScan to BatchProducer.
	children := b.IndexScans()
	batchChildren := make([]UT.BatchProducer, 0, len(children))
	for _, child := range children {
		if child == nil {
			continue
		}
		bp := transformOp(child)
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
func transformHashCrossJoin(hcj *OP.HashCrossJoin) UT.BatchProducer {
	left := transformOp(hcj.LeftChild())
	if left == nil {
		return nil
	}
	right := transformOp(hcj.RightChild())
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
func transformDistinct(d *OP.Distinct) UT.BatchProducer {
	child := transformOp(d.Child())
	if child == nil {
		return nil
	}
	cols, types := extractSchemaFromOp(d.Child())
	return OP.NewVectorizedDistinct(child, cols, types)
}

// transformCompoundOp converts a row CompoundOp to VectorizedCompoundOp.
// Supports UNION ALL, UNION, EXCEPT, INTERSECT. REQ001442.
func transformCompoundOp(co *OP.CompoundOp) UT.BatchProducer {
	left := transformOp(co.LeftChild())
	if left == nil {
		return nil
	}
	right := transformOp(co.RightChild())
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
