// Package TB: schema_codec.go is the binary codec for TableSchema.
//
// The wire format is length-prefixed binary, all multi-byte
// integers in binary.LittleEndian order:
//
//	[nameLen:4][name:bytes]
//	[colCount:4]
//	(colCount times):
//	    [nameLen:4][name:bytes]
//	    [type:1]
//	    [nullable:1]
//	    [defaultLen:4][default:bytes]
//	[pkCount:4]
//	(pkCount times):
//	    [colIndex:4]
//
// The format is self-describing (column types and lengths are
// inline) and stable across LS engine reopens. The codec is
// deliberately not MessagePack (see iter-10 gap analysis item 1);
// the choice keeps the third-party dependency surface at zero.
package tb

import (
	"encoding/binary"
	"errors"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

var (
	// ErrSchemaCodecTruncated is returned when the input is
	// shorter than the format's length prefixes claim.
	ErrSchemaCodecTruncated = errors.New("tb: schema codec truncated input")

	// ErrSchemaCodecColumnCount is returned when colCount is
	// negative or larger than the input can hold.
	ErrSchemaCodecColumnCount = errors.New("tb: schema codec invalid column count")

	// ErrSchemaCodecPKCount is returned when pkCount is negative
	// or larger than the input can hold.
	ErrSchemaCodecPKCount = errors.New("tb: schema codec invalid pk count")
)

// MarshalTableSchema serializes schema into the catalog wire format.
// The returned slice is owned by the caller; the codec does not
// retain references to the input.
func MarshalTableSchema(schema *sc.TableSchema) ([]byte, error) {
	if schema == nil {
		return nil, errors.New("tb: MarshalTableSchema: nil schema")
	}

	// Compute total size up front for a single allocation.
	size := 4 + len(schema.Name)
	size += 4
	for _, col := range schema.Columns {
		size += 4 + len(col.Name)
		size += 1 + 1
		size += 4 + len(col.Default)
	}
	size += 4
	size += 4 * len(schema.PrimaryKey)

	buf := make([]byte, 0, size)

	// Name.
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(schema.Name)))
	buf = append(buf, schema.Name...)

	// Columns.
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(schema.Columns)))
	for _, col := range schema.Columns {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(col.Name)))
		buf = append(buf, col.Name...)
		buf = append(buf, byte(col.Type))
		var nullable byte
		if col.Nullable {
			nullable = 1
		}
		buf = append(buf, nullable)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(col.Default)))
		buf = append(buf, col.Default...)
	}

	// Primary key.
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(schema.PrimaryKey)))
	for _, idx := range schema.PrimaryKey {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(idx))
	}

	return buf, nil
}

// UnmarshalTableSchema is the inverse of MarshalTableSchema. It
// returns ErrSchemaCodecTruncated on any input that is shorter than
// its length prefixes claim, and ErrSchemaCodecColumnCount /
// ErrSchemaCodecPKCount on out-of-range counts.
func UnmarshalTableSchema(data []byte) (*sc.TableSchema, error) {
	schema := &sc.TableSchema{}
	offset := 0

	// Name.
	if offset+4 > len(data) {
		return nil, ErrSchemaCodecTruncated
	}
	nameLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	if nameLen < 0 || offset+nameLen > len(data) {
		return nil, ErrSchemaCodecTruncated
	}
	schema.Name = string(data[offset : offset+nameLen])
	offset += nameLen

	// Columns.
	if offset+4 > len(data) {
		return nil, ErrSchemaCodecTruncated
	}
	colCount := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	if colCount < 0 {
		return nil, ErrSchemaCodecColumnCount
	}
	schema.Columns = make([]sc.ColumnDef, colCount)
	for i := 0; i < colCount; i++ {
		col := &schema.Columns[i]
		if offset+4 > len(data) {
			return nil, ErrSchemaCodecTruncated
		}
		cnLen := int(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
		if cnLen < 0 || offset+cnLen > len(data) {
			return nil, ErrSchemaCodecTruncated
		}
		col.Name = string(data[offset : offset+cnLen])
		offset += cnLen

		if offset+2 > len(data) {
			return nil, ErrSchemaCodecTruncated
		}
		col.Type = sc.ColumnType(data[offset])
		col.Nullable = data[offset+1] != 0
		offset += 2

		if offset+4 > len(data) {
			return nil, ErrSchemaCodecTruncated
		}
		defLen := int(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
		if defLen < 0 || offset+defLen > len(data) {
			return nil, ErrSchemaCodecTruncated
		}
		if defLen > 0 {
			col.Default = append([]byte(nil), data[offset:offset+defLen]...)
		}
		offset += defLen
	}

	// Primary key.
	if offset+4 > len(data) {
		return nil, ErrSchemaCodecTruncated
	}
	pkCount := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	if pkCount < 0 {
		return nil, ErrSchemaCodecPKCount
	}
	schema.PrimaryKey = make([]int, pkCount)
	for i := 0; i < pkCount; i++ {
		if offset+4 > len(data) {
			return nil, ErrSchemaCodecTruncated
		}
		schema.PrimaryKey[i] = int(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
	}

	return schema, nil
}
