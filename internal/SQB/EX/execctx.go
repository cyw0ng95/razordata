package EX

import pl "github.com/cyw0ng95/razordata/internal/SQF/PL"

// ExecContext is the per-execution state carrier. Aliased from PL.
type ExecContext = pl.ExecContext

// WithExecContext attaches an ExecContext to a Row's Outer chain
// so subquery eval functions can retrieve it without a global.
func WithExecContext(row *Row, ctx *ExecContext) *Row {
	if row == nil {
		return nil
	}
	row.ExecCtx = ctx
	return row
}

// ExecContextFromRow walks the Row outer chain and returns the
// first ExecContext found, or nil.
func ExecContextFromRow(row *Row) *ExecContext {
	for cur := row; cur != nil; cur = cur.Outer {
		if cur.ExecCtx != nil {
			return cur.ExecCtx
		}
	}
	return nil
}
