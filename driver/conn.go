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
		return nil, AP.New(AP.KindClosed, "engine closed")
	}
	// REQ002291: route parameter binding through ParamBinder so the
	// ad-hoc Exec path shares the same coercion as the prepared Stmt path.
	anyArgs := ParamBinder{}.Bind(args)
	// REQ001125: route through the session so the per-session
	// counter for CHANGES() and TOTAL_CHANGES() is maintained.
	// REQ001419: Session.Exec internally handles TxWriter for active
	// transactions; no driver-level SetTxWriterForTxn needed.
	res, err := c.session.Exec(ctx, query, anyArgs...)
	if err != nil {
		return nil, err
	}
	return Result{lastID: int64(res.LastInsertID), n: res.RowsAffected}, nil
}

func (c *Conn) Exec(query string, args []driver.Value) (driver.Result, error) {
	return c.ExecContext(context.Background(), query, ToNamedValues(args))
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
