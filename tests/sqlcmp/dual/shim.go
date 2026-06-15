package dual

import (
	"context"

	"github.com/cyw0ng95/razordata/tests/sqlcmp/slt"
)

// razorDriverShim is a small adapter that lets the dual
// package call into the slt driver without importing slt
// from this file (which would create a cycle if we ever add
// more slt-internal helpers).
type razorDriverShim = slt.RazorDriver

func newRazorDriverShim() *slt.RazorDriver {
	return slt.NewRazorDriver()
}

// RunOne is the single-case entry point. It is exposed for
// table-driven tests that need fine-grained control.
func RunOne(ctx context.Context, c dualCase) (Verdict, string) {
	return runOne(ctx, c)
}
