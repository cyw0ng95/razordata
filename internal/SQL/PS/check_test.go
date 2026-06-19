package PS

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

// TestParseCheckConstraint verifies CHECK constraint parsing (REQ000210).
func TestParseCheckConstraint(t *testing.T) {
	tests := []struct {
		input string
	}{
		{"CREATE TABLE t (c INT CHECK (c > 0))"},
		{"CREATE TABLE t (c INT CHECK (c >= 1 AND c <= 100))"},
		{"CREATE TABLE t (score INT CHECK (score BETWEEN 0 AND 100))"},
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
			if ct.Cols[0].Check == nil {
				t.Error("expected Check constraint, got nil")
			}
		})
	}
}

// TestParseCheckToken verifies CHECK token is recognized.
func TestParseCheckToken(t *testing.T) {
	l := LX.NewLexer("CHECK")
	tok := l.Next()
	if tok.Type != LX.T_CHECK {
		t.Errorf("got type %v, want T_CHECK (%v)", tok.Type, LX.T_CHECK)
	}
}

// TestParseCheckMixedConstraints verifies CHECK with other constraints.
func TestParseCheckMixedConstraints(t *testing.T) {
	tests := []struct {
		input     string
		wantCheck bool
		wantPK    bool
		wantUniq  bool
	}{
		{"CREATE TABLE t (c INT CHECK (c > 0) NOT NULL)", true, false, false},
		{"CREATE TABLE t (c INT PRIMARY KEY CHECK (c > 0))", true, true, false},
		{"CREATE TABLE t (c INT UNIQUE CHECK (c > 0))", true, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p := NewParser(tt.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ct := stmt.(*CreateTable)
			if ct.Cols[0].Check == nil && tt.wantCheck {
				t.Error("expected Check, got nil")
			}
			if ct.Cols[0].PK != tt.wantPK {
				t.Errorf("PK: got %v, want %v", ct.Cols[0].PK, tt.wantPK)
			}
			if ct.Cols[0].Unique != tt.wantUniq {
				t.Errorf("Unique: got %v, want %v", ct.Cols[0].Unique, tt.wantUniq)
			}
		})
	}
}
