package tb

import (
	"errors"
	"testing"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

func newTestCatalog(t *testing.T) (*Catalog, *MapStore) {
	t.Helper()
	store := NewMapStore()
	c := New(store)
	if err := c.Load(); err != nil {
		t.Fatalf("Load on empty store: %v", err)
	}
	return c, store
}

func sampleSchema() ([]sc.ColumnDef, []int) {
	return []sc.ColumnDef{
		{Name: "id", Type: sc.CTInt, Nullable: false, PrimaryKey: true},
		{Name: "name", Type: sc.CTVarchar, Nullable: false},
		{Name: "email", Type: sc.CTVarchar, Nullable: true},
	}, []int{0}
}

func TestCatalog_CreateTable(t *testing.T) {
	c, _ := newTestCatalog(t)
	cols, pk := sampleSchema()
	schema, err := c.CreateTable("users", cols, pk)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if schema.TableID == 0 {
		t.Error("expected non-zero TableID")
	}
	if schema.Name != "users" {
		t.Errorf("Name: got %q, want %q", schema.Name, "users")
	}
	if c.Len() != 1 {
		t.Errorf("Len: got %d, want 1", c.Len())
	}
}

func TestCatalog_DoubleCreate(t *testing.T) {
	c, _ := newTestCatalog(t)
	cols, pk := sampleSchema()
	if _, err := c.CreateTable("users", cols, pk); err != nil {
		t.Fatalf("first CreateTable: %v", err)
	}
	_, err := c.CreateTable("users", cols, pk)
	if !errors.Is(err, ErrTableExists) {
		t.Fatalf("expected ErrTableExists, got %v", err)
	}
}

func TestCatalog_GetTable(t *testing.T) {
	c, _ := newTestCatalog(t)
	cols, pk := sampleSchema()
	created, _ := c.CreateTable("users", cols, pk)
	got, err := c.GetTable(created.TableID)
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}
	if got.Name != "users" {
		t.Errorf("Name: got %q, want %q", got.Name, "users")
	}
	if got.TableID != created.TableID {
		t.Errorf("TableID: got %d, want %d", got.TableID, created.TableID)
	}
}

func TestCatalog_GetTableByName(t *testing.T) {
	c, _ := newTestCatalog(t)
	cols, pk := sampleSchema()
	created, _ := c.CreateTable("users", cols, pk)
	got, err := c.GetTableByName("users")
	if err != nil {
		t.Fatalf("GetTableByName: %v", err)
	}
	if got.TableID != created.TableID {
		t.Errorf("TableID: got %d, want %d", got.TableID, created.TableID)
	}
}

func TestCatalog_GetTable_NotFound(t *testing.T) {
	c, _ := newTestCatalog(t)
	if _, err := c.GetTable(42); !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("expected ErrTableNotFound, got %v", err)
	}
	if _, err := c.GetTableByName("missing"); !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("expected ErrTableNotFound, got %v", err)
	}
}

func TestCatalog_GetTable_ReturnsCopy(t *testing.T) {
	c, _ := newTestCatalog(t)
	cols, pk := sampleSchema()
	created, _ := c.CreateTable("users", cols, pk)
	got1, _ := c.GetTable(created.TableID)
	got1.Name = "mutated"
	got1.Columns[0].Name = "mutated_col"
	got1.PrimaryKey[0] = 99
	got2, _ := c.GetTable(created.TableID)
	if got2.Name != "users" {
		t.Errorf("Name leaked: got %q, want %q", got2.Name, "users")
	}
	if got2.Columns[0].Name != "id" {
		t.Errorf("Column leaked: got %q", got2.Columns[0].Name)
	}
	if got2.PrimaryKey[0] != 0 {
		t.Errorf("PrimaryKey leaked: got %d", got2.PrimaryKey[0])
	}
}

func TestCatalog_DropTable(t *testing.T) {
	c, _ := newTestCatalog(t)
	cols, pk := sampleSchema()
	created, _ := c.CreateTable("users", cols, pk)
	if err := c.DropTable(created.TableID); err != nil {
		t.Fatalf("DropTable: %v", err)
	}
	if _, err := c.GetTable(created.TableID); !errors.Is(err, ErrTableNotFound) {
		t.Errorf("expected ErrTableNotFound after drop, got %v", err)
	}
	if _, err := c.GetTableByName("users"); !errors.Is(err, ErrTableNotFound) {
		t.Errorf("expected ErrTableNotFound by name after drop, got %v", err)
	}
	if c.Len() != 0 {
		t.Errorf("Len after drop: got %d, want 0", c.Len())
	}
}

func TestCatalog_DropTable_NotFound(t *testing.T) {
	c, _ := newTestCatalog(t)
	if err := c.DropTable(99); !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("expected ErrTableNotFound, got %v", err)
	}
}

func TestCatalog_ListTables(t *testing.T) {
	c, _ := newTestCatalog(t)
	cols, pk := sampleSchema()
	names := []string{"a", "b", "c"}
	for _, n := range names {
		if _, err := c.CreateTable(n, cols, pk); err != nil {
			t.Fatalf("CreateTable(%s): %v", n, err)
		}
	}
	list := c.ListTables()
	if len(list) != 3 {
		t.Fatalf("expected 3 tables, got %d", len(list))
	}
	// IDs are assigned monotonically; the list is sorted by ID.
	for i := 0; i < len(list)-1; i++ {
		if list[i].TableID >= list[i+1].TableID {
			t.Errorf("not sorted: %d >= %d", list[i].TableID, list[i+1].TableID)
		}
	}
}

func TestCatalog_NameReuseAfterDrop(t *testing.T) {
	c, _ := newTestCatalog(t)
	cols, pk := sampleSchema()
	first, _ := c.CreateTable("users", cols, pk)
	if err := c.DropTable(first.TableID); err != nil {
		t.Fatalf("DropTable: %v", err)
	}
	second, err := c.CreateTable("users", cols, pk)
	if err != nil {
		t.Fatalf("CreateTable after drop: %v", err)
	}
	if second.TableID == first.TableID {
		t.Errorf("expected fresh tableID after drop, got %d (same as %d)", second.TableID, first.TableID)
	}
	if second.TableID <= first.TableID {
		t.Errorf("expected new ID > %d, got %d", first.TableID, second.TableID)
	}
}

// R16: persistence test.
func TestCatalog_ReopenPreservesState(t *testing.T) {
	store := NewMapStore()
	cols, pk := sampleSchema()

	// First session.
	c1 := New(store)
	if err := c1.Load(); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	a, _ := c1.CreateTable("a", cols, pk)
	b, _ := c1.CreateTable("b", cols, pk)
	_ = a
	_ = b
	if c1.Len() != 2 {
		t.Fatalf("first session: got %d, want 2", c1.Len())
	}

	// Simulate close + reopen on a fresh catalog pointing at the
	// same store.
	c2 := New(store)
	if err := c2.Load(); err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if c2.Len() != 2 {
		t.Fatalf("after reopen: got %d, want 2", c2.Len())
	}
	gotA, err := c2.GetTableByName("a")
	if err != nil {
		t.Fatalf("GetTableByName(a): %v", err)
	}
	if gotA.Name != "a" {
		t.Errorf("a: got %q", gotA.Name)
	}
	gotB, err := c2.GetTableByName("b")
	if err != nil {
		t.Fatalf("GetTableByName(b): %v", err)
	}
	if gotB.Name != "b" {
		t.Errorf("b: got %q", gotB.Name)
	}
}

func TestCatalog_Reopen_AfterDrop(t *testing.T) {
	store := NewMapStore()
	cols, pk := sampleSchema()

	c1 := New(store)
	_ = c1.Load()
	a, _ := c1.CreateTable("a", cols, pk)
	b, _ := c1.CreateTable("b", cols, pk)
	_ = c1.DropTable(b.TableID)

	c2 := New(store)
	_ = c2.Load()
	if c2.Len() != 1 {
		t.Fatalf("after reopen: got %d, want 1", c2.Len())
	}
	if _, err := c2.GetTableByName("a"); err != nil {
		t.Errorf("expected a to survive: %v", err)
	}
	if _, err := c2.GetTableByName("b"); !errors.Is(err, ErrTableNotFound) {
		t.Errorf("expected b to be gone, got %v", err)
	}
	_ = a
}

func TestCatalog_Reopen_IDsAreFresh(t *testing.T) {
	store := NewMapStore()
	cols, pk := sampleSchema()

	c1 := New(store)
	_ = c1.Load()
	a, _ := c1.CreateTable("a", cols, pk)
	_ = c1.DropTable(a.TableID)

	c2 := New(store)
	_ = c2.Load()
	b, err := c2.CreateTable("a", cols, pk)
	if err != nil {
		t.Fatalf("CreateTable after reopen: %v", err)
	}
	// a.TableID was 1, so b.TableID must be > 1.
	if b.TableID <= a.TableID {
		t.Errorf("expected b.TableID > %d, got %d", a.TableID, b.TableID)
	}
}

func TestCatalog_Load_Idempotent(t *testing.T) {
	c, _ := newTestCatalog(t)
	cols, pk := sampleSchema()
	_, _ = c.CreateTable("a", cols, pk)
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Len() != 1 {
		t.Errorf("after second Load: got %d, want 1", c.Len())
	}
}

func TestCatalog_Load_SkipsOrphanSchema(t *testing.T) {
	// Hand-craft a store with a __catalog__ entry but no
	// __catalog_name__ entry. Load should skip the orphan.
	store := NewMapStore()
	cols, pk := sampleSchema()
	{
		c := New(store)
		_ = c.Load()
		created, _ := c.CreateTable("a", cols, pk)
		_ = c.DropTable(created.TableID)
		// After drop, the byName entry is gone but the underlying
		// __catalog_name__ entry may still be a tombstone. The
		// orphan test simulates a different failure: schema
		// written but name index not.
		_ = c
	}

	// Manually inject an orphan __catalog__ entry.
	orphanID := uint64(999)
	schema := &sc.TableSchema{
		TableID:    orphanID,
		Name:       "orphan",
		Columns:    cols,
		PrimaryKey: pk,
	}
	enc, _ := MarshalTableSchema(schema)
	_ = store.Insert(catalogKey(orphanID), enc)

	c := New(store)
	_ = c.Load()
	if _, err := c.GetTable(orphanID); !errors.Is(err, ErrTableNotFound) {
		t.Errorf("expected orphan to be skipped, got %v", err)
	}
}

func TestCatalog_Load_SkipsOrphanName(t *testing.T) {
	// __catalog_name__ entry pointing to a missing __catalog__.
	store := NewMapStore()
	_ = store.Insert(catalogNameKeyFor("ghost"), encodeID(4242))

	c := New(store)
	_ = c.Load()
	if c.Len() != 0 {
		t.Errorf("expected empty catalog, got %d entries", c.Len())
	}
}

func TestCatalog_Flush_NoOp(t *testing.T) {
	c, _ := newTestCatalog(t)
	if err := c.Flush(); err != nil {
		t.Errorf("Flush: %v", err)
	}
}

func TestCatalog_TableIDsMonotonic(t *testing.T) {
	c, _ := newTestCatalog(t)
	cols, pk := sampleSchema()
	prev := uint64(0)
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		schema, _ := c.CreateTable(n, cols, pk)
		if schema.TableID <= prev {
			t.Errorf("non-monotonic: %d after %d", schema.TableID, prev)
		}
		prev = schema.TableID
	}
}
