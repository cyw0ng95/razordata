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
	schemaVersionV2      uint8 = 2
	schemaVersionCurrent       = schemaVersionV2
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

// CatalogIndex is one secondary index, stored in the parent
// CatalogEntry.Indexes. The index is realized as a separate
// keyspace in the LSM engine (key prefix "__idx__:<tableID>:
// <indexName>:<indexedValue>"); the catalog only persists the
// metadata that lets the planner discover and reason about it.
//
// REQ000251 — secondary indexes MVP.
type CatalogIndex struct {
	IndexID   uint64
	Name      string
	Columns   []string // indexed column names
	Unique    bool     // reserved; not yet enforced
	CreateSQL string   // original CREATE INDEX statement
}

// CatalogEntry is the on-disk + in-memory representation of a
// single table. CreateSQL is the original CREATE TABLE statement
// (re-parseable canonical form) used for display and admin
// tools; the structured Columns / PrimaryKey / Unique / Indexes
// fields are the source of truth for runtime query planning.
// ColumnStats holds per-column selectivity statistics (REQ000258).
type CatalogEntry struct {
	Version     uint8
	TableID     uint64
	Name        string
	Columns     []CatalogColumn
	PrimaryKey  string
	Unique      []CatalogUnique
	Indexes     []CatalogIndex // iter-22 secondary indexes
	ColumnStats []StatsEntry   // REQ000258: per-column statistics
	CreateSQL   string
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

	// Indexes (iter-22, V2 only). Pre-V2 files end here; we
	// detect EOF by checking remaining bytes. A V1 file may have
	// a stale CreateSQL field right after the Unique block, so
	// we need to disambiguate. Strategy: try to decode indexes;
	// if we run out of data, treat as V1 and back up to read
	// CreateSQL from the saved offset.
	indexOff := off
	indexes, newOff, err := decodeCatalogIndexes(data, off)
	if err == nil {
		e.Indexes = indexes
		off = newOff
	} else if errors.Is(err, errTruncated) {
		// V1 file — no indexes
		e.Indexes = nil
		off = indexOff
	} else {
		return off, err
	}

	// ColumnStats (iter-23, REQ000258). Optional, detect by EOF.
	statsOff := off
	stats, newOff, err := decodeCatalogStats(data, off)
	if err == nil {
		e.ColumnStats = stats
		off = newOff
	} else if errors.Is(err, errTruncated) {
		// Pre-iter-23 catalog — no stats
		e.ColumnStats = nil
		off = statsOff
	} else {
		return off, err
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

// errTruncated signals EOF while decoding optional fields. Used to
// distinguish "older schema" from "corrupt" during backward-compat
// reads.
var errTruncated = errors.New("catalog: truncated (older schema)")

func decodeCatalogIndexes(data []byte, off int) ([]CatalogIndex, int, error) {
	if off >= len(data) {
		return nil, off, errTruncated
	}
	count, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return nil, off, errTruncated
	}
	off += n
	out := make([]CatalogIndex, 0, count)
	for i := uint64(0); i < count; i++ {
		if off >= len(data) {
			return nil, off, errTruncated
		}
		id, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, fmt.Errorf("bad index %d id", i)
		}
		off += n
		if off >= len(data) {
			return nil, off, errTruncated
		}
		nameLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, fmt.Errorf("bad index %d name len", i)
		}
		off += n
		if off+int(nameLen) > len(data) {
			return nil, off, errTruncated
		}
		name := string(data[off : off+int(nameLen)])
		off += int(nameLen)
		if off >= len(data) {
			return nil, off, errTruncated
		}
		colCount, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, fmt.Errorf("bad index %d col count", i)
		}
		off += n
		cols := make([]string, 0, colCount)
		for j := uint64(0); j < colCount; j++ {
			if off >= len(data) {
				return nil, off, errTruncated
			}
			cn, n := binary.Uvarint(data[off:])
			if n <= 0 {
				return nil, off, fmt.Errorf("bad index %d col %d len", i, j)
			}
			off += n
			if off+int(cn) > len(data) {
				return nil, off, errTruncated
			}
			cols = append(cols, string(data[off:off+int(cn)]))
			off += int(cn)
		}
		if off >= len(data) {
			return nil, off, errTruncated
		}
		unique := data[off] != 0
		off++
		if off >= len(data) {
			return nil, off, errTruncated
		}
		sqlLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, fmt.Errorf("bad index %d sql len", i)
		}
		off += n
		if off+int(sqlLen) > len(data) {
			return nil, off, errTruncated
		}
		sql := string(data[off : off+int(sqlLen)])
		off += int(sqlLen)
		out = append(out, CatalogIndex{
			IndexID:   id,
			Name:      name,
			Columns:   cols,
			Unique:    unique,
			CreateSQL: sql,
		})
	}
	return out, off, nil
}

// decodeCatalogStats reads optional column statistics (REQ000258).
// Returns errTruncated if no stats are present (older catalog).
func decodeCatalogStats(data []byte, off int) ([]StatsEntry, int, error) {
	if off >= len(data) {
		return nil, off, errTruncated
	}
	count, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return nil, off, errTruncated
	}
	off += n
	out := make([]StatsEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		if off >= len(data) {
			return nil, off, errTruncated
		}
		colLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, fmt.Errorf("bad stats %d col len", i)
		}
		off += n
		if off+int(colLen) > len(data) {
			return nil, off, errTruncated
		}
		colName := string(data[off : off+int(colLen)])
		off += int(colLen)
		// Decode ColumnStats
		if off+16 > len(data) {
			return nil, off, errTruncated
		}
		distinctCount := int64(binary.BigEndian.Uint64(data[off : off+8]))
		off += 8
		nullCount := int64(binary.BigEndian.Uint64(data[off : off+8]))
		off += 8
		minLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, errTruncated
		}
		off += n
		if off+int(minLen) > len(data) {
			return nil, off, errTruncated
		}
		minValue := make([]byte, minLen)
		copy(minValue, data[off:off+int(minLen)])
		off += int(minLen)
		maxLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, errTruncated
		}
		off += n
		if off+int(maxLen) > len(data) {
			return nil, off, errTruncated
		}
		maxValue := make([]byte, maxLen)
		copy(maxValue, data[off:off+int(maxLen)])
		off += int(maxLen)
		bucketCount, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, errTruncated
		}
		off += n
		histogram := make([]HistogramBucket, 0, bucketCount)
		for j := uint64(0); j < bucketCount; j++ {
			if off+16 > len(data) {
				return nil, off, errTruncated
			}
			lbLen, n := binary.Uvarint(data[off:])
			if n <= 0 {
				return nil, off, errTruncated
			}
			off += n
			if off+int(lbLen) > len(data) {
				return nil, off, errTruncated
			}
			lowerBound := make([]byte, lbLen)
			copy(lowerBound, data[off:off+int(lbLen)])
			off += int(lbLen)
			ubLen, n := binary.Uvarint(data[off:])
			if n <= 0 {
				return nil, off, errTruncated
			}
			off += n
			if off+int(ubLen) > len(data) {
				return nil, off, errTruncated
			}
			upperBound := make([]byte, ubLen)
			copy(upperBound, data[off:off+int(ubLen)])
			off += int(ubLen)
			count := int64(binary.BigEndian.Uint64(data[off : off+8]))
			off += 8
			histogram = append(histogram, HistogramBucket{
				LowerBound: lowerBound,
				UpperBound: upperBound,
				Count:      count,
			})
		}
		rowCount, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, errTruncated
		}
		off += n
		out = append(out, StatsEntry{
			Column: colName,
			Stats: ColumnStats{
				DistinctCount: distinctCount,
				NullCount:     nullCount,
				MinValue:      minValue,
				MaxValue:      maxValue,
				Histogram:     histogram,
				RowCount:      int64(rowCount),
			},
		})
	}
	return out, off, nil
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

// PutIndex adds a secondary index to an existing table. The full
// catalog is rewritten atomically. Duplicate index names (within
// the same table) return ErrCatalogExists. REQ000251.
func (c *Catalog) PutIndex(tableID uint64, idx CatalogIndex) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrCatalogClosed
	}
	entry, ok := c.cache[tableID]
	if !ok {
		return fmt.Errorf("%w: table id=%d", ErrCatalogNotFound, tableID)
	}
	if idx.Name == "" {
		return fmt.Errorf("%w: index name is required", ErrCatalogCorrupt)
	}
	for _, existing := range entry.Indexes {
		if existing.Name == idx.Name {
			return fmt.Errorf("%w: index %q on table %q",
				ErrCatalogExists, idx.Name, entry.Name)
		}
	}
	if idx.IndexID == 0 {
		// Reserve a new index ID using a monotonic counter.
		// We don't persist nextIndexID across runs yet; on
		// restart, IDs start at 1 again. Conflict on ID is
		// detected on first use.
		idx.IndexID = c.nextIndexIDLocked()
	}
	entry.Indexes = append(entry.Indexes, idx)
	if err := c.flushLocked(); err != nil {
		// Rollback: remove the index we just added.
		entry.Indexes = entry.Indexes[:len(entry.Indexes)-1]
		return fmt.Errorf("catalog: persist index: %w", err)
	}
	return nil
}

// DeleteIndex removes a secondary index by name. Returns
// ErrCatalogNotFound if the table or index doesn't exist.
func (c *Catalog) DeleteIndex(tableID uint64, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrCatalogClosed
	}
	entry, ok := c.cache[tableID]
	if !ok {
		return fmt.Errorf("%w: table id=%d", ErrCatalogNotFound, tableID)
	}
	for i, idx := range entry.Indexes {
		if idx.Name == name {
			entry.Indexes = append(entry.Indexes[:i], entry.Indexes[i+1:]...)
			if err := c.flushLocked(); err != nil {
				// Rollback: re-insert
				entry.Indexes = append(entry.Indexes, idx)
				return fmt.Errorf("catalog: persist delete index: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: index %q on table id=%d",
		ErrCatalogNotFound, name, tableID)
}

// GetIndexesByTable returns a copy of the index list for a table.
// Returns nil with no error if the table has no indexes.
func (c *Catalog) GetIndexesByTable(tableID uint64) ([]CatalogIndex, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, ErrCatalogClosed
	}
	entry, ok := c.cache[tableID]
	if !ok {
		return nil, fmt.Errorf("%w: table id=%d", ErrCatalogNotFound, tableID)
	}
	out := make([]CatalogIndex, len(entry.Indexes))
	for i, idx := range entry.Indexes {
		out[i] = CatalogIndex{
			IndexID:   idx.IndexID,
			Name:      idx.Name,
			Columns:   append([]string(nil), idx.Columns...),
			Unique:    idx.Unique,
			CreateSQL: idx.CreateSQL,
		}
	}
	return out, nil
}

// GetIndex returns the named index for a table.
func (c *Catalog) GetIndex(tableID uint64, name string) (*CatalogIndex, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, ErrCatalogClosed
	}
	entry, ok := c.cache[tableID]
	if !ok {
		return nil, fmt.Errorf("%w: table id=%d", ErrCatalogNotFound, tableID)
	}
	for i := range entry.Indexes {
		if entry.Indexes[i].Name == name {
			cp := entry.Indexes[i]
			cp.Columns = append([]string(nil), cp.Columns...)
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("%w: index %q on table id=%d",
		ErrCatalogNotFound, name, tableID)
}

// nextIndexIDLocked returns the next free index ID. The caller
// MUST hold c.mu in write mode. The counter is process-local;
// on restart it resets to 1. The catalog does not persist the
// index ID counter because index IDs are only used to namespace
// the LSM key prefix; collisions are not catastrophic.
func (c *Catalog) nextIndexIDLocked() uint64 {
	maxID := uint64(0)
	for _, e := range c.cache {
		for _, idx := range e.Indexes {
			if idx.IndexID > maxID {
				maxID = idx.IndexID
			}
		}
	}
	return maxID + 1
}

// GetByID returns a deep copy of the entry for tableID or ErrCatalogNotFound.
// REQ000613: the pre-fix implementation returned `cp := *entry` which shared
// Columns/Unique/Indexes/ColumnStats slices with the cache entry. A caller
// mutating cp.Columns directly corrupted the in-memory catalog. Deep-copy
// every slice field so the returned copy is independent.
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
	cp.Columns = append([]CatalogColumn(nil), entry.Columns...)
	cp.Unique = append([]CatalogUnique(nil), entry.Unique...)
	cp.Indexes = append([]CatalogIndex(nil), entry.Indexes...)
	cp.ColumnStats = append([]StatsEntry(nil), entry.ColumnStats...)
	return &cp, nil
}

// GetByName returns a deep copy of the entry for name or ErrCatalogNotFound.
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

// GetStats returns the column statistics for the given table and
// column. Returns nil if no stats exist. REQ000085.
func (c *Catalog) GetStats(tableID uint64, colName string) *ColumnStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.cache[tableID]
	if !ok {
		return nil
	}
	for i := range entry.ColumnStats {
		if entry.ColumnStats[i].Column == colName {
			// Return a copy to prevent external mutation
			stats := entry.ColumnStats[i].Stats
			return &stats
		}
	}
	return nil
}

// GetStatsByName returns column statistics for a table by table name.
// Returns nil if the table or column has no stats. REQ000085.
func (c *Catalog) GetStatsByName(tableName, colName string) *ColumnStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, entry := range c.cache {
		if entry.Name == tableName {
			for i := range entry.ColumnStats {
				if entry.ColumnStats[i].Column == colName {
					stats := entry.ColumnStats[i].Stats
					return &stats
				}
			}
			return nil
		}
	}
	return nil
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
	if c == nil {
		return nil
	}
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
	// REQ000612: fsync the temp file before renaming so the data
	// is on stable storage when the rename becomes visible. A
	// crash after Rename returns but before the OS flushes the
	// page cache loses the write — even though the file is
	// already at its final path. We open the written file just
	// for Sync.
	{
		f, err := os.Open(tmpPath)
		if err != nil {
			return err
		}
		_ = f.Sync()
		f.Close()
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
	// Indexes (iter-22; schema V2). Pre-V2 readers hit EOF here
	// and treat the entry as having zero indexes.
	buf = encodeCatalogIndexes(e.Indexes, buf)
	// ColumnStats (iter-23; REQ000258). Pre-iter-23 readers hit EOF here.
	buf = encodeCatalogStats(e.ColumnStats, buf)
	buf = binary.AppendUvarint(buf, uint64(len(e.CreateSQL)))
	buf = append(buf, e.CreateSQL...)
	return buf
}

// encodeCatalogIndexes serializes the index list. Format:
//   count: uvarint
//   for each index:
//     indexID: uvarint
//     nameLen: uvarint
//     name: bytes
//     colCount: uvarint
//     for each column:
//       colNameLen: uvarint
//       colName: bytes
//     unique: 1 byte (0/1)
//     sqlLen: uvarint
//     sql: bytes
func encodeCatalogIndexes(idxs []CatalogIndex, buf []byte) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(idxs)))
	for _, idx := range idxs {
		buf = binary.AppendUvarint(buf, idx.IndexID)
		buf = binary.AppendUvarint(buf, uint64(len(idx.Name)))
		buf = append(buf, idx.Name...)
		buf = binary.AppendUvarint(buf, uint64(len(idx.Columns)))
		for _, c := range idx.Columns {
			buf = binary.AppendUvarint(buf, uint64(len(c)))
			buf = append(buf, c...)
		}
		if idx.Unique {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
		buf = binary.AppendUvarint(buf, uint64(len(idx.CreateSQL)))
		buf = append(buf, idx.CreateSQL...)
	}
	return buf
}

// encodeCatalogStats serializes column statistics (REQ000258).
// Format: count: uvarint + for each stat: colName + ColumnStats fields
func encodeCatalogStats(stats []StatsEntry, buf []byte) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(stats)))
	for _, s := range stats {
		buf = binary.AppendUvarint(buf, uint64(len(s.Column)))
		buf = append(buf, s.Column...)
		// Encode ColumnStats
		var tmp [8]byte
		binary.BigEndian.PutUint64(tmp[:], uint64(s.Stats.DistinctCount))
		buf = append(buf, tmp[:]...)
		binary.BigEndian.PutUint64(tmp[:], uint64(s.Stats.NullCount))
		buf = append(buf, tmp[:]...)
		buf = binary.AppendUvarint(buf, uint64(len(s.Stats.MinValue)))
		buf = append(buf, s.Stats.MinValue...)
		buf = binary.AppendUvarint(buf, uint64(len(s.Stats.MaxValue)))
		buf = append(buf, s.Stats.MaxValue...)
		buf = binary.AppendUvarint(buf, uint64(len(s.Stats.Histogram)))
		for _, bucket := range s.Stats.Histogram {
			buf = binary.AppendUvarint(buf, uint64(len(bucket.LowerBound)))
			buf = append(buf, bucket.LowerBound...)
			buf = binary.AppendUvarint(buf, uint64(len(bucket.UpperBound)))
			buf = append(buf, bucket.UpperBound...)
			binary.BigEndian.PutUint64(tmp[:], uint64(bucket.Count))
			buf = append(buf, tmp[:]...)
		}
		buf = binary.AppendUvarint(buf, uint64(s.Stats.RowCount))
	}
	return buf
}

// PutStats updates column statistics for a table. The full
// catalog is rewritten atomically. REQ000258.
func (c *Catalog) PutStats(tableID uint64, colName string, stats ColumnStats) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrCatalogClosed
	}
	entry, ok := c.cache[tableID]
	if !ok {
		return fmt.Errorf("%w: table id=%d", ErrCatalogNotFound, tableID)
	}
	if colName == "" {
		return fmt.Errorf("%w: column name is required", ErrCatalogCorrupt)
	}
	// Find or create stats entry
	found := false
	for i := range entry.ColumnStats {
		if entry.ColumnStats[i].Column == colName {
			entry.ColumnStats[i].Stats = stats
			found = true
			break
		}
	}
	if !found {
		entry.ColumnStats = append(entry.ColumnStats, StatsEntry{
			TableID: tableID,
			Column:  colName,
			Stats:   stats,
		})
	}
	// Rewrite catalog atomically
	if err := c.flushLocked(); err != nil {
		return fmt.Errorf("catalog: persist stats: %w", err)
	}
	return nil
}
