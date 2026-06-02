package SN

import (
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
)

func TestNewReadView(t *testing.T) {
	mv := MV.NewMV()
	rv := newReadView(mv, 100)

	if rv.readTS != 100 {
		t.Errorf("expected readTS 100, got %d", rv.readTS)
	}

	if rv.IsClosed() {
		t.Error("new readView should not be closed")
	}
}

func TestReadViewGetFromChain(t *testing.T) {
	mv := MV.NewMV()
	rv := newReadView(mv, 100)

	key := []byte("testkey")
	node := MV.NewVersionNode(1, 10, key, []byte("value1"), false)
	chain := mv.GetOrCreateVersionChain(key)
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
	rv := newReadView(mv, 100)

	val, err := rv.Get([]byte("nonexistent"))
	if err != MV.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if val != nil {
		t.Errorf("expected nil value, got %v", val)
	}
}

func TestReadViewGetDeleted(t *testing.T) {
	mv := MV.NewMV()
	rv := newReadView(mv, 100)

	key := []byte("testkey")
	node := MV.NewVersionNode(1, 10, key, []byte(""), true)
	chain := mv.GetOrCreateVersionChain(key)
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
	mv := MV.NewMV()
	rv := newReadView(mv, 100)

	key := []byte("testkey")
	node := MV.NewVersionNode(1, 10, key, []byte("value1"), false)
	chain := mv.GetOrCreateVersionChain(key)
	chain.Insert(node)

	_, _ = rv.Get(key)

	rv.addSnapshot(key, chain.GetHead())

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
	rv := newReadView(mv, 100)

	rv.Close()

	_, err := rv.Get([]byte("testkey"))
	if err != MV.ErrInvalidTx {
		t.Errorf("expected ErrInvalidTx after close, got %v", err)
	}
}

func TestReadViewClose(t *testing.T) {
	mv := MV.NewMV()
	rv := newReadView(mv, 100)

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
	rv := newReadView(mv, 100)

	rv.Close()
	rv.Close()
	rv.Close()

	if !rv.IsClosed() {
		t.Error("should remain closed")
	}
}

func TestReadViewGetVisibleVersion(t *testing.T) {
	mv := MV.NewMV()
	rv := newReadView(mv, 50)

	key := []byte("testkey")

	node1 := MV.NewVersionNode(1, 10, key, []byte("value1"), false)
	node2 := MV.NewVersionNode(2, 30, key, []byte("value2"), false)

	chain := mv.GetOrCreateVersionChain(key)
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
	mv := MV.NewMV()
	rv := newReadView(mv, 100)

	key := []byte("testkey")

	node1 := MV.NewVersionNode(1, 10, key, []byte("value1"), false)
	node2 := MV.NewVersionNode(2, 30, key, []byte("value2"), false)

	chain := mv.GetOrCreateVersionChain(key)
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
	mv := MV.NewMV()
	rv := newReadView(mv, 25)

	key := []byte("testkey")

	node1 := MV.NewVersionNode(1, 10, key, []byte("value1"), false)
	node2 := MV.NewVersionNode(2, 30, key, []byte("value2"), false)

	chain := mv.GetOrCreateVersionChain(key)
	chain.Insert(node1)
	chain.Insert(node2)

	_, err := rv.Get(key)
	if err != nil {
		t.Errorf("expected nil (node1 visible at readTS 25: beginTS 10 < 25), got ErrNotFound: %v", err)
	}
}

func TestReadViewMultipleKeys(t *testing.T) {
	mv := MV.NewMV()
	rv := newReadView(mv, 100)

	key1 := []byte("key1")
	key2 := []byte("key2")

	node1 := MV.NewVersionNode(1, 10, key1, []byte("value1"), false)
	node2 := MV.NewVersionNode(2, 20, key2, []byte("value2"), false)

	chain1 := mv.GetOrCreateVersionChain(key1)
	chain1.Insert(node1)

	chain2 := mv.GetOrCreateVersionChain(key2)
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
	mv := MV.NewMV()
	rv := newReadView(mv, 100)

	key := []byte("testkey")
	chain := mv.GetOrCreateVersionChain(key)

	node1 := MV.NewVersionNode(1, 10, key, []byte("value1"), false)
	chain.Insert(node1)

	_, _ = rv.Get(key)

	if len(rv.snapshot) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(rv.snapshot))
	}

	node2 := MV.NewVersionNode(2, 20, key, []byte("value2"), false)
	chain.Insert(node2)

	_, _ = rv.Get(key)

	if len(rv.snapshot) != 1 {
		t.Errorf("snapshot should still be 1 (key already in snapshot), got %d", len(rv.snapshot))
	}
}

func TestReadViewConcurrency(t *testing.T) {
	mv := MV.NewMV()
	rv := newReadView(mv, 100)

	key := []byte("testkey")
	node := MV.NewVersionNode(1, 10, key, []byte("value1"), false)
	chain := mv.GetOrCreateVersionChain(key)
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
	mv := MV.NewMV()
	rv := newReadView(mv, 100)

	key := []byte("testkey")
	chain := mv.GetOrCreateVersionChain(key)
	node := MV.NewVersionNode(1, 10, key, []byte("value1"), false)
	chain.Insert(node)

	rv.addSnapshot(key, chain.GetHead())

	if len(rv.snapshot) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(rv.snapshot))
	}

	if string(rv.snapshot[0].key) != "testkey" {
		t.Errorf("expected key 'testkey', got '%s'", string(rv.snapshot[0].key))
	}

	if rv.snapshot[0].head != chain.GetHead() {
		t.Error("snapshot head should match chain head")
	}
}
