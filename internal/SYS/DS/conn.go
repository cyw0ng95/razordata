package DS

import (
	"context"
	"database/sql/driver"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	v1 "github.com/cyw0ng95/razordata/internal/SYS/SY"
)

// Conn is a single database/sql connection backed by an AP.Session.
// The underlying engine is shared across all Conn instances for the
// same DSN via the package-level engineCache.
type Conn struct {
	eng     *v1.Engine
	session AP.Session
	cfg     Config
}

// Prepare returns a prepared statement bound to the connection.
func (c *Conn) Prepare(query string) (driver.Stmt, error) {
	return Prepare(c, query)
}

// Close rolls back the active session and releases the cached
// engine. The engine itself is closed only when the last Conn for
// its DSN closes.
func (c *Conn) Close() error {
	if c == nil {
		return nil
	}
	if c.session != nil {
		_ = c.session.Rollback(context.Background())
		c.session = nil
	}
	releaseEngine(c.cfg)
	return nil
}

// Begin starts a transaction.
func (c *Conn) Begin() (driver.Tx, error) {
	if c == nil || c.eng == nil {
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
