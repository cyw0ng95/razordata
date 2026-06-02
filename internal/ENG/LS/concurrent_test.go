package ls

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentWrite_SingleKey(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "test_concurrent_single")

	e, err := newEngine(engineDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			e.Write([]byte("key1"), []byte(string(rune('0'+val%10))))
		}(i)
	}

	wg.Wait()

	e.Read([]byte("key1"))

	stats := e.GetStats()
	if stats.MemtableHits == 0 {
		t.Fatalf("expected memtable hits after concurrent writes")
	}
}

func TestConcurrentWrite_MultipleKeys(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "test_concurrent_multi")

	e, err := newEngine(engineDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	var wg sync.WaitGroup
	keyCount := 100

	for i := 0; i < keyCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := []byte(string(rune('a' + idx%26)))
			e.Write(key, []byte("value"))
		}(i)
	}

	wg.Wait()

	for i := 0; i < keyCount; i++ {
		key := []byte(string(rune('a' + i%26)))
		_, err := e.Read(key)
		if err != nil {
			t.Fatalf("failed to read key %c after concurrent writes: %v", 'a'+i%26, err)
		}
	}
}

func TestConcurrentWrite_Find(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "test_concurrent_find")

	e, err := newEngine(engineDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	for i := 0; i < 100; i++ {
		key := []byte(string(rune('a' + i%26)))
		e.Write(key, []byte("value"))
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := []byte(string(rune('a' + idx%26)))
			e.Read(key)
		}(i)
	}

	wg.Wait()
}

func TestConcurrentWrite_UpdateSameKey(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "test_concurrent_update")

	e, err := newEngine(engineDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	e.Write([]byte("key1"), []byte("initial"))

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			key := []byte("key1")
			e.Write(key, []byte(string(rune('0'+val))))
		}(i)
	}

	wg.Wait()

	val, err := e.Read([]byte("key1"))
	if err != nil {
		t.Fatalf("failed to read after concurrent updates: %v", err)
	}

	if len(val) != 1 {
		t.Fatalf("expected single byte value, got %d bytes", len(val))
	}
}

func TestConcurrentWrite_Stats(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "test_concurrent_stats")

	e, err := newEngine(engineDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := []byte(string(rune('a' + j%26)))
				e.Write(key, []byte("value"))
			}
		}()
	}

	wg.Wait()

	e.Read([]byte("a"))

	stats := e.GetStats()
	if stats.MemtableHits == 0 {
		t.Fatal("expected memtable hits")
	}
}

func TestSkipListConcurrentInsert(t *testing.T) {
	sl := New()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := []byte(string(rune('a' + idx%26)))
			sl.Insert(key, []byte("value"))
		}(i)
	}

	wg.Wait()

	if sl.Len() == 0 {
		t.Fatal("expected skiplist length > 0 after concurrent inserts")
	}
}

func TestSkipListConcurrentInsertFind(t *testing.T) {
	sl := New()

	for i := 0; i < 100; i++ {
		key := []byte(string(rune('a' + i%26)))
		sl.Insert(key, []byte("value"))
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := []byte(string(rune('a' + idx%26)))
			sl.Find(key)
		}(i)
	}

	wg.Wait()
}

func TestMemtableConcurrentInsert(t *testing.T) {
	mt := newMemtable(64 * 1024 * 1024)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := []byte(string(rune('a' + (idx*10+j)%26)))
				mt.Insert(key, []byte("value"))
			}
		}(i)
	}

	wg.Wait()

	if mt.Len() == 0 {
		t.Fatal("expected memtable length > 0 after concurrent inserts")
	}
}

func TestEngineConcurrentWriteRead(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "test_concurrent_write_read")

	e, err := newEngine(engineDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			key := []byte(string(rune('a' + i%26)))
			e.Write(key, []byte("value"))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			key := []byte(string(rune('a' + i%26)))
			e.Read(key)
		}
	}()

	wg.Wait()
}
