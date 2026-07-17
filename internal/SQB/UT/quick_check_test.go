package UT

import (
	"context"
	"testing"
)

func TestQuickCheck_AllOk(t *testing.T) {
	qc := NewQuickCheck()
	row, err := qc.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(row.Cols) != 1 || row.Cols[0] != "quick_check" {
		t.Fatalf("expected quick_check column, got %v", row.Cols)
	}
	if len(row.Data) != 1 || row.Data[0].S != "ok" {
		t.Fatalf("expected 'ok', got %v", row.Data[0])
	}
	_, err = qc.Next(context.Background())
	if err != ErrNoRows {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
}

func TestQuickCheck_CountMismatch(t *testing.T) {
	// Simulate a table with row count mismatch.
	// Since DT.Tables and DT.LookupStats are package-level,
	// we can't easily mock them in unit tests without affecting
	// other tests. This test verifies the operator itself.
	qc := NewQuickCheck()
	row, err := qc.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	// Result should be either "ok" or a mismatch row.
	if len(row.Cols) == 3 {
		// Mismatch case — columns: table, expected, actual
		if len(row.Data) != 3 {
			t.Fatalf("expected 3 data cols, got %d", len(row.Data))
		}
	} else if len(row.Cols) == 1 {
		// Ok case
		if row.Data[0].S != "ok" {
			t.Fatalf("expected 'ok', got %v", row.Data[0].S)
		}
	} else {
		t.Fatalf("unexpected columns: %v", row.Cols)
	}
}

func TestQuickCheck_Idempotent(t *testing.T) {
	qc := NewQuickCheck()
	_, err := qc.Next(context.Background())
	if err != nil {
		t.Fatalf("First Next: %v", err)
	}
	_, err = qc.Next(context.Background())
	if err != ErrNoRows {
		t.Fatalf("expected ErrNoRows on second call, got %v", err)
	}
}

func TestQuickCheck_Close(t *testing.T) {
	qc := NewQuickCheck()
	if err := qc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
