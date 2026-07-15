package ST

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
)

type Stmt struct {
	engine     *SY.Engine
	sql        string
	mu         sync.Mutex
	closed     atomic.Bool
	paramTypes []int

	// REQ001422: compiled plan cache — set after first Exec/Query,
	// reused on subsequent calls to skip re-parsing and re-planning.
	prepared *EX.CompiledPlan
}

func Prepare(engine *SY.Engine, sql string) (*Stmt, error) {
	if engine == nil {
		return nil, AP.New(AP.KindClosed, "engine not open")
	}
	if sql == "" {
		return nil, AP.New(AP.KindSyntax, "syntax error")
	}
	s := &Stmt{engine: engine, sql: sql}
	s.paramTypes = extractParamTypes(engine, sql)
	return s, nil
}

func PrepareFromInterface(e AP.Engine, sql string) (*Stmt, error) {
	if e == nil {
		return nil, AP.New(AP.KindClosed, "engine not open")
	}
	syEng, ok := e.(*SY.Engine)
	if !ok {
		return nil, AP.New(AP.KindClosed, "engine not open")
	}
	return Prepare(syEng, sql)
}

func (s *Stmt) SQL() string { return s.sql }

func (s *Stmt) ParamTypes() []int {
	out := make([]int, len(s.paramTypes))
	copy(out, s.paramTypes)
	return out
}

type argTypeError struct {
	ArgIdx  int
	GoType  reflect.Type
	SQLType ls.ColumnType
}

func (e *argTypeError) Error() string {
	goType := "<nil>"
	if e.GoType != nil {
		goType = e.GoType.String()
	}
	return fmt.Sprintf("st: arg %d (Go %s) cannot bind to column type %d", e.ArgIdx, goType, int(e.SQLType))
}

func (e *argTypeError) Unwrap() error { return nil }

func validateArgTypes(args []any, colTypes []int) error {
	if len(args) == 0 && len(colTypes) == 0 {
		return nil
	}
	if len(args) != len(colTypes) {
		return &argTypeError{ArgIdx: -1, SQLType: ls.ColumnType(len(colTypes))}
	}
	for i, a := range args {
		ct := colTypes[i]
		if ct < 0 {
			continue
		}
		if !coercible(reflect.TypeOf(a), ls.ColumnType(ct)) {
			return &argTypeError{ArgIdx: i, GoType: reflect.TypeOf(a), SQLType: ls.ColumnType(ct)}
		}
	}
	return nil
}

func coercible(goType reflect.Type, sqlType ls.ColumnType) bool {
	if goType == nil {
		return true
	}
	switch sqlType {
	case ls.CTInt, ls.CTBigInt, ls.CTTimestamp:
		switch goType.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return true
		}
	case ls.CTFloat:
		switch goType.Kind() {
		case reflect.Float32, reflect.Float64,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return true
		}
	case ls.CTBool:
		return goType.Kind() == reflect.Bool
	case ls.CTVarchar, ls.CTText, ls.CTBlob:
		switch goType.Kind() {
		case reflect.String:
			return true
		case reflect.Slice:
			return goType.Elem().Kind() == reflect.Uint8
		}
	}
	return false
}

func extractParamTypes(engine *SY.Engine, sql string) []int {
	return engine.ExtractParamTypes(sql)
}

func (s *Stmt) Query(ctx context.Context, args ...any) (*AP.Rows, error) {
	if s.engine.IsClosed() {
		return nil, AP.New(AP.KindClosed, "engine closed")
	}
	EC.BUG_ON(s.closed.Load(), "stmt.Query: use-after-close")
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		return nil, AP.New(AP.KindClosed, "engine closed")
	}
	if err := validateArgTypes(args, s.paramTypes); err != nil {
		s.mu.Unlock()
		return nil, err
	}

	exe := s.engine.Executor()

	// REQ001422: compiled plan path — reuses the operator tree.
	if s.prepared != nil {
		stream, err := exe.QueryStreamCompiled(ctx, s.prepared, args...)
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		next := func() (AP.Row, error) {
			row, err := stream.Next()
			if err != nil {
				if err == DT.ErrNoRows {
					return AP.Row{}, AP.New(AP.KindNotFound, "no more rows")
				}
				return AP.Row{}, err
			}
			return AP.Row{Cols: row.Cols, Types: row.Types, Data: row.Data}, nil
		}
		return AP.NewRows(stream.Cols(), stream.Types(), next, func() error { return stream.Close() }), nil
	}
	s.mu.Unlock()

	// First call: compile, cache, and execute.
	cp, err := exe.CompilePlan(s.sql)
	if err != nil {
		return nil, err
	}
	stream, err := exe.QueryStreamCompiled(ctx, cp, args...)
	if err != nil {
		cp.Close()
		return nil, err
	}

	// Cache the compiled plan for subsequent calls.
	s.mu.Lock()
	if s.prepared == nil {
		s.prepared = cp
	} else {
		cp.Close() // another goroutine cached first
	}
	s.mu.Unlock()

	next := func() (AP.Row, error) {
		row, err := stream.Next()
		if err != nil {
			if err == DT.ErrNoRows {
				return AP.Row{}, AP.New(AP.KindNotFound, "no more rows")
			}
			return AP.Row{}, err
		}
		return AP.Row{Cols: row.Cols, Types: row.Types, Data: row.Data}, nil
	}
	return AP.NewRows(stream.Cols(), stream.Types(), next, func() error { return stream.Close() }), nil
}

func (s *Stmt) Exec(ctx context.Context, args ...any) (AP.Result, error) {
	if s.engine.IsClosed() {
		return AP.Result{}, AP.New(AP.KindClosed, "engine closed")
	}
	EC.BUG_ON(s.closed.Load(), "stmt.Exec: use-after-close")
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		return AP.Result{}, AP.New(AP.KindClosed, "engine closed")
	}
	if err := validateArgTypes(args, s.paramTypes); err != nil {
		s.mu.Unlock()
		return AP.Result{}, err
	}

	exe := s.engine.Executor()

	// REQ001422: compiled plan path — reuses the operator tree.
	if s.prepared != nil {
		res, err := exe.ExecCompiled(ctx, s.prepared, args...)
		s.mu.Unlock()
		if err != nil {
			return AP.Result{}, err
		}
		return AP.Result{RowsAffected: res.RowsAffected, LastInsertID: res.LastInsertID}, nil
	}
	s.mu.Unlock()

	// First call: compile, execute, and cache the plan for reuse.
	cp, err := exe.CompilePlan(s.sql)
	if err != nil {
		return AP.Result{}, err
	}
	res, err := exe.ExecCompiled(ctx, cp, args...)
	if err != nil {
		cp.Close()
		return AP.Result{}, err
	}

	// Cache the compiled plan for subsequent calls.
	s.mu.Lock()
	if s.prepared == nil {
		s.prepared = cp
	} else {
		cp.Close()
	}
	s.mu.Unlock()

	return AP.Result{RowsAffected: res.RowsAffected, LastInsertID: res.LastInsertID}, nil
}

func (s *Stmt) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return nil
	}
	s.closed.Store(true)
	if s.prepared != nil {
		s.prepared.Close()
		s.prepared = nil
	}
	return nil
}

var _ = DT.ErrNoRows
var errNotEnoughArgs = errors.New("st: not enough args for `?` placeholders")
