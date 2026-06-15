package MV

import (
	"testing"
)

func TestNewMV(t *testing.T) {
	mv := NewMV()
	if mv == nil {
		t.Fatal("expected non-nil MV")
	}
}

func TestMVGetVersionChain(t *testing.T) {
	mv := NewMV()

	chain := mv.GetVersionChain([]byte("nonexistent"))
	if chain != nil {
		t.Error("expected nil for nonexistent key")
	}
}

func TestMVGetOrCreateVersionChain(t *testing.T) {
	mv := NewMV()

	chain1 := mv.GetOrCreateVersionChain([]byte("key1"))
	if chain1 == nil {
		t.Fatal("expected non-nil chain")
	}

	chain2 := mv.GetOrCreateVersionChain([]byte("key1"))
	if chain2 != chain1 {
		t.Error("expected same chain for same key")
	}
}

func TestMVInsert(t *testing.T) {
	arena := newArena()
	mv := NewMV()

	node := NewVersionNode(arena, 1, 10, []byte("key"), []byte("value"), false)

	if !mv.Insert([]byte("key"), node) {
		t.Error("expected insert to succeed")
	}

	chain := mv.GetVersionChain([]byte("key"))
	if chain == nil {
		t.Fatal("expected chain after insert")
	}

	head := chain.GetHead()
	if head != node {
		t.Error("expected head to be inserted node")
	}
}

func TestMVFindVisible(t *testing.T) {
	arena := newArena()
	mv := NewMV()

	node := NewVersionNode(arena, 1, 10, []byte("key"), []byte("value"), false)
	mv.Insert([]byte("key"), node)

	found := mv.FindVisible([]byte("key"), 100)
	if found != node {
		t.Error("expected to find node at readTS 100")
	}

	found = mv.FindVisible([]byte("nonexistent"), 100)
	if found != nil {
		t.Error("expected nil for nonexistent key")
	}
}

func TestMVMultipleKeys(t *testing.T) {
	arena := newArena()
	mv := NewMV()

	node1 := NewVersionNode(arena, 1, 10, []byte("key1"), []byte("value1"), false)
	node2 := NewVersionNode(arena, 2, 20, []byte("key2"), []byte("value2"), false)

	mv.Insert([]byte("key1"), node1)
	mv.Insert([]byte("key2"), node2)

	found := mv.FindVisible([]byte("key1"), 100)
	if found != node1 {
		t.Error("expected to find key1")
	}

	found = mv.FindVisible([]byte("key2"), 100)
	if found != node2 {
		t.Error("expected to find key2")
	}
}

func TestMVVersionNodeAccessors(t *testing.T) {
	arena := newArena()
	node := NewVersionNode(arena, 1, 10, []byte("key"), []byte("value"), false)

	if node.TxnID() != 1 {
		t.Errorf("expected TxnID 1, got %d", node.TxnID())
	}
	if node.BeginTS() != 10 {
		t.Errorf("expected BeginTS 10, got %d", node.BeginTS())
	}
	if node.EndTS() != maxUint64 {
		t.Errorf("expected EndTS maxUint64, got %d", node.EndTS())
	}
	if string(node.Key()) != "key" {
		t.Errorf("expected Key 'key', got %s", string(node.Key()))
	}
	if string(node.Value()) != "value" {
		t.Errorf("expected Value 'value', got %s", string(node.Value()))
	}
	if node.Deleted() {
		t.Error("expected Deleted false")
	}
}

func TestMVVersionNodeDeleted(t *testing.T) {
	arena := newArena()
	node := NewVersionNode(arena, 1, 10, []byte("key"), []byte(""), true)

	if !node.Deleted() {
		t.Error("expected Deleted true")
	}
}

func TestMVVersionNodeCommit(t *testing.T) {
	arena := newArena()
	mv := NewMV()

	node := NewVersionNode(arena, 1, 10, []byte("key"), []byte("value"), false)
	mv.Insert([]byte("key"), node)

	chain := mv.GetVersionChain([]byte("key"))
	if !chain.Commit(node, 100) {
		t.Error("expected commit to succeed")
	}

	if node.EndTS() != 100 {
		t.Errorf("expected EndTS 100, got %d", node.EndTS())
	}
}

func TestArenaSize(t *testing.T) {
	a := newArena()

	// REQ000064: total size is now young + old.
	expected := a.YoungSize() + a.OldSize()
	if a.Size() != expected {
		t.Errorf("expected size %d, got %d", expected, a.Size())
	}
}

func BenchmarkMVInsert(b *testing.B) {
	mv := NewMV()
	arena := newArena()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		node := NewVersionNode(arena, uint64(i), uint64(i), []byte("key"), []byte("value"), false)
		mv.Insert([]byte("key"), node)
	}
}

func BenchmarkMVFindVisible(b *testing.B) {
	mv := NewMV()
	arena := newArena()

	for i := 0; i < 1000; i++ {
		node := NewVersionNode(arena, uint64(i), uint64(i*10), []byte("key"), []byte("value"), false)
		mv.Insert([]byte("key"), node)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mv.FindVisible([]byte("key"), 5000)
	}
}
