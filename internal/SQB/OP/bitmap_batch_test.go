package OP

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

// makeKeyBatch creates a batch with a single int64 column (primary key).
func makeKeyBatch(keys []int64) *UT.Batch {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "pk")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(keys))
	copy(b.Cols[0].Data.Ints, keys)
	b.Size = len(keys)
	return b
}

// bmMemStore is an in-memory DT.Store for testing bitmap heap scans.
type bmMemStore struct {
	data map[string][]byte
}

func (m *bmMemStore) Insert(key, value []byte) error {
	m.data[string(key)] = append([]byte(nil), value...)
	return nil
}

func (m *bmMemStore) Delete(key []byte) error {
	delete(m.data, string(key))
	return nil
}

func (m *bmMemStore) Get(key []byte) ([]byte, bool, error) {
	v, ok := m.data[string(key)]
	return v, ok, nil
}

func (m *bmMemStore) NewIterator(prefix []byte) ls.RangeIter {
	return nil
}

func (m *bmMemStore) ManualCompact() error { return nil }

func newBmMemStore() *bmMemStore {
	return &bmMemStore{data: make(map[string][]byte)}
}

func TestBatchBitmapHeapScan_Basic(t *testing.T) {
	store := newBmMemStore()
	store.Insert([]byte("1"), []byte("row1"))
	store.Insert([]byte("2"), []byte("row2"))
	store.Insert([]byte("3"), []byte("row3"))

	// Single child with 2 keys.
	child1 := &testBatchProducer{
		batches: []*UT.Batch{makeKeyBatch([]int64{1, 2})},
	}
	child2 := &testBatchProducer{
		batches: []*UT.Batch{makeKeyBatch([]int64{2, 3})},
	}

	j := NewBatchBitmapHeapScan("test", store, []UT.BatchProducer{child1, child2})
	defer j.Close()

	ctx := context.Background()
	totalRows := 0
	results := make(map[string]bool)
	for {
		batch, err := j.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if batch == nil {
			break
		}
		for r := 0; r < batch.Size; r++ {
			val := UT.BatchValueAt(batch.Cols[0], r).(string)
			results[val] = true
		}
		totalRows += batch.Size
		batch.Put()
	}

	// Expected: 3 unique rows (1, 2, 3) — key 2 deduplicated across children.
	if totalRows != 3 {
		t.Fatalf("expected 3 rows, got %d", totalRows)
	}
	if !results["row1"] {
		t.Errorf("missing row1")
	}
	if !results["row2"] {
		t.Errorf("missing row2")
	}
	if !results["row3"] {
		t.Errorf("missing row3")
	}
}

func TestBatchBitmapHeapScan_EmptyChildren(t *testing.T) {
	store := newBmMemStore()
	child1 := &testBatchProducer{batches: nil}

	j := NewBatchBitmapHeapScan("test", store, []UT.BatchProducer{child1})
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Fatal("expected nil for empty children, got batch")
	}
}

func TestBatchBitmapHeapScan_KeyNotInStore(t *testing.T) {
	store := newBmMemStore()
	store.Insert([]byte("1"), []byte("row1"))
	// key 2 doesn't exist in store

	child1 := &testBatchProducer{
		batches: []*UT.Batch{makeKeyBatch([]int64{1, 2})},
	}

	j := NewBatchBitmapHeapScan("test", store, []UT.BatchProducer{child1})
	defer j.Close()

	ctx := context.Background()
	totalRows := 0
	for {
		batch, err := j.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if batch == nil {
			break
		}
		totalRows += batch.Size
		batch.Put()
	}
	// Only key 1 should be returned (key 2 not in store).
	if totalRows != 1 {
		t.Fatalf("expected 1 row, got %d", totalRows)
	}
}

func TestBatchBitmapHeapScan_MultiBatchChildren(t *testing.T) {
	store := newBmMemStore()
	store.Insert([]byte("1"), []byte("row1"))
	store.Insert([]byte("2"), []byte("row2"))

	// Child emits 2 batches.
	child1 := &testBatchProducer{
		batches: []*UT.Batch{
			makeKeyBatch([]int64{1}),
			makeKeyBatch([]int64{2}),
		},
	}

	j := NewBatchBitmapHeapScan("test", store, []UT.BatchProducer{child1})
	defer j.Close()

	ctx := context.Background()
	totalRows := 0
	for {
		batch, err := j.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if batch == nil {
			break
		}
		totalRows += batch.Size
		batch.Put()
	}
	if totalRows != 2 {
		t.Fatalf("expected 2 rows, got %d", totalRows)
	}
}