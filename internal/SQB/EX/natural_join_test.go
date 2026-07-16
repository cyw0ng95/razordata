package EX

import (
	"context"
	"strconv"
	"testing"
)

// TestNaturalJoin_Inner_Basic verifies REQ001359: NATURAL INNER JOIN
// synthesizes ON-clause on common column names.
func TestNaturalJoin_Inner_Basic(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE nj1 (id INT, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE nj2 (id INT, role TEXT)"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"INSERT INTO nj1 VALUES (1, 'alice')",
		"INSERT INTO nj1 VALUES (2, 'bob')",
		"INSERT INTO nj2 VALUES (1, 'admin')",
		"INSERT INTO nj2 VALUES (3, 'user')",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	// Common column: id. Natural join should join on nj1.id = nj2.id.
	rows, err := e.QueryAll(ctx, "SELECT nj1.name, nj2.role FROM nj1 NATURAL INNER JOIN nj2")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].S != "alice" || rows[0].Data[1].S != "admin" {
		t.Errorf("expected alice/admin, got %v/%v", rows[0].Data[0].S, rows[0].Data[1].S)
	}
}

// TestNaturalJoin_Default_Inner verifies REQ001359: bare NATURAL JOIN
// (no INNER keyword) is treated as INNER JOIN.
func TestNaturalJoin_Default_Inner(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE nj3 (x INT, y TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE nj4 (x INT, z TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO nj3 VALUES (1, 'y1')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO nj4 VALUES (1, 'z1')"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.QueryAll(ctx, "SELECT nj3.y, nj4.z FROM nj3 NATURAL JOIN nj4")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].S != "y1" || rows[0].Data[1].S != "z1" {
		t.Errorf("expected y1/z1, got %v/%v", rows[0].Data[0].S, rows[0].Data[1].S)
	}
}

// TestNaturalJoin_NoCommonCols_Cross verifies REQ001359: NATURAL JOIN
// with no common columns degrades to CROSS JOIN.
func TestNaturalJoin_NoCommonCols_Cross(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE nj5 (x INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE nj6 (y INT)"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if _, err := e.Exec(ctx, "INSERT INTO nj5 VALUES ("+strconv.Itoa(i)+")"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Exec(ctx, "INSERT INTO nj6 VALUES ("+strconv.Itoa(i)+")"); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT nj5.x, nj6.y FROM nj5 NATURAL JOIN nj6")
	if err != nil {
		t.Fatal(err)
	}
	// Cross of 2x2 = 4 rows.
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows (cross of 2x2), got %d", len(rows))
	}
}