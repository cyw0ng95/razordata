package MV

import (
	"testing"
	"unsafe"
)

func TestGCVersionChain_Empty(t *testing.T) {
	mv := NewMV()
	got := mv.GCVersionChain([]byte("missing"), 100)
	if got != nil {
		t.Errorf("expected nil for missing key, got %d candidates", len(got))
	}
}

func TestGCVersionChain_NoCandidates(t *testing.T) {
	arena := newArena()
	mv := NewMV()
	key := []byte("k")
	n1 := NewVersionNode(arena, 1, 10, key, []byte("v1"), false)
	mv.Insert(key, n1)
	if !n1.Commit(20) {
		t.Fatal("commit failed")
	}

	got := mv.GCVersionChain(key, 10)
	if got != nil {
		t.Errorf("expected nil when oldestReadTS <= endTS, got %d", len(got))
	}
}

func TestGCVersionChain_OneCandidate(t *testing.T) {
	arena := newArena()
	mv := NewMV()
	key := []byte("k")
	n1 := NewVersionNode(arena, 1, 10, key, []byte("v1"), false)
	mv.Insert(key, n1)
	if !n1.Commit(20) {
		t.Fatal("commit failed")
	}

	got := mv.GCVersionChain(key, 30)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	if unsafe.Pointer(got[0]) != unsafe.Pointer(n1) {
		t.Errorf("expected pointer to n1, got different")
	}
}

func TestGCVersionChain_MultipleVersionsMixed(t *testing.T) {
	arena := newArena()
	mv := NewMV()
	key := []byte("k")

	// v1: commit at 20 (reclaimable when oldestReadTS > 20)
	v1 := NewVersionNode(arena, 1, 10, key, []byte("v1"), false)
	mv.Insert(key, v1)
	if !v1.Commit(20) {
		t.Fatal("commit v1 failed")
	}

	// v2: commit at 30 (reclaimable when oldestReadTS > 30)
	v2 := NewVersionNode(arena, 2, 15, key, []byte("v2"), false)
	mv.Insert(key, v2)
	if !v2.Commit(30) {
		t.Fatal("commit v2 failed")
	}

	// v3: commit at 50 (still visible at oldestReadTS=40)
	v3 := NewVersionNode(arena, 3, 20, key, []byte("v3"), false)
	mv.Insert(key, v3)
	if !v3.Commit(50) {
		t.Fatal("commit v3 failed")
	}

	// uncommitted head — endTS == MaxUint64, never reclaimable
	v4 := NewVersionNode(arena, 4, 25, key, []byte("v4"), false)
	mv.Insert(key, v4)

	cases := []struct {
		name        string
		oldest      uint64
		wantReclaim int
	}{
		{"below all commits", 15, 0},
		{"above v1 only", 25, 1},
		{"above v1+v2", 40, 2},
		{"above all committed", 100, 3}, // v4 still uncommitted
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mv.GCVersionChain(key, tc.oldest)
			if len(got) != tc.wantReclaim {
				t.Errorf("oldest=%d: expected %d candidates, got %d", tc.oldest, tc.wantReclaim, len(got))
			}
		})
	}
}

func TestGCVersionChain_ChainUnchanged(t *testing.T) {
	arena := newArena()
	mv := NewMV()
	key := []byte("k")
	v1 := NewVersionNode(arena, 1, 10, key, []byte("v1"), false)
	mv.Insert(key, v1)
	if !v1.Commit(20) {
		t.Fatal("commit failed")
	}

	// GC must not mutate the chain
	_ = mv.GCVersionChain(key, 100)
	chain := mv.VersionChain(key)
	if chain == nil {
		t.Fatal("chain disappeared after GC")
	}
	if chain.Head() != v1 {
		t.Error("head pointer changed after GC")
	}
	// v1 is visible at any readTS in [10,20]
	if visible := mv.FindVisible(key, 15); visible != v1 {
		t.Error("FindVisible broken after GC")
	}
}

func TestGCAllChains_Empty(t *testing.T) {
	mv := NewMV()
	got := mv.GCAllChains(100)
	if got != nil {
		t.Errorf("expected nil, got %d", len(got))
	}
}

func TestGCAllChains_MultipleKeys(t *testing.T) {
	arena := newArena()
	mv := NewMV()
	keys := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	for i, k := range keys {
		n := NewVersionNode(arena, uint64(i+1), 10, k, []byte("v"), false)
		mv.Insert(k, n)
		if !n.Commit(20) {
			t.Fatalf("commit %d failed", i)
		}
	}

	got := mv.GCAllChains(50)
	if len(got) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(got))
	}

	got2 := mv.GCAllChains(15)
	if got2 != nil {
		t.Errorf("expected nil when oldestReadTS < all endTS, got %d", len(got2))
	}
}

func TestNumChains(t *testing.T) {
	arena := newArena()
	mv := NewMV()
	if got := mv.NumChains(); got != 0 {
		t.Errorf("expected 0 chains, got %d", got)
	}
	mv.Insert([]byte("a"), NewVersionNode(arena, 1, 1, []byte("a"), []byte("v"), false))
	mv.Insert([]byte("b"), NewVersionNode(arena, 2, 2, []byte("b"), []byte("v"), false))
	if got := mv.NumChains(); got != 2 {
		t.Errorf("expected 2 chains, got %d", got)
	}
	// Insert into existing chain — count must not change
	mv.Insert([]byte("a"), NewVersionNode(arena, 3, 3, []byte("a"), []byte("v2"), false))
	if got := mv.NumChains(); got != 2 {
		t.Errorf("expected 2 chains after second insert on same key, got %d", got)
	}
}
