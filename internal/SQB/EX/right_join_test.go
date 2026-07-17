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