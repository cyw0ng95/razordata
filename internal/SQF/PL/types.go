// Package PL hosts the core executor types shared across all SQB clusters.
// Operator, Row, Value, ExecContext, Planner, and TxWriter are defined here
// so that any SQB cluster can implement Operator without importing SQB/EX,
// breaking the import cycle that would otherwise block cluster extraction.
package PL

import (
	"context"
	"errors"
	"strings"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
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