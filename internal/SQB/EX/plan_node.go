package EX

import (
	"fmt"
	"math"
	"strings"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

// PlanNode represents a node in the query plan tree for EXPLAIN output.
// It mirrors the Operator tree but captures descriptive metadata for
// human-readable rendering.
type PlanNode struct {
	Type        string          // "SeqScan", "IndexScan", "Filter", etc.
	Table       string          // for scan nodes
	Index       string          // for index nodes
	Cost        float64         // estimated cost
	Rows        int64           // estimated row count
	Width       int             // avg row width (bytes)
	Detail      string          // extra info (filter expr, order by, etc.)
	Children    []*PlanNode     // child nodes
	Analyze     *AnalyzeStats   // REQ000783: runtime stats from EXPLAIN ANALYZE
	Bottleneck  *BottleneckInfo // REQ000788: bottleneck analysis
	IndexHint   *IndexHint      // REQ000790: index diagnostics
	Subquery    *SubqueryInfo   // REQ000791: subquery optimization analysis
	TxnDebug    *TxnDebugInfo   // REQ000792: transaction/MVCC debugging
	Cache       *CacheInfo      // REQ000793: plan cache analysis
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

// BottleneckInfo captures identified performance bottlenecks in a query plan
// (REQ000788). It is attached to PlanNode for EXPLAIN ANALYZE output.
type BottleneckInfo struct {
	Severity     string   // "low", "medium", "high", "critical"
	Type         string   // "seq_scan", "large_sort", "hash_join_fallback", "high_allocs", "skew"
	Details      string   // human-readable description
	Recommendations []string // suggested fixes
	ActualRows   int64
	EstimatedRows int64
	ActualTimeNS int64
	CostRatio    float64 // actual/estimated cost ratio
}

// IndexHint provides index diagnostic information for a PlanNode.
// REQ000790: Index diagnostics in EXPLAIN output.
type IndexHint struct {
	Used         bool     // whether an index was used
	IndexName    string   // name of index used (if Used=true)
	AvailableIdx []string // available indexes on the table
	MissingCols  []string // columns that could benefit from an index
	Reason       string   // why index was skipped or recommended
}

// SubqueryInfo provides subquery optimization analysis.
// REQ000791: Subquery optimization analysis in EXPLAIN output.
type SubqueryInfo struct {
	Type           string // "correlated", "uncorrelated", "semi-join", "anti-join"
	Unnested       bool   // whether the subquery was unnested/flattened
	ExecutionCount int64  // number of times the subquery was executed
	Method         string // "naive", "semi-join", "hash-join", "flattened"
}

// TxnDebugInfo provides transaction/MVCC debugging information.
// REQ000792: Transaction debugging in EXPLAIN ANALYZE output.
type TxnDebugInfo struct {
	SnapshotTS    uint64 // snapshot timestamp
	VisibleRows   int64  // rows visible at snapshot
	HiddenByMVCC  int64  // rows hidden by older versions
	LockWaitTimeNS int64 // total lock wait time in nanoseconds
	IsolationLevel string // transaction isolation level
}

// CacheInfo provides plan cache analysis information.
// REQ000793: Plan cache analysis in EXPLAIN output.
type CacheInfo struct {
	Hit       bool    // whether this plan was a cache hit
	HitRate   float64 // overall cache hit rate percentage
	Hits      int64   // total hits
	Misses    int64   // total misses
	Evictions int64   // total evictions
}

// Add appends a child node to this PlanNode.
func (n *PlanNode) Add(child *PlanNode) {
	n.Children = append(n.Children, child)
}

// SetBottleneck attaches bottleneck analysis to this node (REQ000788).
func (n *PlanNode) SetBottleneck(bn *BottleneckInfo) {
	n.Bottleneck = bn
}

// SetIndexHint attaches index diagnostic information to this node (REQ000790).
func (n *PlanNode) SetIndexHint(ih *IndexHint) {
	n.IndexHint = ih
}

// SetSubquery attaches subquery optimization analysis to this node (REQ000791).
func (n *PlanNode) SetSubquery(sq *SubqueryInfo) {
	n.Subquery = sq
}

// SetTxnDebug attaches transaction debugging information to this node (REQ000792).
func (n *PlanNode) SetTxnDebug(td *TxnDebugInfo) {
	n.TxnDebug = td
}

// SetCache attaches plan cache analysis to this node (REQ000793).
func (n *PlanNode) SetCache(ci *CacheInfo) {
	n.Cache = ci
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

		// REQ000788: append bottleneck analysis if present.
		if bn := node.Bottleneck; bn != nil {
			detail += fmt.Sprintf(" [BOTTLENECK: %s severity=%s]", bn.Type, bn.Severity)
			if bn.Details != "" {
				detail += fmt.Sprintf(" (%s)", bn.Details)
			}
			if len(bn.Recommendations) > 0 {
				detail += fmt.Sprintf(" recommend: %s", strings.Join(bn.Recommendations, "; "))
			}
		}

		// REQ000790: append index hint if present.
		if ih := node.IndexHint; ih != nil {
			if ih.Used {
				detail += fmt.Sprintf(" [INDEX: %s used]", ih.IndexName)
			} else {
				detail += fmt.Sprintf(" [INDEX: skipped — %s]", ih.Reason)
				if len(ih.MissingCols) > 0 {
					detail += fmt.Sprintf(" consider index on (%s)", strings.Join(ih.MissingCols, ", "))
				}
			}
		}

		// REQ000791: append subquery info if present.
		if sq := node.Subquery; sq != nil {
			if sq.Unnested {
				detail += fmt.Sprintf(" [SUBQUERY: unnested → %s]", sq.Method)
			} else {
				detail += fmt.Sprintf(" [SUBQUERY: %s executed %d times]", sq.Type, sq.ExecutionCount)
				if sq.ExecutionCount > 1000 {
					detail += " WARNING: consider rewriting as JOIN"
				}
			}
		}

		// REQ000792: append txn debug info if present.
		if td := node.TxnDebug; td != nil {
			detail += fmt.Sprintf(" [MVCC: visible=%d hidden=%d snapshot=%d]", td.VisibleRows, td.HiddenByMVCC, td.SnapshotTS)
			if td.LockWaitTimeNS > 0 {
				detail += fmt.Sprintf(" lock_wait=%dns", td.LockWaitTimeNS)
			}
		}

		// REQ000793: append cache info if present.
		if ci := node.Cache; ci != nil {
			if ci.Hit {
				detail += fmt.Sprintf(" [CACHE: hit (rate=%.1f%%)]", ci.HitRate)
			} else {
				detail += fmt.Sprintf(" [CACHE: miss (hits=%d misses=%d rate=%.1f%%)]", ci.Hits, ci.Misses, ci.HitRate)
			}
		}

		rows = append(rows, Row{
			Cols:  []string{"id", "parent", "notused", "detail"},
			Types: []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW, LX.T_INT_KW, LX.T_TEXT},
			Data:  []Value{NewIntValue(int64(id)), NewIntValue(int64(parent)), NewIntValue(0), NewTextValue(detail)},
		})

		for _, child := range node.Children {
			walk(child, id)
		}
	}

	walk(n, 0)
	return rows
}

// AnalyzePlanForBottlenecks walks the plan tree and identifies performance
// bottlenecks based on runtime statistics and cost estimates (REQ000788).
func AnalyzePlanForBottlenecks(root *PlanNode) []*BottleneckInfo {
	var bottlenecks []*BottleneckInfo
	if root == nil {
		return bottlenecks
	}

	var walk func(node *PlanNode)
	walk = func(node *PlanNode) {
		if node == nil {
			return
		}

		bn := identifyBottleneck(node)
		if bn != nil {
			node.SetBottleneck(bn)
			bottlenecks = append(bottlenecks, bn)
		}

		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)

	return bottlenecks
}

// identifyBottleneck examines a single plan node and returns a BottleneckInfo
// if a performance issue is detected (REQ000788).
func identifyBottleneck(node *PlanNode) *BottleneckInfo {
	if node.Analyze == nil {
		return nil
	}

	bn := &BottleneckInfo{
		ActualRows:    node.Analyze.RowsReturned,
		EstimatedRows: node.Rows,
		ActualTimeNS:  node.Analyze.TimeNS,
	}

	// Check for high actual vs estimated row ratio (cardinality misestimate).
	if node.Rows > 0 && node.Analyze.RowsReturned > 0 {
		bn.CostRatio = float64(node.Analyze.RowsReturned) / float64(node.Rows)
		if bn.CostRatio > 5.0 {
			bn.Severity = "high"
			bn.Type = "cardinality_misestimate"
			bn.Details = fmt.Sprintf("actual rows (%d) >> estimated rows (%d)", node.Analyze.RowsReturned, node.Rows)
			bn.Recommendations = []string{"run ANALYZE to refresh statistics", "consider adding indexes on filter columns"}
		} else if bn.CostRatio > 2.0 {
			bn.Severity = "medium"
			bn.Type = "cardinality_misestimate"
			bn.Details = fmt.Sprintf("actual rows (%d) > estimated rows (%d)", node.Analyze.RowsReturned, node.Rows)
			bn.Recommendations = []string{"run ANALYZE to refresh statistics"}
		}
	}

	// Check for high allocation count (memory pressure).
	if node.Analyze.Allocs > 100 {
		if bn.Severity == "" {
			bn.Severity = "medium"
		}
		bn.Type = "high_allocs"
		bn.Details = fmt.Sprintf("high allocation count (%d)", node.Analyze.Allocs)
		bn.Recommendations = append(bn.Recommendations, "consider vectorized execution or pre-allocated buffers")
	}

	// Check for sequential scan on large table.
	if node.Type == "SeqScan" && node.Analyze.RowsReturned > 1000 {
		if bn.Severity == "" {
			bn.Severity = "medium"
		}
		if bn.Type == "" {
			bn.Type = "seq_scan"
		}
		bn.Details = fmt.Sprintf("sequential scan processed %d rows", node.Analyze.RowsReturned)
		bn.Recommendations = append(bn.Recommendations, "consider adding an index on filter/join columns")
	}

	// Check for sort on large input.
	if node.Type == "Sort" && node.Analyze.TimeNS > 1000000 { // > 1ms
		if bn.Severity == "" {
			bn.Severity = "low"
		}
		bn.Type = "large_sort"
		bn.Details = fmt.Sprintf("sort took %d ns on %d rows", node.Analyze.TimeNS, node.Analyze.RowsReturned)
		bn.Recommendations = append(bn.Recommendations, "consider adding an index to avoid sort")
	}

	if bn.Type == "" {
		return nil
	}
	if bn.Severity == "" {
		bn.Severity = "low"
	}
	return bn
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

// ToJSON serializes the PlanNode tree as JSON.
func (n *PlanNode) ToJSON() string {
	result := planNodeToJSON(n)
	
	// Inline JSON serialization
	var b strings.Builder
	buildJSON(&b, result)
	return b.String()
}

// planNodeToJSON converts a PlanNode to a JSON-serializable struct.
func planNodeToJSON(node *PlanNode) *planNodeJSON {
	if node == nil {
		return nil
	}
	
	jn := &planNodeJSON{
		Type:   node.Type,
		Table:  node.Table,
		Index:  node.Index,
		Cost:   node.Cost,
		Rows:   node.Rows,
		Detail: node.Detail,
	}
	if len(node.Children) > 0 {
		for _, child := range node.Children {
			jn.Children = append(jn.Children, planNodeToJSON(child))
		}
	}
	if node.Analyze != nil {
		jn.Analyze = &analyzeStatsJSON{
			RowsReturned: node.Analyze.RowsReturned,
			TimeNS:       node.Analyze.TimeNS,
			Allocs:       node.Analyze.Allocs,
		}
	}
	if node.Bottleneck != nil {
		jn.Bottleneck = &bottleneckJSON{
			Severity:        node.Bottleneck.Severity,
			Type:            node.Bottleneck.Type,
			Details:         node.Bottleneck.Details,
			Recommendations: node.Bottleneck.Recommendations,
			ActualRows:      node.Bottleneck.ActualRows,
			EstimatedRows:   node.Bottleneck.EstimatedRows,
			ActualTimeNS:    node.Bottleneck.ActualTimeNS,
			CostRatio:       node.Bottleneck.CostRatio,
		}
	}
	return jn
}

// buildJSON writes JSON for a planNodeJSON to the builder.
func buildJSON(b *strings.Builder, jn *planNodeJSON) {
	if jn == nil {
		b.WriteString("null")
		return
	}
	b.WriteByte('{')
	if jn.ID != 0 {
		b.WriteString(`"id":`)
		b.WriteString(fmt.Sprintf("%d", jn.ID))
		b.WriteByte(',')
	}
	b.WriteString(`"type":"`)
	b.WriteString(escapeJSON(jn.Type))
	b.WriteByte('"')
	if jn.Table != "" {
		b.WriteString(`,"table":"`)
		b.WriteString(escapeJSON(jn.Table))
		b.WriteByte('"')
	}
	if jn.Index != "" {
		b.WriteString(`,"index":"`)
		b.WriteString(escapeJSON(jn.Index))
		b.WriteByte('"')
	}
	if jn.Cost > 0 {
		b.WriteString(`,"cost":`)
		b.WriteString(fmt.Sprintf("%g", jn.Cost))
	}
	if jn.Rows > 0 {
		b.WriteString(`,"rows":`)
		b.WriteString(fmt.Sprintf("%d", jn.Rows))
	}
	if jn.Detail != "" {
		b.WriteString(`,"detail":"`)
		b.WriteString(escapeJSON(jn.Detail))
		b.WriteByte('"')
	}
	if len(jn.Children) > 0 {
		b.WriteString(`,"children":[`)
		for i, child := range jn.Children {
			if i > 0 {
				b.WriteByte(',')
			}
			buildJSON(b, child)
		}
		b.WriteByte(']')
	}
	if jn.Analyze != nil {
		b.WriteString(`,"analyze":{"rows_returned":`)
		b.WriteString(fmt.Sprintf("%d", jn.Analyze.RowsReturned))
		b.WriteString(`,"time_ns":`)
		b.WriteString(fmt.Sprintf("%d", jn.Analyze.TimeNS))
		b.WriteString(`,"allocs":`)
		b.WriteString(fmt.Sprintf("%d", jn.Analyze.Allocs))
		b.WriteByte('}')
	}
	if jn.Bottleneck != nil {
		b.WriteString(`,"bottleneck":{"severity":"`)
		b.WriteString(escapeJSON(jn.Bottleneck.Severity))
		b.WriteByte('"')
		b.WriteString(`,"type":"`)
		b.WriteString(escapeJSON(jn.Bottleneck.Type))
		b.WriteByte('"')
		if jn.Bottleneck.Details != "" {
			b.WriteString(`,"details":"`)
			b.WriteString(escapeJSON(jn.Bottleneck.Details))
			b.WriteByte('"')
		}
		if len(jn.Bottleneck.Recommendations) > 0 {
			b.WriteString(`,"recommendations":[`)
			for i, rec := range jn.Bottleneck.Recommendations {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(`"`)
				b.WriteString(escapeJSON(rec))
				b.WriteByte('"')
			}
			b.WriteByte(']')
		}
		b.WriteByte('}')
	}
	b.WriteByte('}')
}

// ToDOT generates Graphviz DOT format for the PlanNode tree.
func (n *PlanNode) ToDOT() string {
	var b strings.Builder
	b.WriteString("digraph plan {\n")
	b.WriteString("  rankdir=TB;\n")
	b.WriteString("  node [shape=box, style=filled];\n")
	
	nodeID := 0
	var walk func(node *PlanNode) string
	walk = func(node *PlanNode) string {
		if node == nil {
			return ""
		}
		id := nodeID
		nodeID++
		
		// Build label
		label := node.Type
		if node.Table != "" {
			label += "\\n" + node.Table
		}
		if node.Detail != "" {
			label += "\\n" + node.Detail
		}
		if node.Cost > 0 {
			label += "\\ncost=" + fmt.Sprintf("%.2f", node.Cost)
		}
		if node.Rows > 0 {
			label += "\\nrows=" + fmt.Sprintf("%d", node.Rows)
		}
		if node.Analyze != nil {
			label += "\\n(actual=" + fmt.Sprintf("%d", node.Analyze.RowsReturned) + ")"
		}
		
		// Write node
		b.WriteString(fmt.Sprintf("  n%d [label=\"%s\"];\n", id, escapeDOT(label)))
		
		// Write edges and recurse
		for _, child := range node.Children {
			childID := walk(child)
			if childID != "" {
				b.WriteString(fmt.Sprintf("  n%d -> n%s;\n", id, childID))
			}
		}
		
		return fmt.Sprintf("%d", id)
	}
	
	walk(n)
	b.WriteString("}\n")
	return b.String()
}

// ToTree renders the PlanNode tree as ASCII art.
func (n *PlanNode) ToTree() string {
	if n == nil {
		return ""
	}
	
	var b strings.Builder
	var walk func(node *PlanNode, prefix string, isLast bool)
	walk = func(node *PlanNode, prefix string, isLast bool) {
		if node == nil {
			return
		}
		
		// Build line
		connector := "└── "
		if !isLast {
			connector = "├── "
		}
		if prefix == "" {
			connector = ""
		}
		
		line := connector
		if node.Type != "" {
			line += node.Type
		}
		if node.Table != "" {
			line += " " + node.Table
		}
		if node.Index != "" {
			line += " [idx:" + node.Index + "]"
		}
		if node.Detail != "" && node.Detail != node.Type {
			line += " " + node.Detail
		}
		if node.Cost > 0 {
			line += " cost=" + fmt.Sprintf("%.2f", node.Cost)
		}
		if node.Rows > 0 {
			line += " rows=" + fmt.Sprintf("%d", node.Rows)
		}
		if node.Analyze != nil {
			line += " (actual=" + fmt.Sprintf("%d", node.Analyze.RowsReturned) + " time=" + fmt.Sprintf("%d", node.Analyze.TimeNS) + "ns)"
		}
		if node.Bottleneck != nil {
			line += " [BOTTLENECK: " + node.Bottleneck.Type + "]"
		}
		
		b.WriteString(line + "\n")
		
		// Build new prefix for children
		newPrefix := prefix
		if prefix != "" {
			if isLast {
				newPrefix += "    "
			} else {
				newPrefix += "│   "
			}
		}
		
		// Process children
		for i, child := range node.Children {
			isLastChild := i == len(node.Children)-1
			walk(child, newPrefix, isLastChild)
		}
	}
	
	walk(n, "", true)
	return b.String()
}

// escapeDOT escapes special characters for DOT labels.
func escapeDOT(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

// escapeJSON escapes special characters for JSON strings.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

// analyzeStatsJSON is a JSON-serializable version of AnalyzeStats.
type analyzeStatsJSON struct {
	RowsReturned int64 `json:"rows_returned"`
	TimeNS       int64 `json:"time_ns"`
	Allocs       int64 `json:"allocs"`
}

// bottleneckJSON is a JSON-serializable version of BottleneckInfo.
type bottleneckJSON struct {
	Severity        string   `json:"severity"`
	Type            string   `json:"type"`
	Details         string   `json:"details"`
	Recommendations []string `json:"recommendations"`
	ActualRows      int64    `json:"actual_rows"`
	EstimatedRows   int64    `json:"estimated_rows"`
	ActualTimeNS    int64    `json:"actual_time_ns"`
	CostRatio       float64  `json:"cost_ratio"`
}

// planNodeJSON is a JSON-serializable version of PlanNode.
type planNodeJSON struct {
	ID         int              `json:"id"`
	Type       string           `json:"type"`
	Table      string           `json:"table,omitempty"`
	Index      string           `json:"index,omitempty"`
	Cost       float64          `json:"cost,omitempty"`
	Rows       int64            `json:"rows,omitempty"`
	Detail     string           `json:"detail,omitempty"`
	Children   []*planNodeJSON  `json:"children,omitempty"`
	Analyze    *analyzeStatsJSON `json:"analyze,omitempty"`
	Bottleneck *bottleneckJSON   `json:"bottleneck,omitempty"`
}
