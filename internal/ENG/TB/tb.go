// Package tb implements the Table cluster of the ENG subsystem (REQ000048).
package tb

import (
	"sync"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

var (
	ErrTableNotFound  = sc.ErrTableNotFound
	ErrTableExists    = sc.ErrTableExists
	ErrInvalidTableID = sc.ErrInvalidTableID
)

// Registry is the in-memory table registry.
type Registry struct {
	mu     sync.RWMutex
	tables map[uint64]*sc.TableSchema
	nextID uint64
}

// NewRegistry creates a fresh, empty in-memory registry.
func NewRegistry() *Registry {
	return &Registry{
		tables: make(map[uint64]*sc.TableSchema),
		nextID: 1,
	}
}

// Create inserts a new table schema.
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

// Get returns the schema for tableID.
func (r *Registry) Get(tableID uint64) (*sc.TableSchema, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	schema, ok := r.tables[tableID]
	if !ok {
		return nil, sc.ErrTableNotFound
	}
	return schema, nil
}

// Drop removes the table with the given ID.
func (r *Registry) Drop(tableID uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.tables[tableID]; !ok {
		return sc.ErrTableNotFound
	}

	delete(r.tables, tableID)
	return nil
}

// List returns a snapshot of all table schemas.
func (r *Registry) List() []*sc.TableSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*sc.TableSchema, 0, len(r.tables))
	for _, schema := range r.tables {
		result = append(result, schema)
	}
	return result
}

func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tables)
}

// NextID returns the next table ID that will be assigned.
func (r *Registry) NextID() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nextID
}

// SetNextID overrides the next-ID counter.
func (r *Registry) SetNextID(nextID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID = nextID
}
