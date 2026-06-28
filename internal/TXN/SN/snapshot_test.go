package SN

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
)

func TestNewReadView(t *testing.T) {
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	if rv.readTS != 100 {
		t.Errorf("expected readTS 100, got %d", rv.readTS)
	}

	if rv.IsClosed() {
		t.Error("new ReadView should not be closed")
	}
}

func TestReadViewGetFromChain(t *testing.T) {
	arena := MV.NewArena()
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	key := []byte("testkey")
	node := MV.NewVersionNode(arena, 1, 10, key, []byte("value1"), false)
	chain := mv.EnsureVersionChain(key)
	chain.Insert(node)

	val, err := rv.Get(key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(val) != "value1" {
		t.Errorf("expected 'value1', got '%s'", string(val))
	}
}

func TestReadViewGetNotFound(t *testing.T) {
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	val, err := rv.Get([]byte("nonexistent"))
	if err != MV.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if val != nil {
		t.Errorf("expected nil value, got %v", val)
	}
}

func TestReadViewGetDeleted(t *testing.T) {
	arena := MV.NewArena()
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	key := []byte("testkey")
	node := MV.NewVersionNode(arena, 1, 10, key, []byte(""), true)
	chain := mv.EnsureVersionChain(key)
	chain.Insert(node)

	val, err := rv.Get(key)
	if err != MV.ErrNotFound {
		t.Errorf("expected ErrNotFound for deleted node, got %v", err)
	}
	if val != nil {
		t.Errorf("expected nil value for deleted node, got %v", val)
	}
}

func TestReadViewGetFromSnapshot(t *testing.T) {
	arena := MV.NewArena()
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	key := []byte("testkey")
	node := MV.NewVersionNode(arena, 1, 10, key, []byte("value1"), false)
	chain := mv.EnsureVersionChain(key)
	chain.Insert(node)

	_, _ = rv.Get(key)

	rv.addSnapshot(key, chain.Head())

	val, err := rv.Get(key)
	if err != nil {
		t.Fatalf("Get from snapshot failed: %v", err)
	}
	if string(val) != "value1" {
		t.Errorf("expected 'value1', got '%s'", string(val))
	}
}

func TestReadViewGetAfterClose(t *testing.T) {
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	rv.Close()

	_, err := rv.Get([]byte("testkey"))
	if err != MV.ErrInvalidTx {
		t.Errorf("expected ErrInvalidTx after close, got %v", err)
	}
}

func TestReadViewClose(t *testing.T) {
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	if rv.IsClosed() {
		t.Error("should not be closed initially")
	}

	rv.Close()

	if !rv.IsClosed() {
		t.Error("should be closed after Close()")
	}
}

func TestReadViewCloseIdempotent(t *testing.T) {
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	rv.Close()
	rv.Close()
	rv.Close()

	if !rv.IsClosed() {
		t.Error("should remain closed")
	}
}

func TestReadViewGetVisibleVersion(t *testing.T) {
	arena := MV.NewArena()
	mv := MV.NewMV()
	rv := NewReadView(mv, 50)

	key := []byte("testkey")

	node1 := MV.NewVersionNode(arena, 1, 10, key, []byte("value1"), false)
	node2 := MV.NewVersionNode(arena, 2, 30, key, []byte("value2"), false)

	chain := mv.EnsureVersionChain(key)
	chain.Insert(node1)
	chain.Insert(node2)

	val, err := rv.Get(key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(val) != "value2" {
		t.Errorf("expected 'value2', got '%s'", string(val))
	}
}

func TestReadViewGetCommittedVersion(t *testing.T) {
	arena := MV.NewArena()
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	key := []byte("testkey")

	node1 := MV.NewVersionNode(arena, 1, 10, key, []byte("value1"), false)
	node2 := MV.NewVersionNode(arena, 2, 30, key, []byte("value2"), false)

	chain := mv.EnsureVersionChain(key)
	chain.Insert(node1)
	chain.Insert(node2)

	chain.Commit(node1, 15)

	val, err := rv.Get(key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(val) != "value2" {
		t.Errorf("expected 'value2', got '%s'", string(val))
	}
}

func TestReadViewGetUncommittedNotVisible(t *testing.T) {
	arena := MV.NewArena()
	mv := MV.NewMV()
	rv := NewReadView(mv, 25)

	key := []byte("testkey")

	node1 := MV.NewVersionNode(arena, 1, 10, key, []byte("value1"), false)
	node2 := MV.NewVersionNode(arena, 2, 30, key, []byte("value2"), false)

	chain := mv.EnsureVersionChain(key)
	chain.Insert(node1)
	chain.Insert(node2)

	_, err := rv.Get(key)
	if err != nil {
		t.Errorf("expected nil (node1 visible at readTS 25: beginTS 10 < 25), got ErrNotFound: %v", err)
	}
}

func TestReadViewMultipleKeys(t *testing.T) {
	arena := MV.NewArena()
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	key1 := []byte("key1")
	key2 := []byte("key2")

	node1 := MV.NewVersionNode(arena, 1, 10, key1, []byte("value1"), false)
	node2 := MV.NewVersionNode(arena, 2, 20, key2, []byte("value2"), false)

	chain1 := mv.EnsureVersionChain(key1)
	chain1.Insert(node1)

	chain2 := mv.EnsureVersionChain(key2)
	chain2.Insert(node2)

	val1, err := rv.Get(key1)
	if err != nil {
		t.Fatalf("Get key1 failed: %v", err)
	}
	if string(val1) != "value1" {
		t.Errorf("expected 'value1', got '%s'", string(val1))
	}

	val2, err := rv.Get(key2)
	if err != nil {
		t.Fatalf("Get key2 failed: %v", err)
	}
	if string(val2) != "value2" {
		t.Errorf("expected 'value2', got '%s'", string(val2))
	}
}

func TestReadViewSnapshotUpdated(t *testing.T) {
	arena := MV.NewArena()
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	key := []byte("testkey")
	chain := mv.EnsureVersionChain(key)

	node1 := MV.NewVersionNode(arena, 1, 10, key, []byte("value1"), false)
	chain.Insert(node1)

	_, _ = rv.Get(key)

	if len(rv.snapshot) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(rv.snapshot))
	}

	node2 := MV.NewVersionNode(arena, 2, 20, key, []byte("value2"), false)
	chain.Insert(node2)

	_, _ = rv.Get(key)

	if len(rv.snapshot) != 1 {
		t.Errorf("snapshot should still be 1 (key already in snapshot), got %d", len(rv.snapshot))
	}
}

func TestReadViewConcurrency(t *testing.T) {
	arena := MV.NewArena()
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	key := []byte("testkey")
	node := MV.NewVersionNode(arena, 1, 10, key, []byte("value1"), false)
	chain := mv.EnsureVersionChain(key)
	chain.Insert(node)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = rv.Get(key)
			}
		}()
	}

	wg.Wait()
}

func TestVersionChainSnapshot(t *testing.T) {
	arena := MV.NewArena()
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	key := []byte("testkey")
	chain := mv.EnsureVersionChain(key)
	node := MV.NewVersionNode(arena, 1, 10, key, []byte("value1"), false)
	chain.Insert(node)

	rv.addSnapshot(key, chain.Head())

	if len(rv.snapshot) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(rv.snapshot))
	}

	snap, ok := rv.snapshot[string(key)]
	if !ok {
		t.Errorf("expected snapshot for key 'testkey'")
	}
	if snap == nil {
		t.Errorf("snapshot should not be nil")
	}
	if snap.Head != chain.Head() {
		t.Error("snapshot head should match chain head")
	}
}

func TestReadView_LookupLatency(t *testing.T) {
	arena := MV.NewArena()
	mv := MV.NewMV()
	rv := NewReadView(mv, 100)

	// Populate 1000 distinct keys (arena young=16KB + old=1MB, ~1K fits comfortably)
	keys := make([][]byte, 1000)
	for i := 0; i < 1000; i++ {
		k := []byte(fmt.Sprintf("key-%06d", i))
		keys[i] = k
		v := []byte(fmt.Sprintf("value-%d", i))
		node := MV.NewVersionNode(arena, 1, 10, k, v, false)
		chain := mv.EnsureVersionChain(k)
		chain.Insert(node)
	}

	// Warm up: populate snapshot cache for all keys
	for _, k := range keys {
		_, _ = rv.Get(k)
	}

	// Measure lookup latency on cached keys (should be O(1) map lookup)
	start := time.Now()
	for i := 0; i < 1000; i++ {
		_, _ = rv.Get(keys[i])
	}
	elapsed := time.Since(start)

	// 1000 lookups should complete well under 10ms with O(1) map
	if elapsed > 10*time.Millisecond {
		t.Errorf("1000 lookups took %v, expected <10ms with O(1) map", elapsed)
	}

	// Verify correctness: spot checks
	for i := 0; i < 100; i++ {
		idx := i * 10
		val, err := rv.Get(keys[idx])
		if err != nil {
			t.Fatalf("Get keys[%d] failed: %v", idx, err)
		}
		expected := fmt.Sprintf("value-%d", idx)
		if string(val) != expected {
			_ = idx
			t.Errorf("keys[%d]: expected '%s', got '%s'", idx, expected, string(val))
		}
	}
}
