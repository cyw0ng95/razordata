// Package ct provides the shared persistent catalog engine used by
// ENG/TB and ENG/LS. Both subsystems supply encode/decode callbacks
// that convert between their native entry types and the catalog's
// on-disk format, while this package handles the file I/O, caching,
// locking, and atomic persistence.
//
// REQ000660: extracted from duplicated code in ENG/TB and ENG/LS.
package ct

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"

	sc "github.com/cyw0ng95/razordata/internal/ENG/SC"
)

const (
	SchemaVersionV1      uint8 = 1
	SchemaVersionV2      uint8 = 2
	SchemaVersionCurrent       = SchemaVersionV2
)

var (
	CatalogMagic      = [4]byte{'R', 'C', 'A', 'T'}
	CatalogFileName   = "catalog.dat"
	CatalogTmpSuffix  = ".tmp"
	CatalogHeaderSize = 17 // magic(4) + version(1) + reserved(4) + nextID(8)
)

// Error sentinels — re-exported from SC for use by subsystems.
var (
	ErrCatalogCorrupt  = sc.ErrCatalogCorrupt
	ErrUpgradeRequired = sc.ErrUpgradeRequired
	ErrCatalogNotFound = sc.ErrCatalogNotFound
	ErrCatalogExists   = sc.ErrCatalogExists
	ErrCatalogClosed   = sc.ErrCatalogClosed
)

// RawColumn is the shared column representation on disk.
type RawColumn struct {
	Name     string
	Type     int
	Nullable bool
}

// RawUnique is one UNIQUE constraint.
type RawUnique struct {
	Cols []int
}

// RawIndex is a secondary index entry.
type RawIndex struct {
	IndexID   uint64
	Name      string
	Columns   []string
	Unique    bool
	CreateSQL string
}

// RawEntry is the on-disk representation of a table.
type RawEntry struct {
	TableID    uint64
	Name       string
	Columns    []RawColumn
	PrimaryKey string
	Unique     []RawUnique
	Indexes    []RawIndex
	Stats      []byte // opaque per-subsystem stats blob
	Version    uint8
	CreateSQL  string
}

// EncodeFunc serialises a native entry to the binary catalog format.
type EncodeFunc func(e *RawEntry, buf []byte) []byte

// DecodeFunc deserialises one entry from binary data. It reads from
// data[off:] and returns the new offset and any error.
type DecodeFunc func(data []byte, off int, e *RawEntry) (int, error)

// Catalog is the persistent system catalog backed by catalog.dat.
type Catalog struct {
	path        string
	mu          sync.RWMutex
	cache       map[uint64]*RawEntry
	byName      map[string]uint64
	nextID      uint64
	closed      atomic.Bool
	encodeEntry EncodeFunc
	decodeEntry DecodeFunc
}

// NewCatalog opens or creates a catalog rooted at dir. The encode
// and decode callbacks handle subsystem-specific serialisation.
func NewCatalog(dir string, encode EncodeFunc, decode DecodeFunc) (*Catalog, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: dir is required", ErrCatalogCorrupt)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("catalog: mkdir %s: %w", dir, err)
	}
	c := &Catalog{
		path:        filepath.Join(dir, CatalogFileName),
		cache:       make(map[uint64]*RawEntry),
		byName:      make(map[string]uint64),
		encodeEntry: encode,
		decodeEntry: decode,
	}
	if err := c.bootstrap(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Catalog) bootstrap() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			c.nextID = 1
			return nil
		}
		return fmt.Errorf("catalog: read %s: %w", c.path, err)
	}
	if len(data) < CatalogHeaderSize {
		return fmt.Errorf("%w: file too short (%d bytes)", ErrCatalogCorrupt, len(data))
	}
	if string(data[0:4]) != string(CatalogMagic[:]) {
		return fmt.Errorf("%w: bad magic %x", ErrCatalogCorrupt, data[:4])
	}
	version := data[4]
	if version > SchemaVersionCurrent {
		return fmt.Errorf("%w: file version %d > current %d", ErrUpgradeRequired, version, SchemaVersionCurrent)
	}
	c.nextID = binary.BigEndian.Uint64(data[9:17])
	off := 17
	count, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return fmt.Errorf("%w: bad count", ErrCatalogCorrupt)
	}
	off += n
	for i := uint64(0); i < count; i++ {
		e := &RawEntry{Version: version}
		newOff, err := c.decodeEntry(data, off, e)
		if err != nil {
			return fmt.Errorf("%w: entry %d: %v", ErrCatalogCorrupt, i, err)
		}
		c.cache[e.TableID] = e
		c.byName[e.Name] = e.TableID
		if e.TableID >= c.nextID {
			c.nextID = e.TableID + 1
		}
		off = newOff
	}
	if c.nextID == 0 {
		c.nextID = 1
	}
	return nil
}

// NextID reserves and returns the next free tableID.
func (c *Catalog) NextID() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
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

// PutRaw registers a new entry. The catalog is rewritten atomically.
func (c *Catalog) PutRaw(entry *RawEntry) error {
	if entry.Name == "" {
		return fmt.Errorf("%w: name is required", ErrCatalogCorrupt)
	}
	if entry.CreateSQL == "" {
		return fmt.Errorf("%w: CreateSQL is required", ErrCatalogCorrupt)
	}
	entry.Version = SchemaVersionCurrent

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return ErrCatalogClosed
	}
	if _, ok := c.byName[entry.Name]; ok {
		return fmt.Errorf("%w: name=%q", ErrCatalogExists, entry.Name)
	}
	if _, ok := c.cache[entry.TableID]; ok {
		return fmt.Errorf("%w: id=%d", ErrCatalogExists, entry.TableID)
	}
	c.cache[entry.TableID] = entry
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
	if c.closed.Load() {
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

// GetByID returns the entry for tableID.
func (c *Catalog) GetByID(tableID uint64) (*RawEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed.Load() {
		return nil, ErrCatalogClosed
	}
	entry, ok := c.cache[tableID]
	if !ok {
		return nil, fmt.Errorf("%w: id=%d", ErrCatalogNotFound, tableID)
	}
	cp := *entry
	return &cp, nil
}

// GetByIDRef returns a mutable reference to the cached entry for
// tableID. Caller must hold no locks. Used by subsystems that need
// to modify entries in-place before flushing (e.g., PutIndex, PutStats).
func (c *Catalog) GetByIDRef(tableID uint64) (*RawEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed.Load() {
		return nil, ErrCatalogClosed
	}
	entry, ok := c.cache[tableID]
	if !ok {
		return nil, fmt.Errorf("%w: id=%d", ErrCatalogNotFound, tableID)
	}
	return entry, nil
}

// ByName returns the entry for name.
func (c *Catalog) ByName(name string) (*RawEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed.Load() {
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
func (c *Catalog) List() []*RawEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*RawEntry, 0, len(c.cache))
	for _, e := range c.cache {
		cp := *e
		cp.Columns = append([]RawColumn(nil), e.Columns...)
		if e.Unique != nil {
			cp.Unique = append([]RawUnique(nil), e.Unique...)
			for i, u := range cp.Unique {
				cp.Unique[i] = RawUnique{Cols: append([]int(nil), u.Cols...)}
			}
		}
		out = append(out, &cp)
	}
	slices.SortFunc(out, func(a, b *RawEntry) int { return cmp.Compare(a.TableID, b.TableID) })
	return out
}

// Len returns the number of entries.
func (c *Catalog) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

// Close marks the catalog as closed. Safe to call multiple times.
func (c *Catalog) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed.Store(true)
	return nil
}

// Path returns the filesystem path to catalog.dat.
func (c *Catalog) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

// FlushLocked persists the catalog atomically. Caller must hold c.mu.
func (c *Catalog) FlushLocked() error {
	return c.flushLocked()
}

func (c *Catalog) flushLocked() error {
	var buf []byte
	buf = append(buf, CatalogMagic[:]...)
	buf = append(buf, SchemaVersionCurrent)
	buf = append(buf, 0, 0, 0, 0) // reserved
	var nxt [8]byte
	binary.BigEndian.PutUint64(nxt[:], c.nextID)
	buf = append(buf, nxt[:]...)
	ids := make([]uint64, 0, len(c.cache))
	for id := range c.cache {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	buf = binary.AppendUvarint(buf, uint64(len(ids)))
	for _, id := range ids {
		buf = c.encodeEntry(c.cache[id], buf)
	}
	tmpPath := c.path + CatalogTmpSuffix
	if err := os.WriteFile(tmpPath, buf, 0o644); err != nil {
		return err
	}
	f, err := os.Open(tmpPath)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if err := os.Rename(tmpPath, c.path); err != nil {
		return err
	}
	return nil
}

// Cache returns the internal cache map (read-only usage expected).
func (c *Catalog) Cache() map[uint64]*RawEntry {
	return c.cache
}

// ByNameMap returns the internal name→ID map.
func (c *Catalog) ByNameMap() map[string]uint64 {
	return c.byName
}

// NextIDVal returns the current nextID value without reserving.
func (c *Catalog) NextIDVal() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nextID
}

// SetNextID sets the nextID value (used by bootstrap).
func (c *Catalog) SetNextID(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID = id
}
