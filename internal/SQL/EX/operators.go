package EX

import (
	"context"
)

type SeqScan struct {
	table string
	pos   int
	rows  []Row
}

func NewSeqScan(table string) *SeqScan {
	return &SeqScan{table: table}
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

func (s *SeqScan) Close() error {
	s.pos = 0
	s.rows = nil
	return nil
}

type IndexScan struct {
	table      string
	idx        string
	rangeStart []byte
	rangeEnd   []byte
}

func NewIndexScan(table, idx string, rangeStart, rangeEnd []byte) *IndexScan {
	return &IndexScan{
		table:      table,
		idx:        idx,
		rangeStart: rangeStart,
		rangeEnd:   rangeEnd,
	}
}

func (i *IndexScan) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNotImplemented
}

func (i *IndexScan) Close() error {
	return nil
}
