// Package EX's explain.go renders an operator tree as a human-readable
// description. EXPLAIN is not part of the v1 MVP scope per
// design/ARCH.md but the implementation is retained in v1.1 because
// it is the primary debugging surface for the executor.
package EX

import (
	"context"
	"fmt"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// ExplainStmtOp is an operator that produces EXPLAIN output.
// It wraps a planned inner statement and renders its plan tree.
type ExplainStmtOp struct {
	mode     PS.ExplainMode
	planNode *PlanNode
	root     Operator
	rows     []Row
	pos      int
	done     bool
}

func (e *ExplainStmtOp) Next(ctx context.Context) (Row, error) {
	if e.done {
		return Row{}, ErrNoRows
	}

	if e.rows == nil {
		// Generate rows based on mode
		switch e.mode {
		case PS.ExplainQueryPlan:
			e.rows = formatPlanTree(e.planNode)
		default:
			// EXPLAIN (normal mode) - return operator descriptions
			e.rows = formatExplainNormal(e.planNode)
		}
	}

	if e.pos >= len(e.rows) {
		e.done = true
		return Row{}, ErrNoRows
	}

	row := e.rows[e.pos]
	e.pos++
	return row, nil
}

func (e *ExplainStmtOp) Close() error {
	e.rows = nil
	e.pos = 0
	e.done = false
	return nil
}

// formatExplainNormal renders the plan tree in EXPLAIN (non-QUERY PLAN) mode.
// This returns a simple list of operator descriptions.
func formatExplainNormal(n *PlanNode) []Row {
	if n == nil {
		return nil
	}

	var rows []Row
	var walk func(node *PlanNode, depth int)
	walk = func(node *PlanNode, depth int) {
		if node == nil {
			return
		}

		detail := node.Type
		if node.Table != "" {
			detail += " " + node.Table
		}
		if node.Index != "" {
			detail += " USING INDEX " + node.Index
		}
		if node.Detail != "" && node.Detail != detail {
			detail += " (" + node.Detail + ")"
		}

		rows = append(rows, Row{
			Cols:  []string{"id", "parent", "notused", "detail"},
			Types: []int{1, 1, 1, 1},
			Data:  []interface{}{int64(depth + 1), int64(depth), int64(0), detail},
		})

		for _, child := range node.Children {
			walk(child, depth+1)
		}
	}

	walk(n, 0)
	return rows
}

func explainOperator(op Operator, depth int) string {
	var b strings.Builder
	b.WriteString(strings.Repeat("  ", depth))
	b.WriteString(describeOp(op))
	if c, ok := op.(interface{ Child() Operator }); ok {
		child := c.Child()
		if child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(child, depth+1))
		}
		return b.String()
	}
	switch v := op.(type) {
	case *Filter:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *Project:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *Aggregate:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *Sort:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *Limit:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *Distinct:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *NestedLoopJoin:
		if v.left != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.left, depth+1))
		}
		if v.right != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.right, depth+1))
		}
	case *Update:
		if v.iter != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.iter, depth+1))
		}
	case *Delete:
		if v.iter != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.iter, depth+1))
		}
	}
	return b.String()
}

func describeOp(op Operator) string {
	switch v := op.(type) {
	case *SeqScan:
		return fmt.Sprintf("SeqScan(table=%s)", v.table)
	case *IndexScan:
		return fmt.Sprintf("IndexScan(table=%s idx=%s)", v.table, v.idx)
	case *NestedLoopJoin:
		return fmt.Sprintf("NestedLoopJoin(left=%s right=%s)", v.leftTbl, v.rightTbl)
	case *Filter:
		return "Filter"
	case *Project:
		return "Project"
	case *Sort:
		return "Sort"
	case *Limit:
		return "Limit"
	case *Distinct:
		return "Distinct"
	case *Aggregate:
		return "Aggregate"
	case *Insert:
		return fmt.Sprintf("Insert(table=%s rows=%d)", v.table, len(v.values))
	case *Update:
		return fmt.Sprintf("Update(table=%s)", v.table)
	case *Delete:
		return fmt.Sprintf("Delete(table=%s)", v.table)
	case *CreateTable:
		return fmt.Sprintf("CreateTable(name=%s)", v.stmt.Name)
	case *DropTable:
		return fmt.Sprintf("DropTable(name=%s)", v.stmt.Name)
	}
	return "Unknown"
}
