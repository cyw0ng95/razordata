package EX

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// Store is the minimal storage surface the executor needs to integrate
// with the real engine. The in-memory map (tables/schemas) is the fallback
// when Store is nil; when Store is non-nil, operators read from the engine.
type Store interface {
	Insert(key, value []byte) error
	Delete(key []byte) error
	// Get returns the value for an exact key match, or (nil, false, nil)
	// if the key is not present. Added in iter-22 to support secondary
	// index seeks (the index yields a primary key, then the executor
	// fetches the row via Get).
	Get(key []byte) ([]byte, bool, error)
	NewIterator(prefix []byte) ls.RangeIter
}

// ErrNoEngine is returned when a query requires a wired store but the
// executor was constructed without one.
var ErrNoEngine = errors.New("ex: no engine wired; use NewExecutorWithEngine")

// UniqueKey is a UNIQUE constraint resolved to column indices. Cols
// references positions in the parent storeSchema.cols slice.
type UniqueKey struct {
	Cols []int
}

// storeSchema describes a table's column layout for row encoding.
type storeSchema struct {
	cols     []string
	pk       string
	nullable []bool      // parallel to cols; false means NOT NULL
	defaults []PS.Expr   // parallel to cols; nil means no DEFAULT
	unique   []UniqueKey // each entry is1+ columns
	checks   []PS.Expr   // parallel to CHECK constraints
	// colTypes parallel to cols; stores the LX.T_* int token
	// that names the SQL column type. nil means types were not
	// registered (the legacy in-memory mode).
	colTypes []int
}

var (
	storeMu      sync.Mutex
	tableIDSeq   uint64
	tableIDs     = map[string]uint64{}
	storeSchemas = map[uint64]*storeSchema{}

	// currentCatalog is the persistent system catalog wired in
	// by SYS at Open time. nil means the EX layer is in
	// in-memory mode (legacy behavior, used by unit tests that
	// do not have a backing directory).
	currentCatalog *ls.Catalog
)

// nextTableID allocates a new table ID. The id is stable for the lifetime
// of the process; restarting the process reassigns IDs and old data is
// unreachable (consistent with the existing in-memory catalog behavior).
//
// When a persistent catalog is wired in (iter-12), the table ID is
// sourced from the catalog's NextID counter so it survives Close/Open.
func nextTableID() uint64 {
	if currentCatalog != nil {
		id, err := currentCatalog.NextID()
		if err == nil {
			return id
		}
		// Fall through to in-memory counter on error so the
		// process keeps serving (catalog errors are surfaced
		// separately on the operation that triggered them).
	}
	tableIDSeq++
	return tableIDSeq
}

func schemaFor(name string) (*storeSchema, bool) {
	storeMu.Lock()
	defer storeMu.Unlock()
	id, ok := tableIDs[name]
	if !ok {
		return nil, false
	}
	ss, ok := storeSchemas[id]
	return ss, ok
}

func tableIDFor(name string) (uint64, bool) {
	storeMu.Lock()
	defer storeMu.Unlock()
	id, ok := tableIDs[name]
	return id, ok
}

// SetCatalog binds a persistent system catalog into the EX
// layer. Subsequent CREATE TABLE / DROP TABLE calls will go
// through the catalog for persistence. Pass nil to revert to
// in-memory mode (legacy behavior).
func SetCatalog(c *ls.Catalog) {
	storeMu.Lock()
	defer storeMu.Unlock()
	currentCatalog = c
}

// Catalog returns the currently bound catalog, or nil if the EX
// layer is in in-memory mode.
func Catalog() *ls.Catalog {
	storeMu.Lock()
	defer storeMu.Unlock()
	return currentCatalog
}

// registerStoreSchema assigns a table ID to a name and stores its schema.
// Safe to call multiple times for the same name (idempotent). All columns
// default to nullable=true with no DEFAULT clause. Use
// registerStoreSchemaWithConstraints to set NOT NULL and DEFAULT.
func registerStoreSchema(name string, cols []string, pk string) uint64 {
	nullable := make([]bool, len(cols))
	for i := range nullable {
		nullable[i] = true
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	if id, ok := tableIDs[name]; ok {
		if ss, ok := storeSchemas[id]; ok {
			cp := make([]string, len(cols))
			copy(cp, cols)
			ss.cols = cp
			ss.pk = pk
			ss.nullable = nullable
			ss.defaults = nil
			return id
		}
	}
	id := nextTableID()
	tableIDs[name] = id
	storeSchemas[id] = &storeSchema{cols: append([]string(nil), cols...), pk: pk, nullable: nullable}
	return id
}

// registerStoreSchemaWithConstraints stores schema with NOT NULL and DEFAULT
// info carried in the parallel slices. Safe to call multiple times for the
// same name (idempotent). cols, nullable, and defaults must be the same
// length. No UNIQUE constraints are recorded; use
// registerStoreSchemaFull for that.
func registerStoreSchemaWithConstraints(name string, cols []string, nullable []bool, defaults []PS.Expr, pk string) uint64 {
	storeMu.Lock()
	defer storeMu.Unlock()
	cpCols := append([]string(nil), cols...)
	cpNullable := append([]bool(nil), nullable...)
	var cpDefaults []PS.Expr
	if defaults != nil {
		cpDefaults = append([]PS.Expr(nil), defaults...)
	}
	if id, ok := tableIDs[name]; ok {
		if ss, ok := storeSchemas[id]; ok {
			ss.cols = cpCols
			ss.pk = pk
			ss.nullable = cpNullable
			ss.defaults = cpDefaults
			ss.unique = nil
			return id
		}
	}
	id := nextTableID()
	tableIDs[name] = id
	storeSchemas[id] = &storeSchema{cols: cpCols, pk: pk, nullable: cpNullable, defaults: cpDefaults}
	return id
}

// registerStoreSchemaFull stores the full constraint set including
// UNIQUE. unique may be nil. Safe to call multiple times for the
// same name (idempotent).
func registerStoreSchemaFull(name string, cols []string, nullable []bool, defaults []PS.Expr, unique []UniqueKey, pk string) uint64 {
	storeMu.Lock()
	defer storeMu.Unlock()
	cpCols := append([]string(nil), cols...)
	cpNullable := append([]bool(nil), nullable...)
	var cpDefaults []PS.Expr
	if defaults != nil {
		cpDefaults = append([]PS.Expr(nil), defaults...)
	}
	var cpUnique []UniqueKey
	if unique != nil {
		cpUnique = make([]UniqueKey, len(unique))
		for i, u := range unique {
			cpUnique[i] = UniqueKey{Cols: append([]int(nil), u.Cols...)}
		}
	}
	if id, ok := tableIDs[name]; ok {
		if ss, ok := storeSchemas[id]; ok {
			ss.cols = cpCols
			ss.pk = pk
			ss.nullable = cpNullable
			ss.defaults = cpDefaults
			ss.unique = cpUnique
			return id
		}
	}
	id := nextTableIDLocked()
	tableIDs[name] = id
	storeSchemas[id] = &storeSchema{cols: cpCols, pk: pk, nullable: cpNullable, defaults: cpDefaults, unique: cpUnique}
	return id
}

// nextTableIDLocked is the locked variant of nextTableID. The
// caller MUST already hold storeMu.
func nextTableIDLocked() uint64 {
	if currentCatalog != nil {
		id, err := currentCatalog.NextID()
		if err == nil {
			return id
		}
	}
	tableIDSeq++
	return tableIDSeq
}

// RegisterFromCatalog rehydrates the in-memory storeSchemas /
// tableIDs maps from a catalog entry. Used by SYS.Open to
// repopulate runtime state on every restart. Does NOT call
// NextID — the entry already carries its tableID. Subsequent
// unregistration of this table uses the same tableID, which
// must match what other code paths expect.
func RegisterFromCatalog(entry *ls.CatalogEntry) error {
	if entry == nil {
		return errors.New("ex: nil catalog entry")
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	if _, exists := tableIDs[entry.Name]; exists {
		// Already registered (e.g. test setup called RegisterTable
		// manually before SYS bound the catalog). Leave the
		// existing entry alone.
		return nil
	}
	cols := make([]string, len(entry.Columns))
	nullable := make([]bool, len(entry.Columns))
	colTypes := make([]int, len(entry.Columns))
	colIndex := make(map[string]int, len(entry.Columns))
	for i, c := range entry.Columns {
		cols[i] = c.Name
		nullable[i] = c.Nullable
		colTypes[i] = c.Type
		colIndex[c.Name] = i
	}
	// PRIMARY KEY implies NOT NULL.
	if entry.PrimaryKey != "" {
		if idx, ok := colIndex[entry.PrimaryKey]; ok {
			nullable[idx] = false
		}
	}
	unique := make([]UniqueKey, len(entry.Unique))
	for i, u := range entry.Unique {
		unique[i] = UniqueKey{Cols: append([]int(nil), u.Cols...)}
	}
	// Defaults are not persisted across restart in v0.9.0 — the
	// parser AST cannot be safely serialized. Callers that need
	// the defaults back must re-issue the CREATE TABLE statement.
	var defaults []PS.Expr
	if id, ok := tableIDs[entry.Name]; ok && id == entry.TableID {
		// Collision: another table already has this name but
		// with a different ID. Surface the inconsistency.
		_ = id
	}
	tableIDs[entry.Name] = entry.TableID
	storeSchemas[entry.TableID] = &storeSchema{
		cols:     cols,
		pk:       entry.PrimaryKey,
		nullable: nullable,
		defaults: defaults,
		unique:   unique,
		colTypes: colTypes,
	}
	// Also publish to the in-memory `tables` / `schemas` map that
	// the executor scans.
	tablesMu.Lock()
	if _, exists := tables[entry.Name]; !exists {
		tables[entry.Name] = []Row{}
	}
	schemas[entry.Name] = cols
	tablesMu.Unlock()
	// Track the in-memory counter so subsequent NextID calls
	// (in the absence of a catalog) do not reuse this ID.
	if entry.TableID >= tableIDSeq {
		tableIDSeq = entry.TableID
	}
	return nil
}

// tablePrefix returns the storage key prefix for a table, or nil if the
// table is not registered.
func tablePrefix(name string) []byte {
	id, ok := tableIDFor(name)
	if !ok {
		return nil
	}
	return encodeTablePrefix(id)
}

func encodeTablePrefix(id uint64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, id)
	return append(out, ':')
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

// encodeRow serializes a row's values in schema column order. nil values
// become rvNull. The output is a self-describing binary blob:
//
//	[col_count:varint] for each col: [type:1][value_bytes...]
func encodeRow(schema *storeSchema, row Row) ([]byte, error) {
	if len(row.Data) != len(schema.cols) {
		return nil, fmt.Errorf("ex: row has %d values, schema has %d", len(row.Data), len(schema.cols))
	}
	var buf []byte
	buf = binary.AppendUvarint(buf, uint64(len(schema.cols)))
	for i, v := range row.Data {
		if v == nil {
			buf = append(buf, rvNull)
			continue
		}
		switch x := v.(type) {
		case int64:
			buf = append(buf, rvInt)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], uint64(x))
			buf = append(buf, b[:]...)
		case float64:
			buf = append(buf, rvFloat)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], math.Float64bits(x))
			buf = append(buf, b[:]...)
		case string:
			buf = append(buf, rvString)
			buf = binary.AppendUvarint(buf, uint64(len(x)))
			buf = append(buf, x...)
		case bool:
			buf = append(buf, rvBool)
			if x {
				buf = append(buf, 1)
			} else {
				buf = append(buf, 0)
			}
		case []byte:
			buf = append(buf, rvBytes)
			buf = binary.AppendUvarint(buf, uint64(len(x)))
			buf = append(buf, x...)
		default:
			return nil, fmt.Errorf("ex: unsupported value type %T at column %d", v, i)
		}
	}
	return buf, nil
}

// decodeRow is the inverse of encodeRow.
func decodeRow(data []byte, schema *storeSchema) (Row, error) {
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
	if int(n) != len(schema.cols) {
		return Row{}, fmt.Errorf("ex: row has %d cols, schema %d", n, len(schema.cols))
	}
	row := Row{
		Cols: append([]string(nil), schema.cols...),
		Data: make([]interface{}, len(schema.cols)),
	}
	for i := 0; i < int(n); i++ {
		if off >= len(data) {
			return Row{}, errors.New("ex: truncated row")
		}
		tag := data[off]
		off++
		switch tag {
		case rvNull:
			row.Data[i] = nil
		case rvInt:
			if off+8 > len(data) {
				return Row{}, errors.New("ex: truncated int")
			}
			row.Data[i] = int64(binary.BigEndian.Uint64(data[off : off+8]))
			off += 8
		case rvFloat:
			if off+8 > len(data) {
				return Row{}, errors.New("ex: truncated float")
			}
			row.Data[i] = math.Float64frombits(binary.BigEndian.Uint64(data[off : off+8]))
			off += 8
		case rvBool:
			if off+1 > len(data) {
				return Row{}, errors.New("ex: truncated bool")
			}
			row.Data[i] = data[off] != 0
			off++
		case rvString:
			l, err := readVarint()
			if err != nil {
				return Row{}, err
			}
			if off+int(l) > len(data) {
				return Row{}, errors.New("ex: truncated string")
			}
			row.Data[i] = string(data[off : off+int(l)])
			off += int(l)
		case rvBytes:
			l, err := readVarint()
			if err != nil {
				return Row{}, err
			}
			if off+int(l) > len(data) {
				return Row{}, errors.New("ex: truncated bytes")
			}
			row.Data[i] = append([]byte(nil), data[off:off+int(l)]...)
			off += int(l)
		default:
			return Row{}, fmt.Errorf("ex: unknown row tag %d", tag)
		}
	}
	return row, nil
}

// rowKey builds a storage key for a row: <tablePrefix><pk-bytes>.
func rowKey(prefix []byte, pkValue interface{}) []byte {
	out := make([]byte, 0, len(prefix)+16)
	out = append(out, prefix...)
	switch v := pkValue.(type) {
	case int64:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(v))
		out = append(out, b[:]...)
	case string:
		out = append(out, v...)
	case []byte:
		out = append(out, v...)
	case bool:
		if v {
			out = append(out, 1)
		} else {
			out = append(out, 0)
		}
	case float64:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
		out = append(out, b[:]...)
	default:
		out = append(out, fmt.Sprintf("%v", v)...)
	}
	return out
}

// extractPK returns the value of the primary-key column from a row.
func extractPK(schema *storeSchema, row Row) (interface{}, error) {
	if schema.pk == "" {
		return nil, errors.New("ex: table has no primary key")
	}
	for i, c := range schema.cols {
		if c == schema.pk {
			return row.Data[i], nil
		}
	}
	return nil, fmt.Errorf("ex: pk column %q not in schema", schema.pk)
}
