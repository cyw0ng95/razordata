package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestCreateTable_SchemaRegistration(t *testing.T) {
	// Clean up after test
	defer func() {
		DT.TablesMu.Lock()
		defer DT.TablesMu.Unlock()
		for name := range DT.Tables {
			delete(DT.Tables, name)
		}
		for name := range DT.Schemas {
			// REQ000982: clean up DT.Schemas map
			delete(DT.Schemas, name)
		}
		DT.StoreMu.Lock()
		defer DT.StoreMu.Unlock()
		for id := range DT.StoreSchemas {
			delete(DT.StoreSchemas, id)
		}
		for name := range DT.TableIDs {
			delete(DT.TableIDs, name)
		}
		for name := range DT.RegisteredIndexes {
			delete(DT.RegisteredIndexes, name)
		}
	}()

	ctx := context.Background()

	// Test 1: Basic table creation
	stmt := &PS.CreateTable{
		Name: "test1",
		Cols: []PS.ColDef{
			{Name: "id", Type: LX.T_INT_KW, Nullable: false},
			{Name: "name", Type: LX.T_TEXT, Nullable: true},
		},
		PK: strPtr("id"),
	}
	op := NewCreateTable(stmt)
	row, err := op.Next(ctx)
	if err != DT.ErrNoRows {
		t.Fatalf("CreateTable.Next() unexpected error = %v", err)
	}
	if row.Cols != nil || len(row.Data) != 0 {
		t.Errorf("CreateTable.Next() unexpected row: %v", row)
	}

	// Verify table was registered
	DT.TablesMu.RLock()
	_, ok := DT.Tables["test1"]
	DT.TablesMu.RUnlock()
	if !ok {
		t.Error("Table 'test1' was not registered in DT.Tables map")
	}
	_, ok = DT.Schemas["test1"]
	if !ok {
		t.Error("Table 'test1' was not registered in DT.Schemas map")
	}

	// Test 2: Table with unique constraint
	stmt2 := &PS.CreateTable{
		Name: "test2",
		Cols: []PS.ColDef{
			{Name: "id", Type: LX.T_INT_KW, Nullable: false, Unique: true},
			{Name: "value", Type: LX.T_TEXT},
		},
		UniqueConstraints: []PS.UniqueKey{
			{Cols: []string{"id", "value"}},
		},
	}
	op2 := NewCreateTable(stmt2)
	row2, err := op2.Next(ctx)
	if err != DT.ErrNoRows {
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
			{Name: "id", Type: LX.T_INT_KW},
			{Name: "ref_id", Type: LX.T_INT_KW, ReferencesTable: "test1", ReferencesColumn: "id"},
		},
		ForeignKeys: []PS.ForeignKeyConstraint{
			{Columns: []string{"ref_id"}, RefTable: "test1", RefColumns: []string{"id"}},
		},
	}
	op3 := NewCreateTable(stmt3)
	_, err = op3.Next(ctx)
	if err != DT.ErrNoRows {
		t.Fatalf("CreateTable.Next() error for test3 = %v", err)
	}

	// Test 4: Table with CHECK constraint
	stmt4 := &PS.CreateTable{
		Name: "test4",
		Cols: []PS.ColDef{
			{Name: "id", Type: LX.T_INT_KW},
			{Name: "age", Type: LX.T_INT_KW, Check: &PS.BinaryExpr{
				Left:  &PS.Ident{Name: "age"},
				Op:    LX.T_GT,
				Right: &PS.NumberLiteral{Val: 0},
			}},
		},
	}
	op4 := NewCreateTable(stmt4)
	_, err = op4.Next(ctx)
	if err != DT.ErrNoRows {
		t.Fatalf("CreateTable.Next() error for test4 = %v", err)
	}

	// Test 5: Duplicate table name should fail
	stmt5 := &PS.CreateTable{
		Name: "test1",
		Cols: []PS.ColDef{
			{Name: "id", Type: LX.T_INT_KW},
		},
	}
	op5 := NewCreateTable(stmt5)
	_, err = op5.Next(ctx)
	if err != DT.ErrTableExists {
		t.Errorf("Expected DT.ErrTableExists for duplicate table, got: %v", err)
	}

	// Test 6: WITHOUT ROWID should fail
	stmt6 := &PS.CreateTable{
		Name: "test6",
		Cols: []PS.ColDef{
			{Name: "id", Type: LX.T_INT_KW},
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
