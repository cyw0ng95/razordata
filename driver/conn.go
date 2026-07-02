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
		return nil, AP.New(AP.KindClosed, "engine not open")
	}
	// REQ001125: route through the session so the per-session
	// counter for CHANGES() and TOTAL_CHANGES() is maintained. The
	// Prepare+stmt.Exec path goes through ST.Stmt.Exec which calls
	// s.engine.Executor() and bypasses the session layer.
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a.Value
	}
	sess, ok := c.session.(*SE.Session)
	if !ok || !sess.HasActiveTxn() {
		tx, err := c.session.Begin(ctx)
		if err != nil {
			return nil, err
		}
		// Roll back the auto-begin so Session.Exec sees an inactive
		// txn path (it acquires the session lock itself).
		if err := tx.Rollback(ctx); err != nil {
			return nil, err
		}
		sess.ClearTxn()
		res, err := c.session.Exec(ctx, query, anyArgs...)
		if err != nil {
			return nil, err
		}
		return Result{lastID: int64(res.LastInsertID), n: res.RowsAffected}, nil
	}
	sess.SetTxWriterForTxn()
	defer sess.ClearTxWriter()
	res, err := c.session.Exec(ctx, query, anyArgs...)
	if err != nil {
		return nil, err
	}
	return Result{lastID: int64(res.LastInsertID), n: res.RowsAffected}, nil
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
		return nil, AP.New(AP.KindClosed, "engine not open")
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

func (r Result) LastInsertId() (int64, error) { return r.lastID, nil }
func (r Result) RowsAffected() (int64, error) { return r.n, nil }
