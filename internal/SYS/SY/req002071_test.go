package SY

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestOpenMemoryOnly verifies that the engine opens in MemoryOnly mode
// (no WAL, no SST flush, no disk I/O). REQ002071.
func TestOpenMemoryOnly(t *testing.T) {
	eng, err := Open(context.Background(), "", AP.Options{
		MemoryOnly:        true,
		MaxMemoryPerQuery: 512 << 20,
		JoinBufferSize:    256 << 20,
	})
	if err != nil {
		t.Fatalf("Open MemoryOnly: %v", err)
	}
	defer eng.Close(context.Background())

	// Should be able to execute a query.
	exe := eng.Executor()
	rows, err := exe.QueryAll(context.Background(), "SELECT 1+2")
	if err != nil {
		t.Fatalf("QueryAll SELECT 1+2: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows count = %d, want 1", len(rows))
	}
	if len(rows[0].Data) != 1 {
		t.Fatalf("columns count = %d, want 1", len(rows[0].Data))
	}

	// BeginTxn should work (no WAL).
	sess, err := eng.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin session: %v", err)
	}
	tx, err := sess.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin transaction: %v", err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
}

// TestMemoryOnlyReset verifies that Reset clears data in MemoryOnly mode.
// REQ002071.
func TestMemoryOnlyReset(t *testing.T) {
	eng, err := Open(context.Background(), "", AP.Options{
		MemoryOnly:        true,
		MaxMemoryPerQuery: 512 << 20,
		JoinBufferSize:    256 << 20,
	})
	if err != nil {
		t.Fatalf("Open MemoryOnly: %v", err)
	}
	defer eng.Close(context.Background())

	exe := eng.Executor()

	// Create a table and insert data.
	_, err = exe.QueryAll(context.Background(), "CREATE TABLE t (a INT)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	_, err = exe.QueryAll(context.Background(), "INSERT INTO t VALUES (1), (2), (3)")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	rows, err := exe.QueryAll(context.Background(), "SELECT a FROM t ORDER BY a")
	if err != nil {
		t.Fatalf("SELECT before reset: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows before reset = %d, want 3", len(rows))
	}

	// Reset should clear everything.
	if err := eng.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// After reset, the table data should be gone (table schema is
	// cleared by UnregisterAll, LS engine data is cleared by DropAllInMemory).
	rows2, err := exe.QueryAll(context.Background(), "SELECT a FROM t ORDER BY a")
	t.Logf("SELECT after reset: err=%v, rows=%d", err, len(rows2))
	if err == nil && len(rows2) != 0 {
		t.Errorf("SELECT after reset: expected error or empty result, got %d rows", len(rows2))
	}
}

// TestMemoryOnlyStats verifies Stats works in MemoryOnly mode (no WAL stats).
// REQ002071.
func TestMemoryOnlyStats(t *testing.T) {
	eng, err := Open(context.Background(), "", AP.Options{
		MemoryOnly: true,
	})
	if err != nil {
		t.Fatalf("Open MemoryOnly: %v", err)
	}
	defer eng.Close(context.Background())

	stats := eng.Stats()
	// WAL stats should be zero/empty in MemoryOnly mode.
	if stats.WAL.TruncatedSegments != 0 {
		t.Errorf("WAL.TruncatedSegments = %d, want 0", stats.WAL.TruncatedSegments)
	}
}
