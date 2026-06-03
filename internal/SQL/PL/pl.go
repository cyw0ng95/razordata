package PL

// Stmt is a forward-declared prepared statement interface. The
// concrete implementation lives in EX and is returned by an
// Executor.
type Stmt interface {
	Query(args ...any) (*Rows, error)
	Exec(args ...any) (Result, error)
	Close() error
}

type Rows struct {
	Cols  []string
	Types []int
}

type Result struct {
	RowsAffected int64
	LastInsertID uint64
}
