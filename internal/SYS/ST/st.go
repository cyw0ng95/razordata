package ST

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	executor "github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
)

type Stmt struct {
	engine     *SY.Engine
	sql        string
	mu         sync.Mutex
	closed     atomic.Bool
	paramTypes []int
}

func Prepare(engine *SY.Engine, sql string) (*Stmt, error) {
	if engine == nil {
		return nil, AP.ErrNotOpen
	}
	if sql == "" {
		return nil, AP.ErrSyntax
	}
	s := &Stmt{engine: engine, sql: sql}
	s.paramTypes = extractParamTypes(engine, sql)
	return s, nil
}

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
		return nil, AP.ErrClosed
	}
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		return nil, AP.ErrClosed
	}
	s.mu.Unlock()
	if err := validateArgTypes(args, s.paramTypes); err != nil {
		return nil, err
	}
	exe := s.engine.Executor()
	stream, err := exe.QueryStream(ctx, s.sql, args...)
	if err != nil {
		return nil, err
	}
		next := func() (AP.Row, error) {
		row, err := stream.Next()
		if err != nil {
			if err == executor.ErrNoRows {
				return AP.Row{}, AP.ErrNoRows
			}
			return AP.Row{}, err
		}
		// REQ000862: AP.Row.Data is now []AP.Value (same type as EX.Row.Data),
		// so no boxing conversion is needed. Direct assignment eliminates
		// the per-row []any allocation that was 53% of join memory.
		return AP.Row{Cols: row.Cols, Types: row.Types, Data: row.Data}, nil
	}
	return AP.NewRows(stream.Cols(), stream.Types(), next, func() error { return stream.Close() }), nil
}

func (s *Stmt) Exec(ctx context.Context, args ...any) (AP.Result, error) {
	if s.engine.IsClosed() {
		return AP.Result{}, AP.ErrClosed
	}
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		return AP.Result{}, AP.ErrClosed
	}
	s.mu.Unlock()
	if err := validateArgTypes(args, s.paramTypes); err != nil {
		return AP.Result{}, err
	}
	exe := s.engine.Executor()
	res, err := exe.Exec(ctx, s.sql, args...)
	if err != nil {
		return AP.Result{}, err
	}
	return AP.Result{RowsAffected: res.RowsAffected, LastInsertID: res.LastInsertID}, nil
}

func (s *Stmt) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return nil
	}
	s.closed.Store(true)
	return nil
}

var _ = executor.ErrNoRows
var errNotEnoughArgs = errors.New("st: not enough args for `?` placeholders")
