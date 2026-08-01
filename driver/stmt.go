package driver

import (
	"context"
	"database/sql/driver"

	"github.com/cyw0ng95/razordata/internal/SYS/ST"
)

type Stmt struct {
	conn *Conn
	stmt *ST.Stmt
}

func (s *Stmt) Close() error {
	if s == nil || s.stmt == nil {
		return nil
	}
	return s.stmt.Close()
}

func (s *Stmt) NumInput() int {
	if s == nil || s.stmt == nil {
		return -1
	}
	return len(s.stmt.ParamTypes())
}

func (s *Stmt) Exec(args []driver.Value) (driver.Result, error) {
	// REQ002291: bind through ParamBinder so the prepared-statement
	// path shares the same coercion + NumInput validation as the
	// ad-hoc ExecContext path.
	anyArgs, err := ParamBinder{}.BindPrepared(args, s.NumInput())
	if err != nil {
		return nil, err
	}
	res, err := s.stmt.Exec(context.Background(), anyArgs...)
	if err != nil {
		return nil, err
	}
	return Result{lastID: int64(res.LastInsertID), n: res.RowsAffected}, nil
}

func (s *Stmt) Query(args []driver.Value) (driver.Rows, error) {
	anyArgs, err := ParamBinder{}.BindPrepared(args, s.NumInput())
	if err != nil {
		return nil, err
	}
	apRows, err := s.stmt.Query(context.Background(), anyArgs...)
	if err != nil {
		return nil, err
	}
	return newRows(apRows), nil
}
