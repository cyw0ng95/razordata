package EX

import (
	"context"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestIntegrityCheck_Success(t *testing.T) {
	ic := NewIntegrityCheck()
	defer ic.Close()

	ctx := context.Background()
	count := 0
	for {
		_, err := ic.Next(ctx)
		if err == ErrNoRows {
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
	ic := NewIntegrityCheck()
	defer ic.Close()

	ctx := context.Background()

	// First run
	for {
		_, err := ic.Next(ctx)
		if err == ErrNoRows {
			break
		}
	}

	// Second run - should still work
	for {
		_, err := ic.Next(ctx)
		if err == ErrNoRows {
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
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		t.Logf("Row: %+v", row)
		count++
	}

	// Clean database should return 0 rows (no errors)
	if count != 0 {
		t.Errorf("expected 0 error rows, got %d", count)
	}
}
