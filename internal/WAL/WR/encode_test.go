package wr

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestEncodeVarintRoundTrip checks the basic varint helper for several
// value sizes (R02 varint encoding).
func TestEncodeVarintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 255, 16383, 16384, 1 << 30, 1<<64 - 1}
	for _, v := range cases {
		buf := encodeVarint(nil, v)
		got, n, err := decodeVarint(buf, 0)
		if err != nil {
			t.Errorf("decodeVarint(%d): %v", v, err)
		}
		if got != v {
			t.Errorf("round-trip: got %d, want %d", got, v)
		}
		if n != len(buf) {
			t.Errorf("byte count: got %d, want %d", n, len(buf))
		}
	}
}

// TestDecodeVarintPastEnd verifies the graceful EOF case (R26): a
// varint that would read past the buffer returns bytes=-1, no error.
func TestDecodeVarintPastEnd(t *testing.T) {
	// Encode 128 (2 bytes), then truncate to 1.
	buf := encodeVarint(nil, 128)
	buf = buf[:1]
	_, n, err := decodeVarint(buf, 0)
	if n != -1 {
		t.Errorf("expected n=-1 for truncated varint, got %d", n)
	}
	if err != nil {
		t.Errorf("expected no error for truncated varint, got %v", err)
	}
}

// TestDecodeVarintEmpty verifies reading from an empty buffer.
func TestDecodeVarintEmpty(t *testing.T) {
	_, n, err := decodeVarint(nil, 0)
	if n != -1 || err != nil {
		t.Errorf("expected n=-1, err=nil; got n=%d, err=%v", n, err)
	}
}

// TestEncodeRecordHeader verifies the encoded record's length prefix
// matches the body length (R02).
func TestEncodeRecordHeader(t *testing.T) {
	rec := &LogRecord{
		Type:  RTRollback,
		TxnID: 42,
	}
	enc := encodeRecord(rec)
	if len(enc) == 0 {
		t.Fatal("encodeRecord returned empty slice")
	}
	length, n, err := decodeVarint(enc, 0)
	if err != nil {
		t.Fatalf("decodeVarint: %v", err)
	}
	if int(length) != len(enc)-n {
		t.Errorf("length prefix: got %d, want %d", length, len(enc)-n)
	}
}

// TestRoundTripData verifies RTData round-trips losslessly (R02,
// R34's checksum-not-verified-here contract).
func TestRoundTripData(t *testing.T) {
	original := &LogRecord{
		Type:    RTData,
		TxnID:   12345,
		BlockID: 0xDEADBEEFCAFE,
		Value:   []byte("the quick brown fox jumps over the lazy dog"),
	}
	enc := encodeRecord(original)

	decoded, consumed, err := decodeRecord(enc, 0)
	if err != nil {
		t.Fatalf("decodeRecord: %v", err)
	}
	if consumed != len(enc) {
		t.Errorf("consumed %d bytes, encoded %d", consumed, len(enc))
	}
	if decoded.Type != original.Type {
		t.Errorf("Type: got %d, want %d", decoded.Type, original.Type)
	}
	if decoded.TxnID != original.TxnID {
		t.Errorf("TxnID: got %d, want %d", decoded.TxnID, original.TxnID)
	}
	if decoded.BlockID != original.BlockID {
		t.Errorf("BlockID: got %x, want %x", decoded.BlockID, original.BlockID)
	}
	if !bytes.Equal(decoded.Value, original.Value) {
		t.Errorf("Value: got %q, want %q", decoded.Value, original.Value)
	}
}

// TestRoundTripCommit verifies RTCommit round-trips (R02).
func TestRoundTripCommit(t *testing.T) {
	original := &LogRecord{
		Type:    RTCommit,
		TxnID:   99,
		BlockID: 0x1122334455667788, // commitTS encoded here
	}
	enc := encodeRecord(original)
	decoded, _, err := decodeRecord(enc, 0)
	if err != nil {
		t.Fatalf("decodeRecord: %v", err)
	}
	if decoded.Type != RTCommit {
		t.Errorf("Type: got %d, want %d", decoded.Type, RTCommit)
	}
	if decoded.TxnID != 99 {
		t.Errorf("TxnID: got %d, want 99", decoded.TxnID)
	}
	if decoded.BlockID != 0x1122334455667788 {
		t.Errorf("commitTS: got %x, want %x", decoded.BlockID, 0x1122334455667788)
	}
}

// TestRoundTripRollback verifies RTRollback (empty payload) round-trips.
func TestRoundTripRollback(t *testing.T) {
	original := &LogRecord{Type: RTRollback, TxnID: 7}
	enc := encodeRecord(original)
	decoded, consumed, err := decodeRecord(enc, 0)
	if err != nil {
		t.Fatalf("decodeRecord: %v", err)
	}
	if consumed != len(enc) {
		t.Errorf("consumed %d, encoded %d", consumed, len(enc))
	}
	if decoded.Type != RTRollback {
		t.Errorf("Type: got %d, want %d", decoded.Type, RTRollback)
	}
	if decoded.TxnID != 7 {
		t.Errorf("TxnID: got %d, want 7", decoded.TxnID)
	}
}

// TestRoundTripCheckpoint verifies RTCheckpoint round-trips.
func TestRoundTripCheckpoint(t *testing.T) {
	cp := &Checkpoint{
		LSN:              12345,
		CatalogRootPtr:   67890,
		ManifestChecksum: 0xABCD1234,
		ActiveTXNs:       []uint64{100, 200, 300},
	}
	header, txns := AppendCheckpointPayload(cp)
	original := &LogRecord{
		Type:    RTCheckpoint,
		TxnID:   1, // checkpoints are not transaction-scoped; 1 is conventional
		Key:     header,
		Value:   txns,
		BlockID: uint64(len(cp.ActiveTXNs)), // count of active TXNs
	}
	enc := encodeRecord(original)
	decoded, consumed, err := decodeRecord(enc, 0)
	if err != nil {
		t.Fatalf("decodeRecord: %v", err)
	}
	if consumed != len(enc) {
		t.Errorf("consumed %d bytes, encoded %d", consumed, len(enc))
	}
	if decoded.Type != RTCheckpoint {
		t.Errorf("Type: got %d, want %d", decoded.Type, RTCheckpoint)
	}
	if !bytes.Equal(decoded.Key, header) {
		t.Errorf("Key header mismatch: got %x, want %x", decoded.Key, header)
	}
	if decoded.BlockID != uint64(len(cp.ActiveTXNs)) {
		t.Errorf("BlockID (count): got %d, want %d", decoded.BlockID, len(cp.ActiveTXNs))
	}
	// decoded.Value holds the varint-encoded TXN IDs concatenated;
	// verify by re-decoding each.
	rest := decoded.Value
	for i, want := range cp.ActiveTXNs {
		got, n := binary.Uvarint(rest)
		if n <= 0 {
			t.Fatalf("decode TXN %d: n=%d", i, n)
		}
		if got != want {
			t.Errorf("TXN %d: got %d, want %d", i, got, want)
		}
		rest = rest[n:]
	}
}

// TestRoundTripUnknownType verifies the default-case handling for
// record types not yet known to the encoder (forward compatibility).
func TestRoundTripUnknownType(t *testing.T) {
	original := &LogRecord{
		Type:  RecordType(200),
		TxnID: 1,
		Value: []byte("future payload"),
	}
	enc := encodeRecord(original)
	decoded, _, err := decodeRecord(enc, 0)
	if err != nil {
		t.Fatalf("decodeRecord: %v", err)
	}
	if decoded.Type != RecordType(200) {
		t.Errorf("Type: got %d, want 200", decoded.Type)
	}
	if !bytes.Equal(decoded.Value, original.Value) {
		t.Errorf("Value: got %q, want %q", decoded.Value, original.Value)
	}
}

// TestDecodeRecordTruncated verifies the R26 graceful-EOF case:
// decoding a record whose length prefix would extend past the buffer
// returns ErrTruncatedRecord.
func TestDecodeRecordTruncated(t *testing.T) {
	rec := &LogRecord{Type: RTData, TxnID: 1, BlockID: 1, Value: []byte("data")}
	enc := encodeRecord(rec)
	// Truncate to half.
	truncated := enc[:len(enc)/2]
	_, _, err := decodeRecord(truncated, 0)
	if err != ErrTruncatedRecord {
		t.Errorf("expected ErrTruncatedRecord, got %v", err)
	}
}

// TestDecodeRecordAtOffset verifies decoding starting at a non-zero
// offset (the replayer reads sequentially through a segment).
func TestDecodeRecordAtOffset(t *testing.T) {
	rec1 := &LogRecord{Type: RTRollback, TxnID: 1}
	rec2 := &LogRecord{Type: RTCommit, TxnID: 2, BlockID: 1000}
	combined := append(encodeRecord(rec1), encodeRecord(rec2)...)

	first, n1, err := decodeRecord(combined, 0)
	if err != nil {
		t.Fatalf("first decode: %v", err)
	}
	if first.Type != RTRollback || first.TxnID != 1 {
		t.Errorf("first record mismatch: %+v", first)
	}

	second, n2, err := decodeRecord(combined, n1)
	if err != nil {
		t.Fatalf("second decode: %v", err)
	}
	if second.Type != RTCommit || second.TxnID != 2 {
		t.Errorf("second record mismatch: %+v", second)
	}
	if n1+n2 != len(combined) {
		t.Errorf("consumed total %d, encoded %d", n1+n2, len(combined))
	}
}

// TestAppendCheckpointPayload verifies the helper produces 24-byte
// header and varint-encoded active TXNs.
func TestAppendCheckpointPayload(t *testing.T) {
	cp := &Checkpoint{
		LSN:              0xAABBCCDD,
		CatalogRootPtr:   0x11223344,
		ManifestChecksum: 0xCAFEF00D,
		ActiveTXNs:       []uint64{10, 20, 30},
	}
	header, txns := AppendCheckpointPayload(cp)
	if len(header) != 24 {
		t.Errorf("header len: got %d, want 24", len(header))
	}
	if got := binary.LittleEndian.Uint64(header[0:8]); got != cp.LSN {
		t.Errorf("header LSN: got %x, want %x", got, cp.LSN)
	}
	if got := binary.LittleEndian.Uint64(header[8:16]); got != cp.CatalogRootPtr {
		t.Errorf("header rootPtr: got %x, want %x", got, cp.CatalogRootPtr)
	}
	if got := binary.LittleEndian.Uint32(header[16:20]); got != cp.ManifestChecksum {
		t.Errorf("header checksum: got %x, want %x", got, cp.ManifestChecksum)
	}
	// Active TXNs should round-trip through encodeVarint.
	rest := txns
	for i, want := range cp.ActiveTXNs {
		got, n := binary.Uvarint(rest)
		if n <= 0 {
			t.Fatalf("decode TXN %d: n=%d", i, n)
		}
		if got != want {
			t.Errorf("TXN %d: got %d, want %d", i, got, want)
		}
		rest = rest[n:]
	}
	if len(rest) != 0 {
		t.Errorf("leftover bytes: %d", len(rest))
	}
}

// TestMaxRecordLen guards against runaway allocations from corrupt
// length varints.
func TestMaxRecordLen(t *testing.T) {
	// Build a record with a deliberately large length prefix.
	bad := binary.AppendUvarint(nil, uint64(MaxRecordLen+1))
	_, _, err := decodeRecord(bad, 0)
	if err != ErrUnknownRecord {
		t.Errorf("expected ErrUnknownRecord, got %v", err)
	}
}

// TestConsumedExactForMultiByteTxnID guards against the n-variable
// shadowing bug fixed in decodeRecord: when the txnID varint spans
// more than one byte, the prior implementation would clobber the
// length-varint byte count and report a wrong consumed-byte total.
func TestConsumedExactForMultiByteTxnID(t *testing.T) {
	cases := []struct {
		name string
		rec  *LogRecord
	}{
		{
			name: "small txnID (1-byte varint)",
			rec: &LogRecord{
				Type:    RTData,
				TxnID:   1,
				BlockID: 0xCAFE,
				Value:   []byte("x"),
			},
		},
		{
			name: "medium txnID (2-byte varint)",
			rec: &LogRecord{
				Type:    RTData,
				TxnID:   200,
				BlockID: 0xCAFE,
				Value:   []byte("payload"),
			},
		},
		{
			name: "large txnID (3-byte varint)",
			rec: &LogRecord{
				Type:    RTData,
				TxnID:   1 << 17,
				BlockID: 0xDEADBEEF,
				Value:   []byte("the quick brown fox"),
			},
		},
		{
			name: "huge txnID (5-byte varint)",
			rec: &LogRecord{
				Type:    RTData,
				TxnID:   1 << 32,
				BlockID: 1,
				Value:   []byte("v"),
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enc := encodeRecord(c.rec)
			_, consumed, err := decodeRecord(enc, 0)
			if err != nil {
				t.Fatalf("decodeRecord: %v", err)
			}
			if consumed != len(enc) {
				t.Errorf("consumed %d, encoded %d (delta=%d)",
					consumed, len(enc), consumed-len(enc))
			}
		})
	}
}
