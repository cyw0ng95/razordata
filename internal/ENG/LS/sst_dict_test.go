package ls

import (
	"bytes"
	"testing"
)

// TestDictTrainer_RepetitiveInput verifies the dictionary
// captures frequent substrings. REQ000297.
func TestDictTrainer_RepetitiveInput(t *testing.T) {
	// A block with the substring "user_" repeated many times.
	dt := newDictTrainer(1024)
	block := bytes.Repeat([]byte("user_id="), 100)
	dict := dt.train(block)
	if len(dict) == 0 {
		t.Fatal("expected non-empty dictionary for repetitive input")
	}
	if !bytes.Contains(dict, []byte("user_id")) {
		t.Errorf("dictionary should contain 'user_id', got %q", dict)
	}
}

// TestCompressBlockDict_RoundTrip verifies dict-compressed blocks
// decompress to the original. REQ000297.
func TestCompressBlockDict_RoundTrip(t *testing.T) {
	cases := [][]byte{
		bytes.Repeat([]byte("user_id="), 50),
		bytes.Repeat([]byte("https://example.com/"), 30),
		// Adversarial: low repetition. Should still round-trip
		// even if dict doesn't help.
		[]byte("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"),
	}
	for i, original := range cases {
		compressed, err := compressBlockDict(original)
		if err != nil {
			t.Errorf("case %d: compress: %v", i, err)
			continue
		}
		decompressed, err := decompressBlockDict(compressed)
		if err != nil {
			t.Errorf("case %d: decompress: %v", i, err)
			continue
		}
		if !bytes.Equal(decompressed, original) {
			t.Errorf("case %d: round-trip mismatch", i)
		}
	}
}

// TestCompressBlockDict_FlagByte verifies the flag byte indicates
// the compression kind. REQ000297.
func TestCompressBlockDict_FlagByte(t *testing.T) {
	original := bytes.Repeat([]byte("user_id="), 50)
	compressed, err := compressBlockDict(original)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if len(compressed) < 1 {
		t.Fatal("empty compressed output")
	}
	flag := compressed[0]
	if flag != 0 && flag != 1 && flag != 2 {
		t.Errorf("unexpected flag byte: %d", flag)
	}
}

// TestCompressBlockDict_RejectsBadInput verifies the decoder
// rejects malformed input gracefully. REQ000297.
func TestCompressBlockDict_RejectsBadInput(t *testing.T) {
	if _, err := decompressBlockDict([]byte{99, 1, 2, 3}); err == nil {
		t.Errorf("expected error for unknown flag byte 99")
	}
}
