package lz4

import (
	"bytes"
	"testing"
)

func TestRoundTripEmpty(t *testing.T) {
	out, err := Decompress([]byte{})
	if err != nil {
		t.Errorf("Decompress empty: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("empty input: got %d bytes, want 0", len(out))
	}
}

func TestRoundTripShort(t *testing.T) {
	// Short inputs may not compress; test that the round-trip works
	// regardless of whether compression was applied.
	cases := [][]byte{
		[]byte("a"),
		[]byte("ab"),
		[]byte("abc"),
		[]byte("abcd"),
		[]byte("hello world"),
		[]byte("hello world hello world hello world"),
	}
	for _, src := range cases {
		compressed := Compress(src)
		decompressed, err := Decompress(compressed)
		if err != nil {
			t.Errorf("Decompress(%q): %v", src, err)
			continue
		}
		if !bytes.Equal(decompressed, src) {
			t.Errorf("round-trip mismatch: got %q, want %q", decompressed, src)
		}
	}
}

func TestRoundTripRepeating(t *testing.T) {
	// Test that RLE-style matches (matchLen > offset) work.
	src := bytes.Repeat([]byte("abc"), 1000)
	compressed := Compress(src)
	if len(compressed) >= len(src) {
		t.Logf("warning: compression didn't help: %d -> %d", len(src), len(compressed))
	}
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Errorf("round-trip mismatch for RLE input")
	}
}

func TestRoundTripStructured(t *testing.T) {
	// Test with structured data that has real back-references.
	src := make([]byte, 0, 10000)
	for i := 0; i < 100; i++ {
		src = append(src, []byte("the quick brown fox jumps over the lazy dog ")...)
	}
	compressed := Compress(src)
	t.Logf("structured: %d -> %d (%.1f%%)", len(src), len(compressed), 100*float64(len(compressed))/float64(len(src)))
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Errorf("round-trip mismatch for structured input")
	}
}

func TestRoundTripBinary(t *testing.T) {
	// Binary data with patterns.
	src := make([]byte, 0, 1000)
	for i := 0; i < 250; i++ {
		src = append(src, 0, 1, 2, 3)
	}
	compressed := Compress(src)
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Errorf("round-trip mismatch for binary input")
	}
}

func TestCompressBound(t *testing.T) {
	for _, n := range []int{0, 1, 100, 10000, 1024 * 1024} {
		b := CompressBound(n)
		if b < n {
			t.Errorf("CompressBound(%d) = %d < n", n, b)
		}
	}
}
