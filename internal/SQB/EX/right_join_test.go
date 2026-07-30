package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestRightJoin_Planner verifies REQ001355: planner routes RIGHT JOIN
// through the NLJ right-outer path.
func TestRightJoin_Planner(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE rj1 (id INT, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE rj2 (id INT, role TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO rj1 VALUES (1, 'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO rj2 VALUES (1, 'admin')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO rj2 VALUES (2, 'user')"); err != nil {
		t.Fatal(err)
	}

	// RIGHT JOIN should return all rj2 rows; rj1 rows where id matches
	// contribute the name; unmatched rj2 rows get NULL for name.
	rows, err := e.QueryAll(ctx, "SELECT rj1.name, rj2.role FROM rj1 RIGHT JOIN rj2 ON rj1.id = rj2.id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// rj2.id=1 matches rj1, expect "alice" / "admin"
	// rj2.id=2 has no match in rj1, expect NULL / "user"
	gotAlice := false
	gotUser := false
	for _, r := range rows {
		if r.Data[1].S == "admin" {
			gotAlice = true
			if r.Data[0].Kind != DT.KindText {
				t.Errorf("expected TEXT name for matched row, got kind %v", r.Data[0].Kind)
			}
			if r.Data[0].S != "alice" {
				t.Errorf("expected alice, got %q", r.Data[0].S)
			}
		}
		if r.Data[1].S == "user" {
			gotUser = true
			if !r.Data[0].IsNull() {
				t.Errorf("expected NULL name for unmatched row, got %v", r.Data[0])
			}
		}
	}
	if !gotAlice || !gotUser {
		t.Errorf("missing rows: gotAlice=%v gotUser=%v", gotAlice, gotUser)
	}
}

// TestRightJoin_LeftEmpty verifies REQ001355: when the left side is
// empty, RIGHT JOIN returns all right rows with NULL left cols.
func TestRightJoin_LeftEmpty(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE le1 (id INT, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE le2 (id INT, role TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO le2 VALUES (1, 'admin')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO le2 VALUES (2, 'user')"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.QueryAll(ctx, "SELECT le1.name, le2.role FROM le1 RIGHT JOIN le2 ON le1.id = le2.id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if !r.Data[0].IsNull() {
			t.Errorf("expected NULL name when left is empty, got %v", r.Data[0])
		}
	}
}

// TestLeftJoin_RightEmpty verifies REQ002169: LEFT JOIN with an empty
// right table must return all left rows with NULL right columns instead
// of returning 0 rows.
func TestLeftJoin_RightEmpty(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE lje1 (id INT, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE lje2 (id INT, role TEXT)"); err != nil {
		t.Fatal(err)
	}
	// lje2 is created but no rows inserted (empty right table).
	if _, err := e.Exec(ctx, "INSERT INTO lje1 VALUES (1, 'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO lje1 VALUES (2, 'bob')"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.QueryAll(ctx, "SELECT lje1.name, lje2.role FROM lje1 LEFT JOIN lje2 ON lje1.id = lje2.id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (left rows with NULL role), got %d", len(rows))
	}
	for _, r := range rows {
		if !r.Data[1].IsNull() {
			t.Errorf("expected NULL role for unmatched row, got %v", r.Data[1])
		}
		if r.Data[0].S != "alice" && r.Data[0].S != "bob" {
			t.Errorf("unexpected name: %v", r.Data[0])
		}
	}
}

// TestRightJoin_LeftEmpty_BothSidesPopulated verifies the non-empty
// regression guard: nullLeftRow must still use rows[0].Cols when the
// left table has rows (the pre-REQ002166 path), so column names/types
// stay consistent with the populated case.
func TestRightJoin_LeftEmpty_BothSidesPopulated(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE bp1 (id INT, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE bp2 (id INT, role TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO bp1 VALUES (1, 'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO bp2 VALUES (1, 'admin')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO bp2 VALUES (2, 'user')"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.QueryAll(ctx, "SELECT bp1.name, bp2.role FROM bp1 RIGHT JOIN bp2 ON bp1.id = bp2.id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	gotAlice := false
	gotUser := false
	for _, r := range rows {
		if r.Data[1].S == "admin" {
			gotAlice = true
			if r.Data[0].S != "alice" {
				t.Errorf("expected alice for matched row, got %v", r.Data[0])
			}
		}
		if r.Data[1].S == "user" {
			gotUser = true
			if !r.Data[0].IsNull() {
				t.Errorf("expected NULL name for unmatched row, got %v", r.Data[0])
			}
		}
	}
	if !gotAlice || !gotUser {
		t.Errorf("missing rows: gotAlice=%v gotUser=%v", gotAlice, gotUser)
	}
}
