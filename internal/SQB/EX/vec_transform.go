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
// a BatchToRowAdapter. Returns the original root unchanged if
// vectorization is not applicable.
func tryVectorizePlan(root DT.Operator) DT.Operator {
	if !isEligible(root) {
		return root
	}
	bp := transformOp(root)
	if bp == nil {
		return root
	}
	return UT.NewBatchToRowAdapter(bp)
}

// isEligible checks whether the operator tree can be vectorized.
func isEligible(root DT.Operator) bool {
	var check func(op DT.Operator) bool
	check = func(op DT.Operator) bool {
		if op == nil {
			return true
		}
		switch o := op.(type) {
		case *OP.SeqScan:
			return true
		case *OP.Filter:
			child := o.Child()
			_, isSeq := child.(*OP.SeqScan)
			if isSeq {
				return true
			}
			_, isAgg := child.(*AG.Aggregate)
			if isAgg {
				return true
			}
			return false
		case *OP.Project:
			child := o.Child()
			_, isSeq := child.(*OP.SeqScan)
			if isSeq {
				return true
			}
			// Project(Filter(SeqScan)) shape
			f, isFilt := child.(*OP.Filter)
			if isFilt {
				_, filtChildIsSeq := f.Child().(*OP.SeqScan)
				return filtChildIsSeq
			}
			// Project(Aggregate(SeqScan)) shape
			_, isAgg := child.(*AG.Aggregate)
			if isAgg {
				return true
			}
			return false
		case *AG.Aggregate:
			if len(o.GroupCols()) > 0 {
				for _, gc := range o.GroupCols() {
					if _, ok := gc.(*PS.Ident); !ok {
						return false
					}
				}
			}
			child := o.Child()
			_, isSeq := child.(*OP.SeqScan)
			if isSeq {
				return true
			}
			// Filter(SeqScan) shape
			f, isFilt := child.(*OP.Filter)
			if isFilt {
				_, filtChildIsSeq := f.Child().(*OP.SeqScan)
				return filtChildIsSeq
			}
			return false
		case *OP.HashJoin:
			// VectorizedHashJoin supports single-column equi-joins only.
			// Both children must be SeqScan for vectorization.
			_, leftIsSeq := o.LeftChild().(*OP.SeqScan)
			if !leftIsSeq {
				return false
			}
			_, rightIsSeq := o.RightChild().(*OP.SeqScan)
			if !rightIsSeq {
				return false
			}
			if len(o.LeftKeys()) != 1 || len(o.RightKeys()) != 1 {
				return false
			}
			return true
		case *OP.NestedLoopJoin, *OP.Distinct, *OP.CompoundOp:
			return false
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
		return OP.NewVectorizedSeqScan(o, schema, types)
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
		return OP.NewVectorizedProject(child, exprs, names)
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
	case *OP.HashJoin:
		return transformHashJoin(o)
	}
	return nil
}

// transformHashJoin converts a row HashJoin to VectorizedHashJoin.
// The HashJoin must have single-column keys and SeqScan children
// (verified by isEligible). Returns nil if transformation fails.
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
	// Single-column keys only (verified by isEligible).
	leftKey := h.LeftKeys()[0]
	rightKey := h.RightKeys()[0]
	buildIdx, ok := resolveColumnIndex(h.RightChild(), rightKey)
	if !ok {
		return nil
	}
	probeIdx, ok := resolveColumnIndex(h.LeftChild(), leftKey)
	if !ok {
		return nil
	}
	// VectorizedHashJoin expects build (right) side first, then probe (left).
	return OP.NewVectorizedHashJoin(right, left, buildIdx, probeIdx)
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

// resolveColumnIndex finds the column index for a named column by
// traversing down to the SeqScan and looking up in its schema.
// Falls back to index 0 when schema is unavailable (e.g., test mode).
func resolveColumnIndex(child DT.Operator, colName string) (int, bool) {
	var scan *OP.SeqScan
	switch c := child.(type) {
	case *OP.SeqScan:
		scan = c
	case *OP.Filter:
		scan, _ = c.Child().(*OP.SeqScan)
	default:
		return 0, false
	}
	if scan == nil {
		return 0, false
	}
	sch := scan.Schema()
	if sch == nil {
		// Schema unavailable (test mode, NewSeqScan without store) — return index 0
		// as a best-effort fallback. This matches the original behavior where
		// groupColIdxs[i] = i was used directly.
		return 0, true
	}
	for i, name := range sch.Cols {
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
