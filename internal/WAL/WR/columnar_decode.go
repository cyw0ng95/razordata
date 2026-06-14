package wr

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// ErrColumnarTruncated is returned when a columnar batch is shorter
// than the length prefix indicates. REQ000299.
var ErrColumnarTruncated = errors.New("wr: truncated columnar batch")

// DecodeColumnar decodes a columnar batch envelope back into
// individual LogRecords. The `data` argument is the segment bytes
// starting at the length varint; `off` is the byte offset within
// `data` to read from. Returns the records, the number of bytes
// consumed (length prefix + body + CRC), and an error if the
// batch is malformed.
//
// Format:
//
//	[batchLen:varint][recordCount:varint][recordType:1]
//	[numCols:varint]
//	[for each column:]
//	  [columnKind:1][columnLen:varint][columnBytes...]
//	[crc32:4]
func DecodeColumnar(data []byte, off int) ([]*LogRecord, int, error) {
	if off < 0 || off >= len(data) {
		return nil, -1, ErrColumnarTruncated
	}
	// 1. Length varint.
	length, hdrN := DecodeVarint(data, off)
	if hdrN < 0 {
		return nil, -1, ErrColumnarTruncated
	}
	if length < 4 || length > uint64(MaxRecordLen) {
		return nil, -1, ErrUnknownRecord
	}
	bodyLen := int(length) - 4
	bodyStart := off + hdrN
	bodyEnd := bodyStart + bodyLen
	crcEnd := bodyEnd + 4
	if crcEnd > len(data) {
		return nil, -1, ErrColumnarTruncated
	}
	body := data[bodyStart:bodyEnd]
	storedCRC := binary.LittleEndian.Uint32(data[bodyEnd:crcEnd])
	if storedCRC != crc32.ChecksumIEEE(body) {
		return nil, -1, ErrCorrupt
	}

	// 2. Record count + type + numCols.
	cur := 0
	count, n := DecodeVarint(body, cur)
	if n < 0 {
		return nil, -1, ErrColumnarTruncated
	}
	cur += n
	if cur >= len(body) {
		return nil, -1, ErrColumnarTruncated
	}
	recType := RecordType(body[cur])
	cur++
	numCols, n := DecodeVarint(body, cur)
	if n < 0 {
		return nil, -1, ErrColumnarTruncated
	}
	cur += n

	// 3. Read each column into a map keyed by columnKind.
	colsByKind := make(map[uint8][]byte, numCols)
	for i := uint64(0); i < numCols; i++ {
		if cur >= len(body) {
			return nil, -1, ErrColumnarTruncated
		}
		kind := body[cur]
		cur++
		colLen, n := DecodeVarint(body, cur)
		if n < 0 {
			return nil, -1, ErrColumnarTruncated
		}
		cur += n
		if cur+int(colLen) > len(body) {
			return nil, -1, ErrColumnarTruncated
		}
		colsByKind[kind] = body[cur : cur+int(colLen)]
		cur += int(colLen)
	}

	// 4. Reconstruct records. Per-row lengths walk the payload column.
	txnCol := colsByKind[0]
	blkCol := colsByKind[1]
	sumCol := colsByKind[2]
	lenCol := colsByKind[3]
	payCol := colsByKind[4]

	out := make([]*LogRecord, 0, count)
	offTxn, offBlk, offSum, offLen, offPay := 0, 0, 0, 0, 0
	for i := 0; i < int(count); i++ {
		txnID, n := DecodeVarint(txnCol, offTxn)
		if n < 0 {
			return nil, -1, ErrColumnarTruncated
		}
		offTxn += n
		if offBlk+8 > len(blkCol) || offSum+4 > len(sumCol) {
			return nil, -1, ErrColumnarTruncated
		}
		blockID := binary.LittleEndian.Uint64(blkCol[offBlk : offBlk+8])
		offBlk += 8
		// We do not verify per-row payload CRC here; the
		// envelope CRC already covers the whole body. Storing
		// the inner CRC is a defense-in-depth check that the
		// replayer can do per-record.
		if offSum+4 <= len(sumCol) {
			offSum += 4
		}
		dataLen, n := DecodeVarint(lenCol, offLen)
		if n < 0 {
			return nil, -1, ErrColumnarTruncated
		}
		offLen += n
		if offPay+int(dataLen) > len(payCol) {
			return nil, -1, ErrColumnarTruncated
		}
		val := make([]byte, dataLen)
		copy(val, payCol[offPay:offPay+int(dataLen)])
		offPay += int(dataLen)
		out = append(out, &LogRecord{
			Type:    recType,
			TxnID:   txnID,
			BlockID: blockID,
			Value:   val,
		})
	}
	return out, hdrN + int(length), nil
}
