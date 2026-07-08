package EX

import (
	"context"
	"testing"
)

// TestNaturalJoin_Inner_Basic verifies REQ001359: NATURAL JOIN
// synthesizes an equi-join ON clause from common column names.
func TestNaturalJoin_Inner_Basic(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE nat_a (id INT, name TEXT, val INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE nat_b (id INT, name TEXT, extra INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO nat_a VALUES (1, 'a', 10), (2, 'b', 20)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO nat_b VALUES (1, 'a', 100), (2, 'b', 200)"); err != nil {
		t.Fatal(err)
	}

	// NATURAL JOIN should synthesize ON a.id=b.id AND a.name=b.name.
	rows, err := e.QueryAll(ctx, "SELECT nat_a.val, nat_b.extra FROM nat_a NATURAL JOIN nat_b ORDER BY nat_a.id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Data[0].I64 != 10 || rows[0].Data[1].I64 != 100 {
		t.Errorf("row 0: got %d/%d, want 10/100", rows[0].Data[0].I64, rows[0].Data[1].I64)
	}
	if rows[1].Data[0].I64 != 20 || rows[1].Data[1].I64 != 200 {
		t.Errorf("row 1: got %d/%d, want 20/200", rows[1].Data[0].I64, rows[1].Data[1].I64)
	}
}

// TestNaturalJoin_NoCommonCols_Exec verifies NATURAL JOIN on tables
// with no common columns produces a cross-join (no ON clause).
func TestNaturalJoin_NoCommonCols_Exec(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE nat_c (a INT, b INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE nat_d (x INT, y INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO nat_c VALUES (1, 2)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO nat_d VALUES (3, 4)"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.QueryAll(ctx, "SELECT * FROM nat_c NATURAL JOIN nat_d")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (cross join), got %d", len(rows))
	}
}