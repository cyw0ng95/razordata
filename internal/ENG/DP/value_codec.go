// Package DP is the deparser cluster for the storage engine. It owns
// the binary representation of values, rows, and SST data blocks.
//
// This file (value_codec.go) holds the per-type scalar encoders. Each
// ColumnType in the SC package has a matching Encode*/Decode* pair
// here. The encoding is little-endian for fixed-width numerics, the
// raw bytes for variable-width text/blob, and a single byte (0/1) for
// booleans. NULL is represented as a nil slice at the row level
// (handled by row.go's null bitmap); these codecs never see a nil
// input.
//
// This file was moved from ENG/LS/schema.go in iter-10 (Phase 0).
// Behavior is unchanged from the original v1 implementation; only
// the package path differs.
package dp

import (
	"math"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

// EncodeInt encodes v as 8 little-endian bytes. The bit pattern of
// the int64 is preserved verbatim (no two's-complement inversion).
func EncodeInt(v int64) []byte {
	buf := make([]byte, 8)
	putInt64(buf, v)
	return buf
}

// DecodeInt decodes 8 bytes produced by EncodeInt. Any other length
// returns sc.ErrTypeMismatch.
func DecodeInt(data []byte) (int64, error) {
	if len(data) != 8 {
		return 0, sc.ErrTypeMismatch
	}
	return getInt64(data), nil
}

// EncodeBigInt is the v1 alias for EncodeInt (BIGINT is 8 bytes, same
// wire format as INT).
func EncodeBigInt(v int64) []byte {
	return EncodeInt(v)
}

// DecodeBigInt is the v1 alias for DecodeInt.
func DecodeBigInt(data []byte) (int64, error) {
	return DecodeInt(data)
}

// EncodeFloat encodes v as 8 IEEE-754 little-endian bytes via
// math.Float64bits.
func EncodeFloat(v float64) []byte {
	buf := make([]byte, 8)
	putFloat64(buf, v)
	return buf
}

// DecodeFloat decodes 8 bytes produced by EncodeFloat.
func DecodeFloat(data []byte) (float64, error) {
	if len(data) != 8 {
		return 0, sc.ErrTypeMismatch
	}
	return getFloat64(data), nil
}

// EncodeBool encodes v as a single byte: 1 for true, 0 for false.
// Using a single byte keeps row layout trivially aligned.
func EncodeBool(v bool) []byte {
	if v {
		return []byte{1}
	}
	return []byte{0}
}

// DecodeBool decodes the single byte produced by EncodeBool.
func DecodeBool(data []byte) (bool, error) {
	if len(data) != 1 {
		return false, sc.ErrTypeMismatch
	}
	return data[0] != 0, nil
}

// EncodeVarchar is a passthrough: varchar is stored as the raw UTF-8
// bytes of the string. Length is recorded separately in the row
// header, so no framing is needed here.
func EncodeVarchar(v string) []byte {
	return []byte(v)
}

// DecodeVarchar is the inverse of EncodeVarchar.
func DecodeVarchar(data []byte) (string, error) {
	return string(data), nil
}

// EncodeText is the v1 alias for EncodeVarchar (TEXT and VARCHAR
// share the same wire format).
func EncodeText(v string) []byte {
	return EncodeVarchar(v)
}

// DecodeText is the v1 alias for DecodeVarchar.
func DecodeText(data []byte) (string, error) {
	return DecodeVarchar(data)
}

// EncodeBlob returns v unchanged.
func EncodeBlob(v []byte) []byte {
	return v
}

// DecodeBlob returns data unchanged.
func DecodeBlob(data []byte) ([]byte, error) {
	return data, nil
}

// EncodeTimestamp encodes v as 8 little-endian bytes. The caller is
// responsible for choosing the epoch convention (Unix seconds is the
// v1 default).
func EncodeTimestamp(v int64) []byte {
	buf := make([]byte, 8)
	putInt64(buf, v)
	return buf
}

// DecodeTimestamp decodes 8 bytes produced by EncodeTimestamp.
func DecodeTimestamp(data []byte) (int64, error) {
	if len(data) != 8 {
		return 0, sc.ErrTypeMismatch
	}
	return getInt64(data), nil
}

// putInt64 writes v into buf as 8 little-endian bytes. buf must have
// len >= 8. The bit pattern is preserved (no sign inversion).
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

// getInt64 reads 8 little-endian bytes from buf as an int64.
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

// putFloat64 writes v into buf as 8 IEEE-754 little-endian bytes.
func putFloat64(buf []byte, v float64) {
	bits := math.Float64bits(v)
	putInt64(buf, int64(bits))
}

// getFloat64 reads 8 IEEE-754 little-endian bytes from buf as a
// float64.
func getFloat64(buf []byte) float64 {
	bits := uint64(getInt64(buf))
	return math.Float64frombits(bits)
}
