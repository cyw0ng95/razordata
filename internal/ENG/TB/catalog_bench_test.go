package tb

import (
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

func benchColumns() []sc.ColumnDef {
	return []sc.ColumnDef{
		{Name: "id", Type: sc.CTInt, Nullable: false, PrimaryKey: true},
		{Name: "name", Type: sc.CTVarchar, Nullable: false},
		{Name: "email", Type: sc.CTVarchar, Nullable: true},
		{Name: "age", Type: sc.CTInt, Nullable: true},
		{Name: "active", Type: sc.CTBool, Nullable: false},
	}
}

func BenchmarkCatalog_Create(b *testing.B) {
	store := NewMapStore()
	c := New(store)
	_ = c.Load()
	cols := benchColumns()
	pk := []int{0}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := fmt.Sprintf("t_%d", i)
		if _, err := c.CreateTable(name, cols, pk); err != nil {
			b.Fatalf("CreateTable: %v", err)
		}
	}
}

func BenchmarkCatalog_Lookup(b *testing.B) {
	const catalogSize = 1000

	store := NewMapStore()
	c := New(store)
	_ = c.Load()
	cols := benchColumns()
	pk := []int{0}

	names := make([]string, catalogSize)
	for i := 0; i < catalogSize; i++ {
		names[i] = fmt.Sprintf("t_%d", i)
		if _, err := c.CreateTable(names[i], cols, pk); err != nil {
			b.Fatalf("seed CreateTable: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := names[i%catalogSize]
		if _, err := c.GetTableByName(name); err != nil {
			b.Fatalf("GetTableByName(%s): %v", name, err)
		}
	}
}

func BenchmarkCatalog_GetByID(b *testing.B) {
	const catalogSize = 1000

	store := NewMapStore()
	c := New(store)
	_ = c.Load()
	cols := benchColumns()
	pk := []int{0}

	ids := make([]uint64, catalogSize)
	for i := 0; i < catalogSize; i++ {
		schema, err := c.CreateTable(fmt.Sprintf("t_%d", i), cols, pk)
		if err != nil {
			b.Fatalf("seed CreateTable: %v", err)
		}
		ids[i] = schema.TableID
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ids[i%catalogSize]
		if _, err := c.GetTable(id); err != nil {
			b.Fatalf("GetTable(%d): %v", id, err)
		}
	}
}

func BenchmarkCatalog_List(b *testing.B) {
	const catalogSize = 1000

	store := NewMapStore()
	c := New(store)
	_ = c.Load()
	cols := benchColumns()
	pk := []int{0}

	for i := 0; i < catalogSize; i++ {
		if _, err := c.CreateTable(fmt.Sprintf("t_%d", i), cols, pk); err != nil {
			b.Fatalf("seed CreateTable: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		list := c.ListTables()
		if len(list) != catalogSize {
			b.Fatalf("ListTables: got %d, want %d", len(list), catalogSize)
		}
	}
}
