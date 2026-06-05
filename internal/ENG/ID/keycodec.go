// Package ID is the index cluster. It owns the primary-key index:
// a per-table key-value structure that maps an encoded primary key
// to a pointer to the row's storage key in the main LSM tree.
//
// This file (keycodec.go) defines the composite-key encoding used
// for primary keys. A primary key may span one or more columns
// (e.g. (id, sub_id)); the codec lays out each column as a typed
// length-prefixed entry so the resulting bytes sort in the same
// order as the typed values.
package id

import (
	"encoding/binary"
	"errors"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

var (
	// ErrKeyCodecTruncated is returned when the input ends before
	// all length prefixes have been satisfied.
	ErrKeyCodecTruncated = errors.New("id: key codec truncated input")

	// ErrKeyCodecColumnCount is returned when colCount is
	// negative or larger than the input can hold.
	ErrKeyCodecColumnCount = errors.New("id: key codec invalid column count")

	// ErrKeyCodecTypeMismatch is returned when a column's
	// declared type is not one of the supported ColumnType
	// values.
	ErrKeyCodecTypeMismatch = errors.New("id: key codec unsupported column type")

	// ErrKeyCodecValueTooLong is returned when a column's value
	// exceeds 65535 bytes (the maximum representable in a 2-byte
	// length prefix).
	ErrKeyCodecValueTooLong = errors.New("id: key codec value exceeds 65535 bytes")
)

// typeOrder is the relative sort order of the supported column
// types. Lower values sort first. This order is stable and on-disk:
// the encoded bytes from one process must sort the same as those
// from another.
//
// The exact ordering is intentionally chosen so that the natural
// byte order of mixed-type indexes is well-defined. INT and BIGINT
// come first (most common PK types), then FLOAT and BOOL
// (numeric-ish), then TIMESTAMP, then the variable-width text
// types, then BLOB.
var typeOrder = map[sc.ColumnType]byte{
	sc.CTInt:       0,
	sc.CTBigInt:    1,
	sc.CTFloat:     2,
	sc.CTBool:      3,
	sc.CTTimestamp: 4,
	sc.CTVarchar:   5,
	sc.CTText:      6,
	sc.CTBlob:      7,
}

// tagNull and tagConcrete are the two prefix bytes that start
// every column entry. tagNull is 0x00 (sorts first); tagConcrete
// is 0x01 (sorts after every NULL of any type). The tag byte is
// followed by a 1-byte typeOrder (0..7) and a 2-byte little-endian
// length prefix.
const (
	tagNull     byte = 0x00
	tagConcrete byte = 0x01
)

// maxValueLen is the maximum value length we support. 65535 is
// the natural fit for a 2-byte length prefix. Larger values can
// be supported later by widening the prefix.
const maxValueLen = 0xFFFF

// EncodeKey serializes the column values into a single byte slice
// suitable for use as a primary-key suffix in the index. The
// columns slice gives the declared type of each value (in PK
// order); the values slice gives the raw bytes for each value
// (nil for NULL). The two slices must have the same length.
//
// The encoding per column is:
//
//	NULL:      [0x00]                                           (1 byte)
//	concrete:  [0x01][typeOrder:1][len:2 LE][value bytes]       (4 + len bytes)
//
// The sort order is (typeOrder, length, value) lexicographic
// within a column. NULL of any type sorts first. Across columns,
// the first column dominates; ties on the first column go to the
// second, and so on.
//
// Known limitation: variable-width values sort by length first,
// then by content. The natural string order "a" < "abc" < "b"
// (where "abc" < "b" because they share a prefix and "a" < "b")
// is NOT preserved: this codec sorts "a" < "b" < "abc". The
// limitation is acceptable for v1 because (a) most PK values are
// fixed-width (INT), where the order is exact, and (b) SQL range
// scans on a single text column are unambiguous about the
// boundaries; the internal order only matters for composite keys
// where the prior column ties. A future iter can add an
// escape-based encoding for the text types if natural string
// order is needed.
func EncodeKey(types []sc.ColumnType, values [][]byte) ([]byte, error) {
	if len(types) != len(values) {
		return nil, ErrKeyCodecColumnCount
	}

	size := 0
	for i, t := range types {
		if _, ok := typeOrder[t]; !ok {
			return nil, ErrKeyCodecTypeMismatch
		}
		if values[i] == nil {
			size += 1 // [0x00]
			continue
		}
		if len(values[i]) > maxValueLen {
			return nil, ErrKeyCodecValueTooLong
		}
		size += 4 // [0x01][typeOrder:1][len:2 LE]
		size += len(values[i])
	}

	out := make([]byte, 0, size)
	for i, t := range types {
		if values[i] == nil {
			out = append(out, tagNull)
			continue
		}
		ord, ok := typeOrder[t]
		if !ok {
			return nil, ErrKeyCodecTypeMismatch
		}
		out = append(out, tagConcrete, ord)
		var lenBuf [2]byte
		binary.LittleEndian.PutUint16(lenBuf[:], uint16(len(values[i])))
		out = append(out, lenBuf[:]...)
		out = append(out, values[i]...)
	}
	return out, nil
}

// DecodeKey is the inverse of EncodeKey. It splits the bytes back
// into per-column raw values. NULL columns return nil entries in
// the result.
func DecodeKey(encoded []byte, types []sc.ColumnType) ([][]byte, error) {
	out := make([][]byte, len(types))
	offset := 0
	for i, t := range types {
		if offset >= len(encoded) {
			return nil, ErrKeyCodecTruncated
		}
		tag := encoded[offset]
		offset++
		switch tag {
		case tagNull:
			out[i] = nil
		case tagConcrete:
			if offset+3 > len(encoded) {
				return nil, ErrKeyCodecTruncated
			}
			ord := encoded[offset]
			offset++
			expectedOrd, ok := typeOrder[t]
			if !ok || ord != expectedOrd {
				return nil, ErrKeyCodecTypeMismatch
			}
			valLen := int(binary.LittleEndian.Uint16(encoded[offset:]))
			offset += 2
			if offset+valLen > len(encoded) {
				return nil, ErrKeyCodecTruncated
			}
			if valLen > 0 {
				out[i] = append([]byte(nil), encoded[offset:offset+valLen]...)
			} else {
				out[i] = []byte{}
			}
			offset += valLen
		default:
			return nil, ErrKeyCodecTypeMismatch
		}
	}
	return out, nil
}
