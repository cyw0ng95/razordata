package PX

import (
	"errors"
	"fmt"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	QP "github.com/cyw0ng95/razordata/internal/SQF/QP"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
)

// LowerQueryPlan recursively translates a QP plan node into the corresponding
// OP.*/WT.* operator, the battle-tested execution substrate that the
// PipelineExecutor decomposes into its vectorized StageSpec pipeline. It is
// called from the planner (which holds the engine store) so the lowered tree
// is store-aware: SeqScans read through the engine and the WT writers mutate
// rows through it. This is the seam REQ002267 routes DML (INSERT/UPDATE/
// DELETE) through the QueryPlan DAG while keeping the StageSpec machinery
// untouched.
//
// When store is nil the plain in-memory operators are produced instead,
// mirroring the legacy planner's store==nil branch.
//
// Any shape the lowering cannot represent faithfully (joins, aggregates,
// window, const-row SELECT) returns an error so the caller keeps the legacy
// operator tree it already built.
func LowerQueryPlan(node *QP.PlanNode, store DT.Store) (DT.Operator, error) {
	if node == nil {
		return nil, nil
	}
	switch node.Op {
	case QP.OpSeqScan:
		if store != nil {
			return OP.NewSeqScanWithStore(store, node.Table)
		}
		return OP.NewSeqScan(node.Table), nil
	case QP.OpFilter:
		child, err := LowerQueryPlan(qpFirstChild(node), store)
		if err != nil {
			return nil, err
		}
		return OP.NewFilter(child, node.Pred, nil), nil
	case QP.OpProject:
		child, err := LowerQueryPlan(qpFirstChild(node), store)
		if err != nil {
			return nil, err
		}
		return OP.NewProject(child, node.Exprs), nil
	case QP.OpFilterProject:
		child, err := LowerQueryPlan(qpFirstChild(node), store)
		if err != nil {
			return nil, err
		}
		return OP.NewFilterProject(child, node.Pred, node.Exprs), nil
	case QP.OpSort:
		child, err := LowerQueryPlan(qpFirstChild(node), store)
		if err != nil {
			return nil, err
		}
		return OP.NewSort(child, node.OrderBy), nil
	case QP.OpLimit:
		child, err := LowerQueryPlan(qpFirstChild(node), store)
		if err != nil {
			return nil, err
		}
		// A single OpLimit node may carry LIMIT, OFFSET, or both.
		if node.OffsetExpr != nil {
			off, e := qpEvalConstInt(node.OffsetExpr)
			if e != nil {
				return nil, e
			}
			child = OP.NewOffset(child, off)
		}
		if node.LimitExpr != nil {
			lim, e := qpEvalConstInt(node.LimitExpr)
			if e != nil {
				return nil, e
			}
			child = OP.NewLimit(child, lim)
		}
		return child, nil
	case QP.OpOffset:
		child, err := LowerQueryPlan(qpFirstChild(node), store)
		if err != nil {
			return nil, err
		}
		n, e := qpEvalConstInt(node.OffsetExpr)
		if e != nil {
			return nil, e
		}
		return OP.NewOffset(child, n), nil
	case QP.OpDistinct:
		child, err := LowerQueryPlan(qpFirstChild(node), store)
		if err != nil {
			return nil, err
		}
		return OP.NewDistinct(child), nil
	case QP.OpValuesRows:
		return OP.NewValuesRowsOp(node.Values), nil
	case QP.OpCompound:
		left, err := LowerQueryPlan(qpChildAt(node, 0), store)
		if err != nil {
			return nil, err
		}
		right, err := LowerQueryPlan(qpChildAt(node, 1), store)
		if err != nil {
			return nil, err
		}
		return OP.NewCompoundOp(left, right, node.CompoundOp, nil, nil, nil), nil
	case QP.OpInsert:
		return lowerInsert(node, store)
	case QP.OpUpdate:
		return lowerUpdate(node, store)
	case QP.OpDelete:
		return lowerDelete(node, store)
	case QP.OpHashJoin:
		// Joins need left/right key columns derived from the ON expression;
		// fall back to the OP path which performs this resolution.
		return nil, errors.New("px: qp hash join falls back to OP")
	case QP.OpAggregate, QP.OpWindow:
		return nil, errors.New("px: qp aggregate/window falls back to OP")
	case QP.OpConstRow:
		// SELECT without FROM is handled by the planner's const-row path.
		return nil, errors.New("px: qp const row falls back to OP")
	default:
		return nil, fmt.Errorf("px: unknown qp node op %s", node.Op)
	}
}

func lowerInsert(node *QP.PlanNode, store DT.Store) (DT.Operator, error) {
	if len(node.Children) > 0 && node.Children[0] != nil {
		// INSERT ... SELECT
		child, err := LowerQueryPlan(node.Children[0], store)
		if err != nil {
			return nil, err
		}
		var ins *WT.Insert
		if store != nil {
			ins, err = WT.NewInsertWithStore(store, node.Table, node.Cols, nil, node.Returning, node.OnConflict)
			if err != nil {
				return nil, err
			}
		} else {
			ins = WT.NewInsert(node.Table, node.Cols, nil, node.Returning, node.OnConflict)
		}
		ins.SetSelectPlan(child)
		ins.SetConflictAction(node.ConflictAction)
		return ins, nil
	}
	var ins *WT.Insert
	var err error
	if store != nil {
		ins, err = WT.NewInsertWithStore(store, node.Table, node.Cols, node.Values, node.Returning, node.OnConflict)
		if err != nil {
			return nil, err
		}
		ins.SetDefaultValues(false)
	} else {
		ins = WT.NewInsert(node.Table, node.Cols, node.Values, node.Returning, node.OnConflict)
	}
	ins.SetConflictAction(node.ConflictAction)
	return ins, nil
}

func lowerUpdate(node *QP.PlanNode, store DT.Store) (DT.Operator, error) {
	child, err := LowerQueryPlan(qpFirstChild(node), store)
	if err != nil {
		return nil, err
	}
	// node.Where is also carried on the QP node; the scan child is already
	// pre-filtered by a Filter node when Where != nil (see QP buildUpdate).
	if store != nil {
		return WT.NewUpdateWithStore(store, node.Table, node.Set, node.Where, child, node.Returning)
	}
	return WT.NewUpdate(node.Table, node.Set, node.Where, child, node.Returning), nil
}

func lowerDelete(node *QP.PlanNode, store DT.Store) (DT.Operator, error) {
	child, err := LowerQueryPlan(qpFirstChild(node), store)
	if err != nil {
		return nil, err
	}
	if store != nil {
		return WT.NewDeleteWithStore(store, node.Table, node.Where, child, node.Returning)
	}
	return WT.NewDelete(node.Table, node.Where, child, node.Returning), nil
}

func qpFirstChild(n *QP.PlanNode) *QP.PlanNode {
	if n == nil || len(n.Children) == 0 {
		return nil
	}
	return n.Children[0]
}

func qpChildAt(n *QP.PlanNode, i int) *QP.PlanNode {
	if n == nil || i >= len(n.Children) {
		return nil
	}
	return n.Children[i]
}

func qpEvalConstInt(e PS.Expr) (int64, error) {
	if e == nil {
		return 0, errors.New("px: missing limit/offset expression")
	}
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val, nil
	}
	return 0, fmt.Errorf("px: non-constant limit/offset expression %T", e)
}
