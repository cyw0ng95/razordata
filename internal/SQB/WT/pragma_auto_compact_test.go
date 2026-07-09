package WT

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestPragma_AutoCompact_DefaultNone(t *testing.T) {
	DT.SetAutoCompactMode(0)
	if DT.AutoCompactMode() != 0 {
		t.Errorf("default auto_compact = %d, want 0 (none)", DT.AutoCompactMode())
	}
}

func TestPragma_AutoCompact_SetIncremental(t *testing.T) {
	DT.SetAutoCompactMode(0)
	defer DT.SetAutoCompactMode(0)

	ctx := context.Background()
	stmt := &PS.PragmaStmt{Name: "auto_compact", Value: "incremental"}
	p := NewPragma(stmt)

	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].S != "incremental" {
		t.Errorf("got %q, want %q", row.Data[0].S, "incremental")
	}
	if DT.AutoCompactMode() != 1 {
		t.Errorf("AutoCompactMode = %d, want 1", DT.AutoCompactMode())
	}
}

func TestPragma_AutoCompact_SetFull(t *testing.T) {
	DT.SetAutoCompactMode(0)
	defer DT.SetAutoCompactMode(0)

	ctx := context.Background()
	stmt := &PS.PragmaStmt{Name: "auto_compact", Value: "full"}
	p := NewPragma(stmt)

	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].S != "full" {
		t.Errorf("got %q, want %q", row.Data[0].S, "full")
	}
	if DT.AutoCompactMode() != 2 {
		t.Errorf("AutoCompactMode = %d, want 2", DT.AutoCompactMode())
	}
}

func TestPragma_AutoCompact_SetNumeric(t *testing.T) {
	DT.SetAutoCompactMode(0)
	defer DT.SetAutoCompactMode(0)

	ctx := context.Background()
	stmt := &PS.PragmaStmt{Name: "auto_compact", Value: "1"}
	p := NewPragma(stmt)

	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].S != "incremental" {
		t.Errorf("got %q, want %q", row.Data[0].S, "incremental")
	}
	if DT.AutoCompactMode() != 1 {
		t.Errorf("AutoCompactMode = %d, want 1", DT.AutoCompactMode())
	}
}

func TestPragma_AutoCompact_ReadOnly(t *testing.T) {
	DT.SetAutoCompactMode(1)
	defer DT.SetAutoCompactMode(0)

	ctx := context.Background()
	stmt := &PS.PragmaStmt{Name: "auto_compact"} // no value = read
	p := NewPragma(stmt)

	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].S != "incremental" {
		t.Errorf("read: got %q, want %q", row.Data[0].S, "incremental")
	}
}

func TestPragma_AutoCompact_ReadAfterToggle(t *testing.T) {
	DT.SetAutoCompactMode(2)
	defer DT.SetAutoCompactMode(0)

	ctx := context.Background()
	stmt := &PS.PragmaStmt{Name: "auto_compact"}
	p := NewPragma(stmt)

	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].S != "full" {
		t.Errorf("read after toggle: got %q, want %q", row.Data[0].S, "full")
	}
}

func TestPragma_AutoCompact_None(t *testing.T) {
	DT.SetAutoCompactMode(0)
	defer DT.SetAutoCompactMode(0)

	ctx := context.Background()
	stmt := &PS.PragmaStmt{Name: "auto_compact", Value: "none"}
	p := NewPragma(stmt)

	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].S != "none" {
		t.Errorf("got %q, want %q", row.Data[0].S, "none")
	}
	if DT.AutoCompactMode() != 0 {
		t.Errorf("AutoCompactMode = %d, want 0", DT.AutoCompactMode())
	}
}