package EX

import (
	"fmt"
	"strings"

	RE "github.com/cyw0ng95/razordata/internal/SQL/RE"
)

// PlanNode represents a node in the query plan tree for EXPLAIN output.
// It mirrors the Operator tree but captures descriptive metadata for
// human-readable rendering.
type PlanNode struct {
	Type     string      // "SeqScan", "IndexScan", "Filter", etc.
	Table    string      // for scan nodes
	Index    string      // for index nodes
	Cost     float64     // estimated cost
	Rows     int64       // estimated row count
	Width    int         // avg row width (bytes)
	Detail   string      // extra info (filter expr, order by, etc.)
	Children []*PlanNode // child nodes
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
		node.Cost = estimateFilterCost(v)

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
		node.Cost = estimateSortCost(v)

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
		node.Cost = estimateDistinctCost(v)

	case *Aggregate:
		node.Detail = "GROUP BY"
		node.Cost = estimateAggregateCost(v)

	case *NestedLoopJoin:
		node.Detail = fmt.Sprintf("JOIN %s", v.rightTbl)
		node.Cost = estimateJoinCost(v)

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
func formatPlanTree(n *PlanNode) []Row {
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

		detail := node.Detail
		if detail == "" {
			detail = node.Type
		}
		if node.Table != "" && !strings.Contains(detail, node.Table) {
			detail += " " + node.Table
		}
		if node.Index != "" {
			detail += " USING INDEX " + node.Index
		}

		rows = append(rows, Row{
			Cols:  []string{"id", "parent", "notused", "detail"},
			Types: []int{1, 1, 1, 1},
			Data:  []any{int64(id), int64(parent), int64(0), detail},
		})

		for _, child := range node.Children {
			walk(child, id)
		}
	}

	walk(n, 0)
	return rows
}

// Cost estimation helpers

func estimateFilterCost(f *Filter) float64 {
	if f.child == nil {
		return 1.0
	}
	// Filters typically reduce rows; use 0.5 as default selectivity
	return 0.5
}

func estimateProjectCost(p *Project) float64 {
	if p.child == nil {
		return 1.0
	}
	return 1.0 // Projection is cheap
}

func estimateSortCost(s *Sort) float64 {
	if s.child == nil {
		return 1.0
	}
	// Sort is O(n log n)
	return 10.0
}

func estimateLimitCost(l *Limit) float64 {
	if l.child == nil {
		return 1.0
	}
	return 1.0 // Limit is cheap
}

func estimateOffsetCost(o *Offset) float64 {
	if o.child == nil {
		return 1.0
	}
	return 1.0 // Offset is cheap
}

func estimateDistinctCost(d *Distinct) float64 {
	if d.child == nil {
		return 1.0
	}
	return 2.0 // Distinct requires deduplication
}

func estimateAggregateCost(a *Aggregate) float64 {
	if a.child == nil {
		return 1.0
	}
	return 5.0 // Aggregation is moderately expensive
}

func estimateJoinCost(j *NestedLoopJoin) float64 {
	leftCost := 1.0
	rightCost := 1.0
	if j.left != nil {
		leftCost = 1.0
	}
	if j.right != nil {
		rightCost = 1.0
	}
	return leftCost * rightCost
}
