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
