package dual

import (
	"context"
	"fmt"

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

// RunRazorOnly runs a case on Razordata only and compares the
// result against c.Want.  No oracle/sqlite comparison.  This is
// used for Razordata-specific extensions that sqlite does not
// support or where the two engines intentionally diverge.
func RunRazorOnly(ctx context.Context, c dualCase) (Verdict, string) {
	razorSide, err := runRazor(ctx, c)
	if err != nil {
		if isUnsupported(err) {
			return VerdictSkipped, err.Error()
		}
		return VerdictFailed, fmt.Sprintf("razor: %v", err)
	}
	if !resultSetEqual(razorSide, c.Want) {
		return VerdictFailed, fmt.Sprintf("mismatch:\n  razor=%v\n  want=%v", razorSide, c.Want)
	}
	return VerdictPassed, ""
}
