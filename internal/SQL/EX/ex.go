package EX

import (
	"context"
	"errors"
)

var ErrNotImplemented = errors.New("ex: not implemented")
var ErrNoRows = errors.New("ex: no rows")
var ErrClosed = errors.New("ex: operator closed")

type Operator interface {
	Next(ctx context.Context) (Row, error)
	Close() error
}

type Row struct {
	Cols  []string
	Types []int
	Data  [][]byte
}

type Result struct {
	RowsAffected int64
	LastInsertID uint64
}
