package EX

import (
	"github.com/cyw0ng95/razordata/internal/SQB/AD"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	"fmt"
	"strings"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
)

// buildPlanNodeTree converts an DT.Operator tree into a PlanNode tree.
// This is used by EXPLAIN to generate structured plan output.
func buildPlanNodeTree(op DT.Operator, planner *Planner) *AD.PlanNode {
	if op == nil {
		return nil
	}

	// Unwrap AdaptiveOp to show inner operator in EXPLAIN output.
	if aop, ok := op.(*AD.AdaptiveOp); ok {
		inner := buildPlanNodeTree(aop.Inner, planner)
		if inner != nil {
			state := "interpreted"
			st := AD.AdqcState(aop.State())
			if st == AD.AdqcCompiling {
				state = "compiling"
			} else if st == AD.AdqcCompiled {
				state = "compiled"
			}
			if inner.Detail != "" {
				inner.Detail += " [" + state + "]"
			} else {
				inner.Detail = "[" + state + "]"
			}
		}
		return inner
	}

	node := &AD.PlanNode{
		Type: operatorType(op),
	}

	switch v := op.(type) {
	case *OP.IndexScan:
		node.Table = v.Table()
		node.Index = v.Idx()
		node.Cost = 0.1
		detail := fmt.Sprintf("idx=%s", v.Idx())
		if v.Btree() != nil {
			detail += " [btree]"
		} else if v.IndexMode() {
			if len(v.IndexSeek()) > 0 {
				detail += " SEEK"
			} else if len(v.IndexLower()) > 0 || len(v.IndexUpper()) > 0 {
				detail += " RANGE"
			} else {
				detail += " SCAN"
			}
		}
		if v.Store() != nil {
			detail += " [store]"
		} else {
			detail += " [memory]"
		}
		node.Detail = detail

	case *OP.SeqScan:
		node.Table = v.Table()
		node.Cost = 1.0
		node.Rows = int64(planner.estimateRowCount(v.Table(), nil))
		node.Width = 100
		detail := fmt.Sprintf("rows=%d", node.Rows)
		if v.Store() != nil {
			detail += " [store]"
		} else {
			detail += " [memory]"
		}
		node.Detail = detail

	case *OP.Filter:
		node.Detail = "WHERE"
		if v.Predicate() != nil {
			node.Detail = "WHERE " + RE.FormatExpr(v.Predicate())
		}
		var ts *AD.TableStats
		if planner != nil && v.Child() != nil {
			if seq, ok := v.Child().(*OP.SeqScan); ok {
				ts = planner.getTableStats(seq.Table())
			} else if idx, ok := v.Child().(*OP.IndexScan); ok {
				ts = planner.getTableStats(idx.Table())
			}
		}
		node.Cost = AD.EstimateFilterCost(v, ts)

	case *OP.Project:
		node.Detail = "PROJECT"
		if len(v.Cols()) > 0 {
			var parts []string
			for _, c := range v.Cols() {
				parts = append(parts, RE.FormatExpr(c))
			}
			node.Detail = "PROJECT " + strings.Join(parts, ", ")
		}
		node.Cost = AD.EstimateProjectCost(v)

	case *OP.Sort:
		node.Detail = "ORDER BY"
		if len(v.Keys()) > 0 {
			var parts []string
			for _, k := range v.Keys() {
				s := RE.FormatExpr(k.Expr)
				if k.Desc {
					s += " DESC"
				}
				parts = append(parts, s)
			}
			node.Detail = "ORDER BY " + strings.Join(parts, ", ")
		}
		var ts *AD.TableStats
		if planner != nil && v.Child() != nil {
			if seq, ok := v.Child().(*OP.SeqScan); ok {
				ts = planner.getTableStats(seq.Table())
			} else if idx, ok := v.Child().(*OP.IndexScan); ok {
				ts = planner.getTableStats(idx.Table())
			}
		}
		node.Cost = AD.EstimateSortCost(v, ts)

	case *OP.Limit:
		node.Detail = "LIMIT"

	case *OP.Offset:
		node.Detail = "OFFSET"

	case *OP.Distinct:
		node.Detail = "DISTINCT"
		node.Cost = AD.EstimateDistinctCost(v, nil)

	case *AG.Aggregate:
		node.Detail = "AGGREGATE"
		node.Cost = AD.EstimateAggregateCost(v, nil)

	case *AG.HashAggregate:
		node.Detail = "HASH AGGREGATE"
		node.Cost = 5.0

	case *AG.WindowOperator:
		node.Detail = "WINDOW"
		node.Cost = 5.0

	case *OP.NestedLoopJoin:
		if v.Kind() == OP.JoinKindSemi {
			node.Detail = "SEMI JOIN"
		} else {
			node.Detail = "JOIN"
		}
		node.Cost = 5.0

	case *OP.HashJoin:
		node.Detail = "HASH JOIN"
		node.Cost = 5.0

	case *OP.CompoundOp:
		node.Detail = "COMPOUND"
		node.Cost = 5.0

	case *OP.ValuesRows:
		node.Detail = fmt.Sprintf("VALUES %d rows", len(v.Rows()))
		node.Cost = 1.0

	case *AD.ExplainStmtOp:
		node.Detail = "EXPLAIN"
		node.Cost = 0

	case *AD.Noop:
		node.Detail = "NOOP"
		node.Cost = 0
	}

	// Recursively build children
	if c, ok := op.(interface{ Child() DT.Operator }); ok {
		child := c.Child()
		if child != nil {
			node.Add(buildPlanNodeTree(child, planner))
		}
	}

	// Handle multi-child operators
	switch v := op.(type) {
	case *OP.NestedLoopJoin:
		if v.LeftChild() != nil {
			node.Add(buildPlanNodeTree(v.LeftChild(), planner))
		}
		if v.RightChild() != nil {
			node.Add(buildPlanNodeTree(v.RightChild(), planner))
		}
	case *OP.HashJoin:
		if v.LeftChild() != nil {
			node.Add(buildPlanNodeTree(v.LeftChild(), planner))
		}
		if v.RightChild() != nil {
			node.Add(buildPlanNodeTree(v.RightChild(), planner))
		}
	case *OP.CompoundOp:
		if v.LeftChild() != nil {
			node.Add(buildPlanNodeTree(v.LeftChild(), planner))
		}
		if v.RightChild() != nil {
			node.Add(buildPlanNodeTree(v.RightChild(), planner))
		}
	case *AG.HashAggregate:
		if v.Child() != nil {
			node.Add(buildPlanNodeTree(v.Child(), planner))
		}
	case *AD.ExplainStmtOp:
		if v.Root != nil {
			node.Add(buildPlanNodeTree(v.Root, planner))
		}
	}

	return node
}

// operatorType returns a human-readable type string for an operator.
func operatorType(op DT.Operator) string {
	if aop, ok := op.(*AD.AdaptiveOp); ok {
		return operatorType(aop.Inner)
	}
	switch op.(type) {
	case *OP.SeqScan:
		return "Scan"
	case *OP.IndexScan:
		return "Search"
	case *OP.BitmapHeapScan:
		return "OP.BitmapHeapScan"
	case *OP.IndexOnlyScan:
		return "OP.IndexOnlyScan"
	case *OP.Filter:
		return "OP.Filter"
	case *OP.Project:
		return "OP.Project"
	case *OP.Sort:
		return "OP.Sort"
	case *OP.Limit:
		return "OP.Limit"
	case *OP.Offset:
		return "OP.Offset"
	case *OP.Distinct:
		return "OP.Distinct"
	case *AG.Aggregate:
		return "Aggregate"
	case *AG.HashAggregate:
		return "HashAggregate"
	case *OP.NestedLoopJoin:
		return "Join"
	case *OP.HashJoin:
		return "OP.HashJoin"
	case *AG.WindowOperator:
		return "Window"
	case *OP.CompoundOp:
		return "Compound"
	case *OP.Values:
		return "OP.Values"
	case *OP.ValuesRows:
		return "OP.ValuesRows"
	case *WT.Insert:
		return "Insert"
	case *WT.Update:
		return "Update"
	case *WT.Delete:
		return "Delete"
	case *WT.CreateTable:
		return "CreateTable"
	case *WT.DropTable:
		return "DropTable"
	case *WT.CreateIndex:
		return "CreateIndex"
	case *WT.DropIndex:
		return "DropIndex"
	case *WT.CreateViewOperator:
		return "CreateView"
	case *WT.DropView:
		return "DropView"
	case *WT.Trigger:
		return "CreateTrigger"
	case *WT.DropTrigger:
		return "DropTrigger"
	case *WT.AlterTable:
		return "AlterTable"
	case *WT.Pragma:
		return "Pragma"
	case *UT.Analyze:
		return "Analyze"
	case *UT.Vacuum:
		return "UT.Vacuum"
	case *UT.IntegrityCheck:
		return "UT.IntegrityCheck"
	case *WT.Truncate:
		return "Truncate"
	case *WT.Reindex:
		return "Reindex"
	case *WT.CreateMatViewOperator:
		return "CreateMatView"
	case *WT.RefreshMatViewOperator:
		return "RefreshMatView"
	case *WT.DropMatViewOperator:
		return "DropMatView"
	case *AD.ExplainStmtOp:
		return "Explain"
	case *AD.Noop:
		return "Noop"
	case *AD.FallbackOp:
		return "Fallback"
	}
	return "Unknown"
}