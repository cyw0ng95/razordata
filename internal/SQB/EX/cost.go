package EX

import (
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// vectorizedJoinBonus is the cost multiplier applied to join operators
// that have vectorized (batch) implementations. Vectorized joins are
// ~30% cheaper than their row-based counterparts due to batched columnar
// processing and elimination of per-row allocation overhead. REQ001621.
const vectorizedJoinBonus = 0.7

// isVectorizedJoin reports whether the operator will be vectorized
// by tryVectorizePlan. REQ001621.
func isVectorizedJoin(op DT.Operator) bool {
	switch op.(type) {
	case *OP.HashJoin, *OP.NestedLoopJoin, *OP.MergeJoin, *OP.HashCrossJoin:
		return true
	}
	return false
}

func (p *Planner) estimateCostLegacy(op DT.Operator) float64 {
	if op == nil {
		return 0
	}
	// REQ002171: AdaptiveOp removed — op is the raw operator.
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
		return p.estimateCostLegacy(v.Child()) * CO.EstimatePredicateSelectivity(v.Predicate(), p.findTableForColumn, p.statsCatalog, CO.EstimateSelectivityWithStats)
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
		return leftCost * rightCost * vectorizedJoinBonus
	case *OP.HashJoin:
		leftCost := p.estimateCostLegacy(v.LeftChild())
		rightCost := p.estimateCostLegacy(v.RightChild())
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return (leftCost + rightCost) * vectorizedJoinBonus
	case *OP.HashCrossJoin:
		leftCost := p.estimateCostLegacy(v.LeftChild())
		rightCost := p.estimateCostLegacy(v.RightChild())
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return (leftCost + rightCost) * vectorizedJoinBonus
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
		return (leftCost + rightCost + 1) * vectorizedJoinBonus
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
	// REQ002171: AdaptiveOp removed — op is the raw operator.
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
		return childCost + childCost*CO.EstimatePredicateSelectivity(v.Predicate(), p.findTableForColumn, p.statsCatalog, CO.EstimateSelectivityWithStats)*cp.CPUOperatorCost
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
		return leftCost * (rightCost + cp.CPUOperatorCost) * vectorizedJoinBonus
	case *OP.HashJoin:
		leftCost := p.estimateCostWithParams(v.LeftChild(), cp)
		rightCost := p.estimateCostWithParams(v.RightChild(), cp)
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return (rightCost + leftCost*cp.CPUOperatorCost + rightCost*cp.CPUTupleCost) * vectorizedJoinBonus
	case *OP.HashCrossJoin:
		leftCost := p.estimateCostWithParams(v.LeftChild(), cp)
		rightCost := p.estimateCostWithParams(v.RightChild(), cp)
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return (leftCost + rightCost) * vectorizedJoinBonus
	case *OP.MergeJoin:
		leftCost := p.estimateCostWithParams(v.LeftChild(), cp)
		rightCost := p.estimateCostWithParams(v.RightChild(), cp)
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return (leftCost + rightCost + cp.CPUTupleCost) * vectorizedJoinBonus
	case *WT.Insert, *WT.Update, *WT.Delete, *WT.CreateTable, *WT.DropTable:
		return 1.0
	default:
		return 1.0
	}
}

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

func (p *Planner) getTableRowCount(table string) float64 {
	if rows, ok := DT.Tables[table]; ok && len(rows) > 0 {
		return float64(len(rows))
	}
	if cat := DT.Catalog(); cat != nil {
		if ts := cat.TableStats(table); ts != nil && ts.RowCount > 0 {
			return float64(ts.RowCount)
		}
	}
	return 100.0
}

// estimatedCardinality returns the estimated row count for a table
// after applying all single-table predicates. Uses CO.EstimateCardinality
// when catalog stats are available, falling back to getTableRowCount.
// REQ001642.
func (p *Planner) estimatedCardinality(table string, wherePredicates []PS.Expr, pushedPredicates map[string][]PS.Expr) float64 {
	baseRows := p.getTableRowCount(table)

	// Collect all predicates that apply to this table.
	var tablePreds []PS.Expr
	for _, pred := range wherePredicates {
		if p.canPushDown(pred, table) {
			tablePreds = append(tablePreds, pred)
		}
	}
	if pushedPredicates != nil {
		if preds, ok := pushedPredicates[table]; ok {
			tablePreds = append(tablePreds, preds...)
		}
	}

	if len(tablePreds) == 0 {
		return baseRows
	}

	// Try to get column stats for selectivity estimation.
	cat := DT.Catalog()
	if cat != nil {
		if ts := cat.TableStats(table); ts != nil && ts.RowCount > 0 {
			// Use the first column's stats as a proxy for the table.
			for _, col := range ts.ColStats {
				rows := CO.EstimateCardinality(baseRows, tablePreds, col)
				if rows < baseRows {
					return rows
				}
				break
			}
		}
	}

	// Fallback: apply selectivity using the planner's existing heuristic.
	rows := baseRows
	for _, pred := range tablePreds {
		psel := p.joinPredSel(pred, rows)
		rows *= psel
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (p *Planner) estimateJoinCost(leftRows, rightRows int, predicates []PS.Expr, hasIndex bool) float64 {
	indexFactor := 1.0
	if hasIndex {
		indexFactor = 0.2
	}

	if len(predicates) == 0 {
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
	if in, ok := pred.(*PS.InExpr); ok && len(in.List) > 0 {
		var mcvs [][]byte
		var freqs []float64
		if p.statsCatalog != nil {
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
		return CO.EstimateInListSelectivity(in.List, rowCount, mcvs, freqs)
	}
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok {
		return 0.5
	}
	switch bin.Op {
	case LX.T_EQ:
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
		if rowCount > 0 {
			sel := 1.0 / rowCount
			if sel < 0.01 {
				sel = 0.01
			}
			return sel
		}
		return 0.1
	case LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
		nullFrac := p.nullFracFromExpr(bin.Left)
		if nullFrac < 0 {
			nullFrac = 0
		}
		return (1.0 - nullFrac) / 3.0
	case LX.T_OR:
		leaves := flattenOr(pred)
		if len(leaves) < 2 {
			return 0.5
		}
		type colEq struct {
			col   string
			ndv   float64
			count int
		}
		var groups []colEq
		seen := make(map[string]int)
		hasNonEq := false
		for _, leaf := range leaves {
			c, _, ok := CO.ExtractEqualityAnySide(leaf)
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
		sel := 1.0
		for _, s := range colSels {
			sel *= s
		}
		if hasNonEq {
			sel *= 0.5
		}
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
		return 0.5
	default:
		return 0.5
	}
}

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
