package EX

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
	ap "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestBugfix_BuildWriterOp_RoutesAllDDL covers REQ000490, REQ000481, REQ000500,
// REQ000476, REQ000478, REQ000494, REQ000496: buildWriterOp now routes
// PRAGMA, EXPLAIN, EXPLAIN QUERY PLAN, TRUNCATE, REINDEX, DROP VIEW,
// DROP TRIGGER instead of returning "ex: not a writable statement".
func TestBugfix_BuildWriterOp_RoutesAllDDL(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()

	stmts := []string{
		"CREATE TABLE bvt (id INTEGER PRIMARY KEY, v INTEGER)",
		"INSERT INTO bvt VALUES (1, 10)",
		"PRAGMA cache_size",
		"EXPLAIN SELECT * FROM bvt",
		"EXPLAIN QUERY PLAN SELECT * FROM bvt",
		"TRUNCATE TABLE bvt",
		"REINDEX",
		"REINDEX bvt",
		"CREATE VIEW bvv AS SELECT id FROM bvt",
		"DROP VIEW bvv",
		"CREATE TRIGGER bvt_trg AFTER INSERT ON bvt BEGIN SELECT 1; END",
		"DROP TRIGGER bvt_trg",
	}
	for _, s := range stmts {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Errorf("%q failed: %v", s, err)
		}
	}
}

// TestBugfix_Explain_ReturnsPlan covers REQ000481, REQ000500: EXPLAIN
// returns a single-row result with a "plan" column describing the inner
// statement. EXPLAIN QUERY PLAN also works.
func TestBugfix_Explain_ReturnsPlan(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")

	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT * FROM t")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("EXPLAIN: got 0 rows, want >= 1")
	}
	// EXPLAIN returns {id, parent, notused, detail} — last column is
	// the operator description. The planner renders "SeqScan" or
	// "Scan t" depending on whether the inner plan is a SeqScan.
	detail := toString(rows[len(rows)-1].Data[3])
	if !strings.Contains(detail, "t") {
		t.Errorf("EXPLAIN detail = %q, want substring t", detail)
	}

	rows, err = ex.QueryAll(ctx, "EXPLAIN QUERY PLAN SELECT * FROM t")
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("EXPLAIN QUERY PLAN: got 0 rows, want >= 1")
	}
	// Query plan format: scan|search|...|detail columns.
	detail = toString(rows[len(rows)-1].Data[len(rows[len(rows)-1].Data)-1])
	if !strings.Contains(detail, "t") {
		t.Errorf("EXPLAIN QUERY PLAN last col = %q, want substring t", detail)
	}
}

// toString converts the heterogeneous cell type to a string for
// substring checks.
func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

// TestBugfix_Truncate_ClearsTable covers REQ000476: TRUNCATE TABLE
// empties the in-memory table.
func TestBugfix_Truncate_ClearsTable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	if _, err := ex.Exec(ctx, "TRUNCATE TABLE t"); err != nil {
		t.Fatalf("TRUNCATE: %v", err)
	}
	rows, err := ex.QueryAll(ctx, "SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("after TRUNCATE: got %d rows, want 0", len(rows))
	}
}

// TestBugfix_Reindex_NoOp covers REQ000478: REINDEX accepts both
// REINDEX (all) and REINDEX name without error.
func TestBugfix_Reindex_NoOp(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "REINDEX"); err != nil {
		t.Errorf("REINDEX: %v", err)
	}
	if _, err := ex.Exec(ctx, "REINDEX t"); err != nil {
		t.Errorf("REINDEX t: %v", err)
	}
}

// TestBugfix_DropView_RemovesRegistry covers REQ000494: DROP VIEW
// removes the view from the registry.
func TestBugfix_DropView_RemovesRegistry(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	if _, err := ex.Exec(ctx, "CREATE VIEW v AS SELECT id FROM t"); err != nil {
		t.Fatalf("CREATE VIEW: %v", err)
	}
	if LookupView("v") == nil {
		t.Fatal("view not registered after CREATE")
	}
	if _, err := ex.Exec(ctx, "DROP VIEW v"); err != nil {
		t.Fatalf("DROP VIEW: %v", err)
	}
	if LookupView("v") != nil {
		t.Error("view still registered after DROP")
	}
}

// TestBugfix_DropTrigger_RemovesRegistry covers REQ000496: DROP TRIGGER
// removes the trigger from the registry.
func TestBugfix_DropTrigger_RemovesRegistry(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TRIGGER tg AFTER INSERT ON t BEGIN SELECT 1; END"); err != nil {
		t.Fatalf("CREATE TRIGGER: %v", err)
	}
	triggerMu.RLock()
	_, present := triggerReg["tg"]
	triggerMu.RUnlock()
	if !present {
		t.Fatal("trigger not registered after CREATE")
	}
	if _, err := ex.Exec(ctx, "DROP TRIGGER tg"); err != nil {
		t.Fatalf("DROP TRIGGER: %v", err)
	}
	triggerMu.RLock()
	_, present = triggerReg["tg"]
	triggerMu.RUnlock()
	if present {
		t.Error("trigger still registered after DROP")
	}
}

// TestBugfix_Pragma_NoOp covers REQ000490: PRAGMA name [= value]
// executes without error (no-op for unknown pragmas).
func TestBugfix_Pragma_NoOp(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "PRAGMA cache_size"); err != nil {
		t.Errorf("PRAGMA read: %v", err)
	}
	if _, err := ex.Exec(ctx, "PRAGMA cache_size = 1000"); err != nil {
		t.Errorf("PRAGMA write: %v", err)
	}
}

// TestBugfix_InsertOnConflictDoUpdate covers REQ000511: ON CONFLICT
// DO UPDATE applies the SET clause to the conflicting row.
func TestBugfix_InsertOnConflictDoUpdate(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	// registerStoreSchema wires the schema into tableIDs so
	// schemaFor() returns true; without it the in-memory
	// Insert path skips unique-key validation.
	registerStoreSchema("t", []string{"id", "v"}, "id")
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)"); err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	// Conflict on PK 1: update v to 99.
	stmt, err := PS.NewParser(
		"INSERT INTO t VALUES (1, 99) ON CONFLICT (id) DO UPDATE SET v = 99",
	).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	insertStmt, ok := stmt.(*PS.Insert)
	if !ok {
		t.Fatalf("stmt is %T, want *PS.Insert", stmt)
	}
	op, err := ex.buildWriterOp(insertStmt)
	if err != nil {
		t.Fatalf("buildWriterOp: %v", err)
	}
	defer op.Close()
	if _, err := op.Next(ctx); err != nil && err != ErrNoRows {
		t.Fatalf("insert op: %v", err)
	}

	// Read the row back; v should be 99.
	rows, err := ex.QueryAll(ctx, "SELECT v FROM t WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if v, _ := rows[0].Data[0].(int64); v != 99 {
		t.Errorf("v = %d, want 99 (UPSERT update should have taken effect)", v)
	}
}

// TestBugfix_InsertOnConflictDoNothing covers REQ000511 DO NOTHING:
// duplicate row is silently dropped, no error.
func TestBugfix_InsertOnConflictDoNothing(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	registerStoreSchema("t", []string{"id", "v"}, "id")
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)"); err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 99) ON CONFLICT (id) DO NOTHING"); err != nil {
		t.Errorf("DO NOTHING should not error: %v", err)
	}
	rows, _ := ex.QueryAll(ctx, "SELECT v FROM t WHERE id = 1")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if v, _ := rows[0].Data[0].(int64); v != 10 {
		t.Errorf("v = %d, want 10 (DO NOTHING should not have changed the row)", v)
	}
}

// TestBugfix_BuildWriterOp_ErrorsOnUnknown covers the negative case:
// genuine unknowns still error so we don't silently drop statements.
func TestBugfix_BuildWriterOp_ErrorsOnUnknown(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()
	// An unknown DDL should still return an error, not silently no-op.
	_, err := ex.Exec(ctx, "FOOBAR quux")
	if err == nil {
		t.Error("unknown DDL should error")
	}
	if !strings.Contains(err.Error(), "syntax") && !strings.Contains(err.Error(), "FOOBAR") {
		// Either the parser or the executor should reject it.
		t.Logf("got error (acceptable): %v", err)
	}
}

// TestBugfix_Truncate_NotRegistered covers REQ000476: TRUNCATE on an
// unknown table is a no-op (not an error).
func TestBugfix_Truncate_NotRegistered(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()
	// Should not error.
	if _, err := ex.Exec(ctx, "TRUNCATE TABLE unknown_t"); err != nil {
		t.Errorf("TRUNCATE on missing table: %v", err)
	}
}

// TestBugfix_DropView_Unknown covers REQ000494: DROP VIEW on an
// unknown view is a no-op (not an error).
func TestBugfix_DropView_Unknown(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "DROP VIEW unknown_v"); err != nil {
		t.Errorf("DROP VIEW on missing view: %v", err)
	}
}

// TestBugfix_DropTrigger_Unknown covers REQ000496: DROP TRIGGER on
// an unknown trigger is a no-op.
func TestBugfix_DropTrigger_Unknown(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "DROP TRIGGER unknown_t"); err != nil {
		t.Errorf("DROP TRIGGER on missing trigger: %v", err)
	}
}

// TestBugfix_ConstraintNotPresent is a sanity check: the new error
// classification still wraps ErrConstraint.
func TestBugfix_ConstraintNotPresent(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	registerStoreSchema("t", []string{"id"}, "id")
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	_, err := ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	if err == nil {
		t.Fatal("expected duplicate key error")
	}
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("err = %v, want wrap of ErrConstraint", err)
	}
}

// TestBugfix_FKOnUpdate covers REQ000513: when an UPDATE changes a FK
// column to a value that doesn't exist in the referenced table, the
// UPDATE must fail with a wrapped ErrConstraint. The in-memory path
// is exercised via validateForeignKeyUpdateInMemory.
func TestBugfix_FKOnUpdate(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	// Build parent + child schemas.
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

	// Seed in-memory table for parent: id=1, id=2.
	tables["p"] = []Row{
		{Cols: []string{"id"}, Data: []interface{}{int64(1)}},
	}
	tables["c"] = []Row{
		{Cols: []string{"id", "pid"}, Data: []interface{}{int64(10), int64(1)}},
	}

	// Update child's pid from 1 to 99 — should fail since parent
	// has no row with id=99.
	err := validateForeignKeyUpdateInMemory(child,
		[]interface{}{int64(10), int64(1)}, // old
		[]interface{}{int64(10), int64(99)}, // new
	)
	if err == nil {
		t.Fatal("expected FK violation, got nil")
	}
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("err = %v, want wrap of ErrConstraint", err)
	}

	// Same-value update (no FK change) must NOT trigger re-check.
	if err := validateForeignKeyUpdateInMemory(child,
		[]interface{}{int64(10), int64(1)},
		[]interface{}{int64(10), int64(1)},
	); err != nil {
		t.Errorf("same-value update should be a no-op for FK: %v", err)
	}

	// Update child's pid to 2 (which doesn't exist as a parent row
	// either). Should still fail.
	err = validateForeignKeyUpdateInMemory(child,
		[]interface{}{int64(10), int64(1)},
		[]interface{}{int64(10), int64(2)},
	)
	if err == nil {
		t.Error("expected FK violation for new value, got nil")
	}
}

// TestBugfix_FKOnDelete covers REQ000514: DELETE on a parent row
// that has referencing child rows must fail with a wrapped
// ErrConstraint (default RESTRICT action).
func TestBugfix_FKOnDelete(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	parent := &storeSchema{cols: []string{"id"}, pk: "id"}
	child := &storeSchema{
		cols:        []string{"id", "pid"},
		pk:          "id",
		foreignKeys: []ForeignKeyConstraint{{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT"}},
	}
	storeSchemas[10] = parent
	storeSchemas[11] = child
	tableIDs["p"] = 10
	tableIDs["c"] = 11

	tables["p"] = []Row{{Cols: []string{"id"}, Data: []interface{}{int64(1)}}}
	tables["c"] = []Row{{Cols: []string{"id", "pid"}, Data: []interface{}{int64(100), int64(1)}}}

	// Deleting parent id=1 must fail because child (100, 1) references it.
	err := validateForeignKeyDeleteInMemory("p",
		[]interface{}{int64(1)}, parent)
	if err == nil {
		t.Fatal("expected FK violation, got nil")
	}
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("err = %v, want wrap of ErrConstraint", err)
	}
}

// TestBugfix_ExecReturningCount covers REQ000512: Exec with a
// RETURNING clause must return the correct RowsAffected count for
// multi-row DML, instead of hardcoded 1.
func TestBugfix_ExecReturningCount(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	registerStoreSchema("t", []string{"id", "v"}, "id")
	// REQ000512: registerStoreSchema only populates storeSchemas and
	// tableIDs, not the schemas map that Schema() reads. The in-memory
	// Insert path uses Schema() to set row Cols; without it rows have
	// nil Cols and UPDATE/DELETE column lookups fail silently.
	RegisterTableSchema("t", []string{"id", "v"})
	ctx := context.Background()

	// Single-row INSERT with RETURNING
	res, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 10) RETURNING *")
	if err != nil {
		t.Fatalf("single insert returning: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("single insert: RowsAffected = %d, want 1", res.RowsAffected)
	}

	// Multi-row INSERT with RETURNING
	res, err = ex.Exec(ctx, "INSERT INTO t VALUES (2, 20), (3, 30) RETURNING *")
	if err != nil {
		t.Fatalf("multi insert returning: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Errorf("multi insert: RowsAffected = %d, want 2", res.RowsAffected)
	}

	// UPDATE with RETURNING
	res, err = ex.Exec(ctx, "UPDATE t SET v = v + 1 WHERE id > 0 RETURNING id, v")
	if err != nil {
		t.Fatalf("update returning: %v", err)
	}
	if res.RowsAffected != 3 {
		t.Errorf("update returning: RowsAffected = %d, want 3", res.RowsAffected)
	}

	// DELETE with RETURNING
	res, err = ex.Exec(ctx, "DELETE FROM t WHERE id = 1 RETURNING id")
	if err != nil {
		t.Fatalf("delete returning: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("delete returning: RowsAffected = %d, want 1", res.RowsAffected)
	}

	// DML without RETURNING should still report correct RowsAffected
	res, err = ex.Exec(ctx, "UPDATE t SET v = 99 WHERE id > 0")
	if err != nil {
		t.Fatalf("update no returning: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Errorf("update no returning: RowsAffected = %d, want 2", res.RowsAffected)
	}
}

// TestBugfix_CorrelatedSubqueryWithIndex covers REQ000525: when the
// inner table of a correlated subquery has a registered index, the
// planner may choose IndexScan. injectOuter must handle IndexScan to
// inject the outer row reference.
func TestBugfix_CorrelatedSubqueryWithIndex(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"id", "val"})
	ex.RegisterTable("t2", []string{"ref"})
	ex.RegisterIndex("t2", "idx_ref", []string{"ref"})
	ctx := context.Background()

	for _, sql := range []string{
		"INSERT INTO t1 VALUES (1, 'a')",
		"INSERT INTO t1 VALUES (2, 'b')",
		"INSERT INTO t1 VALUES (3, 'c')",
		"INSERT INTO t2 VALUES (1)",
		"INSERT INTO t2 VALUES (3)",
	} {
		if _, err := ex.Exec(ctx, sql); err != nil {
			t.Fatalf("seed: %s: %v", sql, err)
		}
	}

	// Use unqualified column references so the existing Eval
	// lookup works (table.qualified names need outer row columns
	// stored with table prefix, which rows don't carry).
	rows, err := ex.QueryAll(ctx,
		"SELECT val FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE ref = id)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Data[0].(string)] = true
	}
	if !got["a"] || !got["c"] {
		t.Errorf("unexpected: %v", got)
	}
}

// TestBugfix_UniqueOnUpdate covers REQ000516: when an UPDATE changes a
// row's value to one that collides with another row's UNIQUE key, the
// UPDATE must fail with ErrConstraint.
func TestBugfix_UniqueOnUpdate(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	RegisterTableSchema("t", []string{"id", "email"})
	registerStoreSchemaFull("t",
		[]string{"id", "email"},
		[]bool{false, false},
		nil,
		[]UniqueKey{{Cols: []int{1}}}, // UNIQUE on email
		"id",
	)
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 'a@x')"); err != nil {
		t.Fatalf("insert alice: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (2, 'b@x')"); err != nil {
		t.Fatalf("insert bob: %v", err)
	}

	// Updating id=2's email to alice's email must fail.
	_, err := ex.Exec(ctx, "UPDATE t SET email = 'a@x' WHERE id = 2")
	if err == nil {
		t.Fatal("expected unique violation on update, got nil")
	}
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("err = %v, want wrap of ErrConstraint", err)
	}

	// No-op update (same email) must NOT fail.
	res, err := ex.Exec(ctx, "UPDATE t SET email = 'b@x' WHERE id = 2")
	if err != nil {
		t.Fatalf("no-op update should not fail: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", res.RowsAffected)
	}
}

// TestBugfix_CreateTableAsSelect covers REQ000520: CREATE TABLE AS
// SELECT creates a new table populated with the SELECT query results.
func TestBugfix_CreateTableAsSelect(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("src", []string{"id", "name"})
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO src VALUES (1, 'alice')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO src VALUES (2, 'bob')"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// CTAS: copy all rows from src into a new table.
	_, err := ex.Exec(ctx, "CREATE TABLE dst AS SELECT * FROM src")
	if err != nil {
		t.Fatalf("ctas: %v", err)
	}

	// Verify dst has the same rows.
	rows, err := ex.QueryAll(ctx, "SELECT id, name FROM dst ORDER BY id")
	if err != nil {
		t.Fatalf("query dst: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows in dst, got %d", len(rows))
	}
	if rows[0].Data[0].(int64) != 1 || rows[0].Data[1].(string) != "alice" {
		t.Errorf("row 0: %v", rows[0].Data)
	}
	if rows[1].Data[0].(int64) != 2 || rows[1].Data[1].(string) != "bob" {
		t.Errorf("row 1: %v", rows[1].Data)
	}

	// CTAS with WHERE filter.
	_, err = ex.Exec(ctx, "CREATE TABLE dst2 AS SELECT * FROM src WHERE id = 1")
	if err != nil {
		t.Fatalf("ctas filtered: %v", err)
	}
	rows, err = ex.QueryAll(ctx, "SELECT id, name FROM dst2")
	if err != nil {
		t.Fatalf("query dst2: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row in dst2, got %d", len(rows))
	}

	// CTAS with specific columns.
	_, err = ex.Exec(ctx, "CREATE TABLE dst3 AS SELECT name FROM src WHERE id = 2")
	if err != nil {
		t.Fatalf("ctas cols: %v", err)
	}
	rows, err = ex.QueryAll(ctx, "SELECT name FROM dst3")
	if err != nil {
		t.Fatalf("query dst3: %v", err)
	}
	if len(rows) != 1 || rows[0].Data[0].(string) != "bob" {
		t.Errorf("dst3: %v", rows[0].Data)
	}
}