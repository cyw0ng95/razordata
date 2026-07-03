//go:build debug

package JD

import (
	"fmt"
	"testing"
)

// event is a minimal record kept by BufferedTracer for testing.
type event struct {
	Kind string
}

// BufferedTracer implements JoinTracer and records events for verification.
type BufferedTracer struct {
	Events []event
}

func (b *BufferedTracer) RowFlow(operator string, table string, rowID uint64, entering bool) {
	b.Events = append(b.Events, event{Kind: "RowFlow"})
}

func (b *BufferedTracer) Predicate(operator string, expr string, leftRowID, rightRowID uint64, passed bool) {
	b.Events = append(b.Events, event{Kind: "Predicate"})
}

func (b *BufferedTracer) ColumnOffset(operator string, expected, actual int, colName string) {
	b.Events = append(b.Events, event{Kind: "ColumnOffset"})
}

func (b *BufferedTracer) Strategy(operator string, chosen string, reason string, estimatedCost float64) {
	b.Events = append(b.Events, event{Kind: "Strategy"})
}

func (b *BufferedTracer) Correlation(stage int, tables []string, rowCount int64) {
	b.Events = append(b.Events, event{Kind: "Correlation"})
}

func TestJoinDebugE2E(t *testing.T) {
	bt := &BufferedTracer{}

	bt.Strategy("HashJoin", "hash", "equi-join", 42.0)
	bt.RowFlow("HashJoin", "t1", 1, true)
	bt.RowFlow("HashJoin", "t1", 1, false)
	bt.Predicate("HashJoin", "t1.a = t2.b", 1, 5, true)
	bt.ColumnOffset("HashJoin", 0, 1, "t1.id")

	if len(bt.Events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(bt.Events))
	}

	kinds := map[string]int{}
	for _, e := range bt.Events {
		kinds[e.Kind]++
	}
	if kinds["Strategy"] != 1 {
		t.Fatalf("expected 1 Strategy, got %d", kinds["Strategy"])
	}
	if kinds["RowFlow"] != 2 {
		t.Fatalf("expected 2 RowFlow, got %d", kinds["RowFlow"])
	}
	if kinds["Predicate"] != 1 {
		t.Fatalf("expected 1 Predicate, got %d", kinds["Predicate"])
	}
	if kinds["ColumnOffset"] != 1 {
		t.Fatalf("expected 1 ColumnOffset, got %d", kinds["ColumnOffset"])
	}
}

func TestJoinDebugMultiTable(t *testing.T) {
	bt := &BufferedTracer{}

	// Simulate a 3-table join with correlation tracking at stage 0.
	tables := []string{"orders", "lineitem", "customer"}

	bt.Correlation(0, tables, 1500)
	bt.Correlation(1, tables[:2], 800)
	bt.Correlation(2, tables[:1], 100)

	// Simulate row flows across the 3 tables.
	for _, tbl := range tables {
		for i := uint64(0); i < 3; i++ {
			bt.RowFlow("MergeJoin", tbl, i, true)
			bt.RowFlow("MergeJoin", tbl, i, false)
		}
	}

	// 3 correlations + 3 tables * 3 rows * 2 (entering+exiting) = 3 + 18 = 21
	expected := 3 + len(tables)*3*2
	if len(bt.Events) != expected {
		t.Fatalf("expected %d events, got %d", expected, len(bt.Events))
	}

	corrCount := 0
	for _, e := range bt.Events {
		if e.Kind == "Correlation" {
			corrCount++
		}
	}
	if corrCount != 3 {
		t.Fatalf("expected 3 Correlation events, got %d", corrCount)
	}

	// Print summary for verbose output.
	fmt.Printf("Multi-table join: %d events, %d correlations\n", len(bt.Events), corrCount)
}
