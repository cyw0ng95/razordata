package wr

import (
	"testing"
)

// TestEncodeRecord_OnePassCorrectness verifies the single-pass
// encode produces the same bytes as the round-trip decoder
// accepts. This is a regression test for the two-pass → one-pass
// refactor (REQ000343).
func TestEncodeRecord_OnePassCorrectness(t *testing.T) {
	cases := []struct {
		name string
		rec  *LogRecord
	}{
		{"rtdata_small", &LogRecord{
			Type: RTData, TxnID: 1, BlockID: 100, Value: []byte("hi"),
		}},
		{"rtdata_large", &LogRecord{
			Type: RTData, TxnID: 12345, BlockID: 0xDEADBEEFCAFE,
			Value: []byte("the quick brown fox jumps over the lazy dog"),
		}},
		{"rtcommit", &LogRecord{
			Type: RTCommit, TxnID: 99, BlockID: 1234567890,
		}},
		{"rtrollback", &LogRecord{
			Type: RTRollback, TxnID: 100,
		}},
		{"rtmerge_empty", &LogRecord{
			Type: RTMerge, TxnID: 50, BlockID: 7, Key: []byte{}, Value: []byte{},
		}},
		{"rtmerge_populated", &LogRecord{
			Type: RTMerge, TxnID: 60, BlockID: 8,
			Key:   []byte{1, 2, 3},
			Value: []byte{4, 5, 6, 7},
		}},
		{"rtcheckpoint", &LogRecord{
			Type: RTCheckpoint, TxnID: 200, BlockID: 5,
			Key:   make([]byte, 24), // zeros
			Value: []byte{1, 2, 3},
		}},
		{"unknown_type", &LogRecord{
			Type: 99, TxnID: 1, Value: []byte{1, 2, 3},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enc := encodeRecord(c.rec)
			dec, consumed, err := decodeRecord(enc, 0)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if consumed != len(enc) {
				t.Errorf("consumed=%d, encoded=%d", consumed, len(enc))
			}
			if dec.TxnID != c.rec.TxnID {
				t.Errorf("TxnID: got %d, want %d", dec.TxnID, c.rec.TxnID)
			}
			if dec.Type != c.rec.Type {
				t.Errorf("Type: got %d, want %d", dec.Type, c.rec.Type)
			}
		})
	}
}

// BenchmarkEncodeRecord measures the encode cost per record.
func BenchmarkEncodeRecord(b *testing.B) {
	rec := &LogRecord{
		Type:    RTData,
		TxnID:   12345,
		BlockID: 0xDEADBEEFCAFE,
		Value:   []byte("the quick brown fox jumps over the lazy dog"),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = encodeRecord(rec)
	}
}
