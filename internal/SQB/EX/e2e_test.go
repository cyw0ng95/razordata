package EX

import (
	"context"
	"testing"
)

// TestE2E_FullCRUD_AgainstEngine exercises a full SQL DML lifecycle
// against a real storage engine to validate that all paths (create,
// insert, query with filter/order/limit/offset, update, delete) work
// end-to-end.
func TestE2E_FullCRUD_AgainstEngine(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ex.RegisterTableWithPK("users", []string{"id", "name", "age"}, "id")
	ctx := context.Background()

	// CREATE TABLE via SQL DDL.
	if _, err := ex.Exec(ctx, "CREATE TABLE orders (id INTEGER, total INTEGER)"); err != nil {
		t.Fatal(err)
	}
	// Note: SQL CREATE TABLE does not currently take PK constraints; it
	// still registers the schema with no PK, so writes against it will
	// fall back to in-memory mode. We don't write to orders in this test.

	// INSERT three rows.
	for _, s := range []string{
		"INSERT INTO users VALUES (1, 'alice', 30)",
		"INSERT INTO users VALUES (2, 'bob', 25)",
		"INSERT INTO users VALUES (3, 'carol', 40)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}

	// SELECT *
	rows, err := ex.QueryAll(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	// SELECT with WHERE + ORDER BY + LIMIT.
	rows, err = ex.QueryAll(ctx, "SELECT name FROM users WHERE age > 25 ORDER BY age DESC LIMIT 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if !rows[0].Data[0].Equal(NewTextValue("carol")) || !rows[1].Data[0].Equal(NewTextValue("alice")) {
		t.Errorf("ORDER BY age DESC: got %v, want [carol alice]", rows)
	}

	// UPDATE
	res, err := ex.Exec(ctx, "UPDATE users SET age = 26 WHERE name = 'bob'")
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("update: expected 1 row affected, got %d", res.RowsAffected)
	}

	// Verify the update.
	rows, err = ex.QueryAll(ctx, "SELECT age FROM users WHERE name = 'bob'")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].Data[0].Equal(NewIntValue(int64(26))) {
		t.Errorf("update verification: got %v, want [26]", rows)
	}

	// DELETE
	res, err = ex.Exec(ctx, "DELETE FROM users WHERE id = 3")
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("delete: expected 1 row affected, got %d", res.RowsAffected)
	}

	// Verify the delete.
	rows, err = ex.QueryAll(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows remaining, got %d", len(rows))
	}
}

// TestE2E_LimitOffset_AgainstEngine verifies LIMIT/OFFSET against the
// engine-backed scan.
func TestE2E_LimitOffset_AgainstEngine(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "name"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 'a')",
		"INSERT INTO t VALUES (2, 'b')",
		"INSERT INTO t VALUES (3, 'c')",
		"INSERT INTO t VALUES (4, 'd')",
		"INSERT INTO t VALUES (5, 'e')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT name FROM t ORDER BY id LIMIT 2 OFFSET 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	got := []string{rows[0].Data[0].ToAny().(string), rows[1].Data[0].ToAny().(string)}
	if got[0] != "c" || got[1] != "d" {
		t.Errorf("LIMIT 2 OFFSET 2: got %v, want [c d]", got)
	}
}
