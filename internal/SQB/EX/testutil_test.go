//go:build !slt_corpus

package EX

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
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

// REQ002035: shared engine + executor for engine-backed tests.
// Eliminates per-test NewPageCache / WarmFilterBatchPool / flate.NewWriter
// allocations that caused 83% GC overhead and 618ms test time.
// The engine is initialized lazily on first use and reset between tests.
var (
	sharedEngOnce sync.Once
	sharedEng     *ls.Engine
	sharedEngEx   *Executor
	sharedEngDir  string
)

func initSharedEngine(t testing.TB) {
	t.Helper()
	sharedEngOnce.Do(func() {
		var err error
		sharedEngDir, err = os.MkdirTemp("", "razor-ex-test-")
		if err != nil {
			t.Fatalf("os.MkdirTemp: %v", err)
		}
		sharedEng, err = ls.Open(filepath.Join(sharedEngDir, "db"))
		if err != nil {
			t.Fatalf("ls.Open: %v", err)
		}
		sharedEngEx = NewExecutorWithEngine(&engineStore{eng: sharedEng})
	})
}

// resetSharedEngine resets the shared engine to a clean state.
func resetSharedEngine(t testing.TB) {
	t.Helper()
	UnregisterAll()
	sharedEngEx.ClearPlanCache()
	if err := sharedEng.DropAll(); err != nil {
		t.Fatalf("sharedEng.DropAll: %v", err)
	}
	sharedEng.ResetPageCache()
}

// newEngineExecutor returns an Executor backed by the shared LSM engine,
// reset to a clean state. REQ002035: reuses one engine across all tests
// instead of creating a fresh one per test (saves ~921 MB allocs + GC).
// Accepts testing.TB so both tests (*testing.T) and benchmarks (*testing.B) can use it.
func newEngineExecutor(t testing.TB) (*Executor, *ls.Engine) {
	t.Helper()
	initSharedEngine(t)
	resetSharedEngine(t)
	return sharedEngEx, sharedEng
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
