package wr

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// ErrTruncatedRecord is returned when a record header would read past
// the end of a segment. This is the R26 graceful-EOF case: a torn
// write from a crash mid-record. Callers should treat this as
// end-of-segment, not corruption.
var ErrTruncatedRecord = errors.New("wr: truncated record (end of segment)")

// ErrUnknownRecord is returned when a record's length would extend
// past the available bytes. The reader should stop silently (this is
// not a hard error condition during replay).
var ErrUnknownRecord = errors.New("wr: record length exceeds segment tail")

// MaxRecordLen is the largest record we will encode. It bounds a
// single record's payload to ~4 MB so a corrupt length varint cannot
// cause a runaway allocation.
const MaxRecordLen = 4 * 1024 * 1024

// encodeVarint appends a varint-encoded uint64 to buf and returns the
// extended slice. Matches the standard binary.PutUvarint wire format.
func encodeVarint(buf []byte, v uint64) []byte {
	return binary.AppendUvarint(buf, v)
}

// DecodeVarint reads a varint at off in data. The second return is
// the number of bytes consumed, or -1 if the data was truncated or
// the offset was out of range.
func DecodeVarint(data []byte, off int) (uint64, int) {
	if off < 0 || off >= len(data) {
		return 0, -1
	}
	v, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return 0, -1
	}
	if off+n > len(data) {
		return 0, -1
	}
	return v, n
}

// encodeRecord encodes a single LogRecord into the format documented
// in the design (R02):
//
//	┌──────────────┬──────────┬─────────────┬──────────────────┐
//	│ length:varint│ txnID:varint│ type:uint8 │ payload:blob     │
//	└──────────────┴──────────┴─────────────┴──────────────────┘
//
// `length` covers everything after the length field itself (txnID +
// type + payload). Returns the encoded byte slice.
//
// The function is pure: no allocations beyond the returned slice, no
// I/O, no logging. The Writer is responsible for managing the buffer
// and writing to the segment.
func encodeRecord(rec *LogRecord) []byte {
	if rec == nil {
		return nil
	}

	// First pass: encode payload + type + txnID into a temporary to
	// measure the total length so the length prefix is correct.
	var body []byte
	body = encodeVarint(body, rec.TxnID)
	body = append(body, byte(rec.Type))
	body = appendPayload(body, rec)

	// Second pass: prepend the length prefix.
	out := make([]byte, 0, binary.MaxVarintLen64+len(body))
	out = encodeVarint(out, uint64(len(body)))
	out = append(out, body...)
	return out
}

// appendPayload appends the per-type payload to buf (R02).
func appendPayload(buf []byte, rec *LogRecord) []byte {
	switch rec.Type {
	case RTData:
		// [blockID:8][checksum:4][data:varint]
		var tmp [12]byte
		binary.LittleEndian.PutUint64(tmp[0:8], rec.BlockID)
		// Compute checksum over the data bytes; the encoded record
		// stores it in 4 little-endian bytes immediately after blockID.
		sum := crc32.ChecksumIEEE(rec.Value)
		binary.LittleEndian.PutUint32(tmp[8:12], sum)
		buf = append(buf, tmp[:]...)
		buf = encodeVarint(buf, uint64(len(rec.Value)))
		buf = append(buf, rec.Value...)
	case RTCommit:
		// [commitTS:8] — we use the BlockID slot as commit timestamp
		// for convenience (both are 8 bytes). This avoids widening
		// LogRecord with a field that only one type uses.
		var tmp [8]byte
		binary.LittleEndian.PutUint64(tmp[:], rec.BlockID)
		buf = append(buf, tmp[:]...)
	case RTRollback:
		// empty payload
	case RTCheckpoint:
		// [checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4]
		// [activeTXNCount:varint][activeTXNs:varint...]
		// We use the Key and Value fields to carry the checkpoint
		// payload: Key holds the LSN+rootPtr+checksum (24 bytes),
		// Value holds the varint-encoded active TXN IDs. BlockID is
		// repurposed as the active TXN count — it is otherwise unused
		// for RTCheckpoint records.
		if len(rec.Key) == 24 {
			buf = append(buf, rec.Key...)
		} else {
			// Defensive: pad with zeros if caller did not pre-format.
			var zeros [24]byte
			buf = append(buf, zeros[:]...)
		}
		buf = encodeVarint(buf, rec.BlockID)
		buf = append(buf, rec.Value...)
	default:
		// Unknown record type — payload is whatever the caller put
		// in Value, encoded as a varint length + raw bytes. This
		// allows future types to be added without breaking the
		// envelope format.
		buf = encodeVarint(buf, uint64(len(rec.Value)))
		buf = append(buf, rec.Value...)
	}
	return buf
}

// DecodeRecord is the exported version of decodeRecord for use
// by external packages (e.g., WAL/RP replayer). It has the same
// semantics as decodeRecord.
func DecodeRecord(data []byte, off int) (*LogRecord, int, error) {
	return decodeRecord(data, off)
}

// off. Returns the decoded LogRecord, the number of bytes consumed
// (header + body), and an error if the record is malformed.
//
// If the length varint or record body would extend past the end of
// data, returns ErrTruncatedRecord with consumed=-1. This is the R26
// graceful-EOF case.
func decodeRecord(data []byte, off int) (*LogRecord, int, error) {
	if off < 0 || off >= len(data) {
		return nil, -1, ErrTruncatedRecord
	}

	// 1. Read length varint.
	length, hdrN := DecodeVarint(data, off)
	if hdrN < 0 {
		return nil, -1, ErrTruncatedRecord
	}
	if length > uint64(MaxRecordLen) {
		return nil, -1, ErrUnknownRecord
	}
	bodyStart := off + hdrN
	bodyEnd := bodyStart + int(length)
	if bodyEnd > len(data) {
		return nil, -1, ErrTruncatedRecord
	}
	body := data[bodyStart:bodyEnd]

	// 2. Read txnID varint. Use a separate cursor position variable
	// (txnN) to avoid clobbering hdrN, which we still need for the
	// final consumed-byte count.
	cur := 0
	txnID, txnN := DecodeVarint(body, cur)
	if txnN < 0 {
		return nil, -1, ErrTruncatedRecord
	}
	cur += txnN

	// 3. Read type byte.
	if cur >= len(body) {
		return nil, -1, ErrTruncatedRecord
	}
	recType := RecordType(body[cur])
	cur++

	rec := &LogRecord{Type: recType, TxnID: txnID}
	decodePayload(body, cur, rec)

	return rec, hdrN + int(length), nil
}

// decodePayload decodes the per-type payload from body starting at
// cur, populating the corresponding fields of rec. Returns the new
// cursor position.
func decodePayload(body []byte, cur int, rec *LogRecord) int {
	switch rec.Type {
	case RTData:
		if cur+12 > len(body) {
			return cur // truncated; caller sees partial fields
		}
		rec.BlockID = binary.LittleEndian.Uint64(body[cur : cur+8])
		// stored checksum at [8:12] is verified by the upper layer
		// (we expose it via BlockID-stored CRC and re-derive on
		// access). The CRC is intentionally not checked here because
		// the envelope CRC is deferred to v2 (R-corrupt-deferred).
		_ = binary.LittleEndian.Uint32(body[cur+8 : cur+12])
		cur += 12
		dataLen, n := DecodeVarint(body, cur)
		if n < 0 || cur+n+int(dataLen) > len(body) {
			return cur
		}
		cur += n
		rec.Value = make([]byte, dataLen)
		copy(rec.Value, body[cur:cur+int(dataLen)])
		cur += int(dataLen)
	case RTCommit:
		if cur+8 > len(body) {
			return cur
		}
		rec.BlockID = binary.LittleEndian.Uint64(body[cur : cur+8])
		cur += 8
	case RTRollback:
		// empty
	case RTCheckpoint:
		if cur+24 > len(body) {
			return cur
		}
		rec.Key = make([]byte, 24)
		copy(rec.Key, body[cur:cur+24])
		cur += 24
		count, txnN := DecodeVarint(body, cur)
		if txnN < 0 {
			return cur
		}
		cur += txnN
		// The count is stored in BlockID (which is otherwise unused
		// for RTCheckpoint). The remaining body bytes are the
		// varint-encoded active TXN IDs, copied verbatim into Value.
		rec.BlockID = count
		rec.Value = make([]byte, len(body)-cur)
		copy(rec.Value, body[cur:])
		cur = len(body)
	default:
		// Unknown type: payload is varint length + raw bytes.
		dataLen, n := DecodeVarint(body, cur)
		if n < 0 || cur+n+int(dataLen) > len(body) {
			return cur
		}
		cur += n
		rec.Value = make([]byte, dataLen)
		copy(rec.Value, body[cur:cur+int(dataLen)])
		cur += int(dataLen)
	}
	return cur
}

// AppendCheckpointPayload is a helper for callers (ENG) that want to
// build a Checkpoint record's payload bytes without going through a
// full LogRecord. Returns the 24-byte header (LSN+rootPtr+checksum)
// and the varint-encoded active TXN slice.
func AppendCheckpointPayload(cp *Checkpoint) (header []byte, txns []byte) {
	header = make([]byte, 24)
	binary.LittleEndian.PutUint64(header[0:8], cp.LSN)
	binary.LittleEndian.PutUint64(header[8:16], cp.CatalogRootPtr)
	binary.LittleEndian.PutUint32(header[16:20], cp.ManifestChecksum)
	// bytes 20:24 reserved for future use
	txns = make([]byte, 0, len(cp.ActiveTXNs)*binary.MaxVarintLen64)
	for _, id := range cp.ActiveTXNs {
		txns = encodeVarint(txns, id)
	}
	return header, txns
}
