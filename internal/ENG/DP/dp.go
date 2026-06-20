package dp

import (
	"encoding/binary"
	"errors"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

var (
	ErrEncodeRow   = errors.New("failed to encode row")
	ErrDecodeRow   = errors.New("failed to decode row")
	ErrEncodeBlock = errors.New("failed to encode block")
	ErrDecodeBlock = errors.New("failed to decode block")
)

type KV struct {
	Key   []byte
	Value []byte
}

func varintLen(v uint64) int {
	n := 0
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n + 1
}

func EncodeRow(row sc.Row, schema *sc.TableSchema) ([]byte, error) {
	if len(row.Values) != len(schema.Columns) {
		return nil, ErrEncodeRow
	}

	nullBitmapSize := (len(schema.Columns) + 7) / 8
	totalSize := nullBitmapSize
	for i, col := range schema.Columns {
		val := row.Values[i]
		if val == nil {
			continue
		}
		switch col.Type {
		case sc.CTInt, sc.CTBigInt, sc.CTTimestamp:
			totalSize += 8
		case sc.CTFloat:
			totalSize += 8
		case sc.CTBool:
			totalSize += 1
		case sc.CTVarchar, sc.CTText, sc.CTBlob:
			totalSize += varintLen(uint64(len(val))) + len(val)
		}
	}
	buf := make([]byte, 0, totalSize)

	buf = buf[:nullBitmapSize]
	for i := range buf {
		buf[i] = 0
	}
	for i, val := range row.Values {
		if val == nil {
			buf[i/8] |= 1 << (i % 8)
		}
	}

	for i, col := range schema.Columns {
		val := row.Values[i]
		if val == nil {
			continue
		}

		switch col.Type {
		case sc.CTInt, sc.CTBigInt, sc.CTTimestamp:
			buf = append(buf, val...)
		case sc.CTFloat:
			buf = append(buf, val...)
		case sc.CTBool:
			buf = append(buf, val...)
		case sc.CTVarchar, sc.CTText, sc.CTBlob:
			varLen := EncodeUint64(uint64(len(val)))
			buf = append(buf, varLen...)
			buf = append(buf, val...)
		default:
			return nil, ErrEncodeRow
		}
	}

	return buf, nil
}

func DecodeRow(data []byte, schema *sc.TableSchema) (sc.Row, error) {
	if len(schema.Columns) == 0 {
		return sc.Row{}, nil
	}

	nullBitmapSize := (len(schema.Columns) + 7) / 8
	if len(data) < nullBitmapSize {
		return sc.Row{}, ErrDecodeRow
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
		case sc.CTInt, sc.CTBigInt, sc.CTTimestamp:
			if offset+8 > len(data) {
				return sc.Row{}, ErrDecodeRow
			}
			val = data[offset : offset+8]
			offset += 8
		case sc.CTFloat:
			if offset+8 > len(data) {
				return sc.Row{}, ErrDecodeRow
			}
			val = data[offset : offset+8]
			offset += 8
		case sc.CTBool:
			if offset+1 > len(data) {
				return sc.Row{}, ErrDecodeRow
			}
			val = data[offset : offset+1]
			offset += 1
		case sc.CTVarchar, sc.CTText, sc.CTBlob:
			length, n := DecodeUint64(data[offset:])
			offset += n
			if offset+int(length) > len(data) {
				return sc.Row{}, ErrDecodeRow
			}
			val = data[offset : offset+int(length)]
			offset += int(length)
		default:
			return sc.Row{}, ErrDecodeRow
		}

		values[i] = val
	}

	return sc.Row{Values: values}, nil
}

func EncodeBlock(kvs []KV, restartInterval int) ([]byte, error) {
	if len(kvs) == 0 {
		return nil, nil
	}

	if restartInterval <= 0 {
		restartInterval = 1
	}

	totalSize := 0
	for _, kv := range kvs {
		totalSize += varintLen(uint64(len(kv.Key))) + len(kv.Key)
		totalSize += varintLen(uint64(len(kv.Value))) + len(kv.Value)
	}
	numRestarts := len(kvs)/restartInterval + 1
	totalSize += numRestarts*4 + 4
	buf := make([]byte, 0, totalSize)

	restarts := make([]int, 0, len(kvs)/restartInterval+1)

	for i, kv := range kvs {
		keyLen := EncodeUint64(uint64(len(kv.Key)))
		valLen := EncodeUint64(uint64(len(kv.Value)))

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

	var numBuf [4]byte
	binary.LittleEndian.PutUint32(numBuf[:], uint32(len(restarts)))
	buf = append(buf, numBuf[:]...)

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

	kvs := make([]KV, 0, numRestarts)
	offset := 0

	for offset < restartOffset {
		keyLen, n := DecodeUint64(data[offset:])
		offset += n

		if offset+int(keyLen) > restartOffset {
			return nil, nil, ErrDecodeBlock
		}
		key := data[offset : offset+int(keyLen)]
		offset += int(keyLen)

		valLen, n := DecodeUint64(data[offset:])
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

func EncodeUint64(v uint64) []byte {
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

func DecodeUint64(data []byte) (uint64, int) {
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
