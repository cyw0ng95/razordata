// Package DP is the deparser cluster for the storage engine.
//
// This file (row.go) owns the row-level encoding. A row is encoded
// as:
//
//	[nullBitmap][per-column value bytes]
//
// The null bitmap has one bit per column; the high bit of the first
// byte corresponds to column 0. NULL columns contribute no bytes to
// the per-column payload. For non-NULL columns, fixed-width types
// (CTInt, CTBigInt, CTFloat, CTBool, CTTimestamp) contribute exactly
// the type's width; variable-width types (CTVarchar, CTText, CTBlob)
// contribute a varint length followed by the raw bytes.
//
// The encoding is column-major in the sense that the value bytes are
// laid out in schema column order. This makes decoding sequential
// and avoids per-column type-tag overhead.
//
// This file was moved from ENG/LS/deparser.go in iter-10 (Phase 0).
// Behavior is unchanged from the original v1 implementation; only
// the package path differs.
package dp

import (
	"errors"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

var (
	ErrEncodeRow = errors.New("failed to encode row")
	ErrDecodeRow = errors.New("failed to decode row")
)

// Pair is a key-value pair used by SST block encoding (see block.go).
// It is also useful as a return type for ad-hoc scans.
type Pair struct {
	Key   []byte
	Value []byte
}

// KV is the decoded form returned by DecodeBlock. The distinction
// from Pair is intentional: Pair is what callers hand to EncodeBlock,
// KV is what they get back from DecodeBlock. They have identical
// fields and behavior is symmetric, but the name difference keeps the
// call sites self-documenting.
type KV struct {
	Key   []byte
	Value []byte
}

// EncodeRow serializes row per the schema. A row with the wrong
// number of values (relative to the schema's column count) returns
// ErrEncodeRow. NULL columns are recorded in the bitmap only; they
// contribute no payload bytes. Variable-length columns are
// length-prefixed with an unsigned varint.
func EncodeRow(row sc.Row, schema *sc.TableSchema) ([]byte, error) {
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
		case sc.CTInt, sc.CTBigInt, sc.CTTimestamp:
			buf = append(buf, val...)
		case sc.CTFloat:
			buf = append(buf, val...)
		case sc.CTBool:
			buf = append(buf, val...)
		case sc.CTVarchar, sc.CTText, sc.CTBlob:
			varLen := encodeUint64(uint64(len(val)))
			buf = append(buf, varLen...)
			buf = append(buf, val...)
		default:
			return nil, ErrEncodeRow
		}
	}

	return buf, nil
}

// DecodeRow is the inverse of EncodeRow. A schema with zero columns
// returns an empty row (this matches the v1 behavior and is exercised
// by TestDecodeRow_EmptySchema). Truncation, a malformed bitmap, or
// a value of the wrong width for its declared type all return
// ErrDecodeRow.
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
			length, n := decodeUint64(data[offset:])
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
