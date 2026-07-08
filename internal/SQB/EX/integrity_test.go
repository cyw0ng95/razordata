package EX

import (
	"context"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestIntegrityCheck_Success(t *testing.T) {
	ic := UT.NewIntegrityCheck()
	defer ic.Close()

	ctx := context.Background()
	count := 0
	for {
		_, err := ic.Next(ctx)
		if err == DT.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next() error: %v", err)
		}
		count++
	}

	// Success case: empty result set (no errors found)
	if count != 0 {
		t.Errorf("expected 0 rows for clean database, got %d", count)
	}
	if ic.RowsAffected() != 0 {
		t.Errorf("RowsAffected: got %d, want 0", ic.RowsAffected())
	}
}

func TestIntegrityCheck_Idempotent(t *testing.T) {
	ic := UT.NewIntegrityCheck()
	defer ic.Close()

	ctx := context.Background()

	// First run
	for {
		_, err := ic.Next(ctx)
		if err == DT.ErrNoRows {
			break
		}
	}

	// Second run - should still work
	for {
		_, err := ic.Next(ctx)
		if err == DT.ErrNoRows {
			break
		}
	}
}

func TestIntegrityCheck_Integration(t *testing.T) {
	pl := NewPlanner()

	// Parse PRAGMA integrity_check
	parser := PS.NewParser("PRAGMA integrity_check")
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	pragma, ok := stmt.(*PS.PragmaStmt)
	if !ok {
		t.Fatalf("expected *PS.PragmaStmt, got %T", stmt)
	}

	// Plan
	plan, err := pl.Plan(pragma)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Execute
	ctx := context.Background()
	op := plan.Root
	defer op.Close()

	count := 0
	for {
		row, err := op.Next(ctx)
		if err == DT.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		t.Logf("DT.Row: %+v", row)
		count++
	}

	// Clean database should return 0 rows (no errors)
	if count != 0 {
		t.Errorf("expected 0 error rows, got %d", count)
	}
}

// REQ001380: SST block CRC32 checksums pass for engine with flushes.
func TestIntegrityCheck_SSTCRC(t *testing.T) {
	ResetForTest(t)
	const rowBytes = 16 // 8-byte key + 8-byte value
	dir := t.TempDir()
	eng, err := ls.OpenWithOptions(dir, ls.Options{
		MemTableShards: 1,
		MemTableSize:   4096,
	})
	if err != nil {
		t.Fatalf("ls.OpenWithOptions: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	for i := range 2000 {
		k := []byte{byte(i >> 8), byte(i), 0, 0, 0, 0, 0, 0}
		v := []byte{byte(i), byte(i >> 8), 0, 0, 0, 0, 0, 0}
		if err := eng.Insert(k, v); err != nil {
			t.Fatalf("eng.Insert(%d): %v", i, err)
		}
	}
	eng.Flush().WaitForFlush()
	store := &engineStore{eng: eng}
	ctx := context.Background()
	parser := PS.NewParser("PRAGMA integrity_check")
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	pl := NewPlannerWithStore(store)
	plan, err := pl.Plan(stmt)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	op := plan.Root
	defer op.Close()
	var errs []string
	for {
		row, err := op.Next(ctx)
		if err == DT.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		if len(row.Data) > 2 {
			errs = append(errs, row.Data[2].String())
		}
	}
	if len(errs) > 0 {
		t.Errorf("integrity_check: %d errors (want 0):", len(errs))
		for _, e := range errs {
			t.Logf("  %s", e)
		}
	}
	direct := eng.VerifySSTFiles()
	if len(direct) > 0 {
		t.Errorf("VerifySSTFiles: %d errors (want 0):", len(direct))
		for _, e := range direct {
			t.Logf("  %s", e)
		}
	}
}
