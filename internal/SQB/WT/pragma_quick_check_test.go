package WT

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestPragma_QuickCheck_Ok(t *testing.T) {
	p := NewPragma(&PS.PragmaStmt{Name: "quick_check"})

	ctx := context.Background()
	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(row.Data) < 1 || row.Data[0].S != "ok" {
		t.Errorf("got %v, want 'ok'", row.Data[0])
	}
}

func TestPragma_QuickCheck_ReturnsOnlyOneRow(t *testing.T) {
	p := NewPragma(&PS.PragmaStmt{Name: "quick_check"})

	ctx := context.Background()
	count := 0
	for {
		_, err := p.Next(ctx)
		if err == DT.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		count++
	}
	if count != 1 {
		t.Errorf("got %d rows, want 1", count)
	}
}