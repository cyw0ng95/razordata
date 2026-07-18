package SY

import (
	"context"
	"path/filepath"
	"testing"

	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// resetTriggerStateForTest clears the package-level trigger registry
// and the fire-count counter so tests don't pollute each other.
func resetTriggerStateForTest(t *testing.T) {
	t.Helper()
	WT.ClearTriggerState()
	WT.ResetTriggerFireCount()
}

// TestUpdate_BatchTriggerFiring_OncePerRow verifies REQ001578: a 500-
// row UPDATE through a registered AFTER UPDATE trigger fires the
// trigger body exactly 500 times (one per mutated row), but the
// firing happens AFTER each chunk's heap batch is durable rather
// than interleaved with row mutation.
func TestUpdate_BatchTriggerFiring_OncePerRow(t *testing.T) {
	resetTriggerStateForTest(t)
	defer resetTriggerStateForTest(t)

	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer eng.Close(context.Background())
	ctx := context.Background()

	s, err := eng.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer s.Rollback(ctx)

	if _, err := s.Exec(ctx, "CREATE TABLE t1 (id INTEGER, v INTEGER, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create t1: %v", err)
	}
	if _, err := s.Exec(ctx, "CREATE MATERIALIZED VIEW mv1 AS SELECT id, v FROM t1"); err != nil {
		t.Fatalf("create mv1: %v", err)
	}

	trigger := &PS.TriggerStmt{
		Name:    "mv1_refresh_after_update",
		OnTable: "t1",
		Event:   "UPDATE",
		Time:    "AFTER",
		Body: []PS.Stmt{
			func() PS.Stmt { s, _ := PS.NewParser("REFRESH MATERIALIZED VIEW mv1").Parse(); return s }(),
		},
	}
	if err := WT.RegisterTrigger(trigger); err != nil {
		t.Fatalf("RegisterTrigger: %v", err)
	}

	for i := 1; i <= 500; i++ {
		if _, err := s.Exec(ctx, "INSERT INTO t1 VALUES ("+itoaSY(i)+", "+itoaSY(i)+")"); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if _, err := s.Exec(ctx, "REFRESH MATERIALIZED VIEW mv1"); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	WT.ResetTriggerFireCount()
	if _, err := s.Exec(ctx, "UPDATE t1 SET v = v + 1000"); err != nil {
		t.Fatalf("bulk update: %v", err)
	}

	got := WT.TriggerFireCount()
	if got != 500 {
		t.Errorf("TriggerFireCount after 500-row UPDATE = %d, want 500", got)
	}
}

// TestDelete_BatchTriggerFiring_OncePerRow verifies REQ001578's
// DELETE counterpart: a 500-row DELETE fires the trigger body
// exactly 500 times.
func TestDelete_BatchTriggerFiring_OncePerRow(t *testing.T) {
	resetTriggerStateForTest(t)
	defer resetTriggerStateForTest(t)

	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer eng.Close(context.Background())
	ctx := context.Background()

	s, err := eng.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer s.Rollback(ctx)

	if _, err := s.Exec(ctx, "CREATE TABLE t1 (id INTEGER, v INTEGER, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create t1: %v", err)
	}
	if _, err := s.Exec(ctx, "CREATE MATERIALIZED VIEW mv1 AS SELECT id, v FROM t1"); err != nil {
		t.Fatalf("create mv1: %v", err)
	}

	trigger := &PS.TriggerStmt{
		Name:    "mv1_refresh_after_delete",
		OnTable: "t1",
		Event:   "DELETE",
		Time:    "AFTER",
		Body: []PS.Stmt{
			func() PS.Stmt { s, _ := PS.NewParser("REFRESH MATERIALIZED VIEW mv1").Parse(); return s }(),
		},
	}
	if err := WT.RegisterTrigger(trigger); err != nil {
		t.Fatalf("RegisterTrigger: %v", err)
	}

	for i := 1; i <= 500; i++ {
		if _, err := s.Exec(ctx, "INSERT INTO t1 VALUES ("+itoaSY(i)+", "+itoaSY(i)+")"); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if _, err := s.Exec(ctx, "REFRESH MATERIALIZED VIEW mv1"); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	WT.ResetTriggerFireCount()
	if _, err := s.Exec(ctx, "DELETE FROM t1"); err != nil {
		t.Fatalf("bulk delete: %v", err)
	}

	got := WT.TriggerFireCount()
	if got != 500 {
		t.Errorf("TriggerFireCount after 500-row DELETE = %d, want 500", got)
	}
}

// TestUpdate_BatchTriggerFiring_ChunkBoundary verifies that a row
// count spanning exactly N full chunks + one partial chunk fires
// the expected number of trigger events. With 256-row chunks and
// 500 rows, there are 1 complete chunk + 244 in a second chunk.
// All 500 rows must fire.
func TestUpdate_BatchTriggerFiring_ChunkBoundary(t *testing.T) {
	resetTriggerStateForTest(t)
	defer resetTriggerStateForTest(t)

	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer eng.Close(context.Background())
	ctx := context.Background()

	s, err := eng.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer s.Rollback(ctx)

	if _, err := s.Exec(ctx, "CREATE TABLE t1 (id INTEGER, v INTEGER, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create t1: %v", err)
	}
	if _, err := s.Exec(ctx, "CREATE MATERIALIZED VIEW mv1 AS SELECT v FROM t1"); err != nil {
		t.Fatalf("create mv1: %v", err)
	}

	trigger := &PS.TriggerStmt{
		Name:    "mv1_refresh_after_update",
		OnTable: "t1",
		Event:   "UPDATE",
		Time:    "AFTER",
		Body: []PS.Stmt{
			func() PS.Stmt { s, _ := PS.NewParser("REFRESH MATERIALIZED VIEW mv1").Parse(); return s }(),
		},
	}
	if err := WT.RegisterTrigger(trigger); err != nil {
		t.Fatalf("RegisterTrigger: %v", err)
	}

	// 257 rows: one full 256-row chunk + 1 row in a second chunk.
	for i := 1; i <= 257; i++ {
		if _, err := s.Exec(ctx, "INSERT INTO t1 VALUES ("+itoaSY(i)+", "+itoaSY(i)+")"); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if _, err := s.Exec(ctx, "REFRESH MATERIALIZED VIEW mv1"); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	WT.ResetTriggerFireCount()
	if _, err := s.Exec(ctx, "UPDATE t1 SET v = v + 1"); err != nil {
		t.Fatalf("update: %v", err)
	}

	// 257 rows must fire exactly 257 times regardless of chunk boundaries.
	if got := WT.TriggerFireCount(); got != 257 {
		t.Errorf("TriggerFireCount = %d, want 257 (spans 2 chunks)", got)
	}
}

// TestUpdate_BatchTriggerFiring_NoTriggerRegistered verifies that a
// batched UPDATE without a registered trigger doesn't fire anything.
func TestUpdate_BatchTriggerFiring_NoTriggerRegistered(t *testing.T) {
	resetTriggerStateForTest(t)
	defer resetTriggerStateForTest(t)

	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer eng.Close(context.Background())
	ctx := context.Background()

	s, err := eng.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer s.Rollback(ctx)

	if _, err := s.Exec(ctx, "CREATE TABLE t1 (id INTEGER, v INTEGER, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create t1: %v", err)
	}

	for i := 1; i <= 100; i++ {
		if _, err := s.Exec(ctx, "INSERT INTO t1 VALUES ("+itoaSY(i)+", "+itoaSY(i)+")"); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	WT.ResetTriggerFireCount()
	if _, err := s.Exec(ctx, "UPDATE t1 SET v = v + 1000"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := WT.TriggerFireCount(); got != 0 {
		t.Errorf("TriggerFireCount with no trigger registered = %d, want 0", got)
	}
}

func itoaSY(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
