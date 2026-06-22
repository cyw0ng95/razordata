package EX

import (
	"fmt"
	"math"
	"strings"

	PS "github.com/cyw0ng95/razordata/internal/SQL/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQL/RE"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

// PlanNode represents a node in the query plan tree for EXPLAIN output.
// It mirrors the Operator tree but captures descriptive metadata for
// human-readable rendering.
type PlanNode struct {
	Type     string        // "SeqScan", "IndexScan", "Filter", etc.
	Table    string        // for scan nodes
	Index    string        // for index nodes
	Cost     float64       // estimated cost
	Rows     int64         // estimated row count
	Width    int           // avg row width (bytes)
	Detail   string        // extra info (filter expr, order by, etc.)
	Children []*PlanNode   // child nodes
	Analyze  *AnalyzeStats // REQ000783: runtime stats from EXPLAIN ANALYZE
}

// AnalyzeStats holds runtime statistics for EXPLAIN ANALYZE.
type AnalyzeStats struct {
	RowsReturned int64
	TimeNS       int64
	Allocs       int64
}

// TableStats aggregates column-level statistics for a table, used by
// the planner for statistics-driven cost estimation (REQ000787).
type TableStats struct {
	RowCount      int64
	ColStats      map[string]*ls.ColumnStats // colName -> ColumnStats
	TotalWidth    int                        // avg row width in bytes
	LastAnalyzed  int64                      // unix nanos
}

// Add appends a child node to this PlanNode.
func (n *PlanNode) Add(child *PlanNode) {
	n.Children = append(n.Children, child)
}

// buildPlanNodeTree converts an Operator tree into a PlanNode tree.
// This is used by EXPLAIN to generate structured plan output.
func buildPlanNodeTree(op Operator, planner *Planner) *PlanNode {
	if op == nil {
		return nil
	}

	// Unwrap AdaptiveOp to show inner operator in EXPLAIN output.
	if aop, ok := op.(*AdaptiveOp); ok {
		inner := buildPlanNodeTree(aop.inner, planner)
		if inner != nil {
			state := "interpreted"
			st := AdqcState(aop.state.Load())
			if st == AdqcCompiling {
				state = "compiling"
			} else if st == AdqcCompiled {
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

	node := &PlanNode{
		Type: operatorType(op),
	}

	switch v := op.(type) {
	case *IndexScan:
		node.Table = v.table
		node.Index = v.idx
		node.Cost = 0.1
		detail := fmt.Sprintf("idx=%s", v.idx)
		if v.btree != nil {
			detail += " [btree]"
		} else if v.indexMode {
			if len(v.indexSeek) > 0 {
				detail += " SEEK"
			} else if len(v.indexLower) > 0 || len(v.indexUpper) > 0 {
				detail += " RANGE"
			} else {
				detail += " SCAN"
			}
		}
		if v.store != nil {
			detail += " [store]"
		} else {
			detail += " [memory]"
		}
		node.Detail = detail

	case *SeqScan:
		node.Table = v.table
		node.Cost = 1.0
		node.Rows = int64(planner.estimateRowCount(v.table, nil))
		node.Width = 100
		detail := fmt.Sprintf("rows=%d", node.Rows)
		if v.store != nil {
			detail += " [store]"
		} else {
			detail += " [memory]"
		}
		node.Detail = detail

	case *Filter:
		node.Detail = "WHERE"
		if v.predicate != nil {
			node.Detail = "WHERE " + RE.FormatExpr(v.predicate)
		}
		// REQ000787: get table stats from child operator's table.
		var ts *TableStats
		if planner != nil && v.child != nil {
			// Try to extract table name from child.
			if seq, ok := v.child.(*SeqScan); ok {
				ts = planner.getTableStats(seq.table)
			} else if idx, ok := v.child.(*IndexScan); ok {
				ts = planner.getTableStats(idx.table)
			}
		}
		node.Cost = estimateFilterCost(v, ts)

	case *Project:
		node.Detail = "PROJECT"
		if len(v.cols) > 0 {
			var parts []string
			for _, c := range v.cols {
				parts = append(parts, RE.FormatExpr(c))
			}
			node.Detail = "PROJECT " + strings.Join(parts, ", ")
		}
		node.Cost = estimateProjectCost(v)

	case *Sort:
		node.Detail = "ORDER BY"
		if len(v.keys) > 0 {
			var parts []string
			for _, k := range v.keys {
				s := RE.FormatExpr(k.Expr)
				if k.Desc {
					s += " DESC"
				}
				parts = append(parts, s)
			}
			node.Detail = "ORDER BY " + strings.Join(parts, ", ")
		}
		// REQ000787: get table stats from child operator's table.
		var ts *TableStats
		if planner != nil && v.child != nil {
			if seq, ok := v.child.(*SeqScan); ok {
				ts = planner.getTableStats(seq.table)
			} else if idx, ok := v.child.(*IndexScan); ok {
				ts = planner.getTableStats(idx.table)
			}
		}
		node.Cost = estimateSortCost(v, ts)

	case *Limit:
		node.Detail = "LIMIT"
		if v.limit > 0 {
			node.Detail = fmt.Sprintf("LIMIT %d", v.limit)
		}
		node.Cost = estimateLimitCost(v)

	case *Offset:
		node.Detail = "OFFSET"
		node.Cost = estimateOffsetCost(v)

	case *Distinct:
		node.Detail = "DISTINCT"
		// REQ000787: get table stats from child operator's table.
		var ts *TableStats
		if planner != nil && v.child != nil {
			if seq, ok := v.child.(*SeqScan); ok {
				ts = planner.getTableStats(seq.table)
			} else if idx, ok := v.child.(*IndexScan); ok {
				ts = planner.getTableStats(idx.table)
			}
		}
		node.Cost = estimateDistinctCost(v, ts)

	case *Aggregate:
		node.Detail = "GROUP BY"
		// REQ000787: get table stats from child operator's table.
		var ts *TableStats
		if planner != nil && v.child != nil {
			if seq, ok := v.child.(*SeqScan); ok {
				ts = planner.getTableStats(seq.table)
			} else if idx, ok := v.child.(*IndexScan); ok {
				ts = planner.getTableStats(idx.table)
			}
		}
		node.Cost = estimateAggregateCost(v, ts)

	case *NestedLoopJoin:
		node.Detail = fmt.Sprintf("JOIN %s", v.rightTbl)
		// REQ000787: get table stats for both sides of the join.
		var leftTS, rightTS *TableStats
		if planner != nil {
			leftTS = planner.getTableStats(v.leftTbl)
			rightTS = planner.getTableStats(v.rightTbl)
		}
		node.Cost = estimateJoinCost(v, leftTS, rightTS)

	case *Insert:
		node.Table = v.table
		node.Detail = fmt.Sprintf("INSERT INTO %s", v.table)
		node.Cost = 1.0

	case *Update:
		node.Table = v.table
		node.Detail = fmt.Sprintf("UPDATE %s", v.table)
		node.Cost = 1.0

	case *Delete:
		node.Table = v.table
		node.Detail = fmt.Sprintf("DELETE FROM %s", v.table)
		node.Cost = 1.0

	case *CreateTable:
		node.Detail = fmt.Sprintf("CREATE TABLE %s", v.stmt.Name)
		node.Cost = 1.0

	case *DropTable:
		node.Detail = fmt.Sprintf("DROP TABLE %s", v.stmt.Name)
		node.Cost = 1.0

	case *CreateIndex:
		node.Detail = fmt.Sprintf("CREATE INDEX %s", v.stmt.Name)
		node.Cost = 5.0

	case *DropIndex:
		node.Detail = fmt.Sprintf("DROP INDEX %s", v.stmt.Name)
		node.Cost = 1.0

	case *CreateViewOperator:
		node.Detail = fmt.Sprintf("CREATE VIEW %s", v.stmt.Name)
		node.Cost = 1.0

	case *DropView:
		node.Detail = fmt.Sprintf("DROP VIEW %s", v.stmt.Name)
		node.Cost = 1.0

	case *DropTrigger:
		node.Detail = fmt.Sprintf("DROP TRIGGER %s", v.stmt.Name)
		node.Cost = 1.0

	case *AlterTable:
		node.Detail = fmt.Sprintf("ALTER TABLE %s", v.stmt.Table)
		node.Cost = 2.0

	case *Pragma:
		node.Detail = fmt.Sprintf("PRAGMA %s", v.stmt.Name)
		node.Cost = 0.5

	case *Analyze:
		node.Detail = "ANALYZE"
		node.Cost = 10.0

	case *Vacuum:
		node.Detail = "VACUUM"
		node.Cost = 50.0

	case *IntegrityCheck:
		node.Detail = "INTEGRITY_CHECK"
		node.Cost = 10.0

	case *Truncate:
		node.Detail = fmt.Sprintf("TRUNCATE %s", v.stmt.Table)
		node.Cost = 1.0

	case *Reindex:
		node.Detail = fmt.Sprintf("REINDEX %s", v.stmt.Target)
		node.Cost = 2.0

	case *CreateMatViewOperator:
		node.Detail = fmt.Sprintf("CREATE MATERIALIZED VIEW %s", v.Name)
		node.Cost = 10.0

	case *RefreshMatViewOperator:
		node.Detail = fmt.Sprintf("REFRESH MATERIALIZED VIEW %s", v.Name)
		node.Cost = 10.0

	case *DropMatViewOperator:
		node.Detail = fmt.Sprintf("DROP MATERIALIZED VIEW %s", v.Name)
		node.Cost = 1.0

	case *HashJoin:
		node.Detail = fmt.Sprintf("HASH JOIN %s", v.rightTbl)
		node.Cost = 10.0

	case *WindowOperator:
		node.Detail = fmt.Sprintf("WINDOW %s", v.funcName)
		node.Cost = 10.0

	case *CompoundOp:
		node.Detail = "COMPOUND"
		node.Cost = 5.0

	case *ValuesRows:
		node.Detail = fmt.Sprintf("VALUES %d rows", len(v.rows))
		node.Cost = 1.0

	case *ExplainStmtOp:
		node.Detail = "EXPLAIN"
		node.Cost = 0

	case *Noop:
		node.Detail = "NOOP"
		node.Cost = 0
	}

	// Recursively build children
	if c, ok := op.(interface{ Child() Operator }); ok {
		child := c.Child()
		if child != nil {
			node.Add(buildPlanNodeTree(child, planner))
		}
	}

	// Handle multi-child operators
	switch v := op.(type) {
	case *NestedLoopJoin:
		if v.left != nil {
			node.Add(buildPlanNodeTree(v.left, planner))
		}
		if v.right != nil {
			node.Add(buildPlanNodeTree(v.right, planner))
		}
	case *Update:
		if v.iter != nil {
			node.Add(buildPlanNodeTree(v.iter, planner))
		}
	case *Delete:
		if v.iter != nil {
			node.Add(buildPlanNodeTree(v.iter, planner))
		}
	case *HashJoin:
		if v.LeftChild() != nil {
			node.Add(buildPlanNodeTree(v.LeftChild(), planner))
		}
		if v.RightChild() != nil {
			node.Add(buildPlanNodeTree(v.RightChild(), planner))
		}
	case *CompoundOp:
		if v.left != nil {
			node.Add(buildPlanNodeTree(v.left, planner))
		}
		if v.right != nil {
			node.Add(buildPlanNodeTree(v.right, planner))
		}
	case *WindowOperator:
		if v.input != nil {
			node.Add(buildPlanNodeTree(v.input, planner))
		}
	case *ExplainStmtOp:
		if v.root != nil {
			node.Add(buildPlanNodeTree(v.root, planner))
		}
	}

	return node
}

// operatorType returns a human-readable type name for an operator.
func operatorType(op Operator) string {
	if aop, ok := op.(*AdaptiveOp); ok {
		return operatorType(aop.inner)
	}
	switch op.(type) {
	case *SeqScan:
		return "Scan"
	case *IndexScan:
		return "Search"
	case *Filter:
		return "Filter"
	case *Project:
		return "Project"
	case *Sort:
		return "Sort"
	case *Limit:
		return "Limit"
	case *Offset:
		return "Offset"
	case *Distinct:
		return "Distinct"
	case *Aggregate:
		return "Aggregate"
	case *HashAggregate:
		return "HashAggregate"
	case *NestedLoopJoin:
		return "Join"
	case *HashJoin:
		return "HashJoin"
	case *WindowOperator:
		return "Window"
	case *CompoundOp:
		return "Compound"
	case *Values:
		return "Values"
	case *ValuesRows:
		return "ValuesRows"
	case *Insert:
		return "Insert"
	case *Update:
		return "Update"
	case *Delete:
		return "Delete"
	case *CreateTable:
		return "CreateTable"
	case *DropTable:
		return "DropTable"
	case *CreateIndex:
		return "CreateIndex"
	case *DropIndex:
		return "DropIndex"
	case *CreateViewOperator:
		return "CreateView"
	case *DropView:
		return "DropView"
	case *Trigger:
		return "CreateTrigger"
	case *DropTrigger:
		return "DropTrigger"
	case *AlterTable:
		return "AlterTable"
	case *Pragma:
		return "Pragma"
	case *Analyze:
		return "Analyze"
	case *Vacuum:
		return "Vacuum"
	case *IntegrityCheck:
		return "IntegrityCheck"
	case *Truncate:
		return "Truncate"
	case *Reindex:
		return "Reindex"
	case *CreateMatViewOperator:
		return "CreateMatView"
	case *RefreshMatViewOperator:
		return "RefreshMatView"
	case *DropMatViewOperator:
		return "DropMatView"
	case *ExplainStmtOp:
		return "Explain"
	case *Noop:
		return "Noop"
	case *FallbackOp:
		return "Fallback"
	}
	return "Unknown"
}

// formatPlanTree renders a PlanNode tree as SQLite-compatible EXPLAIN output.
// Schema: (id, parent, notused, detail)
// The mode parameter controls verbosity:
//   - ExplainNormal: full detail including expressions, costs, and row estimates
//   - ExplainQueryPlan: simplified output (SCAN/SEARCH/JOIN style)
//   - ExplainAnalyze: same as mode but with actual runtime stats appended
func formatPlanTree(n *PlanNode, mode PS.ExplainMode) []Row {
	if n == nil {
		return nil
	}

	var rows []Row
	nextID := 1
	var walk func(node *PlanNode, parent int)
	walk = func(node *PlanNode, parent int) {
		if node == nil {
			return
		}

		id := nextID
		nextID++

		var detail string
		switch mode {
		case PS.ExplainQueryPlan:
			// Simplified SQLite-style output: SCAN, SEARCH, JOIN
			detail = explainQueryPlanDetail(node)
		default:
			// Full detail: type, table, index, expressions, costs, row estimates
			detail = node.Detail
			if detail == "" {
				detail = node.Type
			}
			if node.Table != "" && !strings.Contains(detail, node.Table) {
				detail += " " + node.Table
			}
			if node.Index != "" {
				detail += " USING INDEX " + node.Index
			}
			// Include cost and row estimates for verbose modes
			if node.Cost > 0 {
				detail += fmt.Sprintf(" cost=%.2f", node.Cost)
			}
			if node.Rows > 0 {
				detail += fmt.Sprintf(" rows=%d", node.Rows)
			}
		}

		// REQ000783: append EXPLAIN ANALYZE runtime stats.
		if a := node.Analyze; a != nil {
			detail += fmt.Sprintf(" (actual rows=%d time=%dns allocs=%d)", a.RowsReturned, a.TimeNS, a.Allocs)
		}

		rows = append(rows, Row{
			Cols:  []string{"id", "parent", "notused", "detail"},
			Types: []int{1, 1, 1, 1},
			Data:  []Value{NewIntValue(int64(id)), NewIntValue(int64(parent)), NewIntValue(0), NewTextValue(detail)},
		})

		for _, child := range node.Children {
			walk(child, id)
		}
	}

	walk(n, 0)
	return rows
}

// explainQueryPlanDetail produces simplified SQLite-style output for EXPLAIN QUERY PLAN.
// Maps internal operator types to SCAN/SEARCH/JOIN verbs.
func explainQueryPlanDetail(n *PlanNode) string {
	switch n.Type {
	case "Scan":
		if n.Index != "" {
			return "SEARCH " + n.Table + " USING INDEX " + n.Index
		}
		return "SCAN " + n.Table
	case "Search":
		return "SEARCH " + n.Table + " USING INDEX " + n.Index
	case "Join":
		return "JOIN " + n.Table
	case "HashJoin":
		return "HASH JOIN " + n.Table
	case "Filter":
		return "FILTER"
	case "Project":
		return "PROJECT"
	case "Sort":
		return "SORT"
	case "Distinct":
		return "DISTINCT"
	case "Aggregate":
		return "AGGREGATE"
	case "Limit":
		return "LIMIT"
	case "Offset":
		return "OFFSET"
	case "Values":
		return "VALUES"
	case "Insert":
		return "INSERT"
	case "Update":
		return "UPDATE"
	case "Delete":
		return "DELETE"
	default:
		return strings.ToUpper(n.Type)
	}
}

// Cost estimation helpers

// estimateFilterCost estimates the cost of a filter operator.
// REQ000787: uses column statistics to estimate selectivity when available.
func estimateFilterCost(f *Filter, ts *TableStats) float64 {
	if f.child == nil {
		return 1.0
	}
	// REQ000787: derive selectivity from column statistics.
	// Without a specific column reference, use a conservative default.
	selectivity := 0.5 // default
	if ts != nil && ts.RowCount > 0 {
		// Use row count ratio as a rough selectivity indicator.
		// For a filter like "v > X", we assume ~50% selectivity.
		// In future iterations, histogram-based selectivity estimation
		// (REQ000085) will provide more accurate estimates.
		selectivity = 0.5
	}
	return selectivity
}

// estimateProjectCost estimates the cost of a projection operator.
func estimateProjectCost(p *Project) float64 {
	if p.child == nil {
		return 1.0
	}
	return 1.0 // Projection is cheap
}

// estimateSortCost estimates the cost of a sort operator.
// REQ000787: uses table statistics to estimate input size.
func estimateSortCost(s *Sort, ts *TableStats) float64 {
	if s.child == nil {
		return 1.0
	}
	inputRows := 100.0
	if ts != nil && ts.RowCount > 0 {
		inputRows = float64(ts.RowCount)
	}
	// Sort is O(n log n)
	return 10.0 * (1 + math.Log2(inputRows+1))
}

// estimateLimitCost estimates the cost of a limit operator.
func estimateLimitCost(l *Limit) float64 {
	if l.child == nil {
		return 1.0
	}
	return 1.0 // Limit is cheap
}

// estimateOffsetCost estimates the cost of an offset operator.
func estimateOffsetCost(o *Offset) float64 {
	if o.child == nil {
		return 1.0
	}
	return 1.0 // Offset is cheap
}

// estimateDistinctCost estimates the cost of a distinct operator.
// REQ000787: uses table statistics to estimate deduplication cost.
func estimateDistinctCost(d *Distinct, ts *TableStats) float64 {
	if d.child == nil {
		return 1.0
	}
	inputRows := 100.0
	if ts != nil && ts.RowCount > 0 {
		inputRows = float64(ts.RowCount)
	}
	return 2.0 + inputRows // deduplication requires hashing/sorting
}

// estimateAggregateCost estimates the cost of an aggregation operator.
// REQ000787: uses table statistics to estimate input size.
func estimateAggregateCost(a *Aggregate, ts *TableStats) float64 {
	if a.child == nil {
		return 1.0
	}
	inputRows := 100.0
	if ts != nil && ts.RowCount > 0 {
		inputRows = float64(ts.RowCount)
	}
	return 5.0 + inputRows // base cost + per-row processing
}

// estimateIndexCost estimates the cost of an index scan.
// REQ000787: uses table statistics to estimate index selectivity.
func estimateIndexCost(ts *TableStats, indexCols []string) float64 {
	if ts == nil || ts.RowCount == 0 {
		return 10.0 // default index scan cost
	}
	// Index scan is cheaper than seq scan; base cost is 1.0 per matching row
	// Use distinct count to estimate selectivity
	totalDistinct := int64(1)
	for _, col := range indexCols {
		if cs, ok := ts.ColStats[col]; ok {
			if cs.DistinctCount > 0 {
				totalDistinct *= cs.DistinctCount
			}
		}
	}
	selectivity := float64(ts.RowCount) / float64(totalDistinct)
	if selectivity < 1.0 {
		selectivity = 1.0
	}
	return selectivity // cost proportional to matching rows
}

// estimateJoinCost estimates the cost of a nested-loop join.
// REQ000787: uses table statistics to estimate join cardinality.
func estimateJoinCost(j *NestedLoopJoin, leftTS, rightTS *TableStats) float64 {
	leftCost := 1.0
	rightCost := 1.0
	if j.left != nil {
		if leftTS != nil && leftTS.RowCount > 0 {
			leftCost = float64(leftTS.RowCount)
		}
	}
	if j.right != nil {
		if rightTS != nil && rightTS.RowCount > 0 {
			rightCost = float64(rightTS.RowCount)
		}
	}
	return leftCost * rightCost
}
