package DS

import (
	"context"
	"database/sql/driver"

	"github.com/cyw0ng95/razordata/internal/SYS/ST"
)

// Stmt is a database/sql prepared statement backed by an ST.Stmt.
type Stmt struct {
	conn *Conn
	stmt *ST.Stmt
}

// Close releases the prepared statement.
func (s *Stmt) Close() error {
	if s == nil || s.stmt == nil {
		return nil
	}
	return s.stmt.Close()
}

// NumInput returns the number of `?` placeholders the SQL contains.
func (s *Stmt) NumInput() int {
	if s == nil || s.stmt == nil {
		return -1
	}
	return len(s.stmt.ParamTypes())
}

// Exec runs a DML/DDL statement with the given bound arguments.
// Returns the driver.Result with last insert id and rows affected.
func (s *Stmt) Exec(args []driver.Value) (driver.Result, error) {
	anyArgs := make([]any, len(args))
	for i, v := range args {
		anyArgs[i] = toDriverValue(v)
	}
	res, err := s.stmt.Exec(context.Background(), anyArgs...)
	if err != nil {
		return nil, err
	}
	return Result{lastID: int64(res.LastInsertID), n: res.RowsAffected}, nil
}

// Query runs a SELECT statement and returns the materialized rows.
// The driver.Rows must be drained or Close'd by the caller.
func (s *Stmt) Query(args []driver.Value) (driver.Rows, error) {
	anyArgs := make([]any, len(args))
	for i, v := range args {
		anyArgs[i] = toDriverValue(v)
	}
	apRows, err := s.stmt.Query(context.Background(), anyArgs...)
	if err != nil {
		return nil, err
	}
	return newRows(apRows), nil
}
