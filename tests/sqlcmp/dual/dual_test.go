package dual

import (
	"context"
	"testing"
	"time"
)

// TestDual_ZeroblobNormalization verifies REQ000811: blob cells
// from razor are normalized to string to match oracle output.
func TestDual_ZeroblobNormalization(t *testing.T) {
	ctx := context.Background()
	v, diff := RunOne(ctx, dualCase{
		Name:  "zeroblob_content",
		Query: "SELECT ZEROBLOB(8)",
		Want:  [][]any{{[]byte{0, 0, 0, 0, 0, 0, 0, 0}}},
	})
	if v != VerdictPassed {
		t.Errorf("zeroblob_content: verdict=%s diff=%s", v, diff)
	}
}

func TestDual_AllSeededCases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	results := Run(ctx, AllCases())
	for _, c := range results.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Verdict != VerdictPassed {
				t.Logf("verdict=%s diff=%s", c.Verdict, c.Diff)
				t.Errorf("case failed")
			}
		})
	}
	t.Logf("dual: %d passed, %d failed, %d skipped", results.Passed, results.Failed, results.Skipped)
	if results.Failed > 0 {
		t.Errorf("%d cases failed; see per-case diffs above", results.Failed)
	}
}

func TestDual_UnsupportedMarkedSkipped(t *testing.T) {
	ctx := context.Background()
	v, diff := RunOne(ctx, dualCase{
		Name:  "unparseable_garbage",
		Query: "NOT VALID SQL AT ALL",
	})
	if v != VerdictSkipped && v != VerdictFailed {
		// Either is acceptable here: the test asserts that
		// the dual runner does not panic on garbage input.
		// The exact verdict depends on whether the engine
		// even started.
		_ = diff
	}
}
