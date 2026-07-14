package CO

import (
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// EstimatePredicateSelectivity returns the selectivity of a predicate
// expression, using column histograms when available.
// REQ000085, REQ001439.
//
// Dependencies are injected via callbacks:
//   - findTableForColumn resolves a column name to a table name.
//   - statsCatalog provides per-column statistics.
//   - estimateWithStats estimates selectivity from histogram data.
func EstimatePredicateSelectivity(
	e PS.Expr,
	findTableForColumn func(string) string,
	statsCatalog PL.StatsCatalog,
	estimateWithStats func(PS.Expr, *ls.ColumnStats) float64,
) float64 {
	if e == nil {
		return 1.0
	}

	col, _, isColLit := ExtractColumnLiteralExpr(e)
	if !isColLit {
		return 0.5
	}

	tableName := findTableForColumn(col)
	if tableName == "" || statsCatalog == nil {
		return 0.5
	}

	stats := statsCatalog.ColumnStatsByName(tableName, col)
	if stats == nil {
		return 0.5
	}

	histSel := estimateWithStats(e, stats)

	lm := PL.Learned()
	if lm.IsTrained() {
		predType := 0
		if v, ok := e.(*PS.BinaryExpr); ok {
			switch v.Op {
			case LX.T_EQ:
				predType = 0
			case LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
				predType = 1
			}
		}
		features := lm.PredicateFeatures(
			predType,
			histSel,
			float64(stats.RowCount),
			float64(stats.DistinctCount),
			float64(stats.NullCount),
		)
		learnedSel := lm.Predict(features)
		alpha := float64(lm.TrainingCount()) / float64(lm.TrainingCount()+100)
		if alpha > 0.8 {
			alpha = 0.8
		}
		return alpha*learnedSel + (1-alpha)*histSel
	}

	return histSel
}
