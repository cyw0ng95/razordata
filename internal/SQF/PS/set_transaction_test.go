package PS

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func TestParse_SetTransaction(t *testing.T) {
	tests := []struct {
		input string
		level string
	}{
		{"SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED", "READ UNCOMMITTED"},
		{"SET TRANSACTION ISOLATION LEVEL READ COMMITTED", "READ COMMITTED"},
		{"SET TRANSACTION ISOLATION LEVEL REPEATABLE READ", "REPEATABLE READ"},
		{"SET TRANSACTION ISOLATION LEVEL SERIALIZABLE", "SERIALIZABLE"},
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			p := NewParser(tt.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			set, ok := stmt.(*SetTransactionStmt)
			if !ok {
				t.Fatalf("expected *SetTransactionStmt, got %T", stmt)
			}
			if set.Level != tt.level {
				t.Errorf("Level = %q, want %q", set.Level, tt.level)
			}
		})
	}
}

func TestParse_SetTransaction_Invalid(t *testing.T) {
	tests := []string{
		"SET TRANSACTION ISOLATION LEVEL INVALID",
		"SET TRANSACTION ISOLATION LEVEL",
		"SET TRANSACTION",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			p := NewParser(input)
			_, err := p.Parse()
			if err == nil {
				t.Errorf("expected error for %q", input)
			}
		})
	}
}

func TestParse_SetTransaction_Tokens(t *testing.T) {
	// Verify tokens exist and are recognized
	input := "SET TRANSACTION ISOLATION LEVEL READ COMMITTED"
	p := NewParser(input)
	p.advance()
	if p.current.Type != LX.T_SET {
		t.Errorf("first token = %v, want T_SET", p.current.Type)
	}
}
