package wr

import (
	"encoding/binary"
	"hash/crc32"
)

// ColumnarBatch is a collection of LogRecords of the same Type
// encoded in column-major order. REQ000299.
//
// Row-oriented format: [length:varint][body...][crc32:4] where body
// is a sequence of fully-encoded records. Large batches of small
// records waste bytes on per-record length prefixes and CRC trailers.
//
// Columnar format packs all values for a given field together:
// keys for record N are at the same offset within the keys column
// as values are within the values column. This shrinks varint
// length prefixes (the keys are typically increasing, so delta
// encoding is cheap) and allows the single batch envelope CRC to
// amortize across N records (~32x CRC savings for a 32-record
// batch).
//
// The on-disk format is:
//
//	[batchLen:varint][recordCount:varint][recordType:1]
//	[numColumns:varint]
//	[for each column:]
//	  [columnKind:1][columnLen:varint][columnBytes...]
//	[crc32:4]
//
// The current implementation only encodes RTData batches (the
// hot path: many small KV inserts per transaction). Other record
// types fall back to the row-oriented encoder.
type ColumnarBatch struct {
	Type    RecordType
	Records []*LogRecord
}

// NewColumnarBatch wraps a slice of LogRecords of the same type.
// All records must be non-nil and share the same Type.
func NewColumnarBatch(typ RecordType, recs []*LogRecord) *ColumnarBatch {
	return &ColumnarBatch{Type: typ, Records: recs}
}

// EncodeColumnar serializes a batch of RTData records into the
// columnar format. REQ000299.
//
// For batches with < 4 records, returns nil so the caller can
// fall back to the row-oriented encoder. Below this threshold the
// columnar overhead (extra length prefixes per column) exceeds
// the per-row savings.
func EncodeColumnar(batch *ColumnarBatch) []byte {
	if batch == nil || len(batch.Records) < 4 {
		return nil
	}
	if batch.Type != RTData {
		return nil
	}

	// Column 0: txnID (varint-delta, since records within a tx
	// share the same id, repeated). We store raw varints here
	// for simplicity; delta encoding can be added in v0.28.
	// Column 1: blockID (uint64 LE).
	// Column 2: payload checksum (uint32 LE).
	// Column 3: payload length (varint).
	// Column 4: payload (concatenated raw bytes; per-row offsets
	// are reconstructed by walking the length column).

	numCols := 5
	cols := make([][]byte, numCols)
	cols[0] = make([]byte, 0, 8*len(batch.Records))
	cols[1] = make([]byte, 0, 8*len(batch.Records))
	cols[2] = make([]byte, 0, 4*len(batch.Records))
	cols[3] = make([]byte, 0, 8*len(batch.Records))
	cols[4] = make([]byte, 0, sumPayloadSizes(batch.Records))

	for _, rec := range batch.Records {
		cols[0] = encodeVarint(cols[0], rec.TxnID)
		var tmp8 [8]byte
		binary.LittleEndian.PutUint64(tmp8[:], rec.BlockID)
		cols[1] = append(cols[1], tmp8[:]...)
		sum := crc32.ChecksumIEEE(rec.Value)
		var tmp4 [4]byte
		binary.LittleEndian.PutUint32(tmp4[:], sum)
		cols[2] = append(cols[2], tmp4[:]...)
		cols[3] = encodeVarint(cols[3], uint64(len(rec.Value)))
		cols[4] = append(cols[4], rec.Value...)
	}

	// Assemble the body: count, type, numCols, then columns.
	body := make([]byte, 0, 64*len(batch.Records))
	body = encodeVarint(body, uint64(len(batch.Records)))
	body = append(body, byte(batch.Type))
	body = encodeVarint(body, uint64(numCols))
	for i, col := range cols {
		// columnKind: 0 = txnID, 1 = blockID, 2 = checksum, 3 = len, 4 = payload
		body = append(body, byte(i))
		body = encodeVarint(body, uint64(len(col)))
		body = append(body, col...)
	}

	// Wrap in envelope: [length:varint][body...][crc32:4]
	totalLen := uint64(len(body) + 4)
	out := make([]byte, binary.MaxVarintLen64, binary.MaxVarintLen64+len(body)+4)
	n := binary.PutUvarint(out, totalLen)
	out = out[:n]
	out = append(out, body...)
	sum := crc32.ChecksumIEEE(body)
	out = append(out,
		byte(sum), byte(sum>>8), byte(sum>>16), byte(sum>>24))
	return out
}

func sumPayloadSizes(recs []*LogRecord) int {
	n := 0
	for _, r := range recs {
		n += len(r.Value)
	}
	return n
}
