package QP

import (
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// This file provides typed, readable constructors for each PlanNode shape.
// They are the single construction API the planner (and BuildQueryPlan) uses,
// keeping the node fields consistent and avoiding ad-hoc struct literals at
// every call site.

// NewSeqScan builds a sequential scan over table with an optional alias and a
// set of requested columns. predicate, when non-nil, becomes a filter
// fused onto the scan (OpFilterProject semantics) — callers that want a
// separate filter node should build OpFilter as a parent instead.
func NewSeqScan(table, alias string, requestedCols []string) *PlanNode {
	return &PlanNode{
		Op:           OpSeqScan,
		Table:        table,
		Alias:        alias,
		RequestedCols: requestedCols,
	}
}

// NewFilter builds a filter node over child using pred.
func NewFilter(child *PlanNode, pred PS.Expr) *PlanNode {
	n := &PlanNode{
		Op:    OpFilter,
		Pred:  pred,
	}
	n.AddChild(child)
	return n
}

// NewProject builds a projection node over child producing exprs under alias.
func NewProject(child *PlanNode, exprs []PS.Expr, alias string) *PlanNode {
	n := &PlanNode{
		Op:    OpProject,
		Exprs: exprs,
		Alias: alias,
	}
	n.AddChild(child)
	return n
}

// NewFilterProject builds a fused filter+project node over child.
func NewFilterProject(child *PlanNode, pred PS.Expr, exprs []PS.Expr) *PlanNode {
	n := &PlanNode{
		Op:    OpFilterProject,
		Pred:  pred,
		Exprs: exprs,
	}
	n.AddChild(child)
	return n
}

// NewSort builds a sort node (already-ordered input pass-through marker).
func NewSort(child *PlanNode) *PlanNode {
	n := &PlanNode{Op: OpSort}
	n.AddChild(child)
	return n
}

// NewLimit builds a LIMIT/OFFSET node over child.
func NewLimit(child *PlanNode, limit, offset PS.Expr) *PlanNode {
	return &PlanNode{
		Op:         OpLimit,
		LimitExpr:  limit,
		OffsetExpr: offset,
		Children:   []*PlanNode{child},
	}
}

// NewOffset builds an OFFSET node over child.
func NewOffset(child *PlanNode, offset PS.Expr) *PlanNode {
	return &PlanNode{
		Op:         OpOffset,
		OffsetExpr: offset,
		Children:   []*PlanNode{child},
	}
}

// NewDistinct builds a DISTINCT node over child.
func NewDistinct(child *PlanNode) *PlanNode {
	return &PlanNode{
		Op:       OpDistinct,
		Distinct: true,
		Children: []*PlanNode{child},
	}
}

// NewHashJoin builds a hash join of left and right on on.
func NewHashJoin(left, right *PlanNode, on PS.Expr, leftAlias, rightAlias string) *PlanNode {
	return &PlanNode{
		Op:         OpHashJoin,
		On:         on,
		LeftAlias:  leftAlias,
		RightAlias: rightAlias,
		Children:   []*PlanNode{left, right},
	}
}

// NewValuesRows builds a single VALUES row-set node (constant rows).
func NewValuesRows(values [][]PS.Expr) *PlanNode {
	return &PlanNode{
		Op:     OpValuesRows,
		Values: values,
	}
}

// NewInsert builds an INSERT node. When source is non-nil it is the child
// plan producing rows to insert (INSERT ... SELECT); otherwise cols/values
// carry the literal payload.
func NewInsert(table string, cols []string, values [][]PS.Expr, source *PlanNode, returning []PS.Expr) *PlanNode {
	n := &PlanNode{
		Op:        OpInsert,
		Table:     table,
		Cols:      cols,
		Values:    values,
		Returning: returning,
	}
	n.AddChild(source)
	return n
}

// NewUpdate builds an UPDATE node over a SeqScan child (source), applying set
// under where.
func NewUpdate(table string, set []PS.Pair, where PS.Expr, source *PlanNode, returning []PS.Expr) *PlanNode {
	n := &PlanNode{
		Op:        OpUpdate,
		Table:     table,
		Set:       set,
		Where:     where,
		Returning: returning,
	}
	n.AddChild(source)
	return n
}

// NewDelete builds a DELETE node over a SeqScan child (source) filtered by
// where.
func NewDelete(table string, where PS.Expr, source *PlanNode, returning []PS.Expr) *PlanNode {
	n := &PlanNode{
		Op:        OpDelete,
		Table:     table,
		Where:     where,
		Returning: returning,
	}
	n.AddChild(source)
	return n
}
