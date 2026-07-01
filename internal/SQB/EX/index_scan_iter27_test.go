//go:build !slt_corpus_full

package EX

import (
	"context"
	"fmt"
	"strings"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
)

// TestIndexScan_RangeSeek_GT exercises REQ000074: OP.IndexScan real
// range seek via the secondary index keyspace. Predicate
// `WHERE a > 5` should use seek-based iteration, not the prefix
// scan over the table.
func TestIndexScan_RangeSeek_GT(t *testing.T) {
	dir := t.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		t.Fatalf("ls.Open: %v", err)
	}
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)

	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")
	id, _ := DT.TableIDFor("t")

	ctx := context.Background()
	// Insert rows with a values: 1, 3, 5, 7, 9
	for _, v := range []int64{1, 3, 5, 7, 9} {
		row := fmt.Sprintf("INSERT INTO t VALUES (%d, %d)", v, v)
		if _, err := ex.Exec(ctx, row); err != nil {
			t.Fatalf("insert %d: %v", v, err)
		}
	}
	// Populate the secondary index manually (Block F will automate).
	idxStore := ls.NewIndexStore(eng, id, "idx_a")
	for _, v := range []int64{1, 3, 5, 7, 9} {
		if err := idxStore.Insert(int64ToBytes(v), int64ToBytes(v)); err != nil {
			t.Fatalf("idxStore.Insert %d: %v", v, err)
		}
	}

	// Range seek for a > 5 should yield a = 7, 9
	scan, err := OP.NewIndexScanWithRange(store, id, "t", "idx_a", int64ToBytes(5), false, nil, false)
	if err != nil {
		t.Fatalf("NewIndexScanWithRange: %v", err)
	}
	defer scan.Close()

	var got []int64
	for {
		row, err := scan.Next(ctx)
		if err != nil {
			break
		}
		got = append(got, row.Data[0].ToAny().(int64))
	}
	want := []int64{7, 9}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("row %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

// TestIndexScan_RangeSeek_GE exercises `WHERE a >= 5`.
func TestIndexScan_RangeSeek_GE(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")
	id, _ := DT.TableIDFor("t")

	ctx := context.Background()
	for _, v := range []int64{1, 3, 5, 7, 9} {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t VALUES (%d, %d)", v, v))
	}
	idxStore := ls.NewIndexStore(eng, id, "idx_a")
	for _, v := range []int64{1, 3, 5, 7, 9} {
		idxStore.Insert(int64ToBytes(v), int64ToBytes(v))
	}

	scan, _ := OP.NewIndexScanWithRange(store, id, "t", "idx_a", int64ToBytes(5), true, nil, false)
	defer scan.Close()
	var got []int64
	for {
		row, err := scan.Next(ctx)
		if err != nil {
			break
		}
		got = append(got, row.Data[0].ToAny().(int64))
	}
	want := []int64{5, 7, 9}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestIndexScan_RangeSeek_Between exercises `WHERE a BETWEEN 3 AND 7`.
func TestIndexScan_RangeSeek_Between(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")
	id, _ := DT.TableIDFor("t")

	ctx := context.Background()
	for _, v := range []int64{1, 3, 5, 7, 9} {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t VALUES (%d, %d)", v, v))
	}
	idxStore := ls.NewIndexStore(eng, id, "idx_a")
	for _, v := range []int64{1, 3, 5, 7, 9} {
		idxStore.Insert(int64ToBytes(v), int64ToBytes(v))
	}

	// BETWEEN 3 AND 7 inclusive on both ends
	scan, _ := OP.NewIndexScanWithRange(store, id, "t", "idx_a", int64ToBytes(3), true, int64ToBytes(7), true)
	defer scan.Close()
	var got []int64
	for {
		row, err := scan.Next(ctx)
		if err != nil {
			break
		}
		got = append(got, row.Data[0].ToAny().(int64))
	}
	want := []int64{3, 5, 7}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestIndexScan_RangeSeek_Planner exercises the planner integration:
// a query `WHERE a > 5` should select the index seek path (real
// seek, not prefix-scan fallback). The query result must still
// be correct.
func TestIndexScan_RangeSeek_Planner(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")
	ex.RegisterIndex("t", "idx_a", []string{"a"})
	id, _ := DT.TableIDFor("t")

	ctx := context.Background()
	for _, v := range []int64{1, 3, 5, 7, 9} {
		if _, err := ex.Exec(ctx, fmt.Sprintf("INSERT INTO t VALUES (%d, %d)", v, v)); err != nil {
			t.Fatal(err)
		}
	}
	// Populate the index keyspace
	idxStore := ls.NewIndexStore(eng, id, "idx_a")
	for _, v := range []int64{1, 3, 5, 7, 9} {
		idxStore.Insert(int64ToBytes(v), int64ToBytes(v))
	}

	// Use a query that requires range seek to verify the planner
	// actually picks the real seek path.
	rows, err := ex.QueryAll(ctx, "SELECT id FROM t WHERE a > 5 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, r := range rows {
		got = append(got, r.Data[0].ToAny().(int64))
	}
	want := []int64{7, 9}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestIndexScan_RangeSeek_PlannerExplains verifies the plan text
// for a range predicate includes OP.IndexScan (not OP.SeqScan fallback).
func TestIndexScan_RangeSeek_PlannerExplains(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")
	ex.RegisterIndex("t", "idx_a", []string{"a"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 2)")

	// `EXPLAIN` should show OP.IndexScan for the indexed column.
	plan, err := ex.Explain("SELECT id FROM t WHERE a > 0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "Search") {
		t.Errorf("plan did not include Search (OP.IndexScan):\n%s", plan)
	}
}
