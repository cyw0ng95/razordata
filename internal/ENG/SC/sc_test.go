package sc

import (
	"math"
	"strings"
	"testing"
	"testing/quick"
)

func TestEncodeDecodeInt(t *testing.T) {
	tests := []struct {
		name  string
		value int64
	}{
		{"zero", 0},
		{"positive", 42},
		{"negative", -42},
		{"max int64", math.MaxInt64},
		{"min int64", math.MinInt64},
		{"one", 1},
		{"negative one", -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeInt(tc.value)
			decoded, err := DecodeInt(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded != tc.value {
				t.Fatalf("expected %d, got %d", tc.value, decoded)
			}
		})
	}
}

func TestDecodeIntInvalidLength(t *testing.T) {
	_, err := DecodeInt([]byte{1, 2, 3})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestEncodeDecodeFloat(t *testing.T) {
	tests := []struct {
		name  string
		value float64
	}{
		{"zero", 0},
		{"positive", 3.14},
		{"negative", -2.71},
		{"max float64", math.MaxFloat64},
		{"smallest positive", math.SmallestNonzeroFloat64},
		{"NaN", math.NaN()},
		{"Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeFloat(tc.value)
			decoded, err := DecodeFloat(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.IsNaN(tc.value) {
				if !math.IsNaN(decoded) {
					t.Fatal("expected NaN")
				}
				return
			}
			if decoded != tc.value {
				t.Fatalf("expected %v, got %v", tc.value, decoded)
			}
		})
	}
}

func TestDecodeFloatInvalidLength(t *testing.T) {
	_, err := DecodeFloat([]byte{1, 2, 3})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestEncodeDecodeBool(t *testing.T) {
	tests := []struct {
		name  string
		value bool
	}{
		{"true", true},
		{"false", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeBool(tc.value)
			decoded, err := DecodeBool(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded != tc.value {
				t.Fatalf("expected %v, got %v", tc.value, decoded)
			}
		})
	}
}

func TestDecodeBoolInvalidLength(t *testing.T) {
	_, err := DecodeBool([]byte{1, 2})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestEncodeDecodeVarchar(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"short", "hello"},
		{"with spaces", "hello world"},
		{"unicode", "你好世界"},
		{"special chars", "a\tb\nc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeVarchar(tc.value)
			decoded, err := DecodeVarchar(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded != tc.value {
				t.Fatalf("expected %q, got %q", tc.value, decoded)
			}
		})
	}
}

func TestEncodeDecodeText(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"short", "hello"},
		{"long", strings.Repeat("a", 10000)},
		{"unicode", "你好世界"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeText(tc.value)
			decoded, err := DecodeText(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded != tc.value {
				t.Fatalf("expected %q, got %q", tc.value, decoded)
			}
		})
	}
}

func TestEncodeDecodeBlob(t *testing.T) {
	tests := []struct {
		name  string
		value []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"single byte", []byte{0xFF}},
		{"multiple bytes", []byte{0x00, 0x01, 0x02, 0x03}},
		{"large", make([]byte, 10000)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeBlob(tc.value)
			decoded, err := DecodeBlob(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(tc.value) == 0 && len(decoded) == 0 {
				return
			}
			if len(decoded) != len(tc.value) {
				t.Fatalf("expected len %d, got %d", len(tc.value), len(decoded))
			}
			for i := range tc.value {
				if decoded[i] != tc.value[i] {
					t.Fatalf("byte %d: expected %d, got %d", i, tc.value[i], decoded[i])
				}
			}
		})
	}
}

func TestEncodeDecodeTimestamp(t *testing.T) {
	tests := []struct {
		name  string
		value int64
	}{
		{"zero", 0},
		{"positive", 1700000000},
		{"negative", -1},
		{"max int64", math.MaxInt64},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeTimestamp(tc.value)
			decoded, err := DecodeTimestamp(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded != tc.value {
				t.Fatalf("expected %d, got %d", tc.value, decoded)
			}
		})
	}
}

func TestDecodeTimestampInvalidLength(t *testing.T) {
	_, err := DecodeTimestamp([]byte{1, 2, 3})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestDecodeIntErrorsOnWrongLen(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"too short", []byte{1, 2, 3, 4}},
		{"too long", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeInt(tc.data)
			if err != ErrTypeMismatch {
				t.Fatalf("expected ErrTypeMismatch, got %v", err)
			}
		})
	}
}

func TestDecodeFloatErrorsOnWrongLen(t *testing.T) {
	_, err := DecodeFloat([]byte{1, 2, 3, 4})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestDecodeBoolErrorsOnWrongLen(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"too long", []byte{1, 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeBool(tc.data)
			if err != ErrTypeMismatch {
				t.Fatalf("expected ErrTypeMismatch, got %v", err)
			}
		})
	}
}

func TestEncodeDecodeIntFuzz(t *testing.T) {
	f := func(v int64) bool {
		decoded, err := DecodeInt(EncodeInt(v))
		return err == nil && decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeFloatFuzz(t *testing.T) {
	f := func(v float64) bool {
		decoded, err := DecodeFloat(EncodeFloat(v))
		if err != nil {
			return false
		}
		if math.IsNaN(v) {
			return math.IsNaN(decoded)
		}
		return decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeBoolFuzz(t *testing.T) {
	f := func(v bool) bool {
		decoded, err := DecodeBool(EncodeBool(v))
		return err == nil && decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeVarcharFuzz(t *testing.T) {
	f := func(v string) bool {
		decoded, err := DecodeVarchar(EncodeVarchar(v))
		return err == nil && decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeTextFuzz(t *testing.T) {
	f := func(v string) bool {
		decoded, err := DecodeText(EncodeText(v))
		return err == nil && decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeBlobFuzz(t *testing.T) {
	f := func(v []byte) bool {
		decoded, err := DecodeBlob(EncodeBlob(v))
		if err != nil {
			return false
		}
		if len(v) == 0 && len(decoded) == 0 {
			return true
		}
		if len(decoded) != len(v) {
			return false
		}
		for i := range v {
			if decoded[i] != v[i] {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeTimestampFuzz(t *testing.T) {
	f := func(v int64) bool {
		decoded, err := DecodeTimestamp(EncodeTimestamp(v))
		return err == nil && decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}
