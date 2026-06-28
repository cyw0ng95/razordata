package EX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestCreateTable_SchemaRegistration(t *testing.T) {
	// Clean up after test
	defer func() {
		tablesMu.Lock()
		defer tablesMu.Unlock()
		for name := range tables {
			delete(tables, name)
		}
		for name := range schemas {
			// REQ000982: clean up schemas map
			delete(schemas, name)
		}
		storeMu.Lock()
		defer storeMu.Unlock()
		for id := range storeSchemas {
			delete(storeSchemas, id)
		}
		for name := range tableIDs {
			delete(tableIDs, name)
		}
		for name := range registeredIndexes {
			delete(registeredIndexes, name)
		}
	}()

	ctx := context.Background()

	// Test 1: Basic table creation
	stmt := &PS.CreateTable{
		Name: "test1",
		Cols: []PS.ColDef{
			{Name: "id", Type: int(LX.T_INT_KW), Nullable: false},
			{Name: "name", Type: int(LX.T_TEXT), Nullable: true},
		},
		PK: strPtr("id"),
	}
	op := NewCreateTable(stmt)
	row, err := op.Next(ctx)
	if err != ErrNoRows {
		t.Fatalf("CreateTable.Next() unexpected error = %v", err)
	}
	if row.Cols != nil || len(row.Data) != 0 {
		t.Errorf("CreateTable.Next() unexpected row: %v", row)
	}

	// Verify table was registered
	tablesMu.RLock()
	_, ok := tables["test1"]
	tablesMu.RUnlock()
	if !ok {
		t.Error("Table 'test1' was not registered in tables map")
	}
	_, ok = schemas["test1"]
	if !ok {
		t.Error("Table 'test1' was not registered in schemas map")
	}

	// Test 2: Table with unique constraint
	stmt2 := &PS.CreateTable{
		Name: "test2",
		Cols: []PS.ColDef{
			{Name: "id", Type: int(LX.T_INT_KW), Nullable: false, Unique: true},
			{Name: "value", Type: int(LX.T_TEXT)},
		},
		UniqueConstraints: []PS.UniqueKey{
			{Cols: []string{"id", "value"}},
		},
	}
	op2 := NewCreateTable(stmt2)
	row2, err := op2.Next(ctx)
	if err != ErrNoRows {
		t.Fatalf("CreateTable.Next() error for test2 = %v", err)
	}
	if row2.Cols != nil || len(row2.Data) != 0 {
		t.Errorf("CreateTable.Next() unexpected row for test2: %v", row2)
	}
	if row2.Cols != nil || len(row2.Data) != 0 {
		t.Errorf("CreateTable.Next() unexpected row for test2: %v", row2)
	}

	// Test 3: Table with foreign key
	stmt3 := &PS.CreateTable{
		Name: "test3",
		Cols: []PS.ColDef{
			{Name: "id", Type: int(LX.T_INT_KW)},
			{Name: "ref_id", Type: int(LX.T_INT_KW), ReferencesTable: "test1", ReferencesColumn: "id"},
		},
		ForeignKeys: []PS.ForeignKeyConstraint{
			{Columns: []string{"ref_id"}, RefTable: "test1", RefColumns: []string{"id"}},
		},
	}
	op3 := NewCreateTable(stmt3)
	_, err = op3.Next(ctx)
	if err != ErrNoRows {
		t.Fatalf("CreateTable.Next() error for test3 = %v", err)
	}

	// Test 4: Table with CHECK constraint
	stmt4 := &PS.CreateTable{
		Name: "test4",
		Cols: []PS.ColDef{
			{Name: "id", Type: int(LX.T_INT_KW)},
			{Name: "age", Type: int(LX.T_INT_KW), Check: &PS.BinaryExpr{
				Left:  &PS.Ident{Name: "age"},
				Op:    int(LX.T_GT),
				Right: &PS.NumberLiteral{Val: 0},
			}},
		},
	}
	op4 := NewCreateTable(stmt4)
	_, err = op4.Next(ctx)
	if err != ErrNoRows {
		t.Fatalf("CreateTable.Next() error for test4 = %v", err)
	}

	// Test 5: Duplicate table name should fail
	stmt5 := &PS.CreateTable{
		Name: "test1",
		Cols: []PS.ColDef{
			{Name: "id", Type: int(LX.T_INT_KW)},
		},
	}
	op5 := NewCreateTable(stmt5)
	_, err = op5.Next(ctx)
	if err != errTableExists {
		t.Errorf("Expected errTableExists for duplicate table, got: %v", err)
	}

	// Test 6: WITHOUT ROWID should fail
	stmt6 := &PS.CreateTable{
		Name: "test6",
		Cols: []PS.ColDef{
			{Name: "id", Type: int(LX.T_INT_KW)},
		},
		WithoutRowid: true,
	}
	op6 := NewCreateTable(stmt6)
	_, err = op6.Next(ctx)
	if err == nil {
		t.Error("Expected error for WITHOUT ROWID, got nil")
	}
}

func strPtr(s string) *string {
	return &s
}
