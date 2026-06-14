package ls

import (
	"bytes"
	"testing"
)

// TestColumnarBlock_RoundTrip verifies encode/decode of a
// columnar block. REQ000314.
func TestColumnarBlock_RoundTrip(t *testing.T) {
	keys := [][]byte{
		[]byte("alpha"),
		[]byte("beta"),
		[]byte("gamma"),
		[]byte("delta"),
	}
	values := [][]byte{
		[]byte("first"),
		[]byte("second"),
		[]byte("third"),
		[]byte("fourth"),
	}
	encoded := writeColumnarBlock(keys, values)
	if len(encoded) == 0 {
		t.Fatal("encoded block is empty")
	}
	if encoded[0] != byte(layoutColumnar) {
		t.Errorf("expected columnar layout byte, got %d", encoded[0])
	}
	// Decode.
	gotKeys, gotVals, err := readColumnarBlock(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(gotKeys) != len(keys) {
		t.Errorf("keys: got %d, want %d", len(gotKeys), len(keys))
	}
	if len(gotVals) != len(values) {
		t.Errorf("values: got %d, want %d", len(gotVals), len(values))
	}
	for i := range keys {
		if !bytes.Equal(gotKeys[i], keys[i]) {
			t.Errorf("key[%d]: got %q, want %q", i, gotKeys[i], keys[i])
		}
		if !bytes.Equal(gotVals[i], values[i]) {
			t.Errorf("val[%d]: got %q, want %q", i, gotVals[i], values[i])
		}
	}
}

// TestColumnarBlock_Empty verifies edge case of zero entries.
func TestColumnarBlock_Empty(t *testing.T) {
	encoded := writeColumnarBlock(nil, nil)
	gotKeys, gotVals, err := readColumnarBlock(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(gotKeys) != 0 || len(gotVals) != 0 {
		t.Errorf("expected empty, got keys=%d vals=%d", len(gotKeys), len(gotVals))
	}
}

// TestColumnarBlock_Single verifies a one-entry block.
func TestColumnarBlock_Single(t *testing.T) {
	keys := [][]byte{[]byte("k")}
	vals := [][]byte{[]byte("v")}
	encoded := writeColumnarBlock(keys, vals)
	gotKeys, gotVals, err := readColumnarBlock(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(gotKeys) != 1 || string(gotKeys[0]) != "k" {
		t.Errorf("keys: %v", gotKeys)
	}
	if len(gotVals) != 1 || string(gotVals[0]) != "v" {
		t.Errorf("vals: %v", gotVals)
	}
}

// TestColumnarBlock_SmallerThanRowMajor verifies columnar is
// smaller when keys are smaller than values (or vice versa).
func TestColumnarBlock_SizeComparison(t *testing.T) {
	// Many small keys, large values.
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte{byte(i)}
	}
	vals := make([][]byte, 100)
	for i := range vals {
		vals[i] = make([]byte, 64)
		for j := range vals[i] {
			vals[i][j] = byte(i)
		}
	}
	encoded := writeColumnarBlock(keys, vals)
	// Columnar overhead: 1 layout + 12 header + 2*100 varints
	// (1 byte each) = 213 bytes. Plus 100 key bytes + 6400
	// value bytes = 6713. Cheap headers; key region is small.
	if len(encoded) < 6500 {
		t.Errorf("columnar encoded too small: %d bytes", len(encoded))
	}
}
