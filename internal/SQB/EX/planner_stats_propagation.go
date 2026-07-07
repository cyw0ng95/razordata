package EX

import (
	"bytes"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// applyStatsPropagation inspects the cross-table equi-join keys
// (`t1.a = t2.a` style) and, when ColumnStats are available for
// either side, derives a range filter from the known
// [MinValue, MaxValue] and pushes it as an additional predicate
// on the OTHER side.
//
// REQ001252: "Statistics propagation — derive range filters from
// join equalities + column stats." The benefit (DuckDB reports
// up to 10×): when the build side has stats telling us `t1.a ∈
// [25, 50]`, the probe side no longer needs to read every row
// that fails the implied range. The propagation is conservative:
// we emit the filter only when the stats are present AND
// non-degenerate (MinValue != MaxValue).
//
// Returns the new plan with synthesized filters. When no
// propagation applies, returns the input unchanged.
func (p *Planner) applyStatsPropagation(scan DT.Operator, s *PS.Select) DT.Operator {
	if p.statsCatalog == nil || scan == nil || s == nil {
		return scan
	}
	// Find all equi-join key pairs in the explicit join clauses.
	// We do NOT chase transitive inferences (multi-hop propagation)
	// — only the direct `t1.a = t2.a` form.
	pairs := extractJoinEquiPairs(s)
	if len(pairs) == 0 {
		return scan
	}
	// Build synthesized filters and apply them as Filters above
	// the existing scan. We do not modify the existing scan; we
	// just wrap it. This preserves any ordering or cost decisions
	// the planner already made.
	var synthesized []PS.Expr
	for _, pair := range pairs {
		if f, ok := p.deriveRangeFilterFromStats(pair); ok && f != nil {
			synthesized = append(synthesized, f...)
		}
	}
	if len(synthesized) == 0 {
		return scan
	}
	// Combine all propagated filters into a single AND expression.
	combined := synthesized[0]
	for i := 1; i < len(synthesized); i++ {
		combined = &PS.BinaryExpr{Left: combined, Op: LX.T_AND, Right: synthesized[i]}
	}
	return OP.NewFilter(scan, combined, nil)
}

// joinEquiPair is a (left-table, left-col, right-table, right-col)
// tuple representing an equi-join predicate.
type joinEquiPair struct {
	LeftTbl  string
	LeftCol  string
	RightTbl string
	RightCol string
}

// extractJoinEquiPairs walks s.Joins for ON clauses that are
// single equi-joins `t1.a = t2.a`. We do not handle cross-table
// conjunctions from the WHERE clause here — that path goes
// through `splitPredicatesByTable` and `extractEquiJoinKeys`
// (consumed by HashJoin construction). REQ001252.
func extractJoinEquiPairs(s *PS.Select) []joinEquiPair {
	if s == nil {
		return nil
	}
	var pairs []joinEquiPair
	for _, j := range s.Joins {
		if j.On == nil {
			continue
		}
		b, ok := j.On.(*PS.BinaryExpr)
		if !ok || b.Op != LX.T_EQ {
			continue
		}
		// Expect qualified names on both sides.
		ln, lOK := b.Left.(*PS.QualifiedName)
		rn, rOK := b.Right.(*PS.QualifiedName)
		if !lOK || !rOK {
			continue
		}
		pairs = append(pairs, joinEquiPair{
			LeftTbl: ln.Table, LeftCol: ln.Name,
			RightTbl: rn.Table, RightCol: rn.Name,
		})
	}
	return pairs
}

// deriveRangeFilterFromStats returns up to two predicates that
// bound the join key on the OPPOSITE side from the table with
// known stats. The caller is responsible for not emitting the
// same filter twice when stats are available on BOTH sides (we
// pick the one with the tighter range; here we simply prefer
// the left side when stats are present there).
//
// The derived predicates are `t.col >= min` AND `t.col <= max`.
// We always produce BOTH bounds together so the optimizer can
// short-circuit, but if the input side has stats on the
// qualified column the right side is the one that gets the
// filter.
func (p *Planner) deriveRangeFilterFromStats(pair joinEquiPair) ([]PS.Expr, bool) {
	// Try left → right propagation first.
	if lstats := p.statsCatalog.ColumnStatsByName(pair.LeftTbl, pair.LeftCol); lstats != nil &&
		len(lstats.MinValue) > 0 && len(lstats.MaxValue) > 0 &&
		!bytes.Equal(lstats.MinValue, lstats.MaxValue) {
		return buildRangePredicates(pair.RightTbl, pair.RightCol, lstats.MinValue, lstats.MaxValue), true
	}
	// Then right → left.
	if rstats := p.statsCatalog.ColumnStatsByName(pair.RightTbl, pair.RightCol); rstats != nil &&
		len(rstats.MinValue) > 0 && len(rstats.MaxValue) > 0 &&
		!bytes.Equal(rstats.MinValue, rstats.MaxValue) {
		return buildRangePredicates(pair.LeftTbl, pair.LeftCol, rstats.MinValue, rstats.MaxValue), true
	}
	return nil, false
}

// buildRangePredicates builds `tbl.col >= min AND tbl.col <= max`
// for the given qualified column. Returns one or two predicates
// (we always return both bounds; downstream AND-combines them).
func buildRangePredicates(tbl, col string, minVal, maxVal []byte) []PS.Expr {
	qualified := &PS.QualifiedName{Table: tbl, Name: col}
	minLit := decodeByteLiteral(minVal)
	maxLit := decodeByteLiteral(maxVal)
	ge := &PS.BinaryExpr{Left: qualified, Op: LX.T_GE, Right: minLit}
	le := &PS.BinaryExpr{Left: qualified, Op: LX.T_LE, Right: maxLit}
	return []PS.Expr{ge, le}
}

// decodeByteLiteral wraps a []byte value as the simplest
// representation the executor can evaluate. For now we
// unconditionally emit a StringLiteral — the executor's
// binary-comparison path handles byte-comparable types
// consistently. REQ001252: byte-for-byte comparison is what
// the secondary index uses, so this matches the storage encoding.
func decodeByteLiteral(b []byte) PS.Expr {
	return &PS.StringLiteral{Val: string(b)}
}

// statsRangeFilterForJoin looks at a single ON expression for an
// equi-join `t1.a = t2.a` and, when column stats are available
// for one side, returns a synthesized predicate that bounds the
// OTHER side's column. Returns nil if no propagation is possible.
//
// Caller is the join-building loop in planSelect; the returned
// filter is applied as an OP.Filter on the right-side scan
// before the join runs, so the right-side row count entering
// the hash table is reduced by the known range.
//
// REQ001252.
func (p *Planner) statsRangeFilterForJoin(on PS.Expr, leftTbl, rightTbl string) PS.Expr {
	if p.statsCatalog == nil || on == nil {
		return nil
	}
	b, ok := on.(*PS.BinaryExpr)
	if !ok || b.Op != LX.T_EQ {
		return nil
	}
	ln, lOK := b.Left.(*PS.QualifiedName)
	rn, rOK := b.Right.(*PS.QualifiedName)
	if !lOK || !rOK {
		return nil
	}
	// Prefer propagation left → right (left is the build side).
	if lstats := p.statsCatalog.ColumnStatsByName(ln.Table, ln.Name); lstats != nil &&
		len(lstats.MinValue) > 0 && len(lstats.MaxValue) > 0 &&
		!bytes.Equal(lstats.MinValue, lstats.MaxValue) {
		// We can compose `t2.col >= min AND t2.col <= max`.
		return buildRangePredicate(rn.Table, rn.Name, lstats.MinValue, lstats.MaxValue)
	}
	if rstats := p.statsCatalog.ColumnStatsByName(rn.Table, rn.Name); rstats != nil &&
		len(rstats.MinValue) > 0 && len(rstats.MaxValue) > 0 &&
		!bytes.Equal(rstats.MinValue, rstats.MaxValue) {
		return buildRangePredicate(ln.Table, ln.Name, rstats.MinValue, rstats.MaxValue)
	}
	return nil
}

// buildRangePredicate composes `tbl.col >= min AND tbl.col <= max`
// into a single AND-joined PS.Expr. The byte values are wrapped
// as StringLiteral; the executor's binary-comparison path handles
// them as bytes — this matches the secondary index encoding.
func buildRangePredicate(tbl, col string, minVal, maxVal []byte) PS.Expr {
	qualified := &PS.QualifiedName{Table: tbl, Name: col}
	minLit := &PS.StringLiteral{Val: string(minVal)}
	maxLit := &PS.StringLiteral{Val: string(maxVal)}
	ge := &PS.BinaryExpr{Left: qualified, Op: LX.T_GE, Right: minLit}
	le := &PS.BinaryExpr{Left: qualified, Op: LX.T_LE, Right: maxLit}
	return &PS.BinaryExpr{Left: ge, Op: LX.T_AND, Right: le}
}

// _ reserved for future use when the propagation is wired
// directly into the join-construction loop.
var _ = (*ls.ColumnStats)(nil)
