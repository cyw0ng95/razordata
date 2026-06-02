package ls

import (
	"encoding/binary"
	"errors"
)

var (
	ErrEncodeRow   = errors.New("failed to encode row")
	ErrDecodeRow   = errors.New("failed to decode row")
	ErrEncodeBlock = errors.New("failed to encode block")
	ErrDecodeBlock = errors.New("failed to decode block")
)

type Pair struct {
	Key   []byte
	Value []byte
}

type KV struct {
	Key   []byte
	Value []byte
}

func EncodeRow(row Row, schema *TableSchema) ([]byte, error) {
	if len(row.Values) != len(schema.Columns) {
		return nil, ErrEncodeRow
	}

	var buf []byte

	nullBitmap := make([]byte, (len(schema.Columns)+7)/8)
	for i, val := range row.Values {
		if val == nil {
			nullBitmap[i/8] |= 1 << (i % 8)
		}
	}
	buf = append(buf, nullBitmap...)

	for i, col := range schema.Columns {
		val := row.Values[i]
		if val == nil {
			continue
		}

		switch col.Type {
		case CTInt, CTBigInt, CTTimestamp:
			buf = append(buf, val...)
		case CTFloat:
			buf = append(buf, val...)
		case CTBool:
			buf = append(buf, val...)
		case CTVarchar, CTText, CTBlob:
			varLen := encodeUint64(uint64(len(val)))
			buf = append(buf, varLen...)
			buf = append(buf, val...)
		default:
			return nil, ErrEncodeRow
		}
	}

	return buf, nil
}

func DecodeRow(data []byte, schema *TableSchema) (Row, error) {
	if len(schema.Columns) == 0 {
		return Row{}, nil
	}

	nullBitmapSize := (len(schema.Columns) + 7) / 8
	if len(data) < nullBitmapSize {
		return Row{}, ErrDecodeRow
	}

	nullBitmap := data[:nullBitmapSize]
	offset := nullBitmapSize

	values := make([][]byte, len(schema.Columns))

	for i := range schema.Columns {
		bit := (nullBitmap[i/8] >> (i % 8)) & 1
		if bit == 1 {
			values[i] = nil
			continue
		}

		col := schema.Columns[i]
		var val []byte

		switch col.Type {
		case CTInt, CTBigInt, CTTimestamp:
			if offset+8 > len(data) {
				return Row{}, ErrDecodeRow
			}
			val = data[offset : offset+8]
			offset += 8
		case CTFloat:
			if offset+8 > len(data) {
				return Row{}, ErrDecodeRow
			}
			val = data[offset : offset+8]
			offset += 8
		case CTBool:
			if offset+1 > len(data) {
				return Row{}, ErrDecodeRow
			}
			val = data[offset : offset+1]
			offset += 1
		case CTVarchar, CTText, CTBlob:
			length, n := decodeUint64(data[offset:])
			offset += n
			if offset+int(length) > len(data) {
				return Row{}, ErrDecodeRow
			}
			val = data[offset : offset+int(length)]
			offset += int(length)
		default:
			return Row{}, ErrDecodeRow
		}

		values[i] = val
	}

	return Row{Values: values}, nil
}

func EncodeBlock(kvs []Pair, restartInterval int) ([]byte, error) {
	if len(kvs) == 0 {
		return nil, nil
	}

	if restartInterval <= 0 {
		restartInterval = 1
	}

	var buf []byte

	restarts := make([]int, 0, len(kvs)/restartInterval+1)

	for i, kv := range kvs {
		keyLen := encodeUint64(uint64(len(kv.Key)))
		valLen := encodeUint64(uint64(len(kv.Value)))

		buf = append(buf, keyLen...)
		buf = append(buf, kv.Key...)
		buf = append(buf, valLen...)
		buf = append(buf, kv.Value...)

		if (i+1)%restartInterval == 0 {
			restarts = append(restarts, len(buf))
		}
	}

	restarts = append(restarts, len(buf))

	for _, r := range restarts {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(r))
		buf = append(buf, b[:]...)
	}

	numRestarts := make([]byte, 4)
	binary.LittleEndian.PutUint32(numRestarts, uint32(len(restarts)))
	buf = append(buf, numRestarts...)

	return buf, nil
}

func DecodeBlock(data []byte) ([]KV, []int, error) {
	if len(data) == 0 {
		return nil, nil, nil
	}

	if len(data) < 4 {
		return nil, nil, ErrDecodeBlock
	}

	numRestarts := binary.LittleEndian.Uint32(data[len(data)-4:])

	restartOffset := len(data) - 4 - int(numRestarts)*4
	if restartOffset < 0 {
		return nil, nil, ErrDecodeBlock
	}

	restarts := make([]int, numRestarts)
	for i := uint32(0); i < numRestarts; i++ {
		restarts[i] = int(binary.LittleEndian.Uint32(data[restartOffset+int(i)*4:]))
	}

	var kvs []KV
	offset := 0

	for offset < restartOffset {
		keyLen, n := decodeUint64(data[offset:])
		offset += n

		if offset+int(keyLen) > restartOffset {
			return nil, nil, ErrDecodeBlock
		}
		key := data[offset : offset+int(keyLen)]
		offset += int(keyLen)

		valLen, n := decodeUint64(data[offset:])
		offset += n

		if offset+int(valLen) > restartOffset {
			return nil, nil, ErrDecodeBlock
		}
		val := data[offset : offset+int(valLen)]
		offset += int(valLen)

		kvs = append(kvs, KV{Key: key, Value: val})
	}

	return kvs, restarts, nil
}

func encodeUint64(v uint64) []byte {
	var buf [10]byte
	n := 0
	for v >= 0x80 {
		buf[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	buf[n] = byte(v)
	n++
	return buf[:n]
}

func decodeUint64(data []byte) (uint64, int) {
	var v uint64
	var shift int
	for i, b := range data {
		if b < 0x80 {
			v |= uint64(b) << shift
			return v, i + 1
		}
		v |= uint64(b&0x7F) << shift
		shift += 7
	}
	return 0, 0
}
