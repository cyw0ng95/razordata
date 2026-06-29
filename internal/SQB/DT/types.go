package DT

import (
	"errors"
	"fmt"

	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	"strings"
	"sync"
	"sync/atomic"
)

// Value is a tagged-union that stores SQL values inline without boxing.
type Value = pl.Value

// ValueKind is the type discriminator for Value.
type ValueKind = pl.ValueKind

// Value kind constants.
const (
	KindNull  = pl.KindNull
	KindInt   = pl.KindInt
	KindFloat = pl.KindFloat
	KindText  = pl.KindText
	KindBlob  = pl.KindBlob
	KindBool  = pl.KindBool
)

// Value constructors.
func NewIntValue(v int64) Value     { return AP.NewIntValue(v) }
func NewFloatValue(v float64) Value { return AP.NewFloatValue(v) }
func NewTextValue(v string) Value   { return AP.NewTextValue(v) }
func NewBlobValue(v []byte) Value   { return AP.NewBlobValue(v) }
func NewBoolValue(v bool) Value     { return AP.NewBoolValue(v) }
func NullValue() Value              { return AP.NullValue() }

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

// Operator is the core execution interface.
type Operator = pl.Operator

// Row is a single row of data with column metadata.
type Row = pl.Row

// ExecContext is per-execution state.
type ExecContext = pl.ExecContext

// ColInfo describes a single column in a table schema.
type ColInfo = pl.ColInfo

// TxWriter is the optional hook an Executor notifies on every key write.
type TxWriter = pl.TxWriter

// Store is the minimal storage surface the executor needs to integrate
// with the real engine. The in-memory map (tables/schemas) is the fallback
// when Store is nil; when Store is non-nil, operators read from the engine.
type Store interface {
	Insert(key, value []byte) error
	Delete(key []byte) error
	// Get returns the value for an exact key match, or (nil, false, nil)
	// if the key is not present. Added in iter-22 to support secondary
	// index seeks (the index yields a primary key, then the executor
	// fetches the row via Get).
	Get(key []byte) ([]byte, bool, error)
	NewIterator(prefix []byte) ls.RangeIter
	// ManualCompact triggers a full LSM compaction cycle. REQ000257.
	// Returns ErrCompactionInProgress if already compacting.
	ManualCompact() error
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
)

// SessionCounterAccessor is an optional callback set by SYS/SE to
// provide per-session counter accessors for changes(), last_insert_rowid(),
// and total_changes() eval functions.
type SessionCounterAccessor interface {
	ChangesCount(sessionID uint64) int64
	LastInsertRowID(sessionID uint64) int64
	TotalChangesCount(sessionID uint64) int64
}

var (
	SessionCounterMu       sync.RWMutex
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

// Compare returns -1/0/1 comparing two any values (or Values). NULLs
// sort less than non-NULLs; two NULLs compare equal.
func Compare(a, b any) int {
	if av, ok := a.(Value); ok {
		a = av.ToAny()
	}
	if bv, ok := b.(Value); ok {
		b = bv.ToAny()
	}
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	switch ax := a.(type) {
	case string:
		if bs, ok := b.(string); ok {
			return strings.Compare(ax, bs)
		}
	case int64:
		switch bx := b.(type) {
		case int64:
			if ax < bx {
				return -1
			}
			if ax > bx {
				return 1
			}
			return 0
		case float64:
			if float64(ax) < bx {
				return -1
			}
			if float64(ax) > bx {
				return 1
			}
			return 0
		}
	case float64:
		if bf, ok := b.(float64); ok {
			if ax < bf {
				return -1
			}
			if ax > bf {
				return 1
			}
			return 0
		}
		if bi, ok := b.(int64); ok {
			if ax < float64(bi) {
				return -1
			}
			if ax > float64(bi) {
				return 1
			}
			return 0
		}
	}
	return 0
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

// EqualValueAny compares two any values for equality after unwrapping
// any Value to its underlying Go type. If either side is nil, returns false.
func EqualValueAny(a, b any) bool {
	if av, ok := a.(Value); ok {
		a = av.ToAny()
	}
	if bv, ok := b.(Value); ok {
		b = bv.ToAny()
	}
	if a == nil || b == nil {
		return false
	}
	if ai, aok := a.(int64); aok {
		if bi, bok := b.(int64); bok {
			return ai == bi
		}
		if bf, bok := b.(float64); bok {
			return float64(ai) == bf
		}
		if bi, bok := b.(int); bok {
			return ai == int64(bi)
		}
		return false
	}
	if af, aok := a.(float64); aok {
		if bf, bok := b.(float64); bok {
			return af == bf
		}
		return false
	}
	if as, aok := a.(string); aok {
		if bs, bok := b.(string); bok {
			return as == bs
		}
		return false
	}
	if ab, aok := a.(bool); aok {
		if bb, bok := b.(bool); bok {
			return ab == bb
		}
		return false
	}
	return a == b
}

// Rows describes the columns of a query result.
type Rows struct {
	Cols  []string
	Types []int // LX.TokenType
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