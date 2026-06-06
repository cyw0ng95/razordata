package ls

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCatalog_Bootstrap_FromEmpty — opening a non-existent
// directory creates a clean catalog. Mirrors the cold-start path
// of a fresh user database.
func TestCatalog_Bootstrap_FromEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if c.Len() != 0 {
		t.Fatalf("Len=%d, want 0", c.Len())
	}
	if c.nextID != 1 {
		t.Fatalf("nextID=%d, want 1", c.nextID)
	}
}

// TestCatalog_Bootstrap_NextIDCounterSurvives — the nextID
// counter must round-trip through Close + NewCatalog. Without
// this, the second process would reuse IDs that the first
// process already issued.
func TestCatalog_Bootstrap_NextIDCounterSurvives(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	// Reserve several IDs without putting any tables.
	for i := 0; i < 5; i++ {
		if _, err := c.NextID(); err != nil {
			t.Fatalf("NextID: %v", err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c2, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = c2.Close() })
	if c2.nextID < 6 {
		t.Fatalf("nextID after reopen = %d, want >= 6", c2.nextID)
	}
	id, err := c2.NextID()
	if err != nil {
		t.Fatalf("NextID after reopen: %v", err)
	}
	if id < 6 {
		t.Fatalf("NextID returned reused id: %d, want >= 6", id)
	}
}

// TestCatalog_Bootstrap_RejectsBadMagic — a file with the wrong
// magic must surface ErrCatalogCorrupt, not silently parse.
func TestCatalog_Bootstrap_RejectsBadMagic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a file whose first 4 bytes are not "RCAT".
	bad := append([]byte("XXXX"), make([]byte, catalogHeaderSize-4)...)
	if err := os.WriteFile(filepath.Join(dir, catalogFileName), bad, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c, err := NewCatalog(dir)
	if err == nil {
		_ = c.Close()
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrCatalogCorrupt) {
		t.Fatalf("bad magic: got %v, want ErrCatalogCorrupt", err)
	}
}

// TestCatalog_Bootstrap_RejectsTruncatedFile — a file smaller
// than the fixed header must surface ErrCatalogCorrupt.
func TestCatalog_Bootstrap_RejectsTruncatedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A 5-byte file is clearly truncated (header needs 17).
	trunc := []byte("RCAT\x00")
	if err := os.WriteFile(filepath.Join(dir, catalogFileName), trunc, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c, err := NewCatalog(dir)
	if err == nil {
		_ = c.Close()
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrCatalogCorrupt) {
		t.Fatalf("truncated: got %v, want ErrCatalogCorrupt", err)
	}
}

// TestCatalog_Bootstrap_RejectsFutureVersion — a file whose
// version byte is greater than schemaVersionCurrent must
// surface ErrUpgradeRequired.
func TestCatalog_Bootstrap_RejectsFutureVersion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Build a header with version=99.
	future := make([]byte, catalogHeaderSize+1)
	copy(future, "RCAT")
	future[4] = 99
	if err := os.WriteFile(filepath.Join(dir, catalogFileName), future, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c, err := NewCatalog(dir)
	if err == nil {
		_ = c.Close()
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUpgradeRequired) {
		t.Fatalf("future version: got %v, want ErrUpgradeRequired", err)
	}
}

// TestCatalog_Bootstrap_RejectsTruncatedEntry — a file whose
// header parses cleanly but whose entry body is truncated must
// surface ErrCatalogCorrupt, not panic or silently ignore.
func TestCatalog_Bootstrap_RejectsTruncatedEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Build a header with count=1 but no entry body. The header
	// declares an entry exists but the file ends mid-header.
	var buf []byte
	buf = append(buf, "RCAT"...)
	buf = append(buf, schemaVersionCurrent)
	buf = append(buf, 0, 0, 0, 0) // reserved
	var nxt [8]byte
	binary.BigEndian.PutUint64(nxt[:], 1)
	buf = append(buf, nxt[:]...)
	buf = binary.AppendUvarint(buf, 1) // count=1
	// No entry body. Bootstrap will fail at "entry 0 truncated
	// at tableID" because the file is too short for tableID.
	if err := os.WriteFile(filepath.Join(dir, catalogFileName), buf, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c, err := NewCatalog(dir)
	if err == nil {
		_ = c.Close()
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrCatalogCorrupt) {
		t.Fatalf("truncated entry: got %v, want ErrCatalogCorrupt", err)
	}
}

// TestCatalog_Bootstrap_PreservesOrder — List() must return
// entries in ascending tableID order, regardless of insertion
// order. Catches non-determinism in the cache iteration path.
func TestCatalog_Bootstrap_PreservesOrder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	// Insert out of order.
	for _, id := range []uint64{5, 1, 3, 2, 4} {
		name := []string{"a", "b", "c", "d", "e"}[id-1]
		if err := c.Put(CatalogEntry{
			TableID: id,
			Name:    name,
			Columns: []CatalogColumn{
				{Name: "x", Nullable: true},
			},
			CreateSQL: "CREATE TABLE " + name + " (x INTEGER)",
		}); err != nil {
			t.Fatalf("Put id=%d: %v", id, err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c2, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = c2.Close() })
	entries := c2.List()
	if len(entries) != 5 {
		t.Fatalf("List returned %d, want 5", len(entries))
	}
	for i, e := range entries {
		want := uint64(i + 1)
		if e.TableID != want {
			t.Fatalf("entry %d: id=%d, want %d", i, e.TableID, want)
		}
	}
}

// TestCatalog_Bootstrap_LargeEntry — boundary: 8 KB name + 64 KB
// CREATE SQL must round-trip across a restart. Catches integer
// overflow / varint decoder bugs that would silently corrupt
// large payloads.
func TestCatalog_Bootstrap_LargeEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	bigName := strings.Repeat("Z", 8*1024)
	bigSQL := "CREATE TABLE " + strings.Repeat("Z", 8*1024) +
		" (id INTEGER, payload TEXT, PRIMARY KEY (id))"
	if err := c.Put(CatalogEntry{
		TableID:    1,
		Name:       bigName,
		Columns:    []CatalogColumn{{Name: "id", Nullable: false}, {Name: "payload", Nullable: true}},
		PrimaryKey: "id",
		CreateSQL:  bigSQL,
	}); err != nil {
		t.Fatalf("Put large: %v", err)
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
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != bigName || got.CreateSQL != bigSQL {
		t.Fatalf("large entry round trip failed: name-len=%d sql-len=%d",
			len(got.Name), len(got.CreateSQL))
	}
}

// TestCatalog_Bootstrap_NoTmpLeftover — the .tmp file from a
// previous interrupted flush must not affect bootstrap. Atomic
// rename guarantees only catalog.dat is read.
func TestCatalog_Bootstrap_NoTmpLeftover(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Plant a stale .tmp file that looks nothing like a catalog.
	stale := []byte("garbage that the parser must not touch")
	if err := os.WriteFile(filepath.Join(dir, catalogFileName+catalogTmpSuffix), stale, 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog with stale .tmp: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if c.Len() != 0 {
		t.Fatalf("Len=%d, want 0 (stale .tmp must not be read)", c.Len())
	}
}

// --- helpers ---
// (helper removed; using encoding/binary.BigEndian directly)
