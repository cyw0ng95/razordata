package ls

import (
	"fmt"
	"sync"
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

// REQ000630: Concurrent inserts of the same key via CAS retry produce
// exactly one entry and a valid primary value.
func TestPrimaryIndexInsertCASRetry(t *testing.T) {
	idx := newPrimaryIndex(1, 1)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			idx.Insert([]byte("key1"), []byte(fmt.Sprintf("primary%d", val)))
		}(i)
	}
	wg.Wait()

	if idx.Len() != 1 {
		t.Fatalf("expected Len() == 1 after concurrent inserts of same key, got %d", idx.Len())
	}

	primary, found := idx.Find([]byte("key1"))
	if !found {
		t.Fatal("expected to find key1")
	}
	if len(primary) == 0 {
		t.Fatal("expected non-empty primary")
	}
}

// REQ000630: Iterator on an empty index returns false immediately.
func TestPrimaryIndexIteratorEmpty(t *testing.T) {
	idx := newPrimaryIndex(1, 1)
	it := idx.Iterator()
	if it.Next() {
		t.Fatal("expected Next() to return false on empty index")
	}
}

// REQ000630: Seek with nil key returns false (no panic).
func TestPrimaryIndexSeekNil(t *testing.T) {
	idx := newPrimaryIndex(1, 1)
	idx.Insert([]byte("key1"), []byte("primary1"))
	it := idx.Iterator()

	result := it.Seek(nil)
	if result {
		t.Fatal("expected Seek(nil) to return false")
	}
}
