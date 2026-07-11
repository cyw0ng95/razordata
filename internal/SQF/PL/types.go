// Package PL hosts the core executor types shared across all SQB clusters.
// Operator, Row, Value, ExecContext, Planner, and TxWriter are defined here
// so that any SQB cluster can implement Operator without importing SQB/EX,
// breaking the import cycle that would otherwise block cluster extraction.
package PL

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	nm "github.com/cyw0ng95/razordata/internal/ENG/NM"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// Value is a tagged-union that stores SQL values inline without boxing.
// This is a type alias for AP.Value. Both packages share the same
// concrete type, eliminating []any boxing at package boundaries.
type Value = AP.Value

// ValueKind is the type discriminator for Value.
type ValueKind = AP.ValueKind

// Value kind constants — aliased from SYS/AP for zero-cost interop.
const (
	KindNull  = AP.KindNull
	KindInt   = AP.KindInt
	KindFloat = AP.KindFloat
	KindText  = AP.KindText
	KindBlob  = AP.KindBlob
	KindBool  = AP.KindBool
)

// ErrNoRows is returned by Operator.Next() when no more rows exist.
// Defined here as a shared sentinel so that all SQB clusters use
// the same error value.
var ErrNoRows = errors.New("pl: no rows")

// Operator is the core execution interface. Every operator implements
// Next() to produce the next row and Close() to release resources.
type Operator interface {
	Next(ctx context.Context) (Row, error)
	Close() error
}

// Row is a single row of data with column metadata and outer-row
// linkage for correlated subqueries.
type Row struct {
	Cols  []string
	Types []LX.TokenType
	Data  []Value
	Outer *Row

	// Planner is the query planner associated with this row (or any
	// of its outer parents). Set by the executor when materializing
	// a row from the main plan. Subquery eval functions read it to
	// plan their nested queries with the same store, catalog, and
	// stats catalog.
	Planner QueryPlanner
	// ColIndex is a pre-built O(1) lookup from column name to
	// column index, built lazily on first Lookup call.
	ColIndex map[string]int
	// StoreKey holds the raw key from the LSM iterator when this
	// row was read from the engine store. Populated by SeqScan
	// and used by Update/Delete to preserve the original key.
	StoreKey []byte
	// ExecCtx carries per-execution state (planner, session ID,
	// tx writer) through the operator tree.
	ExecCtx *ExecContext
	// TableName identifies which table this row was read from.
	// Set by SeqScan/IndexScan when producing rows so correlated
	// subquery eval can resolve QualifiedName references against
	// the correct table.
	TableName string
}

// Lookup returns the value at the given column name.
func (r *Row) Lookup(name string) (any, bool) {
	lname := name
	hasUpper := false
	for _, c := range name {
		if c >= 'A' && c <= 'Z' {
			lname = strings.ToLower(name)
			hasUpper = true
			_ = hasUpper
			break
		}
	}
	for cur := r; cur != nil; cur = cur.Outer {
		if cur.ColIndex == nil {
			cur.buildColIndex()
		}
		if idx, ok := cur.ColIndex[lname]; ok {
			if idx < len(cur.Data) {
				return cur.Data[idx].ToAny(), true
			}
			return nil, false
		}
		for j, c := range cur.Cols {
			if i := strings.LastIndexByte(c, '.'); i >= 0 && i < len(c)-1 {
				if !hasUpper {
					if c[i+1:] == name && j < len(cur.Data) {
						return cur.Data[j].ToAny(), true
					}
				} else if strings.EqualFold(c[i+1:], name) && j < len(cur.Data) {
					return cur.Data[j].ToAny(), true
				}
			}
		}
	}
	return nil, false
}

// LookupValue returns the Value at the given column name without boxing.
func (r *Row) LookupValue(name string) (Value, bool) {
	lname := name
	hasUpper := false
	for _, c := range name {
		if c >= 'A' && c <= 'Z' {
			lname = strings.ToLower(name)
			hasUpper = true
			_ = hasUpper
			break
		}
	}
	for cur := r; cur != nil; cur = cur.Outer {
		if cur.ColIndex == nil {
			cur.buildColIndex()
		}
		if idx, ok := cur.ColIndex[lname]; ok {
			if idx < len(cur.Data) {
				return cur.Data[idx], true
			}
			return Value{}, false
		}
		for j, c := range cur.Cols {
			if i := strings.LastIndexByte(c, '.'); i >= 0 && i < len(c)-1 {
				if !hasUpper {
					if c[i+1:] == name && j < len(cur.Data) {
						return cur.Data[j], true
					}
				} else if strings.EqualFold(c[i+1:], name) && j < len(cur.Data) {
					return cur.Data[j], true
				}
			}
		}
	}
	return Value{}, false
}

// buildColIndex builds the O(1) column name → index map.
func (r *Row) buildColIndex() {
	r.ColIndex = make(map[string]int, len(r.Cols))
	allLower := true
	for _, c := range r.Cols {
		if c != "" && (c[0] < 'a' || c[0] > 'z') && c[0] != '_' && c[0] != '.' {
			for j := 0; j < len(c); j++ {
				if c[j] >= 'A' && c[j] <= 'Z' {
					allLower = false
					break
				}
			}
			if !allLower {
				break
			}
		}
	}
	for i, c := range r.Cols {
		if allLower {
			r.ColIndex[c] = i
		} else {
			r.ColIndex[strings.ToLower(c)] = i
		}
	}
}

// GetPlanner walks the Row outer chain and returns the first
// planner found, or nil.
func (r *Row) GetPlanner() QueryPlanner {
	for cur := r; cur != nil; cur = cur.Outer {
		if cur.Planner != nil {
			return cur.Planner
		}
	}
	return nil
}

// ExecContext carries per-execution state through the operator tree,
// replacing package-level globals that made concurrent Executor
// instances unsafe.
type ExecContext struct {
	Planner       QueryPlanner
	SessionID     uint64
	TxWriter      TxWriter
	LastChanges   int64
	TotalChanges  int64
	SubqueryCache map[string]any
	// REQ001233: rowArena is a bump-pointer allocator shared across
	// all operators in a query. Created once per query execution in
	// propagateExecContext. Stored as any to avoid DT→PL import cycle
	// (DT imports PL, so PL cannot import DT). Type-assert to
	// *DT.RowArena at usage sites.
	RowArena any // *DT.RowArena
}

// PlanResult is the planner-side container for a memoized plan.
type PlanResult struct {
	Root    Operator
	Cost    float64
	MemoKey string
}

// QueryPlanner is the query planner interface. The concrete implementation
// lives in SQB/EX (the planner.go file).
type QueryPlanner interface {
	Plan(stmt PS.Stmt) (*PlanResult, error)
	// ExecuteSubquery plans and executes a subquery statement in one call.
	// outer carries correlated column references; params are bound arguments.
	ExecuteSubquery(ctx context.Context, stmt PS.Stmt, outer *Row, params []any) ([]Row, error)
	RegisterTable(name string, cols []ColInfo, pk string)
	RegisterIndex(table, index string, cols []string)
	SetPool(pool WorkerPool)
	Pool() WorkerPool
	SetJoinBufferSize(v int64)
	SetMaxMemoryPerQuery(v int64)
	InvalidateCache()
	SetStatsCatalog(statsCatalog StatsCatalog)
}

// ColInfo describes a single column in a table schema.
type ColInfo struct {
	Name     string
	Typ      LX.TokenType
	Nullable bool    // default true; false means NOT NULL
	Default  PS.Expr // nil means no DEFAULT clause
	PK       bool    // true means primary key (implies NOT NULL)
}

// StatsCatalog provides access to column statistics for
// histogram-based selectivity estimation.
type StatsCatalog interface {
	ColumnStatsByName(tableName, colName string) *ls.ColumnStats
}

// TxWriter is the optional hook an Executor notifies on every key
// write. Implementations record the pre-write value so ROLLBACK can
// restore. The SYS layer wires this for transactional sessions.
type TxWriter interface {
	RecordWrite(key []byte, newValue []byte)
	InMemoryTxWriter
}

// InMemoryTxWriter is the optional hook for in-memory table
// rollback support.
type InMemoryTxWriter interface {
	RecordInMemoryTable(table string, snapshot []Row)
}

// WorkerPool coordinates parallel task execution across N workers.
type WorkerPool interface {
	Submit(ctx context.Context, task func() error) error
	Close()
	Workers() int
}

// NUMAWorkerPool extends WorkerPool with NUMA-awareness.
// Workers are assigned to NUMA nodes and pinned to local CPUs
// when a NUMATopology is provided (REQ001055).
type NUMAWorkerPool interface {
	WorkerPool
	// SetNUMATopology assigns workers to NUMA nodes and enables
	// thread pinning. Must be called before any Submit.
	SetNUMATopology(topo *nm.Topology)
	// WorkerNode returns the NUMA node assigned to worker i.
	// Returns 0 if no topology is set.
	WorkerNode(i int) int
	// NodeWorkers returns the number of workers assigned to a node.
	NodeWorkers(node int) int
}
// cmpFloat compares two float64 values. Returns -1, 0, or 1.
func cmpFloat(a, b float64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// CompareValue compares two Values. Returns -1, 0, or 1.
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
	if a.Kind == KindInt && b.Kind == KindInt {
		if a.I64 < b.I64 {
			return -1
		}
		if a.I64 > b.I64 {
			return 1
		}
		return 0
	}
	if a.Kind == KindFloat && b.Kind == KindFloat {
		return cmpFloat(a.F64, b.F64)
	}
	if (a.Kind == KindInt || a.Kind == KindFloat) &&
		(b.Kind == KindInt || b.Kind == KindFloat) {
		var af, bf float64
		if a.Kind == KindInt {
			af = float64(a.I64)
		} else {
			af = a.F64
		}
		if b.Kind == KindInt {
			bf = float64(b.I64)
		} else {
			bf = b.F64
		}
		return cmpFloat(af, bf)
	}
	if a.Kind == KindText && b.Kind == KindText {
		return cmpString(a.S, b.S)
	}
	if a.Kind == KindBool && b.Kind == KindBool {
		return cmpBool(a.Bo, b.Bo)
	}
	if a.Kind == KindBlob && b.Kind == KindBlob {
		minLen := len(a.B)
		if len(b.B) < minLen {
			minLen = len(b.B)
		}
		for i := 0; i < minLen; i++ {
			if a.B[i] < b.B[i] {
				return -1
			}
			if a.B[i] > b.B[i] {
				return 1
			}
		}
		if len(a.B) < len(b.B) {
			return -1
		}
		if len(a.B) > len(b.B) {
			return 1
		}
		return 0
	}
	return 0
}

// EqualValueValue compares two Values for equality.
func EqualValueValue(a, b Value) bool {
	if a.Kind == KindNull || b.Kind == KindNull {
		return a.Kind == KindNull && b.Kind == KindNull
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindInt:
		return a.I64 == b.I64
	case KindFloat:
		return a.F64 == b.F64
	case KindText:
		return a.S == b.S
	case KindBool:
		return a.Bo == b.Bo
	case KindBlob:
		if len(a.B) != len(b.B) {
			return false
		}
		for i := range a.B {
			if a.B[i] != b.B[i] {
				return false
			}
		}
		return true
	}
	return false
}

func cmpString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpBool(a, b bool) int {
	if a == b {
		return 0
	}
	if a {
		return 1
	}
	return -1
}

// ValueFromAny converts a Go any to a Value.
// REQ000776: bridges Go primitive types into the SQL Value type.
func ValueFromAny(a any) Value {
	if a == nil {
		return AP.NullValue()
	}
	if v, ok := a.(Value); ok {
		return v
	}
	switch x := a.(type) {
	case int64:
		return AP.NewIntValue(x)
	case float64:
		return AP.NewFloatValue(x)
	case string:
		return AP.NewTextValue(x)
	case bool:
		return AP.NewBoolValue(x)
	case int:
		return AP.NewIntValue(int64(x))
	case []byte:
		return AP.NewBlobValue(x)
	default:
		return AP.NewTextValue(fmt.Sprint(x))
	}
}

// EqualValue compares two any-typed values for equality (REQ000754).
// Accepts both Value types and Go primitives, with int/float fast paths.
func EqualValue(a, b any) bool {
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
		if bi, bok := b.(int64); bok {
			return af == float64(bi)
		}
	}
	if ai, aok := a.(int); aok {
		if bi, bok := b.(int); bok {
			return ai == bi
		}
		if bi, bok := b.(int64); bok {
			return int64(ai) == bi
		}
		return false
	}
	return a == b
}