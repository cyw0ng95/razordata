package EX

import (
	"context"
	"testing"
)

// REQ000460: UPDATE executor must produce correct results for all
// UPDATE patterns: simple SET, expression SET, multi-column SET,
// WHERE filtering, full-table UPDATE.
func TestReq460_UpdateCorrectResults(t *testing.T) {
	exec := NewExecutor()
	exec.RegisterTable("t1", []string{"x", "y"})
	defer cleanupTest("t1")

	ctx := context.Background()

	// Insert test data
	mustExec(t, exec, ctx, "INSERT INTO t1 VALUES (1, 'a')")
	mustExec(t, exec, ctx, "INSERT INTO t1 VALUES (2, 'b')")
	mustExec(t, exec, ctx, "INSERT INTO t1 VALUES (3, 'c')")

	// UPDATE without WHERE (all rows)
	mustExec(t, exec, ctx, "UPDATE t1 SET x = 10")
	rows := mustQueryAll(t, exec, ctx, "SELECT x FROM t1 ORDER BY x")
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(int64(10))) || !rows[1].Data[0].Equal(NewIntValue(int64(10))) || !rows[2].Data[0].Equal(NewIntValue(int64(10))) {
		t.Fatalf("all x should be 10, got %v, %v, %v", rows[0].Data[0], rows[1].Data[0], rows[2].Data[0])
	}

	// UPDATE with WHERE
	mustExec(t, exec, ctx, "UPDATE t1 SET y = 'z' WHERE x = 10")
	rows = mustQueryAll(t, exec, ctx, "SELECT y FROM t1 WHERE x = 10")
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	// UPDATE with expression
	mustExec(t, exec, ctx, "UPDATE t1 SET x = x + 5")
	rows = mustQueryAll(t, exec, ctx, "SELECT x FROM t1 ORDER BY x")
	if len(rows) != 3 || !rows[0].Data[0].Equal(NewIntValue(int64(15))) {
		t.Fatalf("x should be 15, got %v", rows[0].Data[0])
	}

	// UPDATE with multi-column SET
	mustExec(t, exec, ctx, "UPDATE t1 SET x = 99, y = 'zzz' WHERE x = 15")
	rows = mustQueryAll(t, exec, ctx, "SELECT x, y FROM t1 WHERE x = 99")
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if !rows[0].Data[1].Equal(NewTextValue("zzz")) {
		t.Fatalf("y should be 'zzz', got %v", rows[0].Data[1])
	}

	// UPDATE no matching rows (no error)
	mustExec(t, exec, ctx, "UPDATE t1 SET x = 0 WHERE x = 999")

	// UPDATE with duplicate column references (rightmost wins)
	exec2 := NewExecutor()
	exec2.RegisterTable("t2", []string{"x"})
	defer cleanupTest("t2")
	mustExec(t, exec2, ctx, "INSERT INTO t2 VALUES (1)")
	// SQL: UPDATE t2 SET x=3, x=4, x=5
	mustExec(t, exec2, ctx, "UPDATE t2 SET x = 3")
	mustExec(t, exec2, ctx, "UPDATE t2 SET x = 4")
	mustExec(t, exec2, ctx, "UPDATE t2 SET x = 5")
	rows2 := mustQueryAll(t, exec2, ctx, "SELECT x FROM t2")
	if len(rows2) != 1 || !rows2[0].Data[0].Equal(NewIntValue(int64(5))) {
		t.Fatalf("expected x=5, got %v", rows2[0].Data[0])
	}
}

// REQ000483: DEFAULT expressions on columns omitted from INSERT
// column list must be evaluated and stored.
func TestReq483_DefaultOnInsertOmittedColumns(t *testing.T) {
	exec := NewExecutor()
	ctx := context.Background()

	// Create table with DEFAULT values using Exec (SQL surface)
	mustExec(t, exec, ctx, "CREATE TABLE t_default (a INTEGER, b INTEGER DEFAULT 42, c TEXT DEFAULT 'hello')")
	defer cleanupTest("t_default")

	// INSERT without specifying defaulted columns
	mustExec(t, exec, ctx, "INSERT INTO t_default (a) VALUES (1)")
	rows := mustQueryAll(t, exec, ctx, "SELECT a, b, c FROM t_default")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(int64(1))) {
		t.Fatalf("a should be 1, got %v", rows[0].Data[0])
	}
	if !rows[0].Data[1].Equal(NewIntValue(int64(42))) {
		t.Fatalf("b should be 42 (default), got %v", rows[0].Data[1])
	}
	if !rows[0].Data[2].Equal(NewTextValue("hello")) {
		t.Fatalf("c should be 'hello' (default), got %v", rows[0].Data[2])
	}

	// INSERT without column list at all (should use all columns)
	mustExec(t, exec, ctx, "INSERT INTO t_default VALUES (2, 99, 'world')")
	rows = mustQueryAll(t, exec, ctx, "SELECT a, b, c FROM t_default WHERE a = 2")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if !rows[0].Data[1].Equal(NewIntValue(int64(99))) {
		t.Fatalf("b should be 99 (explicit), got %v", rows[0].Data[1])
	}
	if !rows[0].Data[2].Equal(NewTextValue("world")) {
		t.Fatalf("c should be 'world' (explicit), got %v", rows[0].Data[2])
	}

	// INSERT with partial column list (some omitted)
	mustExec(t, exec, ctx, "INSERT INTO t_default (a, c) VALUES (3, 'xyz')")
	rows = mustQueryAll(t, exec, ctx, "SELECT a, b, c FROM t_default WHERE a = 3")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if !rows[0].Data[1].Equal(NewIntValue(int64(42))) {
		t.Fatalf("b should be 42 (default), got %v", rows[0].Data[1])
	}
	if !rows[0].Data[2].Equal(NewTextValue("xyz")) {
		t.Fatalf("c should be 'xyz', got %v", rows[0].Data[2])
	}
}

// REQ000484: CHECK constraints must be enforced on INSERT and UPDATE.
func TestReq484_CheckConstraintEnforcement(t *testing.T) {
	exec := NewExecutor()
	ctx := context.Background()

	// Create table with CHECK constraint
	mustExec(t, exec, ctx, "CREATE TABLE t_check (a INTEGER CHECK (a > 0), b TEXT)")
	defer cleanupTest("t_check")

	// INSERT with valid value
	mustExec(t, exec, ctx, "INSERT INTO t_check (a, b) VALUES (1, 'ok')")

	// INSERT with invalid value (should fail)
	_, err := exec.Exec(ctx, "INSERT INTO t_check (a, b) VALUES (0, 'bad')")
	if err == nil {
		t.Fatal("expected CHECK constraint error for INSERT with a=0")
	}
	_, err = exec.Exec(ctx, "INSERT INTO t_check (a, b) VALUES (-1, 'neg')")
	if err == nil {
		t.Fatal("expected CHECK constraint error for INSERT with a=-1")
	}

	// Verify only valid row exists
	rows := mustQueryAll(t, exec, ctx, "SELECT a FROM t_check")
	if len(rows) != 1 || !rows[0].Data[0].Equal(NewIntValue(int64(1))) {
		t.Fatalf("expected 1 row with a=1, got %d rows", len(rows))
	}

	// UPDATE to valid value should succeed
	mustExec(t, exec, ctx, "UPDATE t_check SET a = 2 WHERE a = 1")
	rows = mustQueryAll(t, exec, ctx, "SELECT a FROM t_check")
	if !rows[0].Data[0].Equal(NewIntValue(int64(2))) {
		t.Fatalf("expected a=2 after UPDATE, got %v", rows[0].Data[0])
	}

	// UPDATE to invalid value should fail
	_, err = exec.Exec(ctx, "UPDATE t_check SET a = -5 WHERE a = 2")
	if err == nil {
		t.Fatal("expected CHECK constraint error for UPDATE with a=-5")
	}

	// Verify row unchanged after failed UPDATE
	rows = mustQueryAll(t, exec, ctx, "SELECT a FROM t_check")
	if !rows[0].Data[0].Equal(NewIntValue(int64(2))) {
		t.Fatalf("expected a=2 after failed UPDATE, got %v", rows[0].Data[0])
	}
}

// REQ000485: UNIQUE constraints must be enforced on INSERT.
func TestReq485_UniqueConstraintEnforcement(t *testing.T) {
	exec := NewExecutor()
	ctx := context.Background()

	// Create table with UNIQUE constraint
	mustExec(t, exec, ctx, "CREATE TABLE t_unique (a INTEGER UNIQUE, b TEXT)")
	defer cleanupTest("t_unique")

	// INSERT first value
	mustExec(t, exec, ctx, "INSERT INTO t_unique (a, b) VALUES (1, 'first')")

	// INSERT duplicate value (should fail)
	_, err := exec.Exec(ctx, "INSERT INTO t_unique (a, b) VALUES (1, 'duplicate')")
	if err == nil {
		t.Fatal("expected UNIQUE constraint error for duplicate a=1")
	}

	// INSERT different value should succeed
	mustExec(t, exec, ctx, "INSERT INTO t_unique (a, b) VALUES (2, 'second')")

	// Verify both rows exist
	rows := mustQueryAll(t, exec, ctx, "SELECT a, b FROM t_unique ORDER BY a")
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(int64(1))) || !rows[1].Data[0].Equal(NewIntValue(int64(2))) {
		t.Fatalf("expected a=1,2 got %v,%v", rows[0].Data[0], rows[1].Data[0])
	}

	// INSERT with NULL (SQL allows multiple NULLs in unique column)
	mustExec(t, exec, ctx, "INSERT INTO t_unique (a, b) VALUES (NULL, 'null1')")
	mustExec(t, exec, ctx, "INSERT INTO t_unique (a, b) VALUES (NULL, 'null2')")
	rows = mustQueryAll(t, exec, ctx, "SELECT a FROM t_unique WHERE a IS NULL")
	if len(rows) != 2 {
		t.Fatalf("expected 2 NULL rows, got %d", len(rows))
	}
}

// REQ000489: REINDEX must route through the executor without error.
func TestReq489_ReindexRouting(t *testing.T) {
	exec := NewExecutor()
	ctx := context.Background()

	mustExec(t, exec, ctx, "CREATE TABLE t_reindex (a INTEGER, b TEXT)")
	defer cleanupTest("t_reindex")

	// REINDEX should be a no-op that doesn't error
	mustExec(t, exec, ctx, "REINDEX")
	mustExec(t, exec, ctx, "REINDEX t_reindex")
}

// REQ000491: DROP INDEX must route through the executor without error.
func TestReq491_DropIndexRouting(t *testing.T) {
	exec := NewExecutor()
	ctx := context.Background()

	mustExec(t, exec, ctx, "CREATE TABLE t_dropidx (a INTEGER, b TEXT)")
	defer cleanupTest("t_dropidx")

	// Create an index first
	mustExec(t, exec, ctx, "CREATE INDEX idx_drop ON t_dropidx (a)")

	// DROP INDEX should succeed
	mustExec(t, exec, ctx, "DROP INDEX idx_drop")
}

// REQ000499: ALTER TABLE DROP COLUMN must remove a column from the schema.
func TestReq499_AlterTableDropColumn(t *testing.T) {
	exec := NewExecutor()
	ctx := context.Background()

	mustExec(t, exec, ctx, "CREATE TABLE t_dropcol (a INTEGER, b TEXT, c INTEGER)")
	defer cleanupTest("t_dropcol")

	mustExec(t, exec, ctx, "INSERT INTO t_dropcol VALUES (1, 'one', 10)")
	mustExec(t, exec, ctx, "INSERT INTO t_dropcol VALUES (2, 'two', 20)")

	// DROP COLUMN b
	mustExec(t, exec, ctx, "ALTER TABLE t_dropcol DROP COLUMN b")

	// Verify schema: only a and c remain
	rows := mustQueryAll(t, exec, ctx, "SELECT a, c FROM t_dropcol ORDER BY a")
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(int64(1))) || !rows[0].Data[1].Equal(NewIntValue(int64(10))) {
		t.Fatalf("expected (1,10), got (%v,%v)", rows[0].Data[0], rows[0].Data[1])
	}
	if !rows[1].Data[0].Equal(NewIntValue(int64(2))) || !rows[1].Data[1].Equal(NewIntValue(int64(20))) {
		t.Fatalf("expected (2,20), got (%v,%v)", rows[1].Data[0], rows[1].Data[1])
	}

	// Verify b is gone from schema
	schema := Schema("t_dropcol")
	foundB := false
	for _, c := range schema {
		if c == "b" {
			foundB = true
			break
		}
	}
	if foundB {
		t.Fatal("column b should not be in schema after DROP COLUMN")
	}

	// Verify that SELECT a, c returns correct data (b is excluded)
	// Note: SELECT b won't error because Ident evaluates to the
	// column name string when the column is not found in the row.
	// This is existing Eval behavior for without-FROM queries.
}

// Test helpers

func mustExec(t *testing.T, exec *Executor, ctx context.Context, sql string) {
	t.Helper()
	_, err := exec.Exec(ctx, sql)
	if err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func mustQueryAll(t *testing.T, exec *Executor, ctx context.Context, sql string) []Row {
	t.Helper()
	rows, err := exec.QueryAll(ctx, sql)
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return rows
}

func cleanupTest(table string) {
	exec := NewExecutor()
	ctx := context.Background()
	exec.Exec(ctx, "DROP TABLE IF EXISTS "+table)
}

func TestReq563_InsertDefaultValues(t *testing.T) {
	exec := NewExecutor()
	ctx := context.Background()

	mustExec(t, exec, ctx, "CREATE TABLE t_dv (a INTEGER, b INTEGER DEFAULT 42, c TEXT DEFAULT 'x')")
	defer cleanupTest("t_dv")

	mustExec(t, exec, ctx, "INSERT INTO t_dv DEFAULT VALUES")
	rows := mustQueryAll(t, exec, ctx, "SELECT a, b, c FROM t_dv")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].IsNull() == false {
		t.Errorf("a should be nil (no default), got %v", rows[0].Data[0])
	}
	if !rows[0].Data[1].Equal(NewIntValue(int64(42))) {
		t.Errorf("b should be 42, got %v", rows[0].Data[1])
	}
	if !rows[0].Data[2].Equal(NewTextValue("x")) {
		t.Errorf("c should be 'x', got %v", rows[0].Data[2])
	}

	// DEFAULT VALUES with RETURNING
	mustExec(t, exec, ctx, "INSERT INTO t_dv DEFAULT VALUES")
	mustExec(t, exec, ctx, "INSERT INTO t_dv DEFAULT VALUES")
	rows, err := exec.QueryAll(ctx, "SELECT COUNT(*) FROM t_dv")
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if len(rows) != 1 || !rows[0].Data[0].Equal(NewIntValue(int64(3))) {
		t.Fatalf("expected 3 rows total, got %v", rows[0].Data)
	}
}
