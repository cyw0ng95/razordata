package driver

import (
	"context"
	"database/sql/driver"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SE"
	v1 "github.com/cyw0ng95/razordata/internal/SYS/SY"
)

// Conn is a single database/sql connection backed by an AP.Session.
// Each Conn owns its own engine and session; the engine survives
// individual connection close/reopen so the database/sql connection
// pool can transparently re-establish connections without losing
// in-memory state.
type Conn struct {
	eng     *v1.Engine
	session AP.Session
}

// Prepare returns a prepared statement bound to the connection.
func (c *Conn) Prepare(query string) (driver.Stmt, error) {
	return Prepare(c, query)
}

// Close rolls back the active session. The engine is left alive
// so future Driver.Open calls (from the connection pool) can
// re-create a session on the same engine. The engine is closed
// when the *sql.DB is fully closed by the caller.
func (c *Conn) Close() error {
	if c == nil || c.session == nil {
		return nil
	}
	ctx := context.Background()
	_ = c.session.Rollback(ctx)
	c.session = nil
	c.eng = nil
	return nil
}

// ExecContext executes a query that doesn't returns rows (INSERT, UPDATE, DELETE, DDL).
// Implements driver.ExecerContext. Auto-commits if not in an explicit transaction.
func (c *Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c == nil || c.session == nil {
		return nil, AP.ErrNotOpen
	}
	// Convert NamedValue to Value for the statement path.
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}

	// Auto-commit: if no explicit transaction is active, wrap execution in one.
	sess, ok := c.session.(*SE.Session)
	if !ok || !sess.HasActiveTxn() {
		// Begin an auto-commit transaction.
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
		// Clear the session's txn reference after auto-commit.
		sess.ClearTxn()
		return res, nil
	}

	// In an explicit transaction: prepare and execute with TxWriter set.
	// REQ000641: the TxWriter must be set so in-memory tables record
	// pre-tx snapshots for rollback.
	sess.SetTxWriterForTxn()
	defer sess.ClearTxWriter()
	stmt, err := Prepare(c, query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return stmt.Exec(vals)
}

// Exec executes a query that doesn't return rows (INSERT, UPDATE, DELETE, DDL).
// Auto-commits if not in an explicit transaction.
func (c *Conn) Exec(query string, args []driver.Value) (driver.Result, error) {
	named := make([]driver.NamedValue, len(args))
	for i, v := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return c.ExecContext(context.Background(), query, named)
}

// Begin starts a transaction.
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

// Result implements driver.Result for non-RETURNING Exec results.
type Result struct {
	lastID int64
	n      int64
}

// LastInsertId returns the last inserted rowid (0 if not applicable).
func (r Result) LastInsertId() (int64, error) { return r.lastID, nil }

// RowsAffected returns the number of rows affected.
func (r Result) RowsAffected() (int64, error) { return r.n, nil }
