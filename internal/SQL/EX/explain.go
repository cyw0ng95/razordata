// Package EX's explain.go renders an operator tree as a human-readable
// description. EXPLAIN is not part of the v1 MVP scope per
// docs/design/ARCH.md but the implementation is retained in v1.1 because
// it is the primary debugging surface for the executor.
package EX

import (
	"context"
	"strconv"
	"strings"
	"time"

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
		switch e.mode {
		case PS.ExplainQueryPlan:
			e.rows = formatPlanTree(e.planNode)
		case PS.ExplainAnalyze:
			// REQ000783: execute and collect runtime stats.
			if err := executeAndCollectStats(ctx, e.root, e.planNode); err != nil {
				return Row{}, err
			}
			e.rows = formatPlanTree(e.planNode)
		default:
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

// executeAndCollectStats drains the operator tree, collects total stats,
// and closes the inner plan to reset iterator state (memo may reuse it).
func executeAndCollectStats(ctx context.Context, root Operator, pn *PlanNode) error {
	start := time.Now()
	var rows int64
	for {
		_, err := root.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return err
		}
		rows++
	}
	root.Close()
	elapsed := time.Since(start)
	if pn.Analyze == nil {
		pn.Analyze = &AnalyzeStats{}
	}
	pn.Analyze.RowsReturned = rows
	pn.Analyze.TimeNS = elapsed.Nanoseconds()
	return nil
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
			Data:  []any{int64(depth + 1), int64(depth), int64(0), detail},
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
	// Unwrap AdaptiveOp to show inner operator.
	if aop, ok := op.(*AdaptiveOp); ok {
		return explainOperator(aop.inner, depth)
	}
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
	if aop, ok := op.(*AdaptiveOp); ok {
		return describeOp(aop.inner)
	}
	var b strings.Builder
	switch v := op.(type) {
	case *SeqScan:
		b.WriteString("SeqScan(table=")
		b.WriteString(v.table)
		b.WriteByte(')')
	case *IndexScan:
		b.WriteString("IndexScan(table=")
		b.WriteString(v.table)
		b.WriteString(" idx=")
		b.WriteString(v.idx)
		b.WriteByte(')')
	case *NestedLoopJoin:
		b.WriteString("NestedLoopJoin(left=")
		b.WriteString(v.leftTbl)
		b.WriteString(" right=")
		b.WriteString(v.rightTbl)
		b.WriteByte(')')
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
		b.WriteString("Insert(table=")
		b.WriteString(v.table)
		b.WriteString(" rows=")
		b.WriteString(strconv.Itoa(len(v.values)))
		b.WriteByte(')')
	case *Update:
		b.WriteString("Update(table=")
		b.WriteString(v.table)
		b.WriteByte(')')
	case *Delete:
		b.WriteString("Delete(table=")
		b.WriteString(v.table)
		b.WriteByte(')')
	case *CreateTable:
		b.WriteString("CreateTable(name=")
		b.WriteString(v.stmt.Name)
		b.WriteByte(')')
	case *DropTable:
		b.WriteString("DropTable(name=")
		b.WriteString(v.stmt.Name)
		b.WriteByte(')')
	case *CreateIndex:
		b.WriteString("CreateIndex(name=")
		b.WriteString(v.stmt.Name)
		b.WriteByte(')')
	case *DropIndex:
		b.WriteString("DropIndex(name=")
		b.WriteString(v.stmt.Name)
		b.WriteByte(')')
	case *CreateViewOperator:
		b.WriteString("CreateView(name=")
		b.WriteString(v.stmt.Name)
		b.WriteByte(')')
	case *DropView:
		b.WriteString("DropView(name=")
		b.WriteString(v.stmt.Name)
		b.WriteByte(')')
	case *DropTrigger:
		b.WriteString("DropTrigger(name=")
		b.WriteString(v.stmt.Name)
		b.WriteByte(')')
	case *AlterTable:
		b.WriteString("AlterTable")
	case *Pragma:
		b.WriteString("Pragma")
	case *Analyze:
		b.WriteString("Analyze")
	case *Vacuum:
		b.WriteString("Vacuum")
	case *Truncate:
		b.WriteString("Truncate")
	case *Reindex:
		b.WriteString("Reindex")
	case *HashJoin:
		b.WriteString("HashJoin")
	case *WindowOperator:
		b.WriteString("Window")
	case *CompoundOp:
		b.WriteString("Compound")
	case *ValuesRows:
		b.WriteString("ValuesRows")
	case *ExplainStmtOp:
		b.WriteString("Explain")
	case *Noop:
		b.WriteString("Noop")
	default:
		return "Unknown"
	}
	return b.String()
}
