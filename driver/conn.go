package driver

import (
	"context"
	"database/sql/driver"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SE"
	v1 "github.com/cyw0ng95/razordata/internal/SYS/SY"
)

type Conn struct {
	eng     *v1.Engine
	session AP.Session
}

func (c *Conn) Prepare(query string) (driver.Stmt, error) { return Prepare(c, query) }

func (c *Conn) Close() error {
	if c == nil || c.session == nil {
		return nil
	}
	_ = c.session.Rollback(context.Background())
	c.session = nil
	c.eng = nil
	return nil
}

func (c *Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c == nil || c.session == nil {
		return nil, AP.ErrNotOpen
	}
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	sess, ok := c.session.(*SE.Session)
	if !ok || !sess.HasActiveTxn() {
		tx, err := c.session.Begin(ctx)
		if err != nil {
			return nil, err
		}
		stmt, err := Prepare(c, query)
		if err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}
		res, err := stmt.Exec(vals)
		if err != nil {
			_ = stmt.Close()
			_ = tx.Rollback(ctx)
			return nil, err
		}
		if err := stmt.Close(); err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		sess.ClearTxn()
		return res, nil
	}
	sess.SetTxWriterForTxn()
	defer sess.ClearTxWriter()
	stmt, err := Prepare(c, query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return stmt.Exec(vals)
}

func (c *Conn) Exec(query string, args []driver.Value) (driver.Result, error) {
	named := make([]driver.NamedValue, len(args))
	for i, v := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return c.ExecContext(context.Background(), query, named)
}

func (c *Conn) Begin() (driver.Tx, error) {
	if c == nil || c.session == nil {
		return nil, AP.ErrNotOpen
	}
	tx, err := c.session.Begin(context.Background())
	if err != nil {
		return nil, err
	}
	sess, _ := c.session.(*SE.Session)
	return &Tx{tx: tx, session: sess}, nil
}

type Result struct {
	lastID int64
	n      int64
}

func (r Result) LastInsertId() (int64, error)  { return r.lastID, nil }
func (r Result) RowsAffected() (int64, error) { return r.n, nil }
