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
	for i := range n {
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
	for i := range n {
		b.Cols[0].Data.Ints[i] = keys[i]
	}
	b.Size = n
	return b
}

// TestVectorizedHashJoin_BatchedEmit_MultiMatch verifies that the
// sequential probe path correctly emits multiple matches for one probe
// row using the batched emit function. REQ002000.
func TestVectorizedHashJoin_BatchedEmit_MultiMatch(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{1, 1, 1, 2},
			[]int64{10, 20, 30, 40},
		)},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{1})},
	}

	// No parallelism → exercises probePhase → emitBatchedMatches.
	j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0})
	defer j.Close()

	batch, err := j.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	// key 1 matches 3 build rows.
	if batch.Size != 3 {
		t.Fatalf("expected 3 matched rows, got %d", batch.Size)
	}

	// All output rows should have build key = probe key = 1.
	// Build column 0 (k) = 1 for all 3 rows.
	// Build column 1 (v) = 10, 20, 30 (one per row).
	// Probe column (pk) = 1 for all 3 rows.
	values := make(map[int64][]int64) // build_key → [build_v, ...]
	for i := 0; i < batch.Size; i++ {
		k := UT.BatchValueAt(batch.Cols[0], i).(int64)
		v := UT.BatchValueAt(batch.Cols[1], i).(int64)
		pk := UT.BatchValueAt(batch.Cols[2], i).(int64)
		if k != 1 {
			t.Errorf("row %d: build key %d, want 1", i, k)
		}
		if pk != 1 {
			t.Errorf("row %d: probe key %d, want 1", i, pk)
		}
		values[k] = append(values[k], v)
	}
	want := []int64{10, 20, 30}
	got := values[1]
	if len(got) != len(want) {
		t.Fatalf("build values: got %d entries, want 3", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("build values[%d] = %d, want %d (full %v)", i, got[i], want[i], got)
		}
	}

	batch.Put()
}

// TestVectorizedHashJoin_BatchedEmit_MixedTypes verifies batched emit
// handles int and string columns correctly. REQ002000.
func TestVectorizedHashJoin_BatchedEmit_MixedTypes(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{
			func() *UT.Batch {
				b := UT.GetBatch(2)
				b.SetColumnName(0, "k")
				b.SetColumnName(1, "name")
				b.Cols[0].Type = LX.T_INT_KW
				b.Cols[1].Type = LX.T_TEXT
				for _, p := range []struct {
					k    int64
					name string
				}{{1, "alice"}, {1, "bob"}, {1, "carol"}} {
					b.AppendRow(0, LX.T_INT_KW, p.k, false)
					b.AppendRow(1, LX.T_TEXT, p.name, false)
					b.AdvanceSize()
				}
				return b
			}(),
		},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{1})},
	}

	j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0})
	defer j.Close()

	batch, err := j.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil || batch.Size != 3 {
		t.Fatalf("expected 3-row batch, got %v", batch)
	}

	// Verify name column (TEXT) was emitted correctly.
	names := make(map[string]bool)
	for i := 0; i < batch.Size; i++ {
		name := batch.Cols[1].Data.Strs[i]
		names[name] = true
	}
	for _, want := range []string{"alice", "bob", "carol"} {
		if !names[want] {
			t.Errorf("missing name %q (got %v)", want, names)
		}
	}
	batch.Put()
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
	batch2, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 == nil {
		t.Fatal("expected unmatched probe row, got nil")
	}
	if batch2.Size != 1 {
		t.Fatalf("expected 1 unmatched probe row, got %d", batch2.Size)
	}
	// Verify probe key column (pk=2) is present; build columns are NULL.
	pk := UT.BatchValueAt(batch2.Cols[2], 0).(int64)
	if pk != 2 {
		t.Errorf("expected pk=2, got %d", pk)
	}
	batch2.Put()

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

// --- REQ001645 parallel probe tests ---

// chunkedBuildProducer wraps keys/vals into BatchSize-row build batches.
func chunkedBuildProducer(keys, vals []int64) *testBatchProducer {
	const sz = UT.BatchSize
	var batches []*UT.Batch
	for i := 0; i < len(keys); i += sz {
		end := min(i+sz, len(keys))
		batches = append(batches, makeJoinBuildBatch(keys[i:end], vals[i:end]))
	}
	return &testBatchProducer{batches: batches}
}

// chunkedProbeProducer wraps keys into BatchSize-row probe batches.
func chunkedProbeProducer(keys []int64) *testBatchProducer {
	const sz = UT.BatchSize
	var batches []*UT.Batch
	for i := 0; i < len(keys); i += sz {
		end := min(i+sz, len(keys))
		batches = append(batches, makeJoinProbeBatch(keys[i:end]))
	}
	return &testBatchProducer{batches: batches}
}

// drainVHJInner collects all (k, v, pk) rows from a VectorizedHashJoin in
// emission order. For inner joins only (no NULLs).
func drainVHJInner(t *testing.T, j *VectorizedHashJoin) [][3]int64 {
	t.Helper()
	var out [][3]int64
	ctx := context.Background()
	for {
		batch, err := j.NextBatch(ctx)
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			k := UT.BatchValueAt(batch.Cols[0], i).(int64)
			v := UT.BatchValueAt(batch.Cols[1], i).(int64)
			pk := UT.BatchValueAt(batch.Cols[2], i).(int64)
			out = append(out, [3]int64{k, v, pk})
		}
		batch.Put()
	}
	return out
}

// TestVectorizedHashJoin_ParallelBuild_MatchesSequential exercises the
// pool-driven parallel BUILD path (buildHashTableParallel, REQ001622) — which
// is otherwise dormant — and verifies it produces the same matched rows as the
// sequential build. WithPool is set but WithParallelism is NOT, so the probe
// stays sequential and only the build parallelizes. REQ001645 wiring safety.
func TestVectorizedHashJoin_ParallelBuild_MatchesSequential(t *testing.T) {
	const buildN = 1024 // >= 512 triggers buildHashTableParallel
	buildKeys := make([]int64, buildN)
	buildVals := make([]int64, buildN)
	for i := range buildKeys {
		// Duplicate build keys: each key 0..511 appears twice. This stresses
		// the parallel-build merge logic, which must collect both build rows
		// into the same hash-table slot's rowIDs across worker chunks.
		buildKeys[i] = int64(i % (buildN / 2))
		buildVals[i] = int64(i * 10)
	}
	// Probe: matches (0, 7, 256 — each matches 2 build rows) and non-matches.
	probeKeys := []int64{0, 7, 256, 5000, 999}

	seq := drainVHJInner(t, NewVectorizedHashJoin(
		chunkedBuildProducer(buildKeys, buildVals),
		chunkedProbeProducer(probeKeys),
		[]int{0}, []int{0}))

	pool := UT.NewWorkerPool(4)
	defer pool.Close()
	parJ := NewVectorizedHashJoin(
		chunkedBuildProducer(buildKeys, buildVals),
		chunkedProbeProducer(probeKeys),
		[]int{0}, []int{0})
	parJ.WithPool(pool) // no WithParallelism → sequential probe, parallel build
	par := drainVHJInner(t, parJ)

	if len(seq) != len(par) {
		t.Fatalf("row count: seq=%d par=%d", len(seq), len(par))
	}
	// Build a set of expected (k,v,pk) triples from the sequential run and
	// verify every parallel row appears in it. Emission order within a probe
	// row follows hash-table slot order, which can differ between the seq and
	// parallel merge paths, so compare as multisets.
	want := make(map[[3]int64]int, len(seq))
	for _, r := range seq {
		want[r]++
	}
	for _, r := range par {
		want[r]--
		if want[r] < 0 {
			t.Errorf("parallel produced extra row %v not in sequential", r)
		}
	}
	for r, c := range want {
		if c != 0 {
			t.Errorf("sequential row %v missing from parallel (deficit %d)", r, c)
		}
	}
}

// TestVectorizedHashJoin_ParallelProbe_InnerEquiJoin verifies the parallel
// probe path produces the same matched rows as the sequential path. REQ001645.
func TestVectorizedHashJoin_ParallelProbe_InnerEquiJoin(t *testing.T) {
	build := &testBatchProducer{batches: []*UT.Batch{makeJoinBuildBatch(
		[]int64{1, 2, 3},
		[]int64{10, 20, 30},
	)}}
	probe := &testBatchProducer{batches: []*UT.Batch{makeJoinProbeBatch([]int64{2, 3, 4})}}

	j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0}).WithParallelism(4)
	defer j.Close()

	batch, err := j.NextBatch(context.Background())
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
	if results[2] != 20 || results[3] != 30 {
		t.Errorf("expected {2:20, 3:30}, got %v", results)
	}
	batch.Put()

	if batch2, _ := j.NextBatch(context.Background()); batch2 != nil {
		t.Fatal("expected EOF after matched phase")
	}
}

// TestVectorizedHashJoin_ParallelProbe_MatchesSequential verifies the parallel
// probe emitter (parallelism=8) yields a byte-identical row sequence to the
// sequential probe path over a large, multi-batch, multi-match dataset.
func TestVectorizedHashJoin_ParallelProbe_MatchesSequential(t *testing.T) {
	const n = 2048
	buildKeys := make([]int64, n)
	buildVals := make([]int64, n)
	for i := range buildKeys {
		buildKeys[i] = int64(i % 512) // 4 duplicates per key
		buildVals[i] = int64(i)
	}
	probeKeys := make([]int64, n)
	for i := range probeKeys {
		probeKeys[i] = int64((i * 7) % 600) // some misses (512..599)
	}

	// Sequential.
	seqJ := NewVectorizedHashJoin(chunkedBuildProducer(buildKeys, buildVals),
		chunkedProbeProducer(probeKeys), []int{0}, []int{0})
	seq := drainVHJInner(t, seqJ)
	seqJ.Close()

	// Parallel.
	parJ := NewVectorizedHashJoin(chunkedBuildProducer(buildKeys, buildVals),
		chunkedProbeProducer(probeKeys), []int{0}, []int{0}).WithParallelism(8)
	par := drainVHJInner(t, parJ)
	parJ.Close()

	if len(seq) != len(par) {
		t.Fatalf("row count: seq=%d par=%d", len(seq), len(par))
	}
	for i := range seq {
		if seq[i] != par[i] {
			t.Errorf("row %d: seq=%v par=%v", i, seq[i], par[i])
		}
	}
}

// TestVectorizedHashJoin_ParallelProbe_MultiMatch verifies a probe key matching
// multiple build rows emits all pairs under the parallel path. REQ001645.
func TestVectorizedHashJoin_ParallelProbe_MultiMatch(t *testing.T) {
	build := &testBatchProducer{batches: []*UT.Batch{makeJoinBuildBatch(
		[]int64{1, 1, 1, 2},
		[]int64{10, 20, 30, 40},
	)}}
	probe := &testBatchProducer{batches: []*UT.Batch{makeJoinProbeBatch([]int64{1})}}

	j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0}).WithParallelism(4)
	defer j.Close()

	batch, err := j.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	// key 1 matches 3 build rows.
	if batch.Size != 3 {
		t.Fatalf("expected 3 matched rows, got %d", batch.Size)
	}
	batch.Put()
}

// TestVectorizedHashJoin_ParallelProbe_NoMatch verifies the parallel path
// returns nil when no probe keys match.
func TestVectorizedHashJoin_ParallelProbe_NoMatch(t *testing.T) {
	build := &testBatchProducer{batches: []*UT.Batch{makeJoinBuildBatch(
		[]int64{1, 2, 3},
		[]int64{10, 20, 30},
	)}}
	probe := &testBatchProducer{batches: []*UT.Batch{makeJoinProbeBatch([]int64{4, 5, 6})}}

	j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0}).WithParallelism(4)
	defer j.Close()

	batch, err := j.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected nil (no matches), got batch with %d rows", batch.Size)
	}
}

// TestVectorizedHashJoin_ParallelProbe_LeftOuter verifies LEFT outer join
// correctness under the parallel probe path: matched rows then unmatched build
// rows. REQ001645.
func TestVectorizedHashJoin_ParallelProbe_LeftOuter(t *testing.T) {
	build := &testBatchProducer{batches: []*UT.Batch{makeJoinBuildBatch(
		[]int64{1, 2, 3},
		[]int64{10, 20, 30},
	)}}
	probe := &testBatchProducer{batches: []*UT.Batch{makeJoinProbeBatch([]int64{2})}}

	j := NewVectorizedHashJoinWithKind(build, probe, []int{0}, []int{0}, JoinKindLeft).WithParallelism(4)
	defer j.Close()

	ctx := context.Background()
	// First batch: 1 matched row (2,20,2).
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil || batch.Size != 1 {
		t.Fatalf("expected 1 matched row, got %v", batch)
	}
	k := UT.BatchValueAt(batch.Cols[0], 0).(int64)
	pk := UT.BatchValueAt(batch.Cols[2], 0).(int64)
	if k != 2 || pk != 2 {
		t.Errorf("expected (k=2,pk=2), got (k=%d,pk=%d)", k, pk)
	}
	batch.Put()

	// Next batch: 2 unmatched build rows (1,3) with NULL probe.
	batch2, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 == nil || batch2.Size != 2 {
		t.Fatalf("expected 2 unmatched build rows, got %v", batch2)
	}
	batch2.Put()

	if batch3, _ := j.NextBatch(ctx); batch3 != nil {
		batch3.Put()
		t.Fatal("expected EOF after unmatched build phase")
	}
}

// TestVectorizedHashJoin_ParallelProbe_RightOuter verifies RIGHT outer join
// correctness under the parallel probe path: matched rows then unmatched probe
// rows. REQ001645.
func TestVectorizedHashJoin_ParallelProbe_RightOuter(t *testing.T) {
	build := &testBatchProducer{batches: []*UT.Batch{makeJoinBuildBatch(
		[]int64{1, 3},
		[]int64{10, 30},
	)}}
	probe := &testBatchProducer{batches: []*UT.Batch{makeJoinProbeBatch([]int64{2, 3})}}

	j := NewVectorizedHashJoinWithKind(build, probe, []int{0}, []int{0}, JoinKindRight).WithParallelism(4)
	defer j.Close()

	ctx := context.Background()
	// First batch: 1 matched row (3,30,3).
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil || batch.Size != 1 {
		t.Fatalf("expected 1 matched row, got %v", batch)
	}
	batch.Put()

	// Next batch: 1 unmatched probe row (pk=2) with NULL build side.
	batch2, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 == nil || batch2.Size != 1 {
		t.Fatalf("expected 1 unmatched probe row, got %v", batch2)
	}
	batch2.Put()

	if batch3, _ := j.NextBatch(ctx); batch3 != nil {
		batch3.Put()
		t.Fatal("expected EOF after unmatched probe phase")
	}
}

// BenchmarkParallelHashJoin_LargeBuild measures parallel-probe speedup on a
// 100K-row build side (REQ001645). parallel=1 uses the sequential probePhase;
// parallel=N uses emitParallelMatched (UT.ParallelProbe). Build is identical
// across sub-benchmarks (no pool) so only probe parallelism differs.
func BenchmarkParallelHashJoin_LargeBuild(b *testing.B) {
	const buildN = 100000
	buildKeys := make([]int64, buildN)
	buildVals := make([]int64, buildN)
	for i := range buildKeys {
		buildKeys[i] = int64(i)
		buildVals[i] = int64(i * 10)
	}
	probeKeys := make([]int64, buildN)
	for i := range probeKeys {
		probeKeys[i] = int64(i) // 1:1 match → 100K lookups
	}

	for _, par := range []int{1, 8} {
		b.Run(fmt.Sprintf("parallel=%d", par), func(b *testing.B) {
			ctx := context.Background()
			for i := 0; i < b.N; i++ {
				build := chunkedBuildProducer(buildKeys, buildVals)
				probe := chunkedProbeProducer(probeKeys)
				j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0})
				if par > 1 {
					j.WithParallelism(par)
				}
				for {
					batch, err := j.NextBatch(ctx)
					if err != nil {
						b.Fatalf("NextBatch: %v", err)
					}
					if batch == nil {
						break
					}
					batch.Put()
				}
				j.Close()
			}
		})
	}
}

// BenchmarkParallelHashJoinBuild measures parallel-build speedup across
// worker counts. The probe side is trivial (1 match per build row) so
// timing reflects the build phase. REQ002001.
func BenchmarkParallelHashJoinBuild(b *testing.B) {
	const buildN = 200000
	buildKeys := make([]int64, buildN)
	buildVals := make([]int64, buildN)
	for i := range buildKeys {
		buildKeys[i] = int64(i)
		buildVals[i] = int64(i * 10)
	}
	// Tiny probe so we measure build only.
	probeKeys := []int64{0}

	for _, workers := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			ctx := context.Background()
			pool := UT.NewWorkerPool(workers)
			defer pool.Close()
			for i := 0; i < b.N; i++ {
				build := chunkedBuildProducer(buildKeys, buildVals)
				probe := chunkedProbeProducer(probeKeys)
				j := NewVectorizedHashJoin(build, probe, []int{0}, []int{0}).
					WithPool(pool)
				for {
					batch, err := j.NextBatch(ctx)
					if err != nil {
						b.Fatalf("NextBatch: %v", err)
					}
					if batch == nil {
						break
					}
					batch.Put()
				}
				j.Close()
			}
		})
	}
}
