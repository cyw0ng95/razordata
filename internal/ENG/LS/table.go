package ls

import (
	"encoding/binary"
	"sync"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

var (
	ErrTableNotFound  = sc.ErrTableNotFound
	ErrTableExists    = sc.ErrTableExists
	ErrInvalidTableID = sc.ErrInvalidTableID
)

type tableRegistry struct {
	mu     sync.RWMutex
	tables map[uint64]*sc.TableSchema
	nextID uint64
}

func newTableRegistry() *tableRegistry {
	return &tableRegistry{
		tables: make(map[uint64]*sc.TableSchema),
		nextID: 1,
	}
}

func (tr *tableRegistry) Create(schema *sc.TableSchema) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	for _, existing := range tr.tables {
		if existing.Name == schema.Name {
			return sc.ErrTableExists
		}
	}

	schema.TableID = tr.nextID
	tr.nextID++
	tr.tables[schema.TableID] = schema

	return nil
}

func (tr *tableRegistry) Get(tableID uint64) (*sc.TableSchema, error) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	schema, ok := tr.tables[tableID]
	if !ok {
		return nil, sc.ErrTableNotFound
	}
	return schema, nil
}

func (tr *tableRegistry) Drop(tableID uint64) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if _, ok := tr.tables[tableID]; !ok {
		return sc.ErrTableNotFound
	}

	delete(tr.tables, tableID)
	return nil
}

func (tr *tableRegistry) List() []*sc.TableSchema {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	result := make([]*sc.TableSchema, 0, len(tr.tables))
	for _, schema := range tr.tables {
		result = append(result, schema)
	}
	return result
}

func (tr *tableRegistry) Len() int {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return len(tr.tables)
}

type catalog struct {
	registry *tableRegistry
	index    *primaryIndex
}

func newCatalog() *catalog {
	return &catalog{
		registry: newTableRegistry(),
		index:    newPrimaryIndex(0, 0),
	}
}

func (c *catalog) CreateTable(name string, columns []sc.ColumnDef, primaryKey []int) (*sc.TableSchema, error) {
	schema := &sc.TableSchema{
		Name:       name,
		Columns:    columns,
		PrimaryKey: primaryKey,
	}

	if err := c.registry.Create(schema); err != nil {
		return nil, err
	}

	c.index.Insert([]byte(name), encodeTableID(schema.TableID))

	return schema, nil
}

func (c *catalog) DropTable(tableID uint64) error {
	schema, err := c.registry.Get(tableID)
	if err != nil {
		return err
	}

	if err := c.registry.Drop(tableID); err != nil {
		return err
	}

	c.index.Delete([]byte(schema.Name))

	return nil
}

func (c *catalog) GetTable(tableID uint64) (*sc.TableSchema, error) {
	return c.registry.Get(tableID)
}

func (c *catalog) GetTableByName(name string) ([]byte, bool) {
	return c.index.Find([]byte(name))
}

func (c *catalog) ListTables() []*sc.TableSchema {
	return c.registry.List()
}

func encodeTableID(id uint64) []byte {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, id)
	return buf
}

func decodeTableID(data []byte) uint64 {
	return binary.LittleEndian.Uint64(data)
}
