package sc

import (
	"errors"
	"math"
)

var (
	ErrNullValue      = errors.New("null value not allowed")
	ErrTypeMismatch   = errors.New("type mismatch")
	ErrConstraint     = errors.New("constraint violation")
	ErrInvalidValue   = errors.New("invalid value")
	ErrTableNotFound  = errors.New("table not found")
	ErrTableExists    = errors.New("table already exists")
	ErrInvalidTableID = errors.New("invalid table ID")
)

type Row struct {
	Values [][]byte
}

type TableSchema struct {
	TableID    uint64
	Name       string
	Columns    []ColumnDef
	PrimaryKey []int
}

type ColumnDef struct {
	Name       string
	Type       ColumnType
	Nullable   bool
	Default    []byte
	PrimaryKey bool
}

type ColumnType TokenType

const (
	CTInt       ColumnType = 58
	CTBigInt    ColumnType = 59
	CTVarchar   ColumnType = 64
	CTFloat     ColumnType = 60
	CTBool      ColumnType = 61
	CTText      ColumnType = 62
	CTBlob      ColumnType = 63
	CTTimestamp ColumnType = 65
)

// ValidateRow checks every column in row against the schema. Returns
// ErrNullValue for non-nullable nil columns, ErrTypeMismatch for size
// violations, or ErrInvalidValue for unknown types.
func ValidateRow(row Row, schema *TableSchema) error {
	for i, col := range schema.Columns {
		val := row.Values[i]

		if val == nil {
			if !col.Nullable {
				return ErrNullValue
			}
			continue
		}

		if err := ValidateType(val, col.Type); err != nil {
			return err
		}
	}

	return nil
}

// ValidateType checks that val has the correct size for the given column
// type. Returns ErrTypeMismatch on violation, ErrInvalidValue for unknown
// types, and nil if val is nil.
func ValidateType(val []byte, colType ColumnType) error {
	if val == nil {
		return nil
	}

	switch colType {
	case CTInt, CTBigInt, CTFloat, CTTimestamp:
		if len(val) != 8 {
			return ErrTypeMismatch
		}
	case CTBool:
		if len(val) != 1 {
			return ErrTypeMismatch
		}
	case CTVarchar, CTText:
	case CTBlob:
	default:
		return ErrInvalidValue
	}

	return nil
}

// CompareColumnDef returns true if a and b have identical Name, Type,
// Nullable, and PrimaryKey fields.
func CompareColumnDef(a, b ColumnDef) bool {
	if a.Name != b.Name {
		return false
	}
	if a.Type != b.Type {
		return false
	}
	if a.Nullable != b.Nullable {
		return false
	}
	if a.PrimaryKey != b.PrimaryKey {
		return false
	}
	return true
}

func EncodeInt(v int64) []byte {
	buf := make([]byte, 8)
	putInt64(buf, v)
	return buf
}

func DecodeInt(data []byte) (int64, error) {
	if len(data) != 8 {
		return 0, ErrTypeMismatch
	}
	return getInt64(data), nil
}

func EncodeFloat(v float64) []byte {
	buf := make([]byte, 8)
	putFloat64(buf, v)
	return buf
}

func DecodeFloat(data []byte) (float64, error) {
	if len(data) != 8 {
		return 0, ErrTypeMismatch
	}
	return getFloat64(data), nil
}

func EncodeBool(v bool) []byte {
	if v {
		return []byte{1}
	}
	return []byte{0}
}

func DecodeBool(data []byte) (bool, error) {
	if len(data) != 1 {
		return false, ErrTypeMismatch
	}
	return data[0] != 0, nil
}

func EncodeVarchar(v string) []byte {
	return []byte(v)
}

// EncodeVarcharBytes returns v directly without copying (zero-copy identity).
func EncodeVarcharBytes(v []byte) []byte {
	return v
}

func DecodeVarchar(data []byte) (string, error) {
	return string(data), nil
}

func EncodeText(v string) []byte {
	return []byte(v)
}

// EncodeTextBytes returns v directly without copying (zero-copy identity).
func EncodeTextBytes(v []byte) []byte {
	return v
}

func DecodeText(data []byte) (string, error) {
	return string(data), nil
}

func EncodeBlob(v []byte) []byte {
	return v
}

func DecodeBlob(data []byte) ([]byte, error) {
	return data, nil
}

func EncodeTimestamp(v int64) []byte {
	buf := make([]byte, 8)
	putInt64(buf, v)
	return buf
}

func DecodeTimestamp(data []byte) (int64, error) {
	return DecodeInt(data)
}

func putInt64(buf []byte, v int64) {
	uv := uint64(v)
	buf[0] = byte(uv)
	buf[1] = byte(uv >> 8)
	buf[2] = byte(uv >> 16)
	buf[3] = byte(uv >> 24)
	buf[4] = byte(uv >> 32)
	buf[5] = byte(uv >> 40)
	buf[6] = byte(uv >> 48)
	buf[7] = byte(uv >> 56)
}

func getInt64(buf []byte) int64 {
	v := int64(buf[0])
	v |= int64(buf[1]) << 8
	v |= int64(buf[2]) << 16
	v |= int64(buf[3]) << 24
	v |= int64(buf[4]) << 32
	v |= int64(buf[5]) << 40
	v |= int64(buf[6]) << 48
	v |= int64(buf[7]) << 56
	return v
}

func EncodeBigInt(v int64) []byte {
	buf := make([]byte, 8)
	putInt64(buf, v)
	return buf
}

func DecodeBigInt(data []byte) (int64, error) {
	if len(data) != 8 {
		return 0, ErrTypeMismatch
	}
	return getInt64(data), nil
}

type Validator struct{}

func NewValidator() *Validator { return &Validator{} }

func (v *Validator) ValidateRow(row Row, schema *TableSchema) error {
	return ValidateRow(row, schema)
}

func (v *Validator) CompareColumnDef(a, b ColumnDef) bool {
	return CompareColumnDef(a, b)
}

func (v *Validator) ValidateConstraints(val []byte, col *ColumnDef) error {
	if val == nil {
		if !col.Nullable {
			return ErrNullValue
		}
		return nil
	}
	return ValidateType(val, col.Type)
}

func putFloat64(buf []byte, v float64) {
	bits := math.Float64bits(v)
	putInt64(buf, int64(bits))
}

func getFloat64(buf []byte) float64 {
	bits := uint64(getInt64(buf))
	return math.Float64frombits(bits)
}
