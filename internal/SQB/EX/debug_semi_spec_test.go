//go:build !slt_corpus

package EX

import (
	"context"
	"testing"

	PX "github.com/cyw0ng95/razordata/internal/SQB/PX"
)

// TestREQ002152_SemiJoinPipelineSpec verifies that a correlated EXISTS
// subquery is decomposed into a native SemiJoinStageSpec (not a
// LegacyBatchStageSpec fallback) and that executing the pipeline yields
// the correct probe-side rows. REQ002152.
func TestREQ002152_SemiJoinPipelineSpec(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	ex.RegisterTableWithPK("t1", []string{"id", "val"}, "id")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (3, 30)")

	ex.RegisterTableWithPK("t2", []string{"id", "tid", "name"}, "id")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (10, 1, 'a')")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (20, 1, 'b')")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (30, 2, 'c')")

	sql := "SELECT id FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.tid = t1.id) ORDER BY id"

	// Build the pipeline spec and assert a SemiJoinStageSpec is present
	// (not a LegacyBatchStageSpec fallback).
	spec, err := ex.BuildPipeline(sql)
	if err != nil {
		t.Fatalf("BuildPipeline: %v", err)
	}
	if spec == nil {
		t.Fatal("spec is nil")
	}
	hasSemi := false
	for _, s := range spec.Stages {
		if _, ok := s.(*PX.SemiJoinStageSpec); ok {
			hasSemi = true
			break
		}
	}
	if !hasSemi {
		t.Fatalf("expected a SemiJoinStageSpec in pipeline stages, got %T", spec.Stages)
	}

	// Execute via the pure pipeline fast path and verify correctness.
	// t2.tid=1 matches t1.id=1, t2.tid=2 matches t1.id=2 → rows {1, 2}.
	rows, err := ex.QueryAll(ctx, sql)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2; data=%v", len(rows), rows)
	}
	want := []int64{1, 2}
	for i, w := range want {
		if rows[i].Data[0].I64 != w {
			t.Errorf("row %d: got %d, want %d", i, rows[i].Data[0].I64, w)
		}
	}
}
