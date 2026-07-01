//go:build !slt_corpus_full

package EX

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

func anyToValue(a any) DT.Value {
	switch v := a.(type) {
	case nil:
		return DT.NullValue()
	case string:
		return DT.NewTextValue(v)
	case int64:
		return DT.NewIntValue(v)
	case int:
		return DT.NewIntValue(int64(v))
	default:
		return DT.NewTextValue(v.(string))
	}
}

// TestCompareFastPath verifies REQ000753.
func TestCompareFastPath(t *testing.T) {
	tests := []struct {
		name string
		a, b any
		want int
	}{
		{"int_equal", int64(5), int64(5), 0},
		{"int_less", int64(3), int64(7), -1},
		{"int_greater", int64(9), int64(2), 1},
		{"int_neg", int64(-5), int64(-3), -1},
		{"int_large", int64(1 << 60), int64(1 << 59), 1},
		{"float_equal", float64(3.5), float64(3.5), 0},
		{"float_less", float64(1.2), float64(3.4), -1},
		{"float_greater", float64(7.8), float64(2.3), 1},
		{"mixed_int_float", int64(5), float64(3.0), 1},
		{"mixed_float_int", float64(1.5), int64(2), -1},
		{"nil_nil", nil, nil, 0},
		{"nil_left", nil, int64(5), -1},
		{"nil_right", int64(5), nil, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DT.Compare(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("DT.Compare(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestConcatFastPath verifies REQ000762.
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
			if (tc.a == nil || tc.b == nil) && got.Kind != DT.KindNull {
				t.Errorf("expected KindNull, got %v", got.Kind)
				return
			}
			if got.Kind != DT.KindNull {
				if got.Kind != DT.KindText {
					t.Fatalf("got Kind %v, want KindText", got.Kind)
				}
				if got.S != tc.want {
					t.Errorf("got %q, want %q", got.S, tc.want)
				}
			}
		})
	}
}

// TestCompareValue verifies REQ000776.
func TestCompareValue(t *testing.T) {
	tests := []struct {
		name string
		a, b DT.Value
		want int
	}{
		{"int_int_equal", DT.NewIntValue(5), DT.NewIntValue(5), 0},
		{"int_int_less", DT.NewIntValue(3), DT.NewIntValue(7), -1},
		{"int_int_greater", DT.NewIntValue(9), DT.NewIntValue(2), 1},
		{"int_int_neg", DT.NewIntValue(-5), DT.NewIntValue(-3), -1},
		{"float_float_equal", DT.NewFloatValue(3.5), DT.NewFloatValue(3.5), 0},
		{"float_float_less", DT.NewFloatValue(1.2), DT.NewFloatValue(3.4), -1},
		{"int_float_mixed", DT.NewIntValue(5), DT.NewFloatValue(3.0), 1},
		{"float_int_mixed", DT.NewFloatValue(1.5), DT.NewIntValue(2), -1},
		{"text_text", DT.NewTextValue("abc"), DT.NewTextValue("abd"), -1},
		{"text_text_eq", DT.NewTextValue("abc"), DT.NewTextValue("abc"), 0},
		{"bool_bool", DT.NewBoolValue(true), DT.NewBoolValue(false), 1},
		{"null_null", DT.NullValue(), DT.NullValue(), 0},
		{"null_int", DT.NullValue(), DT.NewIntValue(5), -1},
		{"int_null", DT.NewIntValue(5), DT.NullValue(), 1},
		{"blob_eq", DT.NewBlobValue([]byte{1, 2, 3}), DT.NewBlobValue([]byte{1, 2, 3}), 0},
		{"blob_ne", DT.NewBlobValue([]byte{1, 2, 3}), DT.NewBlobValue([]byte{1, 2, 4}), -1},
		{"int_text", DT.NewIntValue(5), DT.NewTextValue("5"), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pl.CompareValue(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("pl.CompareValue(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}