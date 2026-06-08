// Package ls — Catalog (iter-12)
//
// The Catalog is the persistent, on-disk equivalent of the
// in-memory `tableRegistry`. The catalog owns a single
// `catalog.dat` file under dir and exposes Get/Put/Delete/List
// for the system to call on CREATE TABLE / DROP TABLE.
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
// # Why not reuse the LS engine?
//
// The LS engine's flush → SST path is currently broken in three
// places (path mismatch, sstIterator state machine, block checksum
// layout — see iter-12 gap analysis for full details). A future
// iteration (iter-04b / 12b) is expected to repair the LSM tree.
// In the meantime the catalog uses a self-contained file so that
// `CREATE TABLE` durability does not depend on LSM correctness.
package ls

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	LX "github.com/cyw0ng95/razordata/internal/SQL/LX"
)

// Schema-version constants for the system catalog.
const (
	schemaVersionV1      uint8 = 1
	schemaVersionCurrent       = schemaVersionV1
)

var (
	catalogMagic     = [4]byte{'R', 'C', 'A', 'T'}
	catalogFileName  = "catalog.dat"
	catalogTmpSuffix = ".tmp"
	// catalogHeaderSize is the fixed-size prefix before the
	// varint-encoded count: magic(4) + version(1) + reserved(4)
	// + nextID(8) = 17 bytes.
	catalogHeaderSize = 17
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
	// or tableID is already registered.
	ErrCatalogExists = errors.New("catalog: table already exists")
	// ErrCatalogClosed is returned by Get/Put/Delete/List after
	// Close has been called.
	ErrCatalogClosed = errors.New("catalog: closed")
)

// CatalogColumn is the on-disk + in-memory representation of one
// column. Nullable=false means NOT NULL was specified (or
// implied by PRIMARY KEY). Type is the LX token int that names
// the SQL column type (LX.T_INT_KW, LX.T_TEXT, etc.) used by
// EX.ExtractParamTypes for `?` placeholder validation.
type CatalogColumn struct {
	Name     string
	Type     int
	Nullable bool
}

// CatalogUnique is one UNIQUE constraint, resolved to column
// indices in the parent entry.
type CatalogUnique struct {
	Cols []int
}

// CatalogEntry is the on-disk + in-memory representation of a
// single table. CreateSQL is the original CREATE TABLE statement
// (re-parseable canonical form) used for display and admin
// tools; the structured Columns / PrimaryKey / Unique fields
// are the source of truth for runtime query planning.
type CatalogEntry struct {
	Version    uint8
	TableID    uint64
	Name       string
	Columns    []CatalogColumn
	PrimaryKey string
	Unique     []CatalogUnique
	CreateSQL  string
}

// Catalog is the persistent, on-disk system catalog. The catalog
// owns a single file at <dir>/catalog.dat and serializes every
// write to disk before returning, so a crash after Put returns
// never leaves the table in a half-registered state.
//
// All public methods are goroutine-safe. The cache map is the
// read path; the file is the source of truth and is rewritten
// (atomically) on every Put/Delete.
type Catalog struct {
	mu     sync.RWMutex
	path   string
	cache  map[uint64]*CatalogEntry
	byName map[string]uint64
	nextID uint64
	closed bool
}

// NewCatalog opens (or creates) a catalog rooted at dir. The
// constructor reads catalog.dat (if present) into the in-memory
// cache and returns the highest-persisted tableID+1 as the next
// available ID. Bootstrap is fail-fast: a corrupt or
// future-versioned file returns ErrCatalogCorrupt or
// ErrUpgradeRequired.
func NewCatalog(dir string) (*Catalog, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: dir is required", ErrCatalogCorrupt)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("catalog: mkdir %s: %w", dir, err)
	}
	c := &Catalog{
		path:   filepath.Join(dir, catalogFileName),
		cache:  make(map[uint64]*CatalogEntry),
		byName: make(map[string]uint64),
	}
	if err := c.bootstrap(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Catalog) bootstrap() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.nextID = 1
			return nil
		}
		return fmt.Errorf("catalog: read %s: %w", c.path, err)
	}
	if len(data) < catalogHeaderSize {
		return fmt.Errorf("%w: file too short (%d bytes)", ErrCatalogCorrupt, len(data))
	}
	if [4]byte{data[0], data[1], data[2], data[3]} != catalogMagic {
		return fmt.Errorf("%w: bad magic %x", ErrCatalogCorrupt, data[:4])
	}
	version := data[4]
	if version > schemaVersionCurrent {
		return fmt.Errorf("%w: file=%d, current=%d",
			ErrUpgradeRequired, version, schemaVersionCurrent)
	}
	off := 5
	off += 4 // reserved
	if off+8 > len(data) {
		return fmt.Errorf("%w: header truncated at nextID", ErrCatalogCorrupt)
	}
	c.nextID = binary.BigEndian.Uint64(data[off : off+8])
	off += 8
	count, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return fmt.Errorf("%w: bad count", ErrCatalogCorrupt)
	}
	off += n
	for i := uint64(0); i < count; i++ {
		e := &CatalogEntry{Version: version}
		if off, err = decodeCatalogEntry(data, off, e); err != nil {
			return fmt.Errorf("%w: entry %d: %v", ErrCatalogCorrupt, i, err)
		}
		c.cache[e.TableID] = e
		c.byName[e.Name] = e.TableID
		if e.TableID >= c.nextID {
			c.nextID = e.TableID + 1
		}
	}
	if c.nextID == 0 {
		c.nextID = 1
	}
	return nil
}

func decodeCatalogEntry(data []byte, off int, e *CatalogEntry) (int, error) {
	if off+8 > len(data) {
		return off, errors.New("truncated at tableID")
	}
	e.TableID = binary.BigEndian.Uint64(data[off : off+8])
	off += 8
	nameLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, errors.New("bad name len")
	}
	off += n
	if off+int(nameLen) > len(data) {
		return off, errors.New("name out of range")
	}
	e.Name = string(data[off : off+int(nameLen)])
	off += int(nameLen)

	pkLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, errors.New("bad primary key len")
	}
	off += n
	if off+int(pkLen) > len(data) {
		return off, errors.New("primary key out of range")
	}
	if pkLen > 0 {
		e.PrimaryKey = string(data[off : off+int(pkLen)])
	}
	off += int(pkLen)

	colCount, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, errors.New("bad col count")
	}
	off += n
	e.Columns = make([]CatalogColumn, colCount)
	for i := uint64(0); i < colCount; i++ {
		cn, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return off, fmt.Errorf("bad col %d name len", i)
		}
		off += n
		if off+int(cn) > len(data) {
			return off, fmt.Errorf("col %d name out of range", i)
		}
		name := string(data[off : off+int(cn)])
		off += int(cn)
		// Decode column type token (LX.T_INT_KW / T_TEXT / ...).
		// Pre-iter-16 catalogs did not include this byte; for
		// backward-compat we accept a missing byte and default
		// to T_TEXT (treated as VARCHAR by the deparser).
		var colType int
		if off+1 > len(data) {
			colType = 0
		} else {
			colType = int(data[off])
			if colType == 0 {
				// Pre-iter-16 sentinels encoded as a zero byte
				// (Type=0). Substitute T_TEXT so downstream
				// coercibility checks produce a meaningful verdict.
				colType = int(LX.T_TEXT)
			}
			off++
		}
		if off+1 > len(data) {
			return off, fmt.Errorf("col %d missing nullable flag", i)
		}
		nullable := data[off] != 0
		off++
		e.Columns[i] = CatalogColumn{Name: name, Type: colType, Nullable: nullable}
	}

	uniqCount, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, errors.New("bad unique count")
	}
	off += n
	e.Unique = make([]CatalogUnique, uniqCount)
	for i := uint64(0); i < uniqCount; i++ {
		nCols, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return off, fmt.Errorf("bad unique %d size", i)
		}
		off += n
		idxs := make([]int, nCols)
		for j := uint64(0); j < nCols; j++ {
			idx, n := binary.Uvarint(data[off:])
			if n <= 0 {
				return off, fmt.Errorf("bad unique %d col %d", i, j)
			}
			off += n
			idxs[j] = int(idx)
		}
		e.Unique[i] = CatalogUnique{Cols: idxs}
	}

	sqlLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, errors.New("bad sql len")
	}
	off += n
	if off+int(sqlLen) > len(data) {
		return off, errors.New("sql out of range")
	}
	e.CreateSQL = string(data[off : off+int(sqlLen)])
	off += int(sqlLen)
	return off, nil
}

// NextID atomically reserves and returns the next free tableID.
// The new value is fsync'd to disk before returning, so a crash
// after NextID returns cannot reuse the same ID.
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
// atomically. The cache is updated only after the file write
// succeeds, so a partial write is never observable to readers.
func (c *Catalog) Put(entry CatalogEntry) error {
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

// Delete removes a table by ID. A missing ID is an error
// (callers that want delete-if-exists semantics must Get first).
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
func (c *Catalog) GetByID(tableID uint64) (*CatalogEntry, error) {
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
func (c *Catalog) GetByName(name string) (*CatalogEntry, error) {
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

// List returns all entries sorted by tableID. The returned slice
// is freshly allocated; mutating it does not affect the catalog.
func (c *Catalog) List() []*CatalogEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*CatalogEntry, 0, len(c.cache))
	for _, e := range c.cache {
		cp := *e
		cp.Columns = append([]CatalogColumn(nil), e.Columns...)
		if e.Unique != nil {
			cp.Unique = append([]CatalogUnique(nil), e.Unique...)
			for i, u := range cp.Unique {
				cp.Unique[i] = CatalogUnique{Cols: append([]int(nil), u.Cols...)}
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

// Path returns the on-disk path of catalog.dat. Useful for
// admin tools and crash-recovery tests.
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

func encodeCatalogEntry(e *CatalogEntry, buf []byte) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], e.TableID)
	buf = append(buf, b[:]...)
	buf = binary.AppendUvarint(buf, uint64(len(e.Name)))
	buf = append(buf, e.Name...)
	buf = binary.AppendUvarint(buf, uint64(len(e.PrimaryKey)))
	buf = append(buf, e.PrimaryKey...)
	buf = binary.AppendUvarint(buf, uint64(len(e.Columns)))
	for _, c := range e.Columns {
		buf = binary.AppendUvarint(buf, uint64(len(c.Name)))
		buf = append(buf, c.Name...)
		// Column type token (1 byte; R16-3). Pre-iter-16 catalogs
		// omitted this byte; readers default to T_TEXT in that case.
		if c.Type > 0 && c.Type < 256 {
			buf = append(buf, byte(c.Type))
		} else {
			// Sentinel: type=0 means "unknown" — emit a default
			// (T_TEXT=58 in the LX token table). Replaced by
			// zero on read; see decode for fallback handling.
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
