package OP

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// REQ001445: VectorizedInSubquery builds key set and probes correctly.
func TestVectorizedInSubquery_BuildAndProbe(t *testing.T) {
	batches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{1, 2, 3, 4, 5} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}
	child := &fakeVecProducer{batches: batches}
	v := NewVectorizedInSubquery(child)
	ctx := context.Background()
	if err := v.BuildKeySet(ctx); err != nil {
		t.Fatalf("BuildKeySet: %v", err)
	}

	tests := []struct {
		key    int64
		expect bool
	}{
		{1, true},
		{3, true},
		{5, true},
		{2, true},
		{4, true},
		{0, false},
		{6, false},
		{-1, false},
	}
	for _, tc := range tests {
		got := v.Contains(tc.key)
		if got != tc.expect {
			t.Errorf("Contains(%d): got %v, want %v", tc.key, got, tc.expect)
		}
	}
}

// REQ001445: VectorizedExistsSubquery detects rows correctly.
func TestVectorizedExistsSubquery_HasRows(t *testing.T) {
	batches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{1, 2, 3} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}
	child := &fakeVecProducer{batches: batches}
	v := NewVectorizedExistsSubquery(child)
	ctx := context.Background()
	if err := v.Build(ctx); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !v.Exists() {
		t.Error("expected exists=true, got false")
	}
}

// REQ001445: VectorizedExistsSubquery detects empty result.
func TestVectorizedExistsSubquery_NoRows(t *testing.T) {
	child := &fakeVecProducer{batches: []*UT.Batch{}}
	v := NewVectorizedExistsSubquery(child)
	ctx := context.Background()
	if err := v.Build(ctx); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if v.Exists() {
		t.Error("expected exists=false, got true")
	}
}

// REQ001445: VectorizedScalarSubquery captures first row.
func TestVectorizedScalarSubquery_FirstRow(t *testing.T) {
	batches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "val")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{42, 99} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}
	child := &fakeVecProducer{batches: batches}
	v := NewVectorizedScalarSubquery(child)
	ctx := context.Background()
	if err := v.Build(ctx); err != nil {
		t.Fatalf("Build: %v", err)
	}
	val, ok := v.GetValue()
	if !ok {
		t.Fatal("expected value, got none")
	}
	intVal, ok := val.(int64)
	if !ok {
		t.Fatalf("expected int64, got %T", val)
	}
	if intVal != 42 {
		t.Errorf("expected 42, got %d", intVal)
	}
}

// REQ001445: VectorizedScalarSubquery empty result.
func TestVectorizedScalarSubquery_Empty(t *testing.T) {
	child := &fakeVecProducer{batches: []*UT.Batch{}}
	v := NewVectorizedScalarSubquery(child)
	ctx := context.Background()
	if err := v.Build(ctx); err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, ok := v.GetValue()
	if ok {
		t.Error("expected no value, got one")
	}
}

// REQ002042: multi-column IN subquery must build a composite key set
// from ALL columns, not just Cols[0]. Verifies the fix for the bug
// where (a, b) IN (SELECT x, y FROM ...) silently ignored column b
// and produced wrong results.
func TestVectorizedInSubquery_MultiColumnKey(t *testing.T) {
	// Two-column key set: (x, y) pairs.
	batch := UT.GetBatch(2)
	batch.SetColumnName(0, "x")
	batch.SetColumnName(1, "y")
	batch.Cols[0].Type = LX.T_INT_KW
	batch.Cols[1].Type = LX.T_INT_KW
	rows := []struct{ x, y int64 }{
		{1, 100},
		{2, 200},
		{3, 300},
	}
	batch.Cols[0].Data.Ints = make([]int64, len(rows))
	batch.Cols[1].Data.Ints = make([]int64, len(rows))
	for i, r := range rows {
		batch.Cols[0].Data.Ints[i] = r.x
		batch.Cols[1].Data.Ints[i] = r.y
	}
	batch.Size = len(rows)

	child := &fakeVecProducer{batches: []*UT.Batch{batch}}
	v := NewVectorizedInSubquery(child)
	ctx := context.Background()
	if err := v.BuildKeySet(ctx); err != nil {
		t.Fatalf("BuildKeySet: %v", err)
	}

	// Composite keys present in the set.
	tests := []struct {
		keys   []int64
		expect bool
	}{
		// Exact pairs that exist.
		{[]int64{1, 100}, true},
		{[]int64{2, 200}, true},
		{[]int64{3, 300}, true},
		// Pairs whose first column matches an existing x but the
		// second column does NOT match the paired y. These would be
		// TRUE under the pre-fix bug (only Cols[0] participated).
		{[]int64{1, 200}, false},
		{[]int64{2, 100}, false},
		{[]int64{3, 999}, false},
		// Neither column matches.
		{[]int64{9, 999}, false},
		// Wrong arity → must return false (cannot form a valid key).
		{[]int64{1}, false},
		{[]int64{1, 100, 0}, false},
		{nil, false},
	}
	for _, tc := range tests {
		got := v.ContainsKey(tc.keys)
		if got != tc.expect {
			t.Errorf("ContainsKey(%v): got %v, want %v", tc.keys, got, tc.expect)
		}
	}
}

// REQ002042: a single-column IN subquery still works correctly after
// the multi-column refactor (regression guard for the fast path).
func TestVectorizedInSubquery_SingleColumnRegression(t *testing.T) {
	batch := UT.GetBatch(1)
	batch.SetColumnName(0, "id")
	batch.Cols[0].Type = LX.T_INT_KW
	batch.Cols[0].Data.Ints = []int64{1, 2, 3, 4, 5}
	batch.Size = 5

	child := &fakeVecProducer{batches: []*UT.Batch{batch}}
	v := NewVectorizedInSubquery(child)
	ctx := context.Background()
	if err := v.BuildKeySet(ctx); err != nil {
		t.Fatalf("BuildKeySet: %v", err)
	}

	// Contains (single int64 API) must still work for single-column sets.
	for _, k := range []int64{1, 2, 3, 4, 5} {
		if !v.Contains(k) {
			t.Errorf("Contains(%d): got false, want true", k)
		}
	}
	for _, k := range []int64{0, 6, -1, 99} {
		if v.Contains(k) {
			t.Errorf("Contains(%d): got true, want false", k)
		}
	}
	// ContainsKey with single-element slice must agree with Contains.
	if !v.ContainsKey([]int64{3}) {
		t.Error("ContainsKey([3]): got false, want true")
	}
	if v.ContainsKey([]int64{99}) {
		t.Error("ContainsKey([99]): got true, want false")
	}
	// Wrong arity returns false even on a single-column set.
	if v.ContainsKey([]int64{1, 1}) {
		t.Error("ContainsKey([1,1]) on 1-col set: got true, want false")
	}
}
