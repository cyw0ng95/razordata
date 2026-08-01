package DT

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
	CT "github.com/cyw0ng95/razordata/internal/SYS/CT"
)

// REQ002085: Value, ValueKind, kind constants, and constructors aliased from SYS/CT.
type Value = CT.Value
type ValueKind = CT.ValueKind

const (
	KindNull  = CT.KindNull
	KindInt   = CT.KindInt
	KindFloat = CT.KindFloat
	KindText  = CT.KindText
	KindBlob  = CT.KindBlob
	KindBool  = CT.KindBool
)

func NewIntValue(v int64) Value     { return CT.NewIntValue(v) }
func NewFloatValue(v float64) Value { return CT.NewFloatValue(v) }
func NewTextValue(v string) Value   { return CT.NewTextValue(v) }
func NewBlobValue(v []byte) Value   { return CT.NewBlobValue(v) }
func NewBoolValue(v bool) Value     { return CT.NewBoolValue(v) }
func NullValue() Value              { return CT.NullValue() }

// ValueToString converts a Value to its string representation.
func ValueToString(v Value) string { return v.String() }

// ValueFromAny creates a Value from a boxed any.
func ValueFromAny(a any) Value {
	if a == nil {
		return NullValue()
	}
	if v, ok := a.(Value); ok {
		return v
	}
	switch x := a.(type) {
	case int64:
		return NewIntValue(x)
	case float64:
		return NewFloatValue(x)
	case string:
		return NewTextValue(x)
	case bool:
		return NewBoolValue(x)
	case int:
		return NewIntValue(int64(x))
	case []byte:
		return NewBlobValue(x)
	}
	return NewTextValue(fmt.Sprint(a))
}

// ValueFromAnySlice converts a []any to []Value.
func ValueFromAnySlice(a []any) []Value {
	if a == nil {
		return nil
	}
	out := make([]Value, len(a))
	for i, v := range a {
		out[i] = ValueFromAny(v)
	}
	return out
}

// ValueSliceToAny converts a []Value to []any.
func ValueSliceToAny(v []Value) []any {
	if v == nil {
		return nil
	}
	out := make([]any, len(v))
	for i, val := range v {
		out[i] = val.ToAny()
	}
	return out
}

// Operator is the core execution interface. Every operator implements
// Next() to produce the next row and Close() to release resources.
type Operator interface {
	Next(ctx context.Context) (Row, error)
	Close() error
}

// Resettable is an optional interface an Operator can implement to
// support cursor state reuse without operator tree deallocation.
type Resettable interface {
	Reset(ctx context.Context) error
}

// Row is a single row of data with column metadata.
type Row = pl.Row

// ExecContext is per-execution state.
type ExecContext = pl.ExecContext

// ColInfo describes a single column in a table schema.
type ColInfo = pl.ColInfo

// REQ002085: TxWriter aliased from SQF/PL.
type TxWriter = pl.TxWriter

// Store is the minimal storage surface the executor needs to integrate
// with the real engine. The in-memory map (tables/schemas) is the fallback
// when Store is nil; when Store is non-nil, operators read from the engine.
type Store interface {
	Insert(key, value []byte) error
	Delete(key []byte) error
	Get(key []byte) ([]byte, bool, error)
	NewIterator(prefix []byte) ls.RangeIter
	ManualCompact() error
}

// BatchStore is an optional extension of Store supporting bulk
// writes. Store implementations that can amortise per-row overhead
// (LS engine) implement this interface; executor code type-asserts
// and uses WriteBatch when available, falling back to per-row
// Insert otherwise. REQ001421.
type BatchStore interface {
	Store
	WriteBatch(keys, values [][]byte) error
}

// BatchDeleteStore is an optional extension of Store supporting
// bulk deletes. Symmetrical with BatchStore — implementations
// (LS engine) implement DeleteBatch to amortise per-call overhead
// across many keys in a single tombstone write batch.
//
// REQ001556: executor code type-asserts and uses DeleteBatch when
// available, falling back to per-row Delete otherwise.
type BatchDeleteStore interface {
	Store
	DeleteBatch(keys [][]byte) error
}

// StatsCatalog provides access to column statistics for
// histogram-based selectivity estimation.
type StatsCatalog interface {
	ColumnStatsByName(tableName, colName string) *ls.ColumnStats
}

// InMemoryTxWriter is the optional hook for in-memory table rollback support.
type InMemoryTxWriter = pl.InMemoryTxWriter

// Error sentinels.
var (
	ErrNotImplemented = errors.New("ex: not implemented")
	ErrNoRows         = pl.ErrNoRows
	ErrClosed         = errors.New("ex: operator closed")
	ErrRequiresDebugBuild = errors.New("ex: requires debug build (-tags debug)")
)

// SessionCounterAccessor is an optional callback set by SYS/SE to
// provide per-session counter accessors for changes(), last_insert_rowid(),
// and total_changes() eval functions.
type SessionCounterAccessor interface {
	ChangesCount(sessionID uint64) int64
	LastInsertRowID(sessionID uint64) int64
	TotalChangesCount(sessionID uint64) int64
}

// DBAttachManager provides cross-database attachment management.
// Used by WT.AttachOp/DetachOp without requiring *EX.Executor.
type DBAttachManager interface {
	AttachDB(name, path string)
	DetachDB(name string)
	GetAttachedDBs() map[string]string
}

var (
	SessionCounterMu        sync.RWMutex
	SessionCounterAccessor_ SessionCounterAccessor
)

// CurrentSessionID is the package-level current session ID for eval functions.
var CurrentSessionID atomic.Uint64

// SetSessionCounterAccessor sets the callback for reading per-session
// counters. Called once during SYS initialization.
func SetSessionCounterAccessor(acc SessionCounterAccessor) {
	SessionCounterMu.Lock()
	defer SessionCounterMu.Unlock()
	SessionCounterAccessor_ = acc
}

// GetSessionCounterAccessor returns the current accessor (may be nil).
func GetSessionCounterAccessor() SessionCounterAccessor {
	SessionCounterMu.RLock()
	defer SessionCounterMu.RUnlock()
	return SessionCounterAccessor_
}

// GetCurrentSessionID returns the package-level session ID for eval.
func GetCurrentSessionID() uint64 {
	return CurrentSessionID.Load()
}

// ToInt64 converts an any (or Value) to int64. Returns ok=false
// if the value cannot be converted.
func ToInt64(v any) (int64, bool) {
	if val, ok := v.(Value); ok {
		v = val.ToAny()
	}
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), true
	case int:
		return int64(x), true
	}
	return 0, false
}

// CompareValue compares two Value values directly without boxing into any.
// Returns -1 if a < b, 0 if a == b, 1 if a > b. NULLs sort less than
// non-NULLs; two NULLs compare equal.
// REQ001332: CollateFunc is a user-registered collation function.
type CollateFunc = AP.CollateFunc

func CompareValue(a, b Value) int {
	if a.Kind == KindNull && b.Kind == KindNull {
		return 0
	}
	if a.Kind == KindNull {
		return -1
	}
	if b.Kind == KindNull {
		return 1
	}
	switch a.Kind {
	case KindInt:
		switch b.Kind {
		case KindInt:
			if a.I64 < b.I64 {
				return -1
			}
			if a.I64 > b.I64 {
				return 1
			}
			return 0
		case KindFloat:
			if float64(a.I64) < b.F64 {
				return -1
			}
			if float64(a.I64) > b.F64 {
				return 1
			}
			return 0
		}
	case KindFloat:
		switch b.Kind {
		case KindFloat:
			if a.F64 < b.F64 {
				return -1
			}
			if a.F64 > b.F64 {
				return 1
			}
			return 0
		case KindInt:
			if a.F64 < float64(b.I64) {
				return -1
			}
			if a.F64 > float64(b.I64) {
				return 1
			}
			return 0
		}
	case KindText:
		if b.Kind == KindText {
			return strings.Compare(a.S, b.S)
		}
	case KindBlob:
		if b.Kind == KindBlob {
			return bytes.Compare(a.B, b.B)
		}
	case KindBool:
		if b.Kind == KindBool {
			if !a.Bo && b.Bo {
				return -1
			}
			if a.Bo && !b.Bo {
				return 1
			}
			return 0
		}
	}
	return 0
}

// CompareValueWithCollation compares two Values using an optional collation.
// REQ001332: when coll is non-nil and both values are text, uses the
// registered collation function instead of bytes.Compare.
func CompareValueWithCollation(a, b Value, coll CollateFunc) int {
	if a.Kind == KindNull && b.Kind == KindNull {
		return 0
	}
	if a.Kind == KindNull {
		return -1
	}
	if b.Kind == KindNull {
		return 1
	}
	switch a.Kind {
	case KindInt:
		switch b.Kind {
		case KindInt:
			if a.I64 < b.I64 {
				return -1
			}
			if a.I64 > b.I64 {
				return 1
			}
			return 0
		case KindFloat:
			if float64(a.I64) < b.F64 {
				return -1
			}
			if float64(a.I64) > b.F64 {
				return 1
			}
			return 0
		}
	case KindFloat:
		switch b.Kind {
		case KindFloat:
			if a.F64 < b.F64 {
				return -1
			}
			if a.F64 > b.F64 {
				return 1
			}
			return 0
		case KindInt:
			if a.F64 < float64(b.I64) {
				return -1
			}
			if a.F64 > float64(b.I64) {
				return 1
			}
			return 0
		}
	case KindText:
		if b.Kind == KindText {
			if coll != nil {
				return coll([]byte(a.S), []byte(b.S))
			}
			return strings.Compare(a.S, b.S)
		}
	case KindBlob:
		if b.Kind == KindBlob {
			return bytes.Compare(a.B, b.B)
		}
	case KindBool:
		if b.Kind == KindBool {
			if !a.Bo && b.Bo {
				return -1
			}
			if a.Bo && !b.Bo {
				return 1
			}
			return 0
		}
	}
	return 0
}

// EqualValue compares two Value values for equality directly without boxing.
// Returns (false, nil) if either value is NULL (SQL three-valued logic).
func EqualValue(a, b Value) (bool, error) {
	if a.Kind == KindNull || b.Kind == KindNull {
		return false, nil
	}
	if a.Kind == KindInt && b.Kind == KindFloat {
		return float64(a.I64) == b.F64, nil
	}
	if a.Kind == KindFloat && b.Kind == KindInt {
		return a.F64 == float64(b.I64), nil
	}
	if a.Kind != b.Kind {
		return false, nil
	}
	switch a.Kind {
	case KindInt:
		return a.I64 == b.I64, nil
	case KindFloat:
		return a.F64 == b.F64, nil
	case KindText:
		return a.S == b.S, nil
	case KindBlob:
		return bytes.Equal(a.B, b.B), nil
	case KindBool:
		return a.Bo == b.Bo, nil
	}
	return false, nil
}

// Compare is a backward-compatible wrapper that converts any arguments to
// Value and calls CompareValue.
func Compare(a, b any) int {
	return CompareValue(ValueFromAny(a), ValueFromAny(b))
}

// IsValueTruthy reports whether a Value should be treated as true in
// boolean contexts.
func IsValueTruthy(v Value) bool {
	switch v.Kind {
	case KindNull:
		return false
	case KindInt:
		return v.I64 != 0
	case KindFloat:
		return v.F64 != 0
	case KindText:
		return v.S != ""
	case KindBool:
		return v.Bo
	case KindBlob:
		return len(v.B) > 0
	}
	return false
}

// Result holds the outcome of an Exec call.
type Result struct {
	RowsAffected int64
	LastInsertID uint64
}

// EqualValueAny is a backward-compatible wrapper that converts any
// arguments to Value and calls EqualValue.
func EqualValueAny(a, b any) bool {
	eq, _ := EqualValue(ValueFromAny(a), ValueFromAny(b))
	return eq
}

// Rows describes the columns of a query result.
type Rows struct {
	Cols  []string
	Types []int // LX.TokenType
}

// AggregateLookupKey returns a unique key for storing/looking up an
// aggregate function's result in a virtual row. Two aggregates with
// the same name but different arguments (e.g. MIN(94) vs MIN(-93))
// must produce different keys to avoid collision. REQ001688.
//
// REQ001975: delegates to the cached AggregateFunc.LookupKey() so the
// per-call fmt.Sprintf is eliminated; the key is computed once per
// AST node via sync.Once.
func AggregateLookupKey(e *PS.AggregateFunc) string {
	return e.LookupKey()
}

// ContainsAggregate reports whether an expression tree contains any
// aggregate function call.
func ContainsAggregate(e PS.Expr) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *PS.AggregateFunc:
		return true
	case *PS.WindowFunc:
		return false
	case *PS.BinaryExpr:
		return ContainsAggregate(v.Left) || ContainsAggregate(v.Right)
	case *PS.UnaryExpr:
		return ContainsAggregate(v.Operand)
	case *PS.AliasedExpr:
		return ContainsAggregate(v.Expr)
	case *PS.CastExpr:
		return ContainsAggregate(v.Expr)
	case *PS.FunctionCall:
		for _, a := range v.Args {
			if ContainsAggregate(a) {
				return true
			}
		}
	case *PS.CaseExpr:
		if ContainsAggregate(v.Expr) {
			return true
		}
		for _, w := range v.WhenList {
			if ContainsAggregate(w.Cond) || ContainsAggregate(w.Then) {
				return true
			}
		}
		return ContainsAggregate(v.Else)
	case *PS.BetweenExpr:
		return ContainsAggregate(v.Expr) || ContainsAggregate(v.Low) || ContainsAggregate(v.High)
	case *PS.InExpr:
		if ContainsAggregate(v.Expr) {
			return true
		}
		for _, a := range v.List {
			if ContainsAggregate(a) {
				return true
			}
		}
	}
	return false
}

// ContainsWindowFunc reports whether an expression tree contains any
// window function call.
func ContainsWindowFunc(e PS.Expr) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *PS.WindowFunc:
		return true
	case *PS.BinaryExpr:
		return ContainsWindowFunc(v.Left) || ContainsWindowFunc(v.Right)
	case *PS.UnaryExpr:
		return ContainsWindowFunc(v.Operand)
	case *PS.AliasedExpr:
		return ContainsWindowFunc(v.Expr)
	case *PS.CastExpr:
		return ContainsWindowFunc(v.Expr)
	}
	return false
}

// WithExecContext attaches an ExecContext to a Row's Outer chain.
func WithExecContext(row *pl.Row, ctx *pl.ExecContext) *pl.Row {
	if row == nil {
		return nil
	}
	row.ExecCtx = ctx
	return row
}

// ExecContextFromRow walks the Row outer chain and returns the
// first ExecContext found, or nil.
func ExecContextFromRow(row *pl.Row) *pl.ExecContext {
	for cur := row; cur != nil; cur = cur.Outer {
		if cur.ExecCtx != nil {
			return cur.ExecCtx
		}
	}
	return nil
}
