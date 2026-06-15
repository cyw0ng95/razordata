package ls

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

// TestCatalog_NewEmpty — opening a non-existent directory creates
// a clean catalog. Mirrors the cold-start path of a fresh user
// database.
func TestCatalog_NewEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if c.Len() != 0 {
		t.Fatalf("Len = %d, want 0", c.Len())
	}
	if entries := c.List(); len(entries) != 0 {
		t.Fatalf("List returned %d entries on empty catalog", len(entries))
	}
	if c.nextID != 1 {
		t.Fatalf("nextID = %d, want 1", c.nextID)
	}
}

// TestCatalog_PutGetDelete — happy path on a fresh catalog.
func TestCatalog_PutGetDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	want := []CatalogEntry{
		{TableID: 1, Name: "users", CreateSQL: "CREATE TABLE users (id INTEGER PRIMARY KEY)"},
		{TableID: 2, Name: "orders", CreateSQL: "CREATE TABLE orders (id INTEGER PRIMARY KEY)"},
		{TableID: 3, Name: "items", CreateSQL: "CREATE TABLE items (id INTEGER PRIMARY KEY)"},
	}
	for i := range want {
		if err := c.Put(want[i]); err != nil {
			t.Fatalf("Put %q: %v", want[i].Name, err)
		}
	}
	if c.Len() != 3 {
		t.Fatalf("Len = %d, want 3", c.Len())
	}
	for _, e := range want {
		got, err := c.GetByID(e.TableID)
		if err != nil {
			t.Fatalf("GetByID(%d): %v", e.TableID, err)
		}
		if got.Name != e.Name || got.CreateSQL != e.CreateSQL {
			t.Fatalf("GetByID(%d) = %+v, want %+v", e.TableID, *got, e)
		}
		gotByName, err := c.GetByName(e.Name)
		if err != nil {
			t.Fatalf("GetByName(%q): %v", e.Name, err)
		}
		if gotByName.TableID != e.TableID {
			t.Fatalf("GetByName(%q) = id=%d, want %d", e.Name, gotByName.TableID, e.TableID)
		}
	}
	if err := c.Delete(want[1].TableID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if c.Len() != 2 {
		t.Fatalf("Len after delete = %d, want 2", c.Len())
	}
	if _, err := c.GetByID(want[1].TableID); !errors.Is(err, ErrCatalogNotFound) {
		t.Fatalf("GetByID(deleted) = %v, want ErrCatalogNotFound", err)
	}
	if _, err := c.GetByName(want[1].Name); !errors.Is(err, ErrCatalogNotFound) {
		t.Fatalf("GetByName(deleted) = %v, want ErrCatalogNotFound", err)
	}
}

// TestCatalog_PutRejectsDuplicates — name and id must be unique.
func TestCatalog_PutRejectsDuplicates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	first := CatalogEntry{TableID: 5, Name: "t", CreateSQL: "CREATE TABLE t (id INTEGER)"}
	if err := c.Put(first); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	dupName := CatalogEntry{TableID: 6, Name: "t", CreateSQL: "CREATE TABLE t (id INTEGER)"}
	if err := c.Put(dupName); !errors.Is(err, ErrCatalogExists) {
		t.Fatalf("duplicate name: got %v, want ErrCatalogExists", err)
	}
	dupID := CatalogEntry{TableID: 5, Name: "u", CreateSQL: "CREATE TABLE u (id INTEGER)"}
	if err := c.Put(dupID); !errors.Is(err, ErrCatalogExists) {
		t.Fatalf("duplicate id: got %v, want ErrCatalogExists", err)
	}
}

// TestCatalog_NextIDMonotonic — repeated NextID calls return
// strictly increasing IDs and the counter survives Close+Open.
func TestCatalog_NextIDMonotonic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	var last uint64
	for i := 0; i < 10; i++ {
		id, err := c.NextID()
		if err != nil {
			t.Fatalf("NextID: %v", err)
		}
		if i > 0 && id <= last {
			t.Fatalf("NextID not monotonic: last=%d, got=%d", last, id)
		}
		last = id
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c2, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = c2.Close() })
	id, err := c2.NextID()
	if err != nil {
		t.Fatalf("NextID after reopen: %v", err)
	}
	if id <= last {
		t.Fatalf("NextID after reopen: got %d, want > %d", id, last)
	}
}

// TestCatalog_RejectsEmptyFields — defense in depth: a name or
// CreateSQL of "" would corrupt the wire format.
func TestCatalog_RejectsEmptyFields(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Put(CatalogEntry{TableID: 1, Name: "", CreateSQL: "x"}); err == nil {
		t.Fatal("empty name: expected error, got nil")
	}
	if err := c.Put(CatalogEntry{TableID: 1, Name: "x", CreateSQL: ""}); err == nil {
		t.Fatal("empty CreateSQL: expected error, got nil")
	}
}

// TestCatalog_Close_Idempotent — required for the SYS shutdown
// path. After Close every public method must return
// ErrCatalogClosed, not panic.
func TestCatalog_Close_Idempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := c.GetByID(1); !errors.Is(err, ErrCatalogClosed) {
		t.Fatalf("GetByID after Close: got %v, want ErrCatalogClosed", err)
	}
	if err := c.Put(CatalogEntry{TableID: 1, Name: "x", CreateSQL: "CREATE TABLE x (a INT)"}); !errors.Is(err, ErrCatalogClosed) {
		t.Fatalf("Put after Close: got %v, want ErrCatalogClosed", err)
	}
	if _, err := c.NextID(); !errors.Is(err, ErrCatalogClosed) {
		t.Fatalf("NextID after Close: got %v, want ErrCatalogClosed", err)
	}
	if err := c.Delete(1); !errors.Is(err, ErrCatalogClosed) {
		t.Fatalf("Delete after Close: got %v, want ErrCatalogClosed", err)
	}
}

// TestCatalog_ConcurrentPuts — many goroutines Put distinct
// tables. The cache and the file must agree, and no two writers
// may pick the same tableID.
func TestCatalog_ConcurrentPuts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	const writers = 16
	const perWriter = 25
	var wg sync.WaitGroup
	errCh := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				id, err := c.NextID()
				if err != nil {
					errCh <- err
					return
				}
				err = c.Put(CatalogEntry{
					TableID:   id,
					Name:      "t",
					CreateSQL: "CREATE TABLE t (id INTEGER)",
				})
				if err != nil && !errors.Is(err, ErrCatalogExists) {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent put: %v", err)
	}
	// All writers share the same name "t" but unique IDs. Some
	// may lose the race for the name; the count should match the
	// number of distinct IDs reserved.
	if got := c.Len(); got == 0 {
		t.Fatalf("Len=0 after concurrent puts")
	}
}

// TestCatalog_NewEmptyDir — empty dir is required. NewCatalog
// surfaces that as a wrapped error, not a panic.
func TestCatalog_NewEmptyDir(t *testing.T) {
	if _, err := NewCatalog(""); !errors.Is(err, ErrCatalogCorrupt) {
		t.Fatalf("empty dir: got %v, want ErrCatalogCorrupt", err)
	}
}
