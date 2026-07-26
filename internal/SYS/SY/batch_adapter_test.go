package SY

import (
	"context"
	"reflect"
	"strconv"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestExecutorStoreAdapter_BatchInterfaceAssertion verifies that the
// executorStoreAdapter — the bridge between *ls.Engine and the
// DT.Store interface — now satisfies DT.BatchStore and
// DT.BatchDeleteStore so the writer's chunked mutation path
// (REQ001421 InsertRowBatch, REQ001555 UpdateRowBatch,
// REQ001556 DeleteRowBatch) type-asserts to the batched methods
// instead of falling back to per-row Insert/Delete at runtime.
func TestExecutorStoreAdapter_BatchInterfaceAssertion(t *testing.T) {
	initSharedEngine(t)
	resetSharedEngine(t)
	eng := sharedEng

	if eng.exeAdapter == nil {
		t.Fatal("eng.exeAdapter is nil; cannot verify batch interface wiring")
	}

	// Compile-time / runtime: BatchStore is satisfied.
	bsType := reflect.TypeOf((*DT.BatchStore)(nil)).Elem()
	if !reflect.TypeOf(eng.exeAdapter).Implements(bsType) {
		t.Fatalf("executorStoreAdapter does not implement DT.BatchStore; REQ001421 InsertRowBatch will fall back to per-row Insert")
	}

	// Compile-time / runtime: BatchDeleteStore is satisfied.
	bdsType := reflect.TypeOf((*DT.BatchDeleteStore)(nil)).Elem()
	if !reflect.TypeOf(eng.exeAdapter).Implements(bdsType) {
		t.Fatalf("executorStoreAdapter does not implement DT.BatchDeleteStore; REQ001556 DeleteRowBatch will fall back to per-row Delete")
	}

	// Runtime type assertions against the public Store view.
	var s DT.Store = eng.exeAdapter
	if _, ok := s.(DT.BatchStore); !ok {
		t.Errorf("runtime type assertion to DT.BatchStore failed")
	}
	if _, ok := s.(DT.BatchDeleteStore); !ok {
		t.Errorf("runtime type assertion to DT.BatchDeleteStore failed")
	}
}

// TestExecutorStoreAdapter_BatchPathActivate verifies the end-to-end
// UPDATE/DELETE chunked mutation path is active against a real
// engine: a bulk UPDATE + DELETE round-trips correctly through
// the executor's batched writes, demonstrating that the adapter
// wiring reaches production code paths.
func TestExecutorStoreAdapter_BatchPathActivate(t *testing.T) {
	initSharedEngine(t)
	resetSharedEngine(t)
	eng := sharedEng

	ctx := context.Background()
	s, err := eng.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err := s.Exec(ctx, "CREATE TABLE t (id INTEGER, v INTEGER, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Insert 500 rows so the UPDATE + DELETE have enough volume to
	// exercise the chunked path (chunkSize = 256).
	for i := 1; i <= 500; i++ {
		if _, err := s.Exec(ctx, "INSERT INTO t VALUES ("+strconv.Itoa(i)+", "+strconv.Itoa(i)+")"); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// Bulk UPDATE — should now flow through UpdateRowBatch because
	// the executor's adapter satisfies BatchStore (WriteBatch).
	if _, err := s.Exec(ctx, "UPDATE t SET v = v + 1000"); err != nil {
		t.Fatalf("bulk update: %v", err)
	}

	// Verify the UPDATE actually took effect on a sample of rows.
	for _, id := range []int{1, 100, 250, 500} {
		rows, err := s.Query(ctx, "SELECT v FROM t WHERE id = "+strconv.Itoa(id))
		if err != nil {
			t.Fatalf("query id=%d: %v", id, err)
		}
		row, err := rows.Next()
		if err != nil {
			t.Fatalf("query id=%d: %v", id, err)
		}
		rows.Close()
		if len(row.Data) < 1 {
			t.Fatalf("query id=%d: empty row data", id)
		}
		got := row.Data[0].AsInt()
		want := int64(id + 1000)
		if got != want {
			t.Errorf("id=%d v=%d, want %d", id, got, want)
		}
	}

	// Bulk DELETE — should now flow through DeleteRowBatch because
	// the executor's adapter satisfies BatchDeleteStore (DeleteBatch).
	if _, err := s.Exec(ctx, "DELETE FROM t WHERE id <= 250"); err != nil {
		t.Fatalf("bulk delete: %v", err)
	}

	// Verify the DELETE took effect: 250 remaining rows.
	rows, err := s.Query(ctx, "SELECT COUNT(*) FROM t")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	row, err := rows.Next()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	rows.Close()
	count := row.Data[0].AsInt()
	if count != 250 {
		t.Errorf("after bulk DELETE: COUNT(*) = %d, want 250", count)
	}
}
