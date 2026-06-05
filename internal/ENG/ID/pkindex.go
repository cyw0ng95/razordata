// Package ID: pkindex.go is the LSM-backed primary key index.
//
// The index stores one entry per (tableID, primary-key-value) pair.
// The key is "__pk__:<tableID>:<encoded-pk>"; the value is the
// pointer to the row in the main LSM tree (the row's storage key,
// same format as rowKey in SQL/EX/store.go).
//
// The index is implemented as a thin wrapper over a byte Store.
// All read and write paths go through the underlying Store, so the
// index inherits the engine's durability, atomicity, and
// concurrency properties.
//
// Range scans iterate the "__pk__:<tableID>:" prefix; the encoded
// primary key forms the suffix in sorted order, so Seek and
// Range return entries in PK order.
package id

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
	"github.com/cyw0ng95/razordata/internal/ENG/TB"
)

// pkPrefix is the LSM key prefix reserved for primary-key index
// entries. The TB cluster's reserved prefixes
// ("__catalog__:", "__catalog_name__:", "__catalog_meta__:nextID")
// must not collide with this.
const pkPrefix = "__pk__:"

// Store is the minimal byte-store surface the index needs. The
// production implementation is the LS engine; tests use the
// MapStore from ENG/TB.
type Store interface {
	Insert(key, value []byte) error
	Get(key []byte) ([]byte, error) // returns ErrNotFound for missing
	Delete(key []byte) error
	NewIterator(prefix []byte) tb.Iterator
}

// ErrInvalidTableID is returned by PKIndex methods when the tableID
// is zero (the "no table" sentinel). PKIndex never accepts id 0.
var ErrInvalidTableID = errors.New("id: invalid tableID")

// PKIndex is the primary-key index. It is goroutine-safe; the
// underlying Store handles its own concurrency.
type PKIndex struct {
	store Store
}

// NewPKIndex returns a PKIndex backed by store. The index has no
// in-memory state; all operations go through the store.
func NewPKIndex(store Store) *PKIndex {
	return &PKIndex{store: store}
}

// Insert records the (tableID, primaryKey) -> rowPointer mapping.
// The rowPointer is the row's storage key in the main LSM tree
// (typically the same byte slice produced by rowKey in the SQL
// executor). The caller is responsible for ensuring the row
// itself is written; the PK index is a separate lookup structure
// for the row's location.
func (idx *PKIndex) Insert(tableID uint64, types []sc.ColumnType, values [][]byte, rowPointer []byte) error {
	if tableID == 0 {
		return ErrInvalidTableID
	}
	if len(types) != len(values) {
		return ErrKeyCodecColumnCount
	}
	enc, err := EncodeKey(types, values)
	if err != nil {
		return err
	}
	key := pkKey(tableID, enc)
	return idx.store.Insert(key, append([]byte(nil), rowPointer...))
}

// Delete removes the (tableID, primaryKey) entry. A delete on a
// missing key is a no-op (Store.Delete semantics). Returns nil on
// success.
func (idx *PKIndex) Delete(tableID uint64, types []sc.ColumnType, values [][]byte) error {
	if tableID == 0 {
		return ErrInvalidTableID
	}
	if len(types) != len(values) {
		return ErrKeyCodecColumnCount
	}
	enc, err := EncodeKey(types, values)
	if err != nil {
		return err
	}
	return idx.store.Delete(pkKey(tableID, enc))
}

// Seek performs an exact point-lookup on (tableID, primaryKey).
// Returns the row pointer on hit, or (nil, false, nil) on miss.
// The returned slice is owned by the caller; modifying it does
// not affect the index.
func (idx *PKIndex) Seek(tableID uint64, types []sc.ColumnType, values [][]byte) ([]byte, bool, error) {
	if tableID == 0 {
		return nil, false, ErrInvalidTableID
	}
	if len(types) != len(values) {
		return nil, false, ErrKeyCodecColumnCount
	}
	enc, err := EncodeKey(types, values)
	if err != nil {
		return nil, false, err
	}
	val, err := idx.store.Get(pkKey(tableID, enc))
	if err != nil {
		if errors.Is(err, tb.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return val, true, nil
}

// Range returns a streaming iterator over the PK index entries
// in the half-open range [lo, hi) for the given tableID. The
// caller must Close the returned iterator. The lo and hi
// arguments may be nil to mean "unbounded below" or "unbounded
// above" respectively.
//
// Each iterator step yields the raw row pointer (the value the
// caller previously Inserted). The encoded primary key is not
// exposed; the caller knows the keys it's scanning.
//
// Note: for "WHERE pk BETWEEN x AND y", the caller passes x and
// y as lo and hi. The hi is exclusive in this API because that
// is the natural shape of the underlying Store's prefix iterator.
// The caller adds 1 to a "closed" upper bound if needed.
func (idx *PKIndex) Range(tableID uint64, types []sc.ColumnType, lo, hi [][]byte) (tb.Iterator, error) {
	if tableID == 0 {
		return nil, ErrInvalidTableID
	}
	prefix := pkPrefixFor(tableID)
	src := idx.store.NewIterator(prefix)
	return &rangeIter{src: src, tableID: tableID, lower: lo, upper: hi, types: types}, nil
}

// rangeIter wraps a Store iterator and filters out keys outside
// the optional [lower, upper) bounds. It is returned by
// PKIndex.Range.
type rangeIter struct {
	src     tb.Iterator
	tableID uint64
	lower   [][]byte
	upper   [][]byte
	types   []sc.ColumnType
	curKey  []byte
	curVal  []byte
	closed  bool
}

func (it *rangeIter) Next() bool {
	if it.closed {
		return false
	}
	for it.src.Next() {
		key := it.src.Key()
		val := it.src.Value()
		suffix := key[len(pkPrefix)+8:]
		dec, err := DecodeKey(suffix, it.types)
		if err != nil {
			// Malformed entry; skip it.
			continue
		}
		if it.lower != nil && compareKeys(dec, it.lower) < 0 {
			continue
		}
		if it.upper != nil && compareKeys(dec, it.upper) >= 0 {
			return false
		}
		it.curKey = append([]byte(nil), key...)
		it.curVal = append([]byte(nil), val...)
		return true
	}
	return false
}

func (it *rangeIter) Key() []byte   { return it.curKey }
func (it *rangeIter) Value() []byte { return it.curVal }
func (it *rangeIter) Err() error    { return it.src.Err() }
func (it *rangeIter) Close() error  { it.closed = true; return it.src.Close() }

// compareKeys compares two slices of PK values lexicographically.
// Returns -1 if a < b, 0 if equal, +1 if a > b. Used by Range to
// filter out keys past the upper bound.
func compareKeys(a, b [][]byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		c := bytesCompare(a[i], b[i])
		if c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func bytesCompare(a, b []byte) int {
	la, lb := len(a), len(b)
	n := la
	if lb < n {
		n = lb
	}
	for i := 0; i < n; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case la < lb:
		return -1
	case la > lb:
		return 1
	default:
		return 0
	}
}

func pkKey(tableID uint64, encodedPK []byte) []byte {
	out := make([]byte, 0, len(pkPrefix)+8+len(encodedPK))
	out = append(out, pkPrefix...)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], tableID)
	out = append(out, buf[:]...)
	out = append(out, encodedPK...)
	return out
}

func pkPrefixFor(tableID uint64) []byte {
	out := make([]byte, 0, len(pkPrefix)+8)
	out = append(out, pkPrefix...)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], tableID)
	return append(out, buf[:]...)
}

// String renders a human-readable form of the index entry. Used
// for debugging.
func (idx *PKIndex) String(tableID uint64, types []sc.ColumnType, values [][]byte) string {
	enc, err := EncodeKey(types, values)
	if err != nil {
		return fmt.Sprintf("PKIndex(tableID=%d, err=%v)", tableID, err)
	}
	return fmt.Sprintf("PKIndex(tableID=%d, encoded=%x)", tableID, enc)
}
