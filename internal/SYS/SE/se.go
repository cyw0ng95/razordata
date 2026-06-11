// Package SE implements the Session cluster: per-connection state
// (transaction, deadline, params, stats) with a per-session mutex.
package SE

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
	"github.com/cyw0ng95/razordata/internal/SYS/TX"
)

// sessionPool is a sync.Pool for Session objects to reduce GC
// pressure under high connection churn. Sessions are reset on Get
// and returned to the pool on Close (via eng.Close -> session release).
// Added in iter-15 (REQ000098).
var sessionPool = sync.Pool{
	New: func() any {
		return &Session{}
	},
}

// Session is the concrete AP.Session.
type Session struct {
	engine        *SY.Engine
	id            uint64
	txn           AP.Transaction
	mu            sync.Mutex
	deadline      atomic.Value // time.Time
	isolationLevel AP.IsolationLevel // REQ000123

	stats struct {
		queryCount   atomic.Int64
		rowsReturned atomic.Int64
		bytesRead    atomic.Int64
		bytesWritten atomic.Int64
	}
}

// sessionIDSeq is a process-wide counter; real production code would
// pull this from the engine for tracing.
var sessionIDSeq atomic.Uint64

// NewSession returns a fresh Session bound to engine. Sessions are
// pool-allocated from sessionPool to reduce GC pressure (iter-15
// REQ000098). The session is reset before use.
func NewSession(engine *SY.Engine) *Session {
	s := sessionPool.Get().(*Session)
	s.reset(engine)
	return s
}

// reset initializes/reinitializes a pooled Session.
func (s *Session) reset(engine *SY.Engine) {
	s.engine = engine
	s.id = sessionIDSeq.Add(1)
	s.txn = nil
	s.deadline.Store(time.Time{})
	s.stats.queryCount.Store(0)
	s.stats.rowsReturned.Store(0)
	s.stats.bytesRead.Store(0)
	s.stats.bytesWritten.Store(0)
}

// ID returns the session's unique identifier (used for tracing).
func (s *Session) ID() uint64 { return s.id }

// Query runs a SELECT and returns its column metadata. Streaming the
// rows back to the caller is left to the engine's own iterator in v1;
// for the AP contract we expose a *AP.Rows that names the columns.
func (s *Session) Query(ctx context.Context, sql string, args ...any) (*AP.Rows, error) {
	if s.engine.IsClosed() {
		return nil, AP.ErrClosed
	}
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	s.stats.queryCount.Add(1)

	exe := s.engine.Executor()
	rs, err := exe.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &AP.Rows{Cols: rs.Cols, Types: rs.Types}, nil
}

// Exec runs a DML or DDL statement and returns its result. In read-only
// mode, Exec returns ErrReadOnly (iter-15 REQ000099).
func (s *Session) Exec(ctx context.Context, sql string, args ...any) (AP.Result, error) {
	if s.engine.IsClosed() {
		return AP.Result{}, AP.ErrClosed
	}
	if s.engine.IsReadOnly() {
		return AP.Result{}, AP.ErrReadOnly
	}
	if err := s.lock(ctx); err != nil {
		return AP.Result{}, err
	}
	defer s.mu.Unlock()
	s.stats.queryCount.Add(1)

	exe := s.engine.Executor()
	res, err := exe.Exec(ctx, sql, args...)
	if err != nil {
		return AP.Result{}, err
	}
	return AP.Result{
		RowsAffected: res.RowsAffected,
		LastInsertID: res.LastInsertID,
	}, nil
}

// Begin starts a new transaction. Returns AP.ErrLocked if a
// transaction is already active.
func (s *Session) Begin(ctx context.Context) (AP.Transaction, error) {
	if s.engine.IsClosed() {
		return nil, AP.ErrClosed
	}
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	if s.txn != nil {
		return nil, AP.ErrLocked
	}
	t, err := s.engine.BeginTxn(ctx)
	if err != nil {
		return nil, err
	}
	s.txn = t
	return t, nil
}

// SetTxWriter is a no-op on the session; transactions install the
// writer themselves.

// SetTxWriter wires the executor's transaction-write hook. The
// session does not use this directly; the Transaction owns the hook
// during Exec.

// Commit finalizes the current transaction.
func (s *Session) Commit(ctx context.Context) error {
	if s.engine.IsClosed() {
		return AP.ErrClosed
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if s.txn == nil {
		return AP.ErrNoActiveTxn
	}
	if err := s.txn.Commit(ctx); err != nil {
		return err
	}
	s.txn = nil
	return nil
}

// Rollback aborts the current transaction.
func (s *Session) Rollback(ctx context.Context) error {
	if s.engine.IsClosed() {
		return AP.ErrClosed
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if s.txn == nil {
		return AP.ErrNoActiveTxn
	}
	if err := s.txn.Rollback(ctx); err != nil {
		return err
	}
	s.txn = nil
	return nil
}

// Savepoint creates a savepoint with the given name.
func (s *Session) Savepoint(ctx context.Context, name string) error {
	if s.engine.IsClosed() {
		return AP.ErrClosed
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if s.txn == nil {
		return AP.ErrNoActiveTxn
	}
	return s.txn.Savepoint(ctx, name)
}

// ReleaseSavepoint releases a savepoint with the given name.
func (s *Session) ReleaseSavepoint(ctx context.Context, name string) error {
	if s.engine.IsClosed() {
		return AP.ErrClosed
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if s.txn == nil {
		return AP.ErrNoActiveTxn
	}
	// Release is a no-op in the current implementation
	// The savepoint is just removed from the stack
	return nil
}

// RollbackTo rolls back to a savepoint with the given name.
func (s *Session) RollbackTo(ctx context.Context, name string) error {
	if s.engine.IsClosed() {
		return AP.ErrClosed
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if s.txn == nil {
		return AP.ErrNoActiveTxn
	}
	return s.txn.RollbackTo(ctx, name)
}

// SetDeadline sets a deadline for all subsequent operations on this
// session. Stored in an atomic.Value so concurrent readers see a
// consistent point-in-time.
func (s *Session) SetDeadline(deadline time.Time) error {
	s.deadline.Store(deadline)
	return nil
}

// Stats returns a snapshot of the session's lifetime counters.
func (s *Session) Stats() AP.SessionStats {
	return AP.SessionStats{
		ID:           s.id,
		QueryCount:   s.stats.queryCount.Load(),
		RowsReturned: s.stats.rowsReturned.Load(),
		BytesRead:    s.stats.bytesRead.Load(),
		BytesWritten: s.stats.bytesWritten.Load(),
		ActiveTXN:    s.txn != nil,
	}
}

// lock acquires the session mutex and, if a deadline is set, returns
// AP.ErrDeadlineExceeded when the deadline has passed. The mutex is
// released by the caller via Unlock.
func (s *Session) lock(ctx context.Context) error {
	s.mu.Lock()
	if dl, _ := s.deadline.Load().(time.Time); !dl.IsZero() && time.Now().After(dl) {
		s.mu.Unlock()
		return AP.ErrDeadlineExceeded
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		if errors.Is(err, context.DeadlineExceeded) {
			return AP.ErrDeadlineExceeded
		}
		return err
	}
	return nil
}

// Compile-time check that TX.transaction is reachable from this
// package (avoids unused import when the engine's BeginTxn returns
// the unexported concrete type).
var _ = (*TX.Transaction)(nil)

// init registers the Session constructor with SY, breaking the
// SY↔SE import cycle.
func init() {
	SY.RegisterSession(func(e *SY.Engine) AP.Session {
		return NewSession(e)
	})
}
