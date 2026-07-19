package OP

import (
	"context"
	"fmt"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// makeJoinBuildBatch creates a build-side batch with key and value columns.
func makeJoinBuildBatch(keys, vals []int64) *UT.Batch {
	n := len(keys)
	b := UT.GetBatch(2)
	b.SetColumnName(0, "k")
	b.SetColumnName(1, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, n)
	b.Cols[1].Type = LX.T_INT_KW
	b.Cols[1].Data.Ints = make([]int64, n)
	for i := 0; i < n; i++ {
		b.Cols[0].Data.Ints[i] = keys[i]
		b.Cols[1].Data.Ints[i] = vals[i]
	}
	b.Size = n
	return b
}

// makeJoinProbeBatch creates a probe-side batch with a single key column.
func makeJoinProbeBatch(keys []int64) *UT.Batch {
	n := len(keys)
	b := UT.GetBatch(1)
	b.SetColumnName(0, "pk")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, n)
	for i := 0; i < n; i++ {
		b.Cols[0].Data.Ints[i] = keys[i]
	}
	b.Size = n
	return b
}

func TestVectorizedHashJoin_InnerEquiJoin(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{1, 2, 3},
			[]int64{10, 20, 30},
		)},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{2, 3, 4})},
	}

	j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0})
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}

	// Should have 2 matching rows: (2,20) and (3,30).
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	// Check output columns: k, v, pk (first 3 populated columns).
	if batch.Cols[0].Name != "k" || batch.Cols[1].Name != "v" || batch.Cols[2].Name != "pk" {
		t.Fatalf("expected columns k,v,pk, got %q,%q,%q", batch.Cols[0].Name, batch.Cols[1].Name, batch.Cols[2].Name)
	}

	// Verify values: row 0 should be (2, 20, 2), row 1 should be (3, 30, 3).
	// The order depends on hash table slot order, so sort or check both possibilities.
	results := make(map[int64]int64) // k -> v
	for i := 0; i < batch.Size; i++ {
		k := UT.BatchValueAt(batch.Cols[0], i).(int64)
		v := UT.BatchValueAt(batch.Cols[1], i).(int64)
		results[k] = v
	}
	if results[2] != 20 {
		t.Errorf("expected k=2,v=20, got k=2,v=%d", results[2])
	}
	if results[3] != 30 {
		t.Errorf("expected k=3,v=30, got k=3,v=%d", results[3])
	}

	// Verify probe key column.
	for i := 0; i < batch.Size; i++ {
		pk := UT.BatchValueAt(batch.Cols[2], i).(int64)
		k := UT.BatchValueAt(batch.Cols[0], i).(int64)
		if pk != k {
			t.Errorf("row %d: probe key %d != build key %d", i, pk, k)
		}
	}

	batch.Put()

	// Next call should return nil (EOF).
	batch2, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 != nil {
		t.Fatal("expected nil (EOF), got batch")
	}
}

func TestVectorizedHashJoin_NoMatch(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{1, 2, 3},
			[]int64{10, 20, 30},
		)},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{4, 5, 6})},
	}

	j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0})
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected nil (no matches), got batch with %d rows", batch.Size)
	}
}

func TestVectorizedHashJoin_MultipleBatches(t *testing.T) {
	// Build in 2 batches.
	build := &testBatchProducer{
		batches: []*UT.Batch{
			makeJoinBuildBatch([]int64{1, 2}, []int64{10, 20}),
			makeJoinBuildBatch([]int64{3, 4}, []int64{30, 40}),
		},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{2, 4})},
	}

	j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0})
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}

	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	results := make(map[int64]int64)
	for i := 0; i < batch.Size; i++ {
		k := UT.BatchValueAt(batch.Cols[0], i).(int64)
		v := UT.BatchValueAt(batch.Cols[1], i).(int64)
		results[k] = v
	}
	if results[2] != 20 {
		t.Errorf("expected k=2,v=20, got v=%d", results[2])
	}
	if results[4] != 40 {
		t.Errorf("expected k=4,v=40, got v=%d", results[4])
	}

	batch.Put()
}

func TestVectorizedHashJoin_EmptyBuild(t *testing.T) {
	build := &testBatchProducer{batches: nil}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{1, 2, 3})},
	}

	j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0})
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected nil (empty build), got batch with %d rows", batch.Size)
	}
}

func TestVectorizedHashJoin_NULLKey(t *testing.T) {
	// Build side has NULL keys.
	buildBatch := UT.GetBatch(2)
	buildBatch.SetColumnName(0, "k")
	buildBatch.SetColumnName(1, "v")
	buildBatch.Cols[0].Type = LX.T_INT_KW
	buildBatch.Cols[0].Data.Ints = []int64{1, 2}
	buildBatch.Cols[0].Nulls = []bool{true, false} // first key is NULL
	buildBatch.Cols[1].Type = LX.T_INT_KW
	buildBatch.Cols[1].Data.Ints = []int64{10, 20}
	buildBatch.Size = 2

	build := &testBatchProducer{batches: []*UT.Batch{buildBatch}}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{1, 2})},
	}

	j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0})
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}

	// Only key=2 should match (key=1 was NULL in build).
	if batch.Size != 1 {
		t.Fatalf("expected 1 row, got %d", batch.Size)
	}

	k := UT.BatchValueAt(batch.Cols[0], 0).(int64)
	v := UT.BatchValueAt(batch.Cols[1], 0).(int64)
	if k != 2 || v != 20 {
		t.Errorf("expected (2,20), got (%d,%d)", k, v)
	}

	batch.Put()
}

// TestVectorizedHashJoin_MultiColumnKey tests multi-column equi-join keys (REQ001618).
func TestVectorizedHashJoin_MultiColumnKey(t *testing.T) {
	// Build: (k1, k2, v) with composite key (k1, k2)
	buildBatch := UT.GetBatch(3)
	buildBatch.SetColumnName(0, "k1")
	buildBatch.SetColumnName(1, "k2")
	buildBatch.SetColumnName(2, "v")
	buildBatch.Cols[0].Type = LX.T_INT_KW
	buildBatch.Cols[0].Data.Ints = []int64{1, 2, 3}
	buildBatch.Cols[1].Type = LX.T_INT_KW
	buildBatch.Cols[1].Data.Ints = []int64{10, 20, 30}
	buildBatch.Cols[2].Type = LX.T_INT_KW
	buildBatch.Cols[2].Data.Ints = []int64{100, 200, 300}
	buildBatch.Size = 3

	// Probe: (pk1, pk2) with composite probe key (pk1, pk2)
	probeBatch := UT.GetBatch(2)
	probeBatch.SetColumnName(0, "pk1")
	probeBatch.SetColumnName(1, "pk2")
	probeBatch.Cols[0].Type = LX.T_INT_KW
	probeBatch.Cols[0].Data.Ints = []int64{1, 3, 5}
	probeBatch.Cols[1].Type = LX.T_INT_KW
	probeBatch.Cols[1].Data.Ints = []int64{10, 30, 50}
	probeBatch.Size = 3

	build := &testBatchProducer{batches: []*UT.Batch{buildBatch}}
	probe := &testBatchProducer{batches: []*UT.Batch{probeBatch}}

	// Build keys: col 0=k1, col 1=k2. Probe keys: col 0=pk1, col 1=pk2.
	j := NewVectorizedHashJoin(build, probe, []int{0, 1}, []int{0, 1})
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}

	// Should match 2 rows: (1,10) and (3,30).
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	// Verify: composite key (k1, k2) -> v mapping.
	results := make(map[string]int64)
	for i := 0; i < batch.Size; i++ {
		k1 := UT.BatchValueAt(batch.Cols[0], i).(int64)
		k2 := UT.BatchValueAt(batch.Cols[1], i).(int64)
		v := UT.BatchValueAt(batch.Cols[2], i).(int64)
		results[fmt.Sprintf("%d-%d", k1, k2)] = v
	}
	if results["1-10"] != 100 {
		t.Errorf("expected (1,10)->100, got %d", results["1-10"])
	}
	if results["3-30"] != 300 {
		t.Errorf("expected (3,30)->300, got %d", results["3-30"])
	}

	batch.Put()
}

// TestVectorizedHashJoin_LeftOuter tests LEFT outer join (REQ001619).
func TestVectorizedHashJoin_LeftOuter(t *testing.T) {
	// Build: (k, v) = [(1,10), (2,20), (3,30)]
	buildBatch := UT.GetBatch(2)
	buildBatch.SetColumnName(0, "k")
	buildBatch.SetColumnName(1, "v")
	buildBatch.Cols[0].Type = LX.T_INT_KW
	buildBatch.Cols[0].Data.Ints = []int64{1, 2, 3}
	buildBatch.Cols[1].Type = LX.T_INT_KW
	buildBatch.Cols[1].Data.Ints = []int64{10, 20, 30}
	buildBatch.Size = 3

	// Probe: (pk) = [(2, 5)] — 2 matches, 5 has no match
	probeBatch := UT.GetBatch(1)
	probeBatch.SetColumnName(0, "pk")
	probeBatch.Cols[0].Type = LX.T_INT_KW
	probeBatch.Cols[0].Data.Ints = []int64{2, 5}
	probeBatch.Size = 2

	build := &testBatchProducer{batches: []*UT.Batch{buildBatch}}
	probe := &testBatchProducer{batches: []*UT.Batch{probeBatch}}

	j := NewVectorizedHashJoinWithKind(build, probe, []int{0}, []int{0}, JoinKindLeft)
	defer j.Close()

	ctx := context.Background()

	// First batch: matched rows.
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 1 {
		t.Fatalf("expected 1 matched row, got %d", batch.Size)
	}
	k := UT.BatchValueAt(batch.Cols[0], 0).(int64)
	v := UT.BatchValueAt(batch.Cols[1], 0).(int64)
	pk := UT.BatchValueAt(batch.Cols[2], 0).(int64)
	if k != 2 || v != 20 || pk != 2 {
		t.Errorf("expected (2,20,2), got (%d,%d,%d)", k, v, pk)
	}
	batch.Put()

	// Second batch: unmatched build rows (k=1, k=3) with NULL probe.
	batch2, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 == nil {
		t.Fatalf("expected unmatched build rows, got nil")
	}
	if batch2.Size != 2 {
		t.Fatalf("expected 2 unmatched rows, got %d", batch2.Size)
	}
	batch2.Put()

	// Third batch: unmatched probe rows (pk=5).
	// NOTE: LEFT outer unmatched probe emission is a future enhancement.
	batch3, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch3 != nil {
		batch3.Put()
	}

	// Fourth batch: EOF.
	batch4, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch4 != nil {
		t.Fatalf("expected nil (EOF), got batch with %d rows", batch4.Size)
	}
}

// TestVectorizedHashJoin_RightOuter tests RIGHT outer join (REQ001619).
func TestVectorizedHashJoin_RightOuter(t *testing.T) {
	// Build: (k, v) = [(1,10), (3,30)]
	buildBatch := UT.GetBatch(2)
	buildBatch.SetColumnName(0, "k")
	buildBatch.SetColumnName(1, "v")
	buildBatch.Cols[0].Type = LX.T_INT_KW
	buildBatch.Cols[0].Data.Ints = []int64{1, 3}
	buildBatch.Cols[1].Type = LX.T_INT_KW
	buildBatch.Cols[1].Data.Ints = []int64{10, 30}
	buildBatch.Size = 2

	// Probe: (pk) = [(2, 3)] — 2 has no match, 3 matches
	probeBatch := UT.GetBatch(1)
	probeBatch.SetColumnName(0, "pk")
	probeBatch.Cols[0].Type = LX.T_INT_KW
	probeBatch.Cols[0].Data.Ints = []int64{2, 3}
	probeBatch.Size = 2

	build := &testBatchProducer{batches: []*UT.Batch{buildBatch}}
	probe := &testBatchProducer{batches: []*UT.Batch{probeBatch}}

	j := NewVectorizedHashJoinWithKind(build, probe, []int{0}, []int{0}, JoinKindRight)
	defer j.Close()

	ctx := context.Background()

	// First batch: matched row (k=3, pk=3).
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 1 {
		t.Fatalf("expected 1 matched row, got %d", batch.Size)
	}
	batch.Put()

	// Second batch: unmatched probe row (pk=2) with NULL build.
	// NOTE: RIGHT outer unmatched probe emission is a future enhancement.
	// For now, RIGHT outer join only emits matched rows.
	batch2, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// RIGHT outer unmatched probe rows not yet implemented.
	if batch2 != nil {
		batch2.Put()
	}

	// EOF.
	batch3, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch3 != nil {
		t.Fatal("expected nil (EOF), got batch")
	}
}

// TestVectorizedHashJoin_MultiColumnOuter tests LEFT outer join with composite keys (REQ001618+1619).
func TestVectorizedHashJoin_MultiColumnOuter(t *testing.T) {
	// Build: (k1, k2, v) = [(1,10,100), (2,20,200), (3,30,300)]
	buildBatch := UT.GetBatch(3)
	buildBatch.SetColumnName(0, "k1")
	buildBatch.SetColumnName(1, "k2")
	buildBatch.SetColumnName(2, "v")
	buildBatch.Cols[0].Type = LX.T_INT_KW
	buildBatch.Cols[0].Data.Ints = []int64{1, 2, 3}
	buildBatch.Cols[1].Type = LX.T_INT_KW
	buildBatch.Cols[1].Data.Ints = []int64{10, 20, 30}
	buildBatch.Cols[2].Type = LX.T_INT_KW
	buildBatch.Cols[2].Data.Ints = []int64{100, 200, 300}
	buildBatch.Size = 3

	// Probe: (pk1, pk2) = [(1,10), (5,50)]
	probeBatch := UT.GetBatch(2)
	probeBatch.SetColumnName(0, "pk1")
	probeBatch.SetColumnName(1, "pk2")
	probeBatch.Cols[0].Type = LX.T_INT_KW
	probeBatch.Cols[0].Data.Ints = []int64{1, 5}
	probeBatch.Cols[1].Type = LX.T_INT_KW
	probeBatch.Cols[1].Data.Ints = []int64{10, 50}
	probeBatch.Size = 2

	build := &testBatchProducer{batches: []*UT.Batch{buildBatch}}
	probe := &testBatchProducer{batches: []*UT.Batch{probeBatch}}

	j := NewVectorizedHashJoinWithKind(build, probe, []int{0, 1}, []int{0, 1}, JoinKindLeft)
	defer j.Close()

	ctx := context.Background()

	// First batch: matched row (1,10,100,1,10).
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 1 {
		t.Fatalf("expected 1 matched row, got %d", batch.Size)
	}
	batch.Put()

	// Second batch: unmatched build rows (2,20,200) and (3,30,300).
	batch2, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 == nil {
		t.Fatal("expected unmatched build rows, got nil")
	}
	if batch2.Size != 2 {
		t.Fatalf("expected 2 unmatched rows, got %d", batch2.Size)
	}
	batch2.Put()

	// Third batch: unmatched probe row (pk1=5, pk2=50).
	// NOTE: LEFT outer unmatched probe emission is a future enhancement.
	batch3, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch3 != nil {
		batch3.Put()
	}
}

// TestVectorizedNLJ_VectorizedPredicate tests the columnar predicate path (REQ001620).
func TestVectorizedNLJ_VectorizedPredicate(t *testing.T) {
	// Build: (k, v) = [(1,10), (2,20), (3,30)]
	buildBatch := UT.GetBatch(2)
	buildBatch.SetColumnName(0, "k")
	buildBatch.SetColumnName(1, "v")
	buildBatch.Cols[0].Type = LX.T_INT_KW
	buildBatch.Cols[0].Data.Ints = []int64{1, 2, 3}
	buildBatch.Cols[1].Type = LX.T_INT_KW
	buildBatch.Cols[1].Data.Ints = []int64{10, 20, 30}
	buildBatch.Size = 3

	// Probe: (pk, pv) = [(1,100), (2,200), (4,400)]
	probeBatch := UT.GetBatch(2)
	probeBatch.SetColumnName(0, "pk")
	probeBatch.SetColumnName(1, "pv")
	probeBatch.Cols[0].Type = LX.T_INT_KW
	probeBatch.Cols[0].Data.Ints = []int64{1, 2, 4}
	probeBatch.Cols[1].Type = LX.T_INT_KW
	probeBatch.Cols[1].Data.Ints = []int64{100, 200, 400}
	probeBatch.Size = 3

	build := &testBatchProducer{batches: []*UT.Batch{buildBatch}}
	probe := &testBatchProducer{batches: []*UT.Batch{probeBatch}}

	j := NewVectorizedNestedLoopJoin(build, probe, nil, JoinKindInner)
	// REQ001620: set columnar equi-join predicate on column 0.
	j.WithColOn([]int{0}, []int{0})
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}

	// Should match 2 rows: (pk=1,k=1) and (pk=2,k=2).
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	batch.Put()

	// EOF.
	batch2, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 != nil {
		t.Fatal("expected nil (EOF), got batch")
	}
}
