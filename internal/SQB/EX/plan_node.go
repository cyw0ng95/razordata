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

	// REQ002171: AdaptiveOp removed — raw operator tree is used directly.
	node := &AD.PlanNode{
		Type: operatorType(op),
	}

	switch v := op.(type) {
	case *OP.IndexScan:
		node.Table = v.Table()
		node.Index = v.Idx()
		node.Cost = 0.1
		// REQ001342: populate row estimate for IndexScan.
		if planner != nil {
			node.Rows = int64(planner.estimateRowCount(v.Table(), nil))
		}
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
		// REQ001296: show seek range in detail.
		if len(v.IndexSeek()) > 0 {
			detail += fmt.Sprintf(" seek=%s", formatBytes(v.IndexSeek()))
		}
		if len(v.IndexLower()) > 0 || len(v.IndexUpper()) > 0 {
			detail += fmt.Sprintf(" range=[%s, %s)", formatBytes(v.IndexLower()), formatBytes(v.IndexUpper()))
		}
		node.Detail = detail

	case *OP.SeqScan:
		node.Table = v.Table()
		node.Cost = 1.0
		node.Rows = int64(planner.estimateRowCount(v.Table(), nil))
		node.Width = 100
		detail := fmt.Sprintf("rows=%d", node.Rows)
		// REQ001296: annotate SeqScan with index availability.
		if planner != nil {
			if indexes := planner.availableIndexes(v.Table()); len(indexes) > 0 {
				detail += fmt.Sprintf(" [index available: %s]", strings.Join(indexes, ", "))
			} else {
				detail += " [no index]"
			}
		}
		if v.Store() != nil {
			detail += " [store]"
		} else {
			detail += " [memory]"
		}
		node.Detail = detail

	case *OP.IndexOnlyScan:
		node.Type = "IndexOnlyScan"
		node.Detail = "[covering]"
		if v.Inner() != nil {
			node.Add(buildPlanNodeTree(v.Inner(), planner))
		}

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
		node.Cost = EstimateFilterCost(v, ts)

	case *OP.FilterProject:
		node.Detail = "PROJECT+WHERE"
		if v.Predicate() != nil {
			node.Detail = "WHERE " + RE.FormatExpr(v.Predicate())
		}
		if len(v.Cols()) > 0 {
			var parts []string
			for _, c := range v.Cols() {
				parts = append(parts, RE.FormatExpr(c))
			}
			if node.Detail == "PROJECT+WHERE" {
				node.Detail = "PROJECT " + strings.Join(parts, ", ")
			} else {
				node.Detail = node.Detail + " PROJECT " + strings.Join(parts, ", ")
			}
		}
		// Simple cost: estimate rows * (filter selectivity ~10% + cost of projection)
		inputRows := float64(node.Rows)
		if inputRows == 0 {
			inputRows = 100.0
		}
		node.Cost = inputRows * 0.1 // filter selectivity
		node.Cost += inputRows       // projection cost
		node.Rows = int64(inputRows * 0.1)

	case *OP.Project:
		node.Detail = "PROJECT"
		if len(v.Cols()) > 0 {
			var parts []string
			for _, c := range v.Cols() {
				parts = append(parts, RE.FormatExpr(c))
			}
			node.Detail = "PROJECT " + strings.Join(parts, ", ")
		}
		node.Cost = EstimateProjectCost(v)

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
		node.Cost = EstimateSortCost(v, ts)

	case *OP.Limit:
		node.Detail = "LIMIT"

	case *OP.Offset:
		node.Detail = "OFFSET"

	case *OP.Distinct:
		node.Detail = "DISTINCT"
		node.Cost = EstimateDistinctCost(v, nil)

	case *AG.Aggregate:
		node.Detail = "AGGREGATE"
		node.Cost = EstimateAggregateCost(v, nil)
		// REQ001294: extract aggregate function names and GROUP BY columns.
		if aggs := v.Aggs(); len(aggs) > 0 {
			var names []string
			for _, a := range aggs {
				names = append(names, RE.FormatExpr(a))
			}
			node.Detail = "AGGREGATE (" + strings.Join(names, ", ") + ")"
		}
		if gc := v.GroupCols(); len(gc) > 0 {
			var cols []string
			for _, c := range gc {
				cols = append(cols, RE.FormatExpr(c))
			}
			node.Detail += " GROUP BY (" + strings.Join(cols, ", ") + ")"
		}

	// REQ002173: HashAggregate removed — merged into Aggregate case above.

	case *AG.WindowOperator:
		node.Detail = "WINDOW"
		node.Cost = 5.0

	case *OP.NestedLoopJoin:
		// REQ001294: show join type metadata (INNER/LEFT/RIGHT/FULL/SEMI).
		switch v.Kind() {
		case OP.JoinKindLeft:
			node.Detail = "LEFT OUTER JOIN"
		case OP.JoinKindRight:
			node.Detail = "RIGHT OUTER JOIN"
		case OP.JoinKindFull:
			node.Detail = "FULL OUTER JOIN"
		case OP.JoinKindSemi:
			node.Detail = "SEMI JOIN"
		default:
			node.Detail = "INNER JOIN"
		}
		node.Cost = 5.0

	case *OP.HashJoin:
		// REQ001294: show join type metadata (INNER/LEFT/RIGHT/FULL).
		switch v.Kind() {
		case OP.JoinKindLeft:
			node.Detail = "LEFT OUTER HASH JOIN"
		case OP.JoinKindRight:
			node.Detail = "RIGHT OUTER HASH JOIN"
		case OP.JoinKindFull:
			node.Detail = "FULL OUTER HASH JOIN"
		default:
			node.Detail = "HASH JOIN"
		}
		node.Cost = 5.0

	case *OP.MergeJoin:
		// REQ001294: show join type metadata for merge joins.
		switch v.Kind() {
		case OP.JoinKindLeft:
			node.Detail = "LEFT OUTER MERGE JOIN"
		case OP.JoinKindRight:
			node.Detail = "RIGHT OUTER MERGE JOIN"
		case OP.JoinKindFull:
			node.Detail = "FULL OUTER MERGE JOIN"
		default:
			node.Detail = "MERGE JOIN"
		}
		node.Cost = 5.0

	case *OP.HashCrossJoin:
		node.Detail = "HASH CROSS JOIN"
		node.Cost = 5.0

	case *OP.CompoundOp:
		node.Detail = "COMPOUND"
		node.Cost = 5.0

	case *OP.ValuesRows:
		node.Detail = fmt.Sprintf("VALUES %d rows", len(v.Rows()))
		node.Cost = 1.0

	case *OP.FusedScan:
		// REQ001463: FusedScan shows as a single "FusedScan" node
		// summarizing the inlined scan+filter+projection.
		node.Table = v.Table()
		node.Cost = 0.5
		node.Width = 100
		node.Rows = int64(planner.estimateRowCount(v.Table(), nil))
		parts := []string{fmt.Sprintf("rows=%d", node.Rows), "[fused]"}
		if v.HasFilter() {
			parts = append(parts, "[filter]")
		}
		if v.HasProjection() {
			parts = append(parts, "[project]")
		}
		node.Detail = strings.Join(parts, " ")

	case *AD.ExplainStmtOp:
		node.Detail = "EXPLAIN"
		node.Cost = 0

	case *AD.ScalarSubqueryOp:
		node.Detail = "SCALAR SUBQUERY"
		node.Cost = 0

	case *AD.ExistsOp:
		node.Detail = "EXISTS SUBQUERY"
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
	case *OP.MergeJoin:
		if v.LeftChild() != nil {
			node.Add(buildPlanNodeTree(v.LeftChild(), planner))
		}
		if v.RightChild() != nil {
			node.Add(buildPlanNodeTree(v.RightChild(), planner))
		}
	case *OP.HashCrossJoin:
		if v.LeftChild() != nil {
			node.Add(buildPlanNodeTree(v.LeftChild(), planner))
		}
		if v.RightChild() != nil {
			node.Add(buildPlanNodeTree(v.RightChild(), planner))
		}
	case *AG.Aggregate:
		if v.Child() != nil {
			node.Add(buildPlanNodeTree(v.Child(), planner))
		}
	case *AD.ExplainStmtOp:
		if v.Root != nil {
			node.Add(buildPlanNodeTree(v.Root, planner))
		}
	case *AD.ScalarSubqueryOp:
		if v.Root != nil {
			node.Add(buildPlanNodeTree(v.Root, planner))
		}
	case *AD.ExistsOp:
		if v.Root != nil {
			node.Add(buildPlanNodeTree(v.Root, planner))
		}
	}

	return node
}

// operatorType returns a human-readable type string for an operator.
func operatorType(op DT.Operator) string {
	switch op.(type) {
	case *OP.SeqScan:
		return "Scan"
	case *OP.IndexScan:
		return "Search"
	case *OP.BitmapHeapScan:
		return "OP.BitmapHeapScan"
	case *OP.IndexOnlyScan:
		return "IndexOnlyScan"
	case *OP.Filter:
		return "OP.Filter"
	case *OP.FilterProject:
		return "OP.FilterProject"
	case *OP.FusedScan:
		return "FusedScan"
	case *OP.Project:
		return "OP.Project"
	case *OP.Sort:
		return "OP.Sort"
	case *OP.Limit:
		return "OP.Limit"
	case *OP.TopNSort:
		return "TopN"
	case *OP.Offset:
		return "OP.Offset"
	case *OP.Distinct:
		return "OP.Distinct"
	case *AG.Aggregate:
		return "Aggregate"
	case *OP.NestedLoopJoin:
		return "Join"
	case *OP.HashJoin:
		return "HashJoin"
	case *OP.MergeJoin:
		return "MergeJoin"
	case *OP.HashCrossJoin:
		return "HashCrossJoin"
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
	case *UT.QuickCheck:
		return "UT.QuickCheck"
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
	case *AD.ScalarSubqueryOp:
		return "ScalarSubquery"
	case *AD.ExistsOp:
		return "ExistsSubquery"
	}
	return "Unknown"
}

// formatBytes renders a byte slice as a hex string for EXPLAIN output.
func formatBytes(b []byte) string {
	if len(b) == 0 {
		return "nil"
	}
	return fmt.Sprintf("%x", b)
}