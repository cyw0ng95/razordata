package LX

import (
	"testing"

	CT "github.com/cyw0ng95/razordata/internal/SYS/CT"
)

// TestREQ002287_TokenTypeAlignment proves the lexer's column-type keywords
// align with the frozen CT.ValueKind reserved codes (REQ002287).
func TestREQ002287_TokenTypeAlignment(t *testing.T) {
	cases := []struct {
		tok  TokenType
		want CT.ValueKind
	}{
		{T_INT_KW, CT.KindInt},
		{T_BIGINT, CT.KindInt},
		{T_FLOAT_KW, CT.KindFloat},
		{T_BOOL, CT.KindBool},
		{T_TEXT, CT.KindText},
		{T_VARCHAR, CT.KindText},
		{T_BLOB, CT.KindBlob},
		{T_DECIMAL, CT.KindDecimal},
		{T_NUMERIC, CT.KindDecimal},
		{T_JSON, CT.KindJSON},
		{T_DATE, CT.KindDate},
		{T_TIME, CT.KindTime},
		{T_TIMESTAMP, CT.KindTimestamp},
	}
	for _, c := range cases {
		got, ok := TokenTypeToValueKind(c.tok)
		if !ok {
			t.Fatalf("token %v not recognized as a type keyword", c.tok)
		}
		if got != c.want {
			t.Fatalf("token %v -> %d, want %d", c.tok, got, c.want)
		}
	}
	// Non-type keywords must not resolve.
	if _, ok := TokenTypeToValueKind(T_SELECT); ok {
		t.Fatalf("T_SELECT incorrectly mapped to a ValueKind")
	}
}
