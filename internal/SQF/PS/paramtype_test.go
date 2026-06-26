package PS

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// TestParseVarcharSize verifies VARCHAR(N) parsing (REQ000207).
func TestParseVarcharSize(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"CREATE TABLE t (c VARCHAR)", 0}, // no size
		{"CREATE TABLE t (c VARCHAR(10))", 10},
		{"CREATE TABLE t (c VARCHAR(255))", 255},
		{"CREATE TABLE t (c VARCHAR(1))", 1},
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
			if ct.Cols[0].Type != int(LX.T_VARCHAR) {
				t.Errorf("Type: got %d, want VARCHAR (%d)", ct.Cols[0].Type, int(LX.T_VARCHAR))
			}
			if tt.want != 0 && ct.Cols[0].Size != tt.want {
				t.Errorf("Size: got %d, want %d", ct.Cols[0].Size, tt.want)
			}
		})
	}
}

// TestParseDecimalPrecisionScale verifies DECIMAL(P,S) parsing (REQ000207).
func TestParseDecimalPrecisionScale(t *testing.T) {
	tests := []struct {
		input string
		wantP int
		wantS int
	}{
		{"CREATE TABLE t (c DECIMAL)", 0, 0}, // no params
		{"CREATE TABLE t (c DECIMAL(10,2))", 10, 2},
		{"CREATE TABLE t (c DECIMAL(38,18))", 38, 18},
		{"CREATE TABLE t (c DECIMAL(5))", 5, 0},
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
			if ct.Cols[0].Type != int(LX.T_DECIMAL) {
				t.Errorf("Type: got %d, want DECIMAL (%d)", ct.Cols[0].Type, int(LX.T_DECIMAL))
			}
			if tt.wantP != 0 && ct.Cols[0].Size != tt.wantP {
				t.Errorf("Size (precision): got %d, want %d", ct.Cols[0].Size, tt.wantP)
			}
		})
	}
}

// TestParseNumericType verifies NUMERIC type parsing.
func TestParseNumericType(t *testing.T) {
	p := NewParser("CREATE TABLE t (c NUMERIC(20,5))")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ct := stmt.(*CreateTable)
	if len(ct.Cols) != 1 {
		t.Fatalf("expected 1 col, got %d", len(ct.Cols))
	}
	if ct.Cols[0].Type != int(LX.T_NUMERIC) {
		t.Errorf("Type: got %d, want NUMERIC", ct.Cols[0].Type)
	}
	if ct.Cols[0].Size != 20 {
		t.Errorf("Size: got %d, want 20", ct.Cols[0].Size)
	}
}

// TestCastExprType verifies CAST with parameterized types.
func TestCastExprType(t *testing.T) {
	p := NewParser("SELECT CAST(x AS VARCHAR(50))")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sel := stmt.(*Select)
	if len(sel.Cols) != 1 {
		t.Fatalf("expected 1 col, got %d", len(sel.Cols))
	}
	cast, ok := sel.Cols[0].(*CastExpr)
	if !ok {
		t.Fatalf("expected *CastExpr, got %T", sel.Cols[0])
	}
	if cast.Type == nil {
		t.Fatal("Type should not be nil")
	}
	if cast.Type.Type != int(LX.T_VARCHAR) {
		t.Errorf("Type: got %d, want VARCHAR", cast.Type.Type)
	}
	if cast.Type.Size != 50 {
		t.Errorf("Size: got %d, want 50", cast.Type.Size)
	}
}
