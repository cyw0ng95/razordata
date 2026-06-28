// Package EX's explain.go renders an operator tree as a human-readable
// description. EXPLAIN is not part of the v1 MVP scope per
// docs/design/ARCH.md but the implementation is retained in v1.1 because
// it is the primary debugging surface for the executor.
package EX

import (
	"context"
	"time"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// ExplainStmtOp is an operator that produces EXPLAIN output.
// It wraps a planned inner statement and renders its plan tree.
type ExplainStmtOp struct {
	mode   PS.ExplainMode
	format PS.ExplainFormat
	planNode *PlanNode
	root     Operator
	rows     []Row
	treeText string // cached tree/DOT/JSON output
	pos      int
	done     bool
}

func (e *ExplainStmtOp) Next(ctx context.Context) (Row, error) {
	if e.done {
		return Row{}, ErrNoRows
	}

	if e.rows == nil && e.treeText == "" {
		if e.mode == PS.ExplainAnalyze {
			// REQ000783: execute and collect runtime stats.
			if err := executeAndCollectStats(ctx, e.root, e.planNode); err != nil {
				return Row{}, err
			}
			// REQ000788: analyze for bottlenecks after execution.
			AnalyzePlanForBottlenecks(e.planNode)
		}

		// Handle different output formats
		switch e.format {
		case PS.ExplainFormatText:
			e.rows = formatPlanTree(e.planNode, e.mode)
		case PS.ExplainFormatTree:
			e.treeText = e.planNode.ToTree()
		case PS.ExplainFormatJSON:
			e.treeText = e.planNode.ToJSON()
		case PS.ExplainFormatDOT:
			e.treeText = e.planNode.ToDOT()
		}
	}

	// For tree/json/dot formats, return single row with formatted output
	if e.treeText != "" {
		row := Row{
			Cols:  []string{"explain_output"},
			Types: []LX.TokenType{LX.T_TEXT},
			Data:  []Value{NewTextValue(e.treeText)},
		}
		e.done = true
		return row, nil
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
	e.treeText = ""
	e.pos = 0
	e.done = false
	return nil
}


