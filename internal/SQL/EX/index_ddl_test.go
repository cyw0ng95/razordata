package EX

import (
	"bytes"
	"context"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

func TestCreateIndex_Registers(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "email"}, "id")

	_, err := ex.Exec(context.Background(),
		"CREATE INDEX idx_email ON t (email)")
	if err != nil {
		t.Fatalf("CREATE INDEX: %v", err)
	}

	idxs := GetRegisteredIndexes("t")
	if len(idxs) != 1 {
		t.Fatalf("got %d indexes, want 1", len(idxs))
	}
	if idxs[0].Name != "idx_email" {
		t.Errorf("Name = %q, want idx_email", idxs[0].Name)
	}
	if !hasWriterIndex("t", "idx_email") {
		t.Error("hasWriterIndex should return true")
	}
}

func TestCreateIndex_PopulatesOnInsert(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "email"}, "id")

	_, err := ex.Exec(context.Background(),
		"CREATE INDEX idx_email ON t (email)")
	if err != nil {
		t.Fatalf("CREATE INDEX: %v", err)
	}

	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 'alice@x.com')",
		"INSERT INTO t VALUES (2, 'bob@x.com')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	// Verify the index can be used to seek
	rows, err := ex.QueryAll(ctx, "SELECT id FROM t WHERE email = 'bob@x.com'")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0] != int64(2) {
		t.Errorf("id = %v, want 2", rows[0].Data[0])
	}
}

func TestCreateIndex_Duplicate(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")

	if _, err := ex.Exec(context.Background(), "CREATE INDEX idx_a ON t (a)"); err != nil {
		t.Fatal(err)
	}
	// Second create should not error at the EX layer (catalog
	// is in-memory and not wired). It just appends.
	_, err := ex.Exec(context.Background(), "CREATE INDEX idx_a ON t (a)")
	if err != nil {
		t.Errorf("second CREATE INDEX (in-memory mode): %v", err)
	}
}

func TestDropIndex_RemovesFromRegistry(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE INDEX idx_a ON t (a)"); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Exec(ctx, "DROP INDEX idx_a"); err != nil {
		t.Fatal(err)
	}
	if hasWriterIndex("t", "idx_a") {
		t.Error("after DROP INDEX, hasWriterIndex should return false")
	}
}

func TestDropIndex_FullFlow(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE INDEX idx_a ON t (a)"); err != nil {
		t.Fatal(err)
	}
	_, _ = ex.Exec(ctx, "INSERT INTO t VALUES (1, 'x')")

	// Drop the index
	if _, err := ex.Exec(ctx, "DROP INDEX idx_a"); err != nil {
		t.Fatal(err)
	}

	// New inserts should not maintain the (now-dropped) index
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (2, 'y')"); err != nil {
		t.Fatal(err)
	}

	// The planner should now fall back to prefix scan (since the
	// index is no longer registered for maintenance)
	rows, err := ex.QueryAll(ctx, "SELECT id FROM t WHERE a = 'x'")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
}

// guard against unused import warning
var _ = bytes.NewBuffer
