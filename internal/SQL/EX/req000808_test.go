//go:build !slt_corpus_full

package EX

import (
	"context"
	"testing"
)

// TestHexLiteral verifies REQ000808: X'...' hex string literal
// evaluates to the decoded string value.
func TestHexLiteral(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()

	tests := []struct {
		sql  string
		want string
	}{
		{"SELECT X'4142'", "AB"},
		{"SELECT x'303132'", "012"},
		{"SELECT X''", ""},
		{"SELECT X'48656C6C6F'", "Hello"},
	}

	for _, tc := range tests {
		t.Run(tc.sql, func(t *testing.T) {
			rows, err := ex.QueryAll(ctx, tc.sql)
			if err != nil {
				t.Fatalf("query failed: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("got %d rows, want 1", len(rows))
			}
			got, ok := rows[0].Data[0].ToAny().(string)
			if !ok {
				t.Fatalf("got type %T, want string", rows[0].Data[0])
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
