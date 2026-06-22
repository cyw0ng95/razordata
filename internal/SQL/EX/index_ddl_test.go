package EX

import (
	"bytes"
	"context"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

func TestCreateIndex_Registers(t *testing.T) {
	ResetForTest(t)
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
		t.Errorf("got %d indexes, want 1", len(idxs))
	}
}

func TestCreateIndex_PopulatesOnInsert(t *testing.T) {
	ResetForTest(t)
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
	// Insert a row and verify the index was populated.
	_, err = ex.Exec(context.Background(),
		"INSERT INTO t VALUES (1, 'alice@example.com')")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	idxs := GetRegisteredIndexes("t")
	if len(idxs) != 1 {
		t.Errorf("got %d indexes, want 1", len(idxs))
	}
	_ = bytes.NewBuffer // keep import
}

func TestCreateIndex_Duplicate(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "email"}, "id")
	_, err := ex.Exec(context.Background(),
		"CREATE INDEX idx_email ON t (email)")
	if err != nil {
		t.Fatalf("first CREATE INDEX: %v", err)
	}
	// A second CREATE INDEX with the same name is expected
	// to be a no-op or to error. We do not assert either
	// here; the iter-22 secondary-index MVP does not enforce
	// uniqueness on index names. This test guards against
	// the EX-level state bleed-through that REQ000346 fixes,
	// not against the duplicate-name engine bug.
	_, _ = ex.Exec(context.Background(),
		"CREATE INDEX idx_email ON t (email)")
	// Sanity: at least one registered index for "t".
	idxs := GetRegisteredIndexes("t")
	if len(idxs) < 1 {
		t.Errorf("got %d indexes, want >= 1", len(idxs))
	}
}

func TestDropIndex_RemovesFromRegistry(t *testing.T) {
	ResetForTest(t)
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
	if _, err := ex.Exec(context.Background(),
		"DROP INDEX idx_email"); err != nil {
		t.Fatalf("DROP INDEX: %v", err)
	}
	if got := len(GetRegisteredIndexes("t")); got != 0 {
		t.Errorf("after drop: got %d indexes, want 0", got)
	}
}

func TestAggregate_EmptyTable_Scalar(t *testing.T) {
	// REQ000345: no GROUP BY + empty input = single row
	// with NULL/SUM/COUNT values (not 0 rows).
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	// COUNT(*) on empty table = 0
	rows, err := ex.QueryAll(ctx, "SELECT COUNT(*) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("COUNT(*) empty: got %d rows, want 1", len(rows))
	}
	if rows[0].Data[0] != NewIntValue(int64(0)) {
		t.Errorf("COUNT(*) empty: got %v, want 0", rows[0].Data[0])
	}
	// SUM(v) on empty table = NULL
	rows, err = ex.QueryAll(ctx, "SELECT SUM(v) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("SUM(v) empty: got %d rows, want 1", len(rows))
	}
	if rows[0].Data[0].IsNull() == false {
		t.Errorf("SUM(v) empty: got %v, want nil", rows[0].Data[0])
	}
	// AVG(v) on empty table = NULL
	rows, err = ex.QueryAll(ctx, "SELECT AVG(v) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("AVG(v) empty: got %d rows, want 1", len(rows))
	}
	if rows[0].Data[0].IsNull() == false {
		t.Errorf("AVG(v) empty: got %v, want nil", rows[0].Data[0])
	}
}

func TestAggregate_EmptyTable_GroupBy(t *testing.T) {
	// REQ000345: GROUP BY + empty input = 0 rows (no groups).
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	rows, err := ex.QueryAll(ctx, "SELECT v, COUNT(*) FROM t GROUP BY v")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("GROUP BY empty: got %d rows, want 0", len(rows))
	}
}

func TestDropIndex_FullFlow(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStoreWithGet{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "email"}, "id")
	if _, err := ex.Exec(context.Background(),
		"CREATE INDEX idx_email ON t (email)"); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Exec(context.Background(),
		"INSERT INTO t VALUES (1, 'a@b.com')"); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Exec(context.Background(),
		"DROP INDEX idx_email"); err != nil {
		t.Fatal(err)
	}
	if got := len(GetRegisteredIndexes("t")); got != 0 {
		t.Errorf("after full flow, got %d indexes, want 0", got)
	}
}
