package dp

import (
	"math"
	"testing"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

func TestEncodeDecodeInt(t *testing.T) {
	original := int64(12345)
	encoded := EncodeInt(original)

	decoded, err := DecodeInt(encoded)
	if err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if decoded != original {
		t.Fatalf("expected %d, got %d", original, decoded)
	}
}

func TestEncodeDecodeFloat(t *testing.T) {
	original := float64(3.14159)
	encoded := EncodeFloat(original)

	decoded, err := DecodeFloat(encoded)
	if err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if decoded != original {
		t.Fatalf("expected %f, got %f", original, decoded)
	}
}

func TestEncodeDecodeBool(t *testing.T) {
	testCases := []bool{true, false}

	for _, original := range testCases {
		encoded := EncodeBool(original)
		decoded, err := DecodeBool(encoded)
		if err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if decoded != original {
			t.Fatalf("expected %v, got %v", original, decoded)
		}
	}
}

func TestEncodeDecodeVarchar(t *testing.T) {
	original := "hello world"
	encoded := EncodeVarchar(original)

	decoded, err := DecodeVarchar(encoded)
	if err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if decoded != original {
		t.Fatalf("expected %s, got %s", original, decoded)
	}
}

func TestEncodeDecodeText(t *testing.T) {
	original := "Hello, World!"

	encoded := EncodeText(original)
	decoded, err := DecodeText(encoded)
	if err != nil {
		t.Fatalf("DecodeText failed: %v", err)
	}
	if decoded != original {
		t.Fatalf("expected %s, got %s", original, decoded)
	}
}

func TestEncodeDecodeBlob(t *testing.T) {
	original := []byte{0xDE, 0xAD, 0xBE, 0xEF}

	encoded := EncodeBlob(original)
	decoded, err := DecodeBlob(encoded)
	if err != nil {
		t.Fatalf("DecodeBlob failed: %v", err)
	}
	if string(decoded) != string(original) {
		t.Fatalf("expected %v, got %v", original, decoded)
	}
}

func TestEncodeDecodeTimestamp(t *testing.T) {
	original := int64(1234567890)

	encoded := EncodeTimestamp(original)
	decoded, err := DecodeTimestamp(encoded)
	if err != nil {
		t.Fatalf("DecodeTimestamp failed: %v", err)
	}
	if decoded != original {
		t.Fatalf("expected %d, got %d", original, decoded)
	}
}

func TestEncodeDecodeBigInt(t *testing.T) {
	original := int64(-987654321)
	encoded := EncodeBigInt(original)
	decoded, err := DecodeBigInt(encoded)
	if err != nil {
		t.Fatalf("DecodeBigInt failed: %v", err)
	}
	if decoded != original {
		t.Fatalf("expected %d, got %d", original, decoded)
	}
}

func TestDecodeInt_InvalidLength(t *testing.T) {
	_, err := DecodeInt([]byte{1, 2, 3})
	if err != sc.ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestDecodeFloat_InvalidLength(t *testing.T) {
	_, err := DecodeFloat([]byte{1})
	if err != sc.ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestDecodeBool_InvalidLength(t *testing.T) {
	_, err := DecodeBool([]byte{1, 0})
	if err != sc.ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestDecodeTimestamp_InvalidLength(t *testing.T) {
	_, err := DecodeTimestamp([]byte{0x01})
	if err != sc.ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestEncodeVarchar(t *testing.T) {
	original := "test string"
	encoded := EncodeVarchar(original)
	if string(encoded) != original {
		t.Errorf("expected %s, got %s", original, string(encoded))
	}
}

func TestEncodeInt_AllValues(t *testing.T) {
	tests := []int64{0, 1, -1, 127, -128, 255, -255, 32767, -32768, 2147483647, -2147483648}

	for _, v := range tests {
		encoded := EncodeInt(v)
		decoded, err := DecodeInt(encoded)
		if err != nil {
			t.Errorf("DecodeInt failed for %d: %v", v, err)
		}
		if decoded != v {
			t.Errorf("expected %d, got %d", v, decoded)
		}
	}
}

func TestEncodeFloat_NaNAndInf(t *testing.T) {
	// NaN
	nan := math.NaN()
	enc := EncodeFloat(nan)
	dec, err := DecodeFloat(enc)
	if err != nil {
		t.Fatalf("DecodeFloat(NaN) error: %v", err)
	}
	if !math.IsNaN(dec) {
		t.Errorf("expected NaN, got %v", dec)
	}

	// +Inf
	posInf := math.Inf(1)
	enc = EncodeFloat(posInf)
	dec, err = DecodeFloat(enc)
	if err != nil {
		t.Fatalf("DecodeFloat(+Inf) error: %v", err)
	}
	if !math.IsInf(dec, 1) {
		t.Errorf("expected +Inf, got %v", dec)
	}

	// -Inf
	negInf := math.Inf(-1)
	enc = EncodeFloat(negInf)
	dec, err = DecodeFloat(enc)
	if err != nil {
		t.Fatalf("DecodeFloat(-Inf) error: %v", err)
	}
	if !math.IsInf(dec, -1) {
		t.Errorf("expected -Inf, got %v", dec)
	}
}

func TestEncodeFloat_ZeroAndNegZero(t *testing.T) {
	posZero := EncodeFloat(0)
	negZero := EncodeFloat(math.Copysign(0, -1))

	// +0 and -0 have different bit patterns.
	if string(posZero) == string(negZero) {
		t.Error("expected +0 and -0 to have different bit patterns")
	}

	decPos, _ := DecodeFloat(posZero)
	decNeg, _ := DecodeFloat(negZero)
	if decPos != 0 || decNeg != 0 {
		t.Errorf("expected both to decode to 0, got %v and %v", decPos, decNeg)
	}
}

func TestEncodeBlob_Empty(t *testing.T) {
	enc := EncodeBlob([]byte{})
	dec, err := DecodeBlob(enc)
	if err != nil {
		t.Fatalf("DecodeBlob error: %v", err)
	}
	if len(dec) != 0 {
		t.Errorf("expected empty blob, got %d bytes", len(dec))
	}
}
