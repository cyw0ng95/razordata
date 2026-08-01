package EX

import (
	"context"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

// TestQP_Rewire_DMLSmoke is the integration backstop for REQ002267: it drives
// INSERT / INSERT...SELECT / UPDATE / DELETE / SELECT through the real
// executor (store-backed engine) and asserts the visible data is correct.
// This guards the store-aware lowering (PX.LowerQueryPlan) — the bug this
// rewire fixed wrote DML rows to the wrong (in-memory) storage, which showed
// up as MIN(a) == NULL / SELECT returning 0 rows after an INSERT.
func TestQP_Rewire_DMLSmoke(t *testing.T) {
	dir := t.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		t.Fatalf("ls.Open: %v", err)
	}
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ctx := context.Background()

	const create = "CREATE TABLE t_rw (id INTEGER PRIMARY KEY, a INTEGER, b TEXT)"
	if _, err := ex.Exec(ctx, create); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	// INSERT VALUES (multiple rows, one statement).
	if _, err := ex.Exec(ctx, "INSERT INTO t_rw VALUES (1, 10, 'x'), (2, 20, 'y'), (3, 30, 'z')"); err != nil {
		t.Fatalf("INSERT VALUES: %v", err)
	}

	// INSERT ... SELECT (copy + transform). Exercises the QP source child
	// lowering through the store-aware SeqScan path.
	if _, err := ex.Exec(ctx, "INSERT INTO t_rw SELECT id+100, a+1, b FROM t_rw WHERE id <= 3"); err != nil {
		t.Fatalf("INSERT...SELECT: %v", err)
	}

	count := queryInt(t, ex, ctx, "SELECT COUNT(*) FROM t_rw")
	if count != 6 {
		t.Fatalf("expected 6 rows after two inserts, got %d", count)
	}

	// UPDATE ... WHERE (only matching rows mutated).
	if _, err := ex.Exec(ctx, "UPDATE t_rw SET a = a + 1000 WHERE id <= 3"); err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	mm := queryInt(t, ex, ctx, "SELECT MIN(a) FROM t_rw")
	if mm != 11 { // id 101..103 have a = 11..31 (original 10..30 + 1)
		t.Fatalf("expected MIN(a)=11 after UPDATE, got %d", mm)
	}

	// DELETE ... WHERE.
	if _, err := ex.Exec(ctx, "DELETE FROM t_rw WHERE id > 100"); err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	count = queryInt(t, ex, ctx, "SELECT COUNT(*) FROM t_rw")
	if count != 3 {
		t.Fatalf("expected 3 rows after DELETE, got %d", count)
	}

	// SELECT with projection + filter.
	rows, err := ex.QueryAll(ctx, "SELECT id, a FROM t_rw WHERE a = 1010")
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row for a=1010, got %d", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(int64(1))) {
		t.Fatalf("expected id=1, got %v", rows[0].Data[0])
	}
}

func queryInt(t *testing.T, ex *Executor, ctx context.Context, q string) int64 {
	t.Helper()
	rows, err := ex.QueryAll(ctx, q)
	if err != nil {
		t.Fatalf("QueryAll(%q): %v", q, err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row for %q, got %d", q, len(rows))
	}
	v, ok := rows[0].Data[0].ToAny().(int64)
	if !ok {
		t.Fatalf("expected int64 result for %q, got %T", q, rows[0].Data[0].ToAny())
	}
	return v
}
