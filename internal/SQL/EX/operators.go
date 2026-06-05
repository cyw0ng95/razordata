package EX

import (
	"context"
	"errors"

	sc "github.com/cyw0ng95/razordata/internal/ENG/SC"
)

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
}

func NewSeqScan(table string) *SeqScan {
	return &SeqScan{table: table}
}

// NewSeqScanWithStore builds a SeqScan that reads from the engine instead
// of the in-memory table registry. The schema must have been registered.
func NewSeqScanWithStore(store Store, table string) (*SeqScan, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, errors.New("ex: table not registered for storage: " + table)
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
	return r, nil
}

func (s *SeqScan) nextFromStore(ctx context.Context) (Row, error) {
	if s.it == nil {
		s.it = s.store.NewIterator(s.prefix)
	}
	for s.it.Next() {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		v := s.it.Value()
		row, err := decodeRow(v, s.schema)
		if err != nil {
			return Row{}, err
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
	rows []Row
	pos  int

	// Real-seek path (iter-10). When pkIndex is non-nil, the
	// IndexScan uses the primary-key index to fetch rows by
	// primary key instead of scanning the full table prefix.
	pkIndex   PKIndex
	pkTableID uint64
	pkTypes   []sc.ColumnType
	pkValues  [][]byte
	pkRangeLo [][]byte
	pkRangeHi [][]byte
	pkRangeIt PKIndexIterator
	rowKey    []byte
}

func NewIndexScan(table, idx string, rangeStart, rangeEnd []byte) *IndexScan {
	return &IndexScan{
		table:      table,
		idx:        idx,
		rangeStart: rangeStart,
		rangeEnd:   rangeEnd,
	}
}

// NewIndexScanWithStore builds an IndexScan that reads through the
// engine via a prefix scan. Kept for backward compatibility with
// the v1 plan-select path; new code should use
// NewIndexScanWithStoreAndIndex.
func NewIndexScanWithStore(store Store, table, idx string) (*IndexScan, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, errors.New("ex: table not registered: " + table)
	}
	return &IndexScan{
		table:  table,
		idx:    idx,
		store:  store,
		schema: ss,
		prefix: tablePrefix(table),
	}, nil
}

// NewIndexScanWithStoreAndIndex builds an IndexScan that uses the
// primary-key index for seek (or range) lookups. The PK values
// are the encoded column values to seek for; pkTableID identifies
// the table in the PK index.
//
// If pkRangeHi is nil, this is a point-lookup (single row
// expected); otherwise it's a range scan.
func NewIndexScanWithStoreAndIndex(store Store, pkIndex PKIndex, table, idx string, pkTableID uint64, pkValues, pkRangeLo, pkRangeHi [][]byte) (*IndexScan, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, errors.New("ex: table not registered: " + table)
	}
	return &IndexScan{
		table:     table,
		idx:       idx,
		store:     store,
		schema:    ss,
		prefix:    tablePrefix(table),
		pkIndex:   pkIndex,
		pkTableID: pkTableID,
		pkValues:  pkValues,
		pkRangeLo: pkRangeLo,
		pkRangeHi: pkRangeHi,
	}, nil
}

// PKIndexIterator is defined in store.go as a type alias for
// tb.Iterator. The IndexScan uses it via the package-level alias.

// nextFromIndex performs the real-seek path. It first opens a
// range iterator on the PK index (or, for a point lookup, uses
// Seek directly) and then fetches each row via the engine's
// Get-by-rowKey. Tombstoned rows are skipped silently.
func (i *IndexScan) nextFromIndex(ctx context.Context) (Row, error) {
	if i.pkIndex == nil {
		return Row{}, ErrNoRows
	}
	// Point-lookup path.
	if i.pkRangeHi == nil && i.pkValues != nil {
		if i.rowKey != nil {
			// Already resolved.
			return i.decodeResolvedRow(ctx)
		}
		rowKey, found, err := i.pkIndex.Seek(i.pkTableID, i.pkTypes, i.pkValues)
		if err != nil {
			return Row{}, err
		}
		if !found {
			return Row{}, ErrNoRows
		}
		i.rowKey = rowKey
		return i.decodeResolvedRow(ctx)
	}
	// Range-scan path.
	if i.pkRangeIt == nil {
		it, err := i.pkIndex.Range(i.pkTableID, i.pkTypes, i.pkRangeLo, i.pkRangeHi)
		if err != nil {
			return Row{}, err
		}
		i.pkRangeIt = wrapPKIndexIterator(it)
	}
	for i.pkRangeIt.Next() {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		i.rowKey = i.pkRangeIt.Value()
		return i.decodeResolvedRow(ctx)
	}
	if err := i.pkRangeIt.Err(); err != nil {
		return Row{}, err
	}
	return Row{}, ErrNoRows
}

// decodeResolvedRow fetches the row from the store using the
// resolved rowKey, decodes it, and clears the resolution so the
// next call to nextFromIndex will produce the next row (or close
// the iteration in the range case).
func (i *IndexScan) decodeResolvedRow(ctx context.Context) (Row, error) {
	if i.store == nil {
		return Row{}, ErrNoEngine
	}
	// We need to fetch by exact rowKey. The Store interface
	// doesn't expose Get; instead we open a one-row iterator
	// with a prefix equal to the rowKey. This is a temporary
	// shim; iter-10 close-out will add Store.Get.
	it := i.store.NewIterator(i.rowKey)
	defer it.Close()
	if !it.Next() {
		return Row{}, ErrNoRows
	}
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	row, err := decodeRow(it.Value(), i.schema)
	if err != nil {
		return Row{}, err
	}
	// For the point-lookup path, clear the resolution so the
	// next call sees ErrNoRows. For the range-scan path, the
	// iterator's Next is called again on the next invocation.
	if i.pkRangeHi == nil {
		i.rowKey = nil
	}
	return row, nil
}

// wrapPKIndexIterator adapts a tb.Iterator (or any iterator with
// the same shape) to the PKIndexIterator interface.
func wrapPKIndexIterator(it PKIndexIterator) PKIndexIterator { return it }

func (i *IndexScan) nextFromStore(ctx context.Context) (Row, error) {
	if i.it == nil {
		i.it = i.store.NewIterator(i.prefix)
	}
	for i.it.Next() {
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

func (i *IndexScan) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if i.pkIndex != nil {
		return i.nextFromIndex(ctx)
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
		return err
	}
	if i.pkRangeIt != nil {
		_ = i.pkRangeIt.Close()
		i.pkRangeIt = nil
	}
	i.pos = 0
	i.rows = nil
	i.rowKey = nil
	return nil
}
