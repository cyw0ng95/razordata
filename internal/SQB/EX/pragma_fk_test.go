package EX

import (
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// TestPragma_ForeignKeys_Toggle verifies REQ000905: PRAGMA foreign_keys
// ON/OFF toggles FK enforcement.
func TestPragma_ForeignKeys_Toggle(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()
	defer DT.SetForeignKeysEnabled(true) // restore default

	// Verify default is ON.
	if !DT.IsForeignKeysEnabled() {
		t.Fatal("expected default foreign_keys=ON")
	}

	// Toggle OFF.
	DT.SetForeignKeysEnabled(false)
	if DT.IsForeignKeysEnabled() {
		t.Fatal("expected DT.IsForeignKeysEnabled()=false after OFF")
	}

	// Toggle ON.
	DT.SetForeignKeysEnabled(true)
	if !DT.IsForeignKeysEnabled() {
		t.Fatal("expected DT.IsForeignKeysEnabled()=true after ON")
	}
}

// TestPragma_ForeignKeys_ReadWrite verifies the PRAGMA read/write via Executor.
func TestPragma_ForeignKeys_ReadWrite(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()
	defer DT.SetForeignKeysEnabled(true) // restore default

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
	if DT.IsForeignKeysEnabled() {
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
	if !DT.IsForeignKeysEnabled() {
		t.Fatal("expected foreign_keys=ON after PRAGMA")
	}
}

// TestPragma_ForeignKeyCheck_NoViolations verifies REQ000906: no
// violations when all FK references are valid.
func TestPragma_ForeignKeyCheck_NoViolations(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols:        []string{"id", "pid"},
		Pk:          "id",
		ForeignKeys: []DT.ForeignKeyConstraint{{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT"}},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	// Parent has id=1,2,3. Child references valid parent ids.
	DT.Tables["p"] = []DT.Row{
		{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(1)}},
		{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(2)}},
	}
	DT.Tables["c"] = []DT.Row{
		{Cols: []string{"id", "pid"}, Data: []DT.Value{NewIntValue(10), NewIntValue(1)}},
		{Cols: []string{"id", "pid"}, Data: []DT.Value{NewIntValue(20), NewIntValue(2)}},
	}

	p := WT.NewPragma(&PS.PragmaStmt{Name: "foreign_key_check"})
	p.LoadForeignKeyCheck()
	if len(p.Rows()) != 0 {
		t.Fatalf("expected 0 violations, got %d", len(p.Rows()))
	}
}

// TestPragma_ForeignKeyCheck_Violation verifies REQ000906: violation
// row is reported when a child references a non-existent parent.
func TestPragma_ForeignKeyCheck_Violation(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols:        []string{"id", "pid"},
		Pk:          "id",
		ForeignKeys: []DT.ForeignKeyConstraint{{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT"}},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	DT.Tables["p"] = []DT.Row{
		{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(1)}},
	}
	// Child has pid=99 which does NOT exist in parent.
	DT.Tables["c"] = []DT.Row{
		{Cols: []string{"id", "pid"}, Data: []DT.Value{NewIntValue(10), NewIntValue(99)}},
	}

	p := WT.NewPragma(&PS.PragmaStmt{Name: "foreign_key_check"})
	p.LoadForeignKeyCheck()
	if len(p.Rows()) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(p.Rows()))
	}
	row := p.Rows()[0]
	if row.Cols[0] != "table" || row.Data[0].ToAny() != "c" {
		t.Fatalf("expected table='c', got %v", row.Data[0].ToAny())
	}
}

// TestPragma_ForeignKeyCheck_SpecificTable verifies the optional
// table_name parameter: only that table is checked.
func TestPragma_ForeignKeyCheck_SpecificTable(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols:        []string{"id", "pid"},
		Pk:          "id",
		ForeignKeys: []DT.ForeignKeyConstraint{{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT"}},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	DT.Tables["p"] = []DT.Row{
		{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(1)}},
	}
	DT.Tables["c"] = []DT.Row{
		{Cols: []string{"id", "pid"}, Data: []DT.Value{NewIntValue(10), NewIntValue(99)}},
	}

	// Check only table "p" — no FK constraints on it, so 0 violations.
	p := WT.NewPragma(&PS.PragmaStmt{Name: "foreign_key_check", Value: "p"})
	p.LoadForeignKeyCheck()
	if len(p.Rows()) != 0 {
		t.Fatalf("expected 0 violations for table 'p', got %d", len(p.Rows()))
	}

	// Check table "c" — has violation.
	p2 := WT.NewPragma(&PS.PragmaStmt{Name: "foreign_key_check", Value: "c"})
	p2.LoadForeignKeyCheck()
	if len(p2.Rows()) != 1 {
		t.Fatalf("expected 1 violation for table 'c', got %d", len(p2.Rows()))
	}
}

// TestPragma_ForeignKeyCheck_NullFKColumns verifies that rows with
// all-NULL FK columns are not reported as violations.
func TestPragma_ForeignKeyCheck_NullFKColumns(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols:        []string{"id", "pid"},
		Pk:          "id",
		ForeignKeys: []DT.ForeignKeyConstraint{{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT"}},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	DT.Tables["p"] = []DT.Row{
		{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(1)}},
	}
	// Child has NULL pid — should not be a violation (SQL standard).
	DT.Tables["c"] = []DT.Row{
		{Cols: []string{"id", "pid"}, Data: []DT.Value{NewIntValue(10), NullValue()}},
	}

	p := WT.NewPragma(&PS.PragmaStmt{Name: "foreign_key_check"})
	p.LoadForeignKeyCheck()
	if len(p.Rows()) != 0 {
		t.Fatalf("expected 0 violations for NULL FK, got %d", len(p.Rows()))
	}
}

// noopTxWriter is a no-op TxWriter for testing transaction state.
type noopTxWriter struct{}

func (noopTxWriter) RecordWrite(key, newValue []byte)          {}
func (noopTxWriter) RecordInMemoryTable(string, []DT.Row)     {}

// TestPragma_ForeignKeys_InsideTxn_NoOp verifies REQ001307: PRAGMA
// foreign_keys is a no-op when executed inside a transaction.
func TestPragma_ForeignKeys_InsideTxn_NoOp(t *testing.T) {
	UT.UnregisterAllPragmaListeners()
	defer UT.UnregisterAllPragmaListeners()
	defer DT.SetForeignKeysEnabled(true) // restore default

	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	// Default is ON.
	if !DT.IsForeignKeysEnabled() {
		t.Fatal("expected default foreign_keys=ON")
	}

	// Simulate an active transaction by setting a TxWriter.
	// In production, the session layer (SYS/SE) sets this on BEGIN.
	DT.SetCurrentTxWriter(noopTxWriter{})
	defer DT.SetCurrentTxWriter(nil)

	// Try to turn OFF inside transaction — should be a no-op.
	_, err := e.Exec(ctx, "PRAGMA foreign_keys = OFF")
	if err != nil {
		t.Fatal(err)
	}

	// Should still be ON (the write was ignored).
	if !DT.IsForeignKeysEnabled() {
		t.Fatal("expected foreign_keys still ON inside transaction (no-op)")
	}

	// Read back inside transaction — should still report ON (1).
	rows, err := e.QueryAll(ctx, "PRAGMA foreign_keys")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].I64 != 1 {
		t.Fatalf("expected foreign_keys=1 inside txn, got %v", rows[0].Data[0].I64)
	}
}
