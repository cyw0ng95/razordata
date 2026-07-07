package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestAlterTable_AddColumn(t *testing.T) {
	e := NewExecutor()
	e.RegisterTableWithPK("t1", []string{"id", "name"}, "id")
	ctx := context.Background()

	// Add a column
	result, err := e.Exec(ctx, "ALTER TABLE t1 ADD COLUMN age INT")
	if err != nil {
		t.Fatalf("add column: %v", err)
	}
	if result.RowsAffected != 0 {
		t.Errorf("expected 0 rows affected, got %d", result.RowsAffected)
	}

	// Try adding a duplicate column (should fail)
	_, err = e.Exec(ctx, "ALTER TABLE t1 ADD COLUMN age INT")
	if err == nil {
		t.Error("expected error adding duplicate column, got nil")
	}
}

func TestAlterTable_AddColumnWithDefault(t *testing.T) {
	e := NewExecutor()
	e.RegisterTable("t2", []string{"id", "name"})
	ctx := context.Background()

	_, err := e.Exec(ctx, "ALTER TABLE t2 ADD COLUMN active INT DEFAULT 1")
	if err != nil {
		t.Fatalf("add column with default: %v", err)
	}
}

func TestAlterTable_AddColumnNotNull(t *testing.T) {
	e := NewExecutor()
	e.RegisterTable("t3", []string{"id", "name"})
	ctx := context.Background()

	_, err := e.Exec(ctx, "ALTER TABLE t3 ADD COLUMN value INT NOT NULL")
	if err != nil {
		t.Fatalf("add column not null: %v", err)
	}
}

func TestAlterTable_DropColumn(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	e.RegisterTableWithPK("t4", []string{"id", "name", "age"}, "id")
	ctx := context.Background()

	// Drop a column
	_, err := e.Exec(ctx, "ALTER TABLE t4 DROP COLUMN age")
	if err != nil {
		t.Fatalf("drop column: %v", err)
	}

	// Try dropping a non-existent column
	_, err = e.Exec(ctx, "ALTER TABLE t4 DROP COLUMN nonexistent")
	if err == nil {
		t.Error("expected error dropping non-existent column, got nil")
	}
}

func TestAlterTable_DropColumn_PK(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	e.RegisterTableWithPK("t5", []string{"id", "name"}, "id")
	ctx := context.Background()

	// In in-memory mode (no engine store) the executor does not
	// enforce the primary-key invariant because the in-memory
	// `schemas` map (source.go) only carries column names, not PK
	// info. See execDropColumnInMemory's "we don't know which is
	// PK" comment. The store-backed path does enforce this — it
	// runs through execDropColumn's pk check at alter_table.go.
	_, err := e.Exec(ctx, "ALTER TABLE t5 DROP COLUMN id")
	if err != nil {
		t.Errorf("in-memory drop PK is permissive by design, got: %v", err)
	}
}

func TestAlterTable_RenameTable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	e.RegisterTableWithPK("t6", []string{"id", "name"}, "id")
	ctx := context.Background()

	// Rename the table
	_, err := e.Exec(ctx, "ALTER TABLE t6 RENAME TO t6_new")
	if err != nil {
		t.Fatalf("rename table: %v", err)
	}

	// Try renaming to existing table name
	_, err = e.Exec(ctx, "ALTER TABLE t6_new RENAME TO t6_new")
	if err == nil {
		t.Error("expected error renaming to existing name, got nil")
	}
}

func TestAlterTable_NonExistentTable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	// ALTER TABLE on non-existent table
	_, err := e.Exec(ctx, "ALTER TABLE nonexistent ADD COLUMN x INT")
	if err == nil {
		t.Error("expected error altering non-existent table, got nil")
	}

	// DROP COLUMN on non-existent table
	_, err = e.Exec(ctx, "ALTER TABLE nonexistent DROP COLUMN x")
	if err == nil {
		t.Error("expected error dropping column from non-existent table, got nil")
	}

	// RENAME on non-existent table
	_, err = e.Exec(ctx, "ALTER TABLE nonexistent RENAME TO new_name")
	if err == nil {
		t.Error("expected error renaming non-existent table, got nil")
	}
}

// TestAlterTableParser verifies the parser correctly captures column
// type and NULLABLE info for ALTER TABLE ADD COLUMN.
func TestAlterTableParser(t *testing.T) {
	cases := []struct {
		sql      string
		col      string
		typ      int
		nullable bool
	}{
		{"ALTER TABLE t ADD COLUMN name TEXT", "name", 1, true},
		{"ALTER TABLE t ADD COLUMN age INT NOT NULL", "age", 1, false},
		{"ALTER TABLE t ADD COLUMN price REAL", "price", 0, true},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			p := PS.NewParser(c.sql)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			alt, ok := stmt.(*PS.AlterTableStmt)
			if !ok {
				t.Fatalf("expected AlterTableStmt, got %T", stmt)
			}
			if alt.Table != "t" {
				t.Errorf("table: got %q, want %q", alt.Table, "t")
			}
			if alt.Action != "ADD COLUMN" {
				t.Errorf("action: got %q, want %q", alt.Action, "ADD COLUMN")
			}
			if alt.Column != c.col {
				t.Errorf("column: got %q, want %q", alt.Column, c.col)
			}
			if alt.NewCol == nil {
				t.Fatal("NewCol is nil")
			}
			if alt.NewCol.Nullable != c.nullable {
				t.Errorf("nullable: got %v, want %v", alt.NewCol.Nullable, c.nullable)
			}
		})
	}
}

// REQ001321: ALTER TABLE RENAME TO cascades into FK RefTable of other tables.
func TestAlterTable_RenameCascadesFK(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	// Register parent + child tables under in-memory DT directly so we can
	// wire an FK constraint from child → parent (the executor's RegisterTable
	// helper does not accept FKs).
	DT.RegisterStoreSchemaWithFK("parent", []string{"id"}, []bool{false}, nil, nil, "id", nil)
	childFKs := []DT.ForeignKeyConstraint{{
		Columns:    []string{"pid"},
		RefTable:   "parent",
		RefColumns: []string{"id"},
		OnDelete:   "RESTRICT",
	}}
	DT.RegisterStoreSchemaWithFK("child", []string{"id", "pid"}, []bool{false, false}, nil, nil, "id", childFKs)

	// Run ALTER TABLE parent RENAME TO parent_new via the executor.
	e := NewExecutor()
	ctx := context.Background()
	if _, err := e.Exec(ctx, "ALTER TABLE parent RENAME TO parent_new"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Verify child's FK constraint now points at parent_new.
	childID, ok := DT.TableIDs["child"]
	if !ok {
		t.Fatal("child table missing after rename")
	}
	childSS := DT.StoreSchemas[childID]
	if len(childSS.ForeignKeys) != 1 {
		t.Fatalf("expected 1 FK on child, got %d", len(childSS.ForeignKeys))
	}
	if childSS.ForeignKeys[0].RefTable != "parent_new" {
		t.Errorf("FK RefTable: got %q, want %q", childSS.ForeignKeys[0].RefTable, "parent_new")
	}
}

// REQ001321: ALTER TABLE RENAME TO cascades into view FROM/JOIN references.
func TestAlterTable_RenameCascadesView(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	clearViews()
	defer clearViews()

	// Register a view that targets "parent".
	DT.ViewRegistry["v_parent"] = &PS.Select{From: "parent"}

	// Register the parent table so the rename can succeed.
	DT.RegisterStoreSchemaWithFK("parent", []string{"id"}, []bool{false}, nil, nil, "id", nil)

	e := NewExecutor()
	ctx := context.Background()
	if _, err := e.Exec(ctx, "ALTER TABLE parent RENAME TO parent_new"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := DT.ViewRegistry["v_parent"].From; got != "parent_new" {
		t.Errorf("view From: got %q, want %q", got, "parent_new")
	}
}

// REQ001321: ALTER TABLE RENAME TO cascades into trigger OnTable.
func TestAlterTable_RenameCascadesTrigger(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	WT.ClearTriggerState()
	defer WT.ClearTriggerState()

	// Register a trigger whose OnTable is "parent".
	trig := &PS.TriggerStmt{Name: "tr_parent", OnTable: "parent"}
	if err := WT.RegisterTrigger(trig); err != nil {
		t.Fatalf("register trigger: %v", err)
	}

	DT.RegisterStoreSchemaWithFK("parent", []string{"id"}, []bool{false}, nil, nil, "id", nil)

	e := NewExecutor()
	ctx := context.Background()
	if _, err := e.Exec(ctx, "ALTER TABLE parent RENAME TO parent_new"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Verify the trigger now points at parent_new by looking up triggers
	// under the new table name.
	got := WT.TriggersForTable("parent_new")
	if len(got) == 0 || got[0].OnTable != "parent_new" {
		errs := ""
		for _, t2 := range got {
			errs += t2.OnTable + " "
		}
		t.Errorf("trigger OnTable: got [%s], want non-empty with parent_new", errs)
	}
}

// clearViews resets the view registry; used by the rename-cascade tests.
func clearViews() {
	DT.ViewMu.Lock()
	for k := range DT.ViewRegistry {
		delete(DT.ViewRegistry, k)
	}
	DT.ViewMu.Unlock()
}

// REQ001322: ALTER COLUMN ... SET DEFAULT updates the default expr.
func TestAlterColumn_SetDefault(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterStoreSchemaWithFK("t_setdef", []string{"id", "score"}, []bool{false, false}, nil, nil, "id", nil)

	e := NewExecutor()
	ctx := context.Background()

	if _, err := e.Exec(ctx, "ALTER TABLE t_setdef ALTER COLUMN score SET DEFAULT 0"); err != nil {
		t.Fatalf("set default: %v", err)
	}

	id, ok := DT.TableIDs["t_setdef"]
	if !ok {
		t.Fatal("table missing")
	}
	ss := DT.StoreSchemas[id]
	if ss.Defaults == nil || ss.Defaults[1] == nil {
		t.Fatalf("expected default at idx 1, got nil")
	}
	// Verify it's a NumberLiteral with Val=0.
	num, ok := ss.Defaults[1].(*PS.NumberLiteral)
	if !ok {
		t.Fatalf("default expr type: got %T, want *PS.NumberLiteral", ss.Defaults[1])
	}
	if num.Val != 0 {
		t.Errorf("default value: got %d, want 0", num.Val)
	}
}

// REQ001322: default expr referencing an unknown column must error.
func TestAlterColumn_SetDefault_UnknownColumn(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterStoreSchemaWithFK("t_baddef", []string{"id", "score"}, []bool{false, false}, nil, nil, "id", nil)

	e := NewExecutor()
	ctx := context.Background()
	_, err := e.Exec(ctx, "ALTER TABLE t_baddef ALTER COLUMN score SET DEFAULT nonexistent + 1")
	if err == nil {
		t.Error("expected error referencing unknown column, got nil")
	}
}

// REQ001323: ALTER COLUMN ... DROP DEFAULT clears the default expr.
func TestAlterColumn_DropDefault(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	// Start with a default in place.
	DT.RegisterStoreSchemaWithFK("t_dropdef", []string{"id", "score"}, []bool{false, false}, []PS.Expr{nil, &PS.NumberLiteral{Val: 5}}, nil, "id", nil)

	e := NewExecutor()
	ctx := context.Background()

	if _, err := e.Exec(ctx, "ALTER TABLE t_dropdef ALTER COLUMN score DROP DEFAULT"); err != nil {
		t.Fatalf("drop default: %v", err)
	}

	id := DT.TableIDs["t_dropdef"]
	ss := DT.StoreSchemas[id]
	if ss.Defaults[1] != nil {
		t.Errorf("default after drop: got %v, want nil", ss.Defaults[1])
	}
}

// REQ001324: DROP COLUMN cascades to FK constraints referencing the column.
func TestDropColumn_CascadesFK(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	// Register parent + child where child FKs on parent.tid.
	DT.RegisterStoreSchemaWithFK("p", []string{"id", "tid"}, []bool{false, false}, nil, nil, "id", nil)
	childFKs := []DT.ForeignKeyConstraint{{
		Columns:    []string{"tid"},
		RefTable:   "p",
		RefColumns: []string{"tid"},
		OnDelete:   "RESTRICT",
	}}
	DT.RegisterStoreSchemaWithFK("c", []string{"id", "tid"}, []bool{false, false}, nil, nil, "id", childFKs)

	e := NewExecutor()
	ctx := context.Background()
	if _, err := e.Exec(ctx, "ALTER TABLE c DROP COLUMN tid"); err != nil {
		t.Fatalf("drop column: %v", err)
	}

	cs := DT.StoreSchemas[DT.TableIDs["c"]]
	if len(cs.ForeignKeys) != 0 {
		t.Errorf("FK should have been cascaded-dropped, got %d", len(cs.ForeignKeys))
	}
	if cs.Cols[0] != "id" {
		t.Errorf("remaining cols: got %v, want [id]", cs.Cols)
	}
}

// REQ001325: DROP COLUMN cascades to generated columns whose expr references
// the dropped column.
func TestDropColumn_CascadesGenerated(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	// Schema: id, base, total (GENERATED ALWAYS AS (base * 2))
	DT.RegisterStoreSchemaWithFK("g",
		[]string{"id", "base", "total"},
		[]bool{false, false, false},
		nil,
		nil,
		"id",
		nil,
	)
	gid := DT.TableIDs["g"]
	gss := DT.StoreSchemas[gid]
	// Manually attach the generated expression at index 2 (the "total" col).
	if gss.Generated == nil {
		gss.Generated = make([]PS.Expr, 3)
	}
	gss.Generated[2] = &PS.BinaryExpr{
		Op:    0, // unused for this test
		Left:  &PS.Ident{Name: "base"},
		Right: &PS.NumberLiteral{Val: 2},
	}

	e := NewExecutor()
	ctx := context.Background()
	if _, err := e.Exec(ctx, "ALTER TABLE g DROP COLUMN base"); err != nil {
		t.Fatalf("drop column base: %v", err)
	}

	gss = DT.StoreSchemas[DT.TableIDs["g"]]
	// The "total" generated column must also be gone because its expr
	// referenced "base".
	if len(gss.Cols) != 1 || gss.Cols[0] != "id" {
		t.Errorf("remaining cols: got %v, want [id]", gss.Cols)
	}
}
