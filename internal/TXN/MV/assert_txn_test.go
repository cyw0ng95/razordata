//go:build debug

package MV

import (
	"testing"
)

func TestTXN_Assert_VersionChainOrder_HappyPath(t *testing.T) {
	mv := NewMV()
	n1 := NewVersionNode(nil, 1, 100, []byte("key"), []byte("v1"), false)
	n2 := NewVersionNode(nil, 2, 200, []byte("key"), []byte("v2"), false)

	if !mv.Insert([]byte("key"), n2) {
		t.Fatal("first insert (newer) failed")
	}
	if !mv.Insert([]byte("key"), n1) {
		t.Fatal("second insert (older) failed")
	}

	chain := mv.VersionChain([]byte("key"))
	if chain == nil {
		t.Fatal("expected non-nil chain")
	}
	head := chain.Head()
	if head == nil {
		t.Fatal("expected non-nil head")
	}
	if head.BeginTS() != 100 {
		t.Fatalf("expected head beginTS=100, got %d", head.BeginTS())
	}
	next := head.Next()
	if next == nil {
		t.Fatal("expected non-nil next")
	}
	if next.BeginTS() != 200 {
		t.Fatalf("expected next beginTS=200, got %d", next.BeginTS())
	}
}

func TestTXN_Assert_VersionChainOrder_Descending(t *testing.T) {
	mv := NewMV()
	n1 := NewVersionNode(nil, 1, 200, []byte("key"), []byte("v2"), false)
	n2 := NewVersionNode(nil, 2, 100, []byte("key"), []byte("v1"), false)

	if !mv.Insert([]byte("key"), n1) {
		t.Fatal("first insert (newer) failed")
	}
	if !mv.Insert([]byte("key"), n2) {
		t.Fatal("second insert (older) failed")
	}

	chain := mv.VersionChain([]byte("key"))
	if chain == nil {
		t.Fatal("expected non-nil chain")
	}
	head := chain.Head()
	if head == nil {
		t.Fatal("expected non-nil head")
	}
	if head.BeginTS() != 100 {
		t.Fatalf("expected head beginTS=100 (older inserted after newer), got %d", head.BeginTS())
	}
}