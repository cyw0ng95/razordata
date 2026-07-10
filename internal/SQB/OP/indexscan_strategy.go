// Package EX IndexScan Strategy pattern.
//
// REQ000979: IndexScan previously carried 5 mutually exclusive
// operational modes as optional fields with mode-switching in
// Next(). This file extracts a ScanStrategy interface and a few
// concrete strategy types so the IndexScan can delegate to a
// single strategy instead of branching on a constellation of
// optional fields.
//
// The strategy types below are deliberately thin: they hold the
// per-mode state (iterator, seek value, B-tree, in-memory rows) and
// implement Next/Close uniformly. IndexScan retains its existing
// public API and constructors — the strategy is selected in the
// constructors, and the existing Next() code path is preserved
// for callers that wire directly to IndexScan.
//
// The adapter is the bridge: an IndexScan can be wrapped via
// AsStrategy() to expose itself as a ScanStrategy, which is what
// the planner/executor use when they want to compose strategies
// uniformly.
package OP

import (
	"context"
	"fmt"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	id "github.com/cyw0ng95/razordata/internal/ENG/ID"
)

// ScanStrategy is the interface every concrete index-scan strategy
// implements. IndexScan.Next() and the planner/executor dispatch
// through this interface so the underlying mode is hidden.
//
// REQ000979: replacing the god-struct's mode-switching with
// per-strategy implementations.
type ScanStrategy interface {
	Next(ctx context.Context) (pl.Row, error)
	Close() error
}

// InMemoryScan is the in-memory scan strategy. It iterates a
// previously-materialized []Row slice. No store, no iterator, no
// B-tree — the simplest of the strategies.
type InMemoryScan struct {
	rows []Row
	pos  int
}

// NewInMemoryScan builds an in-memory strategy over the given rows.
func NewInMemoryScan(rows []Row) *InMemoryScan {
	return &InMemoryScan{rows: rows}
}

// Next returns the next row from the in-memory slice, or ErrNoRows
// when exhausted. ctx cancellation is honored.
func (s *InMemoryScan) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if s.pos >= len(s.rows) {
		return Row{}, ErrNoRows
	}
	r := s.rows[s.pos]
	s.pos++
	return r, nil
}

// Close resets the iterator position so the strategy can be reused
// after plan cache hits. Without this, the second execution of a
// cached plan immediately returns ErrNoRows. REQ001162.
func (s *InMemoryScan) Close() error {
	s.pos = 0
	return nil
}

// StorePrefixScan is the store-prefix scan strategy. It opens a
// store iterator over a table prefix and decodes each value. This
// is the default strategy when no secondary index is available
// but the engine store is.
type StorePrefixScan struct {
	store         Store
	prefix        []byte
	schema        *StoreSchema
	it            storeIter
	lastDataSlice []Value // REQ001226: pooled []Value tracking
}

// storeIter is the interface StorePrefixScan expects from
// store.NewIterator(prefix). We re-declare it here to avoid a
// dependency on the concrete store type signature.
type storeIter interface {
	Next() bool
	Key() []byte
	Value() []byte
	Err() error
	Close() error
}

// NewStorePrefixScan builds a prefix-scan strategy over the given
// store, prefix, and schema.
func NewStorePrefixScan(store Store, prefix []byte, schema *StoreSchema) *StorePrefixScan {
	return &StorePrefixScan{store: store, prefix: prefix, schema: schema}
}

// Next returns the next decoded row from the prefix iterator.
func (s *StorePrefixScan) Next(ctx context.Context) (Row, error) {
	if s.it == nil {
		s.it = s.store.NewIterator(s.prefix)
		if s.it == nil {
			return Row{}, ErrNoRows
		}
	}
	if s.it.Next() {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		v := s.it.Value()
		row, err := DecodeRow(v, s.schema)
		if err != nil {
			return Row{}, err
		}
		// REQ001226: return previous slice to pool.
		if s.lastDataSlice != nil {
			DT.PutValueSlice(s.lastDataSlice)
			s.lastDataSlice = nil
		}
		s.lastDataSlice = row.Data
		return row, nil
	}
	return Row{}, ErrNoRows
}

// Close releases the underlying iterator.
func (s *StorePrefixScan) Close() error {
	var err error
	if s.it != nil {
		err = s.it.Close()
		s.it = nil
	}
	// REQ001226: return any remaining pooled slice.
	if s.lastDataSlice != nil {
		DT.PutValueSlice(s.lastDataSlice)
		s.lastDataSlice = nil
	}
	return err
}

// IndexSeekScan is the secondary-index seek strategy. It reads
// index entries from the store and fetches the corresponding rows
// via store.Get. Supports exact-match (seekValue) and range seeks
// (rangeLower, rangeUpper).
type IndexSeekScan struct {
	store            Store
	schema           *StoreSchema
	prefix           []byte
	indexTableID     uint64
	indexName        string
	seekValue        []byte
	rangeLower       []byte
	rangeLowerExcl   bool
	rangeUpper       []byte
	rangeUpperIncl   bool
	prefixIdxKey     []byte
	it               storeIter
	lastDataSlice    []Value // REQ001226: pooled []Value tracking
}

// NewIndexSeekScan builds an index-seek strategy. seekValue is the
// exact-match value; rangeLower/rangeUpper (with exclusivity flags)
// bound a range scan. When both are nil, the strategy returns all
// rows in index order. REQ000847: replaces the prefix-scan fallback
// with a real index seek.
func NewIndexSeekScan(store Store, prefix []byte, schema *StoreSchema, tableID uint64, idxName string, seekValue, rangeLower []byte, rangeLowerExcl bool, rangeUpper []byte, rangeUpperIncl bool) *IndexSeekScan {
	return &IndexSeekScan{
		store:          store,
		prefix:         prefix,
		schema:         schema,
		indexTableID:   tableID,
		indexName:      idxName,
		seekValue:      seekValue,
		rangeLower:     rangeLower,
		rangeLowerExcl: rangeLowerExcl,
		rangeUpper:     rangeUpper,
		rangeUpperIncl: rangeUpperIncl,
	}
}

// Next reads the next index entry within the configured bounds
// and fetches the corresponding row from the store.
func (s *IndexSeekScan) Next(ctx context.Context) (Row, error) {
	if s.it == nil {
		if s.prefixIdxKey == nil {
			s.prefixIdxKey = BuildIndexKey(s.indexTableID, s.indexName, nil)
		}
		// For exact-match seeks (seekValue without rangeLower),
		// narrow the prefix to the seek value. For range scans
		// (rangeLower/rangeUpper), use the broad index prefix and
		// let the loop filter by lower/exclusive bounds.
		if s.seekValue != nil && s.rangeLower == nil {
			s.it = s.store.NewIterator(BuildIndexKey(s.indexTableID, s.indexName, s.seekValue))
		} else {
			s.it = s.store.NewIterator(s.prefixIdxKey)
		}
		if s.it == nil {
			return Row{}, ErrNoRows
		}
	}
	for {
		if !s.it.Next() {
			return Row{}, ErrNoRows
		}
		key := s.it.Key()
		idxVal := IndexValueFromKey(key, s.prefixIdxKey)
		if idxVal == nil {
			continue
		}
		if s.rangeLower != nil {
			cmp := bytesCompare(idxVal, s.rangeLower)
			if cmp < 0 || (cmp == 0 && s.rangeLowerExcl) {
				continue
			}
		}
		if s.rangeUpper != nil {
			cmp := bytesCompare(idxVal, s.rangeUpper)
			if cmp > 0 || (cmp == 0 && !s.rangeUpperIncl) {
				return Row{}, ErrNoRows
			}
		}
		if s.seekValue != nil && s.rangeLower == nil && s.rangeUpper == nil {
			if bytesCompare(idxVal, s.seekValue) > 0 {
				return Row{}, ErrNoRows
			}
		}
		pk := s.it.Value()
		rowKey := append(append([]byte{}, s.prefix...), pk...)
		rowBytes, found, err := s.store.Get(rowKey)
		if err != nil {
			return Row{}, err
		}
		if !found {
			continue
		}
		row, err := DecodeRow(rowBytes, s.schema)
		if err != nil {
			return Row{}, err
		}
		// REQ001226: return previous slice to pool.
		if s.lastDataSlice != nil {
			DT.PutValueSlice(s.lastDataSlice)
			s.lastDataSlice = nil
		}
		s.lastDataSlice = row.Data
		return row, nil
	}
}

// Close releases the index iterator.
func (s *IndexSeekScan) Close() error {
	var err error
	if s.it != nil {
		err = s.it.Close()
		s.it = nil
	}
	// REQ001226: return any remaining pooled slice.
	if s.lastDataSlice != nil {
		DT.PutValueSlice(s.lastDataSlice)
		s.lastDataSlice = nil
	}
	return err
}

// BTreeScan is the B-tree-backed scan strategy. It uses an
// in-memory B-tree to look up primary keys and fetches the
// corresponding rows from the store.
type BTreeScan struct {
	btree         *id.BTree
	store         Store
	prefix        []byte
	schema        *StoreSchema
	cursor        *id.Cursor
	done          bool
	lastDataSlice []Value // REQ001226: pooled []Value tracking
}

// NewBTreeScan builds a B-tree-backed scan strategy.
func NewBTreeScan(bt *id.BTree, store Store, prefix []byte, schema *StoreSchema) *BTreeScan {
	return &BTreeScan{btree: bt, store: store, prefix: prefix, schema: schema}
}

// Next returns the next row from the B-tree. When the B-tree
// signals a key whose value is not in the store, the strategy
// skips and continues. (See IndexScan.nextFromBTree for the
// original logic; the strategy mirrors it for callers that route
// through ScanStrategy.)
func (s *BTreeScan) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if s.done {
		return Row{}, ErrNoRows
	}
	if s.cursor == nil {
		s.cursor = s.btree.Cursor()
		if !s.cursor.Seek([]byte{0}) {
			s.done = true
			return Row{}, ErrNoRows
		}
	}
	for s.cursor.Valid() {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		pk := s.cursor.Value()
		rowKey := append(append([]byte{}, s.prefix...), pk...)
		rowBytes, found, err := s.store.Get(rowKey)
		if err != nil {
			return Row{}, err
		}
		if !found {
			if !s.cursor.Next() {
				return Row{}, ErrNoRows
			}
			continue
		}
		row, err := DecodeRow(rowBytes, s.schema)
		if err != nil {
			return Row{}, err
		}
		// REQ001226: return previous slice to pool.
		if s.lastDataSlice != nil {
			DT.PutValueSlice(s.lastDataSlice)
			s.lastDataSlice = nil
		}
		s.lastDataSlice = row.Data
		s.cursor.Next()
		return row, nil
	}
	return Row{}, ErrNoRows
}

// Close releases the B-tree cursor.
func (s *BTreeScan) Close() error {
	s.cursor = nil
	s.done = true
	// REQ001226: return any remaining pooled slice.
	if s.lastDataSlice != nil {
		DT.PutValueSlice(s.lastDataSlice)
		s.lastDataSlice = nil
	}
	return nil
}

// RangeSeekScan is the secondary-index range-seek strategy. It is
// structurally identical to IndexSeekScan (both read from the
// secondary index keyspace) but is selected when the planner
// wants to emphasize the range semantics. The two strategies are
// kept as distinct types so callers that need to identify the
// mode (e.g. for diagnostics) can do so via type assertion.
type RangeSeekScan = IndexSeekScan

// NewRangeSeekScan is a convenience constructor for range-seek
// strategies. It is a thin alias for NewIndexSeekScan with
// seekValue=nil.
func NewRangeSeekScan(store Store, prefix []byte, schema *StoreSchema, tableID uint64, idxName string, lower []byte, lowerIncl bool, upper []byte, upperIncl bool) *RangeSeekScan {
	return NewIndexSeekScan(store, prefix, schema, tableID, idxName, nil, lower, !lowerIncl, upper, upperIncl)
}

// SelectStrategy picks the right ScanStrategy for an IndexScan
// based on the configured mode. This is the dispatch the
// REQ000979 god-struct refactor centralizes.
//
// REQ000979: strategy selection rules —
//   1. B-tree present → BTreeScan (if NewIndexScanWithBTree was used).
//   2. indexMode (NewIndexScanWithIndex / NewIndexScanWithRange) → IndexSeekScan.
//   3. store present → StorePrefixScan (NewIndexScanWithStore).
//   4. otherwise → InMemoryScan (NewIndexScan).
func SelectStrategy(scan *IndexScan) (ScanStrategy, error) {
	if scan == nil {
		return nil, fmt.Errorf("SelectStrategy: nil IndexScan")
	}
	switch {
	case scan.btree != nil:
		return NewBTreeScan(scan.btree, scan.store, scan.prefix, scan.schema), nil
	case scan.indexMode:
		// For exact-match seeks without bounds, treat as seek.
		// For range seeks with bounds, use lower/upper.
		lower := scan.indexLower
		lowerExcl := scan.indexLowerExclusive
		upper := scan.indexUpper
		upperIncl := scan.indexUpperInclusive
		if lower == nil && upper == nil {
			// Exact match (NewIndexScanWithIndex).
			return NewIndexSeekScan(scan.store, scan.prefix, scan.schema, scan.indexTableID, scan.indexName, scan.indexSeek, nil, false, nil, false), nil
		}
		// Range seek.
		return NewIndexSeekScan(scan.store, scan.prefix, scan.schema, scan.indexTableID, scan.indexName, nil, lower, lowerExcl, upper, upperIncl), nil
	case scan.store != nil:
		return NewStorePrefixScan(scan.store, scan.prefix, scan.schema), nil
	default:
		// Materialize the in-memory rows the first time and
		// wrap them. IndexScan lazily builds i.rows on first
		// Next() — replicate that here.
		rows := scan.ensureInMemoryRows()
		return NewInMemoryScan(rows), nil
	}
}

// ensureInMemoryRows materializes the table's rows from the
// in-memory DT.Tables map. Mirrors the lazy init in IndexScan.Next.
func (i *IndexScan) ensureInMemoryRows() []Row {
	if i.rows != nil {
		return i.rows
	}
	DT.TablesMu.RLock()
	src := DT.Tables[i.table]
	if tempRows, ok := DT.TempTables[i.table]; ok {
		src = tempRows
	}
	out := make([]Row, len(src))
	for k, r := range src {
		out[k] = DT.CloneRow(r)
	}
	DT.TablesMu.RUnlock()
	i.rows = out
	i.pos = 0
	return out
}

// bytesCompare is a small inline wrapper around bytes.Compare.
// Re-exported here so this file has no extra import.
func bytesCompare(a, b []byte) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	for i := 0; i < len(a); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
