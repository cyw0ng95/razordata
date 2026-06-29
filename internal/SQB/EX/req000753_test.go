//go:build !slt_corpus_full

package EX

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestCompareFastPath verifies REQ000753: DT.Compare() has int64-int64
// and float64-float64 fast paths that avoid float64 conversion.
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
