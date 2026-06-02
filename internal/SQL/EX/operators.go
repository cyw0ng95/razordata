package EX

import (
	"context"
)

type SeqScan struct {
	table string
}

func NewSeqScan(table string) *SeqScan {
	return &SeqScan{table: table}
}

func (s *SeqScan) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNotImplemented
}

func (s *SeqScan) Close() error {
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
