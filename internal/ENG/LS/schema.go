package ls

import (
	"errors"
	"math"
)

var (
	ErrNullValue    = errors.New("null value not allowed")
	ErrTypeMismatch = errors.New("type mismatch")
	ErrConstraint   = errors.New("constraint violation")
	ErrInvalidValue = errors.New("invalid value")
)

type Row struct {
	Values [][]byte
}

type Validator struct{}

func NewValidator() *Validator {
	return &Validator{}
}

func (v *Validator) ValidateRow(row Row, schema *TableSchema) error {
	for i, col := range schema.Columns {
		val := row.Values[i]

		if val == nil {
			if !col.Nullable {
				return ErrNullValue
			}
			continue
		}

		if err := v.ValidateType(val, col.Type); err != nil {
			return err
		}

		if err := v.ValidateConstraints(val, &col); err != nil {
			return err
		}
	}

	return nil
}

func (v *Validator) ValidateType(val []byte, colType ColumnType) error {
	if val == nil {
		return nil
	}

	switch colType {
	case CTInt:
		if len(val) != 8 {
			return ErrTypeMismatch
		}
	case CTBigInt:
		if len(val) != 8 {
			return ErrTypeMismatch
		}
	case CTFloat:
		if len(val) != 8 {
			return ErrTypeMismatch
		}
	case CTBool:
		if len(val) != 1 {
			return ErrTypeMismatch
		}
	case CTVarchar, CTText:
	case CTBlob:
	case CTTimestamp:
		if len(val) != 8 {
			return ErrTypeMismatch
		}
	default:
		return ErrInvalidValue
	}

	return nil
}

func (v *Validator) ValidateConstraints(val []byte, col *ColumnDef) error {
	if col.Default != nil && string(val) == string(col.Default) {
		return nil
	}

	return nil
}

func (v *Validator) CompareColumnDef(a, b ColumnDef) bool {
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

func EncodeBigInt(v int64) []byte {
	return EncodeInt(v)
}

func DecodeBigInt(data []byte) (int64, error) {
	return DecodeInt(data)
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

func DecodeVarchar(data []byte) (string, error) {
	return string(data), nil
}

func EncodeText(v string) []byte {
	return []byte(v)
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

func putFloat64(buf []byte, v float64) {
	bits := math.Float64bits(v)
	putInt64(buf, int64(bits))
}

func getFloat64(buf []byte) float64 {
	bits := uint64(getInt64(buf))
	return math.Float64frombits(bits)
}
