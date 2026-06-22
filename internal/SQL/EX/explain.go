// Package EX's explain.go renders an operator tree as a human-readable
// description. EXPLAIN is not part of the v1 MVP scope per
// docs/design/ARCH.md but the implementation is retained in v1.1 because
// it is the primary debugging surface for the executor.
package EX

import (
	"context"
	"fmt"
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
		if e.mode == PS.ExplainAnalyze {
			// REQ000783: execute and collect runtime stats.
			if err := executeAndCollectStats(ctx, e.root, e.planNode); err != nil {
				return Row{}, err
			}
			// REQ000788: analyze for bottlenecks after execution.
			AnalyzePlanForBottlenecks(e.planNode)
		}
		e.rows = formatPlanTree(e.planNode, e.mode)
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

// explainOperator produces a human-readable string representation
// of an operator tree. Used by Executor.Explain() for debugging.
// This is a simplified version that doesn't produce the structured
// EXPLAIN output but provides a quick text dump.
func explainOperator(op Operator, depth int) string {
	var b strings.Builder
	b.WriteString(strings.Repeat("  ", depth))

	// Unwrap AdaptiveOp to show inner operator.
	if aop, ok := op.(*AdaptiveOp); ok {
		return explainOperator(aop.inner, depth)
	}

	// Get operator type and details
	var detail string
	switch v := op.(type) {
	case *SeqScan:
		detail = fmt.Sprintf("SeqScan(table=%s)", v.table)
	case *IndexScan:
		detail = fmt.Sprintf("IndexScan(table=%s idx=%s)", v.table, v.idx)
	case *NestedLoopJoin:
		detail = fmt.Sprintf("NestedLoopJoin(left=%s right=%s)", v.leftTbl, v.rightTbl)
	case *Filter:
		detail = "Filter"
	case *Project:
		detail = "Project"
	case *Sort:
		detail = "Sort"
	case *Limit:
		detail = "Limit"
	case *Distinct:
		detail = "Distinct"
	case *Aggregate:
		detail = "Aggregate"
	case *HashJoin:
		detail = "HashJoin"
	case *Insert:
		detail = fmt.Sprintf("Insert(table=%s rows=%d)", v.table, len(v.values))
	case *Update:
		detail = fmt.Sprintf("Update(table=%s)", v.table)
	case *Delete:
		detail = fmt.Sprintf("Delete(table=%s)", v.table)
	case *ValuesRows:
		detail = fmt.Sprintf("ValuesRows(%d rows)", len(v.rows))
	default:
		detail = operatorType(op)
	}

	b.WriteString(detail)

	// Recursively append children
	if c, ok := op.(interface{ Child() Operator }); ok {
		child := c.Child()
		if child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(child, depth+1))
		}
		return b.String()
	}

	// Handle multi-child operators
	switch v := op.(type) {
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
	case *HashJoin:
		if v.LeftChild() != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.LeftChild(), depth+1))
		}
		if v.RightChild() != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.RightChild(), depth+1))
		}
	}

	return b.String()
}
