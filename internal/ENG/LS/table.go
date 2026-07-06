package ls

import (
	"encoding/binary"
	"log/slog"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
	tb "github.com/cyw0ng95/razordata/internal/ENG/TB"
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
)

var (
	ErrTableNotFound  = sc.ErrTableNotFound
	ErrTableExists    = sc.ErrTableExists
	ErrInvalidTableID = sc.ErrInvalidTableID
)

// tableRegistry is a thin wrapper around tb.Registry that
// preserves the existing ENG/LS API surface. REQ000048: the
// actual table registry logic now lives in ENG/TB.
// The wrapper is kept for backward compatibility with existing
// callers (e.g., the catalog layer in LS/catalog.go).
type tableRegistry struct {
	inner *tb.Registry
}

func newTableRegistry() *tableRegistry {
	return &tableRegistry{inner: tb.NewRegistry()}
}

func (tr *tableRegistry) Create(schema *sc.TableSchema) error {
	return tr.inner.Create(schema)
}

func (tr *tableRegistry) Get(tableID uint64) (*sc.TableSchema, error) {
	return tr.inner.Get(tableID)
}

func (tr *tableRegistry) Drop(tableID uint64) error {
	return tr.inner.Drop(tableID)
}

func (tr *tableRegistry) List() []*sc.TableSchema {
	return tr.inner.List()
}

func (tr *tableRegistry) Len() int {
	return tr.inner.Len()
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

	EC.WARN_ON(len(schema.Name) == 0, "DropTable: table %d has empty name — index delete may be incorrect", tableID)

	if !c.index.Delete([]byte(schema.Name)) {
		slog.Warn("index delete failed for dropped table", "table", schema.Name)
	}

	return nil
}

func (c *catalog) Table(tableID uint64) (*sc.TableSchema, error) {
	return c.registry.Get(tableID)
}

func (c *catalog) TableByName(name string) ([]byte, bool) {
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
