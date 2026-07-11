// Package SL owns predicate → row-count estimation. Three formula
// families: equality (1/NDV), range ((1-null_frac)/3), IN-list with
// MCVs, OR-chain. REQ001473 / REQ001474.
package SL

import (
	"bytes"
	"fmt"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// StatsCatalog provides table and column statistics for selectivity
// estimation. Backed by LS.ColumnStats.
type StatsCatalog interface {
	TableStats(table string) *TableStats
	ColumnStats(table, col string) *ColumnStats
	Invalidate(schemaVersion uint64)
}

// TableStats holds per-table statistics.
type TableStats struct {
	RowCount int64
	NullFrac float64
}

// ColumnStats holds per-column statistics.
type ColumnStats struct {
	NDV      int64
	NullFrac float64
	MinMax   []byte
}

// EstimateSelectivity returns a simple selectivity estimate for a
// WHERE predicate. Uses uniform-distribution defaults.
func EstimateSelectivity(e PS.Expr) float64 {
	if e == nil {
		return 1.0
	}
	if v, ok := e.(*PS.BinaryExpr); ok {
		if isColumnLiteralPair(v.Left, v.Right) || isColumnLiteralPair(v.Right, v.Left) {
			switch v.Op {
			case LX.T_EQ:
				return 0.1
			}
		}
	}
	return 0.5
}

// EstimateSelectivityWithStats computes selectivity using column
// histograms when available, falling back to uniform distribution.
func EstimateSelectivityWithStats(e PS.Expr, stats *ls.ColumnStats) float64 {
	if e == nil {
		return 1.0
	}
	if v, ok := e.(*PS.BinaryExpr); ok {
		_, lit, isColLit := extractColumnLiteral(v)
		if isColLit && stats != nil {
			switch v.Op {
			case LX.T_EQ:
				return EstimateEqSelectivity(stats, lit)
			case LX.T_LT, LX.T_LE:
				return EstimateRangeSelectivity(stats, nil, lit, false)
			case LX.T_GT, LX.T_GE:
				return EstimateRangeSelectivity(stats, lit, nil, false)
			}
		}
		return 0.5
	}
	return 0.5
}

// EstimateEqSelectivity returns selectivity for column = literal.
func EstimateEqSelectivity(stats *ls.ColumnStats, lit []byte) float64 {
	if stats == nil {
		return 0.1
	}
	if stats.RowCount == 0 {
		return 0.1
	}
	if len(stats.Histogram) > 0 {
		matched := int64(0)
		for _, b := range stats.Histogram {
			if bytes.Compare(lit, b.LowerBound) >= 0 && bytes.Compare(lit, b.UpperBound) <= 0 {
				matched = b.Count
				break
			}
		}
		if matched > 0 {
			return float64(matched) / float64(stats.RowCount)
		}
		return 0.0
	}
	if stats.DistinctCount > 0 {
		return 1.0 / float64(stats.DistinctCount)
	}
	return 0.1
}

// EstimateRangeSelectivity returns selectivity for a range predicate
// [low, high]. If low is nil, range is (-inf, high]. If high is nil,
// range is [low, +inf).
func EstimateRangeSelectivity(stats *ls.ColumnStats, low, high []byte, inclusive bool) float64 {
	if stats == nil || stats.RowCount == 0 {
		return 0.3
	}
	if len(stats.Histogram) == 0 {
		if stats.DistinctCount <= 1 {
			return 1.0
		}
		return 0.33
	}
	totalRows := stats.RowCount
	lowRows := int64(0)
	highRows := int64(0)
	for _, b := range stats.Histogram {
		if low != nil && bytes.Compare(b.UpperBound, low) < 0 {
			lowRows += b.Count
		}
		if high != nil && bytes.Compare(b.LowerBound, high) <= 0 {
			highRows += b.Count
		}
	}
	if low == nil {
		return float64(highRows) / float64(totalRows)
	}
	if high == nil {
		return float64(totalRows-lowRows) / float64(totalRows)
	}
	sel := float64(highRows-lowRows) / float64(totalRows)
	if sel < 0 {
		sel = 0
	}
	return sel
}

// EstimateJoinPredicateSelectivity returns the selectivity of a single
// join predicate expression. rowCount is the estimated number of rows
// in the table the predicate applies to; used for IN-list selectivity
// scaling. Pass 0 to use the default NDV of 100. REQ000819.
func EstimateJoinPredicateSelectivity(pred PS.Expr, rowCount float64) float64 {
	if pred == nil {
		return 1.0
	}
	if in, ok := pred.(*PS.InExpr); ok && len(in.List) > 0 {
		return EstimateInListSelectivity(in.List, rowCount, nil, nil)
	}
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok {
		return 0.5
	}
	isColCol := IsColumnColumnPair(bin.Left, bin.Right) || IsColumnColumnPair(bin.Right, bin.Left)
	switch bin.Op {
	case LX.T_EQ:
		_ = isColCol // for future NDV-based selectivity
		return 0.1
	case LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
		return 0.3
	default:
		return 0.5
	}
}

// EstimateInListSelectivity estimates selectivity of an IN-list with MCVs.
func EstimateInListSelectivity(list []PS.Expr, rowCount float64, mcvs [][]byte, freqs []float64) float64 {
	if len(list) == 0 {
		return 1.0
	}
	if len(mcvs) > 0 && len(mcvs) == len(freqs) {
		freqByVal := make(map[string]float64, len(mcvs))
		for i, v := range mcvs {
			freqByVal[string(v)] = freqs[i]
		}
		var matched int
		var probNotMatched float64 = 1.0
		var tailCount int
		for _, item := range list {
			key, ok := InListLiteralKey(item)
			if !ok {
				tailCount++
				continue
			}
			if f, hit := freqByVal[key]; hit {
				probNotMatched *= (1.0 - f)
				matched++
			} else {
				tailCount++
			}
		}
		var tailSel float64
		if tailCount > 0 {
			ndv := rowCount
			if ndv <= 0 {
				ndv = 100
			}
			rareN := ndv - float64(len(mcvs))
			if rareN < 1 {
				rareN = 1
			}
			tailSel = float64(tailCount) / rareN
			if tailSel > 1.0 {
				tailSel = 1.0
			}
		}
		sel := (1.0 - probNotMatched) + tailSel
		if sel > 1.0 {
			sel = 1.0
		}
		if sel < 0 {
			sel = 0
		}
		return sel
	}
	ndv := rowCount
	if ndv <= 0 {
		ndv = 100
	}
	sel := float64(len(list)) / ndv
	if sel > 1.0 {
		sel = 1.0
	}
	return sel
}

func InListLiteralKey(item PS.Expr) (string, bool) {
	switch v := item.(type) {
	case *PS.NumberLiteral:
		return fmt.Sprintf("I:%d", v.Val), true
	case *PS.FloatLiteral:
		return fmt.Sprintf("F:%v", v.Val), true
	case *PS.StringLiteral:
		return "S:" + v.Val, true
	case *PS.BoolLiteral:
		return fmt.Sprintf("B:%v", v.Val), true
	case *PS.NullLiteral:
		return "null", true
	}
	return "", false
}

// IsColumnColumnPair returns true when both sides of the expression
// are column references (Ident or QualifiedName). Used to detect
// equi-join predicates like t1.a = t2.b.
func IsColumnColumnPair(a, b PS.Expr) bool {
	_, aIsCol := a.(*PS.Ident)
	_, aIsQn := a.(*PS.QualifiedName)
	_, bIsCol := b.(*PS.Ident)
	_, bIsQn := b.(*PS.QualifiedName)
	return (aIsCol || aIsQn) && (bIsCol || bIsQn)
}

func isColumnColumnPair(a, b PS.Expr) bool {
	return IsColumnColumnPair(a, b)
}

func isColumnLiteralPair(a, b PS.Expr) bool {
	if _, ok := a.(*PS.Ident); !ok {
		return false
	}
	switch b.(type) {
	case *PS.NumberLiteral, *PS.StringLiteral, *PS.BoolLiteral, *PS.NullLiteral:
		return true
	}
	return false
}

// ExtractColumnLiteral extracts the column name and literal bytes from a
// binary expression of the form `column OP literal`. Returns ("", nil, false)
// if the expression does not match this pattern. REQ001494.
func ExtractColumnLiteral(v *PS.BinaryExpr) (col string, lit []byte, ok bool) {
	if v == nil {
		return "", nil, false
	}
	if col, lit, ok = oneSide(v.Left, v.Right); ok {
		return
	}
	return oneSide(v.Right, v.Left)
}

func extractColumnLiteral(v *PS.BinaryExpr) (col string, lit []byte, ok bool) {
	return ExtractColumnLiteral(v)
}

func oneSide(a, b PS.Expr) (string, []byte, bool) {
	var col string
	switch c := a.(type) {
	case *PS.Ident:
		col = c.Name
	case *PS.QualifiedName:
		col = c.Name
	default:
		return "", nil, false
	}
	switch l := b.(type) {
	case *PS.NumberLiteral:
		return col, []byte(fmt.Sprintf("%d", l.Val)), true
	case *PS.FloatLiteral:
		return col, []byte(fmt.Sprintf("%f", l.Val)), true
	case *PS.StringLiteral:
		return col, []byte(l.Val), true
	}
	return "", nil, false
}