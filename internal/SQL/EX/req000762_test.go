//go:build !slt_corpus_full

package EX

import (
	"testing"
)

// TestConcatFastPath verifies REQ000762: concat has string-string
// fast path that avoids fmt.Sprintf reflection overhead.
func TestConcatFastPath(t *testing.T) {
	tests := []struct {
		name string
		a, b any
		want string
	}{
		{"str_str", "hello", " world", "hello world"},
		{"str_int", "x", int64(5), "x5"},
		{"int_str", int64(3), "y", "3y"},
		{"nil_left", nil, "x", ""},
		{"nil_right", "x", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := concat(tc.a, tc.b)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if (tc.a == nil || tc.b == nil) && got != nil {
				t.Errorf("expected nil, got %v", got)
				return
			}
			if got != nil {
				s, ok := got.(string)
				if !ok {
					t.Fatalf("got type %T, want string", got)
				}
				if s != tc.want {
					t.Errorf("got %q, want %q", s, tc.want)
				}
			}
		})
	}
}
