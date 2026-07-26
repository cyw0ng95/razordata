package EX

import (
	"context"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

// TestIndex_EndToEnd exercises the full iter-22 secondary-index
// flow: CREATE INDEX, INSERT, query via index, UPDATE, DELETE.
func TestIndex_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("users", []string{"id", "email", "name"}, "id")

	ctx := context.Background()

	// 1. Create a secondary index on email
	if _, err := ex.Exec(ctx, "CREATE INDEX idx_email ON users (email)"); err != nil {
		t.Fatalf("CREATE INDEX: %v", err)
	}

	// 2. Insert rows — index should be populated automatically
	for _, s := range []string{
		"INSERT INTO users VALUES (1, 'alice@example.com', 'Alice')",
		"INSERT INTO users VALUES (2, 'bob@example.com', 'Bob')",
		"INSERT INTO users VALUES (3, 'carol@example.com', 'Carol')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("INSERT: %v", err)
		}
	}

	// 3. Query using the index — should use real seek
	rows, err := ex.QueryAll(ctx, "SELECT id FROM users WHERE email = 'bob@example.com'")
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(int64(2))) {
		t.Errorf("id = %v, want 2", rows[0].Data[0])
	}

	// 4. Query a non-existent value
	rows, err = ex.QueryAll(ctx, "SELECT id FROM users WHERE email = 'nobody@x.com'")
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}

	// 5. UPDATE — change bob's email, verify old index entry is removed
	if _, err := ex.Exec(ctx,
		"UPDATE users SET email = 'robert@example.com' WHERE id = 2"); err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	rows, err = ex.QueryAll(ctx, "SELECT id FROM users WHERE email = 'bob@example.com'")
	if err != nil {
		t.Fatalf("QueryAll after UPDATE: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("after UPDATE, bob should not be findable by old email")
	}
	rows, err = ex.QueryAll(ctx, "SELECT id FROM users WHERE email = 'robert@example.com'")
	if err != nil {
		t.Fatalf("QueryAll after UPDATE: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("after UPDATE, robert should be findable by new email")
	}

	// 6. DELETE — remove alice, verify index entry is removed
	if _, err := ex.Exec(ctx, "DELETE FROM users WHERE id = 1"); err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	rows, err = ex.QueryAll(ctx, "SELECT id FROM users WHERE email = 'alice@example.com'")
	if err != nil {
		t.Fatalf("QueryAll after DELETE: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("after DELETE, alice should not be findable")
	}

	// 7. DROP INDEX — index should be removed
	if _, err := ex.Exec(ctx, "DROP INDEX idx_email"); err != nil {
		t.Fatalf("DROP INDEX: %v", err)
	}
	if hasWriterIndex("users", "idx_email") {
		t.Error("after DROP INDEX, hasWriterIndex should be false")
	}

	// 8. After DROP, queries should still work via fallback
	rows, err = ex.QueryAll(ctx, "SELECT id FROM users WHERE email = 'robert@example.com'")
	if err != nil {
		t.Fatalf("QueryAll after DROP: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("after DROP INDEX, fallback should still find robert, got %d rows", len(rows))
	}
}

// TestIndex_NotFound verifies that looking up a missing index
// value returns no rows.
func TestIndex_NotFound(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE INDEX idx_a ON t (a)"); err != nil {
		t.Fatal(err)
	}
	_, _ = ex.Exec(ctx, "INSERT INTO t VALUES (1, 'x')")

	rows, err := ex.QueryAll(ctx, "SELECT id FROM t WHERE a = 'nope'")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for missing key, got %d", len(rows))
	}
}

// TestIndex_NumericValue verifies that integer keys are
// encoded correctly.
func TestIndex_NumericValue(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "score"}, "id")

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE INDEX idx_score ON t (score)"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 100)",
		"INSERT INTO t VALUES (2, 200)",
		"INSERT INTO t VALUES (3, 100)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT id FROM t WHERE score = 100")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows (id=1 and id=3), got %d", len(rows))
	}
}
