// Package PL hosts the core executor types shared across all SQB clusters.
// Operator, Row, Value, ExecContext, Planner, and TxWriter are defined here
// so that any SQB cluster can implement Operator without importing SQB/EX,
// breaking the import cycle that would otherwise block cluster extraction.
package PL

import (
	"sync"
	"sync/atomic"
)

import (
	"context"
	"errors"
	"strings"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	nm "github.com/cyw0ng95/razordata/internal/ENG/NM"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	CT "github.com/cyw0ng95/razordata/internal/SYS/CT"
)

// REQ002085: Value, ValueKind, and kind constants aliased from SYS/CT.
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

// ErrNoRows is returned by Operator.Next() when no more rows exist.
// Defined here as a shared sentinel so that all SQB clusters use
// the same error value.
var ErrNoRows = errors.New("pl: no rows")

// Operator is the core execution interface. Every operator implements
// Next() to produce the next row and Close() to release resources.
type Operator interface { // Deprecated: use DT.Operator. Kept for backward compat.
	Next(ctx context.Context) (Row, error)
	Close() error
}

// Resettable is an optional interface an Operator can implement to
// support cursor state reuse without operator tree deallocation.
// REQ001464.
type Resettable interface {
	Reset(ctx context.Context) error
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
// RowFromSubsetDecode is set by SeqScan when the row was
	// produced by the column-aware subset decoder (REQ001434).
	// The downstream alias pass in nextFromStore must skip
	// prefixCols for such rows.
	RowFromSubsetDecode bool
	// REQ002098: RowIndex is the position of this row in the
	// in-memory DT.Tables slice. Set by SeqScan.Next for in-memory
	// tables; used by ReplaceBySnapshot to find the row in O(1)
	// instead of scanning the entire table. 0 is a valid index
	// (the first row). For store-backed rows and rows not from a
	// SeqScan, RowIndex is 0 but the store-path fallback in
	// ReplaceBySnapshot handles those correctly via StoreKey.
	RowIndex int
	// REQ002104: DataStable is true when the row's Data slice is
	// an independent copy (not aliasing the SeqScan's internal
	// buffer or the in-memory table). Set by SeqScan when
	// needsStableData is true. Filter.refillBatch checks this flag
	// to skip the defensive deep-copy (28% of flat alloc_space on
	// BenchmarkRazordata_Update).
	DataStable bool
}

// Lookup returns the value at the given column name.
func (r *Row) Lookup(name string) (any, bool) {
	lname := name
	hasUpper := false
	for _, c := range name {
		if c >= 'A' && c <= 'Z' {
			lname = LX.NormalizeIdent(name)
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
			lname = LX.NormalizeIdent(name)
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

// colIndexPool pools map[string]int for Row.ColIndex to eliminate
// per-row allocations in groupby queries. REQ002026.
var colIndexPool = sync.Pool{
	New: func() any {
		m := make(map[string]int, 8)
		return &m
	},
}

// buildColIndex builds the O(1) column name → index map.
// REQ002026: uses pooled map to eliminate per-row allocation.
func (r *Row) buildColIndex() {
	m := colIndexPool.Get().(*map[string]int)
	clear(*m)
	r.ColIndex = *m
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
			r.ColIndex[LX.NormalizeIdent(c)] = i
		}
	}
}

// ReleaseColIndex returns the row's ColIndex map to the pool.
// Call this when the row is no longer needed to allow map reuse.
// REQ002026.
func (r *Row) ReleaseColIndex() {
	if r.ColIndex == nil {
		return
	}
	clear(r.ColIndex)
	m := r.ColIndex
	colIndexPool.Put(&m)
	r.ColIndex = nil
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
	// REQ001994: FallbackHits counts how many times vectorized
	// expression evaluation fell back to row-at-a-time (batchToRow
	// + per-row Eval). Reset to 0 by PRAGMA eval_fallback_stats.
	FallbackHits atomic.Int64
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
	// REQ001420: row count metadata for COUNT(*) optimization.
	UpdateTableRowCount(table string, delta int64)
	GetTableRowCount(table string) int64
	RegisterCollation(name string, fn CollateFunc) error // REQ001332
	LookupCollation(name string) CollateFunc              // REQ001332
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
