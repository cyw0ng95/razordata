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
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
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
	if len(read.Stats) != 3*10 {
		t.Errorf("Stats length = %d, want %d", len(read.Stats), 10*50)
	}
}

func TestCatalogNoPersist(t *testing.T) {
	cat := NewCatalogNoPersist(testEncodeEntry, testDecodeEntry)
	if cat == nil {
		t.Fatal("NewCatalogNoPersist returned nil")
	}
	defer cat.Close()

	// nextID should start at 1.
	if id, err := cat.NextID(); err != nil || id != 1 {
		t.Fatalf("NextID() = %d, err = %v; want 1, nil", id, err)
	}

	// Put should work without creating any file on disk.
	entry := &RawEntry{
		TableID:   1,
		Name:      "test_table",
		CreateSQL: "CREATE TABLE test_table (a INT)",
	}
	if err := cat.PutRaw(entry); err != nil {
		t.Fatalf("PutRaw: %v", err)
	}

	// GetByID should return the entry.
	got, err := cat.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "test_table" {
		t.Errorf("got.Name = %q, want %q", got.Name, "test_table")
	}

	// ByName should work.
	got2, err := cat.ByName("test_table")
	if err != nil {
		t.Fatalf("ByName: %v", err)
	}
	if got2.TableID != 1 {
		t.Errorf("got2.TableID = %d, want 1", got2.TableID)
	}

	// List should return one entry.
	list := cat.List()
	if len(list) != 1 {
		t.Fatalf("List() len = %d, want 1", len(list))
	}

	// Delete should work.
	if err := cat.Delete(1); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// After delete, GetByID should fail.
	if _, err := cat.GetByID(1); err == nil {
		t.Errorf("GetByID after delete: err = nil, want error")
	}

	// Close should succeed (no file cleanup).
	if err := cat.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestCatalogNoPersist_Reset verifies that Reset clears all entries
// and resets nextID. REQ002071.
func TestCatalogNoPersist_Reset(t *testing.T) {
	cat := NewCatalogNoPersist(testEncodeEntry, testDecodeEntry)
	if cat == nil {
		t.Fatal("NewCatalogNoPersist returned nil")
	}
	defer cat.Close()

	// Add some entries.
	for i := 0; i < 3; i++ {
		entry := &RawEntry{
			TableID:   uint64(i + 1),
			Name:      fmt.Sprintf("t%d", i),
			CreateSQL: fmt.Sprintf("CREATE TABLE t%d (a INT)", i),
		}
		if err := cat.PutRaw(entry); err != nil {
			t.Fatalf("PutRaw %d: %v", i, err)
		}
	}
	if cat.Len() != 3 {
		t.Fatalf("Len before reset = %d, want 3", cat.Len())
	}

	// Reset should clear everything.
	cat.Reset()
	if cat.Len() != 0 {
		t.Errorf("Len after reset = %d, want 0", cat.Len())
	}

	// nextID should be back to 1.
	if id, err := cat.NextID(); err != nil || id != 1 {
		t.Errorf("NextID after reset = %d, err = %v; want 1, nil", id, err)
	}

	// Should be able to add entries again.
	entry := &RawEntry{
		TableID:   1,
		Name:      "fresh",
		CreateSQL: "CREATE TABLE fresh (a INT)",
	}
	if err := cat.PutRaw(entry); err != nil {
		t.Fatalf("PutRaw after reset: %v", err)
	}
	if cat.Len() != 1 {
		t.Errorf("Len after re-add = %d, want 1", cat.Len())
	}
}
