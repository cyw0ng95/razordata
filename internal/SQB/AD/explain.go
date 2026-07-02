package AD

import (
	"context"
	"time"

	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// ExplainStmtOp is an operator that produces EXPLAIN output.
// It wraps a planned inner statement and renders its plan tree.
type ExplainStmtOp struct {
	Mode     PS.ExplainMode
	Format   PS.ExplainFormat
	PlanNode *PlanNode
	Root     DT.Operator
	rows     []DT.Row
	treeText string
	pos      int
	done     bool
}

func (e *ExplainStmtOp) Next(ctx context.Context) (DT.Row, error) {
	if e.done {
		return DT.Row{}, DT.ErrNoRows
	}

	if e.rows == nil && e.treeText == "" {
		if e.Mode == PS.ExplainAnalyze {
			// REQ000783: execute and collect runtime stats.
			if err := executeAndCollectStats(ctx, e.Root, e.PlanNode); err != nil {
				return DT.Row{}, err
			}
			// REQ000788: analyze for bottlenecks after execution.
			AnalyzePlanForBottlenecks(e.PlanNode)
		}

		// Handle different output formats
		switch e.Format {
		case PS.ExplainFormatText:
			e.rows = FormatPlanTree(e.PlanNode, e.Mode)
		case PS.ExplainFormatTree:
			e.treeText = e.PlanNode.ToTree()
		case PS.ExplainFormatJSON:
			e.treeText = e.PlanNode.ToJSON()
		case PS.ExplainFormatDOT:
			e.treeText = e.PlanNode.ToDOT()
		}
	}

	// For tree/json/dot formats, return single row with formatted output
	if e.treeText != "" {
		row := DT.Row{
			Cols:  []string{"explain_output"},
			Types: []LX.TokenType{LX.T_TEXT},
			Data:  []DT.Value{AP.NewTextValue(e.treeText)},
		}
		e.done = true
		return row, nil
	}

	if e.pos >= len(e.rows) {
		e.done = true
		return DT.Row{}, DT.ErrNoRows
	}

	row := e.rows[e.pos]
	e.pos++
	return row, nil
}

// executeAndCollectStats drains the operator tree, collects total stats,
// and closes the inner plan to reset iterator state (memo may reuse it).
func executeAndCollectStats(ctx context.Context, root DT.Operator, pn *PlanNode) error {
	start := time.Now()
	var rows int64
	for {
		_, err := root.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
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