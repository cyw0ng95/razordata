package MV

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestVersionNodeFields(t *testing.T) {
	node := NewVersionNode(1, 100, []byte("key"), []byte("value"), false)

	if node.txnID != 1 {
		t.Errorf("expected txnID 1, got %d", node.txnID)
	}
	if node.beginTS != 100 {
		t.Errorf("expected beginTS 100, got %d", node.beginTS)
	}
	if node.endTS.Load() != maxUint64 {
		t.Errorf("expected endTS maxUint64, got %d", node.endTS.Load())
	}
	if string(node.key) != "key" {
		t.Errorf("expected key 'key', got %s", string(node.key))
	}
	if string(node.value) != "value" {
		t.Errorf("expected value 'value', got %s", string(node.value))
	}
	if node.deleted {
		t.Error("expected deleted false")
	}
}

func TestVersionNodeIsUncommitted(t *testing.T) {
	node := NewVersionNode(1, 100, []byte("key"), []byte("value"), false)

	if !node.IsUncommitted() {
		t.Error("new node should be uncommitted")
	}
}

func TestVersionNodeIsVisible(t *testing.T) {
	node := NewVersionNode(1, 50, []byte("key"), []byte("value"), false)

	if !node.IsVisible(100) {
		t.Error("node should be visible at readTS 100 (beginTS 50 < 100 && endTS=maxUint64 >= 100)")
	}

	if node.IsVisible(30) {
		t.Error("node should NOT be visible at readTS 30 (beginTS 50 >= 30)")
	}

	if node.IsVisible(50) {
		t.Error("node should NOT be visible at readTS 50 (beginTS 50 >= 50, version not yet visible)")
	}

	if !node.IsVisible(51) {
		t.Error("node should be visible at readTS 51 (beginTS 50 < 51)")
	}
}

func TestVersionNodeCommittedVisibility(t *testing.T) {
	node := NewVersionNode(1, 50, []byte("key"), []byte("value"), false)
	node.endTS.Store(200)

	if !node.IsVisible(100) {
		t.Error("node should be visible at readTS 100 (50 < 100 && 200 >= 100)")
	}

	if !node.IsVisible(200) {
		t.Error("node should be visible at readTS 200 (50 < 200 && 200 >= 200)")
	}

	if node.IsVisible(250) {
		t.Error("node should not be visible at readTS 250 (200 < 250)")
	}

	if node.IsVisible(49) {
		t.Error("node should not be visible at readTS 49 (50 >= 49)")
	}
}

func TestVersionChainInsert(t *testing.T) {
	vc := &VersionChain{}

	node1 := NewVersionNode(1, 10, []byte("key1"), []byte("value1"), false)
	node2 := NewVersionNode(2, 20, []byte("key2"), []byte("value2"), false)

	if !vc.Insert(node1) {
		t.Error("first insert should succeed")
	}

	if !vc.Insert(node2) {
		t.Error("second insert should succeed")
	}

	head := vc.GetHead()
	if head != node2 {
		t.Error("head should be node2 (newest)")
	}

	if head.Next() != node1 {
		t.Error("next should be node1")
	}
}

func TestVersionChainInsertSameKey(t *testing.T) {
	vc := &VersionChain{}

	node1 := NewVersionNode(1, 10, []byte("key"), []byte("value1"), false)
	node2 := NewVersionNode(2, 20, []byte("key"), []byte("value2"), false)

	vc.Insert(node1)
	vc.Insert(node2)

	head := vc.GetHead()
	if head != node2 {
		t.Error("head should be node2")
	}
}

func TestVersionChainCommit(t *testing.T) {
	vc := &VersionChain{}
	node := NewVersionNode(1, 10, []byte("key"), []byte("value"), false)

	if !vc.Commit(node, 100) {
		t.Error("commit should succeed")
	}

	if node.endTS.Load() != 100 {
		t.Errorf("expected endTS 100, got %d", node.endTS.Load())
	}
}

func TestVersionChainCommitTwice(t *testing.T) {
	vc := &VersionChain{}
	node := NewVersionNode(1, 10, []byte("key"), []byte("value"), false)

	vc.Commit(node, 100)

	if vc.Commit(node, 200) {
		t.Error("second commit should fail")
	}

	if node.endTS.Load() != 100 {
		t.Errorf("endTS should still be 100, got %d", node.endTS.Load())
	}
}

func TestVersionChainFindVisible(t *testing.T) {
	vc := &VersionChain{}

	node1 := NewVersionNode(1, 10, []byte("key"), []byte("v1"), false)
	node2 := NewVersionNode(2, 20, []byte("key"), []byte("v2"), false)
	node3 := NewVersionNode(3, 30, []byte("key"), []byte("v3"), false)

	vc.Insert(node1)
	vc.Insert(node2)
	vc.Insert(node3)

	vc.Commit(node2, 25)
	vc.Commit(node1, 15)

	visible := vc.FindVisible(5)
	if visible != nil {
		t.Errorf("at readTS 5, expected nil (beginTS 10 >= 5), got %v", visible)
	}

	visible = vc.FindVisible(15)
	if visible != node1 {
		t.Errorf("at readTS 15, expected node1 (beginTS 10 < 15 && 15 >= 15), got %v", visible)
	}

	visible = vc.FindVisible(35)
	if visible != node3 {
		t.Errorf("at readTS 35, expected node3 (beginTS 30 < 35 && uncommitted), got %v", visible)
	}
}

func TestVersionChainFindVisibleDeleted(t *testing.T) {
	vc := &VersionChain{}

	node1 := NewVersionNode(1, 10, []byte("key"), []byte("value"), false)
	node2 := NewVersionNode(2, 20, []byte("key"), []byte(""), true)

	vc.Insert(node1)
	vc.Insert(node2)

	vc.Commit(node2, 25)
	vc.Commit(node1, 15)

	visible := vc.FindVisible(12)
	if visible != node1 {
		t.Errorf("at readTS 12, expected node1 (beginTS 10 < 12 && endTS 15 >= 12), got %v", visible)
	}

	visible = vc.FindVisible(18)
	if visible != nil {
		t.Errorf("at readTS 18, expected nil (node1: 10 < 18 but 15 < 18; node2: 20 >= 18), got %v", visible)
	}

	visible = vc.FindVisible(28)
	if visible != nil {
		t.Errorf("at readTS 28, expected nil (node1: 15 < 28 but 15 < 28; node2: 20 < 28 but 25 < 28), got %v", visible)
	}
}

func TestVersionChainConcurrency(t *testing.T) {
	vc := &VersionChain{}
	var inserted int64

	var wg sync.WaitGroup
	goroutines := 10
	iterations := 100

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				node := NewVersionNode(uint64(id), uint64(j*10), []byte("key"), []byte("value"), false)
				if vc.Insert(node) {
					atomic.AddInt64(&inserted, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	head := vc.GetHead()
	if head == nil {
		t.Fatal("chain head should not be nil")
	}

	count := 0
	for node := head; node != nil; node = node.next.Load() {
		count++
	}

	if count != int(inserted) {
		t.Errorf("expected %d nodes in chain, counted %d", inserted, count)
	}
}

func TestVersionNodeDeleted(t *testing.T) {
	node := NewVersionNode(1, 10, []byte("key"), []byte("value"), true)

	if !node.deleted {
		t.Error("expected deleted to be true")
	}
}

func TestVersionChainEmpty(t *testing.T) {
	vc := &VersionChain{}

	if vc.GetHead() != nil {
		t.Error("empty chain should have nil head")
	}

	visible := vc.FindVisible(100)
	if visible != nil {
		t.Error("empty chain should return nil for FindVisible")
	}
}

func TestVersionChainGetHead(t *testing.T) {
	vc := &VersionChain{}

	if vc.GetHead() != nil {
		t.Error("empty chain head should be nil")
	}

	node := NewVersionNode(1, 10, []byte("key"), []byte("value"), false)
	vc.Insert(node)

	if vc.GetHead() != node {
		t.Error("head should be the inserted node")
	}
}

func TestVersionChainFindVisibleMultipleVersions(t *testing.T) {
	vc := &VersionChain{}

	node1 := NewVersionNode(1, 10, []byte("key"), []byte("v1"), false)
	node2 := NewVersionNode(2, 20, []byte("key"), []byte("v2"), false)

	vc.Insert(node1)
	vc.Insert(node2)

	vc.Commit(node1, 15)

	visible := vc.FindVisible(12)
	if visible != node1 {
		t.Errorf("at readTS 12, expected node1 (beginTS 10 < 12 && 15 >= 12), got %v", visible)
	}

	visible = vc.FindVisible(25)
	if visible != node2 {
		t.Errorf("at readTS 25, expected node2 (beginTS 20 < 25 && uncommitted), got %v", visible)
	}
}

func BenchmarkVersionChainInsert(b *testing.B) {
	vc := &VersionChain{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		node := NewVersionNode(uint64(i), uint64(i), []byte("key"), []byte("value"), false)
		vc.Insert(node)
	}
}

func BenchmarkVersionChainFindVisible(b *testing.B) {
	vc := &VersionChain{}

	for i := 0; i < 1000; i++ {
		node := NewVersionNode(uint64(i), uint64(i*10), []byte("key"), []byte("value"), false)
		vc.Insert(node)
	}

	b.ResetTimer()
	readTS := uint64(5000)
	for i := 0; i < b.N; i++ {
		vc.FindVisible(readTS)
	}
}

func BenchmarkArenaAlloc(b *testing.B) {
	a := newArena()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Alloc(64)
	}
}

func BenchmarkArenaAllocContention(b *testing.B) {
	a := newArena()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			a.Alloc(64)
		}
	})
}
