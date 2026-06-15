package wr

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"github.com/cyw0ng95/razordata/internal/WAL/WR/lz4"
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

// ErrCorrupt is returned when a record's envelope CRC does not
// match the body. R13-7: mid-segment corruption. The replayer
// surfaces this to the caller; the database cannot be opened.
var ErrCorrupt = errors.New("wr: record envelope CRC mismatch")

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
// at WAL.md:50-58. The on-disk format is:
//
//   [length:varint][body...][crc32:4]
//
// where length covers everything after the length field itself (body + 4-byte
// CRC). The CRC32 is IEEE, covers `body` only, and is little-endian.
// Returns the encoded byte slice.
//
// The function is pure: no allocations beyond the returned slice, no
// I/O, no logging. The Writer is responsible for managing the buffer
// and writing to the segment.
//
// REQ000343: pre-sized single allocation. We compute the upper bound
// of the body length up-front (varint + 1 type byte + payload), so
// the final slice is allocated once with the right capacity and we
// never grow during encoding. Previously this function did two
// allocations (encode body in temp slice, copy into final slice
// with length prefix), which doubled allocator pressure on the
// hot path.
//
// REQ000034: when compress is true, the body is lz4-compressed
// before the CRC is computed. The length prefix covers the
// compressed body length. The decompressor detects compression
// by reading the segment header flags (FlagCompressionLZ4).
func encodeRecord(rec *LogRecord) []byte {
	return encodeRecordCompressed(rec, false)
}

// encodeRecordCompressed is like encodeRecord but optionally
// lz4-compresses the body. REQ000034.
func encodeRecordCompressed(rec *LogRecord, compress bool) []byte {
	if rec == nil {
		return nil
	}

	// First pass: encode the body (everything after the length
	// prefix) into a separate slice so we can compute the body
	// length and the total length. With a pre-sized capHint, the
	// two slices combined still allocate once each.
	capHint := maxPayloadSize(rec) + binary.MaxVarintLen64 + 1
	body := make([]byte, 0, capHint)
	body = encodeVarint(body, rec.TxnID)
	body = append(body, byte(rec.Type))
	body = appendPayload(body, rec)

	// REQ000034: optionally compress the body. The CRC is
	// computed over the compressed body (what's on disk).
	diskBody := body
	if compress && len(body) > 0 {
		diskBody = lz4.Compress(body)
	}

	bodyLen := len(diskBody)
	totalLen := uint64(bodyLen + 4) // +4 for the trailing CRC

	// Second pass: assemble the final record in a pre-sized slice.
	// We know the exact size: MaxVarintLen64 (length prefix) +
	// bodyLen + 4 (CRC).
	out := make([]byte, binary.MaxVarintLen64, binary.MaxVarintLen64+bodyLen+4)
	n := binary.PutUvarint(out, totalLen)
	// out now has n bytes of length prefix. We allocated
	// MaxVarintLen64 but only n are used. Shrink to actual
	// size by re-slicing.
	out = out[:n]
	out = append(out, diskBody...)
	// Compute and append CRC32 over the body only (not the
	// length prefix). The decoder reads the same `body` slice
	// (everything between the length varint and the CRC) and
	// verifies the CRC matches.
	sum := crc32.ChecksumIEEE(diskBody)
	out = append(out,
		byte(sum), byte(sum>>8), byte(sum>>16), byte(sum>>24))
	return out
}

// maxPayloadSize returns the upper bound of the per-record payload
// for capacity hinting. Returns 0 for unknown types (we'll let make
// grow the buffer in that case).
func maxPayloadSize(rec *LogRecord) int {
	switch rec.Type {
	case RTData:
		// [blockID:8][checksum:4][data:varint+len]
		return 8 + 4 + binary.MaxVarintLen64 + len(rec.Value)
	case RTCommit:
		return 8
	case RTRollback:
		return 0
	case RTMerge:
		// [newVersion:8][del:varint+len][add:varint+len]
		return 8 + binary.MaxVarintLen64*2 + len(rec.Key) + len(rec.Value)
	case RTCheckpoint:
		// [header:24][activeCount:varint][activeTXNs:varint*N]
		return 24 + binary.MaxVarintLen64 + len(rec.Value)
	default:
		return binary.MaxVarintLen64 + len(rec.Value)
	}
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
	case RTMerge:
		// [newVersion:8][deletedFileCount:varint][deletedFiles:varint...]
		// [addedFileCount:varint][addedFiles:varint...]
		// Per WAL.md:85, RTMerge records the per-file manifest
		// deltas produced by compaction: a new manifest version
		// plus the lists of deleted and added SST file IDs.
		// We use rec.BlockID as newVersion, rec.Key as the
		// varint-packed deletedFiles, rec.Value as the
		// varint-packed addedFiles.
		var tmp [8]byte
		binary.LittleEndian.PutUint64(tmp[:], rec.BlockID)
		buf = append(buf, tmp[:]...)
		buf = encodeVarint(buf, uint64(len(rec.Key)))
		buf = append(buf, rec.Key...)
		buf = encodeVarint(buf, uint64(len(rec.Value)))
		buf = append(buf, rec.Value...)
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

// DecodeRecordCompressed is like DecodeRecord but optionally
// lz4-decompresses the body before parsing. REQ000034.
//
// If compressed is true, the body between the length varint and
// the CRC is lz4-decompressed before the CRC is verified and the
// payload is parsed. The CRC is computed over the on-disk
// (compressed) body, so the decompressor must verify the CRC
// against the compressed bytes, then decompress.
func DecodeRecordCompressed(data []byte, off int, compressed bool) (*LogRecord, int, error) {
	return decodeRecordCompressed(data, off, compressed)
}

// off. Returns the decoded LogRecord, the number of bytes consumed
// (length prefix + body + CRC), and an error if the record is
// malformed.
//
// If the length varint or record body would extend past the end of
// data, returns ErrTruncatedRecord with consumed=-1. This is the
// R26 graceful-EOF case.
//
// If the 4-byte envelope CRC does not match, returns ErrCorrupt
// with consumed=-1. R13-7: mid-segment corruption, fail loud.
func decodeRecord(data []byte, off int) (*LogRecord, int, error) {
	return decodeRecordCompressed(data, off, false)
}

// decodeRecordCompressed decodes a single LogRecord at offset off
// in data, optionally lz4-decompressing the body. REQ000034.
//
// The CRC is verified against the on-disk (compressed) body
// before decompression. This ensures corruption is detected
// before any decompression bomb could be triggered.
func decodeRecordCompressed(data []byte, off int, compressed bool) (*LogRecord, int, error) {
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
	// The length field includes the 4-byte CRC. Refuse records
	// that would not have a body to verify.
	if length < 4 {
		return nil, -1, ErrCorrupt
	}
	bodyLen := int(length) - 4
	bodyStart := off + hdrN
	bodyEnd := bodyStart + bodyLen
	crcStart := bodyEnd
	crcEnd := crcStart + 4
	if crcEnd > len(data) {
		return nil, -1, ErrTruncatedRecord
	}
	diskBody := data[bodyStart:bodyEnd]
	storedCRC := binary.LittleEndian.Uint32(data[crcStart:crcEnd])
	computed := crc32.ChecksumIEEE(diskBody)
	if storedCRC != computed {
		return nil, -1, ErrCorrupt
	}

	// REQ000034: decompress the body if the segment is
	// compressed. The CRC was verified against the on-disk
	// (compressed) body, so we can safely decompress now.
	body := diskBody
	if compressed && len(diskBody) > 0 {
		decompressed, err := lz4.Decompress(diskBody)
		if err != nil {
			return nil, -1, fmt.Errorf("%w: decompress: %v", ErrCorrupt, err)
		}
		body = decompressed
	}

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
		// Verify the per-RTData payload CRC (R13-13: with the
		// envelope CRC now in place, the inner CRC is a
		// consistent second line of defense for the data bytes
		// themselves). A mismatch here is corruption just like
		// an envelope-CRC mismatch; the caller maps the error.
		stored := binary.LittleEndian.Uint32(body[cur+8 : cur+12])
		cur += 12
		dataLen, n := DecodeVarint(body, cur)
		if n < 0 || cur+n+int(dataLen) > len(body) {
			return cur
		}
		cur += n
		if stored != crc32.ChecksumIEEE(body[cur:cur+int(dataLen)]) {
			// Tag the record so the replayer can surface
			// ErrCorrupt instead of a partial decode. We do not
			// return ErrCorrupt from this helper because its
			// signature is cursor-only; the replayer checks
			// rec.PayCRCFail below.
			rec.PayCRCFail = true
		}
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
	case RTMerge:
		// [newVersion:8][deletedFileCount:varint][deletedFiles:varint...]
		// [addedFileCount:varint][addedFiles:varint...]
		if cur+8 > len(body) {
			return cur
		}
		rec.BlockID = binary.LittleEndian.Uint64(body[cur : cur+8])
		cur += 8
		// deletedFiles: varint length + varint-packed bytes.
		delLen, n := DecodeVarint(body, cur)
		if n < 0 {
			return cur
		}
		cur += n
		if cur+int(delLen) > len(body) {
			return cur
		}
		rec.Key = make([]byte, delLen)
		copy(rec.Key, body[cur:cur+int(delLen)])
		cur += int(delLen)
		// addedFiles: varint length + varint-packed bytes.
		addLen, n := DecodeVarint(body, cur)
		if n < 0 {
			return cur
		}
		cur += n
		if cur+int(addLen) > len(body) {
			return cur
		}
		rec.Value = make([]byte, addLen)
		copy(rec.Value, body[cur:cur+int(addLen)])
		cur += int(addLen)
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
