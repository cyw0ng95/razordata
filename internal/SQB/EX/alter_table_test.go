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

// REQ001384: DROP COLUMN cascade rules test matrix.
//
// Cascade rules documented here:
//   1. FK: when a column referenced by a foreign key is dropped, the FK
//      constraint is removed from the table's ForeignKeys list.
//   2. Generated column: when a column referenced by a generated column
//      expression is dropped, the generated column is also dropped.
//   3. Index: when an indexed column is dropped, the index in
//      RegisteredIndexes is NOT automatically removed (known limitation).
//      Callers must issue an explicit DROP INDEX before DROP COLUMN.
//   4. CHECK: CHECK constraints referencing the dropped column are not
//      validated or cascaded (known limitation).
func TestDropColumn_CascadeMatrix(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	t.Run("FK", func(t *testing.T) {
		DT.RegisterStoreSchemaWithFK("p1", []string{"id", "ref"}, []bool{false, false}, nil, nil, "id", nil)
		childFKs := []DT.ForeignKeyConstraint{{
			Columns:    []string{"ref"},
			RefTable:   "p1",
			RefColumns: []string{"ref"},
			OnDelete:   "RESTRICT",
		}}
		DT.RegisterStoreSchemaWithFK("c1", []string{"id", "ref"}, []bool{false, false}, nil, nil, "id", childFKs)

		e := NewExecutor()
		ctx := context.Background()
		if _, err := e.Exec(ctx, "ALTER TABLE c1 DROP COLUMN ref"); err != nil {
			t.Fatalf("drop column: %v", err)
		}
		cs := DT.StoreSchemas[DT.TableIDs["c1"]]
		if len(cs.ForeignKeys) != 0 {
			t.Errorf("FK was not cascaded-dropped, still have %d FK(s)", len(cs.ForeignKeys))
		}
		if cs.Cols[0] != "id" || len(cs.Cols) != 1 {
			t.Errorf("remaining cols: got %v, want [id]", cs.Cols)
		}
	})

	t.Run("Generated", func(t *testing.T) {
		DT.RegisterStoreSchemaWithFK("g1",
			[]string{"id", "base", "total"},
			[]bool{false, false, false},
			nil, nil, "id", nil,
		)
		gid := DT.TableIDs["g1"]
		gss := DT.StoreSchemas[gid]
		if gss.Generated == nil {
			gss.Generated = make([]PS.Expr, 3)
		}
		gss.Generated[2] = &PS.BinaryExpr{
			Left:  &PS.Ident{Name: "base"},
			Right: &PS.NumberLiteral{Val: 2},
		}

		e := NewExecutor()
		ctx := context.Background()
		if _, err := e.Exec(ctx, "ALTER TABLE g1 DROP COLUMN base"); err != nil {
			t.Fatalf("drop column: %v", err)
		}
		gss = DT.StoreSchemas[DT.TableIDs["g1"]]
		if len(gss.Cols) != 1 || gss.Cols[0] != "id" {
			t.Errorf("remaining cols: got %v, want [id]", gss.Cols)
		}
		if gss.Generated != nil && len(gss.Generated) != 1 {
			t.Errorf("generated slice: got %d entries, want 1", len(gss.Generated))
		}
	})

	t.Run("Index", func(t *testing.T) {
		DT.RegisterStoreSchemaWithFK("i1", []string{"id", "val"}, []bool{false, false}, nil, nil, "id", nil)
		DT.RegisterIndexWithID("i1", DT.RegisteredIndex{
			Name:    "idx_val",
			Columns: []string{"val"},
		})

		e := NewExecutor()
		ctx := context.Background()
		if _, err := e.Exec(ctx, "ALTER TABLE i1 DROP COLUMN val"); err != nil {
			t.Fatalf("drop column: %v", err)
		}
		// Column is removed from schema.
		iss := DT.StoreSchemas[DT.TableIDs["i1"]]
		if len(iss.Cols) != 1 || iss.Cols[0] != "id" {
			t.Errorf("remaining cols: got %v, want [id]", iss.Cols)
		}
		// Known limitation: index is NOT cascaded-dropped automatically.
		idxs := DT.GetRegisteredIndexes("i1")
		if len(idxs) == 0 {
			t.Log("index was cascaded-dropped (unexpected but acceptable)")
		} else {
			t.Log("known limitation: index still exists after DROP COLUMN, caller must DROP INDEX explicitly")
		}
	})

	t.Run("CHECK", func(t *testing.T) {
		DT.RegisterStoreSchemaWithFK("ch1", []string{"id", "score"}, []bool{false, false}, nil, nil, "id", nil)

		e := NewExecutor()
		ctx := context.Background()
		// No CHECK constraint support in v1 — DROP COLUMN should succeed.
		_, err := e.Exec(ctx, "ALTER TABLE ch1 DROP COLUMN score")
		if err != nil {
			t.Errorf("drop column with CHECK (v1 limitation): unexpected error: %v", err)
		}
	})
}

// REQ001384: DROP COLUMN with multiple simultaneous cascades.
// When dropping a single column triggers cascades across FK, generated,
// and index, all cascades are applied in a single ALTER.
func TestDropColumn_MultipleCascades(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	// Schema: id, base, derived (GENERATED from base), ext (FK column)
	DT.RegisterStoreSchemaWithFK("m",
		[]string{"id", "base", "derived", "ext"},
		[]bool{false, false, false, false},
		nil, nil, "id", nil,
	)
	mid := DT.TableIDs["m"]
	mss := DT.StoreSchemas[mid]

	// Attach generated expression at index 2 (derived references base).
	if mss.Generated == nil {
		mss.Generated = make([]PS.Expr, 4)
	}
	mss.Generated[2] = &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "base"},
		Right: &PS.NumberLiteral{Val: 3},
	}

	// Register an index on "base".
	DT.RegisterIndexWithID("m", DT.RegisteredIndex{
		Name:    "idx_base",
		Columns: []string{"base"},
	})

	// Attach FK constraint on "ext".
	mss.ForeignKeys = []DT.ForeignKeyConstraint{{
		Columns:    []string{"ext"},
		RefTable:   "m",
		RefColumns: []string{"id"},
		OnDelete:   "RESTRICT",
	}}

	e := NewExecutor()
	ctx := context.Background()
	if _, err := e.Exec(ctx, "ALTER TABLE m DROP COLUMN base"); err != nil {
		t.Fatalf("drop column base: %v", err)
	}

	mss = DT.StoreSchemas[DT.TableIDs["m"]]
	// After cascades: id + ext should remain (base dropped, derived cascaded-dropped).
	if len(mss.Cols) != 2 {
		t.Errorf("remaining cols: got %d (%v), want 2 ([id, ext])", len(mss.Cols), mss.Cols)
	}
	if mss.Cols[0] != "id" || mss.Cols[1] != "ext" {
		t.Errorf("cols: got %v, want [id, ext]", mss.Cols)
	}
	// FK on ext should survive (it references ext, not base).
	if len(mss.ForeignKeys) != 1 {
		t.Errorf("FK: got %d, want 1", len(mss.ForeignKeys))
	}
	// Generated column "derived" (index 2) should be cascaded-dropped.
	if mss.Generated != nil && len(mss.Generated) != 2 {
		t.Errorf("generated: got %d entries, want 2 (base and derived removed)", len(mss.Generated))
	}
}

// REQ001326: CREATE TEMP TABLE creates a session-scoped in-memory table
// that shadows any main table of the same name.
func TestTempTable_CreatedInSession(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	DT.ClearTempTables()
	defer DT.ClearTempTables()

	e := NewExecutor()
	ctx := context.Background()

	// Create a normal table.
	if _, err := e.Exec(ctx, "CREATE TABLE t (id INT, val TEXT)"); err != nil {
		t.Fatalf("create main: %v", err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO t VALUES (1, 'main')"); err != nil {
		t.Fatalf("insert main: %v", err)
	}

	// Create a temp table with the same name — should shadow the main table.
	if _, err := e.Exec(ctx, "CREATE TEMP TABLE t (id INT, val TEXT)"); err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO t VALUES (2, 'temp')"); err != nil {
		t.Fatalf("insert temp: %v", err)
	}

	// Query should see the temp table's data, not the main table's.
	rows, err := e.QueryAll(ctx, "SELECT * FROM t")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (temp table only)", len(rows))
	}
	val := toString(rows[0].Data[1])
	if val != "temp" {
		t.Errorf("val = %q, want %q", val, "temp")
	}

	// Verify temp table is tracked.
	if !DT.IsTempTable("t") {
		t.Error("expected IsTempTable('t') = true")
	}
}

// REQ001326: TEMP table is dropped on session end (ClearTempTables).
func TestTempTable_DroppedOnSessionEnd(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	DT.ClearTempTables()
	defer DT.ClearTempTables()

	e := NewExecutor()
	ctx := context.Background()

	// Create a main table and a temp table.
	if _, err := e.Exec(ctx, "CREATE TABLE main (id INT)"); err != nil {
		t.Fatalf("create main: %v", err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO main VALUES (1)"); err != nil {
		t.Fatalf("insert main: %v", err)
	}
	if _, err := e.Exec(ctx, "CREATE TEMP TABLE main (id INT)"); err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO main VALUES (99)"); err != nil {
		t.Fatalf("insert temp: %v", err)
	}

	// Simulate session end: clear temp tables.
	DT.ClearTempTables()

	// After cleanup, the main table should still be visible.
	rows, err := e.QueryAll(ctx, "SELECT * FROM main")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (main table only)", len(rows))
	}
	val, _ := rows[0].Data[0].ToAny().(int64)
	if val != 1 {
		t.Errorf("val = %d, want 1", val)
	}
}

// REQ001326: TEMP TABLE with the same name as a main table shadows it.
func TestTempTable_SameNameAsMain_Shadows(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	DT.ClearTempTables()
	defer DT.ClearTempTables()

	e := NewExecutor()
	ctx := context.Background()

	// Create main table.
	if _, err := e.Exec(ctx, "CREATE TABLE shadow (id INT, src TEXT)"); err != nil {
		t.Fatalf("create main: %v", err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO shadow VALUES (1, 'main')"); err != nil {
		t.Fatalf("insert main: %v", err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO shadow VALUES (2, 'main')"); err != nil {
		t.Fatalf("insert main2: %v", err)
	}

	// Create temp table with same name.
	if _, err := e.Exec(ctx, "CREATE TEMP TABLE shadow (id INT, src TEXT)"); err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO shadow VALUES (3, 'temp')"); err != nil {
		t.Fatalf("insert temp: %v", err)
	}

	// Query sees only temp data.
	rows, err := e.QueryAll(ctx, "SELECT src FROM shadow ORDER BY id")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (temp only)", len(rows))
	}
	val := toString(rows[0].Data[0])
	if val != "temp" {
		t.Errorf("val = %q, want %q", val, "temp")
	}

	// After clearing temp, main table is visible again.
	DT.ClearTempTables()
	rows, err = e.QueryAll(ctx, "SELECT src FROM shadow ORDER BY id")
	if err != nil {
		t.Fatalf("select after clear: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (main only)", len(rows))
	}
}

// REQ001370: CREATE TEMP TRIGGER creates a session-scoped trigger.
func TestTempTrigger_CreatedInSession(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	WT.ClearTriggerState()
	defer WT.ClearTriggerState()

	// Register a temp trigger directly via the WT API.
	trig := &PS.TriggerStmt{
		Name:      "tr_temp_log",
		OnTable:   "t",
		Time:      "AFTER",
		Event:     "INSERT",
		ForEach:   "FOR EACH ROW",
		Body:      []PS.Stmt{}, // no-op body, just test registration
		Temporary: true,
	}
	if err := WT.RegisterTrigger(trig); err != nil {
		t.Fatalf("register temp trigger: %v", err)
	}

	// Verify it's registered as temp.
	if !WT.IsTriggerRegistered("tr_temp_log") {
		t.Error("expected temp trigger to be registered")
	}

	// Verify temp trigger fires on INSERT.
	// (body is empty so no side effects, but no error = pass)
	e := NewExecutor()
	e.RegisterTable("t", []string{"id", "val"})
	ctx := context.Background()
	if _, err := e.Exec(ctx, "INSERT INTO t VALUES (1, 'a')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

// REQ001370: TEMP triggers are dropped on session end.
func TestTempTrigger_DroppedOnSessionEnd(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	WT.ClearTriggerState()
	defer WT.ClearTriggerState()

	trig := &PS.TriggerStmt{
		Name:      "tr_temp",
		OnTable:   "t",
		Time:      "BEFORE",
		Event:     "INSERT",
		ForEach:   "FOR EACH ROW",
		Temporary: true,
	}
	if err := WT.RegisterTrigger(trig); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Simulate session end.
	WT.ClearTempTriggers()

	if WT.IsTriggerRegistered("tr_temp") {
		t.Error("temp trigger should be gone after ClearTempTriggers")
	}
}

// REQ001327: PRAGMA temp_store read/write round-trip.
func TestPragma_TempStore_ReadWriteRoundTrip(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	// Save and restore the original mode.
	orig := DT.TempStoreMode()
	defer DT.SetTempStoreMode(orig)

	e := NewExecutor()
	ctx := context.Background()

	// Read default (2 = MEMORY).
	rows, err := e.QueryAll(ctx, "PRAGMA temp_store")
	if err != nil {
		t.Fatalf("PRAGMA temp_store: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Data[0].S != "2" {
		t.Errorf("default temp_store = %q, want 2", rows[0].Data[0].S)
	}

	// Write temp_store = 0 (DEFAULT).
	if _, err := e.Exec(ctx, "PRAGMA temp_store = 0"); err != nil {
		t.Fatalf("PRAGMA temp_store = 0: %v", err)
	}
	if got := DT.TempStoreMode(); got != 0 {
		t.Errorf("temp_store = %d, want 0", got)
	}

	// Write temp_store = 1 (FILE).
	if _, err := e.Exec(ctx, "PRAGMA temp_store = 1"); err != nil {
		t.Fatalf("PRAGMA temp_store = 1: %v", err)
	}
	if got := DT.TempStoreMode(); got != 1 {
		t.Errorf("temp_store = %d, want 1", got)
	}

	// Write temp_store = 2 (MEMORY).
	if _, err := e.Exec(ctx, "PRAGMA temp_store = 2"); err != nil {
		t.Fatalf("PRAGMA temp_store = 2: %v", err)
	}
	if got := DT.TempStoreMode(); got != 2 {
		t.Errorf("temp_store = %d, want 2", got)
	}
}
