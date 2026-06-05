package VL

import (
	"context"
	"sync"
	"testing"
)

func TestBegin(t *testing.T) {
	tx, err := Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if tx == nil {
		t.Fatal("expected tx, got nil")
	}
	tx.Abort(context.Background())
}

func TestBeginNoSlots(t *testing.T) {
	sm := globalSlotManager

	initialFree := sm.NumFreeSlots()
	if initialFree == 0 {
		t.Skip("no free slots to test")
	}

	slots := make([]*transactionSlot, 0, initialFree)
	for i := 0; i < initialFree; i++ {
		slot := sm.AllocateSlot()
		if slot == nil {
			break
		}
		slots = append(slots, slot)
	}

	if len(slots) == 0 {
		t.Fatal("could not allocate any slots")
	}

	_, err := Begin(context.Background())
	if err != ErrNoSlotsAvailable {
		t.Errorf("expected ErrNoSlotsAvailable, got %v (free=%d)", err, sm.NumFreeSlots())
	}

	for _, slot := range slots {
		sm.ReleaseSlot(slot)
	}
}

func TestTxGetEmpty(t *testing.T) {
	tx, _ := Begin(context.Background())
	defer tx.Abort(context.Background())

	key := []byte("testkey")
	val, err := tx.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != nil {
		t.Errorf("expected nil for non-existent key, got %v", val)
	}
}

func TestTxInsertAndGet(t *testing.T) {
	tx, _ := Begin(context.Background())
	defer tx.Abort(context.Background())

	key := []byte("key1")
	value := []byte("value1")

	err := tx.Insert(context.Background(), key, value)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	val, err := tx.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get after Insert failed: %v", err)
	}
	if string(val) != string(value) {
		t.Errorf("expected %q, got %q", string(value), string(val))
	}
}

func TestTxDelete(t *testing.T) {
	tx, _ := Begin(context.Background())
	defer tx.Abort(context.Background())

	key := []byte("key1")
	tx.Insert(context.Background(), key, []byte("value1"))

	err := tx.Delete(context.Background(), key)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	val, err := tx.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get after Delete failed: %v", err)
	}
	if val != nil {
		t.Errorf("expected nil after delete, got %v", val)
	}
}

func TestTxAbort(t *testing.T) {
	tx, _ := Begin(context.Background())

	err := tx.Abort(context.Background())
	if err != nil {
		t.Fatalf("Abort failed: %v", err)
	}

	tx2, err := Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin after Abort failed: %v", err)
	}
	tx2.Abort(context.Background())
}

func TestTxCommit(t *testing.T) {
	tx, _ := Begin(context.Background())

	tx.Insert(context.Background(), []byte("key1"), []byte("value1"))

	err := tx.Commit(context.Background())
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}

func TestConcurrentTransactions(t *testing.T) {
	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(id int) {
			defer wg.Done()
			tx, _ := Begin(context.Background())
			tx.Insert(context.Background(), []byte("key"), []byte("value"))
			tx.Commit(context.Background())
		}(i)
	}

	wg.Wait()
}

func TestCommitNoConflict(t *testing.T) {
	tx1, _ := Begin(context.Background())
	tx1.Insert(context.Background(), []byte("a"), []byte("1"))
	tx1.Commit(context.Background())

	tx2, _ := Begin(context.Background())
	tx2.Insert(context.Background(), []byte("b"), []byte("2"))

	err := tx2.Commit(context.Background())
	if err != nil {
		t.Errorf("expected commit to succeed, got %v", err)
	}

	tx1.Abort(context.Background())
	tx2.Abort(context.Background())
}

func TestCommitWithConflict(t *testing.T) {
	tx1, _ := Begin(context.Background())
	tx1.Insert(context.Background(), []byte("key"), []byte("1"))
	tx1.Commit(context.Background())

	tx2, _ := Begin(context.Background())
	tx2.Insert(context.Background(), []byte("key"), []byte("2"))

	err := tx2.Commit(context.Background())
	if err != nil {
		t.Logf("Commit returned error (may have aborted): %v", err)
	}

	tx1.Abort(context.Background())
	tx2.Abort(context.Background())
}

func TestTxReadOwnWrites(t *testing.T) {
	tx, _ := Begin(context.Background())
	defer tx.Abort(context.Background())

	key := []byte("mykey")
	value := []byte("myvalue")

	tx.Insert(context.Background(), key, value)

	val, err := tx.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(val) != string(value) {
		t.Errorf("expected %q, got %q", string(value), string(val))
	}
}

func BenchmarkConcurrentCommit(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, _ := Begin(context.Background())
		tx.Insert(context.Background(), []byte("key"), []byte("value"))
		tx.Commit(context.Background())
	}
}
