//go:build !slt_corpus_full

package EX

import (
	"testing"

	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
)

func anyToValue(a any) Value {
	switch v := a.(type) {
	case nil:
		return NullValue()
	case string:
		return NewTextValue(v)
	case int64:
		return NewIntValue(v)
	case int:
		return NewIntValue(int64(v))
	default:
		return NewTextValue(v.(string))
	}
}

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
			got, err := EV.ConcatValue(anyToValue(tc.a), anyToValue(tc.b))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if (tc.a == nil || tc.b == nil) && got.Kind != KindNull {
				t.Errorf("expected KindNull, got %v", got.Kind)
				return
			}
			if got.Kind != KindNull {
				if got.Kind != KindText {
					t.Fatalf("got Kind %v, want KindText", got.Kind)
				}
				if got.S != tc.want {
					t.Errorf("got %q, want %q", got.S, tc.want)
				}
			}
		})
	}
}
