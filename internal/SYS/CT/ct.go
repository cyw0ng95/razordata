// Package CT (Contract Types) defines the shared type system for the
// entire database engine — value representation, row structure, operator
// interface, execution context, and storage contracts. REQ002085.
//
// This package imports only LOG/EC (for error types used in interface
// signatures). All other engine packages import CT to obtain shared
// type definitions without creating upward dependency edges.
package CT

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// TokenType identifies the SQL column type. Defined here so Row and ColInfo
// can reference it without importing SQF/LX. SQF/LX aliases TokenType to
// its own LX.TokenType.
type TokenType int

// ValueKind identifies the type of a Value.
type ValueKind uint8

const (
	KindNull  ValueKind = 0
	KindInt   ValueKind = 1
	KindFloat ValueKind = 2
	KindText  ValueKind = 3
	KindBlob  ValueKind = 4
	KindBool  ValueKind = 5
)

// Value is a type-erased container for SQL values.
type Value struct {
	Kind ValueKind
	I64  int64
	F64  float64
	S    string
	B    []byte
	Bo   bool
}

// Value constructors
func NewIntValue(v int64) Value   { return Value{Kind: KindInt, I64: v} }
func NewFloatValue(v float64) Value { return Value{Kind: KindFloat, F64: v} }
func NewTextValue(v string) Value  { return Value{Kind: KindText, S: v} }
func NewBlobValue(v []byte) Value  { return Value{Kind: KindBlob, B: v} }
func NewBoolValue(v bool) Value    { return Value{Kind: KindBool, Bo: v} }
func NullValue() Value             { return Value{Kind: KindNull} }

// Value methods
func (v Value) IsNull() bool    { return v.Kind == KindNull }
func (v Value) AsInt() int64    { return v.I64 }
func (v Value) AsFloat() float64 { return v.F64 }
func (v Value) AsString() string { return v.S }
func (v Value) AsBlob() []byte  { return v.B }
func (v Value) AsBool() bool    { return v.Bo }

func (v Value) ToAny() any {
	switch v.Kind {
	case KindNull:
		return nil
	case KindInt:
		return v.I64
	case KindFloat:
		return v.F64
	case KindText:
		return v.S
	case KindBlob:
		return v.B
	case KindBool:
		return v.Bo
	default:
		return nil
	}
}

func (v Value) String() string {
	switch v.Kind {
	case KindNull:
		return "NULL"
	case KindInt:
		return strconv.FormatInt(v.I64, 10)
	case KindFloat:
		return strconv.FormatFloat(v.F64, 'g', -1, 64)
	case KindText:
		return v.S
	case KindBlob:
		return fmt.Sprintf("<blob:%d>", len(v.B))
	case KindBool:
		return strconv.FormatBool(v.Bo)
	default:
		return "?"
	}
}

func (v Value) Equal(other Value) bool {
	if v.Kind != other.Kind {
		return false
	}
	switch v.Kind {
	case KindNull:
		return true
	case KindInt:
		return v.I64 == other.I64
	case KindFloat:
		return v.F64 == other.F64
	case KindText:
		return v.S == other.S
	case KindBlob:
		return len(v.B) == len(other.B) && (len(v.B) == 0 || &v.B[0] == &other.B[0] || string(v.B) == string(other.B))
	case KindBool:
		return v.Bo == other.Bo
	default:
		return false
	}
}

func (k ValueKind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindText:
		return "text"
	case KindBlob:
		return "blob"
	case KindBool:
		return "bool"
	default:
		return "?"
	}
}

// CollateFunc is a user-registered collation function for ORDER BY / COMPARE.
type CollateFunc func(a, b []byte) int

// ErrNoRows is returned by Operator.Next() when no more rows exist.
var ErrNoRows = errors.New("pl: no rows")

// ExecContext carries per-execution state through the operator tree.
type ExecContext struct {
	Planner       interface{}
	SessionID     uint64
	TxWriter      interface{}
	RowArena      interface{}
	LastChanges   int64
	TotalChanges  int64
	RowCount      int64
	MaxResultRows int64
}

// ColInfo describes a single column's name and type.
type ColInfo struct {
	Name string
	Type TokenType
}

// Operator is the fundamental execution interface — a row-based iterator.
type Operator interface {
	Next(ctx context.Context) (Row, error)
	Close() error
}

// Row is a single row of data with column metadata.
type Row struct {
	Cols  []string
	Types []TokenType
	Data  []Value
	Outer *Row

	Planner interface{}
	ColIndex map[string]int
	StoreKey []byte
	ExecCtx  *ExecContext
	TableName string
	RowFromSubsetDecode bool
	RowIndex  int
	DataStable bool
}

