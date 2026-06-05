package EX

import (
	"context"
	"errors"
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
	i.pos = 0
	i.rows = nil
	return nil
}
