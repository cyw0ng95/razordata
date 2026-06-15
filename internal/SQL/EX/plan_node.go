package EX

import (
	"fmt"
	"strings"
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

	node := &PlanNode{
		Type: operatorType(op),
	}

	switch v := op.(type) {
	case *SeqScan:
		node.Table = v.table
		node.Cost = 1.0
		node.Rows = int64(planner.estimateRowCount(v.table, nil))
		node.Width = 100 // default estimate

	case *IndexScan:
		node.Table = v.table
		node.Index = v.idx
		node.Cost = 0.1
		node.Detail = fmt.Sprintf("idx=%s", v.idx)

	case *Filter:
		node.Detail = "WHERE"
		if v.predicate != nil {
			node.Detail = fmt.Sprintf("WHERE %s", v.predicate)
		}
		node.Cost = estimateFilterCost(v)

	case *Project:
		node.Detail = "SELECT"
		node.Cost = estimateProjectCost(v)

	case *Sort:
		node.Detail = "ORDER BY"
		node.Cost = estimateSortCost(v)

	case *Limit:
		node.Detail = "LIMIT"
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
	}

	return node
}

// operatorType returns a human-readable type name for an operator.
func operatorType(op Operator) string {
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
	var walk func(node *PlanNode, id, parent int)
	walk = func(node *PlanNode, id, parent int) {
		if node == nil {
			return
		}

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
			Data:  []interface{}{int64(id), int64(parent), int64(0), detail},
		})

		currentID := id
		for _, child := range node.Children {
			currentID++
			childID := currentID
			walk(child, childID, id)
			// Update currentID to account for all descendants
			currentID = countNodes(child) + childID - 1
		}
	}

	walk(n, 1, 0)
	return rows
}

// countNodes returns the total number of nodes in the tree rooted at n.
func countNodes(n *PlanNode) int {
	if n == nil {
		return 0
	}
	count := 1
	for _, child := range n.Children {
		count += countNodes(child)
	}
	return count
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
