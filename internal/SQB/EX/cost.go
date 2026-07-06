package EX

import (
	"bytes"
	"fmt"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQB/AD"
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func (p *Planner) estimateCostLegacy(op DT.Operator) float64 {
	if op == nil {
		return 0
	}
	// Unwrap AdaptiveOp to estimate cost of the inner operator.
	if aop, ok := op.(*AD.AdaptiveOp); ok {
		return p.estimateCostLegacy(aop.Inner)
	}
	switch v := op.(type) {
	case *OP.SeqScan:
		// In v1 we don't track row counts; assume 1.0 per row.
		return 1.0
	case *OP.IndexScan:
		// REQ000156 (iter-27): the cost depends on the scan
		// mode. Real index seek (indexMode=true) is the
		// cheapest; range seek is slightly more expensive;
		// full prefix read is the most expensive of the
		// index paths but still cheaper than OP.SeqScan.
		if v.IndexMode() {
			return 0.05
		}
		return 0.1
	case *OP.BitmapHeapScan:
		// REQ001106: bitmap heap scan cost = sum of child
		// index seek costs + a single heap-fanout pass. We
		// model each child as a real seek (0.05) and add a
		// fixed bookkeeping factor so a 2-child bitmap is
		// cheaper than 2 separate IndexScans+OP.Filter stacks.
		return 0.05*float64(len(v.IndexScans())) + 0.05
	case *OP.IndexOnlyScan:
		// REQ001107: index-only scan is the cheapest path —
		// no heap fetch, just index entry emission.
		return 0.03
	case *OP.Filter:
		return p.estimateCostLegacy(v.Child()) * p.estimatePredicateSelectivity(v.Predicate())
	case *OP.Project:
		return p.estimateCostLegacy(v.Child())
	case *OP.Limit:
		return p.estimateCostLegacy(v.Child())
	case *OP.Offset:
		return p.estimateCostLegacy(v.Child())
	case *OP.Distinct:
		return p.estimateCostLegacy(v.Child())
	case *OP.Sort:
		childCost := p.estimateCostLegacy(v.Child())
		if childCost < 1 {
			childCost = 1
		}
		return childCost * (1 + log2ish(childCost))
	case *AG.Aggregate:
		return p.estimateCostLegacy(v.Child()) + 1
	case *OP.NestedLoopJoin:
		leftCost := p.estimateCostLegacy(v.LeftChild())
		rightCost := p.estimateCostLegacy(v.RightChild())
		return leftCost * rightCost
	case *OP.HashJoin:
		leftCost := p.estimateCostLegacy(v.LeftChild())
		rightCost := p.estimateCostLegacy(v.RightChild())
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost + rightCost
	case *OP.HashCrossJoin:
		leftCost := p.estimateCostLegacy(v.LeftChild())
		rightCost := p.estimateCostLegacy(v.RightChild())
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost + rightCost
	case *OP.MergeJoin:
		// REQ001102: MergeJoin is O(N+M) on pre-sorted inputs. Cost
		// is dominated by the children plus a small merge overhead.
		leftCost := p.estimateCostLegacy(v.LeftChild())
		rightCost := p.estimateCostLegacy(v.RightChild())
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost + rightCost + 1
	case *WT.Insert, *WT.Update, *WT.Delete, *WT.CreateTable, *WT.DropTable:
		// Writer operators: cost ~ 1 (single mutation).
		return 1.0
	default:
		return 1.0
	}
}

// estimateCostWithParams applies the PostgreSQL-style cost formulas
// driven by the supplied CostParams. REQ001104: row counts are
// estimated via estimateRowCount; CPU/IO costs use the supplied
// coefficients; memory pressure is exposed via estimateMemoryPressure.
func (p *Planner) estimateCostWithParams(op DT.Operator, cp CostParams) float64 {
	if op == nil {
		return 0
	}
	if aop, ok := op.(*AD.AdaptiveOp); ok {
		return p.estimateCostWithParams(aop.Inner, cp)
	}
	switch v := op.(type) {
	case *OP.SeqScan:
		rows := p.estimateRowCount(v.Table(), nil)
		cost := float64(rows) * cp.SeqPageCost
		if cost < 1.0 {
			cost = 1.0
		}
		return cost
	case *OP.IndexScan:
		rows := p.estimateRowCount(v.Table(), nil)
		base := float64(rows) * cp.CPUIndexTupleCost
		indexIO := float64(rows) * cp.RandomPageCost / 100
		if v.IndexMode() {
			return base + indexIO
		}
		return 2*base + 2*indexIO
	case *OP.Filter:
		childCost := p.estimateCostWithParams(v.Child(), cp)
		return childCost + childCost*p.estimatePredicateSelectivity(v.Predicate())*cp.CPUOperatorCost
	case *OP.Project:
		return p.estimateCostWithParams(v.Child(), cp) + cp.CPUTupleCost
	case *OP.Limit:
		return p.estimateCostWithParams(v.Child(), cp)
	case *OP.Offset:
		return p.estimateCostWithParams(v.Child(), cp)
	case *OP.Distinct:
		return p.estimateCostWithParams(v.Child(), cp) + cp.CPUTupleCost
	case *OP.Sort:
		childCost := p.estimateCostWithParams(v.Child(), cp)
		if childCost < 1 {
			childCost = 1
		}
		return childCost*(1+log2ish(childCost)) + childCost*cp.CPUOperatorCost
	case *AG.Aggregate:
		return p.estimateCostWithParams(v.Child(), cp) + 1
	case *OP.NestedLoopJoin:
		leftCost := p.estimateCostWithParams(v.LeftChild(), cp)
		rightCost := p.estimateCostWithParams(v.RightChild(), cp)
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost * (rightCost + cp.CPUOperatorCost)
	case *OP.HashJoin:
		leftCost := p.estimateCostWithParams(v.LeftChild(), cp)
		rightCost := p.estimateCostWithParams(v.RightChild(), cp)
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return rightCost + leftCost*cp.CPUOperatorCost + rightCost*cp.CPUTupleCost
	case *OP.HashCrossJoin:
		leftCost := p.estimateCostWithParams(v.LeftChild(), cp)
		rightCost := p.estimateCostWithParams(v.RightChild(), cp)
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost + rightCost
	case *OP.MergeJoin:
		leftCost := p.estimateCostWithParams(v.LeftChild(), cp)
		rightCost := p.estimateCostWithParams(v.RightChild(), cp)
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost + rightCost + cp.CPUTupleCost
	case *WT.Insert, *WT.Update, *WT.Delete, *WT.CreateTable, *WT.DropTable:
		return 1.0
	default:
		return 1.0
	}
}

func estimateSelectivity(e PS.Expr) float64 {
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

// estimateSelectivityWithStats computes selectivity using column
// histograms when available, falling back to uniform distribution.
// REQ000085.
// The function recognizes:
//   - column = literal  → 1 / distinctCount
//   - column < literal  → bucket fraction below literal
//   - column > literal  → bucket fraction above literal
//   - column BETWEEN a AND b → bucket fraction between a and b
//   - IS NULL → nullCount / rowCount
//   - IS NOT NULL → (rowCount - nullCount) / rowCount
func estimateSelectivityWithStats(e PS.Expr, stats *ls.ColumnStats) float64 {
	if e == nil {
		return 1.0
	}

	// Binary expression: column OP literal
	if v, ok := e.(*PS.BinaryExpr); ok {
		_, lit, isColLit := extractColumnLiteral(v)
		if isColLit && stats != nil {
			switch v.Op {
			case LX.T_EQ:
				return estimateEqSelectivity(stats, lit)
			case LX.T_LT, LX.T_LE:
				return estimateRangeSelectivity(stats, nil, lit, false)
			case LX.T_GT, LX.T_GE:
				return estimateRangeSelectivity(stats, lit, nil, false)
			}
		}
		// IS NULL / IS NOT NULL handled at the operator level
		// Default for binary expressions
		return 0.5
	}

	return 0.5
}

// estimateEqSelectivity returns selectivity for column = literal.
func estimateEqSelectivity(stats *ls.ColumnStats, lit []byte) float64 {
	if stats == nil {
		return 0.1
	}
	if stats.RowCount == 0 {
		return 0.1
	}
	// Use histogram if available
	if len(stats.Histogram) > 0 {
		// Find bucket containing the literal
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
	// Uniform distribution fallback
	if stats.DistinctCount > 0 {
		return 1.0 / float64(stats.DistinctCount)
	}
	return 0.1
}

// estimateRangeSelectivity returns selectivity for a range predicate
// [low, high]. If low is nil, range is (-inf, high]. If high is nil,
// range is [low, +inf).
func estimateRangeSelectivity(stats *ls.ColumnStats, low, high []byte, inclusive bool) float64 {
	if stats == nil || stats.RowCount == 0 {
		return 0.3
	}
	// No histogram: assume uniform distribution over [min, max]
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
		// Count rows below `low`
		if low != nil && bytes.Compare(b.UpperBound, low) < 0 {
			lowRows += b.Count
		}
		// Count rows at or below `high`
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
	// Both bounds
	sel := float64(highRows-lowRows) / float64(totalRows)
	if sel < 0 {
		sel = 0
	}
	return sel
}

// extractColumnLiteral extracts (column, literal) from a binary
// expression of the form column OP literal.
func (p *Planner) estimateRowCount(table string, where PS.Expr) int {
	// REQ000780: return actual row count for in-memory tables.
	// REQ001192: skip 0-row entries — CREATE TABLE registers an empty
	// slice in DT.Tables, but actual data lives in the LSM store.
	// Returning 0 causes the planner to emit plans that produce no rows.
	if rows, ok := DT.Tables[table]; ok && len(rows) > 0 {
		return len(rows)
	}
	// REQ000787: use statistics-driven estimate from catalog.
	if cat := DT.Catalog(); cat != nil {
		if ts := cat.TableStats(table); ts != nil && ts.RowCount > 0 {
			return int(ts.RowCount)
		}
	}
	return 100 // default estimate
}

// getTableRowCount returns the estimated number of rows in a table.
// Uses the global in-memory tables map first, then falls back to
// the statistics catalog, and finally to a default of 100.
func (p *Planner) getTableRowCount(table string) float64 {
	// REQ001192: skip 0-row entries — same as estimateRowCount.
	if rows, ok := DT.Tables[table]; ok && len(rows) > 0 {
		return float64(len(rows))
	}
	if cat := DT.Catalog(); cat != nil {
		if ts := cat.TableStats(table); ts != nil && ts.RowCount > 0 {
			return float64(ts.RowCount)
		}
	}
	// Check catalog for registered stats.
	tInfo, ok := p.catalog[table]
	if ok && len(tInfo.cols) > 0 {
		if p.statsCatalog != nil {
			for _, col := range tInfo.cols {
				if cs := p.statsCatalog.ColumnStatsByName(table, col.Name); cs != nil && cs.RowCount > 0 {
					return float64(cs.RowCount)
				}
			}
		}
	}
	return 100
}

// estimateJoinCost returns the estimated cost of joining two tables or
// table sets. The cost is based on the output row count of the join,
// adjusted by predicate selectivity:
//   - equi-join predicate:  selectivity = 0.1
//   - range predicate:      selectivity = 0.3
//   - other predicates:     selectivity = 0.5
//   - no predicates (CROSS JOIN): selectivity = 1.0
func (p *Planner) estimateJoinCost(leftRows, rightRows int, predicates []PS.Expr, hasIndex bool) float64 {
	indexFactor := 1.0
	if hasIndex {
		indexFactor = 0.2
	}

	if len(predicates) == 0 {
		// Cross join: full Cartesian product.
		return float64(leftRows) * float64(rightRows) * indexFactor
	}

	sel := 1.0
	for _, pred := range predicates {
		psel := p.joinPredSel(pred, 0)
		sel *= psel
	}
	cost := float64(leftRows) * float64(rightRows) * sel * indexFactor
	if cost < 1 {
		cost = 1
	}
	return cost
}

// estimateJoinPredicateSelectivity returns the selectivity of a single
// join predicate expression. rowCount is the estimated number of rows
// in the table the predicate applies to; used for IN-list selectivity
// scaling. Pass 0 to use the default NDV of 100.
func estimateJoinPredicateSelectivity(pred PS.Expr, rowCount float64) float64 {
	if pred == nil {
		return 1.0
	}
	// REQ000819: handle IN-list expressions: selectivity ≈ len(list)/rowCount.
	// When rowCount is unavailable, fall back to default NDV=100.
	if in, ok := pred.(*PS.InExpr); ok && len(in.List) > 0 {
		sel := estimateInListSelectivity(in.List, rowCount, nil, nil)
		return sel
	}
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok {
		return 0.5
	}
	isColCol := isColumnColumnPair(bin.Left, bin.Right) || isColumnColumnPair(bin.Right, bin.Left)
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

// REQ000948: NDV-based join predicate selectivity. Method variant of
// estimateJoinPredicateSelectivity that consults the stats catalog
// (ColumnStats.DistinctCount) for column-specific NDV values when
// available. Falls back to the package-level default constants when
// stats are missing.
//
// PostgreSQL reference (eqjoinsel): selectivity = (1 - null_frac) /
// max(ndv_left, ndv_right, 1). For range predicates, default is
// (1 - null_frac) / 3 (uniform distribution assumption).
func (p *Planner) joinPredSel(pred PS.Expr, rowCount float64) float64 {
	if pred == nil {
		return 1.0
	}
	// REQ000819: IN-list expressions. Use rowCount as NDV when available.
	// REQ001057b: when the column has MCV stats, prefer the
	// 1 - ∏(1 - pᵢ) formula over the uniform len/rowCount fallback.
	if in, ok := pred.(*PS.InExpr); ok && len(in.List) > 0 {
		var mcvs [][]byte
		var freqs []float64
		if p.statsCatalog != nil {
			// The IN-list target is the leftmost child (a column
			// reference). Resolve its (table, col) pair and look up
			// the column stats — but only when the target is a bare
			// column ref. Mixed targets (e.g. expr IN (…)) fall
			// through to the legacy formula.
			if colRef, ok := in.Expr.(*PS.Ident); ok {
				table, col := p.findTableForColumn(colRef.Name), colRef.Name
				if table != "" && col != "" {
					if cs := p.statsCatalog.ColumnStatsByName(table, col); cs != nil {
						mcvs = cs.MostCommonVals
						freqs = cs.MostCommonFreqs
					}
				}
			}
		}
		return estimateInListSelectivity(in.List, rowCount, mcvs, freqs)
	}
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok {
		return 0.5
	}
	switch bin.Op {
	case LX.T_EQ:
		// REQ000948: equi-join (col = col) uses NDV of both sides.
		// REQ000948: equi-join (col = literal) uses NDV of the column.
		ndvL, ndvR := p.ndvFromExpr(bin.Left), p.ndvFromExpr(bin.Right)
		if ndvL > 0 && ndvR > 0 {
			if ndvL > ndvR {
				return 1.0 / ndvL
			}
			return 1.0 / ndvR
		}
		if ndvL > 0 {
			return 1.0 / ndvL
		}
		if ndvR > 0 {
			return 1.0 / ndvR
		}
		// REQ001095: when stats are unavailable, use the table's row
		// count as NDV proxy (col = literal hits 1/rows of the table).
		if rowCount > 0 {
			sel := 1.0 / rowCount
			if sel < 0.01 {
				sel = 0.01 // floor at 1% to avoid over-optimism
			}
			return sel
		}
		// No stats — fall back to default.
		return 0.1
	case LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
		// Range predicate: use (1 - null_frac) / 3 (uniform).
		nullFrac := p.nullFracFromExpr(bin.Left)
		if nullFrac < 0 {
			nullFrac = 0
		}
		return (1.0 - nullFrac) / 3.0
	case LX.T_OR:
		// REQ001219: OR-chain selectivity.
		// Same-column equality chain (a=1 OR a=2 OR ...) → group by
		// column and use 1 - ∏(1 - 1/ndv) per column. For multi-column
		// OR (no shared column), multiply per-column selectivities
		// (independence assumption).
		leaves := flattenOr(pred)
		if len(leaves) < 2 {
			return 0.5
		}
		// Group equalities by column.
		type colEq struct {
			col   string
			ndv   float64
			count int // number of OR leaves that reference this column
		}
		var groups []colEq
		seen := make(map[string]int) // colName -> index into groups
		hasNonEq := false
		for _, leaf := range leaves {
			c, _, ok := extractEqualityAnySide(leaf)
			if !ok {
				hasNonEq = true
				continue
			}
			if idx, ok := seen[c]; ok {
				groups[idx].count++
				continue
			}
			ndv := p.ndvFromExpr(&PS.Ident{Name: c})
			if ndv <= 0 {
				if rowCount > 0 {
					ndv = rowCount
				} else {
					ndv = 100
				}
			}
			seen[c] = len(groups)
			groups = append(groups, colEq{col: c, ndv: ndv, count: 1})
		}
		// Compute per-column selectivity = 1 - (1 - 1/ndv)^k where k is
		// the number of equalities on this column (count, not unique
		// columns — duplicates would harm but are rare in practice).
		colSels := make([]float64, 0, len(groups))
		for _, g := range groups {
			k := g.count
			if k == 0 {
				k = 1
			}
			pMiss := 1.0 - 1.0/g.ndv
			miss := 1.0
			for j := 0; j < k; j++ {
				miss *= pMiss
			}
			colSels = append(colSels, 1.0-miss)
		}
		// Combine: multi-column OR (rare but possible) → multiply sels.
		sel := 1.0
		for _, s := range colSels {
			sel *= s
		}
		if hasNonEq {
			sel *= 0.5 // dilate for any non-equality OR leaf
		}
		// Floor: selectivity can't be less than 1/rowCount.
		if rowCount > 0 {
			floor := 1.0 / rowCount
			if floor < 0.0001 {
				floor = 0.0001
			}
			if sel < floor {
				sel = floor
			}
		}
		if sel <= 0 {
			sel = 0.5
		}
		if sel > 1 {
			sel = 1
		}
		return sel
	case LX.T_AND:
		// REQ001219: T_AND rarely reaches joinPredSel because
		// splitPredicatesByTable splits AND-conjuncts upstream and the
		// caller multiplies per-predicate selectivities. Keep a
		// conservative fallback if it ever does.
		return 0.5
	default:
		return 0.5
	}
}

// estimateInListSelectivity computes the selectivity of an IN-list
// predicate using Most-Common-OP.Values stats when available.
//
// REQ001057b: matches CockroachDB / PostgreSQL semantics — when MCVs
// are known, the per-element frequency is used for matching values,
// and a uniform tail `(remaining list count) / (NDV - MCV count)`
// accounts for rare values. When MCVs are absent, the legacy
// uniform-distribution formula `min(1, len(list)/max(rowCount, 1))`
// is used.
//
// Inputs:
//   - list: the IN-list expressions (literals, parameters, etc.)
//   - rowCount: estimated number of rows in the column's table.
//     Used as the NDV denominator when no MCVs are available.
//     Pass 0 to fall back to the default NDV=100.
//   - mcvs / freqs: parallel slices of most-common values and their
//     frequencies. May be nil (legacy path).
//
// Returns selectivity in (0, 1]. A selectivity of 1.0 means the
// predicate matches everything; a value clamped to 1 means every row
// is selected.
func estimateInListSelectivity(list []PS.Expr, rowCount float64, mcvs [][]byte, freqs []float64) float64 {
	if len(list) == 0 {
		return 1.0
	}
	// MCV-aware path: for each IN-list literal, look it up in MCVs.
	// Match: pᵢ = freqs[j]. No match: contribute (1 / max(1, NDV - |MCVs|))
	// to the rare-value tail.
	if len(mcvs) > 0 && len(mcvs) == len(freqs) {
		// Build a small lookup map. Linear scan is fine for the
		// typical |MCVs| ≤ 100 and |list| ≤ 1000 budget.
		freqByVal := make(map[string]float64, len(mcvs))
		for i, v := range mcvs {
			freqByVal[string(v)] = freqs[i]
		}
		// NDV estimate: use rowCount if positive, else fall back
		// to the count of distinct MCVs plus a 1.0/NDV-tail term.
		// We approximate the rare-value uniform frequency as
		// max(0, (1 - sum(MCV freqs))) / max(1, NDV - |MCVs|).
		// NDV itself is unknown from MCVs alone, so we use
		// rowCount as the universe.
		var matched int
		var probNotMatched float64 = 1.0
		var tailCount int
		for _, item := range list {
			// REQ001057b: extract literal bytes from the IN-list
			// element. Most IN-list items are *PS.Literal or
			// *PS.IntegerLit / *PS.FloatLit / *PS.StringLit; we
			// only know the AST shape from PS.Ident (column) /
			// unknown. Treat any non-MCV match as tail.
			key, ok := inListLiteralKey(item)
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
		// Rare-value tail: uniform over (NDV - |MCVs|).
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
	// Legacy path: uniform distribution.
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

// inListLiteralKey returns a canonical byte representation of an
// IN-list literal for MCV lookup, plus an "ok" flag. Only literal
// expressions are supported; column references and complex
// expressions are not (treated as tail).
//
// REQ001057b: this avoids importing PS.Literal-specific types — the
// PS AST exposes a single Literal interface; concrete types are
// *PS.StringLit, *PS.IntegerLit, *PS.FloatLit, etc. We probe a small
// set of likely field names.
func inListLiteralKey(item PS.Expr) (string, bool) {
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

// ndvFromExpr returns the DistinctCount (NDV) of the column referenced
// by expr, or -1 if NDV is unavailable. Handles Ident and QualifiedName
// column references; returns -1 for literals, function calls, etc.
func (p *Planner) ndvFromExpr(expr PS.Expr) float64 {
	if p.statsCatalog == nil {
		return -1
	}
	table, col := p.tableColFromExpr(expr)
	if table == "" || col == "" {
		return -1
	}
	stats := p.statsCatalog.ColumnStatsByName(table, col)
	if stats == nil || stats.DistinctCount <= 0 {
		return -1
	}
	return float64(stats.DistinctCount)
}

// nullFracFromExpr returns the null fraction (NullCount/RowCount) of
// the column referenced by expr, or -1 if unavailable.
func (p *Planner) nullFracFromExpr(expr PS.Expr) float64 {
	if p.statsCatalog == nil {
		return -1
	}
	table, col := p.tableColFromExpr(expr)
	if table == "" || col == "" {
		return -1
	}
	stats := p.statsCatalog.ColumnStatsByName(table, col)
	if stats == nil || stats.RowCount <= 0 {
		return -1
	}
	return float64(stats.NullCount) / float64(stats.RowCount)
}

// tableColFromExpr extracts the (table, column) pair from a column
// reference expression. Supports PS.Ident (unqualified) and
// PS.QualifiedName (table.col). Returns ("", "") for other expr types.
func (p *Planner) tableColFromExpr(expr PS.Expr) (string, string) {
	switch e := expr.(type) {
	case *PS.Ident:
		// Unqualified column — look up via findTableForColumn.
		return p.findTableForColumn(e.Name), e.Name
	case *PS.QualifiedName:
		// table.col — explicit.
		if e.Table != "" {
			return e.Table, e.Name
		}
		if e.Name != "" {
			return "", e.Name
		}
	}
	return "", ""
}

// isColumnColumnPair returns true when both sides of the expression
// are column references (Ident or QualifiedName). Used to detect
// equi-join predicates like t1.a = t2.b.
func isColumnColumnPair(a, b PS.Expr) bool {
	_, aIsCol := a.(*PS.Ident)
	_, aIsQn := a.(*PS.QualifiedName)
	_, bIsCol := b.(*PS.Ident)
	_, bIsQn := b.(*PS.QualifiedName)
	return (aIsCol || aIsQn) && (bIsCol || bIsQn)
}

// n3JoinOrdering implements a simplified N3 (N-nearest-neighbor)
// algorithm inspired by SQLite's NGQP. It finds a near-optimal join
// order by maintaining a heap of the N=12 best partial plans and
// extending them step by step.
//
// REQ000883: table row counts are reduced by single-table predicate
// selectivity before cost estimation. Tables with highly selective
// single-table predicates (e.g., `IN (101,103)` reducing 100→2 rows)
// are joined first, minimizing intermediate result sizes.
//
// Parameters:
//   - baseTable: the FROM-clause table (leftmost in the join tree)
//   - joinTables: list of (table name, join clause) pairs to join
//   - wherePredicates: cross-table WHERE conjuncts for selectivity
//   - pushedPredicates: per-table predicates already pushed down to scans
//
// Returns the ordered list of table names that minimizes estimated cost.
// REQ000946: n3JoinOrdering now returns (order, cost) so the caller
// (n3JoinOrderingMultiStart) can compare costs across different
// starting baseTable choices. The cost is the sum of per-step
// joinCost estimates for the cheapest complete plan in the heap.
