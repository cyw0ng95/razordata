package LX

import "testing"

// TestNewTypeTokens verifies new SQL type tokens (REQ000206).
func TestNewTypeTokens(t *testing.T) {
	tests := []struct {
		name  string
		token TokenType
		want  string
	}{
		{"NUMERIC", T_NUMERIC, "NUMERIC"},
		{"DATE", T_DATE, "DATE"},
		{"TIME", T_TIME, "TIME"},
		{"JSON", T_JSON, "JSON"},
		{"DECIMAL", T_DECIMAL, "DECIMAL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := Token{Type: tt.token}
			got := tok.String()
			if got != tt.want+":" {
				t.Errorf("%s.String(): got %q, want %q", tt.name, got, tt.want+":")
			}
		})
	}
}

// TestNewTypeKeywords verifies new type keywords are recognized.
func TestNewTypeKeywords(t *testing.T) {
	tests := []struct {
		input string
		want  TokenType
	}{
		{"NUMERIC", T_NUMERIC},
		{"DATE", T_DATE},
		{"TIME", T_TIME},
		{"JSON", T_JSON},
		{"DECIMAL", T_DECIMAL},
		{"numeric", T_NUMERIC},
		{"date", T_DATE},
		{"time", T_TIME},
		{"json", T_JSON},
		{"decimal", T_DECIMAL},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			l := NewLexer(tt.input)
			tok := l.Next()
			if tok.Type != tt.want {
				t.Errorf("got type %v, want %v", tok.Type, tt.want)
			}
		})
	}
}
