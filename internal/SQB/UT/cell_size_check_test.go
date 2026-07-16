package UT

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// REQ001387: CellSizeCheck populates rows correctly.
func TestCellSizeCheck_NoViolations(t *testing.T) {
	csc := NewCellSizeCheck()
	ctx := context.Background()
	// First call populates the result set and returns ErrNoRows.
	csc.Next(ctx) // ignore error
	if len(csc.rows) != 0 {
		t.Errorf("expected 0 violation rows, got %d", len(csc.rows))
	}
}

// REQ001387: CellSizeCheck detects oversized BLOB.
func TestCellSizeCheck_OversizedBlob(t *testing.T) {
	DT.TablesMu.Lock()
	DT.Schemas["test_large_blob"] = []string{"data"}
	DT.Tables["test_large_blob"] = []DT.Row{
		{
			Data: []DT.Value{
				{Kind: DT.KindBlob, B: make([]byte, 5000)},
			},
		},
	}
	DT.TablesMu.Unlock()

	csc := NewCellSizeCheck()
	ctx := context.Background()
	csc.Next(ctx) // ignore error

	found := false
	for _, row := range csc.rows {
		if len(row.Data) >= 3 {
			errMsg := row.Data[2]
			if errMsg.Kind == DT.KindText && len(errMsg.S) > 0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("expected to find oversized blob violation")
	}

	// Clean up.
	DT.TablesMu.Lock()
	delete(DT.Schemas, "test_large_blob")
	delete(DT.Tables, "test_large_blob")
	DT.TablesMu.Unlock()
}
