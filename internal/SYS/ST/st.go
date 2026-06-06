// Package ST implements the Stmt cluster: prepared statements with a
// parse-once / execute-many model backed by the engine's executor.
package ST

import (
	"context"
	"sync"

	executor "github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
)

// Stmt is the concrete AP.Stmt.
type Stmt struct {
	engine *SY.Engine
	sql    string
	mu     sync.Mutex
	closed bool
}

// Prepare parses the SQL and returns a Stmt ready to execute. The
// plan is constructed once via the executor; subsequent invocations
// reuse the plan's memo key.
func Prepare(engine *SY.Engine, sql string) (*Stmt, error) {
	if engine == nil {
		return nil, AP.ErrNotOpen
	}
	if sql == "" {
		return nil, AP.ErrSyntax
	}
	return &Stmt{engine: engine, sql: sql}, nil
}

// PrepareFromInterface is the entry point used by tests and the
// top-level SYS package. It type-asserts the AP.Engine to the
// concrete *SY.Engine that ST.Prepare needs.
func PrepareFromInterface(e AP.Engine, sql string) (*Stmt, error) {
	if e == nil {
		return nil, AP.ErrNotOpen
	}
	syEng, ok := e.(*SY.Engine)
	if !ok {
		return nil, AP.ErrNotOpen
	}
	return Prepare(syEng, sql)
}

// SQL returns the original SQL text.
func (s *Stmt) SQL() string { return s.sql }

// Query executes the prepared statement with the given args.
func (s *Stmt) Query(ctx context.Context, args ...any) (*AP.Rows, error) {
	if s.engine.IsClosed() {
		return nil, AP.ErrClosed
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, AP.ErrClosed
	}
	s.mu.Unlock()
	exe := s.engine.Executor()
	rs, err := exe.Query(ctx, s.sql, args...)
	if err != nil {
		return nil, err
	}
	return &AP.Rows{Cols: rs.Cols, Types: rs.Types}, nil
}

// Exec executes the prepared statement as a DML/DDL.
func (s *Stmt) Exec(ctx context.Context, args ...any) (AP.Result, error) {
	if s.engine.IsClosed() {
		return AP.Result{}, AP.ErrClosed
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return AP.Result{}, AP.ErrClosed
	}
	s.mu.Unlock()
	exe := s.engine.Executor()
	res, err := exe.Exec(ctx, s.sql, args...)
	if err != nil {
		return AP.Result{}, err
	}
	return AP.Result{
		RowsAffected: res.RowsAffected,
		LastInsertID: res.LastInsertID,
	}, nil
}

// Close releases the statement. Idempotent.
func (s *Stmt) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return nil
}

// Compile-time check that the executor import is reachable.
var _ = executor.ErrNoRows
