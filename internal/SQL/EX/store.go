package EX

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

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
	// ManualCompact triggers a full LSM compaction cycle. REQ000257.
	// Returns ErrCompactionInProgress if already compacting.
	ManualCompact() error
}

// StatsCatalog provides access to column statistics for
// histogram-based selectivity estimation. REQ000085.
type StatsCatalog interface {
	ColumnStatsByName(tableName, colName string) *ls.ColumnStats
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
	unique   []UniqueKey // each entry is 1+ columns
	checks   []PS.Expr   // parallel to CHECK constraints
	colTypes []int
	// REQ000568: DECIMAL(P,S) precision and scale per column.
	// Only meaningful when colTypes[i] is T_DECIMAL or T_NUMERIC.
	precision []int
	scale     []int
	// REQ000248/249: parallel to cols; non-nil means column is a
	// STORED generated column. The expression is evaluated on
	// INSERT/UPDATE and the result is stored as the cell value.
	// VIRTUAL generated columns are stored as nil here (deferred).
	generated   []PS.Expr
	foreignKeys []ForeignKeyConstraint // REQ000126: FK constraints
	// REQ000367: hiddenPK is set when the table was created
	// without a PRIMARY KEY declaration but is registered for
	// storage. The engine synthesizes an int64 rowid per insert
	// and uses it as the LSM key suffix; the user-visible schema
	// is unchanged (no rowid column appears in SELECT *).
	hiddenPK  bool
	nextRowID int64
}

// ForeignKeyConstraint describes a single FK constraint (REQ000126).
type ForeignKeyConstraint struct {
	Columns    []string // local column names
	RefTable   string   // referenced table
	RefColumns []string // referenced columns
	OnDelete   string   // CASCADE, RESTRICT, SET NULL, SET DEFAULT, NO ACTION
	OnUpdate   string   // same set
}

var (
	storeMu      sync.Mutex
	tableIDSeq   uint64
	tableIDs     = map[string]uint64{}
	storeSchemas = map[uint64]*storeSchema{}
	inMemSchemas = map[string]*storeSchema{} // in-memory fallback, keyed by table name

	// currentCatalog is the persistent system catalog wired in
	// by SYS at Open time. nil means the EX layer is in
	// in-memory mode (legacy behavior, used by unit tests that
	// do not have a backing directory).
	currentCatalog atomic.Pointer[ls.Catalog]

	// registeredIndexes is the EX-layer's view of secondary
	// indexes declared via CREATE INDEX. Keyed by table name.
	// iter-22 secondary indexes MVP.
	registeredIndexes = map[string][]RegisteredIndex{}

	// viewRegistry stores view definitions (REQ000240). Keyed by
	// view name. Values are the parsed SELECT statements.
	viewRegistry = map[string]*PS.Select{}

	// viewMu protects viewRegistry.
	viewMu sync.RWMutex

	// matViewRegistry stores materialized view definitions (REQ000316).
	// Keyed by view name. Values are the parsed SELECT statements.
	matViewRegistry = map[string]*PS.Select{}

	// matViewMu protects matViewRegistry.
	matViewMu sync.RWMutex
)

// RegisteredIndex is one entry in the EX-layer's index registry.
type RegisteredIndex struct {
	Name    string
	Columns []string
	Unique  bool
}

// schemaFor looks up a storeSchema by table name, first in the store-backed
// registry (tableIDs → storeSchemas), then in the in-memory fallback.
func schemaFor(name string) (*storeSchema, bool) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if id, ok := tableIDs[name]; ok {
		if ss, ok := storeSchemas[id]; ok {
			return ss, ok
		}
	}
	if ss, ok := inMemSchemas[name]; ok {
		return ss, ok
	}
	return nil, false
}

// RegisterView stores a view definition (REQ000240).
func RegisterView(name string, sel *PS.Select) {
	viewMu.Lock()
	defer viewMu.Unlock()
	viewRegistry[name] = sel
}

// LookupView returns the SELECT statement for a view, or nil.
func LookupView(name string) *PS.Select {
	viewMu.RLock()
	defer viewMu.RUnlock()
	return viewRegistry[name]
}

// UnregisterAllViews clears all views (for testing).
func UnregisterAllViews() {
	viewMu.Lock()
	defer viewMu.Unlock()
	viewRegistry = map[string]*PS.Select{}
}

// UnregisterView removes a single view by name. REQ000494.
func UnregisterView(name string) {
	viewMu.Lock()
	defer viewMu.Unlock()
	delete(viewRegistry, name)
}

// RegisterMatView stores a materialized view definition (REQ000316).
func RegisterMatView(name string, sel *PS.Select) {
	matViewMu.Lock()
	defer matViewMu.Unlock()
	matViewRegistry[name] = sel
}

// LookupMatView returns the SELECT for a materialized view, or nil.
func LookupMatView(name string) *PS.Select {
	matViewMu.RLock()
	defer matViewMu.RUnlock()
	return matViewRegistry[name]
}

// UnregisterMatView removes a single materialized view by name.
func UnregisterMatView(name string) {
	matViewMu.Lock()
	defer matViewMu.Unlock()
	delete(matViewRegistry, name)
}

// UnregisterAllMatViews clears all materialized views (for testing).
func UnregisterAllMatViews() {
	matViewMu.Lock()
	defer matViewMu.Unlock()
	matViewRegistry = map[string]*PS.Select{}
}

// RegisterIndexWithID registers a secondary index for the given
// table. Called from the CREATE INDEX executor path; for tests
// that don't go through the SQL surface, use RegisterIndex.
func RegisterIndexWithID(table string, idx RegisteredIndex) {
	storeMu.Lock()
	defer storeMu.Unlock()
	registeredIndexes[table] = append(registeredIndexes[table], idx)
}

// GetRegisteredIndexes returns a copy of the index list for a table.
func GetRegisteredIndexes(table string) []RegisteredIndex {
	storeMu.Lock()
	defer storeMu.Unlock()
	src := registeredIndexes[table]
	out := make([]RegisteredIndex, len(src))
	for i, idx := range src {
		out[i] = RegisteredIndex{
			Name:    idx.Name,
			Columns: append([]string(nil), idx.Columns...),
			Unique:  idx.Unique,
		}
	}
	return out
}

// nextTableID allocates a new table ID. The id is stable for the lifetime
// of the process; restarting the process reassigns IDs and old data is
// unreachable (consistent with the existing in-memory catalog behavior).
// When a persistent catalog is wired in (iter-12), the table ID is
// sourced from the catalog's NextID counter so it survives Close/Open.
func nextTableID() uint64 {
	cat := currentCatalog.Load()
	if cat != nil {
		id, err := cat.NextID()
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
	currentCatalog.Store(c)
}

// Catalog returns the currently bound catalog, or nil if the EX
// layer is in in-memory mode.
func Catalog() *ls.Catalog {
	return currentCatalog.Load()
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

// registerInMemorySchema registers a storeSchema in the in-memory fallback
// registry, enabling constraint enforcement for tables without a Store.
func registerInMemorySchema(name string, cols []string, pk string) {
	storeMu.Lock()
	defer storeMu.Unlock()
	nullable := make([]bool, len(cols))
	for i := range nullable {
		nullable[i] = true
	}
	inMemSchemas[name] = &storeSchema{
		cols:     append([]string(nil), cols...),
		pk:       pk,
		nullable: nullable,
	}
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
	return registerStoreSchemaWithFK(name, cols, nullable, defaults, unique, pk, nil)
}

// registerStoreSchemaWithFK stores the full constraint set including
// UNIQUE and FOREIGN KEY constraints. REQ000126.
func registerStoreSchemaWithFK(name string, cols []string, nullable []bool, defaults []PS.Expr, unique []UniqueKey, pk string, fks []ForeignKeyConstraint) uint64 {
	storeMu.Lock()
	defer storeMu.Unlock()
	return registerStoreSchemaWithFKLocked(name, cols, nullable, defaults, unique, pk, fks)
}

// registerStoreSchemaWithFKLocked is the locked variant of
// registerStoreSchemaWithFK. The caller MUST already hold storeMu.
func registerStoreSchemaWithFKLocked(name string, cols []string, nullable []bool, defaults []PS.Expr, unique []UniqueKey, pk string, fks []ForeignKeyConstraint) uint64 {
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
			if fks != nil {
				ss.foreignKeys = fks
			}
			return id
		}
	}
	id := nextTableIDLocked()
	tableIDs[name] = id
	storeSchemas[id] = &storeSchema{cols: cpCols, pk: pk, nullable: cpNullable, defaults: cpDefaults, unique: cpUnique, foreignKeys: fks}
	return id
}

// nextTableIDLocked is the locked variant of nextTableID. The
// caller MUST already hold storeMu.
func nextTableIDLocked() uint64 {
	cat := currentCatalog.Load()
	if cat != nil {
		id, err := cat.NextID()
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
		Data: make([]any, len(schema.cols)),
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
func rowKey(prefix []byte, pkValue any) []byte {
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
// For REQ000367 (hidden-PK tables, no PRIMARY KEY declared at
// CREATE TABLE time), this allocates and returns a synthetic int64
// rowid that is unique within the table.
func extractPK(schema *storeSchema, row Row) (any, error) {
	if schema.pk == "" {
		if !schema.hiddenPK {
			return nil, errors.New("ex: table has no primary key")
		}
		id := atomic.AddInt64(&schema.nextRowID, 1)
		return id, nil
	}
	for i, c := range schema.cols {
		if c == schema.pk {
			if row.Data[i] == nil {
				if !schema.hiddenPK {
					schema.hiddenPK = true
				}
				id := atomic.AddInt64(&schema.nextRowID, 1)
				return id, nil
			}
			return row.Data[i], nil
		}
	}
	return nil, fmt.Errorf("ex: pk column %q not in schema", schema.pk)
}

// extractPKForUpdate returns the primary key to use when writing
// the updated row. For regular PK tables, delegates to extractPK.
// For hidden-PK tables, reuses the original row's key suffix from
// storeKey so the update overwrites the same engine row instead of
// allocating a new synthetic rowid on every UPDATE (REQ000501).
func extractPKForUpdate(schema *storeSchema, oldRow Row, prefix []byte) (any, error) {
	if schema.pk == "" && schema.hiddenPK && len(oldRow.storeKey) > len(prefix) {
		suffix := oldRow.storeKey[len(prefix):]
		return int64(binary.BigEndian.Uint64(suffix)), nil
	}
	return extractPK(schema, oldRow)
}

// maintainIndexesOnInsert populates secondary-index entries
// for a newly-inserted row. iter-22 secondary indexes MVP.
// Returns the first error encountered, or nil on success.
func maintainIndexesOnInsert(store Store, table string, schema *storeSchema, row Row) error {
	indexes := GetRegisteredIndexes(table)
	if len(indexes) == 0 {
		return nil
	}
	tableID, ok := tableIDFor(table)
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
func maintainIndexesOnDelete(store Store, table string, schema *storeSchema, row Row) error {
	indexes := GetRegisteredIndexes(table)
	if len(indexes) == 0 {
		return nil
	}
	tableID, ok := tableIDFor(table)
	if !ok {
		return nil
	}
	for _, idx := range indexes {
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
// the original key for hidden-PK tables). iter-22 secondary indexes.
func maintainIndexesOnUpdate(store Store, table string, schema *storeSchema, oldRow, newRow Row, pk any) error {
	indexes := GetRegisteredIndexes(table)
	if len(indexes) == 0 {
		return nil
	}
	tableID, ok := tableIDFor(table)
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
	switch v := pk.(type) {
	case int64:
		return int64ToBytesBigEndian(v), nil
	case int:
		return int64ToBytesBigEndian(int64(v)), nil
	case string:
		return []byte(v), nil
	case []byte:
		return append([]byte(nil), v...), nil
	default:
		return nil, fmt.Errorf("ex: unsupported pk type %T", pk)
	}
}

// int64ToBytesBigEndian encodes an int64 as 8 bytes big-endian.
func int64ToBytesBigEndian(n int64) []byte {
	b := make([]byte, 8)
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
func indexValueFor(schema *storeSchema, row Row, cols []string) []byte {
	if len(cols) == 0 {
		return nil
	}
	out := []byte{}
	for i, c := range cols {
		var val any
		found := false
		for j, sc := range schema.cols {
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
