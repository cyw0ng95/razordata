package wr

import (
	"bytes"
	"testing"
)

// TestColumnar_RoundTrip verifies encode/decode of an RTData batch.
// REQ000299.
func TestColumnar_RoundTrip(t *testing.T) {
	recs := []*LogRecord{
		{Type: RTData, TxnID: 1, BlockID: 100, Value: []byte("hello")},
		{Type: RTData, TxnID: 1, BlockID: 101, Value: []byte("world")},
		{Type: RTData, TxnID: 1, BlockID: 102, Value: []byte("foo bar")},
		{Type: RTData, TxnID: 1, BlockID: 103, Value: []byte("baz")},
	}
	batch := NewColumnarBatch(RTData, recs)
	encoded := EncodeColumnar(batch)
	if encoded == nil {
		t.Fatal("EncodeColumnar returned nil for >=4 records")
	}
	// Decode
	decoded, n, err := DecodeColumnar(encoded, 0)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != len(encoded) {
		t.Errorf("consumed %d, encoded %d", n, len(encoded))
	}
	if len(decoded) != len(recs) {
		t.Fatalf("decoded %d records, want %d", len(decoded), len(recs))
	}
	for i, r := range recs {
		if decoded[i].TxnID != r.TxnID {
			t.Errorf("record %d txnID: %d != %d", i, decoded[i].TxnID, r.TxnID)
		}
		if decoded[i].BlockID != r.BlockID {
			t.Errorf("record %d blockID: %d != %d", i, decoded[i].BlockID, r.BlockID)
		}
		if !bytes.Equal(decoded[i].Value, r.Value) {
			t.Errorf("record %d value: %q != %q", i, decoded[i].Value, r.Value)
		}
	}
}

// TestColumnar_BelowThreshold verifies <4 records return nil.
// REQ000299.
func TestColumnar_BelowThreshold(t *testing.T) {
	recs := []*LogRecord{
		{Type: RTData, TxnID: 1, BlockID: 100, Value: []byte("a")},
	}
	if encoded := EncodeColumnar(NewColumnarBatch(RTData, recs)); encoded != nil {
		t.Errorf("expected nil for <4 records, got %d bytes", len(encoded))
	}
}

// TestColumnar_NonRTData verifies non-RTData batches return nil.
func TestColumnar_NonRTData(t *testing.T) {
	recs := []*LogRecord{
		{Type: RTCommit, TxnID: 1, BlockID: 1},
		{Type: RTCommit, TxnID: 1, BlockID: 2},
		{Type: RTCommit, TxnID: 1, BlockID: 3},
		{Type: RTCommit, TxnID: 1, BlockID: 4},
	}
	if encoded := EncodeColumnar(NewColumnarBatch(RTCommit, recs)); encoded != nil {
		t.Errorf("expected nil for non-RTData, got %d bytes", len(encoded))
	}
}

// TestColumnar_LargePayload verifies the decoder handles large
// values that span multiple varint bytes for length.
func TestColumnar_LargePayload(t *testing.T) {
	bigVal := make([]byte, 4096)
	for i := range bigVal {
		bigVal[i] = byte(i)
	}
	recs := []*LogRecord{
		{Type: RTData, TxnID: 1, BlockID: 1, Value: bigVal},
		{Type: RTData, TxnID: 1, BlockID: 2, Value: bigVal},
		{Type: RTData, TxnID: 1, BlockID: 3, Value: bigVal},
		{Type: RTData, TxnID: 1, BlockID: 4, Value: bigVal},
	}
	encoded := EncodeColumnar(NewColumnarBatch(RTData, recs))
	decoded, _, err := DecodeColumnar(encoded, 0)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i, r := range recs {
		if !bytes.Equal(decoded[i].Value, r.Value) {
			t.Errorf("record %d: value mismatch", i)
		}
	}
}

// TestColumnar_CorruptCRC verifies the decoder rejects bad CRCs.
func TestColumnar_CorruptCRC(t *testing.T) {
	recs := []*LogRecord{
		{Type: RTData, TxnID: 1, BlockID: 1, Value: []byte("a")},
		{Type: RTData, TxnID: 1, BlockID: 2, Value: []byte("b")},
		{Type: RTData, TxnID: 1, BlockID: 3, Value: []byte("c")},
		{Type: RTData, TxnID: 1, BlockID: 4, Value: []byte("d")},
	}
	encoded := EncodeColumnar(NewColumnarBatch(RTData, recs))
	// Corrupt the CRC (last 4 bytes).
	encoded[len(encoded)-1] ^= 0xFF
	_, _, err := DecodeColumnar(encoded, 0)
	if err != ErrCorrupt {
		t.Errorf("expected ErrCorrupt, got %v", err)
	}
}
