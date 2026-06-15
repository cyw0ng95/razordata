package EX

import (
	"context"
	"path/filepath"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

// engineStore adapts an *ls.Engine to the EX.Store interface.
type engineStore struct {
	eng *ls.Engine
}

func (s *engineStore) Insert(k, v []byte) error { return s.eng.Insert(k, v) }
func (s *engineStore) Delete(k []byte) error    { return s.eng.Delete(k) }
func (s *engineStore) Get(k []byte) ([]byte, bool, error) {
	v, err := s.eng.Get(k)
	if err != nil {
		if err.Error() == "key not found" || err.Error() == "not found" || err.Error() == "eng: key not found" {
			return nil, false, nil
		}
		return nil, false, err
	}
	return v, true, nil
}
func (s *engineStore) NewIterator(prefix []byte) ls.RangeIter {
	return s.eng.NewIterator(prefix)
}
func (s *engineStore) ManualCompact() error {
	return s.eng.ManualCompact()
}

func newEngineExecutor(t *testing.T) (*Executor, *ls.Engine) {
	t.Helper()
	dir := t.TempDir()
	eng, err := ls.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatalf("ls.Open: %v", err)
	}
	ex := NewExecutorWithEngine(&engineStore{eng: eng})
	return ex, eng
}

func TestSeqScan_AgainstRealStore(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ex.RegisterTableWithPK("users", []string{"id", "name"}, "id")
	ctx := context.Background()

	for _, s := range []string{
		"INSERT INTO users VALUES (1, 'alice')",
		"INSERT INTO users VALUES (2, 'bob')",
		"INSERT INTO users VALUES (3, 'carol')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(rows))
	}
}

func TestInsert_AndGetViaExecutor(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ex.RegisterTableWithPK("kv", []string{"k", "v"}, "k")
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO kv VALUES ('a', '1')"); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO kv VALUES ('b', '2')"); err != nil {
		t.Fatalf("insert b: %v", err)
	}

	rows, err := ex.QueryAll(ctx, "SELECT k, v FROM kv")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestUpdate_AppendsVersion(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ex.RegisterTableWithPK("t", []string{"id", "val"}, "id")
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 'old')"); err != nil {
		t.Fatal(err)
	}
	res, err := ex.Exec(ctx, "UPDATE t SET val = 'new' WHERE id = 1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
	}
	rows, err := ex.QueryAll(ctx, "SELECT val FROM t WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if got, ok := rows[0].Data[0].(string); !ok || got != "new" {
		t.Errorf("expected val=new, got %v", rows[0].Data[0])
	}
}

func TestDelete_InsertsTombstone(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ex.RegisterTableWithPK("t", []string{"id", "val"}, "id")
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 'hello')"); err != nil {
		t.Fatal(err)
	}
	res, err := ex.Exec(ctx, "DELETE FROM t WHERE id = 1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
	}
	rows, err := ex.QueryAll(ctx, "SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows after delete, got %d", len(rows))
	}
}

func TestSQLviaEngine_CreateInsertUpdateSelect(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	_, err := ex.Exec(ctx, "CREATE TABLE engine_t (id INTEGER PRIMARY KEY, val INTEGER)")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for i := 1; i <= 5; i++ {
		_, err = ex.Exec(ctx, "INSERT INTO engine_t VALUES (?, ?)", int64(i), int64(i*10))
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT * FROM engine_t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}

	_, err = ex.Exec(ctx, "UPDATE engine_t SET val = val * 2 WHERE id > 2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	rows, err = ex.QueryAll(ctx, "SELECT * FROM engine_t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows after update, got %d", len(rows))
	}
	expected := [][]int64{{1, 10}, {2, 20}, {3, 60}, {4, 80}, {5, 100}}
	for i, exp := range expected {
		if rows[i].Data[0] != exp[0] || rows[i].Data[1] != exp[1] {
			t.Errorf("row %d: got %v, want %v", i, rows[i].Data, exp)
		}
	}
}

func TestEngine_SequentialDDL(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	// Create multiple tables with indexes (like SLT tests do)
	tnames := []string{"ta", "tb", "tc"}
	for _, tname := range tnames {
		_, err := ex.Exec(ctx, "CREATE TABLE "+tname+" (id INTEGER PRIMARY KEY, val NUMERIC, w TEXT)")
		if err != nil {
			t.Fatalf("create %s: %v", tname, err)
		}
		for j := 1; j <= 3; j++ {
			_, err = ex.Exec(ctx, "INSERT INTO "+tname+" VALUES (?, ?, ?)", int64(j), int64(j*10), "hello")
			if err != nil {
				t.Fatalf("insert %s/%d: %v", tname, j, err)
			}
		}
		_, err = ex.Exec(ctx, "CREATE INDEX i1 ON "+tname+"(val)")
		if err != nil {
			t.Fatalf("create index %s: %v", tname, err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT * FROM ta ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Errorf("ta: expected 3 rows, got %d", len(rows))
	}
}
