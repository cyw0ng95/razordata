package SE

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQB/EX"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
	"github.com/cyw0ng95/razordata/internal/SYS/TX"
	vl "github.com/cyw0ng95/razordata/internal/TXN/VL"
)

var sessionPool = sync.Pool{New: func() any { return &Session{} }}

type Session struct {
	engine         *SY.Engine
	id             uint64
	txn            AP.Transaction
	mu             sync.Mutex
	deadline       atomic.Value
	isolationLevel AP.IsolationLevel
	stats          struct {
		queryCount   atomic.Int64
		rowsReturned atomic.Int64
		bytesRead    atomic.Int64
		bytesWritten atomic.Int64
	}
}

var sessionIDSeq atomic.Uint64

type sessionState struct {
	changesCount    atomic.Int64
	lastInsertRowID atomic.Int64
	totalChanges    atomic.Int64
}

var (
	sessionStateMu  sync.RWMutex
	sessionStateMap = make(map[uint64]*sessionState)
)

func getOrCreateSessionState(id uint64) *sessionState {
	if s, ok := getExistingSessionState(id); ok {
		return s
	}
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

func removeSessionState(id uint64) {
	sessionStateMu.Lock()
	defer sessionStateMu.Unlock()
	delete(sessionStateMap, id)
}

func NewSession(engine *SY.Engine) *Session {
	s := sessionPool.Get().(*Session)
	s.reset(engine)
	return s
}

func (s *Session) reset(engine *SY.Engine) {
	s.engine = engine
	s.id = sessionIDSeq.Add(1)
	s.txn = nil
	s.isolationLevel = AP.IsolationReadCommitted
	s.deadline.Store(time.Time{})
	s.stats.queryCount.Store(0)
	s.stats.rowsReturned.Store(0)
	s.stats.bytesRead.Store(0)
	s.stats.bytesWritten.Store(0)
}

func (s *Session) ID() uint64 { return s.id }

func wrapEXError(err error) error {
	if err == nil {
		return nil
	}
	var apErr *AP.Error
	if errors.As(err, &apErr) {
		return err
	}
	switch {
	case errors.Is(err, DT.ErrNoRows):
		return AP.New(AP.KindNotFound, "no more rows")
	case errors.Is(err, EV.ErrEval):
		return AP.WrapAt(AP.KindSyntax, "SYS/SE", AP.LayerSQL, err)
	case errors.Is(err, EV.ErrEvalDivByZero):
		return AP.WrapAt(AP.KindTypeMismatch, "SYS/SE", AP.LayerSQL, err)
	case errors.Is(err, EV.ErrTypeMismatch):
		return AP.WrapAt(AP.KindTypeMismatch, "SYS/SE", AP.LayerSQL, err)
	case errors.Is(err, EX.ErrClosed):
		return AP.WrapAt(AP.KindClosed, "SYS/SE", AP.LayerSQL, err)
	case errors.Is(err, OP.ErrTableNotRegisteredForStorage):
		return AP.WrapAt(AP.KindNotFound, "SYS/SE", AP.LayerSQL, err)
	case errors.Is(err, OP.ErrNoPKForStorage):
		return AP.WrapAt(AP.KindConstraint, "SYS/SE", AP.LayerSQL, err)
	case errors.Is(err, EV.ErrSubquery):
		return AP.WrapAt(AP.KindSyntax, "SYS/SE", AP.LayerSQL, err)
	case errors.Is(err, EV.ErrTriggerAbort):
		return AP.WrapAt(AP.KindConstraint, "SYS/SE", AP.LayerSQL, err)
	case errors.Is(err, EX.ErrMultiDatabaseNotSupported):
		return AP.WrapAt(AP.KindInvalidOptions, "SYS/SE", AP.LayerSQL, err)
	case errors.Is(err, OP.ErrNoEngine):
		return AP.WrapAt(AP.KindClosed, "SYS/SE", AP.LayerSQL, err)
	case errors.Is(err, UT.ErrDecimalOverflow):
		return AP.WrapAt(AP.KindTypeMismatch, "SYS/SE", AP.LayerSQL, err)
	case errors.Is(err, UT.ErrDecimalScale):
		return AP.WrapAt(AP.KindTypeMismatch, "SYS/SE", AP.LayerSQL, err)
	default:
		return AP.WrapAt(AP.KindIO, "SYS/SE", AP.LayerSQL, err)
	}
}

func (s *Session) Query(ctx context.Context, sql string, args ...any) (*AP.Rows, error) {
	if s.engine.IsClosed() {
		return nil, AP.New(AP.KindClosed, "engine closed")
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
		return nil, wrapEXError(err)
	}
	next := func() (AP.Row, error) {
		row, err := stream.Next()
		if err != nil {
			if err == DT.ErrNoRows {
				return AP.Row{}, AP.New(AP.KindNotFound, "no more rows")
			}
			return AP.Row{}, wrapEXError(err)
		}
		// REQ000862: AP.Row.Data is now []AP.Value (same type as DT.Row.Data),
		// so no boxing conversion is needed. Direct assignment eliminates
		// the per-row []any allocation that was 53% of join memory.
		return AP.Row{Cols: row.Cols, Types: row.Types, Data: row.Data}, nil
	}
	// REQ001056: wrap with result row limit when set.
	if s.engine.MaxResultRows() > 0 {
		origNext := next
		var rowCount int64
		limit := s.engine.MaxResultRows()
		next = func() (AP.Row, error) {
			rowCount++
			if rowCount > limit {
				return AP.Row{}, AP.New(AP.KindNotFound, "no more rows")
			}
			return origNext()
		}
	}
	return AP.NewRows(stream.Cols(), stream.Types(), next, func() error { return stream.Close() }), nil
}

func (s *Session) Exec(ctx context.Context, sql string, args ...any) (AP.Result, error) {
	if s.engine.IsClosed() {
		return AP.Result{}, AP.New(AP.KindClosed, "engine closed")
	}
	if s.engine.IsReadOnly() {
		return AP.Result{}, AP.New(AP.KindReadOnly, "read-only")
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
		return AP.Result{}, wrapEXError(err)
	}
	st := getOrCreateSessionState(s.id)
	if res.RowsAffected > 0 {
		st.changesCount.Store(int64(res.RowsAffected))
		st.totalChanges.Add(int64(res.RowsAffected))
	}
	if res.LastInsertID > 0 {
		st.lastInsertRowID.Store(int64(res.LastInsertID))
	}
	return AP.Result{RowsAffected: res.RowsAffected, LastInsertID: res.LastInsertID}, nil
}

func (s *Session) Begin(ctx context.Context) (AP.Transaction, error) {
	if s.engine.IsClosed() {
		return nil, AP.New(AP.KindClosed, "engine closed")
	}
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	if s.txn != nil {
		return nil, AP.New(AP.KindLocked, "resource locked")
	}
	t, err := s.engine.BeginTxn(ctx)
	if err != nil {
		return nil, wrapEXError(err)
	}
	s.txn = t
	if tx, ok := t.(*TX.Transaction); ok {
		tx.SetIsolationLevel(s.isolationLevel)
		tx.SetOnFinish(func() { s.ClearTxn() })
	}
	return t, nil
}

func (s *Session) HasActiveTxn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.txn != nil
}

func (s *Session) SetTxWriterForTxn() {
	s.mu.Lock()
	txn := s.txn
	s.mu.Unlock()
	if txn != nil {
		if tw, ok := txn.(DT.TxWriter); ok {
			exe := s.engine.Executor()
			exe.SetTxWriter(tw)
		}
	}
}

func (s *Session) ClearTxWriter() {
	exe := s.engine.Executor()
	exe.ClearTxWriter()
}

func (s *Session) Commit(ctx context.Context) error {
	if s.engine.IsClosed() {
		return AP.New(AP.KindClosed, "engine closed")
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if s.txn == nil {
		return AP.New(AP.KindConstraint, "no active transaction")
	}
	if err := s.txn.Commit(ctx); err != nil {
		return wrapEXError(err)
	}
	s.txn = nil
	return nil
}

func (s *Session) Rollback(ctx context.Context) error {
	if s.engine.IsClosed() {
		return AP.New(AP.KindClosed, "engine closed")
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if s.txn == nil {
		return AP.New(AP.KindConstraint, "no active transaction")
	}
	if err := s.txn.Rollback(ctx); err != nil {
		return wrapEXError(err)
	}
	s.txn = nil
	return nil
}

func (s *Session) ClearTxn() {
	if s.mu.TryLock() {
		s.txn = nil
		s.mu.Unlock()
		return
	}
}

func (s *Session) Savepoint(ctx context.Context, name string) error {
	if s.engine.IsClosed() {
		return AP.New(AP.KindClosed, "engine closed")
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if s.txn == nil {
		return AP.New(AP.KindConstraint, "no active transaction")
	}
	return wrapEXError(s.txn.Savepoint(ctx, name))
}

func (s *Session) ReleaseSavepoint(ctx context.Context, name string) error {
	if s.engine.IsClosed() {
		return AP.New(AP.KindClosed, "engine closed")
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if s.txn == nil {
		return AP.New(AP.KindConstraint, "no active transaction")
	}
	return wrapEXError(s.txn.ReleaseSavepoint(ctx, name))
}

func (s *Session) RollbackTo(ctx context.Context, name string) error {
	if s.engine.IsClosed() {
		return AP.New(AP.KindClosed, "engine closed")
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if s.txn == nil {
		return AP.New(AP.KindConstraint, "no active transaction")
	}
	return wrapEXError(s.txn.RollbackTo(ctx, name))
}

func (s *Session) SetDeadline(deadline time.Time) error {
	s.deadline.Store(deadline)
	return nil
}

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

func (s *Session) ChangesCount() int64 {
	st, ok := getExistingSessionState(s.id)
	if !ok || st == nil {
		return 0
	}
	return st.changesCount.Load()
}

func (s *Session) LastInsertRowID() int64 {
	st, ok := getExistingSessionState(s.id)
	if !ok || st == nil {
		return 0
	}
	return st.lastInsertRowID.Load()
}

func (s *Session) TotalChangesCount() int64 {
	st, ok := getExistingSessionState(s.id)
	if !ok || st == nil {
		return 0
	}
	return st.totalChanges.Load()
}

func (s *Session) CurrentTS() uint64     { return vl.GetCurrentTS() }
func (s *Session) SetSnapshot(ts uint64) { s.engine.SetSnapshot(ts) }

func (s *Session) lock(ctx context.Context) error {
	if dl, _ := s.deadline.Load().(time.Time); !dl.IsZero() && time.Now().After(dl) {
		return AP.New(AP.KindDeadlineExceeded, "deadline exceeded")
	}
	s.mu.Lock()
	if dl, _ := s.deadline.Load().(time.Time); !dl.IsZero() && time.Now().After(dl) {
		s.mu.Unlock()
		return AP.New(AP.KindDeadlineExceeded, "deadline exceeded")
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		if errors.Is(err, context.DeadlineExceeded) {
			return AP.New(AP.KindDeadlineExceeded, "deadline exceeded")
		}
		if errors.Is(err, context.Canceled) {
			return AP.Wrap(AP.KindIO, err)
		}
		return wrapEXError(err)
	}
	return nil
}

var _ = (*TX.Transaction)(nil)

func init() {
	SY.RegisterSession(func(e *SY.Engine) AP.Session { return NewSession(e) })
	DT.SetSessionCounterAccessor(&sessionStateAccessor{})
}

type sessionStateAccessor struct{}

func (s *sessionStateAccessor) ChangesCount(sessionID uint64) int64 {
	st, _ := getExistingSessionState(sessionID)
	if st == nil {
		return 0
	}
	return st.changesCount.Load()
}

func (s *sessionStateAccessor) LastInsertRowID(sessionID uint64) int64 {
	st, _ := getExistingSessionState(sessionID)
	if st == nil {
		return 0
	}
	return st.lastInsertRowID.Load()
}

func (s *sessionStateAccessor) TotalChangesCount(sessionID uint64) int64 {
	st, _ := getExistingSessionState(sessionID)
	if st == nil {
		return 0
	}
	return st.totalChanges.Load()
}
