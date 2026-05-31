package bf

import (
	"hash/crc32"
	"path/filepath"
	"testing"
)

func TestChecksumVerify(t *testing.T) {
	data := make([]byte, 4088)
	for i := range data {
		data[i] = byte(i & 0xff)
	}

	crc := crc32.ChecksumIEEE(data)

	if !ChecksumVerify(data, crc) {
		t.Error("ChecksumVerify should return true for matching CRC")
	}

	// Corrupt the data.
	data[0] ^= 0xFF
	if ChecksumVerify(data, crc) {
		t.Error("ChecksumVerify should return false for corrupted data")
	}
}

func TestChecksumVerifyEdgeCases(t *testing.T) {
	// Empty data.
	emptyCRC := crc32.ChecksumIEEE([]byte{})
	if !ChecksumVerify([]byte{}, emptyCRC) {
		t.Error("ChecksumVerify failed for empty data")
	}

	// Single byte.
	data := []byte{0xAB}
	crc := crc32.ChecksumIEEE(data)
	if !ChecksumVerify(data, crc) {
		t.Error("ChecksumVerify failed for single byte")
	}
}

func TestHintFileRoundTrip(t *testing.T) {
	// Encode and decode with multiple entries.
	entries := []hintEntry{
		{BlockID: 1, LastAccess: 100},
		{BlockID: 5, LastAccess: 200},
		{BlockID: 10, LastAccess: 300},
	}

	data, err := encodeHintEntries(entries)
	if err != nil {
		t.Fatalf("encodeHintEntries failed: %v", err)
	}

	decoded, err := decodeHintEntries(data)
	if err != nil {
		t.Fatalf("decodeHintEntries failed: %v", err)
	}

	if len(decoded) != len(entries) {
		t.Errorf("expected %d entries, got %d", len(entries), len(decoded))
	}

	for i, e := range entries {
		if decoded[i].BlockID != e.BlockID {
			t.Errorf("entry[%d].BlockID: expected %d, got %d", i, e.BlockID, decoded[i].BlockID)
		}
		if decoded[i].LastAccess != e.LastAccess {
			t.Errorf("entry[%d].LastAccess: expected %d, got %d", i, e.LastAccess, decoded[i].LastAccess)
		}
	}
}

func TestHintFileEmpty(t *testing.T) {
	// Empty entries should encode fine.
	entries := []hintEntry{}
	data, err := encodeHintEntries(entries)
	if err != nil {
		t.Fatalf("encodeHintEntries failed: %v", err)
	}

	decoded, err := decodeHintEntries(data)
	if err != nil {
		t.Fatalf("decodeHintEntries failed: %v", err)
	}

	if len(decoded) != 0 {
		t.Errorf("expected 0 entries, got %d", len(decoded))
	}
}

func TestHintFileManyEntries(t *testing.T) {
	// Encode and decode with many entries.
	var entries []hintEntry
	for i := uint64(0); i < 1000; i++ {
		entries = append(entries, hintEntry{
			BlockID:    i,
			LastAccess: int64(i * 2),
		})
	}

	data, err := encodeHintEntries(entries)
	if err != nil {
		t.Fatalf("encodeHintEntries failed: %v", err)
	}

	decoded, err := decodeHintEntries(data)
	if err != nil {
		t.Fatalf("decodeHintEntries failed: %v", err)
	}

	if len(decoded) != len(entries) {
		t.Errorf("expected %d entries, got %d", len(entries), len(decoded))
	}

	// Spot check a few entries.
	if decoded[0].BlockID != 0 || decoded[0].LastAccess != 0 {
		t.Errorf("first entry mismatch: got %+v", decoded[0])
	}
	if decoded[999].BlockID != 999 || decoded[999].LastAccess != 1998 {
		t.Errorf("last entry mismatch: got %+v", decoded[999])
	}
}

func TestHintFileCorrupt(t *testing.T) {
	// Truncated data.
	_, err := decodeHintEntries([]byte{0xFF, 0xFF})
	if err == nil {
		t.Error("expected error for truncated hint file")
	}

	// Empty data should return empty list.
	entries, err := decodeHintEntries(nil)
	if err != nil {
		t.Fatalf("decodeHintEntries(nil) should not error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for nil input, got %d", len(entries))
	}
}

func TestHintFileReadWrite(t *testing.T) {
	tmp := t.TempDir()
	hintPath := filepath.Join(tmp, "hint")

	entries := []hintEntry{
		{BlockID: 1, LastAccess: 100},
		{BlockID: 5, LastAccess: 200},
	}

	// Write hint file.
	err := writeHintFile(entries, hintPath)
	if err != nil {
		t.Fatalf("writeHintFile failed: %v", err)
	}

	// Read it back.
	read, err := readHintFile(hintPath)
	if err != nil {
		t.Fatalf("readHintFile failed: %v", err)
	}

	if len(read) != len(entries) {
		t.Errorf("expected %d entries, got %d", len(entries), len(read))
	}

	for i, e := range entries {
		if read[i].BlockID != e.BlockID {
			t.Errorf("entry[%d].BlockID: expected %d, got %d", i, e.BlockID, read[i].BlockID)
		}
		if read[i].LastAccess != e.LastAccess {
			t.Errorf("entry[%d].LastAccess: expected %d, got %d", i, e.LastAccess, read[i].LastAccess)
		}
	}
}

func TestHintFileReadMissing(t *testing.T) {
	entries, err := readHintFile("/nonexistent/path")
	if err == nil {
		t.Error("expected error for missing hint file")
	}
	_ = entries // may be nil or empty
}

func TestVarint(t *testing.T) {
	testCases := []uint64{0, 1, 127, 128, 255, 256, 65535, 65536, 1<<63 - 1}

	for _, v := range testCases {
		buf := appendVarint(nil, v)
		r := &reader{data: buf}
		decoded, n := r.readVarint()
		if n < 0 || decoded != v {
			t.Errorf("varint roundtrip failed for %d: got %d after %d bytes", v, decoded, n)
		}
	}
}

func TestHintFileWriteReadLargeEntries(t *testing.T) {
	tmp := t.TempDir()
	hintPath := filepath.Join(tmp, "hint")

	// Large block IDs and timestamps.
	entries := make([]hintEntry, 100)
	for i := range entries {
		entries[i] = hintEntry{
			BlockID:    uint64(i*1000000 + 123456),
			LastAccess: int64(i*1000000 + 654321),
		}
	}

	err := writeHintFile(entries, hintPath)
	if err != nil {
		t.Fatalf("writeHintFile failed: %v", err)
	}

	read, err := readHintFile(hintPath)
	if err != nil {
		t.Fatalf("readHintFile failed: %v", err)
	}

	if len(read) != len(entries) {
		t.Errorf("expected %d entries, got %d", len(entries), len(read))
	}

	for i, e := range entries {
		if read[i].BlockID != e.BlockID || read[i].LastAccess != e.LastAccess {
			t.Errorf("entry[%d] mismatch: expected %+v, got %+v", i, e, read[i])
		}
	}
}

func BenchmarkChecksumVerify(b *testing.B) {
	data := make([]byte, 4088)
	for i := range data {
		data[i] = byte(i & 0xff)
	}
	crc := crc32.ChecksumIEEE(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ChecksumVerify(data, crc)
	}
}

func BenchmarkHintFileEncode(b *testing.B) {
	entries := make([]hintEntry, 100)
	for i := range entries {
		entries[i] = hintEntry{BlockID: uint64(i), LastAccess: int64(i * 10)}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encodeHintEntries(entries)
	}
}

func BenchmarkHintFileDecode(b *testing.B) {
	entries := make([]hintEntry, 100)
	for i := range entries {
		entries[i] = hintEntry{BlockID: uint64(i), LastAccess: int64(i * 10)}
	}
	data, _ := encodeHintEntries(entries)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decodeHintEntries(data)
	}
}
