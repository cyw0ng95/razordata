package tb

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	sc "github.com/cyw0ng95/razordata/internal/ENG/SC"
)

func TestRegistry_CreateGet(t *testing.T) {
	r := NewRegistry()
	schema := &sc.TableSchema{
		Name:       "users",
		Columns:    []sc.ColumnDef{{Name: "id", Type: sc.CTBigInt}},
		PrimaryKey: []int{0},
	}
	if err := r.Create(schema); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if schema.TableID != 1 {
		t.Errorf("TableID=%d, want 1", schema.TableID)
	}
	got, err := r.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "users" {
		t.Errorf("Name=%q, want %q", got.Name, "users")
	}
}

func TestRegistry_CreateDuplicate(t *testing.T) {
	r := NewRegistry()
	schema := &sc.TableSchema{Name: "users"}
	if err := r.Create(schema); err != nil {
		t.Fatalf("Create: %v", err)
	}
	schema2 := &sc.TableSchema{Name: "users"}
	if err := r.Create(schema2); err != sc.ErrTableExists {
		t.Errorf("Create duplicate: got %v, want ErrTableExists", err)
	}
}

func TestRegistry_Drop(t *testing.T) {
	r := NewRegistry()
	schema := &sc.TableSchema{Name: "users"}
	r.Create(schema)
	if err := r.Drop(schema.TableID); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if _, err := r.Get(schema.TableID); err != sc.ErrTableNotFound {
		t.Errorf("Get after Drop: got %v, want ErrTableNotFound", err)
	}
}

func TestRegistry_DropMissing(t *testing.T) {
	r := NewRegistry()
	if err := r.Drop(999); err != sc.ErrTableNotFound {
		t.Errorf("Drop missing: got %v, want ErrTableNotFound", err)
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()
	r.Create(&sc.TableSchema{Name: "a"})
	r.Create(&sc.TableSchema{Name: "b"})
	r.Create(&sc.TableSchema{Name: "c"})
	if r.Len() != 3 {
		t.Errorf("Len=%d, want 3", r.Len())
	}
	list := r.List()
	if len(list) != 3 {
		t.Errorf("List len=%d, want 3", len(list))
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get(999); err != sc.ErrTableNotFound {
		t.Errorf("Get missing: got %v, want ErrTableNotFound", err)
	}
}

func TestRegistry_NextID(t *testing.T) {
	r := NewRegistry()
	if r.NextID() != 1 {
		t.Errorf("NextID=%d, want 1", r.NextID())
	}
	r.Create(&sc.TableSchema{Name: "a"})
	if r.NextID() != 2 {
		t.Errorf("NextID after Create=%d, want 2", r.NextID())
	}
	r.Create(&sc.TableSchema{Name: "b"})
	if r.NextID() != 3 {
		t.Errorf("NextID after 2 Creates=%d, want 3", r.NextID())
	}
}

func TestRegistry_SetNextID(t *testing.T) {
	r := NewRegistry()
	r.SetNextID(100)
	if r.NextID() != 100 {
		t.Errorf("NextID after SetNextID(100)=%d, want 100", r.NextID())
	}
	r.Create(&sc.TableSchema{Name: "a"})
	if r.NextID() != 101 {
		t.Errorf("NextID after Create=%d, want 101", r.NextID())
	}
}

func TestRegistry_Concurrent(t *testing.T) {
	r := NewRegistry()
	// Run concurrent creates.
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			schema := &sc.TableSchema{Name: string(rune('a' + n))}
			r.Create(schema)
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	if r.Len() != 10 {
		t.Errorf("Len=%d, want 10", r.Len())
	}
}

func TestCatalog_PutGet(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	defer c.Close()
	id, err := c.NextID()
	if err != nil {
		t.Fatalf("NextID: %v", err)
	}
	entry := Entry{
		TableID:   id,
		Name:      "users",
		Columns:   []Column{{Name: "id", Type: sc.CTBigInt, Nullable: false}},
		CreateSQL: "CREATE TABLE users (id BIGINT)",
	}
	if err := c.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := c.GetByID(id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "users" {
		t.Errorf("Name=%q, want %q", got.Name, "users")
	}
	got2, err := c.ByName("users")
	if err != nil {
		t.Fatalf("ByName: %v", err)
	}
	if got2.TableID != id {
		t.Errorf("TableID=%d, want %d", got2.TableID, id)
	}
}

func TestCatalog_Persistence(t *testing.T) {
	dir := t.TempDir()
	// First session: create a table.
	c1, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog 1: %v", err)
	}
	id, _ := c1.NextID()
	c1.Put(Entry{
		TableID:   id,
		Name:      "users",
		Columns:   []Column{{Name: "id", Type: sc.CTBigInt}},
		CreateSQL: "CREATE TABLE users (id BIGINT)",
	})
	c1.Close()

	// Second session: verify the table is still there.
	c2, err := NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog 2: %v", err)
	}
	defer c2.Close()
	if c2.Len() != 1 {
		t.Errorf("Len=%d, want 1", c2.Len())
	}
	got, err := c2.GetByID(id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "users" {
		t.Errorf("Name=%q, want %q", got.Name, "users")
	}
}

func TestCatalog_Delete(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCatalog(dir)
	defer c.Close()
	id, _ := c.NextID()
	c.Put(Entry{TableID: id, Name: "users", CreateSQL: "CREATE TABLE users (id BIGINT)"})
	if err := c.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := c.GetByID(id); err == nil {
		t.Error("GetByID after Delete should fail")
	}
}

func TestCatalog_PutDuplicate(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCatalog(dir)
	defer c.Close()
	id, _ := c.NextID()
	c.Put(Entry{TableID: id, Name: "users", CreateSQL: "CREATE TABLE users (id BIGINT)"})
	err := c.Put(Entry{TableID: id + 1, Name: "users", CreateSQL: "CREATE TABLE users (id BIGINT)"})
	if err == nil {
		t.Error("Put duplicate name should fail")
	}
}

func TestCatalog_List(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCatalog(dir)
	defer c.Close()
	for i := 0; i < 3; i++ {
		id, _ := c.NextID()
		c.Put(Entry{
			TableID:   id,
			Name:      string(rune('a' + i)),
			CreateSQL: "CREATE TABLE " + string(rune('a'+i)),
		})
	}
	list := c.List()
	if len(list) != 3 {
		t.Errorf("List len=%d, want 3", len(list))
	}
	// Verify sorted by TableID.
	for i := 1; i < len(list); i++ {
		if list[i].TableID < list[i-1].TableID {
			t.Errorf("List not sorted by TableID")
		}
	}
}

func TestCatalog_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCatalog(dir)
	defer c.Close()
	id, _ := c.NextID()
	c.Put(Entry{TableID: id, Name: "users", CreateSQL: "CREATE TABLE users (id BIGINT)"})
	// Verify no .tmp file left behind.
	if _, err := os.Stat(filepath.Join(dir, "catalog.dat.tmp")); err == nil {
		t.Error("tmp file should not exist after Put")
	}
}

func TestCatalog_NextIDPersistence(t *testing.T) {
	dir := t.TempDir()
	c1, _ := NewCatalog(dir)
	id1, _ := c1.NextID()
	c1.Put(Entry{TableID: id1, Name: "a", CreateSQL: "CREATE TABLE a (x BIGINT)"})
	id2, _ := c1.NextID()
	c1.Close()

	c2, _ := NewCatalog(dir)
	defer c2.Close()
	id3, _ := c2.NextID()
	if id3 != id2+1 {
		t.Errorf("NextID after restart=%d, want %d", id3, id2+1)
	}
}

func TestCatalog_EntryConversion(t *testing.T) {
	schema := &sc.TableSchema{
		TableID:    42,
		Name:       "users",
		Columns:    []sc.ColumnDef{{Name: "id", Type: sc.CTBigInt, Nullable: false}},
		PrimaryKey: []int{0},
	}
	entry := entryFromSC(schema, "CREATE TABLE users (id BIGINT)")
	if entry.TableID != 42 {
		t.Errorf("TableID=%d, want 42", entry.TableID)
	}
	if entry.Name != "users" {
		t.Errorf("Name=%q, want %q", entry.Name, "users")
	}
	if len(entry.Columns) != 1 {
		t.Fatalf("Columns len=%d, want 1", len(entry.Columns))
	}
	if entry.Columns[0].Name != "id" {
		t.Errorf("Column[0].Name=%q, want %q", entry.Columns[0].Name, "id")
	}

	// Round-trip back to SC.
	got := entry.ToSC()
	if got.TableID != 42 {
		t.Errorf("TableID=%d, want 42", got.TableID)
	}
	if got.Name != "users" {
		t.Errorf("Name=%q, want %q", got.Name, "users")
	}
	if len(got.Columns) != 1 {
		t.Fatalf("Columns len=%d, want 1", len(got.Columns))
	}
	if !bytes.Equal([]byte("id"), []byte(got.Columns[0].Name)) {
		t.Errorf("Column name mismatch")
	}
}

func TestCatalog_CorruptMagic(t *testing.T) {
	dir := t.TempDir()
	// Write a file with bad magic.
	if err := os.WriteFile(filepath.Join(dir, "catalog.dat"), []byte("XXXX\x01\x00\x00\x00\x00\x00\x00\x00\x00\x01\x00"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewCatalog(dir)
	if err == nil {
		t.Error("NewCatalog should fail on bad magic")
	}
}

func TestCatalog_UpgradeRequired(t *testing.T) {
	dir := t.TempDir()
	// Write a file with a future version.
	data := []byte{'R', 'C', 'A', 'T', 0xFF, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if err := os.WriteFile(filepath.Join(dir, "catalog.dat"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewCatalog(dir)
	if err == nil {
		t.Error("NewCatalog should fail on future version")
	}
}
