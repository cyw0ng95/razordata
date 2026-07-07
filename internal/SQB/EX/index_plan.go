package EX

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func (p *Planner) tryBitmapHeapScan(s *PS.Select, whereExpr PS.Expr) DT.Operator {
	if s == nil || whereExpr == nil || p.store == nil {
		return nil
	}
	cols, lits, ok := extractOrIndexedEqColumns(whereExpr)
	if !ok || len(cols) < 2 {
		return nil
	}
	if len(cols) != len(lits) {
		return nil
	}
	children := make([]DT.Operator, 0, len(cols))
	for i, col := range cols {
		idx, found := p.selectIndex(s.From, col)
		if !found || !hasWriterIndex(s.From, idx) {
			return nil
		}
		tableID, _ := DT.TableIDFor(s.From)
		isc, err := OP.NewIndexScanWithIndex(p.store, tableID, s.From, idx, lits[i], nil)
		if err != nil {
			return nil
		}
		children = append(children, isc)
	}
	if len(children) < 2 {
		return nil
	}
	bhs := OP.NewBitmapHeapScan(s.From, p.store, children)
	if whereExpr != nil {
		return OP.NewFilter(bhs, whereExpr, nil)
	}
	return bhs
}

// extractOrIndexedEqColumns recognises top-level OR whose
// branches are indexed-column equalities on distinct columns.
// Returns the columns and their indexed-key bytes, in order. Only
// the simplest shape — `col1 = lit1 OR col2 = lit2` (or
// OR-chains) — is recognised. Deeper expressions fall through
// to the OP.IndexScan/OP.SeqScan path.
func extractOrIndexedEqColumns(e PS.Expr) ([]string, [][]byte, bool) {
	b, ok := e.(*PS.BinaryExpr)
	if !ok || b.Op != LX.T_OR {
		// also handle top-level BinaryExpr that wraps a single AND-of-OR?
		// For Step 3b we keep scope tight: OR only.
		return nil, nil, false
	}
	branches := flattenOr(b)
	if len(branches) < 2 {
		return nil, nil, false
	}
	cols := make([]string, 0, len(branches))
	lits := make([][]byte, 0, len(branches))
	seen := make(map[string]struct{}, len(branches))
	for _, br := range branches {
		col, lit, ok := indexedColumnEq(br)
		if !ok {
			return nil, nil, false
		}
		if _, dup := seen[col]; dup {
			// Same column twice → simple OP.IndexScan path is enough;
			// bitmap doesn't help.
			return nil, nil, false
		}
		seen[col] = struct{}{}
		cols = append(cols, col)
		lits = append(lits, lit)
	}
	return cols, lits, true
}

// flattenOr returns the leaves of a top-level chain of OR
// BinaryExprs. The leaves preserve the order they appear in the
// predicate so the bitmap ordering is stable across calls.
func flattenOr(e PS.Expr) []PS.Expr {
	var out []PS.Expr
	var walk func(PS.Expr)
	walk = func(x PS.Expr) {
		if x == nil {
			return
		}
		b, ok := x.(*PS.BinaryExpr)
		if ok && b.Op == LX.T_OR {
			walk(b.Left)
			walk(b.Right)
			return
		}
		out = append(out, x)
	}
	walk(e)
	return out
}

// tryIndexOnlyScan wraps an OP.IndexScan in OP.IndexOnlyScan when the
// projected columns are entirely covered by the index columns
// (plus optionally the primary key). Returns nil if not
// eligible. REQ001107.
//
// Conservative guard: we refuse to wrap when the projection is
// empty or `*` (i.e. SELECT 1 or SELECT *). Those cases already
// work via OP.IndexScan — wrapping them in OP.IndexOnlyScan breaks
// correlated-subquery machinery that inspects the inner scan
// type. Only concrete column projections trigger the path.
//
// REQ001254: also accepts a Filter/FilterProject wrapping an
// OP.IndexScan; the wrapper is preserved on top of the new
// IndexOnlyScan so the residual predicate still applies.
func (p *Planner) tryIndexOnlyScan(s *PS.Select, whereExpr PS.Expr, scan DT.Operator) DT.Operator {
	if s == nil || scan == nil {
		return nil
	}
	isc, wrapper, ok := unwrapIndexScan(scan)
	if !ok || isc == nil {
		return nil
	}
	pk := p.tablePK(s.From)
	idxCols, ok := p.indexColumns(s.From, isc.Idx())
	if !ok {
		return nil
	}
	projected := projectColumns(s)
	if len(projected) == 0 {
		return nil
	}
	if !OP.IsCoveringIndex(projected, idxCols, pk) {
		return nil
	}
	_ = whereExpr
	ios := OP.NewIndexOnlyScan(isc)
	if wrapper == nil {
		return ios
	}
	// Re-attach the wrapper so the residual predicate still runs.
	switch w := wrapper.(type) {
	case *OP.Filter:
		w.SetChild(ios)
		return w
	case *OP.FilterProject:
		w.SetChild(ios)
		return w
	default:
		return ios
	}
}

// unwrapIndexScan returns the underlying OP.IndexScan plus the
// optional outer wrapper (Filter or FilterProject). The boolean
// is true on success. Used by tryIndexOnlyScan to handle plans
// where predicate decomposition has wrapped the scan in a
// residual Filter. REQ001254.
func unwrapIndexScan(scan DT.Operator) (*OP.IndexScan, DT.Operator, bool) {
	if scan == nil {
		return nil, nil, false
	}
	if isc, ok := scan.(*OP.IndexScan); ok {
		return isc, nil, true
	}
	if f, ok := scan.(*OP.Filter); ok {
		if isc, ok := f.Child().(*OP.IndexScan); ok {
			return isc, f, true
		}
	}
	if fp, ok := scan.(*OP.FilterProject); ok {
		if isc, ok := fp.Child().(*OP.IndexScan); ok {
			return isc, fp, true
		}
	}
	return nil, nil, false
}

// projectColumns returns the projected column names from a
// Select. StarExpr maps to nil so IsCoveringIndex rejects
// covering evaluation gracefully (it returns true only for
// empty projection).
func projectColumns(s *PS.Select) []string {
	if s == nil || len(s.Cols) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.Cols))
	for _, c := range s.Cols {
		switch v := c.(type) {
		case *PS.StarExpr:
			return nil // any column → not covering
		case *PS.Ident:
			out = append(out, v.Name)
		default:
			return nil
		}
	}
	return out
}

// tablePK looks up the primary key column for a registered
// table. Empty string when unknown — IsCoveringIndex treats an
// empty pk as "no pk cover" and falls back to index-column
// coverage only.
func (p *Planner) tablePK(table string) string {
	if p.catalog == nil {
		return ""
	}
	if t, ok := p.catalog[table]; ok && t != nil {
		return t.pk
	}
	return ""
}

// indexColumns returns the columns a registered index covers
// and whether the index exists.
func (p *Planner) indexColumns(table, idx string) ([]string, bool) {
	if p.catalog == nil {
		return nil, false
	}
	t, ok := p.catalog[table]
	if !ok || t == nil {
		return nil, false
	}
	cols, ok := t.indexes[idx]
	if !ok {
		return nil, false
	}
	return append([]string(nil), cols...), true
}
func NewIndexOrSeqScan(table string, where PS.Expr, p *Planner) DT.Operator {
	// REQ001043: emit ParallelSeqScan when pool is available and
	// the in-memory table has enough rows.
	if p != nil && p.pool != nil {
		DT.TablesMu.RLock()
		src := DT.Tables[table]
		rowCount := len(src)
		DT.TablesMu.RUnlock()
		if rowCount >= ParallelThreshold {
			// Build schema from planner catalog (available at plan time)
			ti := p.catalog[table]
			if ti != nil && len(ti.cols) > 0 {
				schema := make([]string, len(ti.cols))
				types := make([]LX.TokenType, len(ti.cols))
				for k, ci := range ti.cols {
					schema[k] = ci.Name
					types[k] = LX.TokenType(ci.Typ)
				}
				if ss := OP.NewParallelSeqScanRow(src, schema, types, p.pool.(*UT.WorkerPool)); ss != nil {
					return ss
				}
			}
		}
		// REQ001051: detect IN-list on any column with > 10 values
		// and fan out filtered scans across workers.
		if where != nil {
			if colName, inValues, ok := extractInListValues(where); ok && len(inValues) >= 10 {
				ti := p.catalog[table]
				if ti != nil && len(ti.cols) > 0 && rowCount > 0 {
					schema := make([]string, len(ti.cols))
					types := make([]LX.TokenType, len(ti.cols))
					for k, ci := range ti.cols {
						schema[k] = ci.Name
						types[k] = LX.TokenType(ci.Typ)
					}
					return OP.NewParallelIndexRangeScan(src, schema, types, colName, inValues, p.pool.(*UT.WorkerPool))
				}
			}
		}
	}
	if p != nil && where != nil {
		if col, ok := indexedColumn(where); ok {
			if idx, found := p.selectIndex(table, col); found {
				if p.store != nil {
					if isc, err := OP.NewIndexScanWithStore(p.store, table, idx); err == nil {
						return isc
					}
				}
				return OP.NewIndexScan(table, idx, nil, nil)
			}
		}
		// REQ001068: check for equality predicate (col = ?) on an indexed column.
		if col, seekValue, ok := indexedColumnEq(where); ok {
			if idx, found := p.selectIndex(table, col); found {
				if hasWriterIndex(table, idx) {
					if p.store != nil {
						if tableID, ok := DT.TableIDFor(table); ok {
							if isc, err := OP.NewIndexScanWithIndex(p.store, tableID, table, idx, seekValue, nil); err == nil {
								return isc
							}
						}
					}
					return OP.NewIndexScan(table, idx, seekValue, nil)
				}
			}
		}
		// REQ001069: check for range predicate (col > ? / col < ? / BETWEEN) on an indexed column.
		if col, lower, lowerIncl, upper, upperIncl, ok := indexedColumnRange(where); ok {
			if idx, found := p.selectIndex(table, col); found {
				if hasWriterIndex(table, idx) {
					if p.store != nil {
						if tableID, ok := DT.TableIDFor(table); ok {
							if isc, err := OP.NewIndexScanWithRange(p.store, tableID, table, idx, lower, lowerIncl, upper, upperIncl); err == nil {
								return isc
							}
						}
					}
					return OP.NewIndexScan(table, idx, lower, upper)
				}
			}
		}
		// REQ001070: check for LIKE with constant prefix on an indexed column.
		// Uses OP.IndexScan with range [prefix, prefix+0xff) to seek to matching
		// entries, then the OP.Filter on top applies the full LIKE match.
		if col, prefix, ok := indexedColumnLikePrefix(where); ok {
			if idx, found := p.selectIndex(table, col); found {
				if hasWriterIndex(table, idx) {
					// Upper bound: prefix + 0xff (highest char) for prefix match.
					upper := make([]byte, len(prefix)+1)
					copy(upper, prefix)
					upper[len(prefix)] = 0xff
					if p.store != nil {
						if tableID, ok := DT.TableIDFor(table); ok {
							if isc, err := OP.NewIndexScanWithRange(p.store, tableID, table, idx, prefix, true, upper, false); err == nil {
								return isc
							}
						}
					}
					return OP.NewIndexScan(table, idx, prefix, upper)
				}
			}
		}
	}
	return OP.NewSeqScan(table)
}

// pickCheaperScan returns a cheaper scan alternative for the
// given WHERE predicate, if one exists. The function builds
// both a OP.SeqScan and an OP.IndexScan candidate and returns the
// lower-cost one. REQ000156 (iter-27).
// The cost model is simple but effective:
//   - OP.SeqScan: 1.0 unit per row
//   - OP.IndexScan: 0.1 unit per row, multiplied by predicate
//     selectivity (so a high-selectivity predicate on an
//     indexed column strongly prefers OP.IndexScan)
//
// If no index exists on the WHERE column, the function
// returns the original scan unchanged. If the cost of the
// index scan is not lower, the original scan is returned.
func (p *Planner) pickCheaperScan(table string, where PS.Expr, current DT.Operator) (DT.Operator, bool) {
	// REQ001106/107: don't downgrade a bitmap/index-only scan
	// back to a plain OP.IndexScan via the cost model — the new
	// operators are explicit planner choices, not cost fallback.
	if _, isBitmap := current.(*OP.BitmapHeapScan); isBitmap {
		return current, false
	}
	if _, isCover := current.(*OP.IndexOnlyScan); isCover {
		return current, false
	}
	// REQ000156 (iter-27): cost-based scan selection. The
	// function looks at the WHERE predicate to discover the
	// indexed column. We accept both simple equality
	// (`BinaryExpr col = lit`) and range predicates
	// (`BinaryExpr col > lit` / `BetweenExpr`).
	col, _ := indexedColumnOrRange(where)
	if col == "" {
		return current, false
	}
	idx, found := p.selectIndex(table, col)
	if !found {
		return current, false
	}
	// REQ000156 (iter-27): only swap to the index path if the
	// index is also registered for writer maintenance. This
	// avoids picking an OP.IndexScan whose keyspace has not been
	// backfilled (the existing planSelect code already uses
	// hasWriterIndex for the same reason).
	if !hasWriterIndex(table, idx) {
		return current, false
	}
	// Build a candidate OP.IndexScan.
	var indexScan DT.Operator
	if p.store != nil {
		if isc, err := OP.NewIndexScanWithStore(p.store, table, idx); err == nil {
			indexScan = isc
		}
	}
	if indexScan == nil {
		indexScan = OP.NewIndexScan(table, idx, nil, nil)
	}
	if indexScan == nil {
		return current, false
	}
	// Wrap both scans in a OP.Filter so the cost reflects the
	// post-filter work, matching how they will actually run.
	seqCandidate := OP.NewFilter(current, where, nil)
	idxCandidate := OP.NewFilter(indexScan, where, nil)
	seqCost := p.estimateCost(seqCandidate)
	idxCost := p.estimateCost(idxCandidate)
	if idxCost < seqCost {
		return indexScan, true
	}
	return current, false
}

// indexedColumnOrRange returns the indexed column name from a
// WHERE predicate, accepting both equality/range binary
// expressions and BETWEEN expressions. Returns "" if the
// predicate is not column-bounded.
func indexedColumnOrRange(e PS.Expr) (string, bool) {
	if e == nil {
		return "", false
	}
	if col, ok := indexedColumn(e); ok {
		return col, true
	}
	if col, _, _, _, _, ok := indexedColumnRange(e); ok {
		return col, true
	}
	return "", false
}

func indexedColumn(e PS.Expr) (string, bool) {
	switch v := e.(type) {
	case *PS.BinaryExpr:
		if l, ok := v.Left.(*PS.Ident); ok {
			return l.Name, true
		}
		if r, ok := v.Right.(*PS.Ident); ok {
			return r.Name, true
		}
		return indexedColumn(v.Left)
	}
	return "", false
}

// indexedColumnEq returns (columnName, encodedValue, true) if
// `e` is an equality comparison between an identifier and a
// literal (e.g. `col = 5` or `col = 'x'`). The encodedValue is
// the index key bytes (int64 big-endian for integers, raw
// string for strings).
// iter-22: used by the planner to enable real index seek via
// NewIndexScanWithIndex. REQ000252.
func indexedColumnEq(e PS.Expr) (string, []byte, bool) {
	b, ok := e.(*PS.BinaryExpr)
	if !ok {
		return "", nil, false
	}
	if b.Op != LX.T_EQ {
		return "", nil, false
	}
	// Pattern: Ident = Literal
	if l, ok := b.Left.(*PS.Ident); ok {
		if v, ok := encodeIndexValue(b.Right); ok {
			return l.Name, v, true
		}
	}
	// Pattern: Literal = Ident
	if r, ok := b.Right.(*PS.Ident); ok {
		if v, ok := encodeIndexValue(b.Left); ok {
			return r.Name, v, true
		}
	}
	return "", nil, false
}

// indexedColumnRange returns (columnName, lower, lowerInclusive,
// upper, upperInclusive, true) if `e` is a comparison on a single
// column with one or two literal bounds. Recognized shapes:
//
//	col > X   (lower exclusive, no upper)
//	col >= X  (lower inclusive, no upper)
//	col < X   (no lower, upper exclusive)
//	col <= X  (no lower, upper inclusive)
//	col BETWEEN X AND Y
//	  (lower inclusive, upper inclusive; both literals)
//
// REQ000074 (iter-27): used by the planner to enable real range
// seek via NewIndexScanWithRange. Replaces the prefix-scan
// fallback for non-equality predicates on indexed columns.
func indexedColumnRange(e PS.Expr) (string, []byte, bool, []byte, bool, bool) {
	switch v := e.(type) {
	case *PS.BetweenExpr:
		col, ok := v.Expr.(*PS.Ident)
		if !ok {
			return "", nil, false, nil, false, false
		}
		low, ok := encodeIndexValue(v.Low)
		if !ok {
			return "", nil, false, nil, false, false
		}
		high, ok := encodeIndexValue(v.High)
		if !ok {
			return "", nil, false, nil, false, false
		}
		return col.Name, low, true, high, true, true
	case *PS.BinaryExpr:
		// Recognize the comparison op
		lower, lowerIncl, upper, upperIncl, hasBounds, isCol := rangeBounds(v)
		if !isCol {
			return "", nil, false, nil, false, false
		}
		if !hasBounds {
			return "", nil, false, nil, false, false
		}
		colName := columnName(v)
		if colName == "" {
			return "", nil, false, nil, false, false
		}
		return colName, lower, lowerIncl, upper, upperIncl, true
	}
	return "", nil, false, nil, false, false
}

// rangeBounds pulls (lower, lowerInclusive, upper, upperInclusive)
// out of a single comparison. Returns isCol=true if a column is
// involved, and hasBounds=true if at least one bound is present.
func rangeBounds(b *PS.BinaryExpr) (lower []byte, lowerIncl bool, upper []byte, upperIncl bool, hasBounds bool, isCol bool) {
	// Pattern: Ident op Literal
	if l, ok := b.Left.(*PS.Ident); ok {
		if v, ok := encodeIndexValue(b.Right); ok {
			_ = l
			l, i, u, ii, h := rangeFromOp(b.Op, v)
			return l, i, u, ii, h, true
		}
		return nil, false, nil, false, false, true
	}
	// Pattern: Literal op Ident
	if r, ok := b.Right.(*PS.Ident); ok {
		if v, ok := encodeIndexValue(b.Left); ok {
			_ = r
			// Flip op direction
			flipped := flipOp(b.Op)
			l, i, u, ii, h := rangeFromOp(flipped, v)
			return l, i, u, ii, h, true
		}
		return nil, false, nil, false, false, true
	}
	return nil, false, nil, false, false, false
}

// rangeFromOp converts (op, literalValue) to (lower, lowerIncl,
// upper, upperIncl, hasBounds).
func rangeFromOp(op LX.TokenType, v []byte) (lower []byte, lowerIncl bool, upper []byte, upperIncl bool, hasBounds bool) {
	switch op {
	case LX.T_GT:
		return v, false, nil, false, true
	case LX.T_GE:
		return v, true, nil, false, true
	case LX.T_LT:
		return nil, false, v, false, true
	case LX.T_LE:
		return nil, false, v, true, true
	}
	return nil, false, nil, false, false
}

// flipOp mirrors a comparison: `5 < col` becomes `col > 5`.
// The token table uses distinct constants for each op, so we map
// each one explicitly.
func flipOp(op LX.TokenType) LX.TokenType {
	switch op {
	case LX.T_LT:
		return LX.T_GT
	case LX.T_LE:
		return LX.T_GE
	case LX.T_GT:
		return LX.T_LT
	case LX.T_GE:
		return LX.T_LE
	}
	return op
}

// columnName returns the column name from a comparison's column
// side, or "" if neither side is an Ident.
func columnName(b *PS.BinaryExpr) string {
	if l, ok := b.Left.(*PS.Ident); ok {
		return l.Name
	}
	if r, ok := b.Right.(*PS.Ident); ok {
		return r.Name
	}
	return ""
}

// indexedColumnLikePrefix detects LIKE expressions with a constant prefix.
// For `col LIKE 'abc%'`, returns ("col", []byte("abc"), true).
// For `col LIKE '%abc'` (wildcard first), returns ("", nil, false).
// Underscore (_) also ends the prefix since it matches any single char.
// REQ001070.
func indexedColumnLikePrefix(e PS.Expr) (string, []byte, bool) {
	if e == nil {
		return "", nil, false
	}
	b, ok := e.(*PS.BinaryExpr)
	if !ok {
		return "", nil, false
	}
	if b.Op != LX.T_LIKE && b.Op != LX.T_GLOB {
		return "", nil, false
	}
	// Column must be on the left side.
	ident, ok := b.Left.(*PS.Ident)
	if !ok {
		return "", nil, false
	}
	// Pattern must be a string literal.
	s, ok := b.Right.(*PS.StringLiteral)
	if !ok {
		return "", nil, false
	}
	prefix := extractLikePrefix(s.Val)
	if prefix == "" {
		return "", nil, false
	}
	return ident.Name, []byte(prefix), true
}

// extractLikePrefix returns the constant prefix before the first
// LIKE wildcard character (% or _). Returns "" if the pattern
// starts with a wildcard (no usable prefix).
func extractLikePrefix(pattern string) string {
	if pattern == "" {
		return ""
	}
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '%', '_':
			if i == 0 {
				return ""
			}
			return pattern[:i]
		}
	}
	// No wildcards — the entire pattern is a usable prefix.
	return pattern
}

// encodeIndexValue converts a literal expression into the byte
// form used by the index. Returns (value, true) on success.
func encodeIndexValue(e PS.Expr) ([]byte, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		b := make([]byte, 8)
		u := uint64(v.Val)
		b[7] = byte(u)
		b[6] = byte(u >> 8)
		b[5] = byte(u >> 16)
		b[4] = byte(u >> 24)
		b[3] = byte(u >> 32)
		b[2] = byte(u >> 40)
		b[1] = byte(u >> 48)
		b[0] = byte(u >> 56)
		return b, true
	case *PS.StringLiteral:
		return []byte(v.Val), true
	case *PS.BoolLiteral:
		if v.Val {
			return []byte{1}, true
		}
		return []byte{0}, true
	}
	return nil, false
}

func limitInt64(e PS.Expr) (int64, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		if v.Val < 0 {
			return 0, false
		}
		return v.Val, true
	case *PS.Param:
		_ = v
	}
	return 0, false
}
