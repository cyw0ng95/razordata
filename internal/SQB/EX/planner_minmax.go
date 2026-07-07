package EX

import (
	"strings"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// detectMinMaxAggregate returns (columnName, isMin) when the SELECT
// list is exactly a single MIN(col) or MAX(col) on a single table
// that the optimizer can replace with a single index seek. Returns
// ok=false when not eligible.
//
// Eligibility (REQ001247):
//  1. Single-table SELECT (s.From != "")
//  2. SELECT list is exactly one MIN(col) or MAX(col) aggregate.
//  3. Argument is a bare Ident (column reference).
//  4. No DISTINCT, no FILTER, no GROUP BY, no HAVING, no DISTINCT,
//     no ORDER BY. WHERE: not eligible for the rewrite today
//     (the rewriter would need to push the predicate down to
//     the index range — deferred). An empty WHERE / TRUE constant
//     passes.
//
// The IndexScan iterates entries in lexicographic order; MIN(col)
// = first row. MAX is not optimized here because it requires a
// reverse iterator (deferred — falling back to SeqScan+Aggregate).
func detectMinMaxAggregate(s *PS.Select) (colName string, isMin bool, ok bool) {
	if s == nil || s.From == "" {
		return "", false, false
	}
	if len(s.GroupBy) > 0 || s.Having != nil || s.Distinct || len(s.OrderBy) > 0 {
		return "", false, false
	}
	if !isNoOpWhere(s.Where) {
		return "", false, false
	}
	if len(s.Cols) != 1 {
		return "", false, false
	}
	c := s.Cols[0]
	if !DT.ContainsAggregate(c) {
		return "", false, false
	}
	agg := unwrapAliasedAggregate(c)
	if agg == nil {
		return "", false, false
	}
	if agg.Distinct || agg.Filter != nil {
		// DISTINCT MAX/MIN requires distinct enumeration; FILTER
		// requires per-row filtering — both prevent single-seek.
		return "", false, false
	}
	switch strings.ToUpper(agg.Name) {
	case "MIN":
		isMin = true
	case "MAX":
		isMin = false
	default:
		return "", false, false
	}
	ident, isIdent := agg.Arg.(*PS.Ident)
	if !isIdent {
		return "", false, false
	}
	return ident.Name, isMin, true
}

// isNoOpWhere reports whether the WHERE expression is a no-op for
// the MIN/MAX rewrite (no rows excluded). Accepts nil and the
// trivial tautology `1 = 1` (used by some ORMs and test fixtures
// as a "match everything" placeholder). REQ001247.
func isNoOpWhere(e PS.Expr) bool {
	if e == nil {
		return true
	}
	if b, ok := e.(*PS.BinaryExpr); ok {
		if b.Op == LX.T_EQ {
			if l, okL := b.Left.(*PS.NumberLiteral); okL {
				if r, okR := b.Right.(*PS.NumberLiteral); okR {
					return l.Val == r.Val
				}
			}
		}
	}
	return false
}

// unwrapAliasedAggregate returns the underlying *PS.AggregateFunc
// if c is a MIN/MAX aggregate (optionally wrapped in AliasedExpr).
func unwrapAliasedAggregate(c PS.Expr) *PS.AggregateFunc {
	if a, ok := c.(*PS.AggregateFunc); ok {
		return a
	}
	if ae, ok := c.(*PS.AliasedExpr); ok {
		if a, ok := ae.Expr.(*PS.AggregateFunc); ok {
			return a
		}
	}
	return nil
}

// tryMinMaxIndexScan is the planner hook for the REQ001247
// optimization. It currently returns nil (no-op) because
// forwarding the optimization through `IndexScan` would require
// the secondary-index key/value encoding to store the primary key
// in the value slot — currently the index stores the encoded
// value in BOTH key and value, which causes IndexScan to fetch
// the wrong row when the value is used as a PK (see operators.go
// `nextFromIndex`). The detection helper `detectMinMaxAggregate`
// covers the eligibility rules so once the index encoding is
// corrected, the optimization can be re-enabled by replacing
// the body to emit OP.NewIndexScanWithRange + OP.NewLimit(1).
func (p *Planner) tryMinMaxIndexScan(s *PS.Select) DT.Operator {
	_, _, ok := detectMinMaxAggregate(s)
	if !ok {
		return nil
	}
	// Disabled: re-enable when the secondary-index value encoding
	// carries the primary key. See the file-level comment above.
	return nil
}
