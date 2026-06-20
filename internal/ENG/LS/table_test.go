package ls

import (
	"testing"
)

func TestTableRegistryCreate(t *testing.T) {
	tr := newTableRegistry()

	schema := &TableSchema{
		Name: "users",
		Columns: []ColumnDef{
			{Name: "id", Type: CTInt},
			{Name: "name", Type: CTVarchar},
		},
		PrimaryKey: []int{0},
	}

	if err := tr.Create(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	if schema.TableID == 0 {
		t.Fatal("expected TableID to be set")
	}

	retrieved, err := tr.Get(schema.TableID)
	if err != nil {
		t.Fatalf("failed to get table: %v", err)
	}

	if retrieved.Name != "users" {
		t.Fatalf("expected users, got %s", retrieved.Name)
	}
}

func TestTableRegistryDuplicate(t *testing.T) {
	tr := newTableRegistry()

	schema1 := &TableSchema{Name: "users"}
	schema2 := &TableSchema{Name: "users"}

	tr.Create(schema1)

	if err := tr.Create(schema2); err != ErrTableExists {
		t.Fatalf("expected ErrTableExists, got %v", err)
	}
}

func TestTableRegistryDrop(t *testing.T) {
	tr := newTableRegistry()

	schema := &TableSchema{Name: "users"}
	tr.Create(schema)

	if err := tr.Drop(schema.TableID); err != nil {
		t.Fatalf("failed to drop table: %v", err)
	}

	_, err := tr.Get(schema.TableID)
	if err != ErrTableNotFound {
		t.Fatalf("expected ErrTableNotFound, got %v", err)
	}
}

func TestTableRegistryList(t *testing.T) {
	tr := newTableRegistry()

	tr.Create(&TableSchema{Name: "users"})
	tr.Create(&TableSchema{Name: "orders"})
	tr.Create(&TableSchema{Name: "products"})

	tables := tr.List()
	if len(tables) != 3 {
		t.Fatalf("expected 3 tables, got %d", len(tables))
	}
}

func TestCatalogCreateTable(t *testing.T) {
	c := newCatalog()

	schema, err := c.CreateTable("users", []ColumnDef{
		{Name: "id", Type: CTInt, PrimaryKey: true},
		{Name: "name", Type: CTVarchar},
	}, []int{0})

	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	if schema.TableID == 0 {
		t.Fatal("expected TableID to be set")
	}

	retrieved, err := c.Table(schema.TableID)
	if err != nil {
		t.Fatalf("failed to get table: %v", err)
	}

	if retrieved.Name != "users" {
		t.Fatalf("expected users, got %s", retrieved.Name)
	}
}

func TestCatalogDropTable(t *testing.T) {
	c := newCatalog()

	schema, _ := c.CreateTable("users", []ColumnDef{
		{Name: "id", Type: CTInt},
	}, []int{0})

	tableID := schema.TableID

	if err := c.DropTable(tableID); err != nil {
		t.Fatalf("failed to drop table: %v", err)
	}

	_, err := c.Table(tableID)
	if err != ErrTableNotFound {
		t.Fatalf("expected ErrTableNotFound, got %v", err)
	}
}

func TestCatalogTableByName(t *testing.T) {
	c := newCatalog()

	schema, _ := c.CreateTable("users", []ColumnDef{
		{Name: "id", Type: CTInt},
	}, []int{0})

	tableID, found := c.TableByName("users")
	if !found {
		t.Fatal("expected to find table by name")
	}

	if decodeTableID(tableID) != schema.TableID {
		t.Fatalf("expected tableID %d, got %d", schema.TableID, decodeTableID(tableID))
	}
}
