package wr

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"github.com/cyw0ng95/razordata/internal/WAL/WR/lz4"
)

var (
	ErrTruncatedRecord = errors.New("wr: truncated record (end of segment)")
	ErrUnknownRecord   = errors.New("wr: record length exceeds segment tail")
	ErrCorrupt         = errors.New("wr: record envelope CRC mismatch")
)

const MaxRecordLen = 4 * 1024 * 1024

func encodeVarint(buf []byte, v uint64) []byte {
	return binary.AppendUvarint(buf, v)
}

// DecodeVarint reads a varint at off in data.
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

func encodeRecord(rec *LogRecord) []byte {
	return encodeRecordCompressed(rec, false)
}

func encodeRecordCompressed(rec *LogRecord, compress bool) []byte {
	if rec == nil {
		return nil
	}

	capHint := maxPayloadSize(rec) + binary.MaxVarintLen64 + 1
	body := make([]byte, 0, capHint)
	body = encodeVarint(body, rec.TxnID)
	body = append(body, byte(rec.Type))
	body = appendPayload(body, rec)

	diskBody := body
	if compress && len(body) > 0 {
		diskBody = lz4.Compress(body)
	}

	bodyLen := len(diskBody)
	totalLen := uint64(bodyLen + 4)

	out := make([]byte, binary.MaxVarintLen64, binary.MaxVarintLen64+bodyLen+4)
	n := binary.PutUvarint(out, totalLen)
	// out now has n bytes of length prefix. We allocated
	// MaxVarintLen64 but only n are used. Shrink to actual
	// size by re-slicing.
	out = out[:n]
	out = append(out, diskBody...)
	sum := crc32.ChecksumIEEE(diskBody)
	out = append(out,
		byte(sum), byte(sum>>8), byte(sum>>16), byte(sum>>24))
	return out
}

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

func appendPayload(buf []byte, rec *LogRecord) []byte {
	switch rec.Type {
	case RTData:
		var tmp [12]byte
		binary.LittleEndian.PutUint64(tmp[0:8], rec.BlockID)
		sum := crc32.ChecksumIEEE(rec.Value)
		binary.LittleEndian.PutUint32(tmp[8:12], sum)
		buf = append(buf, tmp[:]...)
		buf = encodeVarint(buf, uint64(len(rec.Value)))
		buf = append(buf, rec.Value...)
	case RTCommit:
		var tmp [8]byte
		binary.LittleEndian.PutUint64(tmp[:], rec.BlockID)
		buf = append(buf, tmp[:]...)
	case RTRollback:
		// empty payload
	case RTMerge:
		var tmp [8]byte
		binary.LittleEndian.PutUint64(tmp[:], rec.BlockID)
		buf = append(buf, tmp[:]...)
		buf = encodeVarint(buf, uint64(len(rec.Key)))
		buf = append(buf, rec.Key...)
		buf = encodeVarint(buf, uint64(len(rec.Value)))
		buf = append(buf, rec.Value...)
	case RTCheckpoint:
		if len(rec.Key) == 24 {
			buf = append(buf, rec.Key...)
		} else {
			var zeros [24]byte
			buf = append(buf, zeros[:]...)
		}
		buf = encodeVarint(buf, rec.BlockID)
		buf = append(buf, rec.Value...)
	default:
		buf = encodeVarint(buf, uint64(len(rec.Value)))
		buf = append(buf, rec.Value...)
	}
	return buf
}

// DecodeRecord decodes a single LogRecord at offset off.
func DecodeRecord(data []byte, off int) (*LogRecord, int, error) {
	return decodeRecord(data, off)
}

// DecodeRecordCompressed optionally lz4-decompresses the body (REQ000034).
func DecodeRecordCompressed(data []byte, off int, compressed bool) (*LogRecord, int, error) {
	return decodeRecordCompressed(data, off, compressed)
}

func decodeRecord(data []byte, off int) (*LogRecord, int, error) {
	return decodeRecordCompressed(data, off, false)
}

func decodeRecordCompressed(data []byte, off int, compressed bool) (*LogRecord, int, error) {
	if off < 0 || off >= len(data) {
		return nil, -1, ErrTruncatedRecord
	}

	length, hdrN := DecodeVarint(data, off)
	if hdrN < 0 {
		return nil, -1, ErrTruncatedRecord
	}
	if length > uint64(MaxRecordLen) {
		return nil, -1, ErrUnknownRecord
	}
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

	body := diskBody
	if compressed && len(diskBody) > 0 {
		decompressed, err := lz4.Decompress(diskBody)
		if err != nil {
			return nil, -1, fmt.Errorf("%w: decompress: %v", ErrCorrupt, err)
		}
		body = decompressed
	}

	cur := 0
	txnID, txnN := DecodeVarint(body, cur)
	if txnN < 0 {
		return nil, -1, ErrTruncatedRecord
	}
	cur += txnN

	if cur >= len(body) {
		return nil, -1, ErrTruncatedRecord
	}
	recType := RecordType(body[cur])
	cur++

	rec := &LogRecord{Type: recType, TxnID: txnID}
	decodePayload(body, cur, rec)

	return rec, hdrN + int(length), nil
}

func decodePayload(body []byte, cur int, rec *LogRecord) int {
	switch rec.Type {
	case RTData:
		if cur+12 > len(body) {
			return cur // truncated; caller sees partial fields
		}
		rec.BlockID = binary.LittleEndian.Uint64(body[cur : cur+8])
		stored := binary.LittleEndian.Uint32(body[cur+8 : cur+12])
		cur += 12
		dataLen, n := DecodeVarint(body, cur)
		if n < 0 || cur+n+int(dataLen) > len(body) {
			return cur
		}
		cur += n
		if stored != crc32.ChecksumIEEE(body[cur:cur+int(dataLen)]) {
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
	case RTMerge:
		if cur+8 > len(body) {
			return cur
		}
		rec.BlockID = binary.LittleEndian.Uint64(body[cur : cur+8])
		cur += 8
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
		rec.BlockID = count
		rec.Value = make([]byte, len(body)-cur)
		copy(rec.Value, body[cur:])
		cur = len(body)
	default:
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

// AppendCheckpointPayload builds a Checkpoint record's payload bytes.
func AppendCheckpointPayload(cp *Checkpoint) (header []byte, txns []byte) {
	header = make([]byte, 24)
	binary.LittleEndian.PutUint64(header[0:8], cp.LSN)
	binary.LittleEndian.PutUint64(header[8:16], cp.CatalogRootPtr)
	binary.LittleEndian.PutUint32(header[16:20], cp.ManifestChecksum)
	txns = make([]byte, 0, len(cp.ActiveTXNs)*binary.MaxVarintLen64)
	for _, id := range cp.ActiveTXNs {
		txns = encodeVarint(txns, id)
	}
	return header, txns
}
