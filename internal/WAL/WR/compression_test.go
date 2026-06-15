package wr

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	lf "github.com/cyw0ng95/razordata/internal/FIL/LF"
	lg "github.com/cyw0ng95/razordata/internal/LOG/LG"
	sp "github.com/cyw0ng95/razordata/internal/MEM/SP"
)

// TestCompression_RoundTrip verifies that a record written with
// compression enabled can be decoded correctly. REQ000034.
func TestCompression_RoundTrip(t *testing.T) {
	rec := &LogRecord{
		Type:  RTData,
		TxnID: 42,
		Key:   []byte("key"),
		Value: bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog "), 100),
	}
	encoded := encodeRecordCompressed(rec, true)
	if len(encoded) == 0 {
		t.Fatal("encodeRecordCompressed returned empty")
	}
	uncompressed := encodeRecordCompressed(rec, false)
	if len(encoded) >= len(uncompressed) {
		t.Logf("warning: compressed size %d >= uncompressed size %d", len(encoded), len(uncompressed))
	}
	decoded, consumed, err := DecodeRecordCompressed(encoded, 0, true)
	if err != nil {
		t.Fatalf("DecodeRecordCompressed: %v", err)
	}
	if consumed != len(encoded) {
		t.Errorf("consumed=%d, want %d", consumed, len(encoded))
	}
	if decoded.Type != rec.Type {
		t.Errorf("Type=%d, want %d", decoded.Type, rec.Type)
	}
	if decoded.TxnID != rec.TxnID {
		t.Errorf("TxnID=%d, want %d", decoded.TxnID, rec.TxnID)
	}
	if !bytes.Equal(decoded.Value, rec.Value) {
		t.Errorf("Value mismatch: got %d bytes, want %d bytes", len(decoded.Value), len(rec.Value))
	}
}

// TestCompression_HeaderFlag verifies that a compressed segment
// has the FlagCompressionLZ4 bit set in its header. REQ000034.
func TestCompression_HeaderFlag(t *testing.T) {
	dir := t.TempDir()
	sm, err := lf.New(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatalf("lf.New: %v", err)
	}
	w, err := NewWithOptions(dir, sm, sp.New(), lg.New(lg.Options{Output: &nullWriter{}}), false, Options{Compress: true})
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}
	rec := LogRecord{Type: RTCommit, TxnID: 1, BlockID: 100}
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".wal" {
			data, err := os.ReadFile(filepath.Join(dir, "wal", e.Name()))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if data[5]&FlagCompressionLZ4 == 0 {
				t.Errorf("FlagCompressionLZ4 not set: flags=0x%02x", data[5])
			}
		}
	}
}

// TestCompression_NoFlag verifies that an uncompressed segment
// does NOT have the FlagCompressionLZ4 bit set. REQ000034.
func TestCompression_NoFlag(t *testing.T) {
	dir := t.TempDir()
	sm, err := lf.New(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatalf("lf.New: %v", err)
	}
	w, err := New(dir, sm, sp.New(), lg.New(lg.Options{Output: &nullWriter{}}), false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := LogRecord{Type: RTCommit, TxnID: 1, BlockID: 100}
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".wal" {
			data, err := os.ReadFile(filepath.Join(dir, "wal", e.Name()))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if data[5]&FlagCompressionLZ4 != 0 {
				t.Errorf("FlagCompressionLZ4 should not be set: flags=0x%02x", data[5])
			}
		}
	}
}

// TestCompression_CRCOnCompressed verifies that the CRC is computed
// over the compressed body, so corruption is detected before
// decompression. REQ000034.
func TestCompression_CRCOnCompressed(t *testing.T) {
	rec := &LogRecord{
		Type:  RTData,
		TxnID: 1,
		Key:   []byte("k"),
		Value: bytes.Repeat([]byte("compressible data "), 50),
	}
	encoded := encodeRecordCompressed(rec, true)
	if len(encoded) < 20 {
		t.Fatal("encoded record too short")
	}
	encoded[len(encoded)/2] ^= 0xFF
	_, _, err := DecodeRecordCompressed(encoded, 0, true)
	if err == nil {
		t.Error("expected error from corrupted compressed body, got nil")
	}
}

// TestCompression_EmptyBody verifies that compression handles
// empty record bodies correctly (e.g., RTRollback). REQ000034.
func TestCompression_EmptyBody(t *testing.T) {
	rec := &LogRecord{Type: RTRollback, TxnID: 1}
	encoded := encodeRecordCompressed(rec, true)
	decoded, consumed, err := DecodeRecordCompressed(encoded, 0, true)
	if err != nil {
		t.Fatalf("DecodeRecordCompressed: %v", err)
	}
	if consumed != len(encoded) {
		t.Errorf("consumed=%d, want %d", consumed, len(encoded))
	}
	if decoded.Type != RTRollback {
		t.Errorf("Type=%d, want RTRollback", decoded.Type)
	}
	if decoded.TxnID != 1 {
		t.Errorf("TxnID=%d, want 1", decoded.TxnID)
	}
}

// TestCompression_IncompressibleData verifies that compression
// handles incompressible data (random bytes) without errors.
// REQ000034.
func TestCompression_IncompressibleData(t *testing.T) {
	rec := &LogRecord{
		Type:  RTData,
		TxnID: 1,
		Value: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
	}
	encoded := encodeRecordCompressed(rec, true)
	decoded, _, err := DecodeRecordCompressed(encoded, 0, true)
	if err != nil {
		t.Fatalf("DecodeRecordCompressed: %v", err)
	}
	if !bytes.Equal(decoded.Value, rec.Value) {
		t.Errorf("Value mismatch")
	}
}
