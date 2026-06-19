package ls

import (
	"math"
	"testing"
)

func TestEncodeDecodeVarint_Roundtrip(t *testing.T) {
	cases := []struct {
		name string
		val  int64
	}{
		{"zero", 0},
		{"max single byte", 127},
		{"min two byte", 128},
		{"max int64", math.MaxInt64},
		{"min int64", math.MinInt64},
		{"max uint64 as int64", math.MaxInt64},
		{"negative one", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := encodeVarint(tc.val)
			got, n := decodeVarint(enc)
			if n != len(enc) {
				t.Errorf("decodeVarint consumed %d bytes, want %d", n, len(enc))
			}
			if got != tc.val {
				t.Errorf("round-trip: got %d, want %d", got, tc.val)
			}
		})
	}
}

func TestEncodeDecodeVarint_Boundary(t *testing.T) {
	vals := []int64{
		0, 1, 127, 128, 255, 256,
		1<<14 - 1, 1 << 14,
		1<<21 - 1, 1 << 21,
		1<<28 - 1, 1 << 28,
		1<<35 - 1, 1 << 35,
		1<<42 - 1, 1 << 42,
		1<<49 - 1, 1 << 49,
		1<<56 - 1, 1 << 56,
		1<<63 - 1,
		-1, -127, -128,
		math.MinInt64, math.MaxInt64,
	}
	for _, v := range vals {
		t.Run("", func(t *testing.T) {
			enc := encodeVarint(v)
			got, n := decodeVarint(enc)
			if n != len(enc) {
				t.Errorf("decodeVarint consumed %d bytes, want %d", n, len(enc))
			}
			if got != v {
				t.Errorf("encode(decode(x)): got %d, want %d", got, v)
			}
		})
	}
}

func TestDecodeVarint_Truncated(t *testing.T) {
	truncated := []byte{0xff, 0xff, 0xff}
	got, n := decodeVarint(truncated)
	if n != len(truncated) {
		t.Errorf("truncated input: consumed %d bytes, want %d", n, len(truncated))
	}
	if got == 0 {
		t.Log("truncated input returned 0, indicating incomplete data")
	}
}

func TestDecodeVarint_Empty(t *testing.T) {
	got, n := decodeVarint(nil)
	if n != 0 {
		t.Errorf("empty input: consumed %d bytes, want 0", n)
	}
	if got != 0 {
		t.Errorf("empty input: got %d, want 0", got)
	}
}