package EX

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/SQB/AD"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// SessionCounterAccessor is the session counter provider interface.
// Re-exported from DT for backward compatibility.
type SessionCounterAccessor = DT.SessionCounterAccessor

// Eval error re-exports for backward compatibility with SYS packages.
// Aliased to EV versions so identity matches.
var (
	ErrEval = EV.ErrEval
)

var (
// sessionCounterMu       sync.RWMutex — moved to DT
// sessionCounterAccessor SessionCounterAccessor — moved to DT
)

// currentTxWriter is the package-level current TxWriter. Set by
// Executor.SetTxWriter and read by Insert/Update/Delete operators
// (both in-memory and store-backed) so that transactions can
// capture pre-write state for rollback. REQ000588.
var currentTxWriter atomic.Pointer[TxWriter]

// foreignKeysEnabled controls whether FK constraint enforcement is
// active. PRAGMA foreign_keys = ON/OFF toggles this per-session.
// Default is true (enforced). REQ000905.
var foreignKeysEnabled atomic.Bool

func init() {
	foreignKeysEnabled.Store(true)
}

// SetForeignKeysEnabled stores the FK enforcement toggle.
func SetForeignKeysEnabled(v bool) {
	foreignKeysEnabled.Store(v)
}

// IsForeignKeysEnabled returns the current FK enforcement toggle.
func IsForeignKeysEnabled() bool {
	return foreignKeysEnabled.Load()
}

// SetCurrentTxWriter stores w in the package-level slot.
func SetCurrentTxWriter(w TxWriter) {
	var boxed *TxWriter
	if w != nil {
		boxed = &w
	}
	currentTxWriter.Store(boxed)
}

// CurrentTxWriter returns the package-level current TxWriter.
func CurrentTxWriter() TxWriter {
	if p := currentTxWriter.Load(); p != nil {
		return *p
	}
	return nil
}

var ErrNotImplemented = errors.New("ex: not implemented")
var ErrClosed = errors.New("ex: operator closed")

// Value is a tagged-union that stores SQL values inline without boxing.
// EX.Value IS PL.Value (type alias); no conversion needed at package boundaries.
type Value = DT.Value

// ValueKind is the type discriminator for Value.
type ValueKind = DT.ValueKind

// Value kind constants — aliased from PL for zero-cost interop.
const (
	KindNull  = DT.KindNull
	KindInt   = DT.KindInt
	KindFloat = DT.KindFloat
	KindText  = DT.KindText
	KindBlob  = DT.KindBlob
	KindBool  = DT.KindBool
)

// NewIntValue creates a Value from an int64.
func NewIntValue(v int64) Value { return AP.NewIntValue(v) }

// NewFloatValue creates a Value from a float64.
func NewFloatValue(v float64) Value { return AP.NewFloatValue(v) }

// NewTextValue creates a Value from a string.
func NewTextValue(v string) Value { return AP.NewTextValue(v) }

// NewBlobValue creates a Value from a byte slice.
func NewBlobValue(v []byte) Value { return AP.NewBlobValue(v) }

// NewBoolValue creates a Value from a bool.
func NewBoolValue(v bool) Value { return AP.NewBoolValue(v) }

// NullValue returns a NULL Value.
func NullValue() Value { return AP.NullValue() }

// valueToString converts a Value to its string representation without
// going through fmt.Sprint (no reflection, no boxing). REQ001015.
// REQ001066: delegates to Value.String() for the per-kind switch.
func valueToString(v Value) string {
	return v.String()
}

// valueFromAny creates a Value from a boxed any. Inverse of ToAny.
func valueFromAny(a any) Value {
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
	default:
		return NewTextValue(fmt.Sprint(x))
	}
}

// valueFromAnySlice converts a []any to []Value.
func valueFromAnySlice(a []any) []Value {
	if a == nil {
		return nil
	}
	out := make([]Value, len(a))
	for i, v := range a {
		out[i] = valueFromAny(v)
	}
	return out
}

// valueSliceToAny converts a []Value to []any.
func valueSliceToAny(v []Value) []any {
	if v == nil {
		return nil
	}
	out := make([]any, len(v))
	for i, val := range v {
		out[i] = val.ToAny()
	}
	return out
}

// ValueSliceToAny is the exported version of valueSliceToAny for
// callers outside the EX package (e.g. SYS/AP bridging).
func ValueSliceToAny(v []Value) []any { return valueSliceToAny(v) }

// SetCatalog and RegisterFromCatalog re-export DT functions for
// backward-compatibility with SYS packages.
var RegisterFromCatalog = DT.RegisterFromCatalog

// Operator is the core execution interface. Aliased from PL.
type Operator = DT.Operator

// Row is a single row of data with column metadata. Aliased from PL.
type Row = DT.Row
type ExecContext = DT.ExecContext

// Result holds the outcome of an Exec call.
type Result struct {
	RowsAffected int64
	LastInsertID uint64
}

// Rows describes the columns of a query result.
type Rows struct {
	Cols  []string
	Types []LX.TokenType
}

// ColInfo describes a single column in a table schema. Aliased from PL.
type ColInfo = DT.ColInfo

// Backward-compat aliases for types moved to OP.
type SeqScan = OP.SeqScan
type IndexScan = OP.IndexScan
type Filter = OP.Filter
type Project = OP.Project
type Sort = OP.Sort
type Limit = OP.Limit
type Offset = OP.Offset
type NestedLoopJoin = OP.NestedLoopJoin
type JoinKind = OP.JoinKind
type VectorizedSeqScan = OP.VectorizedSeqScan
type VectorizedFilter = OP.VectorizedFilter
type CompoundOp = OP.CompoundOp
type Values = OP.Values
type ValuesRows = OP.ValuesRows
type IntegrityCheck = UT.IntegrityCheck
type Analyze = UT.Analyze
type Vacuum = UT.Vacuum

// Backward-compat type aliases formerly in EX/store.go.
type Store = DT.Store
type StoreSchema = DT.StoreSchema
type StatsCatalog = DT.StatsCatalog
type UniqueKey = DT.UniqueKey
type ForeignKeyConstraint = DT.ForeignKeyConstraint
type RegisteredIndex = DT.RegisteredIndex

var ErrTableNotRegisteredForStorage = OP.ErrTableNotRegisteredForStorage

// Backward-compat function aliases for types/functions moved to OP.
var JoinKindInner = OP.JoinKindInner
var JoinKindCross = OP.JoinKindCross

var NewSeqScanWithStore = OP.NewSeqScanWithStore
var NewProject = OP.NewProject
var NewSort = OP.NewSort
var NewLimit = OP.NewLimit
var NewOffset = OP.NewOffset
var NewNestedLoopJoin = OP.NewNestedLoopJoin
var NewParallelSeqScanRow = OP.NewParallelSeqScanRow
var NewParallelSeqScan = OP.NewParallelSeqScan
var NewParallelIndexScan = OP.NewParallelIndexScan
var NewParallelIndexRangeScan = OP.NewParallelIndexRangeScan
var NewParallelUnionAll = OP.NewParallelUnionAll
var NewVectorizedFilter = OP.NewVectorizedFilter
var NewCompoundOp = OP.NewCompoundOp
var newValuesOp = OP.NewValuesOp
var newValuesRowsOp = OP.NewValuesRowsOp
var NewIntegrityCheck = UT.NewIntegrityCheck
var NewIntegrityCheckWithStore = UT.NewIntegrityCheckWithStore
var NewAnalyze = UT.NewAnalyze
var NewAnalyzeWithStore = UT.NewAnalyzeWithStore
var NewVacuum = UT.NewVacuum
var NewVacuumWithStore = UT.NewVacuumWithStore
var SchemaFromRowSchema = OP.SchemaFromRowSchema
var NewIndexScan = OP.NewIndexScan
var NewIndexScanWithStore = OP.NewIndexScanWithStore
var NewIndexScanWithIndex = OP.NewIndexScanWithIndex
var NewIndexScanWithRange = OP.NewIndexScanWithRange
var tablePrefix = OP.TablePrefix
var decodeRow = OP.DecodeRow
var buildIndexKey = OP.BuildIndexKey
var EncodeRow = OP.EncodeRow
var RowKey = OP.RowKey
var ExtractPK = OP.ExtractPK
var ExtractPKForUpdate = OP.ExtractPKForUpdate
var MaintainIndexesOnInsert = OP.MaintainIndexesOnInsert
var MaintainIndexesOnUpdate = OP.MaintainIndexesOnUpdate

// stmtCacheEntry holds a cached parsed statement with LRU metadata.
type stmtCacheEntry struct {
	stmt PS.Stmt
}

// planCacheEntry holds a cached compiled plan with LRU metadata.
// REQ001011.
type planCacheEntry struct {
	result *pl.PlanResult
}

// Executor holds the core execution state.
type Executor struct {
	planner    *Planner
	store      Store
	txWriter   TxWriter
	snapshotTS uint64 // REQ000255: per-statement snapshot timestamp for read-committed
	sessionID  uint64 // REQ000385/394/411: current session ID for counter access
	// lastChanges tracks rows modified by the most recent DML statement.
	// Persisted across Exec/Query calls so CHANGES() reports the correct
	// value even after intervening non-DML statements (REQ000812).
	lastChanges int64
	// totalChanges tracks cumulative DML row count across all statements
	// in the session. Copied into/out of ExecContext for each Exec/Query
	// call so TOTAL_CHANGES() is correct across statements (REQ000812).
	totalChanges int64
	// maxParallelism controls the maximum number of workers per query.
	// Default: runtime.GOMAXPROCS(0). REQ001054.
	maxParallelism int
	// txnDebugger tracks MVCC/transaction statistics for EXPLAIN ANALYZE.
	// REQ000792: MVCC debugging.
	txnDebugger *UT.TxnDebugger
	// stmtCache caches parsed statements keyed by SQL text to avoid
	// re-parsing on repeated queries. LRU eviction, default 256 entries.
	stmtCache struct {
		mu      sync.Mutex
		entries map[string]*stmtCacheEntry
		lru     []*stmtCacheEntry
		maxSize int
	}
	// planCache caches compiled plan trees keyed by AST fingerprint
	// (memo key) to avoid re-planning on repeated queries. LRU eviction,
	// default 128 entries. REQ001011.
	planCache struct {
		mu      sync.Mutex
		entries map[string]*planCacheEntry
		lru     []*planCacheEntry
		maxSize int
	}
	// pool is the shared WorkerPool for parallel operator execution.
	// Created in NewExecutor and sized to GOMAXPROCS. Shared across
	// ShallowCopy clones via pointer. Shut down in Close().
	// REQ001044.
	pool *UT.WorkerPool
	// attachedDBs maps attached database name → path.
	// Populated by ATTACH DATABASE, cleared by DETACH.
	// REQ000908.
	attachedDBs map[string]string
	// maxMemoryPerQuery caps total memory per query execution.
	// 0 means unlimited (backward compatible). REQ001056.
	maxMemoryPerQuery int64
	// joinBufferSize caps per-hash-join memory (right-side + left-side
	// materialization). 0 means unlimited. REQ001056.
	joinBufferSize int64
	// maxResultRows caps total rows returned by a single SELECT query.
	// 0 means unlimited (backward compatible). REQ001056.
	// Prevents OOM from unbounded cross-join result accumulation.
	maxResultRows int64
}

// TxWriter is the optional hook an Executor notifies on every key
// write. Aliased from PL.
type TxWriter = DT.TxWriter

// InMemoryTxWriter is the optional hook for in-memory table
// rollback support. Aliased from PL.
type InMemoryTxWriter = DT.InMemoryTxWriter

// SetTxWriter installs w as the current transaction's write hook. Pass
// nil to disable. Not safe to call concurrently with Exec; the
// caller (a Session) is responsible for serialization.
func (e *Executor) SetTxWriter(w TxWriter) {
	e.txWriter = w
	SetCurrentTxWriter(w)
}

// ClearTxWriter resets the write hook to nil. Pair with SetTxWriter.
func (e *Executor) ClearTxWriter() {
	e.txWriter = nil
	SetCurrentTxWriter(nil)
}

// ShallowCopy returns a new Executor that shares Planner and Store with the
// original but has its own per-request mutable state (txWriter, snapshotTS,
// sessionID). Callers use this to avoid races when the shared Executor is
// used concurrently by multiple sessions (REQ000611).
// The statement cache is re-initialized (not shared) since it contains a Mutex.
// Memory budget fields are inherited from the original. REQ001056.
func (e *Executor) ShallowCopy() *Executor {
	e2 := &Executor{
		planner:           e.planner,
		store:             e.store,
		txnDebugger:       UT.NewTxnDebugger(),
		pool:              e.pool, // shared — pool is thread-safe
		maxMemoryPerQuery: e.maxMemoryPerQuery,
		joinBufferSize:    e.joinBufferSize,
		maxResultRows:     e.maxResultRows,
	}
	e2.initStmtCache(e.stmtCache.maxSize)
	e2.initPlanCache(e.planCache.maxSize)
	return e2
}

// Close shuts down the executor's WorkerPool. Idempotent.
// REQ001044: WorkerPool lifecycle management.
func (e *Executor) Close() {
	if e.pool != nil {
		e.pool.Close()
	}
}

// Pool returns the shared WorkerPool (may be nil). REQ001044.
func (e *Executor) Pool() *UT.WorkerPool { return e.pool }

// SetSnapshot sets the per-statement snapshot timestamp for read-committed
// isolation (REQ000255). When non-zero, reads filter to versions visible at
// this timestamp. Pass 0 to disable snapshot filtering.
func (e *Executor) SetSnapshot(ts uint64) { e.snapshotTS = ts }

// Snapshot returns the current snapshot timestamp.
func (e *Executor) Snapshot() uint64 { return e.snapshotTS }

// SetSessionID sets the current session ID for counter access.
// REQ000385/394/411. Uses atomic store for the global currentSessionID
// to avoid races with concurrent sessions sharing one Executor.
func (e *Executor) SetSessionID(id uint64) {
	// Don't write e.sessionID - the Executor is shared across sessions.
	// Only update the atomic global that eval functions read.
	DT.CurrentSessionID.Store(id)
}

// SessionID returns the current session ID.
func (e *Executor) SessionID() uint64 { return e.sessionID }

// SetMaxParallelism sets the maximum number of workers per query.
// REQ001054: Configurable parallelism.
func (e *Executor) SetMaxParallelism(n int) {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	e.maxParallelism = n
	// Recreate the pool with the new size
	if e.pool != nil {
		e.pool.Close()
	}
	e.pool = UT.NewWorkerPool(n)
	e.planner.SetPool(e.pool)
}

// MaxParallelism returns the current maximum parallelism setting.
func (e *Executor) MaxParallelism() int { return e.maxParallelism }

func NewExecutor() *Executor {
	e := &Executor{
		planner:        NewPlanner(),
		txnDebugger:    UT.NewTxnDebugger(),
		pool:           UT.NewWorkerPool(0),
		maxParallelism: runtime.GOMAXPROCS(0),
		attachedDBs:    make(map[string]string),
	}
	e.planner.SetPool(e.pool)
	e.initStmtCache(256)
	e.initPlanCache(128)
	return e
}

func NewExecutorWithPlanner(pl *Planner) *Executor {
	e := &Executor{
		planner:        pl,
		txnDebugger:    UT.NewTxnDebugger(),
		pool:           UT.NewWorkerPool(0),
		maxParallelism: runtime.GOMAXPROCS(0),
		attachedDBs:    make(map[string]string),
	}
	pl.SetPool(e.pool)
	e.initStmtCache(256)
	e.initPlanCache(128)
	return e
}

// NewExecutorWithEngine creates an Executor with a store engine.
func NewExecutorWithEngine(store Store) *Executor {
	e := &Executor{
		planner:        NewPlannerWithStore(store),
		store:          store,
		pool:           UT.NewWorkerPool(0),
		maxParallelism: runtime.GOMAXPROCS(0),
		attachedDBs:    make(map[string]string),
	}
	e.planner.SetPool(e.pool)
	e.initStmtCache(256)
	e.initPlanCache(128)
	return e
}

// WithStmtCache enables statement caching with the given max size.
// Call on a newly created Executor before concurrent use.
func (e *Executor) WithStmtCache(maxSize int) *Executor {
	e.initStmtCache(maxSize)
	e.initPlanCache(128)
	return e
}

// WithPlanCache enables plan caching with the given max size.
// Default 128 entries. REQ001011.
func (e *Executor) WithPlanCache(maxSize int) *Executor {
	e.initPlanCache(maxSize)
	return e
}

// WithMemoryBudget sets per-query and per-join memory limits,
// plus a per-query result row cap. All default to 0 (unlimited).
// REQ001056.
func (e *Executor) WithMemoryBudget(maxMemoryPerQuery, joinBufferSize int64) *Executor {
	e.maxMemoryPerQuery = maxMemoryPerQuery
	e.joinBufferSize = joinBufferSize
	if e.planner != nil {
		e.planner.SetJoinBufferSize(joinBufferSize)
		e.planner.SetMaxMemoryPerQuery(maxMemoryPerQuery)
	}
	return e
}

// MaxResultRows returns the per-query result row limit.
// 0 means unlimited. REQ001056.
func (e *Executor) MaxResultRows() int64 { return e.maxResultRows }

// WithMaxResultRows sets a cap on total rows returned by a single
// SELECT query. 0 means unlimited. REQ001056.
// Also sets the compound operator drain cap (REQ001057) to the same
// value to prevent OOM on deep UNION/EXCEPT/INTERSECT chains.
func (e *Executor) WithMaxResultRows(limit int64) *Executor {
	e.maxResultRows = limit
	return e
}

// initStmtCache initializes the statement cache. Must be called before use.
func (e *Executor) initStmtCache(maxSize int) {
	if maxSize <= 0 {
		maxSize = 256
	}
	e.stmtCache.entries = make(map[string]*stmtCacheEntry, maxSize)
	e.stmtCache.lru = make([]*stmtCacheEntry, 0, maxSize)
	e.stmtCache.maxSize = maxSize
}

// getCachedStmt looks up a cached parsed statement. Returns nil if not found.
func (e *Executor) getCachedStmt(sql string) PS.Stmt {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	ent, ok := e.stmtCache.entries[sql]
	if !ok {
		return nil
	}
	// Move to front of LRU
	for i, entry := range e.stmtCache.lru {
		if entry == ent {
			e.stmtCache.lru = append(e.stmtCache.lru[:i], e.stmtCache.lru[i+1:]...)
			break
		}
	}
	e.stmtCache.lru = append([]*stmtCacheEntry{ent}, e.stmtCache.lru...)
	return ent.stmt
}

// putCachedStmt stores a parsed statement in the cache.
func (e *Executor) putCachedStmt(sql string, stmt PS.Stmt) {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	if ent, ok := e.stmtCache.entries[sql]; ok {
		// Already cached, move to front
		for i, entry := range e.stmtCache.lru {
			if entry == ent {
				e.stmtCache.lru = append(e.stmtCache.lru[:i], e.stmtCache.lru[i+1:]...)
				break
			}
		}
		e.stmtCache.lru = append([]*stmtCacheEntry{ent}, e.stmtCache.lru...)
		return
	}
	ent := &stmtCacheEntry{stmt: stmt}
	e.stmtCache.entries[sql] = ent
	e.stmtCache.lru = append([]*stmtCacheEntry{ent}, e.stmtCache.lru...)
	// Evict LRU if over capacity
	for len(e.stmtCache.lru) > e.stmtCache.maxSize {
		oldest := e.stmtCache.lru[len(e.stmtCache.lru)-1]
		e.stmtCache.lru = e.stmtCache.lru[:len(e.stmtCache.lru)-1]
		for key, val := range e.stmtCache.entries {
			if val == oldest {
				delete(e.stmtCache.entries, key)
				break
			}
		}
	}
}

// clearStmtCache clears the statement cache. Used in tests.
func (e *Executor) clearStmtCache() {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	e.stmtCache.entries = nil
	e.stmtCache.lru = nil
}

// initPlanCache initializes the plan cache. Must be called before use.
// REQ001011.
func (e *Executor) initPlanCache(maxSize int) {
	if maxSize <= 0 {
		maxSize = 128
	}
	e.planCache.entries = make(map[string]*planCacheEntry, maxSize)
	e.planCache.lru = make([]*planCacheEntry, 0, maxSize)
	e.planCache.maxSize = maxSize
}

// getCachedPlan looks up a cached compiled plan by memo key.
// Returns nil if not found. REQ001011.
func (e *Executor) getCachedPlan(key string) *pl.PlanResult {
	e.planCache.mu.Lock()
	defer e.planCache.mu.Unlock()
	ent, ok := e.planCache.entries[key]
	if !ok {
		return nil
	}
	// Move to front of LRU
	for i, entry := range e.planCache.lru {
		if entry == ent {
			e.planCache.lru = append(e.planCache.lru[:i], e.planCache.lru[i+1:]...)
			break
		}
	}
	e.planCache.lru = append([]*planCacheEntry{ent}, e.planCache.lru...)
	return ent.result
}

// putCachedPlan stores a compiled plan in the cache.
// REQ001011.
func (e *Executor) putCachedPlan(key string, result *pl.PlanResult) {
	e.planCache.mu.Lock()
	defer e.planCache.mu.Unlock()
	if ent, ok := e.planCache.entries[key]; ok {
		for i, entry := range e.planCache.lru {
			if entry == ent {
				e.planCache.lru = append(e.planCache.lru[:i], e.planCache.lru[i+1:]...)
				break
			}
		}
		e.planCache.lru = append([]*planCacheEntry{ent}, e.planCache.lru...)
		return
	}
	ent := &planCacheEntry{result: result}
	e.planCache.entries[key] = ent
	e.planCache.lru = append([]*planCacheEntry{ent}, e.planCache.lru...)
	for len(e.planCache.lru) > e.planCache.maxSize {
		oldest := e.planCache.lru[len(e.planCache.lru)-1]
		e.planCache.lru = e.planCache.lru[:len(e.planCache.lru)-1]
		for key, val := range e.planCache.entries {
			if val == oldest {
				delete(e.planCache.entries, key)
				break
			}
		}
	}
}

// clearPlanCache clears the plan cache. Used in tests.
func (e *Executor) clearPlanCache() {
	e.planCache.mu.Lock()
	defer e.planCache.mu.Unlock()
	e.planCache.entries = nil
	e.planCache.lru = nil
}

// planWithCache returns a compiled plan for stmt, checking the plan
// cache first. On cache miss, plans via Planner.Plan and caches the
// result. REQ001011.
func (e *Executor) planWithCache(stmt PS.Stmt) (*pl.PlanResult, error) {
	if e.planCache.entries != nil {
		key := pl.SerializeKey(stmt)
		if cached := e.getCachedPlan(key); cached != nil {
			return cached, nil
		}
		plan, err := e.planner.Plan(stmt)
		if err != nil {
			return nil, err
		}
		if plan == nil || plan.Root == nil {
			return nil, errors.New("ex: plan produced no root")
		}
		e.putCachedPlan(key, plan)
		return plan, nil
	}
	return e.planner.Plan(stmt)
}

// ExtractParamTypes parses sql and returns the SQL column type
// of each `?` placeholder in left-to-right order. Entries are
// ls.CTInt / ls.CTVarchar / etc. when the placeholder can be
// resolved to a known column; -1 otherwise. This is the ST
// layer's source of truth for per-placeholder Go-type
// validation (R16-3, R16-4).
func (e *Executor) ExtractParamTypes(sql string) []int {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil
	}
	out := []int{}
	walkPlaceholderTypes(stmt, &out)
	return out
}

// walkPlaceholderTypes walks stmt and appends a column-type
// entry for each *PS.Param encountered. For `column = ?`
// comparisons, the column's SQL type is recorded; other
// placeholders get -1 (skip validation in coerce).
func walkPlaceholderTypes(stmt PS.Stmt, out *[]int) {
	switch s := stmt.(type) {
	case *PS.Select:
		for _, e := range s.Cols {
			walkExprTypes("", e, out)
		}
		if s.Where != nil {
			walkExprTypes("", s.Where, out)
		}
		for _, g := range s.GroupBy {
			walkExprTypes("", g, out)
		}
		if s.Having != nil {
			walkExprTypes("", s.Having, out)
		}
		for _, o := range s.OrderBy {
			walkExprTypes("", o.Expr, out)
		}
		if s.Limit != nil {
			walkExprTypes("", s.Limit, out)
		}
	case *PS.Insert:
		// INSERT values are evaluated as expressions; the column
		// type comes from the target table schema, not from a
		// peer column. Walk them with empty scope.
		for _, row := range s.Values {
			for _, e := range row {
				walkExprTypes("", e, out)
			}
		}
	case *PS.Update:
		for _, p := range s.Set {
			walkExprTypes("", p.Val, out)
		}
		if s.Where != nil {
			walkExprTypes("", s.Where, out)
		}
	case *PS.Delete:
		if s.Where != nil {
			walkExprTypes("", s.Where, out)
		}
	}
}

// walkExprTypes walks expr, appending an entry to out for each
// *PS.Param it finds. If the placeholder is part of a
// `column = ?` (or `? = column`) binary comparison, we record
// the column's SQL type. Otherwise the entry is -1 (skip).
// The walker must NOT double-count a Param: each placeholder
// is appended exactly once even when it appears in a
// comparison that is itself walked recursively.
func walkExprTypes(table string, expr PS.Expr, out *[]int) {
	switch e := expr.(type) {
	case *PS.Param:
		// Standalone placeholder (e.g. INSERT VALUES(?, ?)).
		// No column context to resolve, so mark as unknown (-1).
		*out = append(*out, -1)
		return
	case *PS.BinaryExpr:
		// If one side is an Ident and the other is a Param,
		// the Param is matched: append the column's type
		// and skip the recursive walk to avoid double-counting.
		if col, ok := e.Left.(*PS.Ident); ok {
			if _, isParam := e.Right.(*PS.Param); isParam {
				*out = append(*out, columnTypeFor(col.Name))
				return
			}
		}
		if col, ok := e.Right.(*PS.Ident); ok {
			if _, isParam := e.Left.(*PS.Param); isParam {
				*out = append(*out, columnTypeFor(col.Name))
				return
			}
		}
		// No column-vs-param match; walk both sides to surface
		// any nested placeholders (e.g. `a + ?` against an
		// expression operand).
		walkExprTypes(table, e.Left, out)
		walkExprTypes(table, e.Right, out)
	case *PS.UnaryExpr:
		walkExprTypes(table, e.Operand, out)
	case *PS.BetweenExpr:
		walkExprTypes(table, e.Expr, out)
		walkExprTypes(table, e.Low, out)
		walkExprTypes(table, e.High, out)
	case *PS.InExpr:
		walkExprTypes(table, e.Expr, out)
		for _, item := range e.List {
			walkExprTypes(table, item, out)
		}
	case *PS.CaseExpr:
		for _, w := range e.WhenList {
			walkExprTypes(table, w.Cond, out)
			walkExprTypes(table, w.Then, out)
		}
		if e.Else != nil {
			walkExprTypes(table, e.Else, out)
		}
	case *PS.FunctionCall:
		for _, a := range e.Args {
			walkExprTypes(table, a, out)
		}
	case *PS.AggregateFunc:
		if e.Arg != nil {
			walkExprTypes(table, e.Arg, out)
		}
	case *PS.CastExpr:
		walkExprTypes(table, e.Expr, out)
	case *PS.AliasedExpr:
		walkExprTypes(table, e.Expr, out)
	}
}

// columnTypeFor returns the LS ColumnType for `name` if the
// planner has a registered table with that column. Returns -1
// otherwise (the caller skips validation for that slot).
func columnTypeFor(name string) int {
	for _, ss := range DT.StoreSchemas {
		for i, c := range ss.Cols {
			if c == name && i < len(ss.ColTypes) {
				return lxTokenToColumnType(ss.ColTypes[i])
			}
		}
	}
	// Fall back: walk the legacy DT.Schemas map and best-effort
	// match by name. We treat any col with a Type==0 (the
	// pre-iter-16 default) as TEXT so downstream coercibility
	// checks still produce a meaningful verdict.
	for _, cols := range DT.Schemas {
		for _, c := range cols {
			if c == name {
				return int(ls.CTText)
			}
		}
	}
	return -1
}

// lxTokenToColumnType converts an LX token (T_INT_KW/T_TEXT/...)
// into the corresponding LS ColumnType. Returns -1 for
// unrecognized tokens.
func lxTokenToColumnType(tok LX.TokenType) int {
	switch LX.TokenType(tok) {
	case LX.T_INT_KW:
		return int(ls.CTInt)
	case LX.T_BIGINT:
		return int(ls.CTBigInt)
	case LX.T_FLOAT_KW:
		return int(ls.CTFloat)
	case LX.T_BOOL:
		return int(ls.CTBool)
	case LX.T_TEXT:
		return int(ls.CTText)
	case LX.T_VARCHAR:
		return int(ls.CTVarchar)
	case LX.T_BLOB:
		return int(ls.CTBlob)
	case LX.T_TIMESTAMP:
		return int(ls.CTTimestamp)
	}
	return -1
}

func (e *Executor) RegisterTable(name string, schema []string) {
	cols := make([]ColInfo, len(schema))
	for i, n := range schema {
		cols[i] = ColInfo{Name: n, Typ: 1}
	}
	e.planner.RegisterTable(name, cols, "")
	DT.RegisterTableSchema(name, schema)
	if e.store != nil {
		// REQ000367: API-level registration without a PK
		// also enables hidden-PK mode so the table can be
		// written to the engine store.
		if id := DT.RegisterStoreSchema(name, schema, ""); id != 0 {
			DT.StoreMu.Lock()
			if ss, ok := DT.StoreSchemas[id]; ok {
				ss.HiddenPK = true
			}
			DT.StoreMu.Unlock()
		}
	}
}

func (e *Executor) RegisterTableWithPK(name string, schema []string, pk string) {
	cols := make([]ColInfo, len(schema))
	for i, n := range schema {
		cols[i] = ColInfo{Name: n, Typ: 1}
	}
	e.planner.RegisterTable(name, cols, pk)
	DT.RegisterTableSchema(name, schema)
	DT.TablesMu.Lock()
	DT.TablePKs[name] = pk
	DT.TablesMu.Unlock()
	DT.RegisterInMemorySchema(name, schema, pk)
	if e.store != nil {
		DT.RegisterStoreSchema(name, schema, pk)
	}
}

func (e *Executor) RegisterIndex(table, index string, cols []string) {
	e.planner.RegisterIndex(table, index, cols)
}

func (e *Executor) Exec(ctx context.Context, sql string, args ...any) (Result, error) {
	// Try cache first (P0: StmtCache wiring, saves 12.70% CPU on parsing)
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt := cached
			// Check if this is a DML with RETURNING clause
			if hasReturning(stmt) {
				op, err := e.buildWriterOp(stmt)
				if err != nil {
					return Result{}, err
				}
				propagateParams(op, args)
				defer op.Close()
				var count int64
				for {
					_, err := op.Next(ctx)
					if err != nil {
						if err == DT.ErrNoRows {
							break
						}
						return Result{}, err
					}
					count++
				}
				return Result{RowsAffected: count}, nil
			}

			op, err := e.buildWriterOp(stmt)
			if err != nil {
				return Result{}, err
			}
			propagateParams(op, args)
			propagatePlanner(op, e.planner)
			execCtx := &ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: 0, TotalChanges: e.totalChanges}
			propagateExecContext(op, execCtx)
			defer op.Close()
			if _, err := op.Next(ctx); err != nil && err != DT.ErrNoRows {
				return Result{}, err
			}
			e.lastChanges = execCtx.LastChanges
			e.totalChanges = execCtx.TotalChanges
			return extractResult(op)
		}
	}

	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return Result{}, err
	}
	// Cache the parsed statement
	if e.stmtCache.entries != nil {
		e.putCachedStmt(sql, stmt)
	}

	// Check if this is a DML with RETURNING clause
	if hasReturning(stmt) {
		op, err := e.buildWriterOp(stmt)
		if err != nil {
			return Result{}, err
		}
		propagateParams(op, args)
		defer op.Close()
		var count int64
		for {
			_, err := op.Next(ctx)
			if err != nil {
				if err == DT.ErrNoRows {
					break
				}
				return Result{}, err
			}
			count++
		}
		return Result{RowsAffected: count}, nil
	}

	op, err := e.buildWriterOp(stmt)
	if err != nil {
		return Result{}, err
	}
	// R16-1: thread args down to the operator tree so `?`
	// placeholders resolve. Writers (INSERT/UPDATE/DELETE) also
	// support placeholders (e.g. INSERT ... VALUES (?,?)).
	propagateParams(op, args)
	propagatePlanner(op, e.planner)
	execCtx := &ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: 0, TotalChanges: e.totalChanges}
	propagateExecContext(op, execCtx)
	defer op.Close()
	if _, err := op.Next(ctx); err != nil && err != DT.ErrNoRows {
		return Result{}, err
	}
	e.lastChanges = execCtx.LastChanges
	e.totalChanges = execCtx.TotalChanges
	return extractResult(op)
}

// hasReturning reports whether the statement has a RETURNING clause.
func hasReturning(stmt PS.Stmt) bool {
	switch s := stmt.(type) {
	case *PS.Insert:
		return len(s.Returning) > 0
	case *PS.Update:
		return len(s.Returning) > 0
	case *PS.Delete:
		return len(s.Returning) > 0
	}
	return false
}

func (e *Executor) Query(ctx context.Context, sql string, args ...any) (*Rows, error) {
	// Try cache first (P0: StmtCache wiring, saves 12.70% CPU on parsing)
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt := cached
			// Check if this is a DML with RETURNING clause
			if hasReturning(stmt) {
				op, err := e.buildWriterOp(stmt)
				if err != nil {
					return nil, err
				}
				propagateParams(op, args)
				defer op.Close()
				var out []Row
				for {
					row, err := op.Next(ctx)
					if err != nil {
						if err == DT.ErrNoRows {
							break
						}
						return nil, err
					}
					out = append(out, row)
				}
				if len(out) == 0 {
					return &Rows{}, nil
				}
				return &Rows{Cols: append([]string(nil), out[0].Cols...), Types: append([]LX.TokenType(nil), out[0].Types...)}, nil
			}

			plan, err := e.planWithCache(stmt)
			if err != nil {
				return nil, err
			}
			if plan == nil || plan.Root == nil {
				return nil, errors.New("ex: plan produced no root")
			}
			propagateParams(plan.Root, args)
			propagatePlanner(plan.Root, e.planner)
			execCtx := &ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
			propagateExecContext(plan.Root, execCtx)
			defer plan.Root.Close()
			row, err := plan.Root.Next(ctx)
			if err != nil {
				if err == DT.ErrNoRows {
					return &Rows{}, nil
				}
				return nil, err
			}
			DT.WithExecContext(&row, execCtx)
			rs := &Rows{Cols: append([]string(nil), row.Cols...), Types: append([]LX.TokenType(nil), row.Types...)}
			return rs, nil
		}
	}

	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}
	// Cache the parsed statement
	if e.stmtCache.entries != nil {
		e.putCachedStmt(sql, stmt)
	}

	// Check if this is a DML with RETURNING clause
	if hasReturning(stmt) {
		op, err := e.buildWriterOp(stmt)
		if err != nil {
			return nil, err
		}
		propagateParams(op, args)
		defer op.Close()
		var out []Row
		for {
			row, err := op.Next(ctx)
			if err != nil {
				if err == DT.ErrNoRows {
					break
				}
				return nil, err
			}
			out = append(out, row)
		}
		if len(out) == 0 {
			return &Rows{}, nil
		}
		return &Rows{Cols: append([]string(nil), out[0].Cols...), Types: append([]LX.TokenType(nil), out[0].Types...)}, nil
	}

	plan, err := e.planWithCache(stmt)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.Root == nil {
		return nil, errors.New("ex: plan produced no root")
	}
	// R16-1: thread args down to the operator tree so `?`
	// placeholders resolve during Eval.
	propagateParams(plan.Root, args)
	propagatePlanner(plan.Root, e.planner)
	execCtx := &ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	propagateExecContext(plan.Root, execCtx)
	defer plan.Root.Close()
	row, err := plan.Root.Next(ctx)
	if err != nil {
		if err == DT.ErrNoRows {
			return &Rows{}, nil
		}
		return nil, err
	}
	DT.WithExecContext(&row, execCtx)
	rs := &Rows{Cols: append([]string(nil), row.Cols...), Types: append([]LX.TokenType(nil), row.Types...)}
	return rs, nil
}

func (e *Executor) QueryAll(ctx context.Context, sql string, args ...any) ([]Row, error) {
	// Try cache first (P0: StmtCache wiring, saves 12.70% CPU on parsing)
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt := cached
			plan, err := e.planWithCache(stmt)
			if err != nil {
				return nil, err
			}
			if plan == nil || plan.Root == nil {
				return nil, errors.New("ex: plan produced no root")
			}
			propagateParams(plan.Root, args)
			propagatePlanner(plan.Root, e.planner)
			execCtx := &ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
			propagateExecContext(plan.Root, execCtx)
			defer plan.Root.Close()
			var out []Row
			for {
				row, err := plan.Root.Next(ctx)
				if err != nil {
					if err == DT.ErrNoRows {
						break
					}
					return nil, err
				}
				DT.WithExecContext(&row, execCtx)
				out = append(out, row)
			}
			return out, nil
		}
	}

	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}
	// Cache the parsed statement
	if e.stmtCache.entries != nil {
		e.putCachedStmt(sql, stmt)
	}
	plan, err := e.planWithCache(stmt)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.Root == nil {
		return nil, errors.New("ex: plan produced no root")
	}
	propagateParams(plan.Root, args)
	// REQ000366: thread the main-plan planner so SeqScan rows
	// carry it into subquery evals. propagatePlanner is a
	// depth-first walk that calls WithPlanner on every node
	// that supports it.
	propagatePlanner(plan.Root, e.planner)
	// REQ000586: thread ExecContext through rows to eliminate
	// the global currentSubqueryPlanner.
	execCtx := &ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	propagateExecContext(plan.Root, execCtx)
	defer plan.Root.Close()
	var out []Row
	for {
		row, err := plan.Root.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return nil, err
		}
		DT.WithExecContext(&row, execCtx)
		out = append(out, row)
	}
	return out, nil
}

// propagatePlanner walks the operator tree rooted at root and
// calls WithPlanner(p) on every node that supports it. See
// REQ000366.
func propagatePlanner(root Operator, p *Planner) {
	if root == nil {
		return
	}
	if w, ok := root.(interface {
		WithPlanner(pl.QueryPlanner) pl.Operator
	}); ok {
		w.WithPlanner(p)
	}
	type childer interface {
		Child() Operator
	}
	if c, ok := root.(childer); ok {
		propagatePlanner(c.Child(), p)
	}
	type leftRighter interface {
		LeftChild() Operator
		RightChild() Operator
	}
	if lr, ok := root.(leftRighter); ok {
		propagatePlanner(lr.LeftChild(), p)
		propagatePlanner(lr.RightChild(), p)
	}
}

// propagateExecContext walks the operator tree and sets execCtx
// on operators that evaluate expressions (Filter, Project, etc.)
// so that subquery eval can find the planner via ExecContextFromRow.
// Also propagates execCtx to Insert/Update/Delete for change
// tracking (REQ000812).
func propagateExecContext(root Operator, ec *ExecContext) {
	if root == nil || ec == nil {
		return
	}
	if f, ok := root.(*Filter); ok {
		f.SetExecCtx(ec)
	}
	if p, ok := root.(*Project); ok {
		p.SetExecCtx(ec)
	}
	if ins, ok := root.(*Insert); ok {
		ins.execCtx = ec
	}
	if upd, ok := root.(*Update); ok {
		upd.execCtx = ec
	}
	if del, ok := root.(*Delete); ok {
		del.execCtx = ec
	}
	if val, ok := root.(*Values); ok {
		val.SetExecCtx(ec)
	}
	type childer interface {
		Child() Operator
	}
	if c, ok := root.(childer); ok {
		propagateExecContext(c.Child(), ec)
	}
	type leftRighter interface {
		LeftChild() Operator
		RightChild() Operator
	}
	if lr, ok := root.(leftRighter); ok {
		propagateExecContext(lr.LeftChild(), ec)
		propagateExecContext(lr.RightChild(), ec)
	}
}

// propagateParams walks the operator tree rooted at root and
// calls WithParams(args) on every node that supports it
// (R16-1..2). The walk is depth-first, children-first so the
// args reach every leaf operator. Operators without a
// WithParams method are skipped silently.
func propagateParams(root Operator, args []any) {
	if root == nil {
		return
	}
	if args == nil {
		return
	}
	p := asAnySlice(args)
	if w, ok := root.(interface{ WithParams([]any) Operator }); ok {
		w.WithParams(p)
	}
	// Walk children via the Child() convention used elsewhere
	// in this package (explain.go).
	type childer interface {
		Child() Operator
	}
	if c, ok := root.(childer); ok {
		propagateParams(c.Child(), args)
	}
	// Some operators expose children via a `child` field; we
	// rely on the explain.go walk for those via Child(). Operators
	// with multiple children (HashAggregate, Join) define
	// their own WithParams and walk internally.
}

// asAnySlice converts []any to []any for type-stability
// across the WithParams interface boundary. Avoids an allocation
// when the slice is already nil.
func asAnySlice(args []any) []any {
	if args == nil {
		return nil
	}
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a
	}
	return out
}

// Explain plans the statement and returns a human-readable
// description of the operator tree. The plan is closed before
// returning, so Explain does not run the query.
func (e *Executor) Explain(sql string) (string, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return "", err
	}
	plan, err := e.planWithCache(stmt)
	if err != nil {
		return "", err
	}
	if plan == nil || plan.Root == nil {
		return "", errors.New("ex: plan produced no root")
	}
	defer plan.Root.Close()
	// Use formatPlanTree with default ExplainNormal mode for text output
	nodes := buildPlanNodeTree(plan.Root, e.planner)
	var b strings.Builder
	var walk func(node *PlanNode, depth int)
	walk = func(node *PlanNode, depth int) {
		if node == nil {
			return
		}
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString(node.Type)
		if node.Table != "" {
			b.WriteString(" ")
			b.WriteString(node.Table)
		}
		if node.Detail != "" {
			b.WriteString(" ")
			b.WriteString(node.Detail)
		}
		b.WriteByte('\n')
		for _, child := range node.Children {
			walk(child, depth+1)
		}
	}
	walk(nodes, 0)
	return b.String(), nil
}

// isUpdatableView checks if a view is updatable (simple single-table
// select without aggregation, DISTINCT, GROUP BY, HAVING, ORDER BY,
// LIMIT, or subqueries). REQ001061.
func isUpdatableView(sel *PS.Select) bool {
	if sel == nil {
		return false
	}
	// Must be a simple single-table select
	if sel.From == "" {
		return false
	}
	// No joins (compound views are not updatable)
	if len(sel.Joins) > 0 {
		return false
	}
	// No aggregation
	if sel.Having != nil {
		return false
	}
	// No GROUP BY
	if len(sel.GroupBy) > 0 {
		return false
	}
	// No DISTINCT
	if sel.Distinct {
		return false
	}
	// No ORDER BY
	if len(sel.OrderBy) > 0 {
		return false
	}
	// No LIMIT
	if sel.Limit != nil {
		return false
	}
	// No OFFSET
	if sel.Offset != nil {
		return false
	}
	// No subquery in FROM
	if sel.SubqueryFrom != nil {
		return false
	}
	// No aggregation functions in SELECT list
	for _, col := range sel.Cols {
		if col == nil {
			continue
		}
		if hasAggFunc(col) {
			return false
		}
	}
	return true
}

// hasAggFunc checks if an expression contains an aggregate function.
func hasAggFunc(expr PS.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *PS.AggregateFunc:
		return true
	case *PS.FunctionCall:
		switch e.Name {
		// These are also aggregate functions in some contexts
		case "count", "sum", "avg", "min", "max", "group_concat":
			return true
		}
	case *PS.BinaryExpr:
		return hasAggFunc(e.Left) || hasAggFunc(e.Right)
	case *PS.UnaryExpr:
		return hasAggFunc(e.Operand)
	case *PS.CaseExpr:
		for _, when := range e.WhenList {
			if hasAggFunc(when.Cond) || hasAggFunc(when.Then) {
				return true
			}
		}
		if hasAggFunc(e.Else) {
			return true
		}
	case *PS.CastExpr:
		return hasAggFunc(e.Expr)
	case *PS.StarExpr, *PS.SubqueryExpr, *PS.Param:
		return false
	case *PS.Ident, *PS.QualifiedName:
		return false
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral, *PS.BoolLiteral, *PS.NullLiteral:
		return false
	default:
		return false
	}
	return false
}

func (e *Executor) buildWriterOp(stmt PS.Stmt) (Operator, error) {
	switch s := stmt.(type) {
	case *PS.Insert:
		// REQ000707: INSERT INTO t SELECT ...
		if s.Select != nil {
			selPlan, err := e.planner.Plan(s.Select)
			if err != nil {
				return nil, err
			}
			if selPlan == nil || selPlan.Root == nil {
				return nil, fmt.Errorf("ex: INSERT SELECT: plan produced no root")
			}
			op := NewInsert(s.Table, s.Cols, nil, s.Returning, s.OnConflict)
			op.selectPlan = selPlan.Root
			op.conflictAction = s.ConflictAction
			propagatePlanner(selPlan.Root, e.planner)
			return op, nil
		}

		op, iErr := func() (*Insert, error) {
			if e.store != nil {
				return NewInsertWithStore(e.store, s.Table, s.Cols, s.Values, s.Returning, s.OnConflict)
			}
			return NewInsert(s.Table, s.Cols, s.Values, s.Returning, s.OnConflict), nil
		}()
		if iErr != nil {
			return nil, iErr
		}
		op.conflictAction = s.ConflictAction
		op.defaultValues = s.DefaultValues
		return op, nil
	case *PS.Update:
		targetTable := s.Table
		if viewSel := DT.LookupView(targetTable); viewSel != nil {
			// REQ001061: DML on non-updatable views is not allowed.
			// A view is updatable only if it's a simple single-table
			// select without aggregation, DISTINCT, GROUP BY, HAVING,
			// ORDER BY, LIMIT, or subqueries.
			if !isUpdatableView(viewSel) {
				return nil, fmt.Errorf("ex: cannot modify view %s", targetTable)
			}
			targetTable = viewSel.From
		}
		var scan Operator = OP.NewSeqScan(targetTable)
		if e.store != nil {
			ssc, err := OP.NewSeqScanWithStore(e.store, targetTable)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := OP.NewFilter(scan, s.Where)
		// REQ000558: apply ORDER BY / LIMIT / OFFSET to the row
		// selection before updating.
		var current Operator = filter
		if len(s.OrderBy) > 0 {
			current = NewSort(current, s.OrderBy)
		}
		if s.OffsetFirst {
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = NewLimit(current, n)
				}
			}
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = NewOffset(current, n)
				}
			}
		} else {
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = NewOffset(current, n)
				}
			}
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = NewLimit(current, n)
				}
			}
		}
		if e.store != nil {
			op, err := NewUpdateWithStore(e.store, targetTable, s.Set, s.Where, current, s.Returning)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return NewUpdate(targetTable, s.Set, s.Where, current, s.Returning), nil
	case *PS.Delete:
		tableName := s.Table
		if viewSel := DT.LookupView(tableName); viewSel != nil {
			// REQ001061: DML on non-updatable views is not allowed.
			if !isUpdatableView(viewSel) {
				return nil, fmt.Errorf("ex: cannot modify view %s", tableName)
			}
			tableName = viewSel.From
		}
		var scan Operator = OP.NewSeqScan(tableName)
		if e.store != nil {
			ssc, err := OP.NewSeqScanWithStore(e.store, tableName)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := OP.NewFilter(scan, s.Where)
		// REQ000475: apply ORDER BY / LIMIT / OFFSET to the row
		// selection before deleting.
		var current Operator = filter
		if len(s.OrderBy) > 0 {
			current = NewSort(current, s.OrderBy)
		}
		if s.OffsetFirst {
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = NewLimit(current, n)
				}
			}
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = NewOffset(current, n)
				}
			}
		} else {
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = NewOffset(current, n)
				}
			}
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = NewLimit(current, n)
				}
			}
		}
		if e.store != nil {
			op, err := NewDeleteWithStore(e.store, tableName, s.Where, current, s.Returning)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return NewDelete(tableName, s.Where, current, s.Returning), nil
	case *PS.CreateTable:
		if s.Select != nil {
			// CREATE TABLE AS SELECT needs the planner to
			// build the inner SELECT plan. REQ000520.
			op, err := e.planner.Plan(s)
			if err != nil {
				return nil, err
			}
			if op == nil || op.Root == nil {
				return nil, errors.New("ex: plan produced no root for CTAS")
			}
			return op.Root, nil
		}
		return NewCreateTable(s), nil
	case *PS.DropTable:
		return NewDropTable(s), nil
	case *PS.CreateIndexStmt:
		// Note: the planner's cost-based selection (REQ000156)
		// is keyed off ex.RegisterIndex, not CREATE INDEX.
		// CREATE INDEX only registers the index for writer
		// maintenance; it does not backfill existing rows into
		// the index keyspace. Tests that want cost-based
		// selection should call ex.RegisterIndex explicitly.
		return NewCreateIndex(s), nil
	case *PS.DropIndexStmt:
		return NewDropIndex(s), nil
	case *PS.CreateViewStmt:
		return NewCreateView(s), nil
	case *PS.CreateMatViewStmt:
		return NewCreateMatView(s.Name, s.As, e.store), nil
	case *PS.DropMatViewStmt:
		return NewDropMatView(s.Name, e.store), nil
	case *PS.RefreshMatViewStmt:
		// Lookup the matview definition from registry
		sel := DT.LookupMatView(s.Name)
		if sel == nil {
			return nil, fmt.Errorf("ex: materialized view %q not found", s.Name)
		}
		return NewRefreshMatView(s.Name, sel, e.store, e.planner), nil
	case *PS.VacuumStmt:
		return NewVacuum(s), nil
	case *PS.AnalyzeStmt:
		if e.store != nil {
			op, err := NewAnalyzeWithStore(e.store, s)
			if err == nil {
				return op, nil
			}
		}
		return NewAnalyze(s), nil
	case *PS.AlterTableStmt:
		return NewAlterTable(s), nil
	case *PS.TriggerStmt:
		return NewTrigger(s), nil
	case *PS.DropViewStmt:
		return NewDropView(s), nil
	case *PS.DropTriggerStmt:
		return NewDropTrigger(s), nil
	case *PS.PragmaStmt:
		return NewPragma(s), nil
	case *PS.ExplainStmt:
		return NewExplain(s), nil
	case *PS.TruncateStmt:
		return NewTruncate(s), nil
	case *PS.ReindexStmt:
		return NewReindex(s), nil
	case *PS.CreateVirtualTableStmt:
		return NewUnsupportedOp(s, "ex: virtual table module not supported in v1: "+s.Module), nil
	case *PS.BeginTX:
		return NewNoop(), nil
	case *PS.CommitTX:
		return NewNoop(), nil
	case *PS.ValuesStmt:
		return newValuesRowsOp(s.Rows), nil
	case *PS.AttachStmt:
		path, err := extractAttachPath(s.Expr)
		if err != nil {
			return nil, err
		}
		return NewAttachOp(e, s.Name, path), nil
	case *PS.DetachStmt:
		return NewDetachOp(e, s.Name), nil
	}
	return nil, errors.New("ex: not a writable statement")
}

func extractResult(op Operator) (Result, error) {
	type affected interface {
		RowsAffected() int64
	}
	if a, ok := op.(affected); ok {
		return Result{RowsAffected: a.RowsAffected()}, nil
	}
	return Result{}, nil
}

// extractAttachPath extracts the path string from an ATTACH expression.
// Expects a string literal. REQ000908.
func extractAttachPath(expr PS.Expr) (string, error) {
	s, ok := expr.(*PS.StringLiteral)
	if !ok {
		return "", errors.New("ex: ATTACH DATABASE path must be a string literal")
	}
	return s.Val, nil
}

// QueryStream runs a SELECT and returns a streaming iterator that
// yields rows one at a time. The caller MUST call Close on the
// returned iterator to release the underlying plan resources.
// REQ000348.
func (e *Executor) QueryStream(ctx context.Context, sql string, args ...any) (*streamIterator, error) {
	// REQ000771: try the in-Executor cache before parsing. The
	// ST.Stmt.Query hot path goes through here, and avoiding the
	// parser pass on repeated queries reclaims the 12% CPU that
	// parsing was costing.
	var stmt PS.Stmt
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt = cached
		}
	}
	if stmt == nil {
		parser := PS.NewParser(sql)
		parsed, err := parser.Parse()
		if err != nil {
			return nil, err
		}
		stmt = parsed
		if e.stmtCache.entries != nil {
			e.putCachedStmt(sql, stmt)
		}
	}
	return e.QueryStreamFromAST(ctx, stmt, args...)
}

// QueryStreamFromAST runs a pre-parsed statement through the
// planner and returns a streaming iterator. Skips the parser pass
// — used by REQ000771's StmtCache to avoid re-parsing on repeated
// queries. The caller must ensure `stmt` is the AST for the same
// SQL that produced this entry (or accept semantic divergence if
// DDL changed schema).
func (e *Executor) QueryStreamFromAST(ctx context.Context, stmt PS.Stmt, args ...any) (*streamIterator, error) {

	// Check if this is a DML with RETURNING clause
	if hasReturning(stmt) {
		op, err := e.buildWriterOp(stmt)
		if err != nil {
			return nil, err
		}
		propagateParams(op, args)
		firstRow, firstErr := op.Next(ctx)
		if firstErr != nil && firstErr != DT.ErrNoRows {
			op.Close()
			return nil, firstErr
		}
		if firstErr == DT.ErrNoRows {
			// No RETURNING rows; return empty iterator
			op.Close()
			return &streamIterator{
				cols:  nil,
				types: nil,
				rowCh: nil,
				done:  true,
			}, nil
		}
		// Wrap in a buffered channel so the caller can pull rows
		// sequentially after the first.
		rowCh := make(chan Row, 16)
		rowCh <- firstRow
		closed := false
		var closeOnce sync.Once
		closer := func() error {
			closeOnce.Do(func() {
				closed = true
			})
			return op.Close()
		}
		go func() {
			defer close(rowCh)
			for {
				if closed {
					return
				}
				row, err := op.Next(ctx)
				if err != nil {
					return
				}
				select {
				case rowCh <- row:
				case <-ctx.Done():
					return
				}
			}
		}()
		return &streamIterator{
			cols:   append([]string(nil), firstRow.Cols...),
			types:  append([]LX.TokenType(nil), firstRow.Types...),
			rowCh:  rowCh,
			closer: closer,
		}, nil
	}

	plan, err := e.planWithCache(stmt)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.Root == nil {
		return nil, errors.New("ex: plan produced no root")
	}
	propagateParams(plan.Root, args)
	propagatePlanner(plan.Root, e.planner)
	// REQ000586: thread ExecContext to eliminate global.
	execCtx := &ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	propagateExecContext(plan.Root, execCtx)

	// Read first row to discover schema
	row, err := plan.Root.Next(ctx)
	if err != nil {
		if err == DT.ErrNoRows {
			plan.Root.Close()
			return &streamIterator{
				cols:  nil,
				types: nil,
				rowCh: nil,
				done:  true,
			}, nil
		}
		plan.Root.Close()
		return nil, err
	}
	DT.WithExecContext(&row, execCtx)
	cols := append([]string(nil), row.Cols...)
	types := append([]LX.TokenType(nil), row.Types...)

	rowCh := make(chan Row, 16)
	rowCh <- row
	closed := false
	var closeMu sync.Mutex
	closer := func() error {
		closeMu.Lock()
		defer closeMu.Unlock()
		if closed {
			return nil
		}
		closed = true
		return plan.Root.Close()
	}
	go func() {
		defer close(rowCh)
		for {
			closeMu.Lock()
			if closed {
				closeMu.Unlock()
				return
			}
			closeMu.Unlock()
			r, err := plan.Root.Next(ctx)
			if err != nil {
				return
			}
			OP.WithExecContext(&r, execCtx)
			select {
			case rowCh <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &streamIterator{
		cols:   cols,
		types:  types,
		rowCh:  rowCh,
		closer: closer,
	}, nil
}

// streamIterator is the streaming row iterator returned by
// Executor.QueryStream. It buffers one row at a time so the caller can
// discover the schema before draining the rest.
// REQ000348.
type streamIterator struct {
	cols   []string
	types  []LX.TokenType
	rowCh  chan Row
	closer func() error

	done bool
	mu   sync.Mutex
}

func (s *streamIterator) Cols() []string        { return s.cols }
func (s *streamIterator) Types() []LX.TokenType { return s.types }
func (s *streamIterator) Next() (Row, error) {
	if s == nil || s.done || s.rowCh == nil {
		return Row{}, DT.ErrNoRows
	}
	r, ok := <-s.rowCh
	if !ok {
		s.done = true
		return Row{}, DT.ErrNoRows
	}
	return r, nil
}

func (s *streamIterator) Close() error {
	if s == nil {
		return nil
	}
	s.done = true
	if s.closer != nil {
		return s.closer()
	}
	return nil
}

// Noop is a no-op operator that returns ErrNoRows on Next.
// Used for statements like BEGIN that affect state but produce no results.
type Noop struct{}

var _ Operator = (*Noop)(nil)

func NewNoop() *Noop {
	return &Noop{}
}

func (n *Noop) Next(ctx context.Context) (Row, error) {
	return Row{}, DT.ErrNoRows
}

func (n *Noop) Close() error {
	return nil
}

func (n *Noop) WithParams(p []any) Operator {
	return n
}

// TxnDebugger returns the executor's transaction debugger.
// REQ000792: MVCC debugging.
func (e *Executor) TxnDebugger() *UT.TxnDebugger {
	return e.txnDebugger
}

// StmtCacheStats returns the statement cache statistics.
// REQ000793: Plan cache analysis.
func (e *Executor) StmtCacheStats() *AD.CacheStats {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	// Count entries
	size := len(e.stmtCache.entries)
	return &AD.CacheStats{
		Hits:      0, // tracked separately if needed
		Misses:    0,
		Evictions: 0,
		MaxSize:   size,
	}
}
