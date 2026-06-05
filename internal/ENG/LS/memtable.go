package ls

import (
	"bytes"
	"sync/atomic"
)

const defaultMemtableSize = 64 * 1024 * 1024

type memtable struct {
	skiplist *skipList
	size     atomic.Int64
	maxSize  int64
	frozen   atomic.Bool
	refs     atomic.Int64
}

func newMemtable(maxSize int64) *memtable {
	return &memtable{
		skiplist: New(),
		maxSize:  maxSize,
	}
}

func (m *memtable) Insert(key, value []byte) error {
	m.skiplist.Insert(key, value)
	m.size.Add(int64(len(key) + len(value)))
	return nil
}

func (m *memtable) Get(key []byte) ([]byte, bool) {
	return m.skiplist.Find(key)
}

func (m *memtable) Iterator() *Iterator {
	return m.skiplist.Iterator()
}

func (m *memtable) Size() int64 {
	return m.size.Load()
}

func (m *memtable) Len() int64 {
	return m.skiplist.Len()
}

func (m *memtable) ShouldFlush() bool {
	return m.size.Load() >= m.maxSize
}

func (m *memtable) Freeze() {
	m.frozen.Store(true)
}

func (m *memtable) IsFrozen() bool {
	return m.frozen.Load()
}

func (m *memtable) IncRef() {
	m.refs.Add(1)
}

func (m *memtable) DecRef() {
	m.refs.Add(-1)
}

func (m *memtable) RefCount() int64 {
	return m.refs.Load()
}

type tableSchema struct {
	TableID    uint64
	Name       string
	Columns    []columnDef
	PrimaryKey []int
}

type columnDef struct {
	Name       string
	Type       columnType
	Nullable   bool
	Default    []byte
	PrimaryKey bool
}

type columnType uint8

const (
	ctInt columnType = iota
	ctBigInt
	ctVarchar
	ctFloat
	ctBool
	ctText
	ctBlob
	ctTimestamp
)

func encodeRow(schema *tableSchema, values [][]byte) ([]byte, error) {
	var buf bytes.Buffer
	nullBitmap := make([]byte, (len(schema.Columns)+7)/8)

	for i, col := range schema.Columns {
		if values[i] == nil {
			if !col.Nullable {
				return nil, ErrNotNullConstraint
			}
			nullBitmap[i/8] |= 1 << (i % 8)
			continue
		}

		switch col.Type {
		case ctInt:
			buf.Write(values[i])
		case ctBigInt:
			buf.Write(values[i])
		case ctVarchar, ctText:
			varintBuf := encodeVarint(int64(len(values[i])))
			buf.Write(varintBuf)
			buf.Write(values[i])
		case ctFloat:
			buf.Write(values[i])
		case ctBool:
			buf.Write(values[i])
		case ctBlob:
			varintBuf := encodeVarint(int64(len(values[i])))
			buf.Write(varintBuf)
			buf.Write(values[i])
		case ctTimestamp:
			buf.Write(values[i])
		}
	}

	header := make([]byte, 4+len(nullBitmap))
	writeInt32(header, 0, int32(nullBitmap[0]))
	writeInt32(header, 4, int32(len(nullBitmap)))

	buf.Write(nullBitmap)
	return buf.Bytes(), nil
}

func decodeRow(data []byte, schema *tableSchema) ([][]byte, error) {
	if len(data) < 4 {
		return nil, ErrInvalidRowData
	}

	nullBitmapLen := int(data[0]) | int(data[1])<<8 | int(data[2])<<16 | int(data[3])<<24
	nullBitmap := data[4 : 4+nullBitmapLen]

	values := make([][]byte, len(schema.Columns))
	offset := 4 + nullBitmapLen

	for i := range schema.Columns {
		if nullBitmap[i/8]&(1<<(i%8)) != 0 {
			values[i] = nil
			continue
		}

		switch schema.Columns[i].Type {
		case ctInt, ctBigInt, ctFloat, ctBool:
			size := 8
			if schema.Columns[i].Type == ctBool {
				size = 1
			}
			if offset+size > len(data) {
				return nil, ErrInvalidRowData
			}
			values[i] = data[offset : offset+size]
			offset += size
		case ctVarchar, ctText, ctBlob:
			varlen, n := decodeVarint(data[offset:])
			offset += n
			if offset+int(varlen) > len(data) {
				return nil, ErrInvalidRowData
			}
			values[i] = data[offset : offset+int(varlen)]
			offset += int(varlen)
		case ctTimestamp:
			if offset+8 > len(data) {
				return nil, ErrInvalidRowData
			}
			values[i] = data[offset : offset+8]
			offset += 8
		}
	}

	return values, nil
}

func encodeVarint(val int64) []byte {
	var buf [10]byte
	n := 0
	for {
		if n >= len(buf) {
			return buf[:n]
		}
		buf[n] = byte(val & 0x7f)
		if val >>= 7; val == 0 {
			break
		}
		buf[n] |= 0x80
		n++
	}
	return buf[:n+1]
}

func decodeVarint(data []byte) (int64, int) {
	var val int64
	var shift uint
	for i, b := range data {
		if b < 0x80 {
			return val | int64(b)<<shift, i + 1
		}
		val |= int64(b&0x7f) << shift
		shift += 7
	}
	return val, len(data)
}

func writeInt32(buf []byte, offset int, val int32) {
	buf[offset] = byte(val)
	buf[offset+1] = byte(val >> 8)
	buf[offset+2] = byte(val >> 16)
	buf[offset+3] = byte(val >> 24)
}

var ErrNotNullConstraint = &encodingError{"not null constraint violated"}
var ErrInvalidRowData = &encodingError{"invalid row data"}

type encodingError struct {
	msg string
}

func (e *encodingError) Error() string {
	return e.msg
}
