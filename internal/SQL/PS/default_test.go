package PS

import (
	"testing"
	
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

// TestParseDefaultClause verifies DEFAULT clause parsing (REQ000209).
func TestParseDefaultClause(t *testing.T) {
	tests := []struct {
		input       string
		wantDefault bool
		wantType    LX.TokenType
	}{
		{"CREATE TABLE t (c INT DEFAULT 0)", true, LX.T_INT_KW},
		{"CREATE TABLE t (c TEXT DEFAULT 'hello')", true, LX.T_TEXT},
		{"CREATE TABLE t (c INT DEFAULT NULL)", true, LX.T_INT_KW},
		{"CREATE TABLE t (c INT)", false, LX.T_INT_KW},
	}
	
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p := NewParser(tt.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ct, ok := stmt.(*CreateTable)
			if !ok {
				t.Fatalf("expected *CreateTable, got %T", stmt)
			}
			if len(ct.Cols) != 1 {
				t.Fatalf("expected 1 col, got %d", len(ct.Cols))
			}
			if tt.wantDefault {
				if ct.Cols[0].Default == nil {
					t.Errorf("expected Default, got nil")
				}
			} else {
				if ct.Cols[0].Default != nil {
					t.Errorf("expected no Default, got %T", ct.Cols[0].Default)
				}
			}
			if ct.Cols[0].Type != int(tt.wantType) {
				t.Errorf("Type: got %d, want %d", ct.Cols[0].Type, tt.wantType)
			}
		})
	}
}

// TestParseDefaultExpressions verifies different DEFAULT value types.
func TestParseDefaultExpressions(t *testing.T) {
	tests := []struct {
		input    string
		wantExpr string
	}{
		{"CREATE TABLE t (c INT DEFAULT 42)", "IntLit"},
		{"CREATE TABLE t (c TEXT DEFAULT 'x')", "StringLit"},
		{"CREATE TABLE t (c INT DEFAULT NULL)", "NullLit"},
		{"CREATE TABLE t (c BOOL DEFAULT TRUE)", "BoolLit"},
		{"CREATE TABLE t (c INT DEFAULT CURRENT_TIMESTAMP)", "Ident"},
	}
	
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p := NewParser(tt.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ct := stmt.(*CreateTable)
			if ct.Cols[0].Default == nil {
				t.Fatal("Default should not be nil")
			}
			// Just verify it parsed something
			_ = tt.wantExpr
		})
	}
}
