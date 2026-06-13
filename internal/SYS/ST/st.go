// Package ST implements the Stmt cluster: prepared statements with a
// parse-once / execute-many model backed by the engine's executor.
package ST

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
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
	// paramTypes caches the SQL column types of each `?`
	// placeholder, extracted once at Prepare time from the parsed
	// AST. The slice is parallel to the placeholders in
	// left-to-right order; a -1 entry means "type unknown" (the
	// planner could not resolve a column type for that slot,
	// e.g. a `?` on the right side of a comparison whose left
	// side is an expression we cannot resolve). The ST layer
	// skips validation for -1 entries.
	paramTypes []int
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
	s := &Stmt{engine: engine, sql: sql}
	// Best-effort: extract the column types of each `?` placeholder
	// from the planner's resolved AST. If the planner cannot
	// resolve a type for a given slot (e.g. ad-hoc expressions),
	// the slot stays -1 and validateArgTypes skips it.
	s.paramTypes = extractParamTypes(engine, sql)
	return s, nil
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

// ParamTypes returns the cached SQL column type of each `?`
// placeholder (parallel slice; -1 means unknown / unvalidated).
// Exposed for callers that want to introspect or skip validation.
func (s *Stmt) ParamTypes() []int {
	out := make([]int, len(s.paramTypes))
	copy(out, s.paramTypes)
	return out
}

// argTypeError is returned by validateArgTypes when the Go type
// of a bound argument cannot be coerced to the SQL column type
// of the corresponding `?` placeholder (R16-4).
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
	return fmt.Sprintf("st: arg %d (Go %s) cannot bind to column type %d",
		e.ArgIdx, goType, int(e.SQLType))
}

// validateArgTypes walks the args slice and confirms each Go
// value can be coerced to the SQL column type recorded for that
// placeholder (R16-3, R16-4). Returns *argTypeError on
// mismatch; nil on success.
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
			// Slot was unresolvable at Prepare time. Skip.
			continue
		}
		if !coercible(reflect.TypeOf(a), ls.ColumnType(ct)) {
			return &argTypeError{
				ArgIdx:  i,
				GoType:  reflect.TypeOf(a),
				SQLType: ls.ColumnType(ct),
			}
		}
	}
	return nil
}

// coercible reports whether a Go value of goType can be coerced
// to a column of SQL type sqlType (R16-4). The mapping mirrors
// the engine's per-type encoding in ENG/LS/deparser.go:
// - INTEGER/BIGINT/TIMESTAMP: any int/uint family
// - FLOAT: any float family, plus the int family (lossy)
// - BOOLEAN: bool only
// - VARCHAR/TEXT/BLOB: string and []byte
// Anything else returns false.
func coercible(goType reflect.Type, sqlType ls.ColumnType) bool {
	if goType == nil {
		// nil can bind to a nullable column. The actual
		// nullability check is enforced by NOT NULL/DEFAULT
		// elsewhere; here we accept nil for any type.
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
			// []byte maps to BLOB/TEXT.
			return goType.Elem().Kind() == reflect.Uint8
		}
	}
	return false
}

// extractParamTypes parses the SQL and inspects each `?`
// placeholder, returning a parallel []int of SQL column types
// (ls.CTInt/ls.CTVarchar/...). Entries are -1 when the planner
// could not resolve a column type for the placeholder (e.g.
// ambiguous expressions on either side of a comparison).
func extractParamTypes(engine *SY.Engine, sql string) []int {
	// We delegate to the executor: re-parse the SQL, walk the
	// resolved AST's placeholders, and look up each column's
	// SQL type via the planner. The executor keeps a private
	// helper for this; we expose it via a thin shim.
	return engine.ExtractParamTypes(sql)
}

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

	// R16-3: type-validate before forwarding to the executor.
	// The type coercion check is permissive for nil-bindable
	// types and strict for everything else.
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
		return AP.Row{Cols: row.Cols, Types: row.Types, Data: row.Data}, nil
	}
	closer := func() error { return stream.Close() }
	return AP.NewRows(stream.Cols(), stream.Types(), next, closer), nil
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

	// R16-3: same type-validate as Query.
	if err := validateArgTypes(args, s.paramTypes); err != nil {
		return AP.Result{}, err
	}

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

// errNotEnoughArgs is returned when the caller passes fewer args
// than there are `?` placeholders. Kept private; callers should
// inspect the wrapped error type if they care.
var errNotEnoughArgs = errors.New("st: not enough args for `?` placeholders")
