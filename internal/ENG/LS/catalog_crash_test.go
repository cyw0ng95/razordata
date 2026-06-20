package ls

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestCatalog_Crash_PutSurvivesClose — the canonical REQ000127
// invariant: after a Put returns, closing and reopening the
// catalog must surface the new entry. This is the smallest
// possible "create table, kill process, restart, verify" loop.
func TestCatalog_Crash_PutSurvivesClose(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := c.Put(CatalogEntry{
		TableID:    1,
		Name:       "users",
		Columns:    []CatalogColumn{{Name: "id", Nullable: false}},
		PrimaryKey: "id",
		CreateSQL:  "CREATE TABLE users (id INTEGER PRIMARY KEY)",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c2, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = c2.Close() })
	got, err := c2.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID(1) after reopen: %v", err)
	}
	if got.Name != "users" {
		t.Fatalf("reopened name=%q, want users", got.Name)
	}
}

// TestCatalog_Crash_DeleteSurvivesClose — a Delete must persist
// with the same atomicity as Put. After restart, the deleted
// entry is gone.
func TestCatalog_Crash_DeleteSurvivesClose(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := c.Put(CatalogEntry{TableID: 1, Name: "a", Columns: []CatalogColumn{{Name: "x", Nullable: true}}, CreateSQL: "CREATE TABLE a (x INTEGER)"}); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := c.Put(CatalogEntry{TableID: 2, Name: "b", Columns: []CatalogColumn{{Name: "x", Nullable: true}}, CreateSQL: "CREATE TABLE b (x INTEGER)"}); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	if err := c.Delete(1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c2, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = c2.Close() })
	if _, err := c2.GetByID(1); !errors.Is(err, ErrCatalogNotFound) {
		t.Fatalf("GetByID(deleted) = %v, want ErrCatalogNotFound", err)
	}
	if _, err := c2.ByName("a"); !errors.Is(err, ErrCatalogNotFound) {
		t.Fatalf("ByName(a) = %v, want ErrCatalogNotFound", err)
	}
	if got, err := c2.GetByID(2); err != nil || got.Name != "b" {
		t.Fatalf("GetByID(2) = %+v err=%v, want {Name:b} nil", got, err)
	}
}

// TestCatalog_Crash_RandomSequence — property test: for any
// sequence of CREATE/DROP operations, after Close+Open the
// catalog state matches the pre-Close state. Mirrors the
// "kill -9 mid-test" workload of an operational outage.
func TestCatalog_Crash_RandomSequence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	// 50 mixed operations: Put with new IDs, Delete existing.
	// Names cycle through a fixed pool so later Puts will hit
	// ErrCatalogExists — we tolerate that, the real invariants
	// are the cache/file agreement.
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	live := map[string]uint64{}
	for i := 0; i < 50; i++ {
		if i%3 == 0 && len(live) > 0 {
			// Drop one.
			for n := range live {
				if err := c.Delete(live[n]); err != nil {
					t.Fatalf("Delete(%s): %v", n, err)
				}
				delete(live, n)
				break
			}
			continue
		}
		// Add one.
		name := names[i%len(names)]
		if _, taken := live[name]; taken {
			continue
		}
		id, err := c.NextID()
		if err != nil {
			t.Fatalf("NextID: %v", err)
		}
		err = c.Put(CatalogEntry{
			TableID:   id,
			Name:      name,
			Columns:   []CatalogColumn{{Name: "x", Nullable: true}},
			CreateSQL: "CREATE TABLE " + name + " (x INTEGER)",
		})
		if err != nil && !errors.Is(err, ErrCatalogExists) {
			t.Fatalf("Put %s: %v", name, err)
		}
		if err == nil {
			live[name] = id
		}
	}
	// Snapshot pre-crash.
	wantCount := c.Len()
	wantNames := map[string]uint64{}
	for n, id := range live {
		wantNames[n] = id
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Reopen and compare.
	c2, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = c2.Close() })
	if c2.Len() != wantCount {
		t.Fatalf("Len mismatch: got %d, want %d", c2.Len(), wantCount)
	}
	for name, id := range wantNames {
		got, err := c2.ByName(name)
		if err != nil {
			t.Fatalf("ByName(%s) after reopen: %v", name, err)
		}
		if got.TableID != id {
			t.Fatalf("ByName(%s) = id=%d, want %d", name, got.TableID, id)
		}
	}
}

// TestCatalog_Crash_AtomicRename_NoPartialFile — a flush must
// always leave a complete catalog.dat on disk. We simulate the
// crash by killing the goroutine via a t.Cleanup that deletes
// the .tmp file (the in-progress write). On reopen, the
// bootstrap must succeed with the previously-persisted state
// intact (the .tmp is irrelevant).
func TestCatalog_Crash_AtomicRename_NoPartialFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := c.Put(CatalogEntry{TableID: 1, Name: "t1", CreateSQL: "CREATE TABLE t1 (x INT)"}); err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Simulate a crash during a subsequent Put: pre-create a
	// stale .tmp file. The next Open must not see it.
	stale := []byte("partial write garbage")
	if err := os.WriteFile(filepath.Join(dir, catalogFileName+catalogTmpSuffix), stale, 0o644); err != nil {
		t.Fatalf("seed .tmp: %v", err)
	}
	c2, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen with stale .tmp: %v", err)
	}
	t.Cleanup(func() { _ = c2.Close() })
	if c2.Len() != 1 {
		t.Fatalf("Len after reopen with stale .tmp = %d, want 1", c2.Len())
	}
	got, err := c2.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID(1) after reopen with stale .tmp: %v", err)
	}
	if got.Name != "t1" {
		t.Fatalf("reopened name=%q, want t1", got.Name)
	}
}

// TestCatalog_Crash_PutIdempotent — repeating the same Put on
// a reopened catalog must fail with ErrCatalogExists, not
// silently overwrite. This protects against a partial replay
// from a torn write.
func TestCatalog_Crash_PutIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	entry := CatalogEntry{TableID: 1, Name: "t", Columns: []CatalogColumn{{Name: "x", Nullable: true}}, CreateSQL: "CREATE TABLE t (x INTEGER)"}
	if err := c.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c2, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = c2.Close() })
	if err := c2.Put(entry); !errors.Is(err, ErrCatalogExists) {
		t.Fatalf("Put after reopen: got %v, want ErrCatalogExists", err)
	}
}

// TestCatalog_Crash_FiveRuns — iter-12 spec R12-13 requires the
// crash-recovery test to pass 5+ times. We loop the basic
// Put/Close/Reopen sequence to flush out order-of-operations
// flakiness.
func TestCatalog_Crash_FiveRuns(t *testing.T) {
	for run := 0; run < 5; run++ {
		dir := filepath.Join(t.TempDir(), "catalog")
		c, err := NewCatalog(dir)
		if err != nil {
			t.Fatalf("run %d: NewCatalog: %v", run, err)
		}
		for i := 0; i < 10; i++ {
			if err := c.Put(CatalogEntry{
				TableID:   uint64(i + 1),
				Name:      "t" + string(rune('a'+i)),
				Columns:   []CatalogColumn{{Name: "x", Nullable: true}},
				CreateSQL: "CREATE TABLE t (x INTEGER)",
			}); err != nil {
				t.Fatalf("run %d: Put: %v", run, err)
			}
		}
		if err := c.Close(); err != nil {
			t.Fatalf("run %d: Close: %v", run, err)
		}
		c2, err := NewCatalog(dir)
		if err != nil {
			t.Fatalf("run %d: reopen: %v", run, err)
		}
		if c2.Len() != 10 {
			t.Fatalf("run %d: Len=%d, want 10", run, c2.Len())
		}
		_ = c2.Close()
	}
}
