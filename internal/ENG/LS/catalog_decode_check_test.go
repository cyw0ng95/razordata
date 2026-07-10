package ls

import (
	"path/filepath"
	"testing"
)

// TestCatalog_StatsRoundtrip — PutStats + close + reopen yields ColumnStats.
func TestCatalog_StatsRoundtrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cat")
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}

	if err := c.Put(CatalogEntry{
		TableID:    1,
		Name:       "rt_t",
		PrimaryKey: "id",
		Columns:    []CatalogColumn{{Name: "id", Type: 9}, {Name: "v", Type: 9}},
		CreateSQL:  "CREATE TABLE rt_t (id INTEGER, v INTEGER, PRIMARY KEY (id))",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := c.PutStats(1, "id", ColumnStats{
		DistinctCount: 7, NullCount: 1, RowCount: 10,
		MinValue: []byte("1"), MaxValue: []byte("9"),
	}); err != nil {
		t.Fatalf("PutStats: %v", err)
	}
	c.Close()

	c2, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog reopen: %v", err)
	}
	defer c2.Close()

	entries := c2.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if len(e.ColumnStats) != 1 {
		t.Fatalf("expected 1 ColumnStats, got %d", len(e.ColumnStats))
	}
	if e.ColumnStats[0].Stats.DistinctCount != 7 {
		t.Errorf("DistinctCount = %d, want 7", e.ColumnStats[0].Stats.DistinctCount)
	}
}
