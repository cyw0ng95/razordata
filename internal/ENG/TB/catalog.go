// Package tb — Catalog (persistent table registry)
//
// The Catalog is the persistent, on-disk equivalent of the
// in-memory Registry. The catalog owns a single catalog.dat file
// under dir and exposes Get/Put/Delete/List for the system to
// call on CREATE TABLE / DROP TABLE.
//
// # On-disk format (catalog.dat)
//
// The file is a packed sequence of header + entries. All multi-byte
// integers are big-endian; all lengths are varint-encoded so a 256 KB
// CREATE TABLE SQL is fine without inflating the file size.
//
//	┌────────────────────────────────────────────────────────┐
//	│  magic    :4   = "RCAT"  (0x52 0x43 0x41 0x54)         │
//	│  version  :1   = SchemaVersionV1 (0x01)                │
//	│  reserved :4   = 0 (future use; readers MUST skip)     │
//	│  nextID   :8   (counter, big-endian)                   │
//	│  count    :varint    (number of entries that follow)   │
//	├────────────────────────────────────────────────────────┤
//	│  entry[i]:                                             │
//	│    tableID   :8                                        │
//	│    nameLen   :varint                                   │
//	│    name      :nameLen bytes                            │
//	│    primaryKey:varint (0 = no PK; otherwise length &    │
//	│              string)                                   │
//	│    colCount  :varint                                   │
//	│    colNameLen:varint                                   │
//	│    colName   :bytes  (repeated colCount times)         │
//	│    nullable  :colCount bytes (0x00 false, 0x01 true)   │
//	│    uniqueCount:varint                                  │
//	│    uniqueKey :{nCols:varint, nCols col-idx varints}    │
//	│              (repeated uniqueCount times)              │
//	│    sqlLen    :varint                                   │
//	│    sql       :sqlLen bytes (original CREATE TABLE)     │
//	└────────────────────────────────────────────────────────┘
//
// # Atomicity
//
// Writes go to a `.tmp` file first, then `rename(2)` to the final
// path. The rename is atomic on POSIX file systems, so a crash
// never leaves the file in a half-written state. The next Open
// either sees the old file or the new file — never a mix.
//
// # Schema versioning
//
// Future migrations (CHECK constraints, foreign keys, ...)
// bump `schemaVersionCurrent`. Reads of a higher version fail
// with `ErrUpgradeRequired`.
//
// REQ000048: the persistent catalog was previously in
// internal/ENG/LS/catalog.go. This file in internal/ENG/TB/
// provides the same on-disk format and semantics, owned by the
// Table cluster per the design spec.
package tb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

// Schema-version constants for the system catalog.
const (
	schemaVersionV1      uint8 = 1
	schemaVersionV2      uint8 = 2
	schemaVersionCurrent       = schemaVersionV2
)

var (
	catalogMagic     = [4]byte{'R', 'C', 'A', 'T'}
	catalogFileName  = "catalog.dat"
	catalogTmpSuffix = ".tmp"
	catalogHeaderSize = 17 // magic(4) + version(1) + reserved(4) + nextID(8)
)

// Catalog-specific errors.
var (
	// ErrCatalogCorrupt marks a value whose length or structure
	// does not match the wire format.
	ErrCatalogCorrupt = errors.New("catalog: data corrupt")
	// ErrUpgradeRequired marks a file written by a future binary
	// (file.Version > schemaVersionCurrent). Open refuses to
	// proceed; the user must upgrade the engine.
	ErrUpgradeRequired = errors.New("catalog: schema version newer than supported")
	// ErrCatalogNotFound is returned by GetByID / GetByName when
	// no matching entry exists.
	ErrCatalogNotFound = errors.New("catalog: table not found")
	// ErrCatalogExists is returned by Put when the supplied name
	// or ID is already in use.
	ErrCatalogExists = errors.New("catalog: table already exists")
	// ErrCatalogClosed is returned by Get/Put/Delete/List after
	// Close.
	ErrCatalogClosed = errors.New("catalog: closed")
)

// Column is the on-disk + in-memory representation of one column.
type Column struct {
	Name     string
	Type     uint8
	Nullable bool
}

// Unique is one UNIQUE constraint, resolved to column indices.
type Unique struct {
	Cols []int
}

// Index is one secondary index.
type Index struct {
	IndexID   uint64
	Name      string
	Columns   []string
	Unique    bool
	CreateSQL string
}

// Entry is the on-disk + in-memory representation of a table.
type Entry struct {
	Version    uint8
	TableID    uint64
	Name       string
	Columns    []Column
	Unique     []Unique
	Indexes    []Index
	PrimaryKey []int
	CreateSQL  string
}

// Catalog is the persistent, on-disk system catalog.
type Catalog struct {
	path   string
	mu     sync.RWMutex
	cache  map[uint64]*Entry
	byName map[string]uint64
	nextID uint64
	closed bool
}

// NewCatalog opens (or creates) a catalog rooted at dir. The
// returned Catalog is safe for concurrent use.
func NewCatalog(dir string) (*Catalog, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: dir is required", ErrCatalogCorrupt)
	}
	c := &Catalog{
		path:   filepath.Join(dir, catalogFileName),
		cache:  make(map[uint64]*Entry),
		byName: make(map[string]uint64),
	}
	if err := c.bootstrap(); err != nil {
		return nil, err
	}
	return c, nil
}

// bootstrap reads the on-disk file into the in-memory cache.
func (c *Catalog) bootstrap() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) < catalogHeaderSize {
		return fmt.Errorf("%w: file too short (%d bytes)", ErrCatalogCorrupt, len(data))
	}
	if string(data[0:4]) != string(catalogMagic[:]) {
		return fmt.Errorf("%w: bad magic %x", ErrCatalogCorrupt, data[:4])
	}
	version := data[4]
	if version > schemaVersionCurrent {
		return fmt.Errorf("%w: file version %d > current %d", ErrUpgradeRequired, version, schemaVersionCurrent)
	}
	// Skip reserved (4 bytes at offset 5)
	c.nextID = binary.BigEndian.Uint64(data[9:17])
	off := 17
	count, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return fmt.Errorf("%w: bad count", ErrCatalogCorrupt)
	}
	off += n
	for i := uint64(0); i < count; i++ {
		e := &Entry{Version: version}
		newOff, err := decodeCatalogEntry(data, off, e)
		if err != nil {
			return fmt.Errorf("%w: entry %d: %v", ErrCatalogCorrupt, i, err)
		}
		c.cache[e.TableID] = e
		c.byName[e.Name] = e.TableID
		off = newOff
	}
	return nil
}

func decodeCatalogEntry(data []byte, off int, e *Entry) (int, error) {
	if off+8 > len(data) {
		return off, fmt.Errorf("truncated at tableID")
	}
	e.TableID = binary.BigEndian.Uint64(data[off : off+8])
	off += 8
	nameLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, fmt.Errorf("bad nameLen")
	}
	off += n
	if off+int(nameLen) > len(data) {
		return off, fmt.Errorf("truncated at name")
	}
	e.Name = string(data[off : off+int(nameLen)])
	off += int(nameLen)
	pkLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, fmt.Errorf("bad primaryKeyLen")
	}
	off += n
	if off+int(pkLen) > len(data) {
		return off, fmt.Errorf("truncated at primaryKey")
	}
	e.PrimaryKey = make([]int, pkLen)
	for i := 0; i < int(pkLen); i++ {
		idx, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return off, fmt.Errorf("bad primaryKey[%d]", i)
		}
		e.PrimaryKey[i] = int(idx)
		off += n
	}
	colCount, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, fmt.Errorf("bad colCount")
	}
	off += n
	e.Columns = make([]Column, colCount)
	for i := 0; i < int(colCount); i++ {
		cnLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return off, fmt.Errorf("bad colNameLen[%d]", i)
		}
		off += n
		if off+int(cnLen) > len(data) {
			return off, fmt.Errorf("truncated at colName[%d]", i)
		}
		cname := string(data[off : off+int(cnLen)])
		off += int(cnLen)
		if off+1 > len(data) {
			return off, fmt.Errorf("truncated at colType[%d]", i)
		}
		colType := data[off]
		off++
		if off+1 > len(data) {
			return off, fmt.Errorf("truncated at nullable[%d]", i)
		}
		nullable := data[off] != 0
		off++
		e.Columns[i] = Column{Name: cname, Type: colType, Nullable: nullable}
	}
	uniqCount, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, fmt.Errorf("bad uniqCount")
	}
	off += n
	e.Unique = make([]Unique, uniqCount)
	for i := 0; i < int(uniqCount); i++ {
		nCols, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return off, fmt.Errorf("bad uniqCols[%d]", i)
		}
		off += n
		u := Unique{Cols: make([]int, nCols)}
		for j := 0; j < int(nCols); j++ {
			idx, n := binary.Uvarint(data[off:])
			if n <= 0 {
				return off, fmt.Errorf("bad uniqCols[%d][%d]", i, j)
			}
			u.Cols[j] = int(idx)
			off += n
		}
		e.Unique[i] = u
	}
	// sqlLen + sql (always present)
	sqlLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, fmt.Errorf("bad sqlLen")
	}
	off += n
	if off+int(sqlLen) > len(data) {
		return off, fmt.Errorf("truncated at sql")
	}
	e.CreateSQL = string(data[off : off+int(sqlLen)])
	off += int(sqlLen)
	return off, nil
}

// NextID atomically reserves and returns the next free tableID.
func (c *Catalog) NextID() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, ErrCatalogClosed
	}
	id := c.nextID
	c.nextID++
	if err := c.flushLocked(); err != nil {
		c.nextID--
		return 0, fmt.Errorf("catalog: persist nextID: %w", err)
	}
	return id, nil
}

// Put registers a new table. The full catalog is rewritten
// atomically.
func (c *Catalog) Put(entry Entry) error {
	if entry.Name == "" {
		return fmt.Errorf("%w: name is required", ErrCatalogCorrupt)
	}
	if entry.CreateSQL == "" {
		return fmt.Errorf("%w: CreateSQL is required", ErrCatalogCorrupt)
	}
	entry.Version = schemaVersionCurrent

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrCatalogClosed
	}
	if _, ok := c.byName[entry.Name]; ok {
		return fmt.Errorf("%w: name=%q", ErrCatalogExists, entry.Name)
	}
	if _, ok := c.cache[entry.TableID]; ok {
		return fmt.Errorf("%w: id=%d", ErrCatalogExists, entry.TableID)
	}
	c.cache[entry.TableID] = &entry
	c.byName[entry.Name] = entry.TableID
	if err := c.flushLocked(); err != nil {
		delete(c.cache, entry.TableID)
		delete(c.byName, entry.Name)
		return fmt.Errorf("catalog: persist: %w", err)
	}
	return nil
}

// Delete removes a table by ID.
func (c *Catalog) Delete(tableID uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrCatalogClosed
	}
	entry, ok := c.cache[tableID]
	if !ok {
		return fmt.Errorf("%w: id=%d", ErrCatalogNotFound, tableID)
	}
	delete(c.cache, tableID)
	delete(c.byName, entry.Name)
	if err := c.flushLocked(); err != nil {
		c.cache[tableID] = entry
		c.byName[entry.Name] = tableID
		return fmt.Errorf("catalog: persist: %w", err)
	}
	return nil
}

// GetByID returns the entry for tableID or ErrCatalogNotFound.
func (c *Catalog) GetByID(tableID uint64) (*Entry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, ErrCatalogClosed
	}
	entry, ok := c.cache[tableID]
	if !ok {
		return nil, fmt.Errorf("%w: id=%d", ErrCatalogNotFound, tableID)
	}
	cp := *entry
	return &cp, nil
}

// GetByName returns the entry for name or ErrCatalogNotFound.
func (c *Catalog) GetByName(name string) (*Entry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, ErrCatalogClosed
	}
	id, ok := c.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrCatalogNotFound, name)
	}
	cp := *c.cache[id]
	return &cp, nil
}

// List returns all entries sorted by tableID.
func (c *Catalog) List() []*Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*Entry, 0, len(c.cache))
	for _, e := range c.cache {
		cp := *e
		cp.Columns = append([]Column(nil), e.Columns...)
		if e.Unique != nil {
			cp.Unique = append([]Unique(nil), e.Unique...)
			for i, u := range cp.Unique {
				cp.Unique[i] = Unique{Cols: append([]int(nil), u.Cols...)}
			}
		}
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TableID < out[j].TableID })
	return out
}

// Len returns the number of registered tables.
func (c *Catalog) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

// Close releases the catalog. Idempotent.
func (c *Catalog) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// Path returns the on-disk path of catalog.dat.
func (c *Catalog) Path() string { return c.path }

// flushLocked serializes the full catalog to a temp file and
// renames it over catalog.dat. The caller MUST hold c.mu in
// write mode.
func (c *Catalog) flushLocked() error {
	var buf []byte
	buf = append(buf, catalogMagic[:]...)
	buf = append(buf, schemaVersionCurrent)
	buf = append(buf, 0, 0, 0, 0) // reserved
	var nxt [8]byte
	binary.BigEndian.PutUint64(nxt[:], c.nextID)
	buf = append(buf, nxt[:]...)
	ids := make([]uint64, 0, len(c.cache))
	for id := range c.cache {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	buf = binary.AppendUvarint(buf, uint64(len(ids)))
	for _, id := range ids {
		buf = encodeCatalogEntry(c.cache[id], buf)
	}
	tmpPath := c.path + catalogTmpSuffix
	if err := os.WriteFile(tmpPath, buf, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, c.path); err != nil {
		return err
	}
	return nil
}

func encodeCatalogEntry(e *Entry, buf []byte) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], e.TableID)
	buf = append(buf, b[:]...)
	buf = binary.AppendUvarint(buf, uint64(len(e.Name)))
	buf = append(buf, e.Name...)
	buf = binary.AppendUvarint(buf, uint64(len(e.PrimaryKey)))
	for _, idx := range e.PrimaryKey {
		buf = binary.AppendUvarint(buf, uint64(idx))
	}
	buf = binary.AppendUvarint(buf, uint64(len(e.Columns)))
	for _, c := range e.Columns {
		buf = binary.AppendUvarint(buf, uint64(len(c.Name)))
		buf = append(buf, c.Name...)
		if c.Type > 0 {
			buf = append(buf, byte(c.Type))
		} else {
			buf = append(buf, 0)
		}
		if c.Nullable {
			buf = append(buf, 0x01)
		} else {
			buf = append(buf, 0x00)
		}
	}
	buf = binary.AppendUvarint(buf, uint64(len(e.Unique)))
	for _, u := range e.Unique {
		buf = binary.AppendUvarint(buf, uint64(len(u.Cols)))
		for _, idx := range u.Cols {
			buf = binary.AppendUvarint(buf, uint64(idx))
		}
	}
	buf = binary.AppendUvarint(buf, uint64(len(e.CreateSQL)))
	buf = append(buf, e.CreateSQL...)
	return buf
}

// LoadFromSC converts an SC.TableSchema to a catalog Entry.
func entryFromSC(schema *sc.TableSchema, createSQL string) Entry {
	e := Entry{
		TableID:    schema.TableID,
		Name:       schema.Name,
		PrimaryKey: schema.PrimaryKey,
		CreateSQL:  createSQL,
	}
	for _, col := range schema.Columns {
		e.Columns = append(e.Columns, Column{
			Name:     col.Name,
			Type:     uint8(col.Type),
			Nullable: col.Nullable,
		})
	}
	return e
}

// ToSC converts a catalog Entry back to an SC.TableSchema.
func (e *Entry) ToSC() *sc.TableSchema {
	schema := &sc.TableSchema{
		TableID:    e.TableID,
		Name:       e.Name,
		PrimaryKey: e.PrimaryKey,
	}
	for _, col := range e.Columns {
		schema.Columns = append(schema.Columns, sc.ColumnDef{
			Name:     col.Name,
			Type:     sc.ColumnType(col.Type),
			Nullable: col.Nullable,
		})
	}
	return schema
}
