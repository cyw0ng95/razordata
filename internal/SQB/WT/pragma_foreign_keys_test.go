package WT

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestPragma_ForeignKeys_DefaultOn(t *testing.T) {
	if !DT.IsForeignKeysEnabled() {
		t.Error("foreign_keys should default to ON")
	}
}

func TestPragma_ForeignKeys_SetOff(t *testing.T) {
	DT.SetForeignKeysEnabled(true)
	defer DT.SetForeignKeysEnabled(true)

	ctx := context.Background()
	stmt := &PS.PragmaStmt{Name: "foreign_keys", Value: "OFF"}
	p := NewPragma(stmt)

	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].I64 != 0 {
		t.Errorf("got %d, want 0 (OFF)", row.Data[0].I64)
	}
	if DT.IsForeignKeysEnabled() {
		t.Error("foreign_keys should be OFF after PRAGMA")
	}
}

func TestPragma_ForeignKeys_SetOn(t *testing.T) {
	DT.SetForeignKeysEnabled(false)
	defer DT.SetForeignKeysEnabled(true)

	ctx := context.Background()
	stmt := &PS.PragmaStmt{Name: "foreign_keys", Value: "ON"}
	p := NewPragma(stmt)

	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].I64 != 1 {
		t.Errorf("got %d, want 1 (ON)", row.Data[0].I64)
	}
	if !DT.IsForeignKeysEnabled() {
		t.Error("foreign_keys should be ON after PRAGMA")
	}
}

func TestPragma_ForeignKeys_ReadOnly(t *testing.T) {
	DT.SetForeignKeysEnabled(true)
	defer DT.SetForeignKeysEnabled(true)

	ctx := context.Background()
	stmt := &PS.PragmaStmt{Name: "foreign_keys"} // no value = read
	p := NewPragma(stmt)

	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].I64 != 1 {
		t.Errorf("read: got %d, want 1", row.Data[0].I64)
	}
}

func TestPragma_ForeignKeys_ReadAfterToggle(t *testing.T) {
	DT.SetForeignKeysEnabled(true)
	defer DT.SetForeignKeysEnabled(true)

	// Set OFF
	DT.SetForeignKeysEnabled(false)

	ctx := context.Background()
	stmt := &PS.PragmaStmt{Name: "foreign_keys"} // read
	p := NewPragma(stmt)

	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].I64 != 0 {
		t.Errorf("read after toggle: got %d, want 0", row.Data[0].I64)
	}
}

func TestPragma_ForeignKeys_SetNum(t *testing.T) {
	DT.SetForeignKeysEnabled(false)
	defer DT.SetForeignKeysEnabled(true)

	ctx := context.Background()
	stmt := &PS.PragmaStmt{Name: "foreign_keys", Value: "1"}
	p := NewPragma(stmt)

	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].I64 != 1 {
		t.Errorf("got %d, want 1", row.Data[0].I64)
	}
	if !DT.IsForeignKeysEnabled() {
		t.Error("foreign_keys should be ON after PRAGMA 1")
	}
}