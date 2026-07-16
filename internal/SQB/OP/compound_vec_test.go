package OP

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// REQ001442: VectorizedUnionAll streams batches from left then right.
func TestVectorizedUnionAll_Basic(t *testing.T) {
	leftBatches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(2)
			b.SetColumnName(0, "id")
			b.SetColumnName(1, "val")
			b.Cols[0].Type = LX.T_INT_KW
			b.Cols[1].Type = LX.T_TEXT
			for _, v := range []int64{1, 2, 3} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AppendRow(1, LX.T_TEXT, "a", false)
				b.AdvanceSize()
			}
			return b
		}(),
	}
	rightBatches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(2)
			b.SetColumnName(0, "id")
			b.SetColumnName(1, "val")
			b.Cols[0].Type = LX.T_INT_KW
			b.Cols[1].Type = LX.T_TEXT
			for _, v := range []int64{4, 5} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AppendRow(1, LX.T_TEXT, "b", false)
				b.AdvanceSize()
			}
			return b
		}(),
	}

	left := &fakeVecProducer{batches: leftBatches}
	right := &fakeVecProducer{batches: rightBatches}
	cop := NewVectorizedCompoundOpWithOp(left, right, PS.CompoundUnionAll, []string{"id", "val"}, []LX.TokenType{LX.T_INT_KW, LX.T_TEXT})

	var allSizes []int
	for {
		batch, err := cop.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		allSizes = append(allSizes, batch.Size)
		batch.Put()
	}

	if len(allSizes) != 2 {
		t.Fatalf("expected 2 batches, got %d", len(allSizes))
	}
	if allSizes[0] != 3 {
		t.Errorf("left batch size: got %d, want 3", allSizes[0])
	}
	if allSizes[1] != 2 {
		t.Errorf("right batch size: got %d, want 2", allSizes[1])
	}
}

// REQ001442: VectorizedUnion deduplicates across both sides.
func TestVectorizedUnion_Dedup(t *testing.T) {
	leftBatches := []*UT.Batch{
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
	rightBatches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{2, 3, 4} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}

	left := &fakeVecProducer{batches: leftBatches}
	right := &fakeVecProducer{batches: rightBatches}
	cop := NewVectorizedCompoundOpWithOp(left, right, PS.CompoundUnion, []string{"id"}, []LX.TokenType{LX.T_INT_KW})

	var ids []int64
	for {
		batch, err := cop.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			ids = append(ids, batch.Cols[0].Data.Ints[i])
		}
		batch.Put()
	}

	if len(ids) != 4 {
		t.Fatalf("expected 4 unique rows, got %d: %v", len(ids), ids)
	}
	expected := []int64{1, 2, 3, 4}
	for i, exp := range expected {
		if ids[i] != exp {
			t.Errorf("ids[%d]: got %d, want %d", i, ids[i], exp)
		}
	}
}

// REQ001442: VectorizedExcept emits rows in left not in right.
func TestVectorizedExcept_Basic(t *testing.T) {
	leftBatches := []*UT.Batch{
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
	rightBatches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{3, 4, 5, 6, 7} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}

	left := &fakeVecProducer{batches: leftBatches}
	right := &fakeVecProducer{batches: rightBatches}
	cop := NewVectorizedCompoundOpWithOp(left, right, PS.CompoundExcept, []string{"id"}, []LX.TokenType{LX.T_INT_KW})

	var ids []int64
	for {
		batch, err := cop.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			ids = append(ids, batch.Cols[0].Data.Ints[i])
		}
		batch.Put()
	}

	// Left={1,2,3,4,5} Except Right={3,4,5,6,7} = {1,2}
	if len(ids) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(ids), ids)
	}
	if ids[0] != 1 || ids[1] != 2 {
		t.Errorf("expected [1, 2], got %v", ids)
	}
}

// REQ001442: VectorizedIntersect emits rows in both left and right.
func TestVectorizedIntersect_Basic(t *testing.T) {
	leftBatches := []*UT.Batch{
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
	rightBatches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{3, 4, 5, 6, 7} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}

	left := &fakeVecProducer{batches: leftBatches}
	right := &fakeVecProducer{batches: rightBatches}
	cop := NewVectorizedCompoundOpWithOp(left, right, PS.CompoundIntersect, []string{"id"}, []LX.TokenType{LX.T_INT_KW})

	var ids []int64
	for {
		batch, err := cop.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			ids = append(ids, batch.Cols[0].Data.Ints[i])
		}
		batch.Put()
	}

	// Left={1,2,3,4,5} Intersect Right={3,4,5,6,7} = {3,4,5}
	if len(ids) != 3 {
		t.Fatalf("expected 3 rows, got %d: %v", len(ids), ids)
	}
	expected := []int64{3, 4, 5}
	for i, exp := range expected {
		if ids[i] != exp {
			t.Errorf("ids[%d]: got %d, want %d", i, ids[i], exp)
		}
	}
}

// REQ001442: VectorizedExcept handles empty right side.
func TestVectorizedExcept_EmptyRight(t *testing.T) {
	leftBatches := []*UT.Batch{
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

	left := &fakeVecProducer{batches: leftBatches}
	right := &fakeVecProducer{batches: []*UT.Batch{}}
	cop := NewVectorizedCompoundOpWithOp(left, right, PS.CompoundExcept, []string{"id"}, []LX.TokenType{LX.T_INT_KW})

	var ids []int64
	for {
		batch, err := cop.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			ids = append(ids, batch.Cols[0].Data.Ints[i])
		}
		batch.Put()
	}

	// Left minus empty = all left
	if len(ids) != 3 {
		t.Fatalf("expected 3 rows, got %d: %v", len(ids), ids)
	}
}

// REQ001442: VectorizedIntersect handles no overlap.
func TestVectorizedIntersect_NoOverlap(t *testing.T) {
	leftBatches := []*UT.Batch{
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
	rightBatches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{7, 8, 9} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}

	left := &fakeVecProducer{batches: leftBatches}
	right := &fakeVecProducer{batches: rightBatches}
	cop := NewVectorizedCompoundOpWithOp(left, right, PS.CompoundIntersect, []string{"id"}, []LX.TokenType{LX.T_INT_KW})

	var ids []int64
	for {
		batch, err := cop.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			ids = append(ids, batch.Cols[0].Data.Ints[i])
		}
		batch.Put()
	}

	if len(ids) != 0 {
		t.Fatalf("expected 0 rows, got %d: %v", len(ids), ids)
	}
}

// REQ001442: VectorizedExcept handles multi-batch left side.
func TestVectorizedExcept_MultiBatch(t *testing.T) {
	leftBatches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{1, 2} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{3, 4} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}
	rightBatches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{2, 4} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}

	left := &fakeVecProducer{batches: leftBatches}
	right := &fakeVecProducer{batches: rightBatches}
	cop := NewVectorizedCompoundOpWithOp(left, right, PS.CompoundExcept, []string{"id"}, []LX.TokenType{LX.T_INT_KW})

	var ids []int64
	for {
		batch, err := cop.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			ids = append(ids, batch.Cols[0].Data.Ints[i])
		}
		batch.Put()
	}

	// Left={1,2,3,4} Except Right={2,4} = {1,3}
	if len(ids) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(ids), ids)
	}
	if ids[0] != 1 || ids[1] != 3 {
		t.Errorf("expected [1, 3], got %v", ids)
	}
}

// REQ001442: VectorizedIntersect handles multi-batch left side.
func TestVectorizedIntersect_MultiBatch(t *testing.T) {
	leftBatches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{1, 2} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{3, 4} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}
	rightBatches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{2, 4, 6} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}

	left := &fakeVecProducer{batches: leftBatches}
	right := &fakeVecProducer{batches: rightBatches}
	cop := NewVectorizedCompoundOpWithOp(left, right, PS.CompoundIntersect, []string{"id"}, []LX.TokenType{LX.T_INT_KW})

	var ids []int64
	for {
		batch, err := cop.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			ids = append(ids, batch.Cols[0].Data.Ints[i])
		}
		batch.Put()
	}

	// Left={1,2,3,4} Intersect Right={2,4,6} = {2,4}
	if len(ids) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(ids), ids)
	}
	if ids[0] != 2 || ids[1] != 4 {
		t.Errorf("expected [2, 4], got %v", ids)
	}
}

// REQ001442: VectorizedExcept handles 3-way chain (A EXCEPT B) EXCEPT C.
func TestVectorizedExcept_3Way(t *testing.T) {
	// First: A EXCEPT B
	leftBatches := []*UT.Batch{
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
	rightBatches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{3, 4, 5} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}

	left := &fakeVecProducer{batches: leftBatches}
	right := &fakeVecProducer{batches: rightBatches}
	step1 := NewVectorizedCompoundOpWithOp(left, right, PS.CompoundExcept, []string{"id"}, []LX.TokenType{LX.T_INT_KW})

	// step1 result = {1, 2}. Now EXCEPT C = {2, 5, 6}
	var step1Rows []int64
	for {
		batch, err := step1.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("step1 NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			step1Rows = append(step1Rows, batch.Cols[0].Data.Ints[i])
		}
		batch.Put()
	}

	if len(step1Rows) != 2 || step1Rows[0] != 1 || step1Rows[1] != 2 {
		t.Fatalf("step1: expected [1, 2], got %v", step1Rows)
	}

	// Feed step1 result into second EXCEPT with C
	// We need to recreate the left producer with step1 results
	leftBatches2 := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range step1Rows {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}
	rightBatches2 := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "id")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{2, 5, 6} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}

	left2 := &fakeVecProducer{batches: leftBatches2}
	right2 := &fakeVecProducer{batches: rightBatches2}
	step2 := NewVectorizedCompoundOpWithOp(left2, right2, PS.CompoundExcept, []string{"id"}, []LX.TokenType{LX.T_INT_KW})

	var finalIds []int64
	for {
		batch, err := step2.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("step2 NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			finalIds = append(finalIds, batch.Cols[0].Data.Ints[i])
		}
		batch.Put()
	}

	// {1,2} EXCEPT {2,5,6} = {1}
	if len(finalIds) != 1 || finalIds[0] != 1 {
		t.Fatalf("expected [1], got %v", finalIds)
	}
}

// fakeVecProducer implements UT.BatchProducer for testing.
type fakeVecProducer struct {
	batches []*UT.Batch
	idx     int
}

func (f *fakeVecProducer) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if f.idx >= len(f.batches) {
		return nil, nil
	}
	b := f.batches[f.idx]
	f.idx++
	return b, nil
}

func (f *fakeVecProducer) Close() error { return nil }
