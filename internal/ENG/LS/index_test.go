package ls

import (
	"testing"
)

func TestPrimaryIndexInsertFind(t *testing.T) {
	idx := newPrimaryIndex(1, 1)

	idx.Insert([]byte("key1"), []byte("primary1"))
	idx.Insert([]byte("key2"), []byte("primary2"))

	primary, found := idx.Find([]byte("key1"))
	if !found {
		t.Fatal("expected to find key1")
	}
	if string(primary) != "primary1" {
		t.Fatalf("expected primary1, got %s", string(primary))
	}

	primary, found = idx.Find([]byte("key2"))
	if !found {
		t.Fatal("expected to find key2")
	}
	if string(primary) != "primary2" {
		t.Fatalf("expected primary2, got %s", string(primary))
	}
}

func TestPrimaryIndexNotFound(t *testing.T) {
	idx := newPrimaryIndex(1, 1)

	idx.Insert([]byte("key1"), []byte("primary1"))

	_, found := idx.Find([]byte("nonexistent"))
	if found {
		t.Fatal("expected not to find nonexistent key")
	}
}

func TestPrimaryIndexDelete(t *testing.T) {
	idx := newPrimaryIndex(1, 1)

	idx.Insert([]byte("key1"), []byte("primary1"))
	idx.Insert([]byte("key2"), []byte("primary2"))

	deleted := idx.Delete([]byte("key1"))
	if !deleted {
		t.Fatal("expected delete to succeed")
	}

	_, found := idx.Find([]byte("key1"))
	if found {
		t.Fatal("expected key1 to be deleted")
	}

	primary, found := idx.Find([]byte("key2"))
	if !found {
		t.Fatal("expected key2 to still exist")
	}
	if string(primary) != "primary2" {
		t.Fatalf("expected primary2, got %s", string(primary))
	}
}

func TestPrimaryIndexLen(t *testing.T) {
	idx := newPrimaryIndex(1, 1)

	if idx.Len() != 0 {
		t.Fatalf("expected len 0, got %d", idx.Len())
	}

	idx.Insert([]byte("key1"), []byte("primary1"))
	if idx.Len() != 1 {
		t.Fatalf("expected len 1, got %d", idx.Len())
	}

	idx.Insert([]byte("key2"), []byte("primary2"))
	if idx.Len() != 2 {
		t.Fatalf("expected len 2, got %d", idx.Len())
	}

	idx.Delete([]byte("key1"))
	if idx.Len() != 1 {
		t.Fatalf("expected len 1, got %d", idx.Len())
	}
}

func TestPrimaryIndexIterator(t *testing.T) {
	idx := newPrimaryIndex(1, 1)

	idx.Insert([]byte("a"), []byte("p1"))
	idx.Insert([]byte("b"), []byte("p2"))
	idx.Insert([]byte("c"), []byte("p3"))

	it := idx.Iterator()
	count := 0
	for it.Next() {
		count++
	}
	if count != 3 {
		t.Fatalf("expected 3 iterations, got %d", count)
	}
}
