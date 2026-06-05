// Package TB: catalog.go is the persistent system catalog.
//
// The catalog maps (tableID, name) to a TableSchema and persists
// each entry through a byte Store. The on-disk layout is two
// disjoint key ranges in the same store:
//
//	__catalog__:<tableID>      → MarshalTableSchema(schema)
//	__catalog_name__:<name>   → uint64LE(tableID)  (secondary index)
//
// Both prefixes are reserved for catalog use. The engine's other
// subsystems (row data, PK index, secondary indexes) must not
// produce keys with these prefixes.
//
// Load rebuilds the in-memory cache from the store on Open. Writes
// go through both ranges atomically from the caller's perspective:
// CreateTable writes the schema first, then the name index; if the
// first write succeeds and the second fails, a subsequent Load will
// see the schema entry but no name entry, and will skip the schema
// (the entry is considered orphaned and ignored). The reverse
// failure mode is harmless: a name entry without a schema is also
// skipped on Load.
//
// DropTable writes tombstones for both keys (via Store.Delete). The
// merged iterator in the LS engine skips tombstones, so subsequent
// reads see the entry as gone. The in-memory cache is updated
// synchronously.
package tb

import (
	"encoding/binary"
	"errors"
	"sort"
	"sync"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

// Reserved key prefixes. The catalog owns these exclusively; any
// other subsystem producing a key with one of these prefixes is a
// bug.
const (
	catalogPrefix  = "__catalog__:"
	catalogNameKey = "__catalog_name__:"
	// catalogNextIDKey stores the high-water mark for the next
	// tableID to assign. The value is uint64LE. It is written
	// before the corresponding schema and name entries so a crash
	// between writes cannot cause nextID to regress.
	catalogNextIDKey = "__catalog_meta__:nextID"
)

var (
	// ErrTableNotFound is returned by Get and GetByName when the
	// requested table is not in the catalog (either it never
	// existed, or it was dropped).
	ErrTableNotFound = errors.New("tb: table not found")

	// ErrTableExists is returned by CreateTable when the name is
	// already in the catalog.
	ErrTableExists = errors.New("tb: table already exists")
)

// Catalog is the system catalog. It is goroutine-safe. The zero
// value is not usable; construct via New.
type Catalog struct {
	store  Store
	mu     sync.RWMutex
	byID   map[uint64]*sc.TableSchema
	byName map[string]uint64
	nextID uint64
}

// New returns a Catalog backed by store. The in-memory cache is
// empty; call Load to populate it from persistent state.
func New(store Store) *Catalog {
	return &Catalog{
		store:  store,
		byID:   make(map[uint64]*sc.TableSchema),
		byName: make(map[string]uint64),
		nextID: 1,
	}
}

// Load scans the store's two reserved prefixes and rebuilds the
// in-memory cache. It is idempotent: a second call on a populated
// catalog re-reads the store and replaces the cache. The store
// may be empty (first run after Open).
//
// Orphaned entries — a schema without a name index, or a name
// index without a schema — are silently dropped. They are usually
// the residue of a partially-applied CreateTable or DropTable and
// do not represent a real table.
func (c *Catalog) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.byID = make(map[uint64]*sc.TableSchema)
	c.byName = make(map[string]uint64)
	c.nextID = 1

	// Read the persisted nextID high-water mark. If absent (first
	// run on a fresh store), default to 1 and let the live-ID
	// scan below raise it if needed.
	if val, err := c.store.Get([]byte(catalogNextIDKey)); err == nil && len(val) == 8 {
		c.nextID = binary.LittleEndian.Uint64(val)
	}

	// Pass 1: scan __catalog__: and unmarshal each schema.
	candidateByID := make(map[uint64]*sc.TableSchema)
	maxID := uint64(0)
	it := c.store.NewIterator([]byte(catalogPrefix))
	for it.Next() {
		key := it.Key()
		idBytes := key[len(catalogPrefix):]
		if len(idBytes) != 8 {
			continue
		}
		id := binary.LittleEndian.Uint64(idBytes)
		schema, err := UnmarshalTableSchema(it.Value())
		if err != nil {
			continue
		}
		schema.TableID = id
		candidateByID[id] = schema
		if id > maxID {
			maxID = id
		}
	}
	it.Close()

	// Pass 2: scan __catalog_name__: and build the byName map.
	nameIt := c.store.NewIterator([]byte(catalogNameKey))
	for nameIt.Next() {
		key := nameIt.Key()
		name := key[len(catalogNameKey):]
		val := nameIt.Value()
		if len(val) != 8 {
			continue
		}
		id := binary.LittleEndian.Uint64(val)
		schema, ok := candidateByID[id]
		if !ok {
			continue
		}
		c.byID[id] = schema
		c.byName[string(name)] = id
	}
	nameIt.Close()

	// Defensive: if we observed live IDs higher than the stored
	// nextID, raise nextID to stay above them.
	if maxID+1 > c.nextID {
		c.nextID = maxID + 1
	}

	return nil
}

// CreateTable allocates a fresh tableID, persists the schema, and
// indexes it by name. The returned schema has TableID set; the
// caller can use it for subsequent writes. ErrTableExists is
// returned if the name is already in the catalog.
func (c *Catalog) CreateTable(name string, columns []sc.ColumnDef, primaryKey []int) (*sc.TableSchema, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.byName[name]; exists {
		return nil, ErrTableExists
	}

	id := c.nextID
	c.nextID++

	cols := make([]sc.ColumnDef, len(columns))
	copy(cols, columns)
	pk := make([]int, len(primaryKey))
	copy(pk, primaryKey)

	schema := &sc.TableSchema{
		TableID:    id,
		Name:       name,
		Columns:    cols,
		PrimaryKey: pk,
	}

	schemaBytes, err := MarshalTableSchema(schema)
	if err != nil {
		c.nextID-- // restore the counter on error
		return nil, err
	}

	// Write order: nextID meta → schema → name. Persisting nextID
	// first means a crash between writes cannot cause nextID to
	// regress on reload. (See TestCatalog_Reopen_IDsAreFresh for
	// the failure mode this prevents.)
	if err := c.store.Insert([]byte(catalogNextIDKey), encodeID(c.nextID)); err != nil {
		c.nextID--
		return nil, err
	}
	if err := c.store.Insert(catalogKey(id), schemaBytes); err != nil {
		c.nextID--
		return nil, err
	}
	if err := c.store.Insert(catalogNameKeyFor(name), encodeID(id)); err != nil {
		c.nextID--
		return nil, err
	}

	c.byID[id] = schema
	c.byName[name] = id
	return schema, nil
}

// DropTable removes the table with the given id. The id may refer
// to a freshly created table or a table loaded from a previous
// session. ErrTableNotFound is returned if the id is unknown.
//
// DropTable writes tombstones through Store.Delete; subsequent
// Get/GetByName return ErrTableNotFound. The id is not reused —
// a future CreateTable with the same name gets a new id (R15).
func (c *Catalog) DropTable(id uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	schema, ok := c.byID[id]
	if !ok {
		return ErrTableNotFound
	}

	if err := c.store.Delete(catalogKey(id)); err != nil {
		return err
	}
	if err := c.store.Delete(catalogNameKeyFor(schema.Name)); err != nil {
		return err
	}

	delete(c.byID, id)
	delete(c.byName, schema.Name)
	return nil
}

// GetTable returns a defensive copy of the schema with the given
// id. The copy is safe for the caller to mutate.
func (c *Catalog) GetTable(id uint64) (*sc.TableSchema, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	schema, ok := c.byID[id]
	if !ok {
		return nil, ErrTableNotFound
	}
	return cloneSchema(schema), nil
}

// GetTableByName is the name-indexed counterpart of GetTable.
func (c *Catalog) GetTableByName(name string) (*sc.TableSchema, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.byName[name]
	if !ok {
		return nil, ErrTableNotFound
	}
	schema, ok := c.byID[id]
	if !ok {
		return nil, ErrTableNotFound
	}
	return cloneSchema(schema), nil
}

// ListTables returns all live schemas in id order. The returned
// slice is freshly allocated; the caller owns it. Each element is
// a defensive copy.
func (c *Catalog) ListTables() []*sc.TableSchema {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]uint64, 0, len(c.byID))
	for id := range c.byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]*sc.TableSchema, len(ids))
	for i, id := range ids {
		out[i] = cloneSchema(c.byID[id])
	}
	return out
}

// Flush is a no-op in v1: writes go through Store.Insert which is
// already WAL-durable. The method exists as a public seam for
// future checkpoint-based catalog sync (e.g. when the catalog
// grows large enough that the in-memory cache can be rebuilt from
// a checkpoint rather than a full scan).
func (c *Catalog) Flush() error {
	return nil
}

// Len returns the number of live tables. Useful for tests.
func (c *Catalog) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byID)
}

func catalogKey(id uint64) []byte {
	out := make([]byte, 0, len(catalogPrefix)+8)
	out = append(out, catalogPrefix...)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], id)
	return append(out, buf[:]...)
}

func catalogNameKeyFor(name string) []byte {
	out := make([]byte, 0, len(catalogNameKey)+len(name))
	out = append(out, catalogNameKey...)
	return append(out, name...)
}

func encodeID(id uint64) []byte {
	out := make([]byte, 8)
	binary.LittleEndian.PutUint64(out, id)
	return out
}

func cloneSchema(s *sc.TableSchema) *sc.TableSchema {
	out := &sc.TableSchema{
		TableID: s.TableID,
		Name:    s.Name,
	}
	out.Columns = make([]sc.ColumnDef, len(s.Columns))
	copy(out.Columns, s.Columns)
	for i, col := range out.Columns {
		if col.Default != nil {
			out.Columns[i].Default = append([]byte(nil), col.Default...)
		}
	}
	out.PrimaryKey = make([]int, len(s.PrimaryKey))
	copy(out.PrimaryKey, s.PrimaryKey)
	return out
}
