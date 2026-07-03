package OP

import (
	"testing"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// TestREQ001190_HashJoinCloseResetsMatched verifies that HashJoin.Close()
// resets leftMatched and matchedRight slices to nil. This prevents
// state leakage if the operator is reused across queries.
func TestREQ001190_HashJoinCloseResetsMatched(t *testing.T) {
	j := &HashJoin{
		partitions:   16,
		buckets:      make([]hashBucket, 16),
		leftMatched:  []bool{true, false, true},
		matchedRight: [][]bool{{true, false}, {false}},
		leftRows:     []pl.Row{{Data: []pl.Value{}}},
		done:         true,
		built:        true,
	}

	err := j.Close()
	if err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	if j.leftMatched != nil {
		t.Error("leftMatched should be nil after Close()")
	}
	if j.matchedRight != nil {
		t.Error("matchedRight should be nil after Close()")
	}
	if j.done != false {
		t.Error("done should be false after Close()")
	}
	if j.built != false {
		t.Error("built should be false after Close()")
	}
	if j.leftRows != nil {
		t.Error("leftRows should be nil after Close()")
	}
	if j.leftInfos != nil {
		t.Error("leftInfos should be nil after Close()")
	}

	for i, b := range j.buckets {
		if len(b.rightRows) != 0 {
			t.Errorf("bucket %d rightRows not reset", i)
		}
		if len(b.hashes) != 0 {
			t.Errorf("bucket %d hashes not reset", i)
		}
	}
}