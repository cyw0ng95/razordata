package ls

import (
	"bytes"
	"sync"
	"testing"
)

func TestNewSkipList(t *testing.T) {
	sl := New()
	if sl == nil {
		t.Fatal("expected non-nil skipList")
	}
	if sl.Len() != 0 {
		t.Errorf("expected len=0, got %d", sl.Len())
	}
}

func TestInsertSingle(t *testing.T) {
	sl := New()
	sl.Insert([]byte("key1"), []byte("value1"))

	if sl.Len() != 1 {
		t.Errorf("expected len=1, got %d", sl.Len())
	}

	val, found := sl.Find([]byte("key1"))
	if !found {
		t.Error("expected to find key1")
	}
	if !bytes.Equal(val, []byte("value1")) {
		t.Errorf("expected value1, got %s", val)
	}
}

func TestInsertMultiple(t *testing.T) {
	sl := New()

	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		val := []byte{byte(i + 100)}
		sl.Insert(key, val)
	}

	if sl.Len() != 100 {
		t.Errorf("expected len=100, got %d", sl.Len())
	}

	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		expected := []byte{byte(i + 100)}
		val, found := sl.Find(key)
		if !found {
			t.Errorf("expected to find key %d", i)
		}
		if !bytes.Equal(val, expected) {
			t.Errorf("expected value %d, got %s", i+100, val)
		}
	}
}

func TestFindNonExistent(t *testing.T) {
	sl := New()
	sl.Insert([]byte("key1"), []byte("value1"))

	_, found := sl.Find([]byte("nonexistent"))
	if found {
		t.Error("expected not to find nonexistent key")
	}
}

func TestInsertDuplicateKey(t *testing.T) {
	sl := New()
	sl.Insert([]byte("key1"), []byte("value1"))
	sl.Insert([]byte("key1"), []byte("value2"))

	if sl.Len() != 1 {
		t.Errorf("expected len=1 after duplicate insert, got %d", sl.Len())
	}

	val, found := sl.Find([]byte("key1"))
	if !found {
		t.Error("expected to find key1")
	}
	if !bytes.Equal(val, []byte("value2")) {
		t.Errorf("expected value2 (last insert), got %s", val)
	}
}

func TestIteratorEmpty(t *testing.T) {
	sl := New()
	it := sl.Iterator()

	if it.Next() {
		t.Error("expected no elements in empty list")
	}
}

func TestIteratorSingle(t *testing.T) {
	sl := New()
	sl.Insert([]byte("key1"), []byte("value1"))

	it := sl.Iterator()
	if !it.Next() {
		t.Error("expected at least one element")
	}
	if !bytes.Equal(it.Key(), []byte("key1")) {
		t.Errorf("expected key1, got %s", it.Key())
	}
	if !bytes.Equal(it.Value(), []byte("value1")) {
		t.Errorf("expected value1, got %s", it.Value())
	}
}

func TestIteratorMultiple(t *testing.T) {
	sl := New()
	for i := 0; i < 10; i++ {
		sl.Insert([]byte{byte(i)}, []byte{byte(i + 10)})
	}

	count := 0
	it := sl.Iterator()
	for it.Next() {
		count++
	}
	if count != 10 {
		t.Errorf("expected 10 elements, got %d", count)
	}
}

func TestIteratorAllKeys(t *testing.T) {
	sl := New()
	keys := [][]byte{
		[]byte("a"),
		[]byte("b"),
		[]byte("c"),
		[]byte("d"),
		[]byte("e"),
	}
	for i, k := range keys {
		sl.Insert(k, []byte{byte(i)})
	}

	found := make(map[string]bool)
	it := sl.Iterator()
	for it.Next() {
		found[string(it.Key())] = true
	}

	for _, k := range keys {
		if !found[string(k)] {
			t.Errorf("expected to find key %s in iterator", k)
		}
	}
}

func TestConcurrentInsert(t *testing.T) {
	sl := New()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := []byte{byte(id*100 + j)}
				val := []byte{byte(j)}
				sl.Insert(key, val)
			}
		}(i)
	}

	wg.Wait()
}

func TestConcurrentInsertFind(t *testing.T) {
	sl := New()
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				key := []byte{byte(id)}
				sl.Insert(key, []byte{byte(j)})
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < 5; i++ {
		key := []byte{byte(i)}
		val, found := sl.Find(key)
		if !found {
			t.Errorf("expected to find key %d", i)
		}
		if len(val) == 0 {
			t.Errorf("expected non-empty value for key %d", i)
		}
	}
}

func TestIteratorAfterConcurrentInsert(t *testing.T) {
	sl := New()
	var wg sync.WaitGroup

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				key := []byte{byte(id*50 + j)}
				sl.Insert(key, []byte{byte(j)})
			}
		}(i)
	}

	wg.Wait()

	count := 0
	it := sl.Iterator()
	for it.Next() {
		count++
	}

	if count == 0 {
		t.Error("expected at least some elements after concurrent insert")
	}
}

func TestFindAfterConcurrentInsert(t *testing.T) {
	sl := New()
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				key := []byte{byte(id)}
				val := []byte{byte(id*100 + j)}
				sl.Insert(key, val)
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < 4; i++ {
		key := []byte{byte(i)}
		_, found := sl.Find(key)
		if !found {
			t.Errorf("expected to find key %d", i)
		}
	}
}