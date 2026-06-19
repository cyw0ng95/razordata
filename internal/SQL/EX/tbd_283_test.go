package EX

import (
	"context"
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
	if got != false {
		t.Fatalf("NULL IN () = %v (type %T), want false", got, got)
	}
}

func TestREQ642_ReplaceRemoveConflicting(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
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
	if y != "b" {
		t.Fatalf("REPLACE: y = %v, want 'b' (stale value!)", y)
	}
}

func TestREQ640_NOTIN_TableScan(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
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
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
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
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	ex.RegisterTableWithPK("t1", []string{"a"}, "a")
	for i := 1; i <= 30; i++ {
		if i%4 == 0 {
			ex.Exec(ctx, "INSERT INTO t1 VALUES (NULL)")
		} else {
			ex.Exec(ctx, "INSERT INTO t1 VALUES ("+itos(i)+")")
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
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
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
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
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
