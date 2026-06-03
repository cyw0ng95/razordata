package EX

import (
	"context"
)

type SeqScan struct {
	table string
	rows  []Row
	pos   int
}

func NewSeqScan(table string) *SeqScan {
	s := &SeqScan{table: table}
	tablesMu.RLock()
	if r, ok := tables[table]; ok {
		s.rows = r
	}
	tablesMu.RUnlock()
	return s
}

func (s *SeqScan) Next(ctx context.Context) (Row, error) {
	if s.pos >= len(s.rows) {
		return Row{}, ErrNoRows
	}
	r := s.rows[s.pos]
	s.pos++
	return r, nil
}

func (s *SeqScan) Close() error {
	s.pos = 0
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
