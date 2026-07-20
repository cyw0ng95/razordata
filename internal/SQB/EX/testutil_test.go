//go:build !slt_corpus

package EX

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// ResetForTest clears the package-level DT.Tables and schemas
// maps and arranges a Cleanup that re-clears after the test
// returns. Tests that call RegisterTable, RegisterTableSchema,
// or RegisterTableWithPK MUST call ResetForTest at the top
// of their Test* function to remain safe under
// `go test -count=N`. See REQ000346 (iter-26).
func ResetForTest(t testing.TB) {
	t.Helper()
	UnregisterAll()
	t.Cleanup(UnregisterAll)
}

// engineStore adapts an *ls.Engine to the EX.Store interface.
type engineStore struct{ eng *ls.Engine }

func (s *engineStore) Insert(k, v []byte) error { return s.eng.Insert(k, v) }
func (s *engineStore) Delete(k []byte) error    { return s.eng.Delete(k) }
func (s *engineStore) Get(k []byte) ([]byte, bool, error) {
	v, err := s.eng.Get(k)
	if err != nil {
		if errors.Is(err, ls.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return v, true, nil
}
func (s *engineStore) NewIterator(prefix []byte) ls.RangeIter {
	return s.eng.NewIterator(prefix)
}
func (s *engineStore) ManualCompact() error { return s.eng.ManualCompact() }

// newEngineExecutor creates an Executor backed by a real LSM engine.
// Accepts testing.TB so both tests (*testing.T) and benchmarks (*testing.B) can use it.
func newEngineExecutor(t testing.TB) (*Executor, *ls.Engine) {
	t.Helper()
	dir := t.TempDir()
	eng, err := ls.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatalf("ls.Open: %v", err)
	}
	ex := NewExecutorWithEngine(&engineStore{eng: eng})
	return ex, eng
}

// mustExec runs a statement and fails the test on error.
func mustExec(t testing.TB, exec *Executor, ctx context.Context, sql string) {
	t.Helper()
	if _, err := exec.Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// mustQueryAll runs a query and fails the test on error.
func mustQueryAll(t testing.TB, exec *Executor, ctx context.Context, sql string) []DT.Row {
	t.Helper()
	rows, err := exec.QueryAll(ctx, sql)
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return rows
}

// cleanupTest drops a table if it exists (for deferred cleanup).
func cleanupTest(table string) {
	exec := NewExecutor()
	exec.Exec(context.Background(), "DROP TABLE IF EXISTS "+table)
}

func TestResetForTest_Reentrant(t *testing.T) {
	ResetForTest(t)
	ResetForTest(t)
}
