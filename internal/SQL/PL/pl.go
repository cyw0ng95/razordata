package PL

import (
	"github.com/cyw0ng95/razordata/internal/SQL/EX"
)

type Operator = EX.Operator

type Row = EX.Row

type Result = EX.Result

type Rows = EX.Rows

type Stmt interface {
	Query(args ...any) (*Rows, error)
	Exec(args ...any) (Result, error)
	Close() error
}
