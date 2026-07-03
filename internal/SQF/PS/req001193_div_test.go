package PS

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// REQ001193: DIV operator parsing — double-negative and basic cases.
func TestReq001193_DivParsing(t *testing.T) {
	tests := []struct {
		sql      string
		wantOp   LX.TokenType
		wantLeft string // left operand text
	}{
		{"SELECT 20 DIV 4", LX.T_DIV, "20"},
		{"SELECT 20 DIV - - 96", LX.T_DIV, "20"},
		{"SELECT 100 DIV 3", LX.T_DIV, "100"},
	}
	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			p := NewParser(tt.sql)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("parse %q: %v", tt.sql, err)
			}
			sel, ok := stmt.(*Select)
			if !ok {
				t.Fatalf("expected Select, got %T", stmt)
			}
			if len(sel.Cols) != 1 {
				t.Fatalf("expected 1 col, got %d", len(sel.Cols))
			}
			be, ok := sel.Cols[0].(*BinaryExpr)
			if !ok {
				t.Fatalf("expected BinaryExpr, got %T", sel.Cols[0])
			}
			if be.Op != tt.wantOp {
				t.Errorf("op = %v, want %v", be.Op, tt.wantOp)
			}
		})
	}
}
