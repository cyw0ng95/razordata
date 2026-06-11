package EX

import (
	"context"
	"errors"
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
		// Translate "not found" to (nil, false, nil)
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

	for _, s := range []string{
		"INSERT INTO t VALUES (1, 'a')",
		"INSERT INTO t VALUES (2, 'b')",
		"INSERT INTO t VALUES (3, 'c')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	res, err := ex.Exec(ctx, "DELETE FROM t WHERE id = 2")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
	}
	rows, err := ex.QueryAll(ctx, "SELECT id FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows after delete, got %d", len(rows))
	}
}

func TestCreateTable_RegistersStoreSchema(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE u (id INTEGER, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	ss, ok := schemaFor("u")
	if !ok {
		t.Fatal("schema not registered")
	}
	if len(ss.cols) != 2 {
		t.Errorf("expected 2 cols, got %d", len(ss.cols))
	}
}

func TestExecutor_Filter_AgainstStore(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ex.RegisterTableWithPK("t", []string{"id", "name"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 'alice')",
		"INSERT INTO t VALUES (2, 'bob')",
		"INSERT INTO t VALUES (3, 'carol')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT name FROM t WHERE id > 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}
}

func TestNoPK_InsertReturnsError(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ex.RegisterTable("t", []string{"a", "b"}) // no PK
	ctx := context.Background()
	_, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 'x')")
	if err == nil {
		t.Fatal("expected error for table without PK")
	}
	if !errors.Is(err, ErrNoPKForStorage) {
		t.Errorf("expected ErrNoPKForStorage, got %v", err)
	}
}
