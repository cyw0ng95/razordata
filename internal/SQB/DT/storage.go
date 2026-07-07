package DT

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
)

// Storage encoding/decoding helpers moved from OP/store.go (REQ??).

var (
	// ErrTableNotRegisteredForStorage is returned when an operator is asked
	// to route through the storage engine for a table not in the catalog.
	ErrTableNotRegisteredForStorage = errors.New("DT: table not registered for storage")
	// ErrNoPKForStorage is returned when a storage-write operation has no PK.
	ErrNoPKForStorage = errors.New("DT: cannot write to storage without a primary key")
)

// These functions form the storage-engine-agnostic key encoding
// and row codec used by SeqScan, IndexScan, IndexSeekScan, writers,
// and any operator that needs to build a storage key or serialize a
// row. They depend only on DT-level types (StoreSchema, Row, Value,
// Store) — never on operator types — so they live in DT where any
// future store/SQL consumer can call them without creating a package
// dependency on SQB/OP or SQB/EV.

// rowValueType tags the binary encoding of a single value within a row.
const (
	rvNull   byte = 0
	rvInt    byte = 1
	rvString byte = 2
	rvBool   byte = 3
	rvFloat  byte = 4
	rvBytes  byte = 5
)

// encodeRowBufPool is a sync.Pool for reusable EncodeRow scratch buffers.
// REQ000985: reduces GC pressure on the hot path (bulk INSERT/UPDATE).
var encodeRowBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 4096) // 4KB page size
		return &buf
	},
}

// valueSlicePool is a sync.Pool for reusable Value slices in DecodeRow.
// REQ001226: eliminates per-row make([]Value, N) on the hot path.
var valueSlicePool = sync.Pool{
	New: func() any {
		return make([]Value, 0, 16)
	},
}

// PoolValueSlice retrieves a []Value from the pool, growing if needed.
func PoolValueSlice(n int) []Value {
	slice := valueSlicePool.Get().([]Value)
	if cap(slice) < n {
		slice = make([]Value, 0, n)
	}
	return slice[:n]
}

// PutValueSlice returns a Value slice to the pool for reuse.
// Callers should invoke this when a decoded row's Data is no longer needed.
func PutValueSlice(s []Value) {
	if cap(s) <= 128 {
		valueSlicePool.Put(s[:0])
	}
}

// TablePrefix returns the storage key prefix for a table, or nil if the
// table is not registered.
func TablePrefix(name string) []byte {
	id, ok := TableIDFor(name)
	if !ok {
		return nil
	}
	return EncodeTablePrefix(id)
}

// EncodeTablePrefix encodes a numeric table ID as the storage key prefix.
func EncodeTablePrefix(id uint64) []byte {
	// REQ001032: use stack array to avoid heap allocation.
	var buf [9]byte
	binary.BigEndian.PutUint64(buf[:8], id)
	buf[8] = ':'
	return buf[:]
}

// EncodeRow serializes a row's values in schema column order. nil values
// become rvNull. The output is a self-describing binary blob:
//
//	[col_count:varint] for each col: [type:1][value_bytes...]
func EncodeRow(schema *StoreSchema, row Row) ([]byte, error) {
	if len(row.Data) != len(schema.Cols) {
		return nil, fmt.Errorf("DT: row has %d values, schema has %d", len(row.Data), len(schema.Cols))
	}
	// REQ000985: use pooled buffer to reduce allocations on hot path.
	bufPtr := encodeRowBufPool.Get().(*[]byte)
	buf := *bufPtr
	buf = buf[:0]
	// REQ001022: pre-estimate buffer size to avoid 3-4x growth reallocations.
	if cap(buf) < 9*len(schema.Cols)+8 {
		buf = make([]byte, 0, 9*len(schema.Cols)+8)
	}
	// REQ001279: use fixed-width single byte for column count when < 255.
	if len(schema.Cols) < 255 {
		buf = append(buf, byte(len(schema.Cols)))
	} else {
		buf = append(buf, 255)
		buf = binary.AppendUvarint(buf, uint64(len(schema.Cols)))
	}
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
			return nil, fmt.Errorf("DT: unsupported value kind %d at column %d", v.Kind, i)
		}
	}
	// Return a copy of the buffer so the pooled buffer can be reused.
	result := make([]byte, len(buf))
	copy(result, buf)
	*bufPtr = buf
	encodeRowBufPool.Put(bufPtr)
	return result, nil
}

// DecodeRow is the inverse of EncodeRow.
func DecodeRow(data []byte, schema *StoreSchema) (Row, error) {
	if data == nil || len(data) == 0 {
		return Row{}, errors.New("DT: empty row payload")
	}
	off := 0
	readVarint := func() (uint64, error) {
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return 0, errors.New("DT: bad varint")
		}
		off += n
		return v, nil
	}
	n, err := readVarint()
	if err != nil {
		return Row{}, err
	}
	if int(n) != len(schema.Cols) {
		return Row{}, fmt.Errorf("DT: row has %d cols, schema %d", n, len(schema.Cols))
	}
	dataSlice := PoolValueSlice(len(schema.Cols))
	row := Row{
		Cols:     schema.Cols, // share schema's cols slice (immutable)
		Data:     dataSlice,
		ColIndex: schema.ColIndex, // share schema's pre-built index (no allocation)
	}
	for i := 0; i < int(n); i++ {
		if off >= len(data) {
			return Row{}, errors.New("DT: truncated row")
		}
		tag := data[off]
		off++
		switch tag {
		case rvNull:
			row.Data[i] = NullValue()
		case rvInt:
			if off+8 > len(data) {
				return Row{}, errors.New("DT: truncated int")
			}
			row.Data[i] = NewIntValue(int64(binary.BigEndian.Uint64(data[off : off+8])))
			off += 8
		case rvFloat:
			if off+8 > len(data) {
				return Row{}, errors.New("DT: truncated float")
			}
			row.Data[i] = NewFloatValue(math.Float64frombits(binary.BigEndian.Uint64(data[off : off+8])))
			off += 8
		case rvBool:
			if off+1 > len(data) {
				return Row{}, errors.New("DT: truncated bool")
			}
			row.Data[i] = NewBoolValue(data[off] != 0)
			off++
		case rvString:
			l, err := readVarint()
			if err != nil {
				return Row{}, err
			}
			if off+int(l) > len(data) {
				return Row{}, errors.New("DT: truncated string")
			}
			row.Data[i] = NewTextValue(string(data[off : off+int(l)]))
			off += int(l)
		case rvBytes:
			l, err := readVarint()
			if err != nil {
				return Row{}, err
			}
			if off+int(l) > len(data) {
				return Row{}, errors.New("DT: truncated bytes")
			}
			row.Data[i] = NewBlobValue(append([]byte{}, data[off:off+int(l)]...))
			off += int(l)
		default:
			return Row{}, fmt.Errorf("DT: unknown row tag %d", tag)
		}
	}
	return row, nil
}

// RowKey builds a storage key for a row: <tablePrefix><pk-bytes>.
func RowKey(prefix []byte, pkValue any) []byte {
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
			out = append(out, 0)
		}
	// REQ001023: stack-friendly fast path for float64 PKs.
	case float64:
		out = out[:len(prefix)+8]
		binary.BigEndian.PutUint64(out[len(prefix):], math.Float64bits(v))
	default:
		out = append(out, fmt.Sprintf("%v", v)...)
	}
	return out
}

// ExtractPK returns the value of the primary-key column from a row.
// For REQ000367 (hidden-PK DT.Tables, no PRIMARY KEY declared at
// CREATE TABLE time), this allocates and returns a synthetic int64
// rowid that is unique within the table.
func ExtractPK(schema *StoreSchema, row Row) (any, error) {
	if schema.Pk == "" {
		if !schema.HiddenPK {
			return nil, errors.New("DT: table has no primary key")
		}
		id := schema.NextRowID.Add(1)
		return id, nil
	}
	for i, c := range schema.Cols {
		if c == schema.Pk {
			if row.Data[i].IsNull() {
				if !schema.HiddenPK {
					// Need the generated ID to be > all existing PKs.
					// NextRowID already tracks the max seen so far;
					// the Add(1) below will give max+1.
					schema.HiddenPK = true
				}
				id := schema.NextRowID.Add(1)
				return id, nil
			}
			// Track the max PK value so NULL -> auto-generated values
			// are always > existing values (REQ001128).
			if !schema.HiddenPK {
				if row.Data[i].Kind == KindInt && row.Data[i].I64 >= schema.NextRowID.Load() {
					schema.NextRowID.Store(row.Data[i].I64)
				}
			}
			return row.Data[i], nil
		}
	}
	return nil, fmt.Errorf("DT: pk column %q not in schema", schema.Pk)
}

// ExtractPKForUpdate returns the primary key to use when writing
// the updated row. For regular PK DT.Tables, delegates to ExtractPK.
// For hidden-PK DT.Tables, reuses the original row's key suffix from
// storeKey so the update overwrites the same engine row instead of
// allocating a new synthetic rowid on every UPDATE (REQ000501).
func ExtractPKForUpdate(schema *StoreSchema, oldRow Row, prefix []byte) (any, error) {
	if schema.Pk == "" && schema.HiddenPK && len(oldRow.StoreKey) > len(prefix) {
		suffix := oldRow.StoreKey[len(prefix):]
		return int64(binary.BigEndian.Uint64(suffix)), nil
	}
	return ExtractPK(schema, oldRow)
}

// MaintainIndexesOnInsert populates secondary-index entries
// for a newly-inserted row. iter-22 secondary indexes MVP.
// Returns the first error encountered, or nil on success.
func MaintainIndexesOnInsert(store Store, table string, schema *StoreSchema, row Row) error {
	indexes := GetRegisteredIndexes(table)
	if len(indexes) == 0 {
		return nil
	}
	tableID, ok := TableIDFor(table)
	if !ok {
		return nil
	}
	pk, err := ExtractPK(schema, row)
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
		fullKey := BuildIndexKey(tableID, idx.Name, key)
		if err := store.Insert(fullKey, pkBytes); err != nil {
			return fmt.Errorf("DT: index %q insert: %w", idx.Name, err)
		}
	}
	return nil
}

// MaintainIndexesOnDelete removes secondary-index entries for a
// deleted row. iter-22.
func MaintainIndexesOnDelete(store Store, table string, schema *StoreSchema, row Row) error {
	indexes := GetRegisteredIndexes(table)
	if len(indexes) == 0 {
		return nil
	}
	tableID, ok := TableIDFor(table)
	if !ok {
		return nil
	}
	for _, idx := range indexes {
		key := indexValueFor(schema, row, idx.Columns)
		if key == nil {
			continue
		}
		fullKey := BuildIndexKey(tableID, idx.Name, key)
		if err := store.Delete(fullKey); err != nil {
			return fmt.Errorf("DT: index %q delete: %w", idx.Name, err)
		}
	}
	return nil
}

// MaintainIndexesOnUpdate updates secondary-index entries when
// the indexed column value changes. The pk parameter is the primary
// key of the row being updated (extracted from oldRow to preserve
// the original key for hidden-PK DT.Tables). iter-22 secondary indexes.
func MaintainIndexesOnUpdate(store Store, table string, schema *StoreSchema, oldRow, newRow Row, pk any) error {
	indexes := GetRegisteredIndexes(table)
	if len(indexes) == 0 {
		return nil
	}
	tableID, ok := TableIDFor(table)
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
		oldFull := BuildIndexKey(tableID, idx.Name, oldKey)
		newFull := BuildIndexKey(tableID, idx.Name, newKey)
		// If the key didn't change, no-op.
		if bytes.Equal(oldFull, newFull) {
			continue
		}
		// Key changed: delete old, insert new.
		if err := store.Delete(oldFull); err != nil {
			return fmt.Errorf("DT: index %q update delete: %w", idx.Name, err)
		}
		if err := store.Insert(newFull, pkBytes); err != nil {
			return fmt.Errorf("DT: index %q update insert: %w", idx.Name, err)
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
		return nil, fmt.Errorf("DT: unsupported pk type %T", pk)
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

// BuildIndexKey synthesizes the index keyspace prefix for use with
// StorePrefix/IndexSeekScan operators:
//
//	__idx__:<tableID>:<indexName>:<indexValue>
//
// Used for both Put and Get on the secondary-index keyspace.
// iter-22 secondary indexes MVP.
func BuildIndexKey(tableID uint64, indexName string, indexValue []byte) []byte {
	out := make([]byte, 0, 32+len(indexName)+len(indexValue))
	out = append(out, "__idx__:"...)
	encodeUint64BE(&out, tableID)
	out = append(out, ':')
	out = append(out, indexName...)
	out = append(out, ':')
	out = append(out, indexValue...)
	return out
}

// encodeUint64BE appends a big-endian uint64 to the byte slice.
func encodeUint64BE(buf *[]byte, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	*buf = append(*buf, b[:]...)
}

// IndexValueFromKey strips `__idx__:<tableID>:<idxName>:` from the key
// and returns the remaining bytes (the indexed column value). Returns
// nil if the key does not start with the expected prefix.
func IndexValueFromKey(key []byte, prefix []byte) []byte {
	if len(key) < len(prefix) {
		return nil
	}
	if !bytes.Equal(key[:len(prefix)], prefix) {
		return nil
	}
	return key[len(prefix):]
}

// TableHandle bundles a Store, StoreSchema, and the table's cached
// storage-key prefix into a single value that callers (INSERT/UPDATE/
// DELETE writers, FK validation, and any future operator that needs to
// read or write rows of a specific table) can use without repeating the
// TablePrefix → RowKey → EncodeRow → store.{Put,Get,Delete} dance at
// every call site. REQ000987.
//
// TableHandle is the single source of truth for "where do rows of
// table T live in the engine and how do I encode them". Prior to
// REQ000987 this logic was inlined in writers.go at 8 sites and in
// fk.go at 1 site; see writers.go commit history.
type TableHandle struct {
	// Store is the engine store to read/write through. May be nil
	// for in-memory tests that only exercise TableHandle.EncodeRow.
	Store Store
	// Schema describes the row layout (column order, PK column, NULL
	// constraints, generated columns, secondary indexes).
	Schema *StoreSchema
	// Table is the table name. Cached here so callers don't need to
	// carry it alongside the handle (writers.go uses it for
	// MaintainIndexesOn{Insert,Update,Delete} which need a name).
	Table string
	// prefix is the table's storage key prefix. Cached at OpenTable
	// time so InsertRow/UpdateRow/DeleteRow/GetRow can avoid the
	// per-call TablePrefix lookup + TableIDFor mutex.
	prefix []byte
}

// OpenTable returns a TableHandle for `table` using `store`. Returns
// ErrTableNotRegisteredForStorage if the table has no registered
// schema or no assigned tableID (i.e. it was never registered with
// the engine, even via the in-memory fallback).
//
// OpenTable is the canonical entry point for the REQ000987
// abstraction: writers that previously did
// `prefix := DT.TablePrefix(t); ss := DT.SchemaFor(t); pk, _ := DT.ExtractPK(ss, row)`
// now do `h, err := DT.OpenTable(store, t); err := h.InsertRow(row)`.
func OpenTable(store Store, table string) (*TableHandle, error) {
	ss, ok := SchemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	prefix := TablePrefix(table)
	if prefix == nil {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &TableHandle{
		Store:  store,
		Schema: ss,
		Table:  table,
		prefix: prefix,
	}, nil
}

// Prefix returns the storage key prefix for this table's rows.
// Exposed for callers that need to do prefix-scoped scans.
func (h *TableHandle) Prefix() []byte { return h.prefix }

// InsertRow encodes `row`, builds the row key, writes it through the
// store, and maintains secondary indexes on success. On PK NULL
// with a declared PK column (REQ001128), the synthesized synthetic
// rowid is written back into row.Data so the persisted cell value
// matches the generated ID, matching SQLite's INTEGER PRIMARY KEY
// auto-fill behavior.
//
// InsertRow returns the storage key and encoded buffer alongside the
// error so callers that need to feed a TxWriter (transaction rollback
// log) don't have to re-extract the PK and re-encode the row. The
// returned key/buf are byte-stable for the lifetime of the call.
//
// InsertRow is the single replacement for the 8-call-site pattern
// that previously appeared in writers.go Insert/Update/Delete paths.
func (h *TableHandle) InsertRow(row Row) (key []byte, buf []byte, err error) {
	pk, err := ExtractPK(h.Schema, row)
	if err != nil {
		return nil, nil, err
	}
	// REQ001128: when PK was NULL (ExtractPK generated a synthetic
	// rowid), update the row data so the stored value matches the
	// generated ID instead of NULL. Mirrors SQLite's INTEGER PRIMARY
	// KEY auto-fill.
	if pkInt, ok := pk.(int64); ok && h.Schema.Pk != "" {
		for pi, pc := range h.Schema.Cols {
			if pc == h.Schema.Pk && pi < len(row.Data) && row.Data[pi].Kind == KindNull {
				row.Data[pi] = NewIntValue(pkInt)
				break
			}
		}
	}
	buf, err = EncodeRow(h.Schema, row)
	if err != nil {
		return nil, nil, err
	}
	key = RowKey(h.prefix, pk)
	if err := h.Store.Insert(key, buf); err != nil {
		return nil, nil, err
	}
	if err := MaintainIndexesOnInsert(h.Store, h.Table, h.Schema, row); err != nil {
		return key, buf, err
	}
	return key, buf, nil
}

// MustInsertRow is a convenience wrapper for callers that don't need
// the returned key/buf (e.g. tests or non-transactional writers).
func (h *TableHandle) MustInsertRow(row Row) error {
	_, _, err := h.InsertRow(row)
	return err
}

// UpdateRow is like InsertRow but extracts the PK from `oldRow`
// (preserving the original key for hidden-PK tables per REQ000501)
// and maintains secondary indexes for the change.
//
// `newRow` is the row to encode and write; `oldRow` is the
// pre-update row used for PK extraction and old-index removal.
func (h *TableHandle) UpdateRow(oldRow, newRow Row) (key []byte, buf []byte, err error) {
	pk, err := ExtractPKForUpdate(h.Schema, oldRow, h.prefix)
	if err != nil {
		return nil, nil, err
	}
	buf, err = EncodeRow(h.Schema, newRow)
	if err != nil {
		return nil, nil, err
	}
	key = RowKey(h.prefix, pk)
	if err := h.Store.Insert(key, buf); err != nil {
		return nil, nil, err
	}
	if err := MaintainIndexesOnUpdate(h.Store, h.Table, h.Schema, oldRow, newRow, pk); err != nil {
		return key, buf, err
	}
	return key, buf, nil
}

// MustUpdateRow is a convenience wrapper for callers that don't need
// the returned key/buf (e.g. tests or non-transactional writers).
func (h *TableHandle) MustUpdateRow(oldRow, newRow Row) error {
	_, _, err := h.UpdateRow(oldRow, newRow)
	return err
}

// DeleteRow extracts the PK from `row` (preserving the original key
// for hidden-PK tables) and removes both the primary row entry and
// any secondary index entries. Returns ErrNoPKForStorage if the
// row has no extractable PK.
//
// DeleteRow returns the storage key alongside the error so TxWriter
// callers can log the delete without re-extracting the PK.
func (h *TableHandle) DeleteRow(row Row) (key []byte, err error) {
	pk, err := ExtractPKForUpdate(h.Schema, row, h.prefix)
	if err != nil {
		return nil, err
	}
	key = RowKey(h.prefix, pk)
	if err := h.Store.Delete(key); err != nil {
		return nil, err
	}
	if err := MaintainIndexesOnDelete(h.Store, h.Table, h.Schema, row); err != nil {
		return key, err
	}
	return key, nil
}

// MustDeleteRow is a convenience wrapper for callers that don't need
// the returned key (e.g. tests or non-transactional writers).
func (h *TableHandle) MustDeleteRow(row Row) error {
	_, err := h.DeleteRow(row)
	return err
}

// GetRow reads the encoded row bytes for `pk` and decodes them.
// Returns (Row{}, false, nil) if the key is not present.
// This wraps Store.Get with the schema decode step so callers
// don't have to repeat the RowKey + DecodeRow dance at every
// call site.
func (h *TableHandle) GetRow(pk any) (Row, bool, error) {
	key := RowKey(h.prefix, pk)
	buf, found, err := h.Store.Get(key)
	if err != nil || !found {
		return Row{}, false, err
	}
	row, err := DecodeRow(buf, h.Schema)
	if err != nil {
		return Row{}, false, err
	}
	// Tag the row with its storage key so a subsequent UpdateRow
	// can reuse the same PK even for hidden-PK tables.
	row.StoreKey = append([]byte(nil), key...)
	return row, true, nil
}

// Exists reports whether a row with PK `pk` is present in the store.
// Convenience wrapper around GetRow for callers that only need the
// boolean (e.g. INSERT OR IGNORE conflict detection).
func (h *TableHandle) Exists(pk any) (bool, error) {
	_, found, err := h.Store.Get(RowKey(h.prefix, pk))
	return found, err
}
