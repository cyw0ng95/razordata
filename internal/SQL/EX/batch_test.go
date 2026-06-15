package EX

import (
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

// TestBatchPoolBasic verifies GetBatch/Put round-trip.
func TestBatchPoolBasic(t *testing.T) {
	b := GetBatch(3)
	if b == nil {
		t.Fatal("GetBatch returned nil")
	}
	if b.Pooled != true {
		t.Error("expected Pooled=true")
	}
	if b.Size != 0 {
		t.Errorf("expected Size=0, got %d", b.Size)
	}
	if b.Sel != nil {
		t.Error("expected Sel=nil")
	}
	if len(b.Cols) < 3 {
		t.Errorf("expected >= 3 Cols, got %d", len(b.Cols))
	}
	b.Put()
}

// TestBatchPoolReuse verifies that a batch returned to the pool
// can be retrieved again with reset state.
func TestBatchPoolReuse(t *testing.T) {
	b1 := GetBatch(2)
	b1.Size = 100
	b1.Sel = []uint16{0, 1, 2}
	b1.Cols[0].Data = []int64{1, 2, 3}
	b1.Put()

	b2 := GetBatch(2)
	if b2.Size != 0 {
		t.Errorf("reused batch: expected Size=0, got %d", b2.Size)
	}
	if b2.Sel != nil {
		t.Error("reused batch: expected Sel=nil")
	}
	if b2.Cols[0].Data != nil {
		t.Error("reused batch: expected Cols[0].Data=nil")
	}
	b2.Put()
}

// TestBatchPoolConcurrent verifies pool safety under concurrency.
func TestBatchPoolConcurrent(t *testing.T) {
	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := GetBatch(2)
			b.Size = 10
			b.Put()
		}()
	}
	wg.Wait()
}

// TestBatchOversized verifies that batches exceeding MaxColumns
// are not pooled (direct allocation).
func TestBatchOversized(t *testing.T) {
	b := GetBatch(MaxColumns + 10)
	if b.Pooled {
		t.Error("oversized batch should not be Pooled")
	}
	if len(b.Cols) < MaxColumns+10 {
		t.Errorf("expected %d Cols, got %d", MaxColumns+10, len(b.Cols))
	}
	// Put should be a no-op
	b.Put()
}

// TestBatchAppendRow verifies typed append operations.
func TestBatchAppendRow(t *testing.T) {
	b := GetBatch(3)
	defer b.Put()

	b.AppendRow(0, LX.T_INT_KW, int64(42), false)
	b.AppendRow(1, LX.T_TEXT, "hello", false)
	b.AppendRow(2, LX.T_BOOL, true, false)
	b.AdvanceSize()

	if b.Size != 1 {
		t.Errorf("expected Size=1, got %d", b.Size)
	}
	col0 := b.Cols[0].Data.([]int64)
	if col0[0] != 42 {
		t.Errorf("col[0][0] = %d; want 42", col0[0])
	}
	col1 := b.Cols[1].Data.([]string)
	if col1[0] != "hello" {
		t.Errorf("col[1][0] = %q; want hello", col1[0])
	}
	col2 := b.Cols[2].Data.([]bool)
	if !col2[0] {
		t.Error("col[2][0] = false; want true")
	}
}

// TestBatchAppendNull verifies null bitmap handling.
func TestBatchAppendNull(t *testing.T) {
	b := GetBatch(1)
	defer b.Put()

	b.AppendRow(0, LX.T_INT_KW, int64(0), true)
	b.AdvanceSize()

	if !b.Cols[0].Nulls[0] {
		t.Error("expected Nulls[0]=true")
	}
}

// TestBatchIsFull verifies the full batch detection.
func TestBatchIsFull(t *testing.T) {
	b := GetBatch(1)
	defer b.Put()

	if b.IsFull() {
		t.Error("empty batch should not be full")
	}

	for i := 0; i < BatchSize; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(i), false)
		b.AdvanceSize()
	}

	if !b.IsFull() {
		t.Error("batch at capacity should be full")
	}
}

// TestBatchLogicalSize verifies the selection vector accounting.
func TestBatchLogicalSize(t *testing.T) {
	b := GetBatch(1)
	defer b.Put()
	b.Size = 10
	b.Sel = []uint16{0, 5, 9}
	if got := b.LogicalSize(); got != 3 {
		t.Errorf("LogicalSize with Sel = %d; want 3", got)
	}
	b.Sel = nil
	if got := b.LogicalSize(); got != 10 {
		t.Errorf("LogicalSize without Sel = %d; want 10", got)
	}
}

// BenchmarkBatchPool measures pool allocation cost.
func BenchmarkBatchPool(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch := GetBatch(3)
		batch.Put()
	}
}

// BenchmarkBatchPoolParallel measures pool allocation under concurrency.
func BenchmarkBatchPoolParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			batch := GetBatch(3)
			batch.Put()
		}
	})
}
