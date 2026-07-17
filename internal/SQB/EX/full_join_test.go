package EX

import (
	"context"
	"testing"
)

// TestFullJoin_Planner verifies REQ001357: planner routes FULL JOIN
// through the NLJ full-outer path. Both sides' unmatched rows should
// appear with NULL columns on the other side.
func TestFullJoin_Planner(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE fj1 (id INT, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE fj2 (id INT, role TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO fj1 VALUES (1, 'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO fj2 VALUES (1, 'admin')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO fj2 VALUES (2, 'user')"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.QueryAll(ctx, "SELECT fj1.name, fj2.role FROM fj1 FULL JOIN fj2 ON fj1.id = fj2.id")
	if err != nil {
		t.Fatal(err)
	}
	// Expected: (alice, admin) [match] and (NULL, user) [unmatched right].
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	matched := false
	unmatchedRight := false
	for _, r := range rows {
		if r.Data[1].S == "admin" {
			matched = true
			if r.Data[0].S != "alice" {
				t.Errorf("expected alice, got %q", r.Data[0].S)
			}
		}
		if r.Data[1].S == "user" {
			unmatchedRight = true
			if !r.Data[0].IsNull() {
				t.Errorf("expected NULL name for unmatched right row, got %v", r.Data[0])
			}
		}
	}
	if !matched || !unmatchedRight {
		t.Errorf("missing rows: matched=%v unmatchedRight=%v", matched, unmatchedRight)
	}
}

// TestFullJoin_BothSidesUnmatched verifies REQ001357: unmatched rows
// from BOTH sides appear.
func TestFullJoin_BothSidesUnmatched(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE bs1 (id INT, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE bs2 (id INT, role TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO bs1 VALUES (1, 'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO bs2 VALUES (2, 'admin')"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.QueryAll(ctx, "SELECT bs1.name, bs2.role FROM bs1 FULL JOIN bs2 ON bs1.id = bs2.id")
	if err != nil {
		t.Fatal(err)
	}
	// Expected: (alice, NULL) and (NULL, admin).
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	gotLeft := false
	gotRight := false
	for _, r := range rows {
		if r.Data[0].S == "alice" {
			gotLeft = true
			if !r.Data[1].IsNull() {
				t.Errorf("expected NULL role, got %v", r.Data[1])
			}
		}
		if r.Data[1].S == "admin" {
			gotRight = true
			if !r.Data[0].IsNull() {
				t.Errorf("expected NULL name, got %v", r.Data[0])
			}
		}
	}
	if !gotLeft || !gotRight {
		t.Errorf("missing rows: gotLeft=%v gotRight=%v", gotLeft, gotRight)
	}
}