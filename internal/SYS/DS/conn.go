package DS

import (
	"context"
	"database/sql/driver"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
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

// Begin starts a transaction.
func (c *Conn) Begin() (driver.Tx, error) {
	if c == nil || c.session == nil {
		return nil, AP.ErrNotOpen
	}
	tx, err := c.session.Begin(context.Background())
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx}, nil
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
