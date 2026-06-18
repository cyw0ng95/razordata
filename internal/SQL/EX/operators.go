package EX

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	id "github.com/cyw0ng95/razordata/internal/ENG/ID"
)

// ErrTableNotRegisteredForStorage is returned when an operator is asked to
// route through the storage engine for a table that has not been registered
// in the in-memory catalog. Callers can match it with errors.Is and inspect
// the table name via the wrapped error.
var ErrTableNotRegisteredForStorage = errors.New("ex: table not registered for storage")

var ErrNoPKForStorage = errors.New("ex: cannot write to storage without a primary key")

type SeqScan struct {
	table  string
	store  Store
	schema *storeSchema
	prefix []byte
	it     interface {
		Next() bool
		Key() []byte
		Value() []byte
		Err() error
		Close() error
	}
	// in-memory fallback
	rows []Row
	pos  int
	// params are the bound `?` placeholders (R16-1..2). SeqScan
	// itself does not evaluate expressions, but child operators
	// (Filter/Project) need access. Stored here so WithParams
	// can propagate it down through the tree at executor
	// construction time.
	params []interface{}
	// planner is set by the executor's main plan so the rows
	// produced by SeqScan carry it through to the Filter,
	// Project, and (importantly) subquery eval sites.
	// See REQ000366.
	planner *Planner
	// currentKey is the raw key from the LSM iterator for the
	// most recently decoded row. Preserved so Update/Delete
	// can reuse the original row key for hidden-PK tables.
	currentKey []byte
}

// WithParams propagates the bound `?` placeholders to this
// operator (R16-1..2). Returns the receiver for chaining.
func (s *SeqScan) WithParams(p []interface{}) Operator {
	s.params = p
	return s
}

// WithPlanner attaches the main-plan planner to rows produced by
// this SeqScan. REQ000366.
func (s *SeqScan) WithPlanner(p *Planner) Operator {
	s.planner = p
	return s
}

func NewSeqScan(table string) *SeqScan {
	return &SeqScan{table: table}
}

// NewSeqScanWithStore builds a SeqScan that reads from the engine instead
// of the in-memory table registry. The schema must have been registered.
func NewSeqScanWithStore(store Store, table string) (*SeqScan, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &SeqScan{
		table:  table,
		store:  store,
		schema: ss,
		prefix: tablePrefix(table),
	}, nil
}

func (s *SeqScan) snapshot() []Row {
	tablesMu.RLock()
	defer tablesMu.RUnlock()
	src := tables[s.table]
	out := make([]Row, len(src))
	for i, r := range src {
		out[i] = cloneRow(r)
	}
	return out
}

func (s *SeqScan) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if s.store != nil {
		return s.nextFromStore(ctx)
	}
	if s.rows == nil {
		s.rows = s.snapshot()
		s.pos = 0
	}
	if s.pos >= len(s.rows) {
		return Row{}, ErrNoRows
	}
	r := s.rows[s.pos]
	s.pos++
	if s.planner != nil {
		r.planner = s.planner
	}
	return r, nil
}

func (s *SeqScan) nextFromStore(ctx context.Context) (Row, error) {
	if s.it == nil {
		s.it = s.store.NewIterator(s.prefix)
	}
	if s.it == nil {
		return Row{}, ErrNoRows
	}
	if s.it.Next() {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		// REQ000501: save the raw key so Update/Delete can
		// preserve the original row key for hidden-PK tables.
		s.currentKey = s.it.Key()
		v := s.it.Value()
		row, err := decodeRow(v, s.schema)
		if err != nil {
			return Row{}, err
		}
		row.storeKey = s.currentKey
		// REQ000366: thread the planner so subquery evals see
		// the same store/catalog.
		if s.planner != nil {
			row.planner = s.planner
		}
		return row, nil
	}
	if err := s.it.Err(); err != nil {
		return Row{}, err
	}
	return Row{}, ErrNoRows
}

func (s *SeqScan) Close() error {
	if s.it != nil {
		err := s.it.Close()
		s.it = nil
		return err
	}
	s.pos = 0
	s.rows = nil
	return nil
}

type IndexScan struct {
	table      string
	idx        string
	rangeStart []byte
	rangeEnd   []byte
	store      Store
	schema     *storeSchema
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
	params []interface{}

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
}

// WithParams propagates the bound `?` placeholders to this
// operator (R16-1..2).
func (i *IndexScan) WithParams(p []interface{}) Operator {
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
	ss, ok := schemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &IndexScan{
		table:  table,
		idx:    idx,
		store:  store,
		schema: ss,
		prefix: tablePrefix(table),
	}, nil
}

// NewIndexScanWithIndex builds an IndexScan that uses a real secondary
// index seek (iter-22). The scan reads primary keys from the index
// store, then fetches the corresponding rows via Store.Get.
//
// seekValue is the index value to look up (exact match); if empty,
// the scan returns all rows in index order. rangeEnd, if non-nil,
// limits the scan to entries strictly less than this value
// (lexicographic).
//
// REQ000252 — secondary indexes MVP.
func NewIndexScanWithIndex(store Store, tableID uint64, table, idx string, seekValue, rangeEnd []byte) (*IndexScan, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &IndexScan{
		table:         table,
		idx:           idx,
		store:         store,
		schema:        ss,
		prefix:        tablePrefix(table),
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
	ss, ok := schemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &IndexScan{
		table:  table,
		idx:    idx,
		store:  store,
		schema: ss,
		prefix: tablePrefix(table),
		btree:  bt,
	}, nil
}

// NewIndexScanWithRange builds an IndexScan that uses the secondary
// index keyspace for a real range seek. The lower bound is
// indexLower; lowerInclusive controls whether the bound is
// included. indexUpper is the (exclusive) upper bound; pass nil
// for an unbounded upper scan.
//
// REQ000074 (iter-27): real seek for `col > X`, `col >= X`,
// `col BETWEEN X AND Y`, etc. Replaces the prefix-scan fallback
// that the planner previously used for non-equality predicates.
//
// Implementation note: the iterator is opened with the BROAD
// index prefix (`__idx__:<tableID>:<idxName>:`) so it walks all
// index entries; the lower/upper bounds are enforced in
// `nextFromIndex` by inspecting the iterator's key. This avoids
// the problem of trying to express an exclusive lower bound as
// a byte prefix (which would require knowing the value's
// successor, impossible for variable-length strings).
func NewIndexScanWithRange(store Store, tableID uint64, table, idx string, lower []byte, lowerInclusive bool, upper []byte, upperInclusive bool) (*IndexScan, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	if len(lower) == 0 {
		return nil, fmt.Errorf("ex: IndexScan range requires non-empty lower bound")
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
		prefix:              tablePrefix(table),
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

func (i *IndexScan) nextFromStore(ctx context.Context) (Row, error) {
	// Index-seek path (iter-22 secondary indexes).
	if i.indexMode {
		return i.nextFromIndex(ctx)
	}
	if i.it == nil {
		i.it = i.store.NewIterator(i.prefix)
	}
	if i.it == nil {
		return Row{}, ErrNoRows
	}
	if i.it.Next() {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		v := i.it.Value()
		row, err := decodeRow(v, i.schema)
		if err != nil {
			return Row{}, err
		}
		return row, nil
	}
	if err := i.it.Err(); err != nil {
		return Row{}, err
	}
	return Row{}, ErrNoRows
}

// nextFromIndex is the iter-22 secondary-index seek path. It reads
// primary keys from the index iterator, then fetches the full row
// via Store.Get.
func (i *IndexScan) nextFromIndex(ctx context.Context) (Row, error) {
	if i.indexIt == nil {
		// Lazy open: scan all entries in the index matching the
		// seek prefix. For exact-match, the seek value is the
		// full index value. For prefix-match, it's a truncated
		// value.
		i.indexIt = i.openIndexIter()
	}
	for i.indexIt.Next() {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		// REQ000074 (iter-27): range-seek bounds on the indexed
		// column value. The iterator is opened with the broad
		// index prefix (tableID + idxName) when range seek is in
		// use, so we filter the key here.
		if i.indexLower != nil || i.indexUpper != nil {
			k := i.indexIt.Key()
			idxValue := indexValueFromKey(k, i.indexTableID, i.indexName)
			if idxValue == nil {
				// Key is not part of this index; skip.
				continue
			}
			if i.indexLower != nil {
				cmp := bytes.Compare(idxValue, i.indexLower)
				if i.indexLowerExclusive {
					if cmp <= 0 {
						continue
					}
				} else {
					if cmp < 0 {
						continue
					}
				}
			}
			if i.indexUpper != nil {
				cmp := bytes.Compare(idxValue, i.indexUpper)
				if i.indexUpperInclusive {
					if cmp > 0 {
						return Row{}, ErrNoRows
					}
				} else {
					if cmp >= 0 {
						return Row{}, ErrNoRows
					}
				}
			}
		}
		pk := i.indexIt.Value()
		// Optional range-end cap on PK (existing behavior)
		if len(i.indexRangeEnd) > 0 && bytes.Compare(pk, i.indexRangeEnd) >= 0 {
			return Row{}, ErrNoRows
		}
		// Fetch the row by primary key
		rowKey := rowKey(i.prefix, pk)
		rowBytes, ok, err := i.store.Get(rowKey)
		if err != nil {
			return Row{}, err
		}
		if !ok {
			// Stale index entry: row was deleted but index
			// not yet cleaned up. Skip.
			continue
		}
		row, err := decodeRow(rowBytes, i.schema)
		if err != nil {
			return Row{}, err
		}
		return row, nil
	}
	if err := i.indexIt.Err(); err != nil {
		return Row{}, err
	}
	return Row{}, ErrNoRows
}

// indexValueFromKey strips the index prefix
// `__idx__:<tableID>:<idxName>:` from the key and returns the
// remaining bytes (the indexed column value). Returns nil if the
// key does not start with the expected prefix.
func indexValueFromKey(key []byte, tableID uint64, idxName string) []byte {
	expected := buildIndexKey(tableID, idxName, nil)
	if len(key) < len(expected) {
		return nil
	}
	if !bytes.Equal(key[:len(expected)], expected) {
		return nil
	}
	return key[len(expected):]
}

// openIndexIter returns the index iterator positioned at the
// configured seek. It uses the prefix-iter interface on the
// underlying store. For exact match, the prefix is the full
// index value; for prefix-match, it's a truncated value.
//
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
		prefix = buildIndexKey(i.indexTableID, i.indexName, nil)
	} else {
		prefix = buildIndexKey(i.indexTableID, i.indexName, i.indexSeek)
	}
	return i.store.NewIterator(prefix)
}

func (i *IndexScan) Next(ctx context.Context) (Row, error) {
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
		tablesMu.RLock()
		src := tables[i.table]
		out := make([]Row, len(src))
		for k, r := range src {
			out[k] = cloneRow(r)
		}
		tablesMu.RUnlock()
		i.rows = out
		i.pos = 0
	}
	if i.pos >= len(i.rows) {
		return Row{}, ErrNoRows
	}
	r := i.rows[i.pos]
	i.pos++
	return r, nil
}

func (i *IndexScan) Close() error {
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
		rowKey := rowKey(i.prefix, pk)
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
		row, err := decodeRow(rowBytes, i.schema)
		if err != nil {
			return Row{}, err
		}
		i.btreeIt.Next()
		return row, nil
	}
	return Row{}, ErrNoRows
}

// buildIndexKey synthesizes the index keyspace prefix for use with
// Store.NewIterator. The full key is:
//
//	"__idx__:" + tableID(u64, BE) + ":" + indexName + ":" + indexValue
//
// iter-22 secondary indexes MVP.
func buildIndexKey(tableID uint64, indexName string, indexValue []byte) []byte {
	out := make([]byte, 0, 32+len(indexName)+len(indexValue))
	out = append(out, "__idx__:"...)
	encodeUint64BE(&out, tableID)
	out = append(out, ':')
	out = append(out, indexName...)
	out = append(out, ':')
	out = append(out, indexValue...)
	return out
}

// encodeUint64BE writes v big-endian into *buf.
func encodeUint64BE(buf *[]byte, v uint64) {
	var b [8]byte
	b[7] = byte(v)
	b[6] = byte(v >> 8)
	b[5] = byte(v >> 16)
	b[4] = byte(v >> 24)
	b[3] = byte(v >> 32)
	b[2] = byte(v >> 40)
	b[1] = byte(v >> 48)
	b[0] = byte(v >> 56)
	*buf = append(*buf, b[:]...)
}
