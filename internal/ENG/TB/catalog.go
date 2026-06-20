// Package tb implements the persistent system catalog (REQ000048).
// ct.dat is a packed binary file with header + entries, atomic
// via rename(2). See docs/design/subsystems/ENG.md for format details.
package tb

import (
	"encoding/binary"
	"fmt"
	"path/filepath"

	sc "github.com/cyw0ng95/razordata/internal/ENG/SC"
	ct "github.com/cyw0ng95/razordata/internal/ENG/CT"
)

// Constants re-exported from the shared catalog package.
const (
	schemaVersionV1      = ct.SchemaVersionV1
	schemaVersionV2      = ct.SchemaVersionV2
	schemaVersionCurrent = ct.SchemaVersionCurrent
)

var (
	catalogMagic      = ct.CatalogMagic
	catalogFileName   = ct.CatalogFileName
	catalogTmpSuffix  = ct.CatalogTmpSuffix
	catalogHeaderSize = ct.CatalogHeaderSize
)

// Catalog error sentinels — shared with LS via SC package.
var (
	ErrCatalogCorrupt  = sc.ErrCatalogCorrupt
	ErrUpgradeRequired = sc.ErrUpgradeRequired
	ErrCatalogNotFound = sc.ErrCatalogNotFound
	ErrCatalogExists   = sc.ErrCatalogExists
	ErrCatalogClosed   = sc.ErrCatalogClosed
)

type Column struct {
	Name     string
	Type     uint8
	Nullable bool
}

type Unique struct {
	Cols []int
}

type Index struct {
	IndexID   uint64
	Name      string
	Columns   []string
	Unique    bool
	CreateSQL string
}

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

// Catalog is the persistent system catalog backed by ct.dat.
type Catalog struct {
	inner *ct.Catalog
	path  string
}

// NewCatalog opens or creates a catalog rooted at dir.
func NewCatalog(dir string) (*Catalog, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: dir is required", ErrCatalogCorrupt)
	}
	inner, err := ct.NewCatalog(dir, tbEncodeEntry, tbDecodeEntry)
	if err != nil {
		return nil, err
	}
	return &Catalog{
		inner: inner,
		path:  filepath.Join(dir, catalogFileName),
	}, nil
}

// NextID reserves and returns the next free tableID.
func (c *Catalog) NextID() (uint64, error) {
	return c.inner.NextID()
}

// Put registers a new table. The catalog is rewritten atomically.
func (c *Catalog) Put(entry Entry) error {
	raw := tbEntryToRaw(&entry)
	return c.inner.PutRaw(raw)
}

// Delete removes a table by ID.
func (c *Catalog) Delete(tableID uint64) error {
	return c.inner.Delete(tableID)
}

// GetByID returns the entry for tableID.
func (c *Catalog) GetByID(tableID uint64) (*Entry, error) {
	raw, err := c.inner.GetByID(tableID)
	if err != nil {
		return nil, err
	}
	return tbRawToEntry(raw), nil
}

// ByName returns the entry for name.
func (c *Catalog) ByName(name string) (*Entry, error) {
	raw, err := c.inner.ByName(name)
	if err != nil {
		return nil, err
	}
	return tbRawToEntry(raw), nil
}

// List returns all entries sorted by tableID.
func (c *Catalog) List() []*Entry {
	raws := c.inner.List()
	out := make([]*Entry, len(raws))
	for i, r := range raws {
		out[i] = tbRawToEntry(r)
	}
	return out
}

func (c *Catalog) Len() int {
	return c.inner.Len()
}

func (c *Catalog) Close() error {
	return c.inner.Close()
}

func (c *Catalog) Path() string { return c.path }

// --- conversion helpers ---

func tbEntryToRaw(e *Entry) *ct.RawEntry {
	raw := &ct.RawEntry{
		TableID:   e.TableID,
		Name:      e.Name,
		Version:   e.Version,
		CreateSQL: e.CreateSQL,
	}
	for _, c := range e.Columns {
		raw.Columns = append(raw.Columns, ct.RawColumn{
			Name:     c.Name,
			Type:     int(c.Type),
			Nullable: c.Nullable,
		})
	}
	for _, u := range e.Unique {
		raw.Unique = append(raw.Unique, ct.RawUnique{Cols: append([]int(nil), u.Cols...)})
	}
	for _, idx := range e.Indexes {
		raw.Indexes = append(raw.Indexes, ct.RawIndex{
			IndexID:   idx.IndexID,
			Name:      idx.Name,
			Columns:   append([]string(nil), idx.Columns...),
			Unique:    idx.Unique,
			CreateSQL: idx.CreateSQL,
		})
	}
	if len(e.PrimaryKey) > 0 {
		raw.PrimaryKey = encodePK(e.PrimaryKey)
	}
	return raw
}

func tbRawToEntry(raw *ct.RawEntry) *Entry {
	e := &Entry{
		Version:   raw.Version,
		TableID:   raw.TableID,
		Name:      raw.Name,
		CreateSQL: raw.CreateSQL,
	}
	for _, c := range raw.Columns {
		e.Columns = append(e.Columns, Column{
			Name:     c.Name,
			Type:     uint8(c.Type),
			Nullable: c.Nullable,
		})
	}
	for _, u := range raw.Unique {
		e.Unique = append(e.Unique, Unique{Cols: append([]int(nil), u.Cols...)})
	}
	for _, idx := range raw.Indexes {
		e.Indexes = append(e.Indexes, Index{
			IndexID:   idx.IndexID,
			Name:      idx.Name,
			Columns:   append([]string(nil), idx.Columns...),
			Unique:    idx.Unique,
			CreateSQL: idx.CreateSQL,
		})
	}
	if raw.PrimaryKey != "" {
		e.PrimaryKey = decodePK(raw.PrimaryKey)
	}
	return e
}

func encodePK(pk []int) string {
	if len(pk) == 0 {
		return ""
	}
	buf := make([]byte, 0, len(pk)*4)
	for _, v := range pk {
		buf = binary.AppendUvarint(buf, uint64(v))
	}
	return string(buf)
}

func decodePK(s string) []int {
	if s == "" {
		return nil
	}
	data := []byte(s)
	off := 0
	var pk []int
	for off < len(data) {
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			break
		}
		pk = append(pk, int(v))
		off += n
	}
	return pk
}

// --- encode/decode callbacks ---

func tbEncodeEntry(re *ct.RawEntry, buf []byte) []byte {
	e := tbRawToEntry(re)
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

func tbDecodeEntry(data []byte, off int, re *ct.RawEntry) (int, error) {
	if off+8 > len(data) {
		return off, fmt.Errorf("truncated at tableID")
	}
	re.TableID = binary.BigEndian.Uint64(data[off : off+8])
	off += 8
	nameLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, fmt.Errorf("bad nameLen")
	}
	off += n
	if off+int(nameLen) > len(data) {
		return off, fmt.Errorf("truncated at name")
	}
	re.Name = string(data[off : off+int(nameLen)])
	off += int(nameLen)
	pkLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, fmt.Errorf("bad primaryKeyLen")
	}
	off += n
	if off+int(pkLen) > len(data) {
		return off, fmt.Errorf("truncated at primaryKey")
	}
	pk := make([]int, pkLen)
	for i := 0; i < int(pkLen); i++ {
		idx, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return off, fmt.Errorf("bad primaryKey[%d]", i)
		}
		pk[i] = int(idx)
		off += n
	}
	re.PrimaryKey = encodePK(pk)
	colCount, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, fmt.Errorf("bad colCount")
	}
	off += n
	re.Columns = make([]ct.RawColumn, colCount)
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
		re.Columns[i] = ct.RawColumn{Name: cname, Type: int(colType), Nullable: nullable}
	}
	uniqCount, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, fmt.Errorf("bad uniqCount")
	}
	off += n
	re.Unique = make([]ct.RawUnique, uniqCount)
	for i := 0; i < int(uniqCount); i++ {
		nCols, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return off, fmt.Errorf("bad uniqCols[%d]", i)
		}
		off += n
		u := ct.RawUnique{Cols: make([]int, nCols)}
		for j := 0; j < int(nCols); j++ {
			idx, n := binary.Uvarint(data[off:])
			if n <= 0 {
				return off, fmt.Errorf("bad uniqCols[%d][%d]", i, j)
			}
			u.Cols[j] = int(idx)
			off += n
		}
		re.Unique[i] = u
	}
	sqlLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, fmt.Errorf("bad sqlLen")
	}
	off += n
	if off+int(sqlLen) > len(data) {
		return off, fmt.Errorf("truncated at sql")
	}
	re.CreateSQL = string(data[off : off+int(sqlLen)])
	off += int(sqlLen)
	return off, nil
}

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
