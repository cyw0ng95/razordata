package OP

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	id "github.com/cyw0ng95/razordata/internal/ENG/ID"
	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TableSchemaCache caches shared column metadata per table to avoid
// rebuilding Cols, Types, and colIndex on every SeqScan.snapshot() call.
type tableSchemaEntry struct {
	cols     []string
	types    []LX.TokenType
	colIndex map[string]int
}

type TableSchemaEntry = tableSchemaEntry

var (
	TableSchemaMu    sync.RWMutex
	TableSchemaCache = map[string]*TableSchemaEntry{}
)

func getTableSchema(table string, src []Row) *tableSchemaEntry {
	if len(src) == 0 {
		return nil
	}
	// Fast path: read lock.
	TableSchemaMu.RLock()
	entry, ok := TableSchemaCache[table]
	TableSchemaMu.RUnlock()
	if ok {
		return entry
	}
	// Slow path: write lock and build.
	TableSchemaMu.Lock()
	defer TableSchemaMu.Unlock()
	// Double-check after acquiring write lock.
	if entry, ok = TableSchemaCache[table]; ok {
		return entry
	}
	cols := append([]string(nil), src[0].Cols...)
	var types []LX.TokenType
	if len(src[0].Types) > 0 {
		types = append([]LX.TokenType(nil), src[0].Types...)
	}
	colIndex := make(map[string]int, len(cols))
	for i, c := range cols {
		colIndex[strings.ToLower(c)] = i
	}
	entry = &tableSchemaEntry{cols: cols, types: types, colIndex: colIndex}
	TableSchemaCache[table] = entry
	return entry
}

// REQ001657: BlockStatProvider is implemented by iterators that can
// report per-block column statistics for range predicate pruning.
type BlockStatProvider interface {
	// BlockColumnStats returns the min/max value for the given column
	// in the current block. Returns (nil, nil, false) if not available.
	BlockColumnStats(colIdx int) (min, max []byte, ok bool)
}
// rows without decoding (e.g., SeqScan, IndexScan).
type Skipper interface {
	Skip(ctx context.Context, n int64) error
}

type SeqScan struct {
	table  string
	store  Store
	schema *StoreSchema
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
	planner pl.QueryPlanner
	// currentKey is the raw key from the LSM iterator for the
	// most recently decoded row. Preserved so Update/Delete
	// can reuse the original row key for hidden-PK DT.Tables.
	currentKey []byte
	// keyBuf is a reusable buffer for geometric-growth key copies.
	// REQ001635: eliminates the per-row make([]byte, len(key)) allocation.
	keyBuf []byte
	// alias is the table alias (e.g. "x" in "FROM t1 AS x").
	// When set, produced rows have column names prefixed with
	// "alias." so correlated subquery eval can resolve x.col.
	alias string
	// REQ000760: pre-computed prefixed column names and index
	// to avoid per-row allocation in prefixRowCols.
	prefixedCols     []string
	prefixedColIndex map[string]int

	// REQ000790: index usage tracking for diagnostics.
	iu *DT.IndexUsage
	// availableIdx tracks indexes available on this table for skip detection.
	availableIdx []string

	// REQ000820: pointLookup maps column value→row indices for in-memory
	// DT.Tables. When set, Next() only returns rows whose column value is in
	// the lookup set. Built lazily on first Next() call.
	pointLookup     map[any]bool // wanted values
	pointLookupCol  string       // column name to index by (e.g. "a")
	pointLookupPos  int          // current position within the matching indices
	pointLookupOnce bool         // true after lookup is built
	pointLookupRows []int        // pre-computed matching row indices

	// REQ000840: shallow clone — reuse source row Data for read-only
	// queries. Set to true for pure SELECT paths to avoid per-row
	// allocation in DT.CloneRow. Must be false for mutable operators
	// (UPDATE/DELETE returning, ON CONFLICT DO UPDATE).
	shallow bool

	// REQ001042: batched context check counter.
	ctxCheckCounter int

	// REQ001080: usedCols is the set of columns actually referenced by
	// the query. When set, Next() only populates these columns in the
	// returned rows, pruning unused columns from the scan output.
	usedCols   []string
	usedColSet map[string]bool
	usedColIdx []int // index into the full schema

	// REQ001229: projection pushdown — when set, only these column indices
	// are decoded from the row by cloneRow. Nil means all columns.
	RequestedCols []int

	// REQ001421: pre-allocated prune buffers to avoid per-row make
	// in pruneRowCols. Initialized by WithUsedCols; reused across
	// all rows of the same scan. The index map is rebuilt each time
	// (slice-backed map, ~10ns for <8 keys), but the slice backing
	// arrays are pre-sized.
	pruneBufCols  []string
	pruneBufTypes []LX.TokenType
	pruneBufData  []Value
	pruneBufIndex map[string]int

	// REQ001221: rowArena replaces decodeBuf for bump-pointer
	rowArena *DT.RowArena

	// REQ001558: pre-computed projection metadata. When RequestedCols
	// is set, these are built once in NewSeqScan and reused across
	// all rows, eliminating per-row make([]string) + make([]Value) +
	// make(map[string]int) allocations.
	projCols     []string
	projTypes    []LX.TokenType
	projColIndex map[string]int

	// REQ001434: subsetSchema caches a derived StoreSchema whose
	// Cols/ColTypes/ColIndex cover only the columns referenced
	// by the planner (usedColIdx). Cached on the SeqScan so the
	// subset construction runs once per scan, not per row.
	subsetSchema *DT.StoreSchema
	// REQ001434: subsetSchemaAliasKey records the alias context
	// under which subsetSchema was built. When the seq alias or
	// prefixedCols state changes, we rebuild the subset.
	subsetSchemaAliasKey string
	// REQ001640: subsetWantedSet caches the wantedIdx→pos map
	// used by DecodeRowSubsetInto. Built once alongside subsetSchema
	// to eliminate the per-row make(map[int]int) allocation.
	subsetWantedSet map[int]int

	// REQ001225: rawByteFilter is a predicate compiled from a filter
	// conjunct that can be evaluated on raw encoded bytes without
	// decoding the row. Set by NewFilter when pushdown is possible.
	rawByteFilter func([]byte) bool

	// REQ001654: predicate metadata for block-level range skipping.
	// Set by the planner when a range predicate is pushed down.
	// -1 means no predicate is set.
	predicateCol   int
	predicateMin   int64
	predicateMax   int64
	predicateIsSet bool

	closed atomic.Bool
}

// SetRawByteFilter sets a raw-byte predicate filter. REQ001225.
func (s *SeqScan) SetRawByteFilter(f func([]byte) bool) {
	s.rawByteFilter = f
}

// SetRangePredicate sets the column and range for block-level skipping.
// REQ001654. colIdx is the column index in the schema; min/max are the
// range bounds. The SeqScan will skip blocks whose column min/max are
// entirely outside this range.
func (s *SeqScan) SetRangePredicate(colIdx int, min, max int64) {
	s.predicateCol = colIdx
	s.predicateMin = min
	s.predicateMax = max
	s.predicateIsSet = true
}

// WithParams propagates the bound `?` placeholders to this
// operator (R16-1..2). Returns the receiver for chaining.
func (s *SeqScan) WithParams(p []any) pl.Operator {
	s.params = p
	return s
}

// WithPlanner attaches the main-plan planner to rows produced by
// this SeqScan. REQ000366.
func (s *SeqScan) WithPlanner(p pl.QueryPlanner) pl.Operator {
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
		s.prefixedCols = make([]string, len(s.schema.Cols))
		s.prefixedColIndex = make(map[string]int, len(s.schema.Cols)*2)
		for i, c := range s.schema.Cols {
			pc := prefix + c
			s.prefixedCols[i] = pc
			s.prefixedColIndex[pc] = i
		}
		// REQ000941: also register unprefixed names so unqualified
		// column lookups (e.g. "col0") work when a table alias is used.
		// Without this, expressions like "- col0" inside aggregates
		// resolve to the column name string instead of the value.
		for i, c := range s.schema.Cols {
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

// growKeyBuf grows buf to at least n bytes using geometric growth.
// Returns a slice of length n. REQ001635: eliminates per-row
// make([]byte, n) allocation by reusing a pre-sized buffer.
func growKeyBuf(buf []byte, n int) []byte {
	if cap(buf) >= n {
		return buf[:n]
	}
	newCap := cap(buf) * 2
	if newCap < n {
		newCap = n
	}
	if newCap < 64 {
		newCap = 64
	}
	return make([]byte, n, newCap)
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

// WithUsedCols sets the set of column names the query actually uses.
// SeqScan will only populate these columns in returned rows.
// REQ001080.
func (s *SeqScan) WithUsedCols(cols []string) *SeqScan {
	s.usedCols = cols
	s.usedColSet = make(map[string]bool, len(cols))
	for _, c := range cols {
		s.usedColSet[c] = true
	}
	// REQ001421: pre-allocate prune buffers at plan time to avoid
	// per-row make([]Value, N) in the hot path. The index map
	// is rebuilt each call (~10ns for small maps), but the slice
	// backing arrays are pre-sized.
	s.pruneBufCols = make([]string, 0, len(cols))
	s.pruneBufTypes = make([]LX.TokenType, 0, len(cols))
	s.pruneBufData = make([]Value, 0, len(cols))
	s.pruneBufIndex = make(map[string]int, len(cols)*2)
	// REQ001434: pre-compute the indices into the schema for
	// fast column-aware decoding. decodeRowBuffered consults
	// this to skip non-wanted columns during the byte-stream
	// parse instead of decoding them and pruning them later.
	if s.schema != nil {
		s.usedColIdx = make([]int, 0, len(cols))
		colIdx := make(map[string]int, len(s.schema.Cols))
		for i, c := range s.schema.Cols {
			colIdx[c] = i
		}
		for _, c := range cols {
			if i, ok := colIdx[c]; ok {
				s.usedColIdx = append(s.usedColIdx, i)
			}
		}
	}
	// REQ001558: pre-compute projection metadata.
	s.initProjection()
	return s
}

// initProjection pre-computes the projected column metadata from
// RequestedCols. Called once after RequestedCols is set. REQ001558.
func (s *SeqScan) initProjection() {
	if len(s.RequestedCols) == 0 || s.schema == nil {
		return
	}
	cols := s.schema.Cols
	types := s.schema.ColTypes
	rc := s.RequestedCols
	n := len(rc)
	s.projCols = make([]string, n)
	s.projTypes = make([]LX.TokenType, n)
	s.projColIndex = make(map[string]int, n*2)
	for i, idx := range rc {
		if idx >= 0 && idx < len(cols) {
			s.projCols[i] = cols[idx]
			if idx < len(types) {
				s.projTypes[i] = types[idx]
			}
		}
		s.projColIndex[s.projCols[i]] = i
	}
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
	ss, ok := DT.SchemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &SeqScan{
		table:  table,
		store:  store,
		schema: ss,
		prefix: TablePrefix(table),
	}, nil
}

// engineBatchSize is the number of rows to fetch per batch from the
// LSM engine. 256 balances iterator overhead with per-batch memory
// (fits in L1 cache). Increased from 64 for improved full-scan
// throughput (REQ001224).
var engineBatchSize = 512

// EngineBatchSize returns the current batch size for SeqScan.NextBatch.
func EngineBatchSize() int { return engineBatchSize }

// SetEngineBatchSize sets the batch size for SeqScan.NextBatch.
// Returns the previous value. Clamped to [1, 4096].
func SetEngineBatchSize(n int) int {
	prev := engineBatchSize
	if n < 1 {
		n = 1
	}
	if n > 4096 {
		n = 4096
	}
	engineBatchSize = n
	return prev
}

// defaultScanRowBuf is the number of rows to buffer in nextFromStore
// for reuse of decoded Value slices. REQ001101: reduces per-row
// make([]Value, N) allocations from 25K to 391 for a 25K row scan.
const defaultScanRowBuf = 64

// valueToBatch converts a Value to the (any, LX.TokenType) pair
// expected by Batch.AppendRow. REQ001064.
func valueToBatch(v Value) (any, LX.TokenType) {
	switch v.Kind {
	case KindInt:
		return v.I64, LX.T_INT_KW
	case KindFloat:
		return v.F64, LX.T_FLOAT_KW
	case KindText:
		return v.S, LX.T_TEXT
	case KindBool:
		return v.Bo, LX.T_BOOL
	case KindBlob:
		return string(v.B), LX.T_BLOB
	default:
		return nil, LX.T_TEXT
	}
}

// NextBatch reads up to engineBatchSize rows from the LSM iterator
// and returns them as a columnar *UT.Batch. Returns (nil, nil) at EOF.
// Caller is responsible for calling Put() on each non-nil batch.
// REQ001064.
func (s *SeqScan) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if s.store == nil {
		return nil, fmt.Errorf("op: SeqScan.NextBatch requires a Store")
	}
	if s.schema == nil || len(s.schema.Cols) == 0 {
		return nil, nil
	}
	if s.it == nil {
		if s.prefix == nil {
			return nil, nil
		}
		s.it = s.store.NewIterator(s.prefix)
	}
	if s.it == nil {
		return nil, nil
	}

	// Use engineBatchSize directly (mutable for PRAGMA batch_size).
	bs := engineBatchSize
	if bs <= 0 {
		bs = 256
	}
	nCols := len(s.schema.Cols)
	cols := s.schema.Cols
	if s.alias != "" && s.prefixedCols != nil {
		cols = s.prefixedCols
	}
	batch := UT.GetBatch(nCols)
	batch.Size = 0
	for k := 0; k < nCols; k++ {
		batch.SetColumnName(k, cols[k])
	}
	batch.SetColMap(s.schema.ColIndex)

	for batch.Size < bs {
		if !s.it.Next() {
			if err := s.it.Err(); err != nil {
				batch.Put()
				return nil, err
			}
			break
		}
		s.ctxCheckCounter++
		if s.ctxCheckCounter >= 1024 {
			s.ctxCheckCounter = 0
			if err := ctx.Err(); err != nil {
				batch.Put()
				return nil, err
			}
		}
		s.currentKey = s.it.Key()
		// REQ001583: deep-copy currentKey — s.it.Key() returns a slice
		// into the LSM iterator's internal buffer. Without the copy,
		// StoreKey on every row in Filter.refillBatch's batchBuf aliases
		// the same iterator buffer and gets silently overwritten.
		// REQ001635: use geometric growth for the temp copy, then
		// allocate a separate StoreKey for the row so the key buffer
		// can be reused without corrupting StoreKey.
		s.keyBuf = growKeyBuf(s.keyBuf, len(s.currentKey))
		copy(s.keyBuf, s.currentKey)
		s.currentKey = s.keyBuf
		// StoreKey is a per-row allocation — cannot share keyBuf.
		storeKey := append([]byte(nil), s.currentKey...)
		v := s.it.Value()
		row, err := DecodeRow(v, s.schema)
		if err != nil {
			batch.Put()
			return nil, err
		}
		row.StoreKey = storeKey
		if s.planner != nil {
			row.Planner = s.planner
		}
		row.TableName = s.table
		if s.alias != "" && !row.RowFromSubsetDecode {
			if s.prefixedCols != nil {
				row.Cols = s.prefixedCols
				row.ColIndex = s.prefixedColIndex
			} else {
				prefix := s.alias + "."
				n := len(row.Cols)
				s.prefixedCols = make([]string, n)
				s.prefixedColIndex = make(map[string]int, n*2)
				for i, c := range row.Cols {
					pc := prefix + c
					s.prefixedCols[i] = pc
					s.prefixedColIndex[pc] = i
					if _, exists := s.prefixedColIndex[c]; !exists {
						s.prefixedColIndex[c] = i
					}
				}
				row.Cols = s.prefixedCols
				row.ColIndex = s.prefixedColIndex
			}
			row.TableName = s.alias
		}
		for i := 0; i < nCols; i++ {
			if i >= len(row.Data) {
				break
			}
			val := row.Data[i]
			isNull := val.IsNull()
			av, typ := valueToBatch(val)
			batch.AppendRow(i, typ, av, isNull)
		}
		batch.AdvanceSize()
	}

	if batch.Size == 0 {
		batch.Put()
		return nil, nil
	}
	return batch, nil
}

func (s *SeqScan) Next(ctx context.Context) (Row, error) {
	ec.BUG_ON(s.closed.Load(), "SeqScan.Next() after Close()")
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if s.store != nil {
		return s.nextFromStore(ctx)
	}
	DT.TablesMu.RLock()
	defer DT.TablesMu.RUnlock()
	src := DT.Tables[s.table]

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
		Planner:   r.Planner,
		StoreKey:  r.StoreKey,
		TableName: s.table,
		ColIndex:  schema.colIndex,
	}
	// REQ001229: projection pushdown — when RequestedCols is set, build
	// the output row with only the requested column indices. This avoids
	// decoding all columns only to prune them afterwards.
	// REQ001558: uses pre-computed projection metadata to avoid per-row
	// make([]string) + make([]Value) + make(map[string]int) allocations.
	if s.RequestedCols != nil && s.projCols != nil {
		rc := s.RequestedCols
		n := len(rc)
		newData := make([]Value, n)
		for i, idx := range rc {
			if idx < len(r.Data) {
				newData[i] = r.Data[idx]
			}
		}
		out.Cols = s.projCols
		out.Types = s.projTypes
		out.Data = newData
		out.ColIndex = s.projColIndex
	} else {
		// REQ000840: when shallow, reuse source row Data without copying.
		// Safe for read-only queries — source rows in DT.Tables[] are never
		// mutated after INSERT, and downstream operators (Filter, Project,
		// Join) read from Data but never write to it in-place.
		if !s.shallow {
			out.Data = getRowData(len(r.Data))
			copy(out.Data, r.Data)
		}
		// REQ001080: prune unused columns from the output row.
		if s.usedCols != nil && !s.shallow {
			out = pruneRowCols(out, s.usedCols, s.usedColSet, s)
		}
	}
	if s.planner != nil {
		out.Planner = s.planner
	}
	if s.alias != "" {
		if s.prefixedCols != nil {
			out.Cols = s.prefixedCols
			out.ColIndex = s.prefixedColIndex
		} else {
			// Lazy compute prefixedCols from the row's column names.
			// REQ001569: compute once, not per-row.
			prefix := s.alias + "."
			n := len(out.Cols)
			s.prefixedCols = make([]string, n)
			s.prefixedColIndex = make(map[string]int, n*2)
			for i, c := range out.Cols {
				pc := prefix + c
				s.prefixedCols[i] = pc
				s.prefixedColIndex[pc] = i
				if _, exists := s.prefixedColIndex[c]; !exists {
					s.prefixedColIndex[c] = i
				}
			}
			out.Cols = s.prefixedCols
			out.ColIndex = s.prefixedColIndex
		}
		out.TableName = s.alias
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
	for s.it.Next() {
		// REQ001042: check ctx.Err() every 1024 rows to reduce overhead.
		s.ctxCheckCounter++
		if s.ctxCheckCounter >= 1024 {
			s.ctxCheckCounter = 0
			if err := ctx.Err(); err != nil {
				return Row{}, err
			}
		}
		// REQ001657: block-level range skipping. Check if the iterator
		// supports block-level stats and if the current block's column
		// range is disjoint from the predicate.
		if s.predicateIsSet {
			if bp, ok := s.it.(BlockStatProvider); ok {
				if min, max, ok := bp.BlockColumnStats(s.predicateCol); ok && len(min) > 0 && len(max) > 0 {
					// Compare predicate range with block's column range.
					// If the predicate range is entirely above max or
					// entirely below min, skip the rest of this block.
					// For int64 values stored as big-endian bytes.
					blockMin := int64(binary.BigEndian.Uint64(min))
					blockMax := int64(binary.BigEndian.Uint64(max))
					if s.predicateMin > blockMax || s.predicateMax < blockMin {
						// Block is entirely outside the predicate range.
						// The iterator will advance to the next block.
						// We need to skip to the end of this block.
						// Since we can't easily skip a block, we just
						// continue — the iterator will load the next
						// block on the next Next() call.
						continue
					}
				}
			}
		}
		// REQ000501: save the raw key so Update/Delete can
		// preserve the original row key for hidden-PK DT.Tables.
		s.currentKey = s.it.Key()
		// REQ001583: deep-copy currentKey — s.it.Key() returns a slice
		// into the LSM iterator's internal buffer that is only valid
		// until the next Next() call. Without the copy, StoreKey on
		// every row in Filter.refillBatch's batchBuf aliases the same
		// iterator buffer and gets silently overwritten, causing
		// ExtractPKForUpdate to see an empty StoreKey and allocate a
		// new synthetic rowid for every UPDATE.
		// REQ001635: use geometric growth for the temp copy, then
		// allocate a separate StoreKey for the row so the key buffer
		// can be reused without corrupting StoreKey.
		s.keyBuf = growKeyBuf(s.keyBuf, len(s.currentKey))
		copy(s.keyBuf, s.currentKey)
		s.currentKey = s.keyBuf
		storeKey := append([]byte(nil), s.currentKey...)
		v := s.it.Value()
		// REQ001225: apply raw-byte filter before decoding to avoid
		// unnecessary DecodeRowInto work for rows that will be filtered.
		if s.rawByteFilter != nil && !s.rawByteFilter(v) {
			continue
		}
		// REQ001101: decode into reusable buffer to avoid per-row
		// make([]Value, N) for every row scanned from the store.
		row, err := s.decodeRowBuffered(v)
		if err != nil {
			return Row{}, err
		}
		row.StoreKey = storeKey
		// REQ000366: thread the planner so subquery evals see
		// the same store/catalog.
		if s.planner != nil {
			row.Planner = s.planner
		}
		row.TableName = s.table
		if s.alias != "" && !row.RowFromSubsetDecode {
			if s.prefixedCols != nil {
				row.Cols = s.prefixedCols
				row.ColIndex = s.prefixedColIndex
			} else {
				prefix := s.alias + "."
				n := len(row.Cols)
				s.prefixedCols = make([]string, n)
				s.prefixedColIndex = make(map[string]int, n*2)
				for i, c := range row.Cols {
					pc := prefix + c
					s.prefixedCols[i] = pc
					s.prefixedColIndex[pc] = i
					if _, exists := s.prefixedColIndex[c]; !exists {
						s.prefixedColIndex[c] = i
					}
				}
				row.Cols = s.prefixedCols
				row.ColIndex = s.prefixedColIndex
			}
			row.TableName = s.alias
		}

		// REQ000790: record index skip if indexes are available but SeqScan is used.
		if s.iu != nil && len(s.availableIdx) > 0 {
			s.iu.RecordIndexSkip(s.availableIdx[0], s.table, "SeqScan used instead of IndexScan")
		}

		// REQ001080: prune unused columns from the store-backed row.
		if s.usedCols != nil && !row.RowFromSubsetDecode {
			row = pruneRowCols(row, s.usedCols, s.usedColSet, s)
			// REQ001481: no defensive copy needed. Downstream Project.Next
			// evaluates expressions synchronously (fn(&row) / EvalValue)
			// and writes results into its own dataBuf — it does not retain
			// a reference to row.Data. The prune buffer (pruneBufCols/
			// pruneBufData) lives on the SeqScan and is reused across
			// rows; since Project.Next is called before the next
			// nextFromStore call, the buffer's contents are still valid
			// throughout Project.Next's synchronous evaluation.
		}

		return row, nil
	}
	if err := s.it.Err(); err != nil {
		return Row{}, err
	}
	return Row{}, ErrNoRows
}

// decodeRowBuffered decodes a row using the arena allocator.
// REQ001221: replaces per-row make([]Value, N) with RowArena bump
// allocation. The arena is reset in Close(), freeing all rows at once.
// REQ001260: pre-size arena to engineBatchSize rows to reduce grow calls.
// REQ001434: when the planner set usedColIdx to a strict subset of the
// schema, decode only that subset via DecodeRowSubsetInto. The decoded
// row carries the SUBSET schema (Cols/Types/ColIndex built from the
// wanted indices) so downstream Project sees only the columns the
// query actually references, eliminating a per-row pruneRowCols pass.
func (s *SeqScan) decodeRowBuffered(data []byte) (Row, error) {
	if s.rowArena == nil {
		s.rowArena = &DT.RowArena{}
		s.rowArena.Init(engineBatchSize, len(s.schema.Cols))
	}
	n := len(s.schema.Cols)
	if n == 0 {
		return Row{}, nil
	}
	// REQ001434: take the subset fast-path when the planner pinned
	// a strict subset of columns AND every wanted index is present
	// in the schema. If usedColIdx is nil/empty or matches the full
	// schema, fall through to the original decode-all path.
	if s.usedColIdx != nil && len(s.usedColIdx) > 0 && len(s.usedColIdx) < n {
		row, err := s.decodeRowSubsetBuffered(data)
		if err != nil {
			return Row{}, err
		}
		// REQ001434: subset decode already produced row.Cols in
		// the correct form (prefixed when an alias is set). The
		// aliasing pass in nextFromStore would otherwise overwrite
		// row.Cols with the FULL prefixed list, breaking the
		// Data/Cols length invariant. Tag the row so the caller
		// can skip the aliasing pass.
		row.RowFromSubsetDecode = true
		return row, nil
	}
	row := s.rowArena.AllocRow(n, s.schema)
	err := DT.DecodeRowInto(&row, data, s.schema)
	if err != nil {
		return Row{}, err
	}
	return row, nil
}

// decodeRowSubsetBuffered decodes only the columns listed in
// usedColIdx, building a Row whose Cols/ColIndex/Types carry the
// SUBSET schema. REQ001434.
func (s *SeqScan) decodeRowSubsetBuffered(data []byte) (Row, error) {
	wanted := s.usedColIdx
	n := len(wanted)
	if n == 0 {
		return Row{}, nil
	}
	// REQ001434: build the subset schema once per scan and cache it
	// on the SeqScan. Reusing across rows avoids per-row allocation
	// of the subset Cols / ColIndex. When a table alias is set, the
	// downstream prefixRowCols pass will overwrite row.Cols with
	// the prefixed form, so the subset schema's Cols must use the
	// prefix here to stay consistent with what callers see.
	if s.subsetSchema == nil || len(s.subsetSchema.Cols) != n || s.subsetSchemaAliasKey != s.aliasCacheKey() {
		subset := &DT.StoreSchema{
			Cols:     make([]string, n),
			ColTypes: make([]LX.TokenType, n),
		}
		subset.ColIndex = make(map[string]int, n*2)
		// REQ001640: build wantedSet alongside subsetSchema.
		wantedSet := make(map[int]int, n)
		prefix := ""
		if s.alias != "" {
			prefix = s.alias + "."
		}
		for pos, fullIdx := range wanted {
			base := s.schema.Cols[fullIdx]
			subset.Cols[pos] = prefix + base
			if fullIdx < len(s.schema.ColTypes) {
				subset.ColTypes[pos] = s.schema.ColTypes[fullIdx]
			}
			subset.ColIndex[subset.Cols[pos]] = pos
			wantedSet[fullIdx] = pos
		}
		s.subsetSchema = subset
		s.subsetSchemaAliasKey = s.aliasCacheKey()
		s.subsetWantedSet = wantedSet
	}
	row := s.rowArena.AllocRow(n, s.subsetSchema)
	err := DT.DecodeRowSubsetInto(&row, data, s.schema, wanted, s.subsetWantedSet)
	if err != nil {
		return Row{}, err
	}
	return row, nil
}

// aliasCacheKey returns a stable identifier for the alias context
// used to invalidate the cached subset schema. REQ001434.
func (s *SeqScan) aliasCacheKey() string {
	if s.prefixedCols != nil {
		return s.alias + "|prefixed"
	}
	return s.alias
}

// Skip advances the iterator by n rows without decoding them.
// Only works for store-backed SeqScan. REQ001650.
func (s *SeqScan) Skip(ctx context.Context, n int64) error {
	if s.store == nil {
		// In-memory fallback: advance the position counter.
		s.pos += int(n)
		return nil
	}
	if s.it == nil {
		s.it = s.store.NewIterator(s.prefix)
	}
	if s.it == nil {
		return nil
	}
	for i := int64(0); i < n; i++ {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if !s.it.Next() {
			if err := s.it.Err(); err != nil {
				return err
			}
			return nil
		}
	}
	return nil
}

func (s *SeqScan) Close() error {
	s.closed.Store(true)
	if s.it != nil {
		err := s.it.Close()
		s.it = nil
		return err
	}
	s.pos = 0
	s.rows = nil
	if s.rowArena != nil {
		s.rowArena.Reset()
	}
	// REQ001421: clear prune buffers so next user of the cached
	// SeqScan starts fresh.
	clear(s.pruneBufIndex)
	s.pruneBufCols = s.pruneBufCols[:0]
	s.pruneBufTypes = s.pruneBufTypes[:0]
	s.pruneBufData = s.pruneBufData[:0]
	// REQ001195: clear point-lookup state so plan cache reuse
	// with different literal values triggers a fresh scan instead
	// of using stale pre-computed row indices.
	s.pointLookupRows = nil
	s.pointLookupOnce = false
	s.pointLookupPos = 0
	return nil
}

// Reset reinitializes SeqScan cursor state for operator tree reuse.
// Does NOT close the iterator (reopened lazily on next Next() call)
// and does NOT close the child (none for leaf SeqScan).
// REQ001464.
func (s *SeqScan) Reset(ctx context.Context) error {
	if s.it != nil {
		_ = s.it.Close()
		s.it = nil
	}
	s.pos = 0
	s.rows = nil
	s.currentKey = nil
	s.ctxCheckCounter = 0
	if s.rowArena != nil {
		s.rowArena.Reset()
	}
	clear(s.pruneBufIndex)
	s.pruneBufCols = s.pruneBufCols[:0]
	s.pruneBufTypes = s.pruneBufTypes[:0]
	s.pruneBufData = s.pruneBufData[:0]
	s.pointLookupRows = nil
	s.pointLookupOnce = false
	s.pointLookupPos = 0
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
		TableName: r.TableName,
	}
	out.Cols = make([]string, len(r.Cols))
	prefix := alias + "."
	// Build colIndex for prefixed names.
	out.ColIndex = make(map[string]int, len(r.Cols)*2)
	for i, c := range r.Cols {
		if strings.HasPrefix(c, prefix) {
			out.Cols[i] = c
			out.ColIndex[c] = i
		} else {
			out.Cols[i] = prefix + c
			out.ColIndex[prefix+c] = i
			// REQ000941: also register unprefixed name so unqualified
			// column lookups work when a table alias is used.
			if _, exists := out.ColIndex[c]; !exists {
				out.ColIndex[c] = i
			}
		}
	}
	return out
}

// nextColumnarBatch fills a batch directly from the store, bypassing
// Row construction. Decodes only the columns in wantedCols via
// DecodeRowSubsetIntoColumnar and writes directly to batch column
// slices. REQ001480.
func (s *SeqScan) nextColumnarBatch(ctx context.Context, batch *UT.Batch, wantedCols []int, wantedTypes []LX.TokenType) (int, error) {
	if s.it == nil {
		s.it = s.store.NewIterator(s.prefix)
	}
	if s.it == nil {
		return 0, nil
	}
	rowIdx := 0
	maxRows := UT.BatchSize
	interner := &UT.StringInterner{}
	w := &batchColumnWriter{
		batch:  batch,
		wanted: wantedCols,
		types:  wantedTypes,
		intern: interner,
	}
	for rowIdx < maxRows && s.it.Next() {
		s.ctxCheckCounter++
		if s.ctxCheckCounter >= 1024 {
			s.ctxCheckCounter = 0
			if err := ctx.Err(); err != nil {
				return rowIdx, err
			}
		}
		v := s.it.Value()
		if s.rawByteFilter != nil && !s.rawByteFilter(v) {
			continue
		}
		w.rowIdx = rowIdx
		if err := DT.DecodeRowSubsetIntoColumnar(v, s.schema, wantedCols, w); err != nil {
			return rowIdx, err
		}
		rowIdx++
	}
	if err := s.it.Err(); err != nil {
		return rowIdx, err
	}
	return rowIdx, nil
}

// batchColumnWriter implements DT.ColumnWriter for filling batch
// column data directly from DecodeRowSubsetIntoColumnar.
type batchColumnWriter struct {
	batch  *UT.Batch
	wanted []int
	types  []LX.TokenType
	rowIdx int
	intern *UT.StringInterner // optional, batch-scoped string dedup
}

func (w *batchColumnWriter) colFor(fullColIdx int) (int, *UT.Column) {
	for i, ci := range w.wanted {
		if ci == fullColIdx {
			if i < len(w.batch.Cols) {
				return i, &w.batch.Cols[i]
			}
			return i, nil
		}
	}
	return -1, nil
}

func (w *batchColumnWriter) ensureInt(col *UT.Column, colIdx int) {
	if col.Data.Ints == nil {
		col.Data.Ints = UT.PoolGetInts(colIdx, UT.BatchSize)
	}
}

func (w *batchColumnWriter) ensureFloat(col *UT.Column, colIdx int) {
	if col.Data.Floats == nil {
		col.Data.Floats = UT.PoolGetFloats(colIdx, UT.BatchSize)
	}
}

func (w *batchColumnWriter) ensureStr(col *UT.Column, colIdx int) {
	if col.Data.Strs == nil {
		col.Data.Strs = UT.PoolGetStrs(colIdx, UT.BatchSize)
	}
}

func (w *batchColumnWriter) ensureBool(col *UT.Column, colIdx int) {
	if col.Data.Bools == nil {
		col.Data.Bools = UT.PoolGetBools(colIdx, UT.BatchSize)
	}
}

func (w *batchColumnWriter) WriteNull(fullColIdx int) error {
	_, col := w.colFor(fullColIdx)
	if col == nil {
		return nil
	}
	if col.Nulls == nil {
		col.Nulls = make([]bool, UT.BatchSize)
	}
	if w.rowIdx < len(col.Nulls) {
		col.Nulls[w.rowIdx] = true
	}
	return nil
}

func (w *batchColumnWriter) WriteInt(fullColIdx int, v int64) error {
	colIdx, col := w.colFor(fullColIdx)
	if col == nil {
		return nil
	}
	w.ensureInt(col, colIdx)
	if w.rowIdx < len(col.Data.Ints) {
		col.Data.Ints[w.rowIdx] = v
	}
	return nil
}

func (w *batchColumnWriter) WriteFloat(fullColIdx int, v float64) error {
	colIdx, col := w.colFor(fullColIdx)
	if col == nil {
		return nil
	}
	w.ensureFloat(col, colIdx)
	if w.rowIdx < len(col.Data.Floats) {
		col.Data.Floats[w.rowIdx] = v
	}
	return nil
}

func (w *batchColumnWriter) WriteBool(fullColIdx int, v bool) error {
	colIdx, col := w.colFor(fullColIdx)
	if col == nil {
		return nil
	}
	w.ensureBool(col, colIdx)
	if w.rowIdx < len(col.Data.Bools) {
		col.Data.Bools[w.rowIdx] = v
	}
	return nil
}

func (w *batchColumnWriter) WriteString(fullColIdx int, v string) error {
	colIdx, col := w.colFor(fullColIdx)
	if col == nil {
		return nil
	}
	w.ensureStr(col, colIdx)
	if w.rowIdx < len(col.Data.Strs) {
		if w.intern != nil {
			v = w.intern.Intern(v)
		}
		col.Data.Strs[w.rowIdx] = v
	}
	return nil
}

func (w *batchColumnWriter) WriteBytes(fullColIdx int, v []byte) error {
	colIdx, col := w.colFor(fullColIdx)
	if col == nil {
		return nil
	}
	w.ensureStr(col, colIdx)
	if w.rowIdx < len(col.Data.Strs) {
		s := string(v)
		if w.intern != nil {
			s = w.intern.Intern(s)
		}
		col.Data.Strs[w.rowIdx] = s
	}
	return nil
}

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

	// REQ001249: covering-index fields. When coveringMode is true,
	// nextFromIndex skips the heap fetch and builds the Row from the
	// secondary index's stored values. coverIdxCols / coverIdxTypes
	// describe the encoded index suffix; coverPK is the column whose
	// value is stored as the index entry's payload.
	coveringMode  bool
	coverIdxCols  []string
	coverIdxTypes []LX.TokenType
	coverPK       string

	closed atomic.Bool
}

// SetCovering marks the IndexScan as a covering scan: its output Row
// is built directly from the secondary-index entry's encoded value
// (multi-column index payload) and primary key, without fetching the
// heap row. idxCols describes the column order in the encoded index
// suffix; idxTypes provides per-column LX type information used to
// decode fixed-width (int = 8B BE) versus variable-length (text until
// 0x00 separator) segments. pk is the column whose raw bytes are
// stored as the index payload; pass "" if the query does not need the
// primary key in the projected Row. REQ001249.
func (i *IndexScan) SetCovering(idxCols []string, idxTypes []LX.TokenType, pk string) {
	if len(idxCols) == 0 {
		return
	}
	if len(idxTypes) != len(idxCols) {
		return
	}
	i.coveringMode = true
	i.coverIdxCols = append([]string(nil), idxCols...)
	i.coverIdxTypes = append([]LX.TokenType(nil), idxTypes...)
	i.coverPK = pk
}

// Covering reports whether this scan is in covering-index mode.
func (i *IndexScan) Covering() bool { return i.coveringMode }

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
		// REQ001583: save StoreKey so Update/Delete can preserve the
		// original row key for hidden-PK tables. Deep-copy because
		// i.it.Key() returns a slice into the iterator's internal buffer.
		if i.it != nil {
			key := i.it.Key()
			if key != nil {
				sk := make([]byte, len(key))
				copy(sk, key)
				row.StoreKey = sk
			}
		}

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

		// REQ001249: covering-index path. Build the Row from the
		// encoded index value + primary key without touching the
		// heap. Residual predicates are still evaluated against the
		// synthesized Row afterwards.
		if i.coveringMode {
			row, err := coveringBuildRow(i.schema, i.table, i.coverIdxCols, i.coverIdxTypes, idxVal, pk, i.coverPK)
			if err != nil {
				return Row{}, err
			}
			if !i.matchResidual(&row) {
				continue
			}
			return row, nil
		}

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
		// REQ001583: save StoreKey so Update/Delete can preserve the
		// original row key for hidden-PK tables. RowKey is already a
		// deep copy (append([]byte{}, i.prefix...), pk...).
		row.StoreKey = RowKey
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
		return err
	}
	if i.btree != nil {
		return i.btree.Close()
	}
	i.rows = nil
	return nil
}

// Skip advances the iterator by n rows without decoding them.
// Only works for store-backed IndexScan. REQ001650.
func (i *IndexScan) Skip(ctx context.Context, n int64) error {
	if i.store == nil {
		i.pos += int(n)
		return nil
	}
	if i.it == nil {
		i.it = i.store.NewIterator(i.prefix)
	}
	if i.it == nil {
		return nil
	}
	for j := int64(0); j < n; j++ {
		if j%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if !i.it.Next() {
			if err := i.it.Err(); err != nil {
				return err
			}
			return nil
		}
	}
	return nil
}

// Reset reinitializes IndexScan cursor. Does NOT close children. REQ001464.
func (i *IndexScan) Reset(ctx context.Context) error {
	if i.it != nil {
		_ = i.it.Close()
		i.it = nil
	}
	if i.indexIt != nil {
		_ = i.indexIt.Close()
		i.indexIt = nil
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

// pruneRowCols filters row Data/Cols/Types to only include columns
// in usedCols. Returns the pruned row. REQ001080.
func pruneRowCols(row Row, usedCols []string, usedSet map[string]bool, seq *SeqScan) Row {
	if len(usedSet) == 0 || len(row.Cols) == 0 {
		return row
	}
	// Check if all columns are already used — skip work.
	allUsed := true
	for _, c := range row.Cols {
		if !usedSet[c] {
			allUsed = false
			break
		}
	}
	if allUsed {
		return row
	}

	var newCols []string
	var newTypes []LX.TokenType
	var newData []Value
	var newIndex map[string]int

	if seq != nil && seq.pruneBufCols != nil {
		newCols = seq.pruneBufCols[:0]
		newTypes = seq.pruneBufTypes[:0]
		newData = seq.pruneBufData[:0]
		newIndex = seq.pruneBufIndex
		// Clear the index map for reuse.
		for k := range newIndex {
			delete(newIndex, k)
		}
	} else {
		newCols = make([]string, 0, len(usedCols))
		newTypes = make([]LX.TokenType, 0, len(usedCols))
		newData = make([]Value, 0, len(usedCols))
		newIndex = make(map[string]int, len(usedCols)*2)
	}

	// Build colIndex from scratch if nil.
	colIndex := row.ColIndex
	if colIndex == nil {
		colIndex = make(map[string]int, len(row.Cols))
		for i, c := range row.Cols {
			colIndex[c] = i
		}
	}
	for _, uc := range usedCols {
		idx, ok := colIndex[uc]
		if !ok {
			// Try without alias prefix
			found := false
			for ci, cn := range row.Cols {
				if cn == uc {
					idx = ci
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if idx < 0 || idx >= len(row.Data) {
			continue
		}
		newCols = append(newCols, row.Cols[idx])
		if idx < len(row.Types) {
			newTypes = append(newTypes, row.Types[idx])
		} else {
			newTypes = append(newTypes, 0)
		}
		newData = append(newData, row.Data[idx])
		newIndex[uc] = len(newCols) - 1
	}
	// Also register unprefixed entries for alias-prefixed cols.
	for i, c := range newCols {
		dotIdx := -1
		for j := 0; j < len(c); j++ {
			if c[j] == '.' {
				dotIdx = j
				break
			}
		}
		if dotIdx >= 0 {
			unprefixed := c[dotIdx+1:]
			if _, exists := newIndex[unprefixed]; !exists {
				newIndex[unprefixed] = i
			}
		}
	}
	row.Cols = newCols
	row.Types = newTypes
	row.Data = newData
	row.ColIndex = newIndex
	return row
}

// Accessor methods for SeqScan fields used by EX plan_node and parallel operators.
func (s *SeqScan) Table() string               { return s.table }
func (s *SeqScan) Store() DT.Store             { return s.store }
func (s *SeqScan) Schema() *DT.StoreSchema     { return s.schema }
func (s *SeqScan) UsedCols() []string          { return s.usedCols }
func (s *SeqScan) UsedColSet() map[string]bool { return s.usedColSet }
func (s *SeqScan) GetRequestedCols() []int     { return s.RequestedCols }
func (s *SeqScan) Btree() *id.BTree            { return nil } // SeqScan has no B-tree

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
