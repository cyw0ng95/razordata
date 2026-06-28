package EX

import (
	"context"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// TestPragma_ForeignKeys_Toggle verifies REQ000905: PRAGMA foreign_keys
// ON/OFF toggles FK enforcement.
func TestPragma_ForeignKeys_Toggle(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()
	defer SetForeignKeysEnabled(true) // restore default

	// Verify default is ON.
	if !IsForeignKeysEnabled() {
		t.Fatal("expected default foreign_keys=ON")
	}

	// Toggle OFF.
	SetForeignKeysEnabled(false)
	if IsForeignKeysEnabled() {
		t.Fatal("expected IsForeignKeysEnabled()=false after OFF")
	}

	// Toggle ON.
	SetForeignKeysEnabled(true)
	if !IsForeignKeysEnabled() {
		t.Fatal("expected IsForeignKeysEnabled()=true after ON")
	}
}

// TestPragma_ForeignKeys_ReadWrite verifies the PRAGMA read/write via Executor.
func TestPragma_ForeignKeys_ReadWrite(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()
	defer SetForeignKeysEnabled(true) // restore default

	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	// Default should be ON (1).
	rows, err := e.QueryAll(ctx, "PRAGMA foreign_keys")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].I64 != 1 {
		t.Fatalf("expected foreign_keys=1, got %v", rows[0].Data[0].I64)
	}

	// Turn OFF.
	_, err = e.Exec(ctx, "PRAGMA foreign_keys = OFF")
	if err != nil {
		t.Fatal(err)
	}
	if IsForeignKeysEnabled() {
		t.Fatal("expected foreign_keys=OFF after PRAGMA")
	}

	// Read back — should be 0.
	rows, err = e.QueryAll(ctx, "PRAGMA foreign_keys")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].I64 != 0 {
		t.Fatalf("expected foreign_keys=0, got %v", rows[0].Data[0].I64)
	}

	// Turn back ON.
	_, err = e.Exec(ctx, "PRAGMA foreign_keys = ON")
	if err != nil {
		t.Fatal(err)
	}
	if !IsForeignKeysEnabled() {
		t.Fatal("expected foreign_keys=ON after PRAGMA")
	}
}

// TestPragma_ForeignKeyCheck_NoViolations verifies REQ000906: no
// violations when all FK references are valid.
func TestPragma_ForeignKeyCheck_NoViolations(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()

	parent := &storeSchema{cols: []string{"id"}, pk: "id"}
	child := &storeSchema{
		cols:        []string{"id", "pid"},
		pk:          "id",
		foreignKeys: []ForeignKeyConstraint{{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT"}},
	}
	storeSchemas[1] = parent
	storeSchemas[2] = child
	tableIDs["p"] = 1
	tableIDs["c"] = 2

	// Parent has id=1,2,3. Child references valid parent ids.
	tables["p"] = []Row{
		{Cols: []string{"id"}, Data: []Value{NewIntValue(1)}},
		{Cols: []string{"id"}, Data: []Value{NewIntValue(2)}},
	}
	tables["c"] = []Row{
		{Cols: []string{"id", "pid"}, Data: []Value{NewIntValue(10), NewIntValue(1)}},
		{Cols: []string{"id", "pid"}, Data: []Value{NewIntValue(20), NewIntValue(2)}},
	}

	p := NewPragma(&PS.PragmaStmt{Name: "foreign_key_check"})
	p.loadForeignKeyCheck()
	if len(p.rows) != 0 {
		t.Fatalf("expected 0 violations, got %d", len(p.rows))
	}
}

// TestPragma_ForeignKeyCheck_Violation verifies REQ000906: violation
// row is reported when a child references a non-existent parent.
func TestPragma_ForeignKeyCheck_Violation(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()

	parent := &storeSchema{cols: []string{"id"}, pk: "id"}
	child := &storeSchema{
		cols:        []string{"id", "pid"},
		pk:          "id",
		foreignKeys: []ForeignKeyConstraint{{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT"}},
	}
	storeSchemas[1] = parent
	storeSchemas[2] = child
	tableIDs["p"] = 1
	tableIDs["c"] = 2

	tables["p"] = []Row{
		{Cols: []string{"id"}, Data: []Value{NewIntValue(1)}},
	}
	// Child has pid=99 which does NOT exist in parent.
	tables["c"] = []Row{
		{Cols: []string{"id", "pid"}, Data: []Value{NewIntValue(10), NewIntValue(99)}},
	}

	p := NewPragma(&PS.PragmaStmt{Name: "foreign_key_check"})
	p.loadForeignKeyCheck()
	if len(p.rows) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(p.rows))
	}
	row := p.rows[0]
	if row.Cols[0] != "table" || row.Data[0].ToAny() != "c" {
		t.Fatalf("expected table='c', got %v", row.Data[0].ToAny())
	}
}

// TestPragma_ForeignKeyCheck_SpecificTable verifies the optional
// table_name parameter: only that table is checked.
func TestPragma_ForeignKeyCheck_SpecificTable(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()

	parent := &storeSchema{cols: []string{"id"}, pk: "id"}
	child := &storeSchema{
		cols:        []string{"id", "pid"},
		pk:          "id",
		foreignKeys: []ForeignKeyConstraint{{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT"}},
	}
	storeSchemas[1] = parent
	storeSchemas[2] = child
	tableIDs["p"] = 1
	tableIDs["c"] = 2

	tables["p"] = []Row{
		{Cols: []string{"id"}, Data: []Value{NewIntValue(1)}},
	}
	tables["c"] = []Row{
		{Cols: []string{"id", "pid"}, Data: []Value{NewIntValue(10), NewIntValue(99)}},
	}

	// Check only table "p" — no FK constraints on it, so 0 violations.
	p := NewPragma(&PS.PragmaStmt{Name: "foreign_key_check", Value: "p"})
	p.loadForeignKeyCheck()
	if len(p.rows) != 0 {
		t.Fatalf("expected 0 violations for table 'p', got %d", len(p.rows))
	}

	// Check table "c" — has violation.
	p2 := NewPragma(&PS.PragmaStmt{Name: "foreign_key_check", Value: "c"})
	p2.loadForeignKeyCheck()
	if len(p2.rows) != 1 {
		t.Fatalf("expected 1 violation for table 'c', got %d", len(p2.rows))
	}
}

// TestPragma_ForeignKeyCheck_NullFKColumns verifies that rows with
// all-NULL FK columns are not reported as violations.
func TestPragma_ForeignKeyCheck_NullFKColumns(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()

	parent := &storeSchema{cols: []string{"id"}, pk: "id"}
	child := &storeSchema{
		cols:        []string{"id", "pid"},
		pk:          "id",
		foreignKeys: []ForeignKeyConstraint{{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT"}},
	}
	storeSchemas[1] = parent
	storeSchemas[2] = child
	tableIDs["p"] = 1
	tableIDs["c"] = 2

	tables["p"] = []Row{
		{Cols: []string{"id"}, Data: []Value{NewIntValue(1)}},
	}
	// Child has NULL pid — should not be a violation (SQL standard).
	tables["c"] = []Row{
		{Cols: []string{"id", "pid"}, Data: []Value{NewIntValue(10), NullValue()}},
	}

	p := NewPragma(&PS.PragmaStmt{Name: "foreign_key_check"})
	p.loadForeignKeyCheck()
	if len(p.rows) != 0 {
		t.Fatalf("expected 0 violations for NULL FK, got %d", len(p.rows))
	}
}
