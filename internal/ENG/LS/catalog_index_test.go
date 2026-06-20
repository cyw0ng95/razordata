package ls

import (
	"testing"
)

// TestCatalog_PutIndex adds a secondary index and verifies it
// round-trips through persistence.
func TestCatalog_PutIndex(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Put(CatalogEntry{
		TableID:    1,
		Name:       "users",
		Columns:    []CatalogColumn{{Name: "id"}, {Name: "email"}},
		PrimaryKey: "id",
		CreateSQL:  "CREATE TABLE users (id INT, email TEXT)",
	}); err != nil {
		t.Fatalf("Put table: %v", err)
	}

	idx := CatalogIndex{
		Name:      "idx_email",
		Columns:   []string{"email"},
		CreateSQL: "CREATE INDEX idx_email ON users (email)",
	}
	if err := c.PutIndex(1, idx); err != nil {
		t.Fatalf("PutIndex: %v", err)
	}

	got, err := c.IndexesByTable(1)
	if err != nil {
		t.Fatalf("IndexesByTable: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d indexes, want 1", len(got))
	}
	if got[0].Name != "idx_email" {
		t.Errorf("Name = %q, want idx_email", got[0].Name)
	}
	if got[0].IndexID == 0 {
		t.Errorf("IndexID should be non-zero")
	}
	if len(got[0].Columns) != 1 || got[0].Columns[0] != "email" {
		t.Errorf("Columns = %v, want [email]", got[0].Columns)
	}
}

// TestCatalog_PutIndex_Duplicate rejects a second index with the
// same name on the same table.
func TestCatalog_PutIndex_Duplicate(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCatalog(dir)
	t.Cleanup(func() { _ = c.Close() })

	_ = c.Put(CatalogEntry{
		TableID: 1, Name: "t",
		Columns:    []CatalogColumn{{Name: "id"}, {Name: "v"}},
		PrimaryKey: "id",
		CreateSQL:  "CREATE TABLE t (id INT, v TEXT)",
	})

	if err := c.PutIndex(1, CatalogIndex{Name: "idx_v", Columns: []string{"v"}}); err != nil {
		t.Fatalf("first PutIndex: %v", err)
	}
	if err := c.PutIndex(1, CatalogIndex{Name: "idx_v", Columns: []string{"v"}}); err == nil {
		t.Errorf("expected duplicate index name to fail")
	}
}

// TestCatalog_PutIndex_TableNotFound rejects index on missing table.
func TestCatalog_PutIndex_TableNotFound(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCatalog(dir)
	t.Cleanup(func() { _ = c.Close() })

	err := c.PutIndex(999, CatalogIndex{Name: "idx", Columns: []string{"x"}})
	if err == nil {
		t.Errorf("expected error for missing table")
	}
}

// TestCatalog_PutIndex_EmptyName rejects empty index name.
func TestCatalog_PutIndex_EmptyName(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCatalog(dir)
	t.Cleanup(func() { _ = c.Close() })

	_ = c.Put(CatalogEntry{
		TableID: 1, Name: "t",
		Columns:    []CatalogColumn{{Name: "id"}},
		PrimaryKey: "id",
		CreateSQL:  "CREATE TABLE t (id INT)",
	})

	err := c.PutIndex(1, CatalogIndex{Columns: []string{"id"}})
	if err == nil {
		t.Errorf("expected error for empty index name")
	}
}

// TestCatalog_DeleteIndex removes a secondary index.
func TestCatalog_DeleteIndex(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCatalog(dir)
	t.Cleanup(func() { _ = c.Close() })

	_ = c.Put(CatalogEntry{
		TableID: 1, Name: "t",
		Columns:    []CatalogColumn{{Name: "id"}, {Name: "v"}},
		PrimaryKey: "id",
		CreateSQL:  "CREATE TABLE t (id INT, v TEXT)",
	})
	_ = c.PutIndex(1, CatalogIndex{Name: "idx_v", Columns: []string{"v"}})

	if err := c.DeleteIndex(1, "idx_v"); err != nil {
		t.Fatalf("DeleteIndex: %v", err)
	}
	got, _ := c.IndexesByTable(1)
	if len(got) != 0 {
		t.Errorf("after delete, got %d indexes, want 0", len(got))
	}
}

// TestCatalog_DeleteIndex_Missing returns error for unknown index.
func TestCatalog_DeleteIndex_Missing(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCatalog(dir)
	t.Cleanup(func() { _ = c.Close() })

	_ = c.Put(CatalogEntry{
		TableID: 1, Name: "t",
		Columns:    []CatalogColumn{{Name: "id"}},
		PrimaryKey: "id",
		CreateSQL:  "CREATE TABLE t (id INT)",
	})

	err := c.DeleteIndex(1, "nonexistent")
	if err == nil {
		t.Errorf("expected error for missing index")
	}
}

// TestCatalog_Index returns a single index by name.
func TestCatalog_Index(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCatalog(dir)
	t.Cleanup(func() { _ = c.Close() })

	_ = c.Put(CatalogEntry{
		TableID: 1, Name: "t",
		Columns:    []CatalogColumn{{Name: "id"}, {Name: "v"}},
		PrimaryKey: "id",
		CreateSQL:  "CREATE TABLE t (id INT, v TEXT)",
	})
	_ = c.PutIndex(1, CatalogIndex{
		Name:    "idx_v",
		Columns: []string{"v"},
		Unique:  true,
	})

	idx, err := c.Index(1, "idx_v")
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if !idx.Unique {
		t.Errorf("Unique should be true")
	}
	if idx.Name != "idx_v" {
		t.Errorf("Name = %q, want idx_v", idx.Name)
	}
}

// TestCatalog_IndexPersistence verifies that indexes survive a
// close + reopen cycle.
func TestCatalog_IndexPersistence(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCatalog(dir)

	_ = c.Put(CatalogEntry{
		TableID: 1, Name: "t",
		Columns:    []CatalogColumn{{Name: "id"}, {Name: "v"}},
		PrimaryKey: "id",
		CreateSQL:  "CREATE TABLE t (id INT, v TEXT)",
	})
	_ = c.PutIndex(1, CatalogIndex{
		Name:      "idx_v",
		Columns:   []string{"v"},
		CreateSQL: "CREATE INDEX idx_v ON t (v)",
	})
	_ = c.Close()

	c2, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = c2.Close() })

	got, err := c2.IndexesByTable(1)
	if err != nil {
		t.Fatalf("IndexesByTable after reopen: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("after reopen, got %d indexes, want 1", len(got))
	}
	if got[0].Name != "idx_v" {
		t.Errorf("Name = %q, want idx_v", got[0].Name)
	}
	if got[0].CreateSQL != "CREATE INDEX idx_v ON t (v)" {
		t.Errorf("CreateSQL = %q, want preserved", got[0].CreateSQL)
	}
}

// TestCatalog_VersionBump verifies schemaVersion is now V2.
func TestCatalog_VersionBump(t *testing.T) {
	if schemaVersionCurrent != schemaVersionV2 {
		t.Errorf("schemaVersionCurrent = %d, want %d",
			schemaVersionCurrent, schemaVersionV2)
	}
}
