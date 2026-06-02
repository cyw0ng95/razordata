package PL

import (
	"context"
)

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

type Stmt interface {
	Query(ctx context.Context, args ...any) (*Rows, error)
	Exec(ctx context.Context, args ...any) (Result, error)
	Close() error
}

type Rows struct {
	Cols  []string
	Types []int
}
