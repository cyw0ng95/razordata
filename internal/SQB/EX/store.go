package EX

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// Store and StatsCatalog are now in DT.
type Store = DT.Store
type StatsCatalog = DT.StatsCatalog

// ErrNoEngine is returned when a query requires a wired store but the
// executor was constructed without one.
var ErrNoEngine = errors.New("ex: no engine wired; use NewExecutorWithEngine")

// Type aliases for schema types now in DT.
type StoreSchema = DT.StoreSchema
type UniqueKey = DT.UniqueKey
type ForeignKeyConstraint = DT.ForeignKeyConstraint
type RegisteredIndex = DT.RegisteredIndex

// tablePrefix returns the storage key prefix for a table, or nil if the
// table is not registered.
func tablePrefix(name string) []byte {
	id, ok := DT.TableIDFor(name)
	if !ok {
		return nil
	}
	return encodeTablePrefix(id)
}

func encodeTablePrefix(id uint64) []byte {
	// REQ001032: use stack array to avoid heap allocation.
	var buf [9]byte
	binary.BigEndian.PutUint64(buf[:8], id)
	buf[8] = ':'
	return buf[:]
}

// rowValueType tags the binary encoding of a single value within a row.
const (
	rvNull   byte = 0
	rvInt    byte = 1
	rvString byte = 2
	rvBool   byte = 3
	rvFloat  byte = 4
	rvBytes  byte = 5
)

// encodeRowBufPool is a sync.Pool for reusable encodeRow scratch buffers.
// REQ000985: reduces GC pressure on the hot path (bulk INSERT/UPDATE).
var encodeRowBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 4096) // 4KB page size
		return &buf
	},
}

// encodeRow serializes a row's values in schema column order. nil values
// become rvNull. The output is a self-describing binary blob:
//
//	[col_count:varint] for each col: [type:1][value_bytes...]
func encodeRow(schema *StoreSchema, row Row) ([]byte, error) {
	if len(row.Data) != len(schema.Cols) {
		return nil, fmt.Errorf("ex: row has %d values, schema has %d", len(row.Data), len(schema.Cols))
	}
	// REQ000985: use pooled buffer to reduce allocations on hot path.
	bufPtr := encodeRowBufPool.Get().(*[]byte)
	buf := *bufPtr
	buf = buf[:0]
	// REQ001022: pre-estimate buffer size to avoid 3-4x growth reallocations.
	if cap(buf) < 9*len(schema.Cols)+8 {
		buf = make([]byte, 0, 9*len(schema.Cols)+8)
	}
	buf = binary.AppendUvarint(buf, uint64(len(schema.Cols)))
	for i, v := range row.Data {
		if v.IsNull() {
			buf = append(buf, rvNull)
			continue
		}
		switch v.Kind {
		case KindInt:
			buf = append(buf, rvInt)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], uint64(v.I64))
			buf = append(buf, b[:]...)
		case KindFloat:
			buf = append(buf, rvFloat)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], math.Float64bits(v.F64))
			buf = append(buf, b[:]...)
		case KindText:
			buf = append(buf, rvString)
			buf = binary.AppendUvarint(buf, uint64(len(v.S)))
			buf = append(buf, v.S...)
		case KindBool:
			buf = append(buf, rvBool)
			if v.Bo {
				buf = append(buf, 1)
			} else {
				buf = append(buf, 0)
			}
		case KindBlob:
			buf = append(buf, rvBytes)
			buf = binary.AppendUvarint(buf, uint64(len(v.B)))
			buf = append(buf, v.B...)
		default:
			encodeRowBufPool.Put(bufPtr)
			return nil, fmt.Errorf("ex: unsupported value kind %d at column %d", v.Kind, i)
		}
	}
	// Return a copy of the buffer so the pooled buffer can be reused.
	result := make([]byte, len(buf))
	copy(result, buf)
	*bufPtr = buf
	encodeRowBufPool.Put(bufPtr)
	return result, nil
}

// decodeRow is the inverse of encodeRow.
func decodeRow(data []byte, schema *StoreSchema) (Row, error) {
	if len(data) == 0 {
		return Row{}, errors.New("ex: empty row payload")
	}
	off := 0
	readVarint := func() (uint64, error) {
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return 0, errors.New("ex: bad varint")
		}
		off += n
		return v, nil
	}
	n, err := readVarint()
	if err != nil {
		return Row{}, err
	}
	if int(n) != len(schema.Cols) {
		return Row{}, fmt.Errorf("ex: row has %d cols, schema %d", n, len(schema.Cols))
	}
	dataSlice := make([]Value, len(schema.Cols))
	row := Row{
		Cols:     schema.Cols, // share schema's cols slice (immutable)
		Data:     dataSlice,
		ColIndex: schema.ColIndex, // share schema's pre-built index (no allocation)
	}
	for i := 0; i < int(n); i++ {
		if off >= len(data) {
			return Row{}, errors.New("ex: truncated row")
		}
		tag := data[off]
		off++
		switch tag {
		case rvNull:
			row.Data[i] = NullValue()
		case rvInt:
			if off+8 > len(data) {
				return Row{}, errors.New("ex: truncated int")
			}
			row.Data[i] = NewIntValue(int64(binary.BigEndian.Uint64(data[off : off+8])))
			off += 8
		case rvFloat:
			if off+8 > len(data) {
				return Row{}, errors.New("ex: truncated float")
			}
			row.Data[i] = NewFloatValue(math.Float64frombits(binary.BigEndian.Uint64(data[off : off+8])))
			off += 8
		case rvBool:
			if off+1 > len(data) {
				return Row{}, errors.New("ex: truncated bool")
			}
			row.Data[i] = NewBoolValue(data[off] != 0)
			off++
		case rvString:
			l, err := readVarint()
			if err != nil {
				return Row{}, err
			}
			if off+int(l) > len(data) {
				return Row{}, errors.New("ex: truncated string")
			}
			row.Data[i] = NewTextValue(string(data[off : off+int(l)]))
			off += int(l)
		case rvBytes:
			l, err := readVarint()
			if err != nil {
				return Row{}, err
			}
			if off+int(l) > len(data) {
				// REQ000776: rvBytes is now decoded as KindBlob.
				return Row{}, errors.New("ex: truncated bytes")
			}
			// REQ000776: rvBytes is now decoded as KindBlob.
			row.Data[i] = NewBlobValue(append([]byte{}, data[off:off+int(l)]...))
			off += int(l)
		default:
			return Row{}, fmt.Errorf("ex: unknown row tag %d", tag)
		}
	}
	return row, nil
}

// rowKey builds a storage key for a row: <tablePrefix><pk-bytes>.
func rowKey(prefix []byte, pkValue any) []byte {
	// REQ001023: stack-friendly fast path for int64 PKs.
	out := make([]byte, 0, len(prefix)+16)
	out = append(out, prefix...)
	if v, ok := pkValue.(Value); ok {
		pkValue = v.ToAny()
	}
	switch v := pkValue.(type) {
	case int64:
		out = out[:len(prefix)+8]
		binary.BigEndian.PutUint64(out[len(prefix):], uint64(v))
	case string:
		out = append(out, v...)
	case []byte:
		out = append(out, v...)
	// REQ001023: stack-friendly fast path for bool PKs.
	case bool:
		if v {
			out = append(out, 1)
		} else {
			// REQ001023: stack-friendly fast path for float64 PKs.
			out = append(out, 0)
		}
	case float64:
		out = out[:len(prefix)+8]
		binary.BigEndian.PutUint64(out[len(prefix):], math.Float64bits(v))
	default:
		out = append(out, fmt.Sprintf("%v", v)...)
	}
	return out
}

// extractPK returns the value of the primary-key column from a row.
// For REQ000367 (hidden-PK DT.Tables, no PRIMARY KEY declared at
// CREATE TABLE time), this allocates and returns a synthetic int64
// rowid that is unique within the table.
func extractPK(schema *StoreSchema, row Row) (any, error) {
	if schema.Pk == "" {
		if !schema.HiddenPK {
			return nil, errors.New("ex: table has no primary key")
		}
		id := schema.NextRowID.Add(1)
		return id, nil
	}
	for i, c := range schema.Cols {
		if c == schema.Pk {
			if row.Data[i].IsNull() {
				if !schema.HiddenPK {
					schema.HiddenPK = true
				}
				id := schema.NextRowID.Add(1)
				return id, nil
			}
			return row.Data[i], nil
		}
	}
	return nil, fmt.Errorf("ex: pk column %q not in schema", schema.Pk)
}

// extractPKForUpdate returns the primary key to use when writing
// the updated row. For regular PK DT.Tables, delegates to extractPK.
// For hidden-PK DT.Tables, reuses the original row's key suffix from
// storeKey so the update overwrites the same engine row instead of
// allocating a new synthetic rowid on every UPDATE (REQ000501).
func extractPKForUpdate(schema *StoreSchema, oldRow Row, prefix []byte) (any, error) {
	if schema.Pk == "" && schema.HiddenPK && len(oldRow.StoreKey) > len(prefix) {
		suffix := oldRow.StoreKey[len(prefix):]
		return int64(binary.BigEndian.Uint64(suffix)), nil
	}
	return extractPK(schema, oldRow)
}

// maintainIndexesOnInsert populates secondary-index entries
// for a newly-inserted row. iter-22 secondary indexes MVP.
// Returns the first error encountered, or nil on success.
func maintainIndexesOnInsert(store Store, table string, schema *StoreSchema, row Row) error {
	indexes := DT.GetRegisteredIndexes(table)
	if len(indexes) == 0 {
		return nil
	}
	tableID, ok := DT.TableIDFor(table)
	if !ok {
		return nil
	}
	pk, err := extractPK(schema, row)
	if err != nil {
		return err
	}
	pkBytes, err := pkToBytes(pk)
	if err != nil {
		return err
	}
	for _, idx := range indexes {
		key := indexValueFor(schema, row, idx.Columns)
		if key == nil {
			continue
		}
		fullKey := buildIndexKey(tableID, idx.Name, key)
		if err := store.Insert(fullKey, pkBytes); err != nil {
			return fmt.Errorf("ex: index %q insert: %w", idx.Name, err)
		}
	}
	return nil
}

// maintainIndexesOnDelete removes secondary-index entries for a
// deleted row. iter-22.
func maintainIndexesOnDelete(store Store, table string, schema *StoreSchema, row Row) error {
	indexes := DT.GetRegisteredIndexes(table)
	if len(indexes) == 0 {
		return nil
	}
	tableID, ok := DT.TableIDFor(table)
	if !ok {
		return nil
	}
	for _, idx := range indexes {
		// REQ001023: stack-friendly fast path for index key extraction.
		key := indexValueFor(schema, row, idx.Columns)
		if key == nil {
			continue
		}
		fullKey := buildIndexKey(tableID, idx.Name, key)
		if err := store.Delete(fullKey); err != nil {
			return fmt.Errorf("ex: index %q delete: %w", idx.Name, err)
		}
	}
	return nil
}

// maintainIndexesOnUpdate updates secondary-index entries when
// the indexed column value changes. The pk parameter is the primary
// key of the row being updated (extracted from oldRow to preserve
// the original key for hidden-PK DT.Tables). iter-22 secondary indexes.
func maintainIndexesOnUpdate(store Store, table string, schema *StoreSchema, oldRow, newRow Row, pk any) error {
	indexes := DT.GetRegisteredIndexes(table)
	if len(indexes) == 0 {
		return nil
	}
	tableID, ok := DT.TableIDFor(table)
	if !ok {
		return nil
	}
	pkBytes, err := pkToBytes(pk)
	if err != nil {
		return err
	}
	for _, idx := range indexes {
		oldKey := indexValueFor(schema, oldRow, idx.Columns)
		newKey := indexValueFor(schema, newRow, idx.Columns)
		if oldKey == nil || newKey == nil {
			continue
		}
		oldFull := buildIndexKey(tableID, idx.Name, oldKey)
		newFull := buildIndexKey(tableID, idx.Name, newKey)
		// If the key didn't change, no-op.
		if bytes.Equal(oldFull, newFull) {
			continue
		}
		// Key changed: delete old, insert new.
		if err := store.Delete(oldFull); err != nil {
			return fmt.Errorf("ex: index %q update delete: %w", idx.Name, err)
		}
		if err := store.Insert(newFull, pkBytes); err != nil {
			return fmt.Errorf("ex: index %q update insert: %w", idx.Name, err)
		}
	}
	return nil
}

// pkToBytes encodes a primary-key value as bytes (big-endian
// for int, raw for string/bytes). Used to populate the value
// side of an index entry.
func pkToBytes(pk any) ([]byte, error) {
	if v, ok := pk.(Value); ok {
		pk = v.ToAny()
	}
	switch v := pk.(type) {
	case int64:
		b := int64ToBytesBigEndian(v)
		return b[:], nil
	case int:
		b := int64ToBytesBigEndian(int64(v))
		return b[:], nil
	case string:
		return []byte(v), nil
	case []byte:
		return append([]byte(nil), v...), nil
	default:
		return nil, fmt.Errorf("ex: unsupported pk type %T", pk)
	}
}

// int64ToBytesBigEndian encodes an int64 as 8 bytes big-endian.
func int64ToBytesBigEndian(n int64) [8]byte {
	// REQ001033: return stack array to avoid heap allocation.
	var b [8]byte
	u := uint64(n)
	b[7] = byte(u)
	b[6] = byte(u >> 8)
	b[5] = byte(u >> 16)
	b[4] = byte(u >> 24)
	b[3] = byte(u >> 32)
	b[2] = byte(u >> 40)
	b[1] = byte(u >> 48)
	b[0] = byte(u >> 56)
	return b
}

// indexValueFor extracts the index key from a row. Multi-column
// indexes concatenate each column's value with a 0x00 separator.
// Returns nil if any referenced column is missing from the row.
func indexValueFor(schema *StoreSchema, row Row, cols []string) []byte {
	if len(cols) == 0 {
		return nil
	}
	out := []byte{}
	for i, c := range cols {
		var val any
		found := false
		for j, sc := range schema.Cols {
			if sc == c {
				if j < len(row.Data) {
					val = row.Data[j]
					found = true
				}
				break
			}
		}
		if !found {
			return nil
		}
		if i > 0 {
			out = append(out, 0x00) // separator
		}
		b, err := pkToBytes(val)
		if err != nil {
			return nil
		}
		out = append(out, b...)
	}
	return out
}