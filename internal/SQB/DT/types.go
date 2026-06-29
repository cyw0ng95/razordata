package DT

import (
	"errors"
	"fmt"

	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
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

// InMemoryTxWriter is the optional hook for in-memory table rollback support.
type InMemoryTxWriter = pl.InMemoryTxWriter

// Error sentinels.
var (
	ErrNotImplemented = errors.New("ex: not implemented")
	ErrNoRows         = pl.ErrNoRows
	ErrClosed         = errors.New("ex: operator closed")
)

// Result holds the outcome of an Exec call.
type Result struct {
	RowsAffected int64
	LastInsertID uint64
}

// Rows describes the columns of a query result.
type Rows struct {
	Cols  []string
	Types []int // LX.TokenType
}