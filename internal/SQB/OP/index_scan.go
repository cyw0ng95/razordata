package OP

import (
	"bytes"
	"context"
	"fmt"
	"sync/atomic"

	id "github.com/cyw0ng95/razordata/internal/ENG/ID"
	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type IndexScan struct {
	table      string
	idx        string
	rangeStart []byte
	rangeEnd   []byte
	store      Store
	schema     *StoreSchema
	prefix     []byte
	it         interface {
		Next() bool
		Key() []byte
		Value() []byte
		Err() error
		Close() error
	}
	rows   []Row
	pos    int
	params []any

	// iter-22 secondary-index fields. When indexMode is true,
	// the scan uses a real index seek via the index keyspace
	// (rather than a full table prefix scan with a code-side
	// filter).
	indexMode     bool
	indexTableID  uint64
	indexName     string
	indexSeek     []byte
	indexRangeEnd []byte
	indexIt       interface {
		Next() bool
		Key() []byte
		Value() []byte
		Err() error
		Close() error
	}

	// iter-23 B-tree index fields. When btree is non-nil,
	// the scan uses the B-tree for index lookups.
	btree      *id.BTree
	btreeIt    *id.Cursor
	btreeStore Store

	// iter-27 (REQ000074) range-seek fields. When indexLower is
	// non-nil, the scan positions the index iterator at the
	// encoded lower bound. The iterator's seek is inclusive by
	// default; indexLowerExclusive makes it strictly greater
	// than the bound. indexUpper (when non-nil) caps the
	// indexed column value; indexUpperInclusive determines
	// whether the cap is exclusive (default) or inclusive.
	indexLower          []byte
	indexLowerExclusive bool
	indexUpper          []byte
	indexUpperInclusive bool

	// REQ000767: pre-computed index key prefix for range-seek
	// filtering, avoiding BuildIndexKey allocation per entry.
	prefixIdxKey []byte

	// REQ000790: index usage tracking for diagnostics.
	iu *DT.IndexUsage

	// REQ001042: batched context check counter.
	ctxCheckCounter int

	// REQ001108: residual predicates evaluated after the seek
	// but before the row is returned. nil/empty = no residual
	// filtering (hot path unchanged). The planner decomposes
	// an AND-of-multi-col WHERE into a single seek predicate
	// (on the indexed column) and one or more residuals (on
	// other columns) and pushes the residuals here so the
	// outer Filter is unnecessary.
	residual []PS.Expr

	// REQ001225: rawByteFilter is a predicate compiled from a filter
	// conjunct that can be evaluated on raw encoded bytes without
	// decoding the row. Set by NewFilter when pushdown is possible.
	rawByteFilter func([]byte) bool

	// REQ001226: lastDataSlice tracks the []Value from the previous
	// DecodeRow call so it can be returned to valueSlicePool on the
	// next iteration, eliminating per-row make([]Value, N) allocations.
	lastDataSlice []Value

	closed atomic.Bool
}

// SetRawByteFilter sets a raw-byte predicate filter. REQ001225.
func (i *IndexScan) SetRawByteFilter(f func([]byte) bool) {
	i.rawByteFilter = f
}

// WithParams propagates the bound `?` placeholders to this
// operator (R16-1..2).
func (i *IndexScan) WithParams(p []any) pl.Operator {
	i.params = p
	return i
}

func NewIndexScan(table, idx string, rangeStart, rangeEnd []byte) *IndexScan {
	return &IndexScan{
		table:      table,
		idx:        idx,
		rangeStart: rangeStart,
		rangeEnd:   rangeEnd,
	}
}

// NewIndexScanWithStore builds an IndexScan that reads through the engine.
// In v1 the planner selects IndexScan based on catalog presence; until
// ENG/ID/ lands, the scan performs a full prefix read against the
// memtable + SSTs and the planner's decision is the only signal that
// the column is indexed. Future work replaces this with a real index
// seek.
func NewIndexScanWithStore(store Store, table, idx string) (*IndexScan, error) {
	ss, ok := DT.SchemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &IndexScan{
		table:  table,
		idx:    idx,
		store:  store,
		schema: ss,
		prefix: TablePrefix(table),
	}, nil
}

// NewIndexScanWithIndex builds an IndexScan that uses a real secondary
// index seek (iter-22). The scan reads primary keys from the index
// store, then fetches the corresponding rows via Store.Get.
// seekValue is the index value to look up (exact match); if empty,
// the scan returns all rows in index order. rangeEnd, if non-nil,
// limits the scan to entries strictly less than this value
// (lexicographic).
// REQ000252 — secondary indexes MVP.
func NewIndexScanWithIndex(store Store, tableID uint64, table, idx string, seekValue, rangeEnd []byte) (*IndexScan, error) {
	ss, ok := DT.SchemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &IndexScan{
		table:         table,
		idx:           idx,
		store:         store,
		schema:        ss,
		prefix:        TablePrefix(table),
		indexMode:     true,
		indexTableID:  tableID,
		indexName:     idx,
		indexSeek:     seekValue,
		indexRangeEnd: rangeEnd,
	}, nil
}

// NewIndexScanWithBTree builds an IndexScan that uses a B-tree secondary
// index for lookups. The B-tree maps index values to primary keys.
func NewIndexScanWithBTree(bt *id.BTree, store Store, table, idx string) (*IndexScan, error) {
	ss, ok := DT.SchemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &IndexScan{
		table:  table,
		idx:    idx,
		store:  store,
		schema: ss,
		prefix: TablePrefix(table),
		btree:  bt,
	}, nil
}

// NewIndexScanWithRange builds an IndexScan that uses the secondary
// index keyspace for a real range seek. The lower bound is
// indexLower; lowerInclusive controls whether the bound is
// included. indexUpper is the (exclusive) upper bound; pass nil
// for an unbounded upper scan.
// REQ000074 (iter-27): real seek for `col > X`, `col >= X`,
// `col BETWEEN X AND Y`, etc. Replaces the prefix-scan fallback
// that the planner previously used for non-equality predicates.
// Implementation note: the iterator is opened with the BROAD
// index prefix (`__idx__:<tableID>:<idxName>:`) so it walks all
// index entries; the lower/upper bounds are enforced in
// `nextFromIndex` by inspecting the iterator's key. This avoids
// the problem of trying to express an exclusive lower bound as
// a byte prefix (which would require knowing the value's
// successor, impossible for variable-length strings).
func NewIndexScanWithRange(store Store, tableID uint64, table, idx string, lower []byte, lowerInclusive bool, upper []byte, upperInclusive bool) (*IndexScan, error) {
	ss, ok := DT.SchemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	if len(lower) == 0 {
		return nil, fmt.Errorf("op: IndexScan range requires non-empty lower bound")
	}
	// Convert the inclusive upper bound into the exclusive form
	// the iterator naturally understands. We append a 0x00 byte
	// so the lex comparison treats the original value as the
	// last entry to include. For `BETWEEN 3 AND 7` (inclusive),
	// this gives upper = 7 + "\x00", so `Compare(7, upper) = -1`
	// (still include) and `Compare(8, upper) = -1` (still
	// include) — wait, that's wrong because 8 > 7 but 8 < "7\x00".
	// Instead, the iterator comparison must happen in two steps:
	// include the bound when equal, stop when strictly greater.
	// That's exactly what `nextFromIndex` does:
	//   `bytes.Compare(idxValue, i.indexUpper) >= 0` returns NoRows.
	// So we need i.indexUpper to be the EXCLUSIVE upper bound, and
	// the inclusivity flag is encoded by adjusting the comparison.
	upperBound := upper
	_ = upperBound
	return &IndexScan{
		table:               table,
		idx:                 idx,
		store:               store,
		schema:              ss,
		prefix:              TablePrefix(table),
		indexMode:           true,
		indexTableID:        tableID,
		indexName:           idx,
		indexSeek:           lower,
		indexLower:          lower,
		indexLowerExclusive: !lowerInclusive,
		indexUpper:          upper,
		indexUpperInclusive: upperInclusive,
	}, nil
}

// NewIndexScanWithResidual returns an IndexScan built atop
// NewIndexScanWithIndex plus a slice of residual predicates
// evaluated after each row is produced. REQ001108. Residual
// filtering happens inside the scan's hot loop, so a
// non-matching row is silently skipped and the next index
// entry is consumed. The planner uses this to push non-index
// side conditions (e.g. `b > 10` on a table with index on `a`
// only) into the scan so the outer Filter is unnecessary.
func NewIndexScanWithResidual(store Store, tableID uint64, table, idx string, seekValue, rangeEnd []byte, residual []PS.Expr) (*IndexScan, error) {
	is, err := NewIndexScanWithIndex(store, tableID, table, idx, seekValue, rangeEnd)
	if err != nil {
		return nil, err
	}
	is.residual = residual
	return is, nil
}

// WithResidual attaches a residual predicate list to an
// existing IndexScan. Returns the scan for chaining. REQ001108.
func (i *IndexScan) WithResidual(residual []PS.Expr) *IndexScan {
	i.residual = residual
	return i
}

// Residual returns the residual predicate list. REQ001108.
func (i *IndexScan) Residual() []PS.Expr { return i.residual }

func (i *IndexScan) nextFromStore(ctx context.Context) (Row, error) {
	if i.indexMode {
		return i.nextFromIndex(ctx)
	}
	if i.it == nil {
		i.it = i.store.NewIterator(i.prefix)
	}
	if i.it == nil {
		return Row{}, ErrNoRows
	}
	for {
		if !i.it.Next() {
			break
		}
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		v := i.it.Value()
		// REQ001225: apply raw-byte filter before decoding.
		if i.rawByteFilter != nil && !i.rawByteFilter(v) {
			continue
		}
		row, err := DecodeRow(v, i.schema)
		if err != nil {
			return Row{}, err
		}
		// REQ001226: return previous slice to pool.
		if i.lastDataSlice != nil {
			DT.PutValueSlice(i.lastDataSlice)
			i.lastDataSlice = nil
		}
		i.lastDataSlice = row.Data
		row.TableName = i.table

		// REQ000790: record index usage for diagnostics.
		if i.iu != nil && i.idx != "" {
			i.iu.RecordIndexUse(i.idx, i.table)
		}

		// REQ001108: residual predicates apply on the prefix
		// path too, so a non-index-mode IndexScan with
		// residuals filters before returning.
		if !i.matchResidual(&row) {
			continue
		}
		return row, nil
	}
	if i.indexIt != nil {
		if err := i.indexIt.Err(); err != nil {
			return Row{}, err
		}
	}
	return Row{}, ErrNoRows
}

// nextFromIndex implements the index-seek path for NewIndexScanWithIndex.
// Reads index entries from the store, extracts the primary key, and fetches
// the corresponding row. REQ000847.
func (i *IndexScan) nextFromIndex(ctx context.Context) (Row, error) {
	// Lazily initialize the index iterator.
	if i.indexIt == nil {
		// REQ001035: cache the broad prefix for IndexValueFromKey.
		if i.prefixIdxKey == nil {
			i.prefixIdxKey = BuildIndexKey(i.indexTableID, i.idx, nil)
		}
		// For exact-match seeks (indexSeek without indexLower),
		// narrow the prefix to the seek value. For range scans
		// (indexLower/indexUpper), use the broad index prefix
		// and let the loop filter by lower/exclusive bounds.
		if i.indexSeek != nil && i.indexLower == nil {
			i.indexIt = i.store.NewIterator(BuildIndexKey(i.indexTableID, i.idx, i.indexSeek))
		} else {
			i.indexIt = i.store.NewIterator(i.prefixIdxKey)
		}
		if i.indexIt == nil {
			return Row{}, ErrNoRows
		}
	}
	for {
		if !i.indexIt.Next() {
			break
		}
		key := i.indexIt.Key()
		// Extract index value from the key for bound-checking.
		// REQ001035: use cached prefix to avoid BuildIndexKey allocation.
		idxVal := IndexValueFromKey(key, i.prefixIdxKey)
		if idxVal == nil {
			continue
		}
		// Check lower bound for range scans.
		if i.indexLower != nil {
			cmp := bytes.Compare(idxVal, i.indexLower)
			if cmp < 0 || (cmp == 0 && i.indexLowerExclusive) {
				continue
			}
		}
		// Check upper bound.
		if i.indexUpper != nil {
			cmp := bytes.Compare(idxVal, i.indexUpper)
			if cmp > 0 || (cmp == 0 && !i.indexUpperInclusive) {
				break
			}
		}
		// For exact-match seeks (no lower/upper bounds), filter
		// entries beyond the specific seek value.
		if i.indexSeek != nil && i.indexLower == nil && i.indexUpper == nil {
			if bytes.Compare(idxVal, i.indexSeek) > 0 {
				break
			}
		}
		// Extract primary key and fetch the row.
		pk := i.indexIt.Value()
		RowKey := append(append([]byte{}, i.prefix...), pk...)
		rowBytes, found, err := i.store.Get(RowKey)
		if err != nil {
			return Row{}, err
		}
		if !found {
			continue
		}
		// REQ001225: apply raw-byte filter before decoding.
		if i.rawByteFilter != nil && !i.rawByteFilter(rowBytes) {
			continue
		}
		row, err := DecodeRow(rowBytes, i.schema)
		if err != nil {
			return Row{}, err
		}
		// REQ001226: return previous slice to pool.
		if i.lastDataSlice != nil {
			DT.PutValueSlice(i.lastDataSlice)
			i.lastDataSlice = nil
		}
		i.lastDataSlice = row.Data
		row.TableName = i.table
		// REQ001108: residual predicates. Skip rows that don't
		// match; the next loop iteration fetches the next index
		// entry. With residual==nil this is a single bool check
		// and the row returns immediately.
		if !i.matchResidual(&row) {
			continue
		}
		return row, nil
	}
	return Row{}, ErrNoRows
}

// matchResidual evaluates every residual predicate against the
// given row and returns true iff all of them succeed. REQ001108.
// Short-circuits on the first false predicate. nil/empty
// residual is the hot path: returns true without allocation.
func (i *IndexScan) matchResidual(row *Row) bool {
	if len(i.residual) == 0 {
		return true
	}
	for _, e := range i.residual {
		v, err := EV.EvalValue(e, row, i.params)
		if err != nil {
			return false
		}
		switch v.Kind {
		case DT.KindNull:
			return false
		case DT.KindBool:
			if !v.Bo {
				return false
			}
		case DT.KindInt:
			if v.I64 == 0 {
				return false
			}
		case DT.KindFloat:
			if v.F64 == 0 {
				return false
			}
		case DT.KindText:
			if v.S == "" {
				return false
			}
		}
	}
	return true
}

// IndexValueFromKey moved to DT/storage.go; aliased in OP/store.go.

// openIndexIter returns the index iterator positioned at the
// configured seek. It uses the prefix-iter interface on the
// underlying store. For exact match, the prefix is the full
// index value; for prefix-match, it's a truncated value.
// REQ000074 (iter-27): for range seek (when indexLower or
// indexUpper is set), the iterator is opened with the BROAD
// index prefix (tableID + idxName only) and the bounds are
// enforced in nextFromIndex by inspecting the iterator's key.
// This is necessary because expressing an exclusive lower bound
// as a byte prefix is not generally possible.
func (i *IndexScan) openIndexIter() interface {
	Next() bool
	Key() []byte
	Value() []byte
	Err() error
	Close() error
} {
	var prefix []byte
	if i.indexLower != nil || i.indexUpper != nil {
		// Range seek: open with the broad index prefix so the
		// iterator walks all index entries; the lower/upper
		// bounds are enforced in nextFromIndex.
		prefix = BuildIndexKey(i.indexTableID, i.indexName, nil)
	} else {
		prefix = BuildIndexKey(i.indexTableID, i.indexName, i.indexSeek)
	}
	return i.store.NewIterator(prefix)
}

func (i *IndexScan) Next(ctx context.Context) (Row, error) {
	ec.BUG_ON(i.closed.Load(), "IndexScan.Next() after Close()")
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if i.btree != nil {
		return i.nextFromBTree(ctx)
	}
	if i.store != nil {
		return i.nextFromStore(ctx)
	}
	if i.rows == nil {
		DT.TablesMu.RLock()
		src := DT.Tables[i.table]
		out := make([]Row, len(src))
		for k, r := range src {
			out[k] = DT.CloneRow(r)
		}
		DT.TablesMu.RUnlock()
		i.rows = out
		i.pos = 0
	}
	if i.pos >= len(i.rows) {
		return Row{}, ErrNoRows
	}
	r := i.rows[i.pos]
	i.pos++
	r.TableName = i.table

	// REQ000790: record index usage for diagnostics.
	if i.iu != nil && i.idx != "" {
		i.iu.RecordIndexUse(i.idx, i.table)
	}

	return r, nil
}

func (i *IndexScan) Close() error {
	i.closed.Store(true)
	if i.it != nil {
		err := i.it.Close()
		i.it = nil
		if err != nil {
			return err
		}
	}
	if i.indexIt != nil {
		err := i.indexIt.Close()
		i.indexIt = nil
		if err != nil {
			return err
		}
	}
	i.btreeIt = nil
	i.pos = 0
	i.rows = nil
	// REQ001226: return any remaining pooled slice.
	if i.lastDataSlice != nil {
		DT.PutValueSlice(i.lastDataSlice)
		i.lastDataSlice = nil
	}
	return nil
}

// nextFromBTree reads index entries from the B-tree cursor,
// fetches the corresponding rows via Store.Get, and returns them.
func (i *IndexScan) nextFromBTree(ctx context.Context) (Row, error) {
	if i.btreeIt == nil {
		i.btreeIt = i.btree.Cursor()
		if len(i.indexSeek) > 0 {
			if !i.btreeIt.Seek(i.indexSeek) {
				return Row{}, ErrNoRows
			}
		} else {
			if !i.btreeIt.Seek([]byte{0}) {
				return Row{}, ErrNoRows
			}
		}
	}
	for i.btreeIt.Valid() {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		pk := i.btreeIt.Value()
		if len(i.indexRangeEnd) > 0 && bytes.Compare(pk, i.indexRangeEnd) >= 0 {
			return Row{}, ErrNoRows
		}
		rowKey := RowKey(i.prefix, pk)
		rowBytes, ok, err := i.store.Get(rowKey)
		if err != nil {
			return Row{}, err
		}
		if !ok {
			if !i.btreeIt.Next() {
				return Row{}, ErrNoRows
			}
			continue
		}
		row, err := DecodeRow(rowBytes, i.schema)
		if err != nil {
			return Row{}, err
		}
		// REQ001226: return previous slice to pool.
		if i.lastDataSlice != nil {
			DT.PutValueSlice(i.lastDataSlice)
			i.lastDataSlice = nil
		}
		i.lastDataSlice = row.Data
		row.TableName = i.table

		// REQ000790: record index usage for diagnostics.
		if i.iu != nil && i.idx != "" {
			i.iu.RecordIndexUse(i.idx, i.table)
		}

		i.btreeIt.Next()
		return row, nil
	}
	return Row{}, ErrNoRows
}

// BuildIndexKey and encodeUint64BE moved to DT/storage.go; aliased in
// OP/store.go via OP.BuildIndexKey var alias.

// Accessor methods for IndexScan fields used by EX plan_node and strategy.
func (i *IndexScan) Table() string           { return i.table }
func (i *IndexScan) Idx() string             { return i.idx }
func (i *IndexScan) Store() DT.Store         { return i.store }
func (i *IndexScan) Schema() *DT.StoreSchema { return i.schema }
func (i *IndexScan) Btree() *id.BTree        { return i.btree }
func (i *IndexScan) IndexMode() bool         { return i.indexMode }
func (i *IndexScan) IndexSeek() []byte       { return i.indexSeek }
func (i *IndexScan) IndexLower() []byte      { return i.indexLower }
func (i *IndexScan) IndexUpper() []byte      { return i.indexUpper }
func (i *IndexScan) Prefix() []byte          { return i.prefix }
func (i *IndexScan) PrefixIdxKey() []byte    { return i.prefixIdxKey }