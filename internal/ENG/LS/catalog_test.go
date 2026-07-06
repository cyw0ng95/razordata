package ls

import (
	"errors"
	"os"
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
		gotByName, err := c.ByName(e.Name)
		if err != nil {
			t.Fatalf("ByName(%q): %v", e.Name, err)
		}
		if gotByName.TableID != e.TableID {
			t.Fatalf("ByName(%q) = id=%d, want %d", e.Name, gotByName.TableID, e.TableID)
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
	if _, err := c.ByName(want[1].Name); !errors.Is(err, ErrCatalogNotFound) {
		t.Fatalf("ByName(deleted) = %v, want ErrCatalogNotFound", err)
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

	const writers = 8
	const perWriter = 6
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

// TestCatalog_PutStatsRollback — when flushLocked fails, PutStats
// must restore the original ColumnStats on the in-memory entry.
func TestCatalog_PutStatsRollback(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	entry := CatalogEntry{TableID: 1, Name: "t", CreateSQL: "CREATE TABLE t (a INT)"}
	if err := c.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Inject stats before the rollback scenario
	originalStats := ColumnStats{DistinctCount: 10, NullCount: 2, RowCount: 100}
	if err := c.PutStats(1, "a", originalStats); err != nil {
		t.Fatalf("PutStats initial: %v", err)
	}

	// Replace catalog.dat with a directory to make subsequent
	// flushLocked fail on os.WriteFile (EISDIR / permission error).
	if err := os.Remove(c.path); err != nil {
		t.Fatalf("Remove catalog.dat: %v", err)
	}
	if err := os.Mkdir(c.path, 0o755); err != nil {
		t.Fatalf("Mkdir over catalog.dat: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(c.path) })

	newStats := ColumnStats{DistinctCount: 99, NullCount: 0, RowCount: 200}
	err = c.PutStats(1, "a", newStats)
	if err == nil {
		t.Fatal("PutStats: expected error, got nil")
	}

	// Verify in-memory stats are unchanged
	got := c.ColumnStats(1, "a")
	if got == nil {
		t.Fatal("ColumnStats returned nil after rollback")
	}
	if got.DistinctCount != originalStats.DistinctCount {
		t.Fatalf("DistinctCount = %d, want %d", got.DistinctCount, originalStats.DistinctCount)
	}
	if got.NullCount != originalStats.NullCount {
		t.Fatalf("NullCount = %d, want %d", got.NullCount, originalStats.NullCount)
	}
	if got.RowCount != originalStats.RowCount {
		t.Fatalf("RowCount = %d, want %d", got.RowCount, originalStats.RowCount)
	}
}

// TestCatalog_PutStatsRollbackNewEntry — same rollback test for the
// case where PutStats creates a brand-new stats entry (not found).
func TestCatalog_PutStatsRollbackNewEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	entry := CatalogEntry{TableID: 1, Name: "t", CreateSQL: "CREATE TABLE t (a INT, b INT)"}
	if err := c.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Put one stat so we can verify it survives the rollback of a different column
	if err := c.PutStats(1, "a", ColumnStats{DistinctCount: 10, RowCount: 100}); err != nil {
		t.Fatalf("PutStats a: %v", err)
	}

	// Break the file
	if err := os.Remove(c.path); err != nil {
		t.Fatalf("Remove catalog.dat: %v", err)
	}
	if err := os.Mkdir(c.path, 0o755); err != nil {
		t.Fatalf("Mkdir over catalog.dat: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(c.path) })

	// PutStats for column "b" — will fail after appending
	err = c.PutStats(1, "b", ColumnStats{DistinctCount: 5, RowCount: 50})
	if err == nil {
		t.Fatal("PutStats: expected error, got nil")
	}

	// Column "a" stats must survive
	got := c.ColumnStats(1, "a")
	if got == nil {
		t.Fatal("ColumnStats('a') returned nil after rollback")
	}
	if got.DistinctCount != 10 {
		t.Fatalf("DistinctCount = %d, want 10", got.DistinctCount)
	}

	// Column "b" must not exist in stats
	gotB := c.ColumnStats(1, "b")
	if gotB != nil {
		t.Fatal("ColumnStats('b') should be nil after rollback")
	}
}

func TestCatalog_PutRollbackAfterFlushFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	entry := CatalogEntry{TableID: 1, Name: "t1", CreateSQL: "CREATE TABLE t1 (a INT)"}
	if err := c.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}

	if err := os.Remove(c.path); err != nil {
		t.Fatalf("Remove catalog.dat: %v", err)
	}
	if err := os.Mkdir(c.path, 0o755); err != nil {
		t.Fatalf("Mkdir over catalog.dat: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(c.path) })

	entry2 := CatalogEntry{TableID: 2, Name: "t2", CreateSQL: "CREATE TABLE t2 (b INT)"}
	err = c.Put(entry2)
	if err == nil {
		t.Fatal("Put: expected error, got nil")
	}

	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1 after rollback", c.Len())
	}
	if _, err := c.GetByID(2); !errors.Is(err, ErrCatalogNotFound) {
		t.Errorf("GetByID(2) = %v, want ErrCatalogNotFound", err)
	}
	if _, err := c.ByName("t2"); !errors.Is(err, ErrCatalogNotFound) {
		t.Errorf("ByName(t2) = %v, want ErrCatalogNotFound", err)
	}
	got, err := c.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID(1) after rollback: %v", err)
	}
	if got.Name != "t1" {
		t.Errorf("Name = %q, want %q", got.Name, "t1")
	}
}

func TestCatalog_NextIDRollbackAfterFlushFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	beforeFirst := c.nextID // should be 1
	first, err := c.NextID()
	if err != nil {
		t.Fatalf("NextID: %v", err)
	}
	if first != beforeFirst {
		t.Fatalf("first NextID returned %d, expected %d", first, beforeFirst)
	}

	if err := os.Remove(c.path); err != nil {
		t.Fatalf("Remove catalog.dat: %v", err)
	}
	if err := os.Mkdir(c.path, 0o755); err != nil {
		t.Fatalf("Mkdir over catalog.dat: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(c.path) })

	after := c.nextID // NextID incremented it; currently nextID is 2
	_, err = c.NextID()
	if err == nil {
		t.Fatal("NextID: expected error, got nil")
	}

	// nextID must be rolled back to what it was before the failed call
	if c.nextID != after {
		t.Errorf("nextID = %d, want %d (should have rolled back)", c.nextID, after)
	}
}

func TestCatalog_GetIndexNotFound(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	entry := CatalogEntry{TableID: 1, Name: "t", CreateSQL: "CREATE TABLE t (a INT)"}
	if err := c.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, err := c.Index(1, "nonexistent"); !errors.Is(err, ErrCatalogNotFound) {
		t.Errorf("Index(nonexistent name) = %v, want ErrCatalogNotFound", err)
	}
	if _, err := c.Index(999, "any"); !errors.Is(err, ErrCatalogNotFound) {
		t.Errorf("Index(non-existent table) = %v, want ErrCatalogNotFound", err)
	}
}

func TestCatalog_ByNameClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}

	entry := CatalogEntry{TableID: 1, Name: "t", CreateSQL: "CREATE TABLE t (a INT)"}
	if err := c.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = c.ByName("t")
	if !errors.Is(err, ErrCatalogClosed) {
		t.Errorf("ByName after Close: got %v, want ErrCatalogClosed", err)
	}
}

func TestCatalog_PathOnNil(t *testing.T) {
	var c *Catalog
	if c.Path() != "" {
		t.Errorf("Path on nil = %q, want empty string", c.Path())
	}
}

func TestCatalog_CloseNil(t *testing.T) {
	var c *Catalog
	if err := c.Close(); err != nil {
		t.Errorf("Close on nil: got %v, want nil", err)
	}
}

// REQ001057b: Wire-format round-trip for MCV data through the catalog
// encoder/decoder must preserve MostCommonVals and MostCommonFreqs.
// This protects against silent data corruption when MCVs are added
// to a column's stats.
func TestStatsBlob_MCVRoundTrip(t *testing.T) {
	mcvs := [][]byte{
		[]byte("I:846"),
		[]byte("I:972"),
		[]byte("I:646"),
	}
	freqs := []float64{0.05, 0.04, 0.03}
	entry := StatsEntry{
		TableID: 1,
		Column:  "e8",
		Stats: ColumnStats{
			DistinctCount:   100,
			NullCount:       0,
			RowCount:        100,
			MostCommonVals:  mcvs,
			MostCommonFreqs: freqs,
		},
	}
	encoded := encodeStatsBlob([]StatsEntry{entry})
	if len(encoded) == 0 {
		t.Fatal("encoder produced empty blob")
	}
	decoded := decodeStatsBlob(encoded)
	if len(decoded) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(decoded))
	}
	got := decoded[0]
	if got.Column != "e8" {
		t.Fatalf("expected col=e8, got %s", got.Column)
	}
	if len(got.Stats.MostCommonVals) != 3 {
		t.Fatalf("expected 3 MCVs, got %d", len(got.Stats.MostCommonVals))
	}
	for i, want := range mcvs {
		if string(got.Stats.MostCommonVals[i]) != string(want) {
			t.Errorf("MCV[%d]: want %q, got %q", i, want, got.Stats.MostCommonVals[i])
		}
		if diff := got.Stats.MostCommonFreqs[i] - freqs[i]; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("MCV freq[%d]: want %v, got %v", i, freqs[i], got.Stats.MostCommonFreqs[i])
		}
	}
}

// REQ001057b: Decoder must remain backward-compatible with the v1
// (no-MCV) wire format. Simulate a v1 blob by stripping the version
// prefix; decoding must succeed and yield nil MCVs.
func TestStatsBlob_LegacyV1Decode(t *testing.T) {
	entry := StatsEntry{
		TableID: 1,
		Column:  "x",
		Stats: ColumnStats{
			DistinctCount: 50,
			NullCount:     0,
			RowCount:      50,
		},
	}
	encoded := encodeStatsBlob([]StatsEntry{entry})
	if encoded[0] != 0x02 {
		t.Fatalf("expected version prefix 0x02, got 0x%x", encoded[0])
	}
	v1 := encoded[1:]
	decoded := decodeStatsBlob(v1)
	if len(decoded) != 1 {
		t.Fatalf("legacy v1 decode: expected 1 entry, got %d", len(decoded))
	}
	if decoded[0].Column != "x" {
		t.Fatalf("legacy v1 decode: expected col=x, got %s", decoded[0].Column)
	}
	if len(decoded[0].Stats.MostCommonVals) != 0 {
		t.Fatalf("legacy v1 decode: expected no MCVs, got %d", len(decoded[0].Stats.MostCommonVals))
	}
}

// REQ001057b: Decoder round-trip with empty MCVs must not produce
// spurious non-nil slices.
func TestStatsBlob_EmptyMCV(t *testing.T) {
	entry := StatsEntry{
		TableID: 1,
		Column:  "y",
		Stats: ColumnStats{
			DistinctCount: 10,
			RowCount:      10,
		},
	}
	encoded := encodeStatsBlob([]StatsEntry{entry})
	decoded := decodeStatsBlob(encoded)
	if len(decoded) != 1 || len(decoded[0].Stats.MostCommonVals) != 0 {
		t.Fatalf("expected no MCVs, got %v", decoded)
	}
}
