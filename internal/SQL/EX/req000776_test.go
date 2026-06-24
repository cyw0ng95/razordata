//go:build !slt_corpus_full

package EX

import (
	"testing"
)

// TestCompareValue verifies REQ000776: compareValue operates directly
// on Value types via Kind switching (no interface conversion).
func TestCompareValue(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		want int
	}{
		{"int_int_equal", NewIntValue(5), NewIntValue(5), 0},
		{"int_int_less", NewIntValue(3), NewIntValue(7), -1},
		{"int_int_greater", NewIntValue(9), NewIntValue(2), 1},
		{"int_int_neg", NewIntValue(-5), NewIntValue(-3), -1},
		{"float_float_equal", NewFloatValue(3.5), NewFloatValue(3.5), 0},
		{"float_float_less", NewFloatValue(1.2), NewFloatValue(3.4), -1},
		{"int_float_mixed", NewIntValue(5), NewFloatValue(3.0), 1},
		{"float_int_mixed", NewFloatValue(1.5), NewIntValue(2), -1},
		{"text_text", NewTextValue("abc"), NewTextValue("abd"), -1},
		{"text_text_eq", NewTextValue("abc"), NewTextValue("abc"), 0},
		{"bool_bool", NewBoolValue(true), NewBoolValue(false), 1},
		{"null_null", NullValue(), NullValue(), 0},
		{"null_int", NullValue(), NewIntValue(5), -1},
		{"int_null", NewIntValue(5), NullValue(), 1},
		{"blob_eq", NewBlobValue([]byte{1, 2, 3}), NewBlobValue([]byte{1, 2, 3}), 0},
		{"blob_ne", NewBlobValue([]byte{1, 2, 3}), NewBlobValue([]byte{1, 2, 4}), -1},
		{"int_text", NewIntValue(5), NewTextValue("5"), 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := compareValue(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("compareValue(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestEqualValueValue verifies REQ000776: equalValueValue switches on
// Kind to compare values without interface dispatch.
func TestEqualValueValue(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		want bool
	}{
		{"int_eq", NewIntValue(5), NewIntValue(5), true},
		{"int_ne", NewIntValue(5), NewIntValue(6), false},
		{"float_eq", NewFloatValue(3.5), NewFloatValue(3.5), true},
		{"text_eq", NewTextValue("hello"), NewTextValue("hello"), true},
		{"text_ne", NewTextValue("hello"), NewTextValue("world"), false},
		{"bool_eq", NewBoolValue(true), NewBoolValue(true), true},
		{"bool_ne", NewBoolValue(true), NewBoolValue(false), false},
		{"null_null", NullValue(), NullValue(), true},
		{"null_int", NullValue(), NewIntValue(5), false},
		{"blob_eq", NewBlobValue([]byte{1, 2, 3}), NewBlobValue([]byte{1, 2, 3}), true},
		{"blob_ne", NewBlobValue([]byte{1, 2, 3}), NewBlobValue([]byte{1, 2, 4}), false},
		{"diff_kinds", NewIntValue(5), NewTextValue("5"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := equalValueValue(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("equalValueValue(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestNumericArithValue verifies REQ000776: numericArithValue operates
// on Value directly and returns Value.
func TestNumericArithValue(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		op  rune
		want Value
	}{
		{"int_add", NewIntValue(2), NewIntValue(3), '+', NewIntValue(5)},
		{"int_sub", NewIntValue(10), NewIntValue(3), '-', NewIntValue(7)},
		{"int_mul", NewIntValue(4), NewIntValue(5), '*', NewIntValue(20)},
		{"int_zero", NewIntValue(0), NewIntValue(5), '*', NewIntValue(0)},
		{"float_add", NewFloatValue(1.5), NewFloatValue(2.5), '+', NewFloatValue(4.0)},
		{"float_mul", NewFloatValue(2.0), NewFloatValue(3.0), '*', NewFloatValue(6.0)},
		{"int_float_mixed", NewIntValue(5), NewFloatValue(2.0), '*', NewFloatValue(10.0)},
		{"null_int", NullValue(), NewIntValue(5), '+', NullValue()},
		{"int_null", NewIntValue(5), NullValue(), '+', NullValue()},
		{"text_int", NewTextValue("x"), NewIntValue(5), '+', NullValue()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := numericArithValue(tc.a, tc.b, tc.op)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("numericArithValue(%v, %v, %c) = %v, want %v",
					tc.a, tc.b, tc.op, got, tc.want)
			}
		})
	}
}
