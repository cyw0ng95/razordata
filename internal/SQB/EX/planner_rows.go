package EX

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// estimateRowCount returns a row count estimate for the given
// table + predicate. The old histogram-based cost model was
// removed with the optimizer redesign; this returns the
// registered in-memory row count when available, otherwise the
// catalog stats, with a conservative default of 100.
func (p *Planner) estimateRowCount(table string, _ PS.Expr) int {
	if rows, ok := DT.Tables[table]; ok && len(rows) > 0 {
		return len(rows)
	}
	if cat := DT.Catalog(); cat != nil {
		if ts := cat.TableStats(table); ts != nil && ts.RowCount > 0 {
			return int(ts.RowCount)
		}
	}
	return 100
}

// getTableRowCount returns the row count estimate as a float64.
func (p *Planner) getTableRowCount(table string) float64 {
	return float64(p.estimateRowCount(table, nil))
}
