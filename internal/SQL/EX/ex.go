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
	Data  []interface{}
}

func (r *Row) Lookup(name string) (interface{}, bool) {
	for i, c := range r.Cols {
		if c == name {
			if i < len(r.Data) {
				return r.Data[i], true
			}
			return nil, false
		}
	}
	return nil, false
}

type Result struct {
	RowsAffected int64
	LastInsertID uint64
}

type Rows struct {
	Cols  []string
	Types []int
}
