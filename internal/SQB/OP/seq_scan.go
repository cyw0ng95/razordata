package OP

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	id "github.com/cyw0ng95/razordata/internal/ENG/ID"
	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

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

	// REQ001221: rowArena replaces decodeBuf for bump-pointer
	rowArena *DT.RowArena

	// REQ001225: rawByteFilter is a predicate compiled from a filter
	// conjunct that can be evaluated on raw encoded bytes without
	// decoding the row. Set by NewFilter when pushdown is possible.
	rawByteFilter func([]byte) bool

	closed atomic.Bool
}

// SetRawByteFilter sets a raw-byte predicate filter. REQ001225.
func (s *SeqScan) SetRawByteFilter(f func([]byte) bool) {
	s.rawByteFilter = f
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
	if len(cols) == 0 {
		return s
	}
	s.usedCols = cols
	s.usedColSet = make(map[string]bool, len(cols))
	for _, c := range cols {
		s.usedColSet[c] = true
	}
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
		v := s.it.Value()
		row, err := DecodeRow(v, s.schema)
		if err != nil {
			batch.Put()
			return nil, err
		}
		row.StoreKey = s.currentKey
		if s.planner != nil {
			row.Planner = s.planner
		}
		row.TableName = s.table
		if s.alias != "" {
			if s.prefixedCols != nil {
				row.Cols = s.prefixedCols
				row.ColIndex = s.prefixedColIndex
			} else {
				row = prefixRowCols(row, s.alias)
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

	row := s.cloneRow(r, schema)
	if ss, ok := DT.SchemaFor(s.table); ok {
		evalVirtualCols(&row, ss)
	}
	return row, nil
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
	if s.RequestedCols != nil {
		cols := schema.cols
		types := schema.types
		rc := s.RequestedCols
		newCols := make([]string, len(rc))
		newTypes := make([]LX.TokenType, len(rc))
		newData := make([]Value, len(rc))
		for i, idx := range rc {
			newCols[i] = cols[idx]
			if idx < len(types) {
				newTypes[i] = types[idx]
			}
			newData[i] = r.Data[idx]
		}
		newIndex := make(map[string]int, len(rc)*2)
		for i, c := range newCols {
			newIndex[c] = i
		}
		out.Cols = newCols
		out.Types = newTypes
		out.Data = newData
		out.ColIndex = newIndex
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
			out = pruneRowCols(out, s.usedCols, s.usedColSet)
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
			out = prefixRowCols(out, s.alias)
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
		// REQ000501: save the raw key so Update/Delete can
		// preserve the original row key for hidden-PK DT.Tables.
		s.currentKey = s.it.Key()
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
		row.StoreKey = s.currentKey
		// REQ000366: thread the planner so subquery evals see
		// the same store/catalog.
		if s.planner != nil {
			row.Planner = s.planner
		}
		row.TableName = s.table
		if s.alias != "" {
			if s.prefixedCols != nil {
				row.Cols = s.prefixedCols
				row.ColIndex = s.prefixedColIndex
			} else {
				row = prefixRowCols(row, s.alias)
			}
			row.TableName = s.alias
		}

		// REQ000790: record index skip if indexes are available but SeqScan is used.
		if s.iu != nil && len(s.availableIdx) > 0 {
			s.iu.RecordIndexSkip(s.availableIdx[0], s.table, "SeqScan used instead of IndexScan")
		}

		// REQ001080: prune unused columns from the store-backed row.
		if s.usedCols != nil {
			row = pruneRowCols(row, s.usedCols, s.usedColSet)
		}

		// REQ001338: evaluate VIRTUAL generated column expressions.
		evalVirtualCols(&row, s.schema)

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
func (s *SeqScan) decodeRowBuffered(data []byte) (Row, error) {
	if s.rowArena == nil {
		s.rowArena = &DT.RowArena{}
		s.rowArena.Init(engineBatchSize, len(s.schema.Cols))
	}
	n := len(s.schema.Cols)
	if n == 0 {
		return Row{}, nil
	}
	row := s.rowArena.AllocRow(n, s.schema)
	if row == nil {
		return Row{}, errors.New("op: arena alloc failed")
	}
	err := DT.DecodeRowInto(row, data, s.schema)
	if err != nil {
		return Row{}, err
	}
	return *row, nil
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
	// REQ001195: clear point-lookup state so plan cache reuse
	// with different literal values triggers a fresh scan instead
	// of using stale pre-computed row indices.
	s.pointLookupRows = nil
	s.pointLookupOnce = false
	s.pointLookupPos = 0
	return nil
}

// evalVirtualCols evaluates VIRTUAL generated column expressions for
// the given row. The schema's Generated field has the expression for
// each column; non-nil entries are evaluated against the row's other
// columns. REQ001338.
func evalVirtualCols(row *Row, schema *DT.StoreSchema) {
	if schema == nil || len(schema.Generated) == 0 {
		return
	}
	for i, expr := range schema.Generated {
		if expr == nil {
			continue
		}
		if i >= len(row.Data) {
			continue
		}
		v, err := EV.EvalValue(expr, row, nil)
		if err == nil {
			row.Data[i] = v
		}
	}
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

// pruneRowCols filters row Data/Cols/Types to only include columns
// in usedCols. Returns the pruned row. REQ001080.
func pruneRowCols(row Row, usedCols []string, usedSet map[string]bool) Row {
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
	newCols := make([]string, 0, len(usedCols))
	newTypes := make([]LX.TokenType, 0, len(usedCols))
	newData := make([]Value, 0, len(usedCols))
	newIndex := make(map[string]int, len(usedCols)*2)
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