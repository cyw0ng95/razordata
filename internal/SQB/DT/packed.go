package DT

import (
	"encoding/binary"
	"errors"
	"math"
)

// PackedRow stores fixed-width column values as a contiguous []byte buffer
// with no per-column type tags. REQ001222: eliminates the 64-byte Value
// tagged-union overhead for integer-only tables.
//
// Layout per column (all fixed-width, in schema order):
//
//	INT:    9 bytes — 1 byte null tag + 8 bytes int64
//	FLOAT:  9 bytes — 1 byte null tag + 8 bytes float64
//	BOOL:   1 byte  — 0x00=null, 0x01=true, 0x02=false
//
// The packed format requires all columns to be the same type (checked
// at schema creation time). If mixed types are detected, the caller
// falls back to the standard Value-based DecodeRow.
type PackedRow struct {
	data    []byte
	schema  *StoreSchema
	colSize int // bytes per column in the packed format
}

// nullTag marks a column as NULL in the packed format.
const nullTag byte = 0

// columnSize returns the packed size for a single column of the given type.
func columnSize(colType int) int {
	switch colType {
	case int(KindInt):
		return 9 // 1 null tag + 8 int64
	case int(KindFloat):
		return 9 // 1 null tag + 8 float64
	case int(KindBool):
		return 1 // null/true/false
	default:
		return 0
	}
}

// CanPack returns true if all columns in the schema are the same fixed-width
// type that supports packed encoding. This is checked at plan time so that
// mixed-type tables fall back to the standard DecodeRow path.
func (s *StoreSchema) CanPack() bool {
	if len(s.Cols) == 0 || len(s.ColTypes) == 0 {
		return false
	}
	t := int(s.ColTypes[0])
	cs := columnSize(t)
	if cs == 0 {
		return false
	}
	for i := 1; i < len(s.ColTypes); i++ {
		if int(s.ColTypes[i]) != t {
			return false
		}
	}
	return true
}

// packInts encodes a slice of int64 values into the packed row format.
// A sentinel value of math.MinInt64 is used to represent NULL.
func PackInts(buf []byte, vals []int64, nulls []bool, colSize int) {
	off := 0
	for i, v := range vals {
		if nulls[i] {
			buf[off] = nullTag
		} else {
			buf[off] = 1
			binary.BigEndian.PutUint64(buf[off+1:off+9], uint64(v))
		}
		off += colSize
	}
}

// UnpackInts decodes packed int columns from the buffer. Returns the
// unpacked values, which the caller owns.
func UnpackInts(data []byte, colSize int, n int) []int64 {
	out := make([]int64, n)
	off := 0
	for i := 0; i < n; i++ {
		if data[off] == nullTag {
			out[i] = 0
		} else {
			out[i] = int64(binary.BigEndian.Uint64(data[off+1 : off+9]))
		}
		off += colSize
	}
	return out
}

// UnpackFloats decodes packed float64 columns from the buffer.
func UnpackFloats(data []byte, n int) []float64 {
	out := make([]float64, n)
	off := 0
	for i := 0; i < n; i++ {
		if data[off] == nullTag {
			out[i] = 0
		} else {
			out[i] = math.Float64frombits(binary.BigEndian.Uint64(data[off+1 : off+9]))
		}
		off += 9
	}
	return out
}

// UnpackBools decodes packed bool columns from the buffer.
func UnpackBools(data []byte, n int) []bool {
	out := make([]bool, n)
	for i := 0; i < n; i++ {
		out[i] = data[i] == 1
	}
	return out
}

// packRow encodes a Row into a packed []byte buffer for a homogeneous
// fixed-width schema. Returns nil if the schema cannot be packed.
func PackRow(schema *StoreSchema, row Row) []byte {
	if !schema.CanPack() {
		return nil
	}
	colSize := columnSize(int(schema.ColTypes[0]))
	buf := make([]byte, len(schema.Cols)*colSize)
	t := int(schema.ColTypes[0])
	for i, v := range row.Data {
		off := i * colSize
		switch t {
		case int(KindInt):
			if v.IsNull() {
				buf[off] = nullTag
			} else {
				buf[off] = 1
				binary.BigEndian.PutUint64(buf[off+1:off+9], uint64(v.I64))
			}
		case int(KindFloat):
			if v.IsNull() {
				buf[off] = nullTag
			} else {
				buf[off] = 1
				binary.BigEndian.PutUint64(buf[off+1:off+9], math.Float64bits(v.F64))
			}
		case int(KindBool):
			if v.IsNull() {
				buf[off] = nullTag
			} else if v.Bo {
				buf[off] = 1
			} else {
				buf[off] = 2
			}
		}
	}
	return buf
}

// unpackRowInto decodes a packed buffer into a pre-allocated row,
// populating row.Data with Value structs. The row's Data slice must
// have exactly nCols entries.
func UnpackRowInto(row *Row, data []byte, schema *StoreSchema) error {
	if !schema.CanPack() {
		return errors.New("DT: schema does not support packed format")
	}
	n := len(schema.Cols)
	if len(row.Data) != n {
		return errors.New("DT: row.Data length mismatch")
	}
	colSize := columnSize(int(schema.ColTypes[0]))
	t := int(schema.ColTypes[0])
	off := 0
	for i := 0; i < n; i++ {
		switch t {
		case int(KindInt):
			if data[off] == nullTag {
				row.Data[i] = NullValue()
			} else {
				row.Data[i] = NewIntValue(int64(binary.BigEndian.Uint64(data[off+1 : off+9])))
			}
		case int(KindFloat):
			if data[off] == nullTag {
				row.Data[i] = NullValue()
			} else {
				row.Data[i] = NewFloatValue(math.Float64frombits(binary.BigEndian.Uint64(data[off+1 : off+9])))
			}
		case int(KindBool):
			switch data[off] {
			case nullTag:
				row.Data[i] = NullValue()
			case 1:
				row.Data[i] = NewBoolValue(true)
			case 2:
				row.Data[i] = NewBoolValue(false)
			default:
				return errors.New("DT: invalid bool packed tag")
			}
		}
		off += colSize
	}
	return nil
}

// int is a zero-cost alias for LX.TokenType used in CanPack.
// We can't import SQF/LX here (dependency order), so we use the
// raw integer constants that match the Kind enum.
// TokenType alias for schema column type comparison.
// We cannot import SQF/LX here (dependency order violation).
type TokenType int
