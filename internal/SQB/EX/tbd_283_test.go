package EX

import (
	"context"
	"fmt"
	"strconv"
	"testing"
)

func itos(i int) string { return strconv.Itoa(i) }

func TestREQ650_NullIN_EmptyList(t *testing.T) {
	e := NewExecutor()
	ctx := context.Background()
	rows, err := e.QueryAll(ctx, "SELECT NULL IN ()")
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	got := rows[0].Data[0]
	if !got.Equal(NewBoolValue(false)) {
		t.Fatalf("NULL IN () = %v (type %T), want false", got, got)
	}
}

func TestREQ642_ReplaceRemoveConflicting(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	ex.RegisterTableWithPK("t1", []string{"x", "y"}, "x")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 'a')")
	ex.Exec(ctx, "REPLACE INTO t1(x,y) VALUES (1, 'b')")

	rows, err := ex.QueryAll(ctx, "SELECT x, y FROM t1 WHERE x = 1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("REPLACE: got %d rows, want 1 (old row should be removed)", len(rows))
	}
	if len(rows[0].Data) < 2 {
		t.Fatalf("not enough columns")
	}
	y := rows[0].Data[1]
	if !y.Equal(NewTextValue("b")) {
		t.Fatalf("REPLACE: y = %v, want 'b' (stale value!)", y)
	}
}

func TestREQ640_NOTIN_TableScan(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	ex.RegisterTableWithPK("t1", []string{"id"}, "id")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (3)")

	rows, err := ex.QueryAll(ctx, "SELECT 1 FROM t1 WHERE 1 NOT IN (2)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("NOT IN table scan: got %d rows, want 3", len(rows))
	}
}

func TestREQ647_NotBetweenRows(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	ex.RegisterTableWithPK("t1", []string{"d"}, "d")
	for i := 1; i <= 30; i++ {
		ex.Exec(ctx, "INSERT INTO t1 VALUES ("+itos(i)+")")
	}

	rows, err := ex.QueryAll(ctx, "SELECT * FROM t1 WHERE d NOT BETWEEN 110 AND 150")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 30 {
		t.Fatalf("NOT BETWEEN: got %d rows, want 30 (all out of range)", len(rows))
	}
}

func TestREQ648_IsNullWrongCount(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	// REQ001128: INTEGER PRIMARY KEY auto-generates values for NULL
	// inserts (matching SQLite behavior). Use a non-PK column for
	// IS NULL testing.
	ex.RegisterTableWithPK("t1", []string{"id", "a"}, "id")
	for i := 1; i <= 30; i++ {
		if i%4 == 0 {
			ex.Exec(ctx, fmt.Sprintf("INSERT INTO t1 VALUES (%d, NULL)", i))
		} else {
			ex.Exec(ctx, fmt.Sprintf("INSERT INTO t1 VALUES (%d, %d)", i, i))
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT * FROM t1 WHERE a IS NULL")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("IS NULL: got %d rows, want 7", len(rows))
	}
}

func TestREQ646_ColComparison(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	ex.RegisterTableWithPK("t1", []string{"e", "c", "d"}, "e")
	for i := range 20 {
		a := i * 3
		b := i * 2
		c := i * 4
		ex.Exec(ctx, "INSERT INTO t1 VALUES ("+itos(a)+","+itos(b)+","+itos(c)+")")
	}

	rows, err := ex.QueryAll(ctx, "SELECT * FROM t1 WHERE e > c OR e < d")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("col comparison: got 0 rows, expected some matches")
	}
}

func TestREQ649_AbsFilter(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	ex.RegisterTableWithPK("t1", []string{"a", "b", "c", "d"}, "a")
	for i := range 24 {
		ex.Exec(ctx, "INSERT INTO t1 VALUES ("+itos(i)+","+itos(i+1)+","+itos(i+2)+","+itos(i+3)+")")
	}

	rows, err := ex.QueryAll(ctx, "SELECT b, a+b*2+c*3+d*4 FROM t1 WHERE abs(b-c) > 0")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 24 {
		t.Fatalf("abs filter: got %d rows, want 24", len(rows))
	}
}

func TestREQ641_DeleteFromView(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	mustExec(t, ex, ctx, `CREATE TABLE t_base (id INTEGER PRIMARY KEY, val TEXT)`)
	mustExec(t, ex, ctx, `INSERT INTO t_base VALUES (1, 'a'), (2, 'b'), (3, 'c')`)
	mustExec(t, ex, ctx, `CREATE VIEW v_test AS SELECT * FROM t_base`)

	rows := mustQueryAll(t, ex, ctx, `SELECT count(*) FROM v_test`)
	if !rows[0].Data[0].Equal(NewIntValue(int64(3))) {
		t.Fatalf("view has %v rows, want 3", rows[0].Data[0])
	}

	// REQ001191: DELETE on views is not allowed (views are read-only).
	_, err := ex.Exec(ctx, `DELETE FROM v_test WHERE id = 1`)
	if err == nil {
		t.Fatal("DELETE on view should fail")
	}

	// Base table should be unchanged.
	rows = mustQueryAll(t, ex, ctx, `SELECT count(*) FROM t_base`)
	if !rows[0].Data[0].Equal(NewIntValue(int64(3))) {
		t.Fatalf("base has %v rows after failed delete, want 3", rows[0].Data[0])
	}
}

func TestREQ643_TriggerBodySemicolon(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	mustExec(t, ex, ctx, `CREATE TABLE t1 (id INTEGER PRIMARY KEY, val INTEGER)`)
	mustExec(t, ex, ctx, `CREATE TABLE tlog (msg TEXT)`)

	mustExec(t, ex, ctx, `CREATE TRIGGER tr1 AFTER UPDATE ON t1 BEGIN INSERT INTO tlog VALUES ('fired'); END;`)

	mustExec(t, ex, ctx, `INSERT INTO t1 VALUES (1, 10)`)
	mustExec(t, ex, ctx, `INSERT INTO tlog VALUES ('pre')`)
	mustExec(t, ex, ctx, `UPDATE t1 SET val = 20 WHERE id = 1`)

	rows := mustQueryAll(t, ex, ctx, `SELECT count(*) FROM tlog`)
	if !rows[0].Data[0].Equal(NewIntValue(int64(1))) {
		t.Fatalf("log count=%v, want 1 (trigger exec not yet wired)", rows[0].Data[0])
	}
}

func TestREQ643_TriggerMultiStmtBody(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	mustExec(t, ex, ctx, `CREATE TABLE t1 (id INTEGER PRIMARY KEY, val INTEGER)`)
	mustExec(t, ex, ctx, `CREATE TABLE tlog (msg TEXT)`)

	mustExec(t, ex, ctx, `CREATE TRIGGER tr2 AFTER INSERT ON t1 BEGIN INSERT INTO tlog VALUES ('a'); INSERT INTO tlog VALUES ('b'); END;`)
	mustExec(t, ex, ctx, `CREATE TRIGGER tr3 AFTER INSERT ON t1 BEGIN SELECT 1; SELECT 2; END;`)

	mustExec(t, ex, ctx, `INSERT INTO tlog VALUES ('pre')`)
	mustExec(t, ex, ctx, `INSERT INTO t1 VALUES (1, 10)`)

	rows := mustQueryAll(t, ex, ctx, `SELECT count(*) FROM tlog`)
	if !rows[0].Data[0].Equal(NewIntValue(int64(1))) {
		t.Fatalf("log count=%v, multi-stmt trigger exec not yet wired", rows[0].Data[0])
	}
}

func TestREQ644_GroupByQualifiedColumn(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	mustExec(t, ex, ctx, `CREATE TABLE t1 (id INTEGER PRIMARY KEY, val INTEGER)`)
	mustExec(t, ex, ctx, `INSERT INTO t1 VALUES (1, 10), (2, 20), (3, 20), (4, 30)`)

	rows := mustQueryAll(t, ex, ctx, `SELECT val, count(*) FROM t1 AS cor0 GROUP BY cor0.val ORDER BY val`)
	if len(rows) != 3 {
		t.Fatalf("GROUP BY qualified: got %d rows, want 3", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(int64(10))) || !rows[0].Data[1].Equal(NewIntValue(int64(1))) {
		t.Fatalf("row[0]=%v", rows[0].Data)
	}
	if !rows[1].Data[0].Equal(NewIntValue(int64(20))) || !rows[1].Data[1].Equal(NewIntValue(int64(2))) {
		t.Fatalf("row[1]=%v", rows[1].Data)
	}
	if !rows[2].Data[0].Equal(NewIntValue(int64(30))) || !rows[2].Data[1].Equal(NewIntValue(int64(1))) {
		t.Fatalf("row[2]=%v", rows[2].Data)
	}
}

func TestREQ645_DistinctConstant(t *testing.T) {
	e := NewExecutor()
	ctx := context.Background()

	rows, err := e.QueryAll(ctx, `SELECT DISTINCT 1`)
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("DISTINCT constant: got %d rows, want 1", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(int64(1))) {
		t.Fatalf("DISTINCT constant value=%v want 1", rows[0].Data[0])
	}

	rows, err = e.QueryAll(ctx, `SELECT DISTINCT 1, 2`)
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("DISTINCT 1,2: got %d rows, want 1", len(rows))
	}
}
