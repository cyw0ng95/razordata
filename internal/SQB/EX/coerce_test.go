package EX

import (
	"bytes"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestTypeToAffinity(t *testing.T) {
	tests := []struct {
		name     string
		typeInfo *PS.TypeInfo
		want     Affinity
	}{
		// TEXT affinity
		{"TEXT", &PS.TypeInfo{Type: int(LX.T_TEXT)}, AffinityText},
		{"VARCHAR", &PS.TypeInfo{Type: int(LX.T_VARCHAR)}, AffinityText},

		// INTEGER affinity
		{"INT", &PS.TypeInfo{Type: int(LX.T_INT_KW)}, AffinityInteger},
		{"INT_KW", &PS.TypeInfo{Type: int(LX.T_INT)}, AffinityInteger},
		{"BIGINT", &PS.TypeInfo{Type: int(LX.T_BIGINT)}, AffinityInteger},

		// REAL affinity
		{"FLOAT", &PS.TypeInfo{Type: int(LX.T_FLOAT_KW)}, AffinityReal},
		{"FLOAT_KW", &PS.TypeInfo{Type: int(LX.T_FLOAT)}, AffinityReal},

		// NUMERIC affinity
		{"NUMERIC", &PS.TypeInfo{Type: int(LX.T_NUMERIC)}, AffinityNumeric},
		{"DECIMAL", &PS.TypeInfo{Type: int(LX.T_DECIMAL)}, AffinityNumeric},
		{"BOOL", &PS.TypeInfo{Type: int(LX.T_BOOL)}, AffinityNumeric},

		// NONE affinity (default)
		{"BLOB", &PS.TypeInfo{Type: int(LX.T_BLOB)}, AffinityNone},
		{"nil", nil, AffinityNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TypeToAffinity(tt.typeInfo)
			if got != tt.want {
				t.Errorf("TypeToAffinity(%v) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestApplyAffinity_Text(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"string", "hello", "hello"},
		{"int64", int64(123), "123"},
		{"float64", float64(3.14), "3.14"},
		{"bool-true", true, "true"},
		{"bool-false", false, "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplyAffinity(tt.in, AffinityText)
			if err != nil {
				t.Fatalf("ApplyAffinity() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ApplyAffinity(%v, TEXT) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestApplyAffinity_Integer(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"int64", int64(123), int64(123)},
		{"float64-trunc", float64(3.14), int64(3)},
		{"float64-trunc-2", float64(99.99), int64(99)},
		{"string-int", "456", int64(456)},
		{"string-float", "3.7", int64(3)},
		{"bool-true", true, int64(1)},
		{"bool-false", false, int64(0)},
		{"unconvertible", "not-a-number", "not-a-number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplyAffinity(tt.in, AffinityInteger)
			if err != nil {
				t.Fatalf("ApplyAffinity() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ApplyAffinity(%v, INTEGER) = %v (%T), want %v (%T)", tt.in, got, got, tt.want, tt.want)
			}
		})
	}
}

func TestApplyAffinity_Real(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"float64", float64(3.14), float64(3.14)},
		{"int64", int64(123), float64(123)},
		{"string", "2.718", float64(2.718)},
		{"unconvertible", "not-a-number", "not-a-number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplyAffinity(tt.in, AffinityReal)
			if err != nil {
				t.Fatalf("ApplyAffinity() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ApplyAffinity(%v, REAL) = %v (%T), want %v (%T)", tt.in, got, got, tt.want, tt.want)
			}
		})
	}
}

func TestApplyAffinity_Numeric(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"int64", int64(123), int64(123)},
		{"float64", float64(3.14), float64(3.14)},
		{"string-float", "3.14", float64(3.14)},
		{"string-int", "456", float64(456)},
		{"bool-true", true, int64(1)},
		{"bool-false", false, int64(0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplyAffinity(tt.in, AffinityNumeric)
			if err != nil {
				t.Fatalf("ApplyAffinity() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ApplyAffinity(%v, NUMERIC) = %v (%T), want %v (%T)", tt.in, got, got, tt.want, tt.want)
			}
		})
	}
}

func TestApplyAffinity_None(t *testing.T) {
	original := []byte{1, 2, 3}
	got, err := ApplyAffinity(original, AffinityNone)
	if err != nil {
		t.Fatalf("ApplyAffinity() error = %v", err)
	}
	if !bytes.Equal(got.([]byte), original) {
		t.Errorf("ApplyAffinity(None) should return original, got %v", got)
	}
}

func TestApplyAffinity_Nil(t *testing.T) {
	got, err := ApplyAffinity(nil, AffinityText)
	if err != nil {
		t.Fatalf("ApplyAffinity() error = %v", err)
	}
	if got != nil {
		t.Errorf("ApplyAffinity(nil) = %v, want nil", got)
	}
}

func TestCompareWithAffinity(t *testing.T) {
	tests := []struct {
		name string
		a    any
		b    any
		aff  Affinity
		want int
	}{
		// String comparison (TEXT)
		{"text-lt", "apple", "banana", AffinityText, -1},
		{"text-eq", "cherry", "cherry", AffinityText, 0},
		{"text-gt", "zebra", "apple", AffinityText, 1},

		// Integer comparison (INTEGER)
		{"int-lt", int64(10), int64(20), AffinityInteger, -1},
		{"int-eq", int64(5), int64(5), AffinityInteger, 0},
		{"int-gt", int64(100), int64(50), AffinityInteger, 1},

		// Real comparison (REAL)
		{"real-lt", float64(1.5), float64(2.5), AffinityReal, -1},
		{"real-eq", float64(3.14), float64(3.14), AffinityReal, 0},

		// Numeric comparison
		{"num-lt", "10", "20", AffinityNumeric, -1},
		{"num-gt", "3.14", "2.71", AffinityNumeric, 1},

		// Nil handling
		{"nil-lt", nil, int64(1), AffinityNumeric, -1},
		{"nil-gt", int64(1), nil, AffinityNumeric, 1},
		{"nil-eq", nil, nil, AffinityNumeric, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CompareWithAffinity(tt.a, tt.b, tt.aff)
			if err != nil {
				t.Fatalf("CompareWithAffinity() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("CompareWithAffinity(%v, %v, %v) = %v, want %v", tt.a, tt.b, tt.aff, got, tt.want)
			}
		})
	}
}
