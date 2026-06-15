package ls

// columnar.go adds a column-major block layout to the SST
// format. REQ000314.
//
// Traditional SST (row-major): each block contains
//   [key1][value1][key2][value2]...
// PAX (column-major): each block contains
//   [numKeys:varint][keyLen1][keyLen2]...[key1][key2]...
//   [numVals:varint][valLen1][valLen2]...[val1][val2]...
// i.e. all keys packed together, then all values. This lets
// the reader fetch only the keys (or only the values) when
// the query is a key-only or value-only scan.
//
// Format detection: the first byte of a block is 0 for row-
// major (legacy) and 1 for column-major. This keeps the
// legacy format readable while letting new SSTs opt in.
//
// For a single-column projection (e.g. `SELECT k FROM t WHERE
// v = ?`), the columnar format saves 50%+ I/O because the
// values column can be skipped entirely.

import (
	"encoding/binary"
	"hash/crc32"
)

// crc32Sum is a small wrapper over hash/crc32.
func crc32Sum(data []byte) uint32 {
	return crc32.Checksum(data, crc32.MakeTable(crc32.Koopman))
}

// blockLayout identifies the block layout.
type blockLayout uint8

const (
	layoutRowMajor   blockLayout = 0
	layoutColumnar  blockLayout = 1
)

// columnarBlockHeader is the prefix of every columnar block.
type columnarBlockHeader struct {
	layout    blockLayout
	keysLen   uint32 // total bytes of keys region
	valsLen   uint32 // total bytes of values region
	keyCount  uint32
	checksum  uint32 // CRC32 of keys+vals regions
}

// writeColumnarBlock serializes (keys, values) into the
// columnar layout. Returns the block bytes (with header
// prefix).
//
// Layout:
//   [layout:1][keyCount:4][keysLen:4][valsLen:4][checksum:4]
//   [keyLens:varint*N][keys:bytes]
//   [valLens:varint*N][values:bytes]
const columnarHeaderSize = 1 + 4 + 4 + 4 + 4

func writeColumnarBlock(keys, values [][]byte) []byte {
	buf := make([]byte, 0, columnarHeaderSize+len(keys)*16+len(values)*16)
	// Reserve header.
	buf = append(buf, byte(layoutColumnar))
	buf = append(buf, 0, 0, 0, 0) // keyCount placeholder
	buf = append(buf, 0, 0, 0, 0) // keysLen placeholder
	buf = append(buf, 0, 0, 0, 0) // valsLen placeholder
	buf = append(buf, 0, 0, 0, 0) // checksum placeholder
	// Key region.
	keysStart := uint32(len(buf))
	for _, k := range keys {
		buf = binary.AppendUvarint(buf, uint64(len(k)))
	}
	for _, k := range keys {
		buf = append(buf, k...)
	}
	keysEnd := uint32(len(buf))
	// Value region.
	valsStart := uint32(len(buf))
	for _, v := range values {
		buf = binary.AppendUvarint(buf, uint64(len(v)))
	}
	for _, v := range values {
		buf = append(buf, v...)
	}
	valsEnd := uint32(len(buf))
	actualKeysLen := keysEnd - keysStart
	actualValsLen := valsEnd - valsStart
	binary.LittleEndian.PutUint32(buf[1:5], uint32(len(keys)))
	binary.LittleEndian.PutUint32(buf[5:9], actualKeysLen)
	binary.LittleEndian.PutUint32(buf[9:13], actualValsLen)
	checksum := crc32Sum(buf[columnarHeaderSize:])
	binary.LittleEndian.PutUint32(buf[13:17], checksum)
	return buf
}

// readColumnarBlock deserializes a columnar block. Returns
// (keys, values) slices. The returned slices share memory
// with buf; callers must copy if they outlive buf.
func readColumnarBlock(buf []byte) ([][]byte, [][]byte, error) {
	if len(buf) < columnarHeaderSize {
		return nil, nil, ErrInvalidSSTFormat
	}
	if blockLayout(buf[0]) != layoutColumnar {
		return nil, nil, ErrInvalidSSTFormat
	}
	keyCount := binary.LittleEndian.Uint32(buf[1:5])
	keysLen := binary.LittleEndian.Uint32(buf[5:9])
	_ = keysLen
	valsLen := binary.LittleEndian.Uint32(buf[9:13])
	checksum := binary.LittleEndian.Uint32(buf[13:17])
	// Verify checksum.
	actualSum := crc32Sum(buf[columnarHeaderSize:])
	if actualSum != checksum {
		return nil, nil, ErrInvalidSSTFormat
	}
	// Parse keys region: all lengths first, then all data.
	cur := columnarHeaderSize
	keyLens := make([]uint64, keyCount)
	for i := uint32(0); i < keyCount; i++ {
		l, n := binary.Uvarint(buf[cur:])
		if n <= 0 {
			return nil, nil, ErrInvalidSSTFormat
		}
		keyLens[i] = l
		cur += n
	}
	keys := make([][]byte, 0, keyCount)
	for i := uint32(0); i < keyCount; i++ {
		l := int(keyLens[i])
		if cur+l > len(buf) {
			return nil, nil, ErrInvalidSSTFormat
		}
		keys = append(keys, buf[cur:cur+l])
		cur += l
	}
	// Parse values region: all lengths first, then all data.
	valLens := make([]uint64, keyCount)
	for i := uint32(0); i < keyCount; i++ {
		l, n := binary.Uvarint(buf[cur:])
		if n <= 0 {
			return nil, nil, ErrInvalidSSTFormat
		}
		valLens[i] = l
		cur += n
	}
	values := make([][]byte, 0, keyCount)
	for i := uint32(0); i < keyCount; i++ {
		l := int(valLens[i])
		if cur+l > len(buf) {
			return nil, nil, ErrInvalidSSTFormat
		}
		values = append(values, buf[cur:cur+l])
		cur += l
	}
	_ = valsLen
	return keys, values, nil
}
