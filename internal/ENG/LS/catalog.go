// Package ls implements the persistent system catalog (iter-12).
// catalog.dat is a packed binary file with header + entries, atomic
// via rename(2). See docs/design/subsystems/ENG.md for format details.
package ls

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	ct "github.com/cyw0ng95/razordata/internal/ENG/CT"
	sc "github.com/cyw0ng95/razordata/internal/ENG/SC"

	LX "github.com/cyw0ng95/razordata/internal/SQL/LX"
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

// Catalog error sentinels — shared with TB via SC package.
var (
	ErrCatalogCorrupt  = sc.ErrCatalogCorrupt
	ErrUpgradeRequired = sc.ErrUpgradeRequired
	ErrCatalogNotFound = sc.ErrCatalogNotFound
	ErrCatalogExists   = sc.ErrCatalogExists
	ErrCatalogClosed   = sc.ErrCatalogClosed
)

// CatalogColumn represents a column in the on-disk catalog.
type CatalogColumn struct {
	Name     string
	Type     int
	Nullable bool
}

// CatalogUnique is one UNIQUE constraint.
type CatalogUnique struct {
	Cols []int
}

// CatalogIndex is a secondary index entry (REQ000251).
type CatalogIndex struct {
	IndexID   uint64
	Name      string
	Columns   []string // indexed column names
	Unique    bool     // reserved; not yet enforced
	CreateSQL string   // original CREATE INDEX statement
}

// CatalogEntry is the on-disk representation of a table.
type CatalogEntry struct {
	TableID     uint64
	Name        string
	Columns     []CatalogColumn
	PrimaryKey  string
	Unique      []CatalogUnique
	Indexes     []CatalogIndex // iter-22 secondary indexes
	ColumnStats []StatsEntry   // REQ000258: per-column statistics
	CreateSQL   string
	Version     uint8
}

// Catalog is the persistent system catalog backed by catalog.dat.
type Catalog struct {
	inner  *ct.Catalog
	path   string
	mu     sync.RWMutex // protects LS-specific operations (indexes, stats)
	nextID uint64       // cached copy of inner's nextID for test access
}

// NewCatalog opens or creates a catalog rooted at dir.
func NewCatalog(dir string) (*Catalog, error) {
	inner, err := ct.NewCatalog(dir, lsEncodeEntry, lsDecodeEntry)
	if err != nil {
		return nil, err
	}
	return &Catalog{
		inner:  inner,
		path:   inner.Path(),
		nextID: inner.NextIDVal(),
	}, nil
}

// NextID reserves and returns the next free tableID.
func (c *Catalog) NextID() (uint64, error) {
	id, err := c.inner.NextID()
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.nextID = c.inner.NextIDVal()
	c.mu.Unlock()
	return id, nil
}

// Put registers a new table. The catalog is rewritten atomically.
func (c *Catalog) Put(entry CatalogEntry) error {
	raw := lsEntryToRaw(&entry)
	return c.inner.PutRaw(raw)
}

// Delete removes a table by ID.
func (c *Catalog) Delete(tableID uint64) error {
	return c.inner.Delete(tableID)
}

// PutIndex adds a secondary index to a table (REQ000251).
func (c *Catalog) PutIndex(tableID uint64, idx CatalogIndex) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inner == nil {
		return ErrCatalogClosed
	}
	raw, err := c.inner.GetByIDRef(tableID)
	if err != nil {
		return fmt.Errorf("%w: table id=%d", ErrCatalogNotFound, tableID)
	}
	if idx.Name == "" {
		return fmt.Errorf("%w: index name is required", ErrCatalogCorrupt)
	}
	for _, existing := range raw.Indexes {
		if existing.Name == idx.Name {
			return fmt.Errorf("%w: index %q on table %q",
				ErrCatalogExists, idx.Name, raw.Name)
		}
	}
	if idx.IndexID == 0 {
		idx.IndexID = c.nextIndexIDLocked()
	}
	raw.Indexes = append(raw.Indexes, ct.RawIndex{
		IndexID:   idx.IndexID,
		Name:      idx.Name,
		Columns:   append([]string(nil), idx.Columns...),
		Unique:    idx.Unique,
		CreateSQL: idx.CreateSQL,
	})
	if err := c.inner.FlushLocked(); err != nil {
		raw.Indexes = raw.Indexes[:len(raw.Indexes)-1]
		return fmt.Errorf("catalog: persist index: %w", err)
	}
	return nil
}

// DeleteIndex removes a secondary index by name.
func (c *Catalog) DeleteIndex(tableID uint64, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inner == nil {
		return ErrCatalogClosed
	}
	raw, err := c.inner.GetByIDRef(tableID)
	if err != nil {
		return fmt.Errorf("%w: table id=%d", ErrCatalogNotFound, tableID)
	}
	for i, idx := range raw.Indexes {
		if idx.Name == name {
			removed := raw.Indexes[i]
			raw.Indexes = append(raw.Indexes[:i], raw.Indexes[i+1:]...)
			if err := c.inner.FlushLocked(); err != nil {
				raw.Indexes = append(raw.Indexes, removed)
				return fmt.Errorf("catalog: persist delete index: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: index %q on table id=%d",
		ErrCatalogNotFound, name, tableID)
}

// IndexesByTable returns a copy of the index list for a table.
func (c *Catalog) IndexesByTable(tableID uint64) ([]CatalogIndex, error) {
	raw, err := c.inner.GetByID(tableID)
	if err != nil {
		return nil, fmt.Errorf("%w: table id=%d", ErrCatalogNotFound, tableID)
	}
	out := make([]CatalogIndex, len(raw.Indexes))
	for i, idx := range raw.Indexes {
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

// Index returns the named index for a table.
func (c *Catalog) Index(tableID uint64, name string) (*CatalogIndex, error) {
	raw, err := c.inner.GetByID(tableID)
	if err != nil {
		return nil, fmt.Errorf("%w: table id=%d", ErrCatalogNotFound, tableID)
	}
	for i := range raw.Indexes {
		if raw.Indexes[i].Name == name {
			cp := CatalogIndex{
				IndexID:   raw.Indexes[i].IndexID,
				Name:      raw.Indexes[i].Name,
				Columns:   append([]string(nil), raw.Indexes[i].Columns...),
				Unique:    raw.Indexes[i].Unique,
				CreateSQL: raw.Indexes[i].CreateSQL,
			}
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("%w: index %q on table id=%d",
		ErrCatalogNotFound, name, tableID)
}

func (c *Catalog) nextIndexIDLocked() uint64 {
	maxID := uint64(0)
	for _, e := range c.inner.Cache() {
		for _, idx := range e.Indexes {
			if idx.IndexID > maxID {
				maxID = idx.IndexID
			}
		}
	}
	return maxID + 1
}

// GetByID returns a deep copy of the entry for tableID (REQ000613).
func (c *Catalog) GetByID(tableID uint64) (*CatalogEntry, error) {
	raw, err := c.inner.GetByID(tableID)
	if err != nil {
		return nil, err
	}
	return lsRawToEntry(raw), nil
}

// ByName returns a deep copy of the entry for name.
func (c *Catalog) ByName(name string) (*CatalogEntry, error) {
	raw, err := c.inner.ByName(name)
	if err != nil {
		return nil, err
	}
	return lsRawToEntry(raw), nil
}

// ColumnStats returns column statistics for a table column (REQ000085).
func (c *Catalog) ColumnStats(tableID uint64, colName string) *ColumnStats {
	raw, err := c.inner.GetByID(tableID)
	if err != nil {
		return nil
	}
	stats := decodeStatsBlob(raw.Stats)
	for i := range stats {
		if stats[i].Column == colName {
			s := stats[i].Stats
			return &s
		}
	}
	return nil
}

// ColumnStatsByName returns column statistics by table name (REQ000085).
func (c *Catalog) ColumnStatsByName(tableName, colName string) *ColumnStats {
	for _, raw := range c.inner.Cache() {
		if raw.Name == tableName {
			stats := decodeStatsBlob(raw.Stats)
			for i := range stats {
				if stats[i].Column == colName {
					s := stats[i].Stats
					return &s
				}
			}
			return nil
		}
	}
	return nil
}

// List returns all entries sorted by tableID.
func (c *Catalog) List() []*CatalogEntry {
	raws := c.inner.List()
	out := make([]*CatalogEntry, len(raws))
	for i, r := range raws {
		out[i] = lsRawToEntry(r)
	}
	return out
}

func (c *Catalog) Len() int {
	return c.inner.Len()
}

func (c *Catalog) Close() error {
	if c == nil {
		return nil
	}
	return c.inner.Close()
}

func (c *Catalog) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

// PutStats updates column statistics for a table (REQ000258).
func (c *Catalog) PutStats(tableID uint64, colName string, stats ColumnStats) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, err := c.inner.GetByIDRef(tableID)
	if err != nil {
		return fmt.Errorf("%w: table id=%d", ErrCatalogNotFound, tableID)
	}
	if colName == "" {
		return fmt.Errorf("%w: column name is required", ErrCatalogCorrupt)
	}
	oldBlob := append([]byte(nil), raw.Stats...)
	existing := decodeStatsBlob(raw.Stats)
	found := false
	for i := range existing {
		if existing[i].Column == colName {
			existing[i].Stats = stats
			found = true
			break
		}
	}
	if !found {
		existing = append(existing, StatsEntry{
			TableID: tableID,
			Column:  colName,
			Stats:   stats,
		})
	}
	raw.Stats = encodeStatsBlob(existing)
	if err := c.inner.FlushLocked(); err != nil {
		raw.Stats = oldBlob
		return fmt.Errorf("catalog: persist stats: %w", err)
	}
	return nil
}

// --- conversion helpers ---

func lsEntryToRaw(e *CatalogEntry) *ct.RawEntry {
	raw := &ct.RawEntry{
		TableID:    e.TableID,
		Name:       e.Name,
		PrimaryKey: e.PrimaryKey,
		Version:    e.Version,
		CreateSQL:  e.CreateSQL,
	}
	for _, c := range e.Columns {
		raw.Columns = append(raw.Columns, ct.RawColumn{
			Name:     c.Name,
			Type:     c.Type,
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
	raw.Stats = encodeStatsBlob(e.ColumnStats)
	return raw
}

func lsRawToEntry(raw *ct.RawEntry) *CatalogEntry {
	e := &CatalogEntry{
		TableID:     raw.TableID,
		Name:        raw.Name,
		PrimaryKey:  raw.PrimaryKey,
		CreateSQL:   raw.CreateSQL,
		Version:     raw.Version,
		ColumnStats: decodeStatsBlob(raw.Stats),
	}
	for _, c := range raw.Columns {
		e.Columns = append(e.Columns, CatalogColumn{
			Name:     c.Name,
			Type:     c.Type,
			Nullable: c.Nullable,
		})
	}
	for _, u := range raw.Unique {
		e.Unique = append(e.Unique, CatalogUnique{Cols: append([]int(nil), u.Cols...)})
	}
	for _, idx := range raw.Indexes {
		e.Indexes = append(e.Indexes, CatalogIndex{
			IndexID:   idx.IndexID,
			Name:      idx.Name,
			Columns:   append([]string(nil), idx.Columns...),
			Unique:    idx.Unique,
			CreateSQL: idx.CreateSQL,
		})
	}
	return e
}

// --- stats blob encode/decode ---

func encodeStatsBlob(stats []StatsEntry) []byte {
	if len(stats) == 0 {
		return nil
	}
	var buf []byte
	buf = binary.AppendUvarint(buf, uint64(len(stats)))
	for _, s := range stats {
		buf = binary.AppendUvarint(buf, uint64(len(s.Column)))
		buf = append(buf, s.Column...)
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

func decodeStatsBlob(data []byte) []StatsEntry {
	if len(data) == 0 {
		return nil
	}
	off := 0
	count, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return nil
	}
	off += n
	out := make([]StatsEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		if off >= len(data) {
			return nil
		}
		colLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil
		}
		off += n
		if off+int(colLen) > len(data) {
			return nil
		}
		colName := string(data[off : off+int(colLen)])
		off += int(colLen)
		if off+16 > len(data) {
			return nil
		}
		distinctCount := int64(binary.BigEndian.Uint64(data[off : off+8]))
		off += 8
		nullCount := int64(binary.BigEndian.Uint64(data[off : off+8]))
		off += 8
		minLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil
		}
		off += n
		if off+int(minLen) > len(data) {
			return nil
		}
		minValue := make([]byte, minLen)
		copy(minValue, data[off:off+int(minLen)])
		off += int(minLen)
		maxLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil
		}
		off += n
		if off+int(maxLen) > len(data) {
			return nil
		}
		maxValue := make([]byte, maxLen)
		copy(maxValue, data[off:off+int(maxLen)])
		off += int(maxLen)
		bucketCount, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil
		}
		off += n
		histogram := make([]HistogramBucket, 0, bucketCount)
		for j := uint64(0); j < bucketCount; j++ {
			if off+16 > len(data) {
				return nil
			}
			lbLen, n := binary.Uvarint(data[off:])
			if n <= 0 {
				return nil
			}
			off += n
			if off+int(lbLen) > len(data) {
				return nil
			}
			lowerBound := make([]byte, lbLen)
			copy(lowerBound, data[off:off+int(lbLen)])
			off += int(lbLen)
			ubLen, n := binary.Uvarint(data[off:])
			if n <= 0 {
				return nil
			}
			off += n
			if off+int(ubLen) > len(data) {
				return nil
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
			return nil
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
	return out
}

// --- encode/decode callbacks ---

func lsEncodeEntry(re *ct.RawEntry, buf []byte) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], re.TableID)
	buf = append(buf, b[:]...)
	buf = binary.AppendUvarint(buf, uint64(len(re.Name)))
	buf = append(buf, re.Name...)
	buf = binary.AppendUvarint(buf, uint64(len(re.PrimaryKey)))
	buf = append(buf, re.PrimaryKey...)
	buf = binary.AppendUvarint(buf, uint64(len(re.Columns)))
	for _, c := range re.Columns {
		buf = binary.AppendUvarint(buf, uint64(len(c.Name)))
		buf = append(buf, c.Name...)
		if c.Type > 0 && c.Type < 256 {
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
	buf = binary.AppendUvarint(buf, uint64(len(re.Unique)))
	for _, u := range re.Unique {
		buf = binary.AppendUvarint(buf, uint64(len(u.Cols)))
		for _, idx := range u.Cols {
			buf = binary.AppendUvarint(buf, uint64(idx))
		}
	}
	buf = encodeCatalogIndexes(re.Indexes, buf)
	// Stats blob is already in inline format (uvarint count + entries).
	// Write it directly for backward compatibility with pre-REQ000660 catalogs.
	if len(re.Stats) > 0 {
		buf = append(buf, re.Stats...)
	} else {
		buf = binary.AppendUvarint(buf, 0) // zero entries
	}
	buf = binary.AppendUvarint(buf, uint64(len(re.CreateSQL)))
	buf = append(buf, re.CreateSQL...)
	return buf
}

var errTruncated = errors.New("catalog: truncated (older schema)")

func lsDecodeEntry(data []byte, off int, re *ct.RawEntry) (int, error) {
	if off+8 > len(data) {
		return off, errors.New("truncated at tableID")
	}
	re.TableID = binary.BigEndian.Uint64(data[off : off+8])
	off += 8
	nameLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, errors.New("bad name len")
	}
	off += n
	if off+int(nameLen) > len(data) {
		return off, errors.New("name out of range")
	}
	re.Name = string(data[off : off+int(nameLen)])
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
		re.PrimaryKey = string(data[off : off+int(pkLen)])
	}
	off += int(pkLen)

	colCount, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, errors.New("bad col count")
	}
	off += n
	re.Columns = make([]ct.RawColumn, colCount)
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
		var colType int
		if off+1 > len(data) {
			colType = 0
		} else {
			colType = int(data[off])
			if colType == 0 {
				colType = int(LX.T_TEXT)
			}
			off++
		}
		if off+1 > len(data) {
			return off, fmt.Errorf("col %d missing nullable flag", i)
		}
		nullable := data[off] != 0
		off++
		re.Columns[i] = ct.RawColumn{Name: name, Type: colType, Nullable: nullable}
	}

	uniqCount, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, errors.New("bad unique count")
	}
	off += n
	re.Unique = make([]ct.RawUnique, uniqCount)
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
		re.Unique[i] = ct.RawUnique{Cols: idxs}
	}

	indexOff := off
	indexes, newOff, err := decodeCatalogIndexes(data, off)
	if err == nil {
		re.Indexes = indexes
		off = newOff
	} else if errors.Is(err, errTruncated) {
		re.Indexes = nil
		off = indexOff
	} else {
		return off, err
	}

	statsOff := off
	statsBytes, newOff, statsErr := readInlineStatsBlob(data, off)
	if statsErr == nil {
		re.Stats = statsBytes
		off = newOff
	} else if errors.Is(statsErr, errTruncated) {
		re.Stats = nil
		off = statsOff
	} else {
		return off, statsErr
	}

	sqlLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return off, errors.New("bad sql len")
	}
	off += n
	if off+int(sqlLen) > len(data) {
		return off, errors.New("sql out of range")
	}
	re.CreateSQL = string(data[off : off+int(sqlLen)])
	off += int(sqlLen)
	return off, nil
}

func decodeCatalogIndexes(data []byte, off int) ([]ct.RawIndex, int, error) {
	if off >= len(data) {
		return nil, off, errTruncated
	}
	count, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return nil, off, errTruncated
	}
	off += n
	out := make([]ct.RawIndex, 0, count)
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
		out = append(out, ct.RawIndex{
			IndexID:   id,
			Name:      name,
			Columns:   cols,
			Unique:    unique,
			CreateSQL: sql,
		})
	}
	return out, off, nil
}

// readInlineStatsBlob reads the stats section in inline format
// (same wire format as the old decodeCatalogStats) and returns
// the raw bytes for storage in RawEntry.Stats.
func readInlineStatsBlob(data []byte, off int) ([]byte, int, error) {
	if off >= len(data) {
		return nil, off, errTruncated
	}
	startOff := off
	count, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return nil, off, errTruncated
	}
	off += n
	for i := uint64(0); i < count; i++ {
		if off >= len(data) {
			return nil, off, errTruncated
		}
		colLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, errTruncated
		}
		off += n
		if off+int(colLen) > len(data) {
			return nil, off, errTruncated
		}
		off += int(colLen)
		if off+16 > len(data) {
			return nil, off, errTruncated
		}
		off += 16 // distinctCount + nullCount
		minLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, errTruncated
		}
		off += n
		if off+int(minLen) > len(data) {
			return nil, off, errTruncated
		}
		off += int(minLen)
		maxLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, errTruncated
		}
		off += n
		if off+int(maxLen) > len(data) {
			return nil, off, errTruncated
		}
		off += int(maxLen)
		bucketCount, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, errTruncated
		}
		off += n
		for j := uint64(0); j < bucketCount; j++ {
			lbLen, n := binary.Uvarint(data[off:])
			if n <= 0 {
				return nil, off, errTruncated
			}
			off += n
			if off+int(lbLen) > len(data) {
				return nil, off, errTruncated
			}
			off += int(lbLen)
			ubLen, n := binary.Uvarint(data[off:])
			if n <= 0 {
				return nil, off, errTruncated
			}
			off += n
			if off+int(ubLen) > len(data) {
				return nil, off, errTruncated
			}
			off += int(ubLen)
			off += 8 // count
		}
		rowCount, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, off, errTruncated
		}
		_ = rowCount
		off += n
	}
	return data[startOff:off], off, nil
}

func encodeCatalogIndexes(idxs []ct.RawIndex, buf []byte) []byte {
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
