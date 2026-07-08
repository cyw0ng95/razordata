//go:build debug

package WT

import (
	"context"
	"strings"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestDebugPragma_JoinTracing_OnOff(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name  string
		value string
	}{
		{"debug_join_tracing", "summary"},
		{"debug_join_tracing", "detailed"},
		{"debug_join_tracing", "full"},
		{"debug_join_tracing", "off"},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			stmt := &PS.PragmaStmt{Name: tt.name, Value: tt.value}
			p := NewPragma(stmt)

			row, err := p.Next(ctx)
			if err != nil {
				t.Fatalf("Next() error: %v", err)
			}
			if len(row.Cols) == 0 || row.Cols[0] != tt.name {
				t.Fatalf("expected col %q, got %v", tt.name, row.Cols)
			}
			if tt.value != "off" && !strings.Contains(row.Data[0].String(), "OK") {
				t.Fatalf("expected OK response, got %q", row.Data[0].String())
			}
			if tt.value == "off" && !strings.Contains(row.Data[0].String(), "disabled") {
				t.Fatalf("expected disabled response, got %q", row.Data[0].String())
			}

			// Second call should be ErrNoRows
			_, err = p.Next(ctx)
			if err != DT.ErrNoRows {
				t.Fatalf("expected ErrNoRows on second call, got %v", err)
			}
		})
	}
}

func TestDebugPragma_JoinTracing_Flush(t *testing.T) {
	ctx := context.Background()

	// Enable tracing
	on := NewPragma(&PS.PragmaStmt{Name: "debug_join_tracing", Value: "summary"})
	_, err := on.Next(ctx)
	if err != nil {
		t.Fatalf("enable tracing error: %v", err)
	}

	// Flush — should succeed even with no events
	flush := NewPragma(&PS.PragmaStmt{Name: "debug_join_flush"})
	row, err := flush.Next(ctx)
	if err != nil {
		t.Fatalf("flush Next() error: %v", err)
	}
	if row.Cols[0] != "debug_join_flush" {
		t.Fatalf("expected col debug_join_flush, got %v", row.Cols)
	}

	// Disable
	off := NewPragma(&PS.PragmaStmt{Name: "debug_join_tracing", Value: "off"})
	_, err = off.Next(ctx)
	if err != nil {
		t.Fatalf("disable tracing error: %v", err)
	}
}