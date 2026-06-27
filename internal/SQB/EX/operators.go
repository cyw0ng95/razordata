package EX

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	id "github.com/cyw0ng95/razordata/internal/ENG/ID"
)

// ErrTableNotRegisteredForStorage is returned when an operator is asked to
// route through the storage engine for a table that has not been registered
// in the in-memory catalog. Callers can match it with errors.Is and inspect
// the table name via the wrapped error.
var ErrTableNotRegisteredForStorage = errors.New("ex: table not registered for storage")

var ErrNoPKForStorage = errors.New("ex: cannot write to storage without a primary key")

// tableSchemaCache caches shared column metadata per table to avoid
// rebuilding Cols, Types, and colIndex on every SeqScan.snapshot() call.
type tableSchemaEntry struct {
	cols     []string
	types    []int
	colIndex map[string]int
}

var (
	tableSchemaMu sync.RWMutex
	tableSchemaCache = map[string]*tableSchemaEntry{}
)

func getTableSchema(table string, src []Row) *tableSchemaEntry {
	if len(src) == 0 {
		return nil
	}
	// Fast path: read lock.
	tableSchemaMu.RLock()
	entry, ok := tableSchemaCache[table]
	tableSchemaMu.RUnlock()
	if ok {
		return entry
	}
	// Slow path: write lock and build.
	tableSchemaMu.Lock()
	defer tableSchemaMu.Unlock()
	// Double-check after acquiring write lock.
	if entry, ok = tableSchemaCache[table]; ok {
		return entry
	}
	cols := append([]string(nil), src[0].Cols...)
	var types []int
	if len(src[0].Types) > 0 {
		types = append([]int(nil), src[0].Types...)
	}
	colIndex := make(map[string]int, len(cols))
	for i, c := range cols {
		colIndex[strings.ToLower(c)] = i
	}
	entry = &tableSchemaEntry{cols: cols, types: types, colIndex: colIndex}
	tableSchemaCache[table] = entry
	return entry
}

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
	params []any
	// planner is set by the executor's main plan so the rows
	// produced by SeqScan carry it through to the Filter,
	// Project, and (importantly) subquery eval sites.
	// See REQ000366.
	planner *Planner
	// currentKey is the raw key from the LSM iterator for the
	// most recently decoded row. Preserved so Update/Delete
	// can reuse the original row key for hidden-PK tables.
	currentKey []byte
	// alias is the table alias (e.g. "x" in "FROM t1 AS x").
	// When set, produced rows have column names prefixed with
	// "alias." so correlated subquery eval can resolve x.col.
	alias string
	// REQ000760: pre-computed prefixed column names and index
	// to avoid per-row allocation in prefixRowCols.
	prefixedCols     []string
	prefixedColIndex map[string]int

	// REQ000790: index usage tracking for diagnostics.
	iu *IndexUsage
	// availableIdx tracks indexes available on this table for skip detection.
	availableIdx []string

	// REQ000820: pointLookup maps column value→row indices for in-memory
	// tables. When set, Next() only returns rows whose column value is in
	// the lookup set. Built lazily on first Next() call.
	pointLookup    map[any]bool   // wanted values
	pointLookupCol string         // column name to index by (e.g. "a")
	pointLookupPos int            // current position within the matching indices
	pointLookupOnce bool          // true after lookup is built
	pointLookupRows []int         // pre-computed matching row indices

	// REQ000840: shallow clone — reuse source row Data for read-only
	// queries. Set to true for pure SELECT paths to avoid per-row
	// allocation in cloneRow. Must be false for mutable operators
	// (UPDATE/DELETE returning, ON CONFLICT DO UPDATE).
	shallow bool
}

// WithParams propagates the bound `?` placeholders to this
// operator (R16-1..2). Returns the receiver for chaining.
func (s *SeqScan) WithParams(p []any) Operator {
	s.params = p
	return s
}

// WithPlanner attaches the main-plan planner to rows produced by
// this SeqScan. REQ000366.
func (s *SeqScan) WithPlanner(p *Planner) Operator {
	s.planner = p
	return s
}

// WithAlias sets a table alias so produced rows have column names
// prefixed with "alias.". Used for correlated subqueries.
// REQ000760: pre-compute prefixed cols once to avoid per-row allocation.
func (s *SeqScan) WithAlias(alias string) *SeqScan {
	s.alias = alias
	// Pre-compute prefixed column names from the schema.
	if s.schema != nil {
		prefix := alias + "."
		s.prefixedCols = make([]string, len(s.schema.cols))
		s.prefixedColIndex = make(map[string]int, len(s.schema.cols)*2)
		for i, c := range s.schema.cols {
			pc := prefix + c
			s.prefixedCols[i] = pc
			s.prefixedColIndex[pc] = i
		}
		// REQ000941: also register unprefixed names so unqualified
		// column lookups (e.g. "col0") work when a table alias is used.
		// Without this, expressions like "- col0" inside aggregates
		// resolve to the column name string instead of the value.
		for i, c := range s.schema.cols {
			if _, exists := s.prefixedColIndex[c]; !exists {
				s.prefixedColIndex[c] = i
			}
		}
	} else {
		// In-memory mode: schema not yet available; compute on first use.
	}
	return s
}

func NewSeqScan(table string) *SeqScan {
	return &SeqScan{table: table, shallow: true}
}

// WithPointLookup sets up point-lookup filtering on an in-memory table.
// Only rows where the given column's value is in values are returned.
// REQ000820.
func (s *SeqScan) WithPointLookup(col string, values []any) *SeqScan {
	if len(values) == 0 {
		return s
	}
	s.pointLookupCol = col
	want := make(map[any]bool, len(values))
	for _, v := range values {
		want[v] = true
	}
	s.pointLookup = want
	s.pointLookupOnce = false
	return s
}

// buildPointLookup scans the in-memory table and pre-computes the list
// of row indices whose pointLookupCol value is in the pointLookup set.
// REQ000820.
func (s *SeqScan) buildPointLookup(src []Row) {
	s.pointLookupOnce = true
	if len(src) == 0 || s.pointLookup == nil {
		s.pointLookupRows = nil
		return
	}
	// Find column index.
	colIdx := -1
	for i, c := range src[0].Cols {
		if strings.EqualFold(c, s.pointLookupCol) {
			colIdx = i
			break
		}
	}
	if colIdx < 0 || colIdx >= len(src[0].Data) {
		s.pointLookup = nil
		return
	}
	// Scan table and collect matching row indices.
	var matching []int
	for i, row := range src {
		v := row.Data[colIdx].ToAny()
		if s.pointLookup[v] {
			matching = append(matching, i)
		}
	}
	s.pointLookupRows = matching
	s.pointLookupPos = 0
	s.pointLookup = nil // free the map, no longer needed
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

func (s *SeqScan) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if s.store != nil {
		return s.nextFromStore(ctx)
	}
	tablesMu.RLock()
	defer tablesMu.RUnlock()
	src := tables[s.table]

	// REQ000820: if pointLookup is set, build the value→row-index map
	// lazily and iterate only over matching rows.
	if s.pointLookup != nil && !s.pointLookupOnce {
		s.buildPointLookup(src)
	}
	if s.pointLookupRows != nil {
		if s.pointLookupPos >= len(s.pointLookupRows) {
			return Row{}, ErrNoRows
		}
		r := src[s.pointLookupRows[s.pointLookupPos]]
		s.pointLookupPos++
		schema := getTableSchema(s.table, src)
		if schema == nil {
			return Row{}, ErrNoRows
		}
		return s.cloneRow(r, schema), nil
	}

	if s.pos >= len(src) {
		return Row{}, ErrNoRows
	}
	// REQ000759: clone one row on demand instead of eager snapshot.
	r := src[s.pos]
	s.pos++
	schema := getTableSchema(s.table, src)
	if schema == nil {
		return Row{}, ErrNoRows
	}

	// REQ000790: record index skip if indexes are available but SeqScan is used.
	if s.iu != nil && len(s.availableIdx) > 0 {
		s.iu.RecordIndexSkip(s.availableIdx[0], s.table, "SeqScan used instead of IndexScan")
	}

	return s.cloneRow(r, schema), nil
}

func (s *SeqScan) cloneRow(r Row, schema *tableSchemaEntry) Row {
	out := Row{
		Cols:      schema.cols,
		Types:     schema.types,
		Data:      r.Data,
		Outer:     r.Outer,
		planner:   r.planner,
		storeKey:  r.storeKey,
		tableName: s.table,
		colIndex:  schema.colIndex,
	}
	// REQ000840: when shallow, reuse source row Data without copying.
	// Safe for read-only queries — source rows in tables[] are never
	// mutated after INSERT, and downstream operators (Filter, Project,
	// Join) read from Data but never write to it in-place.
	if !s.shallow {
		out.Data = append([]Value(nil), r.Data...)
	}
	if s.planner != nil {
		out.planner = s.planner
	}
	if s.alias != "" {
		if s.prefixedCols != nil {
			out.Cols = s.prefixedCols
			out.colIndex = s.prefixedColIndex
		} else {
			out = prefixRowCols(out, s.alias)
		}
		out.tableName = s.alias
	}
	return out
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
		row.tableName = s.table
		if s.alias != "" {
			if s.prefixedCols != nil {
				row.Cols = s.prefixedCols
				row.colIndex = s.prefixedColIndex
			} else {
				row = prefixRowCols(row, s.alias)
			}
			row.tableName = s.alias
		}

		// REQ000790: record index skip if indexes are available but SeqScan is used.
		if s.iu != nil && len(s.availableIdx) > 0 {
			s.iu.RecordIndexSkip(s.availableIdx[0], s.table, "SeqScan used instead of IndexScan")
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

// prefixRowCols returns a copy of r with each column name prefixed
// by "alias.". Used by SeqScan when a FROM alias is specified so
// correlated subquery eval can resolve qualified names like x.col.
func prefixRowCols(r Row, alias string) Row {
	out := Row{
		Data:      r.Data,
		Outer:     r.Outer,
		Types:     r.Types,
		tableName: r.tableName,
	}
	out.Cols = make([]string, len(r.Cols))
	prefix := alias + "."
	// Build colIndex for prefixed names.
	out.colIndex = make(map[string]int, len(r.Cols)*2)
	for i, c := range r.Cols {
		if strings.HasPrefix(c, prefix) {
			out.Cols[i] = c
			out.colIndex[c] = i
		} else {
			out.Cols[i] = prefix + c
			out.colIndex[prefix+c] = i
			// REQ000941: also register unprefixed name so unqualified
			// column lookups work when a table alias is used.
			if _, exists := out.colIndex[c]; !exists {
				out.colIndex[c] = i
			}
		}
	}
	return out
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
	// filtering, avoiding buildIndexKey allocation per entry.
	prefixIdxKey []byte

	// REQ000790: index usage tracking for diagnostics.
	iu *IndexUsage
}

// WithParams propagates the bound `?` placeholders to this
// operator (R16-1..2).
func (i *IndexScan) WithParams(p []any) Operator {
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
// seekValue is the index value to look up (exact match); if empty,
// the scan returns all rows in index order. rangeEnd, if non-nil,
// limits the scan to entries strictly less than this value
// (lexicographic).
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
		row.tableName = i.table

		// REQ000790: record index usage for diagnostics.
		if i.iu != nil && i.idx != "" {
			i.iu.RecordIndexUse(i.idx, i.table)
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
		// REQ001035: cache the broad prefix for indexValueFromKey.
		if i.prefixIdxKey == nil {
			i.prefixIdxKey = buildIndexKey(i.indexTableID, i.idx, nil)
		}
		// For exact-match seeks (indexSeek without indexLower),
		// narrow the prefix to the seek value. For range scans
		// (indexLower/indexUpper), use the broad index prefix
		// and let the loop filter by lower/exclusive bounds.
		if i.indexSeek != nil && i.indexLower == nil {
			i.indexIt = i.store.NewIterator(buildIndexKey(i.indexTableID, i.idx, i.indexSeek))
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
		// REQ001035: use cached prefix to avoid buildIndexKey allocation.
		idxVal := indexValueFromKey(key, i.prefixIdxKey)
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
		rowKey := append(append([]byte{}, i.prefix...), pk...)
		rowBytes, found, err := i.store.Get(rowKey)
		if err != nil {
			return Row{}, err
		}
		if !found {
			continue
		}
		row, err := decodeRow(rowBytes, i.schema)
		if err != nil {
			return Row{}, err
		}
		row.tableName = i.table
		return row, nil
	}
	return Row{}, ErrNoRows
}

// indexValueFromKey strips the index prefix
// `__idx__:<tableID>:<idxName>:` from the key and returns the
// remaining bytes (the indexed column value). Returns nil if the
// key does not start with the expected prefix.
func indexValueFromKey(key []byte, prefix []byte) []byte {
	if len(key) < len(prefix) {
		return nil
	}
	if !bytes.Equal(key[:len(prefix)], prefix) {
		return nil
	}
	return key[len(prefix):]
}

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
	r.tableName = i.table

	// REQ000790: record index usage for diagnostics.
	if i.iu != nil && i.idx != "" {
		i.iu.RecordIndexUse(i.idx, i.table)
	}

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
		row.tableName = i.table

		// REQ000790: record index usage for diagnostics.
		if i.iu != nil && i.idx != "" {
			i.iu.RecordIndexUse(i.idx, i.table)
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
