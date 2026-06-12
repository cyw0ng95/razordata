//go:build !slt_corpus

package EX

import (
	"testing"
)

// ResetForTest clears the package-level tables and schemas
// maps and arranges a Cleanup that re-clears after the test
// returns. Tests that call RegisterTable, RegisterTableSchema,
// or RegisterTableWithPK MUST call ResetForTest at the top
// of their Test* function to remain safe under
// `go test -count=N`. See REQ000346 (iter-26).
//
// The pre-iter-26 code relied on package init order and
// distinct test names to avoid bleed-through; under -count=N
// the maps accumulated entries from previous invocations and
// assertions like `len(idxs) == 1` saw the running total.
//
// Calling ResetForTest twice in the same test is safe (it
// runs the cleanup on test exit but the second Cleanup is a
// no-op against an already-empty map).
func ResetForTest(t testing.TB) {
	t.Helper()
	UnregisterAll()
	t.Cleanup(UnregisterAll)
}

func TestResetForTest_Reentrant(t *testing.T) {
	ResetForTest(t)
	ResetForTest(t)
	// No assertion needed; the function is expected to be
	// idempotent and a panic would be caught by the test
	// runner.
}
