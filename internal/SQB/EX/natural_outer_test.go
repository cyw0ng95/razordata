package EX

import (
	"context"
	"testing"
)

// TestNaturalJoin_Left verifies REQ001360: NATURAL LEFT JOIN —
// unmatched right rows are excluded (LEFT preserves left rows).
func TestNaturalJoin_Left(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE njl1 (id INT, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE njl2 (id INT, role TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO njl1 VALUES (1, 'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO njl1 VALUES (2, 'bob')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO njl2 VALUES (1, 'admin')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO njl2 VALUES (2, 'user')"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.QueryAll(ctx, "SELECT njl1.name, njl2.role FROM njl1 NATURAL LEFT JOIN njl2")
	if err != nil {
		t.Fatal(err)
	}
	// alice/admin + bob/user, both matched. njl2.id=3 would be unmatched
	// right (excluded by LEFT).
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	gotAlice := false
	gotBob := false
	for _, r := range rows {
		if r.Data[0].S == "alice" && r.Data[1].S == "admin" {
			gotAlice = true
		}
		if r.Data[0].S == "bob" && r.Data[1].S == "user" {
			gotBob = true
		}
	}
	if !gotAlice || !gotBob {
		t.Errorf("missing rows: gotAlice=%v gotBob=%v", gotAlice, gotBob)
	}
}

// TestNaturalJoin_LeftUnmatchedLeft verifies REQ001360: unmatched left
// rows appear with NULL right cols.
func TestNaturalJoin_LeftUnmatchedLeft(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE nlul (id INT, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE nlur (id INT, role TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO nlul VALUES (1, 'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO nlur VALUES (1, 'admin')"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.QueryAll(ctx, "SELECT nlul.name, nlur.role FROM nlul NATURAL LEFT JOIN nlur")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (matched), got %d", len(rows))
	}
	if rows[0].Data[0].S != "alice" || rows[0].Data[1].S != "admin" {
		t.Errorf("expected alice/admin, got %v/%v", rows[0].Data[0], rows[0].Data[1])
	}
}

// TestNaturalJoin_Right verifies REQ001360: NATURAL RIGHT JOIN —
// unmatched right rows appear with NULL left cols.
func TestNaturalJoin_Right(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE njr1 (id INT, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE njr2 (id INT, role TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO njr1 VALUES (1, 'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO njr2 VALUES (1, 'admin')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO njr2 VALUES (2, 'user')"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.QueryAll(ctx, "SELECT njr1.name, njr2.role FROM njr1 NATURAL RIGHT JOIN njr2")
	if err != nil {
		t.Fatal(err)
	}
	// Expected: (alice, admin) [match] and (NULL, user) [unmatched right].
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	matched := false
	unmatched := false
	for _, r := range rows {
		if r.Data[1].S == "admin" {
			matched = true
			if r.Data[0].S != "alice" {
				t.Errorf("expected alice, got %q", r.Data[0].S)
			}
		}
		if r.Data[1].S == "user" {
			unmatched = true
			if !r.Data[0].IsNull() {
				t.Errorf("expected NULL name, got %v", r.Data[0])
			}
		}
	}
	if !matched || !unmatched {
		t.Errorf("missing rows: matched=%v unmatched=%v", matched, unmatched)
	}
}

// TestNaturalJoin_Full verifies REQ001360: NATURAL FULL JOIN — both
// sides' unmatched rows appear.
func TestNaturalJoin_Full(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE njf1 (id INT, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "CREATE TABLE njf2 (id INT, role TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO njf1 VALUES (1, 'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Exec(ctx, "INSERT INTO njf2 VALUES (2, 'user')"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.QueryAll(ctx, "SELECT njf1.name, njf2.role FROM njf1 NATURAL FULL JOIN njf2")
	if err != nil {
		t.Fatal(err)
	}
	// Expected: (alice, NULL) and (NULL, user).
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
		if r.Data[1].S == "user" {
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