package ls

// columnar.go adds a column-major block layout to SST (REQ000314).

import (
	"encoding/binary"
	"hash/crc32"
)

func crc32Sum(data []byte) uint32 {
	return crc32.Checksum(data, crc32Koopman)
}

type blockLayout uint8

const (
	layoutRowMajor blockLayout = 0
	layoutColumnar blockLayout = 1
)

type columnarBlockHeader struct {
	layout   blockLayout
	keysLen  uint32 // total bytes of keys region
	valsLen  uint32 // total bytes of values region
	keyCount uint32
	checksum uint32 // CRC32 of keys+vals regions
}

const columnarHeaderSize = 1 + 4 + 4 + 4 + 4

func writeColumnarBlock(keys, values [][]byte) []byte {
	buf := make([]byte, 0, columnarHeaderSize+len(keys)*16+len(values)*16)
	buf = append(buf, byte(layoutColumnar))
	buf = append(buf, 0, 0, 0, 0) // keyCount placeholder
	buf = append(buf, 0, 0, 0, 0) // keysLen placeholder
	buf = append(buf, 0, 0, 0, 0) // valsLen placeholder
	buf = append(buf, 0, 0, 0, 0) // checksum placeholder
	keysStart := uint32(len(buf))
	for _, k := range keys {
		buf = binary.AppendUvarint(buf, uint64(len(k)))
	}
	for _, k := range keys {
		buf = append(buf, k...)
	}
	keysEnd := uint32(len(buf))
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

// readColumnarBlock deserializes a columnar block.
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
	actualSum := crc32Sum(buf[columnarHeaderSize:])
	if actualSum != checksum {
		return nil, nil, ErrInvalidSSTFormat
	}
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
