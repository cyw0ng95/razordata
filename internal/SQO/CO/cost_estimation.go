package CO

import (
	"bytes"
	"fmt"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// EstimateSelectivity returns a simple heuristic selectivity for a
// predicate expression.
// REQ000085, REQ001440.
func EstimateSelectivity(e PS.Expr) float64 {
	if e == nil {
		return 1.0
	}
	if v, ok := e.(*PS.BinaryExpr); ok {
		if IsColumnLiteralPair(v.Left, v.Right) || IsColumnLiteralPair(v.Right, v.Left) {
			switch v.Op {
			case LX.T_EQ:
				return 0.1
			}
		}
	}
	return 0.5
}

// EstimateSelectivityWithStats computes selectivity using column
// histograms when available.
// REQ000085, REQ001440.
func EstimateSelectivityWithStats(e PS.Expr, stats *ls.ColumnStats) float64 {
	if e == nil {
		return 1.0
	}
	if v, ok := e.(*PS.BinaryExpr); ok {
		_, lit, isColLit := ExtractColumnLiteral(v)
		if isColLit && stats != nil {
			switch v.Op {
			case LX.T_EQ:
				return EstimateEqSelectivity(stats, lit)
			case LX.T_LT, LX.T_LE:
				return EstimateRangeSelectivity(stats, nil, lit)
			case LX.T_GT, LX.T_GE:
				return EstimateRangeSelectivity(stats, lit, nil)
			}
		}
		return 0.5
	}
	return 0.5
}

// EstimateEqSelectivity returns selectivity for column = literal.
// REQ000085, REQ001440.
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

// EstimateRangeSelectivity returns selectivity for a range predicate.
// REQ000085, REQ001440.
func EstimateRangeSelectivity(stats *ls.ColumnStats, low, high []byte) float64 {
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

// IsColumnColumnPair returns true when both arguments are column
// references (Ident or QualifiedName).
// REQ001440.
func IsColumnColumnPair(a, b PS.Expr) bool {
	_, aIsCol := a.(*PS.Ident)
	_, aIsQn := a.(*PS.QualifiedName)
	_, bIsCol := b.(*PS.Ident)
	_, bIsQn := b.(*PS.QualifiedName)
	return (aIsCol || aIsQn) && (bIsCol || bIsQn)
}

// EstimateJoinPredicateSelectivity returns the selectivity of a
// single join predicate.
// REQ000819, REQ001440.
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
		if isColCol {
			return 0.1
		}
		return 0.1
	case LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
		return 0.3
	default:
		return 0.5
	}
}

// InListLiteralKey returns a stable string key for a literal
// expression, or false for non-literals.
// REQ001440.
func InListLiteralKey(item PS.Expr) (string, bool) {
	switch v := item.(type) {
	case *PS.StringLiteral:
		return "S:" + v.Val, true
	case *PS.NumberLiteral:
		return fmt.Sprintf("I:%d", v.Val), true
	case *PS.FloatLiteral:
		return fmt.Sprintf("F:%v", v.Val), true
	case *PS.BoolLiteral:
		return fmt.Sprintf("B:%v", v.Val), true
	}
	return "", false
}

// EstimateInListSelectivity computes selectivity for an IN-list
// using MCV data when available.
// REQ000819, REQ001440.
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
		_ = matched
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
