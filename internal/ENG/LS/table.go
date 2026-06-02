package ls

import (
	"encoding/binary"
	"errors"
	"sync"
)

var (
	ErrTableNotFound    = errors.New("table not found")
	ErrTableExists      = errors.New("table already exists")
	ErrInvalidTableID   = errors.New("invalid table ID")
)

type TableSchema struct {
	TableID    uint64
	Name       string
	Columns    []ColumnDef
	PrimaryKey []int
}

type ColumnDef struct {
	Name       string
	Type       ColumnType
	Nullable   bool
	Default    []byte
	PrimaryKey bool
}

type ColumnType uint8

const (
	CTInt       ColumnType = 0
	CTBigInt    ColumnType = 1
	CTVarchar   ColumnType = 2
	CTFloat     ColumnType = 3
	CTBool      ColumnType = 4
	CTText      ColumnType = 5
	CTBlob      ColumnType = 6
	CTTimestamp ColumnType = 7
)

type tableRegistry struct {
	mu      sync.RWMutex
	tables  map[uint64]*TableSchema
	nextID  uint64
}

func newTableRegistry() *tableRegistry {
	return &tableRegistry{
		tables: make(map[uint64]*TableSchema),
		nextID: 1,
	}
}

func (tr *tableRegistry) Create(schema *TableSchema) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	for _, existing := range tr.tables {
		if existing.Name == schema.Name {
			return ErrTableExists
		}
	}

	schema.TableID = tr.nextID
	tr.nextID++
	tr.tables[schema.TableID] = schema

	return nil
}

func (tr *tableRegistry) Get(tableID uint64) (*TableSchema, error) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	schema, ok := tr.tables[tableID]
	if !ok {
		return nil, ErrTableNotFound
	}
	return schema, nil
}

func (tr *tableRegistry) Drop(tableID uint64) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if _, ok := tr.tables[tableID]; !ok {
		return ErrTableNotFound
	}

	delete(tr.tables, tableID)
	return nil
}

func (tr *tableRegistry) List() []*TableSchema {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	result := make([]*TableSchema, 0, len(tr.tables))
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
	mu       sync.RWMutex
}

func newCatalog() *catalog {
	return &catalog{
		registry: newTableRegistry(),
		index:    newPrimaryIndex(0, 0),
	}
}

func (c *catalog) CreateTable(name string, columns []ColumnDef, primaryKey []int) (*TableSchema, error) {
	schema := &TableSchema{
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

func (c *catalog) GetTable(tableID uint64) (*TableSchema, error) {
	return c.registry.Get(tableID)
}

func (c *catalog) GetTableByName(name string) ([]byte, bool) {
	return c.index.Find([]byte(name))
}

func (c *catalog) ListTables() []*TableSchema {
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