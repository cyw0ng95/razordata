package ct

import (
	"fmt"
	"sync"
	"testing"
)

// testEncodeEntry is a minimal encoder for testing.
func testEncodeEntry(e *RawEntry, buf []byte) []byte {
	var tmp [8]byte
	tmp[0] = byte(len(e.Name))
	buf = append(buf, tmp[:1]...)
	buf = append(buf, []byte(e.Name)...)
	return buf
}

// testDecodeEntry is a minimal decoder for testing.
func testDecodeEntry(data []byte, off int, e *RawEntry) (int, error) {
	if off >= len(data) {
		return off, fmt.Errorf("truncated")
	}
	nameLen := int(data[off])
	off++
	if off+nameLen > len(data) {
		return off, fmt.Errorf("truncated name")
	}
	e.Name = string(data[off : off+nameLen])
	e.TableID = uint64(len(e.Name))
	off += nameLen
	return off, nil
}

func TestCatalog_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	cat, err := NewCatalog(dir, testEncodeEntry, testDecodeEntry)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	defer cat.Close()

	// Seed the catalog with entries
	for i := uint64(1); i <= 10; i++ {
		entry := &RawEntry{
			TableID:   i,
			Name:      fmt.Sprintf("t%d", i),
			CreateSQL: fmt.Sprintf("CREATE TABLE t%d (a INT)", i),
		}
		if err := cat.PutRaw(entry); err != nil {
			t.Fatalf("PutRaw(%d): %v", i, err)
		}
	}

	var wg sync.WaitGroup

	// Concurrent readers: GetByID (safe, returns copy)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				id := uint64(j%10 + 1)
				e, err := cat.GetByID(id)
				if err != nil {
					t.Errorf("GetByID(%d): %v", id, err)
					return
				}
				if e == nil {
					t.Errorf("GetByID(%d) returned nil", id)
					return
				}
			}
		}()
	}

	// Concurrent readers: ByName (safe, returns copy)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				name := fmt.Sprintf("t%d", j%10+1)
				e, err := cat.ByName(name)
				if err != nil {
					t.Errorf("ByName(%s): %v", name, err)
					return
				}
				if e == nil {
					t.Errorf("ByName(%s) returned nil", name)
					return
				}
			}
		}()
	}

	// Concurrent CacheSnapshot readers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				snap := cat.CacheSnapshot()
				if len(snap) < 1 {
					t.Error("CacheSnapshot returned empty map")
					return
				}
			}
		}()
	}

	wg.Wait()
}

func TestCatalog_UpdateEntryConcurrent(t *testing.T) {
	dir := t.TempDir()
	cat, err := NewCatalog(dir, testEncodeEntry, testDecodeEntry)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	defer cat.Close()

	entry := &RawEntry{
		TableID:   1,
		Name:      "test",
		CreateSQL: "CREATE TABLE test (a INT)",
	}
	if err := cat.PutRaw(entry); err != nil {
		t.Fatalf("PutRaw: %v", err)
	}

	var wg sync.WaitGroup

	// Concurrent updates via UpdateEntry (safe — holds write lock)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				err := cat.UpdateEntry(1, func(raw *RawEntry) error {
					raw.Stats = append(raw.Stats, byte(id))
					return nil
				})
				if err != nil {
					t.Errorf("UpdateEntry: %v", err)
					return
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify the stats blob has the expected number of bytes
	read, err := cat.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(read.Stats) != 10*50 {
		t.Errorf("Stats length = %d, want %d", len(read.Stats), 10*50)
	}
}
