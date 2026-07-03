//go:build debug

package JD

import (
	"fmt"
	"testing"
)

func TestJoinDebugE2E(t *testing.T) {
	tr := NewBufferedTracer(64, LevelFull)

	tr.Strategy("HashJoin", "hash", "equi-join", 42.0)
	tr.RowFlow("HashJoin", "t1", 1, true)
	tr.RowFlow("HashJoin", "t1", 1, false)
	tr.Predicate("HashJoin", "t1.a = t2.b", 1, 5, true)
	tr.ColumnOffset("HashJoin", 0, 1, "t1.id")

	events := tr.Flush()
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}

	kinds := map[EventType]int{}
	for _, e := range events {
		kinds[e.Type]++
	}
	if kinds[EventStrategy] != 1 {
		t.Fatalf("expected 1 Strategy, got %d", kinds[EventStrategy])
	}
	if kinds[EventRowFlow] != 2 {
		t.Fatalf("expected 2 RowFlow, got %d", kinds[EventRowFlow])
	}
	if kinds[EventPredicate] != 1 {
		t.Fatalf("expected 1 Predicate, got %d", kinds[EventPredicate])
	}
	if kinds[EventColumnOffset] != 1 {
		t.Fatalf("expected 1 ColumnOffset, got %d", kinds[EventColumnOffset])
	}
}

func TestJoinDebugMultiTable(t *testing.T) {
	tr := NewBufferedTracer(64, LevelFull)

	// Simulate a 3-table join with correlation tracking at stage 0.
	tables := []string{"orders", "lineitem", "customer"}

	tr.Correlation(0, tables, 1500)
	tr.Correlation(1, tables[:2], 800)
	tr.Correlation(2, tables[:1], 100)

	// Simulate row flows across the 3 tables.
	for _, tbl := range tables {
		for i := uint64(0); i < 3; i++ {
			tr.RowFlow("MergeJoin", tbl, i, true)
			tr.RowFlow("MergeJoin", tbl, i, false)
		}
	}

	// 3 correlations + 3 tables * 3 rows * 2 (entering+exiting) = 3 + 18 = 21
	expected := 3 + len(tables)*3*2
	events := tr.Flush()
	if len(events) != expected {
		t.Fatalf("expected %d events, got %d", expected, len(events))
	}

	corrCount := 0
	for _, e := range events {
		if e.Type == EventCorrelation {
			corrCount++
		}
	}
	if corrCount != 3 {
		t.Fatalf("expected 3 Correlation events, got %d", corrCount)
	}

	// Print summary for verbose output.
	fmt.Printf("Multi-table join: %d events, %d correlations\n", len(events), corrCount)
}
