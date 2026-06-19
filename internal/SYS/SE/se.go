// Package SE implements the Session cluster: per-connection state
// (transaction, deadline, params, stats) with a per-session mutex.
package SE

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
	"github.com/cyw0ng95/razordata/internal/SYS/TX"
	vl "github.com/cyw0ng95/razordata/internal/TXN/VL"
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
	engine         *SY.Engine
	id             uint64
	txn            AP.Transaction
	mu             sync.Mutex
	deadline       atomic.Value      // time.Time
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

// sessionStateMu guards the sessionState map. Each key is a session ID
// (from sessionIDSeq), and each value holds atomic counters that are
// read by evalFunction for changes(), last_insert_rowid(), and
// total_changes().
type sessionState struct {
	// REQ000610: changesCount, lastInsertRowID, and totalChanges
	// are read from arbitrary goroutines (evalFunction runs on
	// the query goroutine; the executor updates them from the
	// same goroutine in practice, but isolation across multiple
	// session users needs explicit atomicity). Plain int64 read
	// while another goroutine writes is a data race per the Go
	// memory model.
	changesCount    atomic.Int64
	lastInsertRowID atomic.Int64
	totalChanges    atomic.Int64
}

var sessionStateMu sync.RWMutex
var sessionStateMap = make(map[uint64]*sessionState)

// getOrCreateSessionState returns the per-session atomic counter block
// for the given session ID, creating it on first access.
func getOrCreateSessionState(id uint64) *sessionState {
	// Fast path: read without lock
	if s, ok := getExistingSessionState(id); ok {
		return s
	}
	// Slow path: create under lock
	sessionStateMu.Lock()
	defer sessionStateMu.Unlock()
	if s, ok := sessionStateMap[id]; ok {
		return s
	}
	s := &sessionState{}
	sessionStateMap[id] = s
	return s
}

func getExistingSessionState(id uint64) (*sessionState, bool) {
	sessionStateMu.RLock()
	defer sessionStateMu.RUnlock()
	s, ok := sessionStateMap[id]
	return s, ok
}

// removeSessionState deletes the per-session state entry. REQ000610:
// getOrCreateSessionState only ever inserts; without a removal path
// the global sessionStateMap grows monotonically under connection
// churn. Sessions that have been pooled back into sessionPool (via
// Close) call this so the map stays bounded.
func removeSessionState(id uint64) {
	sessionStateMu.Lock()
	defer sessionStateMu.Unlock()
	delete(sessionStateMap, id)
}

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
	s.isolationLevel = AP.IsolationReadCommitted // REQ000061: default to read-committed
	s.deadline.Store(time.Time{})
	s.stats.queryCount.Store(0)
	s.stats.rowsReturned.Store(0)
	s.stats.bytesRead.Store(0)
	s.stats.bytesWritten.Store(0)
}

// ID returns the session's unique identifier (used for tracing).
func (s *Session) ID() uint64 { return s.id }

// Query runs a SELECT and returns a streaming row iterator. The
// caller MUST call Close on the returned *AP.Rows to release
// underlying plan resources. After Next returns ErrNoRows the
// iterator is auto-closed.
// REQ000348.
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
	exe.SetSessionID(s.id)
	stream, err := exe.QueryStream(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	next := func() (AP.Row, error) {
		row, err := stream.Next()
		if err != nil {
			if err == EX.ErrNoRows {
				return AP.Row{}, AP.ErrNoRows
			}
			return AP.Row{}, err
		}
		return AP.Row{Cols: row.Cols, Types: row.Types, Data: row.Data}, nil
	}
	closer := func() error { return stream.Close() }
	return AP.NewRows(stream.Cols(), stream.Types(), next, closer), nil
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
	exe.SetSessionID(s.id)
	res, err := exe.Exec(ctx, sql, args...)
	if err != nil {
		return AP.Result{}, err
	}
	// REQ000385/394/411: update per-session change counters from
	// the exec result.  ChangesCount is the rows affected by the
	// last DML; totalChangesCount is cumulative; LastInsertRowID
	// is the rowid of the most recent INSERT (or 0 for UPDATE/DELETE).
	st := getOrCreateSessionState(s.id)
	if res.RowsAffected > 0 {
		st.changesCount.Store(int64(res.RowsAffected))
		st.totalChanges.Add(int64(res.RowsAffected))
	}
	if res.LastInsertID > 0 {
		st.lastInsertRowID.Store(int64(res.LastInsertID))
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
	// REQ000061: propagate session isolation level to transaction
	if tx, ok := t.(*TX.Transaction); ok {
		tx.SetIsolationLevel(s.isolationLevel)
	}
	return t, nil
}

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
	return s.txn.ReleaseSavepoint(ctx, name)
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

// ChangesCount returns the number of rows modified by the last
// INSERT/UPDATE/DELETE executed on this session. REQ000385.
func (s *Session) ChangesCount() int64 {
	st, ok := getExistingSessionState(s.id)
	if !ok || st == nil {
		return 0
	}
	return st.changesCount.Load()
}

// LastInsertRowID returns the rowid of the most recently inserted row
// on this session. REQ000394.
func (s *Session) LastInsertRowID() int64 {
	st, ok := getExistingSessionState(s.id)
	if !ok || st == nil {
		return 0
	}
	return st.lastInsertRowID.Load()
}

// TotalChangesCount returns the cumulative number of rows modified
// since this session was created. REQ000411.
func (s *Session) TotalChangesCount() int64 {
	st, ok := getExistingSessionState(s.id)
	if !ok || st == nil {
		return 0
	}
	return st.totalChanges.Load()
}

// CurrentTS returns the current logical timestamp (REQ000255).
func (s *Session) CurrentTS() uint64 {
	return vl.GetCurrentTS()
}

// SetSnapshot sets the per-statement snapshot timestamp on the engine
// and executor (REQ000255). Pass 0 to disable.
func (s *Session) SetSnapshot(ts uint64) {
	s.engine.SetSnapshot(ts)
}

// lock acquires the session mutex and, if a deadline is set, returns
// AP.ErrDeadlineExceeded when the deadline has passed. The mutex is
// released by the caller via Unlock.
func (s *Session) lock(ctx context.Context) error {
	// REQ000627: check deadline BEFORE blocking on s.mu.Lock() so
	// an already-expired session does not contend for the mutex.
	if dl, _ := s.deadline.Load().(time.Time); !dl.IsZero() && time.Now().After(dl) {
		return AP.ErrDeadlineExceeded
	}
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
// SY↔SE import cycle. It also wires the session counter accessor
// into the EX package for evalFunction to call changes(),
// last_insert_rowid(), and total_changes().
func init() {
	SY.RegisterSession(func(e *SY.Engine) AP.Session {
		return NewSession(e)
	})
	// Wire the session counter accessor for REQ000385/394/411.
	// This is a no-op closure since getOrCreateSessionState is
	// accessible from this package.
	EX.SetSessionCounterAccessor(&sessionStateAccessor{})
}

// sessionStateAccessor implements EX.SessionCounterAccessor by
// delegating to the session state map.
type sessionStateAccessor struct{}

func (s *sessionStateAccessor) GetChangesCount(sessionID uint64) int64 {
	st, _ := getExistingSessionState(sessionID)
	if st == nil {
		return 0
	}
	return st.changesCount.Load()
}

func (s *sessionStateAccessor) GetLastInsertRowID(sessionID uint64) int64 {
	st, _ := getExistingSessionState(sessionID)
	if st == nil {
		return 0
	}
	return st.lastInsertRowID.Load()
}

func (s *sessionStateAccessor) GetTotalChangesCount(sessionID uint64) int64 {
	st, _ := getExistingSessionState(sessionID)
	if st == nil {
		return 0
	}
	return st.totalChanges.Load()
}
