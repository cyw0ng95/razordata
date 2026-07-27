package PX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// mockStage is a minimal Stage implementation for testing.
type mockStage struct {
	batches []*UT.Batch
	idx     int
	closed  bool
	reset   bool
}

func (m *mockStage) NextBatch(_ context.Context) (*UT.Batch, error) {
	if m.idx >= len(m.batches) {
		return nil, nil
	}
	b := m.batches[m.idx]
	m.idx++
	return b, nil
}

func (m *mockStage) Reset(_ context.Context) error {
	m.idx = 0
	m.reset = true
	return nil
}

func (m *mockStage) Close() error {
	m.closed = true
	return nil
}

func makeIntBatch(values []int64) *UT.Batch {
	b := UT.GetBatch(1)
	b.Pooled = false
	b.Cols[0].Type = LX.T_INT_KW
	b.SetColumnName(0, "v")
	b.Cols[0].Data.Ints = make([]int64, len(values))
	copy(b.Cols[0].Data.Ints, values)
	b.Cols[0].Nulls = make([]bool, len(values))
	b.Size = len(values)
	return b
}

func TestStageInterface_Satisfied(t *testing.T) {
	// Verify mockStage satisfies Stage interface
	var _ Stage = &mockStage{}
}

func TestStageCategory_String(t *testing.T) {
	tests := []struct {
		cat  StageCategory
		want string
	}{
		{CatSource, "source"},
		{CatTransform, "transform"},
		{CatMapReduce, "mapreduce"},
		{CatJoin, "join"},
		{StageCategory(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.cat.String(); got != tt.want {
			t.Errorf("StageCategory(%d).String() = %q, want %q", tt.cat, got, tt.want)
		}
	}
}

func TestErrResetNotSupported(t *testing.T) {
	if ErrResetNotSupported.Error() != "px: reset not supported; recreate from spec" {
		t.Errorf("unexpected ErrResetNotSupported message: %q", ErrResetNotSupported.Error())
	}
}

// mockNonResettableStage returns ErrResetNotSupported on Reset.
type mockNonResettableStage struct {
	batches []*UT.Batch
	idx     int
	closed  bool
}

func (m *mockNonResettableStage) NextBatch(_ context.Context) (*UT.Batch, error) {
	if m.idx >= len(m.batches) {
		return nil, nil
	}
	b := m.batches[m.idx]
	m.idx++
	return b, nil
}

func (m *mockNonResettableStage) Reset(_ context.Context) error {
	return ErrResetNotSupported
}

func (m *mockNonResettableStage) Close() error {
	m.closed = true
	return nil
}
