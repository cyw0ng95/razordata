// Package tb implements the Table cluster of the ENG subsystem.
//
// TB owns table metadata operations:
//   - CREATE TABLE: allocate a tableID, build a TableSchema, persist to catalog
//   - DROP TABLE: remove the schema from the in-memory registry, persist
//   - GetSchema: look up a table by ID
//   - ListTables: enumerate all tables
//
// The on-disk catalog (catalog.dat) is also owned by this package
// (see catalog.go). The catalog provides crash-safe persistence:
// every Put/Delete writes to a temp file and atomically renames.
//
// REQ000048: the table registry is a persistent, on-disk
// structure. Before this REQ, the registry lived in ENG/LS
// (table.go, catalog.go) and was only in-memory. Now it lives
// in ENG/TB and is backed by the catalog.dat file.
package tb

import (
	"sync"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

// Sentinel errors.
var (
	ErrTableNotFound  = sc.ErrTableNotFound
	ErrTableExists    = sc.ErrTableExists
	ErrInvalidTableID = sc.ErrInvalidTableID
)

// Registry is the in-memory table registry. It maps tableID to
// TableSchema and enforces unique table names.
//
// A Registry is safe for concurrent use. It is typically owned by
// an Engine and accessed via the engine's table operations.
//
// The Registry is the write-through cache for the persistent
// Catalog: every mutation is also persisted to disk via the
// Catalog.
type Registry struct {
	mu     sync.RWMutex
	tables map[uint64]*sc.TableSchema
	nextID uint64
}

// NewRegistry creates a fresh, empty in-memory registry.
// The next tableID starts at 1.
func NewRegistry() *Registry {
	return &Registry{
		tables: make(map[uint64]*sc.TableSchema),
		nextID: 1,
	}
}

// Create inserts a new table schema. The schema's TableID is
// assigned by the registry (1, 2, 3, ...). Returns
// sc.ErrTableExists if a table with the same name already exists.
func (r *Registry) Create(schema *sc.TableSchema) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, existing := range r.tables {
		if existing.Name == schema.Name {
			return sc.ErrTableExists
		}
	}

	schema.TableID = r.nextID
	r.nextID++
	r.tables[schema.TableID] = schema

	return nil
}

// Get returns the schema for tableID or sc.ErrTableNotFound.
func (r *Registry) Get(tableID uint64) (*sc.TableSchema, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	schema, ok := r.tables[tableID]
	if !ok {
		return nil, sc.ErrTableNotFound
	}
	return schema, nil
}

// Drop removes the table with the given ID. Returns
// sc.ErrTableNotFound if no such table exists.
func (r *Registry) Drop(tableID uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.tables[tableID]; !ok {
		return sc.ErrTableNotFound
	}

	delete(r.tables, tableID)
	return nil
}

// List returns a snapshot of all table schemas. The order is
// unspecified.
func (r *Registry) List() []*sc.TableSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*sc.TableSchema, 0, len(r.tables))
	for _, schema := range r.tables {
		result = append(result, schema)
	}
	return result
}

// Len returns the number of tables in the registry.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tables)
}

// NextID returns the next table ID that will be assigned. This is
// useful for pre-allocating IDs or for tests that need to know
// the next ID without creating a table.
func (r *Registry) NextID() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nextID
}

// SetNextID overrides the next-ID counter. Used by the catalog
// bootstrap to restore the counter from the on-disk file.
func (r *Registry) SetNextID(nextID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID = nextID
}
