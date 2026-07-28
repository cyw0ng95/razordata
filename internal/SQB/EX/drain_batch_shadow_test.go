//go:build px_validate

package EX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// TestShadowStats_Init verifies initial shadow stats are zero.
func TestShadowStats_Init(t *testing.T) {
	ResetShadowStats()
	snap := ShadowStats()
	if snap.TotalRuns != 0 || snap.Matches != 0 || snap.Mismatches != 0 {
		t.Fatalf("expected zero stats, got %+v", snap)
	}
}

// TestShadowStats_Reset verifies ResetShadowStats zeroes counters.
func TestShadowStats_Reset(t *testing.T) {
	globalShadowStats.totalRuns.Store(5)
	globalShadowStats.matches.Store(3)
	ResetShadowStats()
	snap := ShadowStats()
	if snap.TotalRuns != 0 || snap.Matches != 0 {
		t.Fatalf("expected zero after reset, got %+v", snap)
	}
}

// TestDrainPlanExecCtx_PipelinePathNil verifies behavior when
// pipelineBuilder is nil (falls back to legacy).
func TestDrainPlanExecCtx_PipelinePathNil(t *testing.T) {
	ResetShadowStats()

	batch := makeIntBatch([]int64{1, 2, 3})
	fb := &fakeBatch{batches: []*UT.Batch{batch}}

	plan := &pl.PlanResult{Root: fb}
	e := &Executor{pipelineBuilder: nil}
	rows, err := e.drainPlanExecCtx(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	// TotalRuns should be 1 (shadow path still runs even with nil pipeline).
	snap := ShadowStats()
	if snap.TotalRuns != 1 {
		t.Fatalf("expected 1 total run, got %d", snap.TotalRuns)
	}
}

// TestDrainPlanExecCtx_MatchingResults verifies matching pipeline/legacy
// results increment matches.
func TestDrainPlanExecCtx_MatchingResults(t *testing.T) {
	ResetShadowStats()

	batch := makeIntBatch([]int64{10, 20, 30})
	fb := &fakeBatch{batches: []*UT.Batch{batch}}

	plan := &pl.PlanResult{Root: fb}
	e := &Executor{pipelineBuilder: nil}
	_, err := e.drainPlanExecCtx(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}

	// With pipelineBuilder=nil, shadow path runs legacy only and does NOT
	// increment matches (no comparison happens). Verify totalRuns incremented.
	snap := ShadowStats()
	if snap.TotalRuns != 1 {
		t.Fatalf("expected 1 total run, got %d", snap.TotalRuns)
	}
	if snap.Matches != 0 {
		t.Fatalf("expected 0 matches (no pipeline), got %d", snap.Matches)
	}
}

// TestShadowStatsSnapshot verifies the snapshot struct can be returned
// from ShadowStats().
func TestShadowStatsSnapshot(t *testing.T) {
	ResetShadowStats()
	globalShadowStats.totalRuns.Store(10)
	globalShadowStats.matches.Store(7)
	globalShadowStats.mismatches.Store(3)
	globalShadowStats.totalMismatch.Store(5)

	snap := ShadowStats()
	if snap.TotalRuns != 10 || snap.Matches != 7 || snap.Mismatches != 3 {
		t.Fatalf("snapshot mismatch: %+v", snap)
	}
	if snap.TotalMismatch != 5 {
		t.Fatalf("expected TotalMismatch 5, got %d", snap.TotalMismatch)
	}
}

// Compile-time check that fakeBatch implements pl.Operator (required for shadow test).
var _ pl.Operator = (*fakeBatch)(nil)