package EX

// ExecContext carries per-execution state through the operator tree,
// replacing package-level globals that made concurrent Executor
// instances unsafe (REQ000586).
type ExecContext struct {
	Planner   *Planner
	SessionID uint64
	TxWriter  TxWriter
	// subqueryCache caches results of non-correlated scalar subqueries.
	// Keyed by plan hash; value is the scalar result (any).
	// Eliminates O(N) subquery re-evaluations for N-row result sets.
	subqueryCache map[string]any
}

var execCtxKey = struct{}{}

// WithExecContext attaches an ExecContext to a Row's Outer chain
// so subquery eval functions can retrieve it without a global.
func WithExecContext(row *Row, ctx *ExecContext) *Row {
	if row == nil {
		return nil
	}
	row.execCtx = ctx
	return row
}

// ExecContextFromRow walks the Row outer chain and returns the
// first ExecContext found, or nil.
func ExecContextFromRow(row *Row) *ExecContext {
	for cur := row; cur != nil; cur = cur.Outer {
		if cur.execCtx != nil {
			return cur.execCtx
		}
	}
	return nil
}

// GetSubqueryCache returns the subquery result cache from the ExecContext.
// Creates the cache map on first access.
func (ec *ExecContext) GetSubqueryCache() map[string]any {
	if ec.subqueryCache == nil {
		ec.subqueryCache = make(map[string]any)
	}
	return ec.subqueryCache
}
