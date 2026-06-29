package OP

import pl "github.com/cyw0ng95/razordata/internal/SQF/PL"

// WithExecContext attaches an ExecContext to a Row's Outer chain.
func WithExecContext(row *pl.Row, ctx *pl.ExecContext) *pl.Row {
	if row == nil {
		return nil
	}
	row.ExecCtx = ctx
	return row
}

// ExecContextFromRow walks the Row outer chain and returns the
// first ExecContext found, or nil.
func ExecContextFromRow(row *pl.Row) *pl.ExecContext {
	for cur := row; cur != nil; cur = cur.Outer {
		if cur.ExecCtx != nil {
			return cur.ExecCtx
		}
	}
	return nil
}
