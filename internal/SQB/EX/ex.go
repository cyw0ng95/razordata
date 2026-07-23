package EX

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/SQB/AD"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

var ErrNotImplemented = errors.New("ex: not implemented")
var ErrClosed = errors.New("ex: operator closed")

// Re-export for SYS/SE backward compatibility (moved to WT).
var ErrMultiDatabaseNotSupported = WT.ErrMultiDatabaseNotSupported

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
func NewIntValue(v int64) DT.Value { return AP.NewIntValue(v) }

// NewFloatValue creates a Value from a float64.
func NewFloatValue(v float64) DT.Value { return AP.NewFloatValue(v) }

// NewTextValue creates a Value from a string.
func NewTextValue(v string) DT.Value { return AP.NewTextValue(v) }

// NewBlobValue creates a Value from a byte slice.
func NewBlobValue(v []byte) DT.Value { return AP.NewBlobValue(v) }

// NewBoolValue creates a Value from a bool.
func NewBoolValue(v bool) DT.Value { return AP.NewBoolValue(v) }

// NullValue returns a NULL Value.
func NullValue() DT.Value { return AP.NullValue() }

// valueToString converts a Value to its string representation without
// going through fmt.Sprint (no reflection, no boxing). REQ001015.
// REQ001066: delegates to Value.String() for the per-kind switch.
func valueToString(v DT.Value) string {
	return v.String()
}

// valueFromAny creates a Value from a boxed any. Inverse of ToAny.
func valueFromAny(a any) DT.Value {
	if a == nil {
		return NullValue()
	}
	if v, ok := a.(DT.Value); ok {
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

// valueFromAnySlice converts a []any to []DT.Value.
// REQ001580: uses buf to avoid per-query make([]DT.Value, N).
// Pass nil for buf to allocate a new slice.
func valueFromAnySlice(a []any, buf *[]DT.Value) []DT.Value {
	if a == nil {
		return nil
	}
	if buf != nil {
		if cap(*buf) < len(a) {
			*buf = make([]DT.Value, len(a))
		}
		*buf = (*buf)[:len(a)]
		for i, v := range a {
			(*buf)[i] = valueFromAny(v)
		}
		return *buf
	}
	out := make([]DT.Value, len(a))
	for i, v := range a {
		out[i] = valueFromAny(v)
	}
	return out
}

// valueSliceToAny converts a []DT.Value to []any.
func valueSliceToAny(v []DT.Value) []any {
	if v == nil {
		return nil
	}
	out := make([]any, len(v))
	for i, val := range v {
		out[i] = val.ToAny()
	}
	return out
}

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

// stmtCacheEntry holds a cached parsed statement plus the SQL key
// (the key is needed to delete the map entry on LRU eviction).
// REQ001693: LRU position is tracked by the container/list element, not
// a per-entry field.
type stmtCacheEntry struct {
	key  string
	stmt PS.Stmt
}

// planCacheEntry holds a cached compiled plan with LRU metadata.
// REQ001011.
type planCacheEntry struct {
	result *pl.PlanResult
}

// textPlanCache is a simple LRU cache keyed by exact SQL text.
// Bypasses stmtCache+planCache+memo-key overhead for identical queries.
// REQ001464.
type textPlanCache struct {
	mu      sync.Mutex
	maxSize int
	entries map[string]*textPlanEntry
	lru     []*textPlanEntry
}

// textPlanEntry holds a cached PlanResult for exact SQL text.
type textPlanEntry struct {
	sql  string
	plan *pl.PlanResult
}

// globalStmtCache is the shared statement cache across all Executors.
// REQ001223: eliminates per-Executor stmtCache allocation (171 MB per query).
// Initialized lazily on first NewExecutor call.
var globalStmtCache = &stmtCache{
	entries: make(map[string]*list.Element, 1024),
	lru:     list.New(),
	maxSize: 1024,
}

// stmtCache is a thread-safe LRU cache for parsed statements.
// REQ001220: shared across ShallowCopy clones via pointer.
// REQ001693: true O(1) LRU via container/list (map[string]*list.Element
// + doubly-linked list). The previous design used a lastAccess counter
// and a full O(N) map scan on every over-capacity eviction, which cost
// 120ms / 16.4% of CPU in slt_good_0 (runtime.mapIterNext). MoveToFront
// on get and PushFront + evictBack on put are both O(1) and allocation-free.
type stmtCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	lru     *list.List
	maxSize int
}

// planCache is a thread-safe LRU cache for compiled plan trees.
// REQ001220: shared across ShallowCopy clones via pointer.
type planCache struct {
	mu      sync.Mutex
	entries map[string]*planCacheEntry
	lru     []*planCacheEntry
	maxSize int
}

// Executor holds the core execution state.
type Executor struct {
	planner    *Planner
	store      DT.Store
	txWriter   DT.TxWriter
	snapshotTS uint64 // REQ000255: per-statement snapshot timestamp for read-committed
	sessionID  uint64 // REQ000385/394/411: current session ID for counter access
	// lastChanges tracks rows modified by the most recent DML statement.
	// Persisted across Exec/Query calls so CHANGES() reports the correct
	// value even after intervening non-DML statements (REQ000812).
	lastChanges int64
	// totalChanges tracks cumulative DML row count across all statements
	// in the session. Copied into/out of DT.ExecContext for each Exec/Query
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
	// Pointer shared across ShallowCopy clones (REQ001220).
	stmtCache *stmtCache
	// planCache caches compiled plan trees keyed by AST fingerprint
	// (memo key) to avoid re-planning on repeated queries. LRU eviction,
	// default 128 entries. Pointer shared across ShallowCopy clones
	// (REQ001220).
	planCache *planCache
	// textPlanCache caches PlanResult keyed by exact SQL text.
	// Bypasses stmtCache+planCache memo-key overhead for identical
	// queries. LRU eviction, default 1000 entries. REQ001464.
	textPlanCache *textPlanCache
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

	// rowArena is a double-pointer to the persistent RowArena owned by
	// the Engine. All ShallowCopy clones point to the same location so
	// arena writes propagate to the Engine's field and persist across
	// queries without per-query slab allocation (REQ001419).
	rowArena **DT.RowArena

	// REQ001579: paramBuf is a reusable buffer for propagating params
	// to the operator tree, avoiding per-query make([]any, N) in asAnySlice.
	paramBuf []any
	// REQ001580: valueParamBuf is a reusable buffer for converting
	// []any params to []DT.Value, avoiding per-query make([]DT.Value, N).
	valueParamBuf []DT.Value

	closed atomic.Bool
}

// TxWriter is the optional hook an Executor notifies on every key
// write. Aliased from PL.
func (e *Executor) SetTxWriter(w DT.TxWriter) {
	e.txWriter = w
	DT.SetCurrentTxWriter(w)
}

// ClearTxWriter resets the write hook to nil. Pair with SetTxWriter.
func (e *Executor) ClearTxWriter() {
	e.txWriter = nil
	DT.SetCurrentTxWriter(nil)
}

// AttachDB implements DT.DBAttachManager.
func (e *Executor) AttachDB(name, path string) {
	e.attachedDBs[name] = path
}

// DetachDB implements DT.DBAttachManager.
func (e *Executor) DetachDB(name string) {
	delete(e.attachedDBs, name)
}

// GetAttachedDBs implements DT.DBAttachManager.
func (e *Executor) GetAttachedDBs() map[string]string {
	return e.attachedDBs
}

// ShallowCopy returns a new Executor that shares Planner, Store, stmtCache,
// and planCache with the original. Each clone has its own per-request
// mutable state (txWriter, snapshotTS, sessionID).
// Callers use this to avoid races when the shared Executor is used
// concurrently by multiple sessions (REQ000611).
// The stmtCache and planCache are shared via pointer — both are thread-safe
// LRU with mutex (REQ001220, REQ001259). PlanResult is immutable after
// compilation; replaceLiteralsOnTree operates on the cached tree and is
// safe because concurrent calls use different param slices and the tree
// nodes are not mutated during execution.
// Memory budget fields are inherited from the original. REQ001056.
func (e *Executor) ShallowCopy() *Executor {
	EC.WARN_ON(e.closed.Load(), "ShallowCopy on closed Executor")
	e2 := &Executor{
		planner:           e.planner,
		store:             e.store,
		stmtCache:         e.stmtCache, // shared — thread-safe LRU with mutex
		planCache:         e.planCache, // shared — REQ001259: immutable after compilation
		txnDebugger:       UT.NewTxnDebugger(),
		pool:              e.pool, // shared — pool is thread-safe
		maxMemoryPerQuery: e.maxMemoryPerQuery,
		joinBufferSize:    e.joinBufferSize,
		maxResultRows:     e.maxResultRows,
		rowArena:          e.rowArena, // shared — REQ001419: points to Engine's field
	}
	return e2
}

// Close shuts down the executor's WorkerPool. Idempotent.
// REQ001044: WorkerPool lifecycle management.
func (e *Executor) Close() {
	e.closed.Store(true)
	if e.pool != nil {
		e.pool.Close()
	}
}

// Pool returns the shared WorkerPool (may be nil). REQ001044.
func (e *Executor) Pool() *UT.WorkerPool { return e.pool }

// SetRowArena links the persistent RowArena from the Engine into this
// Executor. All ShallowCopy clones inherit the same double-pointer so
// arena writes propagate to the Engine's field (REQ001419).
func (e *Executor) SetRowArena(ptr **DT.RowArena) { e.rowArena = ptr }

// ensureArena returns the persistent RowArena (REQ001419), creating it
// on first call. The arena is stored on the Engine via the rowArena
// double-pointer, so it persists across ShallowCopy clones. Returns nil
// if no persistent arena is configured (e.g. standalone Executor tests).
func (e *Executor) ensureArena() *DT.RowArena {
	if e.rowArena == nil {
		return nil
	}
	if *e.rowArena == nil {
		*e.rowArena = &DT.RowArena{}
	}
	return *e.rowArena
}

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
func NewExecutorWithEngine(store DT.Store) *Executor {
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
	e.initTextPlanCache(1000)
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
	// REQ001223: use globalStmtCache across all Executors.
	e.stmtCache = globalStmtCache
}

// getCachedStmt looks up a cached parsed statement. Returns nil if not found.
// REQ001693: O(1) — one map lookup + MoveToFront (pointer swaps, no alloc).
func (e *Executor) getCachedStmt(sql string) PS.Stmt {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	elem, ok := e.stmtCache.entries[sql]
	if !ok {
		return nil
	}
	e.stmtCache.lru.MoveToFront(elem)
	return elem.Value.(*stmtCacheEntry).stmt
}

// putCachedStmt stores a parsed statement in the cache.
// REQ001693: O(1) insert + evictBack; replaces the previous O(N) map
// scan that found the min-lastAccess entry on every over-capacity put.
func (e *Executor) putCachedStmt(sql string, stmt PS.Stmt) {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	if elem, ok := e.stmtCache.entries[sql]; ok {
		elem.Value.(*stmtCacheEntry).stmt = stmt
		e.stmtCache.lru.MoveToFront(elem)
		return
	}
	ent := &stmtCacheEntry{key: sql, stmt: stmt}
	elem := e.stmtCache.lru.PushFront(ent)
	e.stmtCache.entries[sql] = elem
	for e.stmtCache.lru.Len() > e.stmtCache.maxSize {
		e.stmtCache.evictBack()
	}
}

// evictBack removes the least-recently-used entry. Caller must hold mu.
func (c *stmtCache) evictBack() {
	elem := c.lru.Back()
	if elem == nil {
		return
	}
	ent := elem.Value.(*stmtCacheEntry)
	c.lru.Remove(elem)
	delete(c.entries, ent.key)
}

// clearStmtCache clears the statement cache. Used in tests.
func (e *Executor) clearStmtCache() {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	// REQ001673: clear() preserves map capacity, avoiding the
	// 17.45MB per-Reset alloc from make(map, 1024).
	clear(e.stmtCache.entries)
	// REQ001693: re-init the LRU list in place (no realloc).
	e.stmtCache.lru.Init()
}

// initPlanCache initializes the plan cache. Must be called before use.
// REQ001011.
func (e *Executor) initPlanCache(maxSize int) {
	if maxSize <= 0 {
		maxSize = 128
	}
	e.planCache = &planCache{
		entries: make(map[string]*planCacheEntry, maxSize),
		lru:     make([]*planCacheEntry, 0, maxSize),
		maxSize: maxSize,
	}
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
	// REQ001587: create a copy of the plan result so that later
	// mutations (tryVectorizePlan replacing plan.Root in-place) do
	// not corrupt the cached entry. Clone the AdaptiveOp wrapper
	// so it stays pristine.
	cachedResult := *result
	if aop, ok := result.Root.(*AD.AdaptiveOp); ok {
		cachedResult.Root = AD.NewAdaptiveOp(aop.Child(), key)
	}
	ent := &planCacheEntry{result: &cachedResult}
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

// PlanCache returns the plan cache (panic-safe if not initialized).
func (e *Executor) PlanCache() *planCache {
	if e.planCache == nil {
		e.planCache = &planCache{entries: make(map[string]*planCacheEntry)}
	}
	return e.planCache
}
func (e *Executor) ClearPlanCache() {
	e.planCache.mu.Lock()
	defer e.planCache.mu.Unlock()
	e.planCache.entries = nil
	e.planCache.lru = nil
	e.clearTextPlanCache()
	// REQ001497 follow-up: also clear the statement cache and the
	// planner's memo so cached plans from the previous SLT file don't
	// silently reused operator state (e.g. stale Iterators) against
	// fresh tables.
	e.clearStmtCache()
	if e.planner != nil {
		e.planner.mu.Lock()
		e.planner.clearMemoLocked()
		e.planner.mu.Unlock()
	}
}

// initTextPlanCache initialises the text-based plan cache. REQ001464.
func (e *Executor) initTextPlanCache(maxSize int) {
	if maxSize <= 0 {
		maxSize = 1000
	}
	e.textPlanCache = &textPlanCache{
		entries: make(map[string]*textPlanEntry, maxSize),
		maxSize: maxSize,
	}
}

// getTextPlan looks up a cached PlanResult by exact SQL text. REQ001464.
func (e *Executor) getTextPlan(sql string) *pl.PlanResult {
	if e.textPlanCache == nil {
		return nil
	}
	e.textPlanCache.mu.Lock()
	defer e.textPlanCache.mu.Unlock()
	ent, ok := e.textPlanCache.entries[sql]
	if !ok {
		return nil
	}
	for i, entry := range e.textPlanCache.lru {
		if entry == ent {
			e.textPlanCache.lru = append(e.textPlanCache.lru[:i], e.textPlanCache.lru[i+1:]...)
			break
		}
	}
	e.textPlanCache.lru = append([]*textPlanEntry{ent}, e.textPlanCache.lru...)
	return ent.plan
}

// putTextPlan stores a PlanResult keyed by exact SQL text. REQ001464.
func (e *Executor) putTextPlan(sql string, plan *pl.PlanResult) {
	if e.textPlanCache == nil {
		return
	}
	e.textPlanCache.mu.Lock()
	defer e.textPlanCache.mu.Unlock()
	if _, ok := e.textPlanCache.entries[sql]; ok {
		return
	}
	ent := &textPlanEntry{sql: sql, plan: plan}
	e.textPlanCache.entries[sql] = ent
	e.textPlanCache.lru = append([]*textPlanEntry{ent}, e.textPlanCache.lru...)
	for len(e.textPlanCache.lru) > e.textPlanCache.maxSize {
		oldest := e.textPlanCache.lru[len(e.textPlanCache.lru)-1]
		e.textPlanCache.lru = e.textPlanCache.lru[:len(e.textPlanCache.lru)-1]
		delete(e.textPlanCache.entries, oldest.sql)
	}
}

// planWithCache returns a compiled plan for stmt, checking the plan
// cache first. On cache miss, plans via Planner.Plan and caches the
// result. REQ001011.
// REQ001195: parameterized cache key so queries with the same
// structure but different literal values share a single cache entry.
// On cache hit, comparison-literals in the plan tree are replaced
// via replaceLiteralsOnTree using the current query's extracted values.
func (e *Executor) planWithCache(stmt PS.Stmt) (*pl.PlanResult, error) {
	if e.planCache.entries != nil {
		paramStmt, params := pl.NormalizeForMemo(stmt)
		if paramStmt == nil {
			paramStmt = stmt
		}
		key := pl.SerializeKey(paramStmt)
		if cached := e.getCachedPlan(key); cached != nil {
			// REQ001585: the cached PlanResult shares the same AdaptiveOp
			// wrapper instance across all callers. After the first execution,
			// the AdaptiveOp is in a "compiled" state (direct=true) and its
			// Inner operator tree is drained. Create a fresh PlanResult with
			// a new AdaptiveOp wrapping the same inner tree so the operator
			// is reusable. Without this, the second call to QueryAll for
			// the same SQL returns 0 rows.
			fresh := &pl.PlanResult{
				Root:    AD.NewAdaptiveOp(cached.Root.(*AD.AdaptiveOp).Child(), key),
				Cost:    cached.Cost,
				MemoKey: cached.MemoKey,
			}
			replaceLiteralsOnTree(fresh.Root, params)
			return fresh, nil
		}
		plan, err := e.planner.Plan(stmt)
		if err != nil {
			return nil, err
		}
		if plan == nil || plan.Root == nil {
			return nil, errors.New("ex: plan produced no root")
		}
		ResolvePlanSlots(plan.Root)
		// REQ001420: skip executor cache for ConstRow (COUNT(*) fast path)
		// since it's trivially cheap to create and caching shares the
		// same operator tree across calls, causing races on mutable state.
		if plan.Root != nil && !isConstRowPlan(plan.Root) {
			e.putCachedPlan(key, plan)
		}
		return plan, nil
	}
	plan, err := e.planner.Plan(stmt)
	if err != nil {
		return nil, err
	}
	if plan != nil && plan.Root != nil {
		ResolvePlanSlots(plan.Root)
	}
	return plan, nil
}

// ExtractParamTypes parses sql and returns the SQL column type
// of each `?` placeholder in left-to-right order. Entries are
// ls.CTInt / ls.CTVarchar / etc. when the placeholder can be
// resolved to a known column; -1 otherwise. This is the ST
// layer's source of truth for per-placeholder Go-type
// validation (R16-3, R16-4).
func (e *Executor) ExtractParamTypes(sql string) []int {
	parser := PS.NewParser(sql)
	defer parser.Close()
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
	cols := make([]DT.ColInfo, len(schema))
	for i, n := range schema {
		cols[i] = DT.ColInfo{Name: n, Typ: 1}
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
	cols := make([]DT.ColInfo, len(schema))
	for i, n := range schema {
		cols[i] = DT.ColInfo{Name: n, Typ: 1}
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

func (e *Executor) RegisterCollation(name string, fn DT.CollateFunc) error {
	return e.planner.RegisterCollation(name, fn)
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
				propagateParams(op, args, &e.paramBuf)
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
			propagateParams(op, args, &e.paramBuf)
			propagatePlanner(op, e.planner)
			execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: 0, TotalChanges: e.totalChanges}
			execCtx.RowArena = e.ensureArena()
			propagateExecContext(op, execCtx)
			defer op.Close()
			if _, err := op.Next(ctx); err != nil && err != DT.ErrNoRows {
				return Result{}, err
			}
			e.lastChanges = execCtx.LastChanges
			e.totalChanges = execCtx.TotalChanges
			res, err := extractResult(op)
			if err == nil {
				updateTableRowCount(op, e.planner)
			}
			if isDDLStmt(stmt) {
				e.clearTextPlanCache()
			}
			return res, err
		}
	}

	parser := PS.NewParser(sql)
	defer parser.Close()
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
		propagateParams(op, args, &e.paramBuf)
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
	propagateParams(op, args, &e.paramBuf)
	propagatePlanner(op, e.planner)
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: 0, TotalChanges: e.totalChanges}
	execCtx.RowArena = e.ensureArena()
	propagateExecContext(op, execCtx)

	if _, err := op.Next(ctx); err != nil && err != DT.ErrNoRows {
		return Result{}, err
	}
	e.lastChanges = execCtx.LastChanges
	e.totalChanges = execCtx.TotalChanges
	res, err := extractResult(op)
	if err == nil {
		updateTableRowCount(op, e.planner)
	}
	if isDDLStmt(stmt) {
		e.clearTextPlanCache()
	}
	return res, err
}
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
	// REQ001480: textPlanCache fast-path — bypass parse/plan/NormalizeForMemo
	// on cache hit. Mirrors QueryAll's fast-path at ex.go:1162-1172.
	if e.textPlanCache != nil {
		if plan := e.getTextPlan(sql); plan != nil {
			propagateParams(plan.Root, args, &e.paramBuf)
			propagatePlanner(plan.Root, e.planner)
			execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
			execCtx.RowArena = e.ensureArena()
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
			return &Rows{Cols: append([]string(nil), row.Cols...), Types: append([]LX.TokenType(nil), row.Types...)}, nil
		}
	}
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
				propagateParams(op, args, &e.paramBuf)
				defer op.Close()
				var out []DT.Row
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
			propagateParams(plan.Root, args, &e.paramBuf)
			propagatePlanner(plan.Root, e.planner)
			execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
			execCtx.RowArena = e.ensureArena()
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
	defer parser.Close()
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
		propagateParams(op, args, &e.paramBuf)
		defer op.Close()
		var out []DT.Row
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
	propagateParams(plan.Root, args, &e.paramBuf)
	propagatePlanner(plan.Root, e.planner)
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	execCtx.RowArena = e.ensureArena()
	propagateExecContext(plan.Root, execCtx)
	// Attempt vectorized execution for eligible query plans.
	plan.Root = tryVectorizePlan(plan.Root, e.planner)
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

func (e *Executor) QueryAll(ctx context.Context, sql string, args ...any) ([]DT.Row, error) {
	// REQ001712: textPlanCache disabled for QueryAll — the cached plan's
	// operator tree state (e.g., Aggregate.buf, ValuesOp.evaluated) is
	// modified by the first execution and not fully reset by Close().
	// The stmtCache (parsed AST) still provides the main perf benefit
	// (12.70% CPU on parsing). The textPlanCache remains active for
	// the single-row Query path (used by QueryStreamCompiled).
	_ = e.getTextPlan
	_ = e.putTextPlan

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
			// REQ001712: textPlanCache disabled for QueryAll.
			if plan.Root != nil && !isConstRowPlan(plan.Root) {
				e.putTextPlan(sql, plan)
			}
			propagateParams(plan.Root, args, &e.paramBuf)
			propagatePlanner(plan.Root, e.planner)
			execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
			execCtx.RowArena = e.ensureArena()
			propagateExecContext(plan.Root, execCtx)
			defer plan.Root.Close()
			return e.drainPlanExecCtx(ctx, plan, execCtx)
		}
	}

	parser := PS.NewParser(sql)
	defer parser.Close()
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
	// REQ001464: cache the plan by exact SQL text.
	// REQ001712: textPlanCache disabled for QueryAll.
	if plan.Root != nil && !isConstRowPlan(plan.Root) {
		e.putTextPlan(sql, plan)
	}
	propagateParams(plan.Root, args, &e.paramBuf)
	// REQ000366: thread the main-plan planner so SeqScan rows
	// carry it into subquery evals. propagatePlanner is a
	// depth-first walk that calls WithPlanner on every node
	// that supports it.
	propagatePlanner(plan.Root, e.planner)
	// REQ000586: thread DT.ExecContext through rows to eliminate
	// the global currentSubqueryPlanner.
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	execCtx.RowArena = e.ensureArena()
	propagateExecContext(plan.Root, execCtx)
	defer plan.Root.Close()
	return e.drainPlanExecCtx(ctx, plan, execCtx)
}

// clearTextPlanCache drops all entries from the text cache. REQ001464.
func (e *Executor) clearTextPlanCache() {
	if e.textPlanCache == nil {
		return
	}
	e.textPlanCache.mu.Lock()
	defer e.textPlanCache.mu.Unlock()
	// REQ001673: clear() preserves map capacity, avoiding the
	// 13.34MB per-Reset alloc from make(map, maxSize).
	clear(e.textPlanCache.entries)
	e.textPlanCache.lru = nil
}

// isDDLStmt reports whether stmt modifies the schema (CREATE/DROP/ALTER). REQ001464.
func isDDLStmt(stmt PS.Stmt) bool {
	switch stmt.(type) {
	case *PS.CreateTable, *PS.DropTable,
		*PS.CreateIndexStmt, *PS.DropIndexStmt,
		*PS.CreateViewStmt, *PS.DropViewStmt,
		*PS.CreateMatViewStmt, *PS.DropMatViewStmt,
		*PS.RefreshMatViewStmt,
		*PS.CreateVirtualTableStmt,
		*PS.AlterTableStmt,
		*PS.DropTriggerStmt,
		*PS.ReindexStmt:
		return true
	}
	return false
}

// CompiledPlan holds a pre-compiled operator tree that skips re-parsing
// and re-planning on each execution. Created by Executor.CompilePlan.
// For SELECT/compound, plan is set with the compiled PlanResult.
// For DML and other exec-only statements, op is set with the writer op.
// REQ001422.
type CompiledPlan struct {
	stmt  PS.Stmt
	isDML bool
	plan  *pl.PlanResult
	op    DT.Operator
}

// Close releases the operator tree in the CompiledPlan. Safe to call
// after ExecCompiled/QueryStreamCompiled have closed the tree. REQ001422.
func (cp *CompiledPlan) Close() {
	if cp == nil {
		return
	}
	if cp.isDML {
		if cp.op != nil {
			cp.op.Close()
		}
	} else {
		if cp.plan != nil && cp.plan.Root != nil {
			cp.plan.Root.Close()
		}
	}
}

// CompilePlan parses sql and returns a CompiledPlan that holds a
// compiled operator tree ready for execution via ExecCompiled or
// QueryStreamCompiled. Subsequent calls skip re-parsing and re-planning.
// The caller must call CloseCompiled when the plan is no longer needed.
// REQ001422.
func (e *Executor) CompilePlan(sql string) (*CompiledPlan, error) {
	parser := PS.NewParser(sql)
	defer parser.Close()
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}

	switch stmt.(type) {
	case *PS.Select, *PS.CompoundStmt:
		plan, err := e.planWithCache(stmt)
		if err != nil {
			return nil, err
		}
		if plan == nil || plan.Root == nil {
			return nil, errors.New("ex: CompilePlan: plan produced no root")
		}
		ResolvePlanSlots(plan.Root)
		// REQ001614: tryVectorizePlan always succeeds — falls back to
		// ScalarBatchProducer wrapping when vectorization is not applicable.
		plan.Root = tryVectorizePlan(plan.Root, e.planner)
		return &CompiledPlan{stmt: stmt, plan: plan}, nil

	default:
		// DML, DDL, PRAGMA, etc.
		op, err := e.buildWriterOp(stmt)
		if err != nil {
			return nil, err
		}
		return &CompiledPlan{stmt: stmt, isDML: true, op: op}, nil
	}
}

// ExecCompiled executes a CompiledPlan produced by CompilePlan,
// injecting args into `?` placeholders. Skips re-parsing and
// re-planning. REQ001422.
func (e *Executor) ExecCompiled(ctx context.Context, cp *CompiledPlan, args ...any) (Result, error) {
	if cp == nil {
		return Result{}, errors.New("ex: ExecCompiled: nil plan")
	}

	if cp.isDML {
		op := cp.op
		// R16-1: thread args down to the operator tree so `?`
		// placeholders resolve.
		propagateParams(op, args, &e.paramBuf)

		if hasReturning(cp.stmt) {
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

		propagatePlanner(op, e.planner)
		execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: 0, TotalChanges: e.totalChanges}
		execCtx.RowArena = e.ensureArena()
		propagateExecContext(op, execCtx)
		defer op.Close()
		if _, err := op.Next(ctx); err != nil && err != DT.ErrNoRows {
			return Result{}, err
		}
		e.lastChanges = execCtx.LastChanges
		e.totalChanges = execCtx.TotalChanges
		res, err := extractResult(op)
		if err == nil {
			updateTableRowCount(op, e.planner)
		}
		return res, err
	}

	// SELECT/compound path
	plan := cp.plan
	propagateParams(plan.Root, args, &e.paramBuf)
	propagatePlanner(plan.Root, e.planner)
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	execCtx.RowArena = e.ensureArena()
	propagateExecContext(plan.Root, execCtx)
	defer plan.Root.Close()
	// REQ001614: tryVectorizePlan always wraps the root in a
	// BatchToRowAdapter, which implements BatchProducer. Drain via
	// the batch path (NextBatch → ToRows) instead of per-row Next.
	if bp, ok := plan.Root.(UT.BatchProducer); ok {
		_, err := drainBatchProducer(ctx, bp, execCtx)
		return Result{}, err
	}
	if _, err := plan.Root.Next(ctx); err != nil && err != DT.ErrNoRows {
		return Result{}, err
	}
	e.lastChanges = execCtx.LastChanges
	e.totalChanges = execCtx.TotalChanges
	return Result{}, nil
}

// Precompile parses each SQL in sqls and populates the shared stmt cache.
// Subsequent QueryAll calls skip the parse step. Plan caching is handled
// by planWithCache on the first execution of each SQL. REQ001458.
func (e *Executor) Precompile(ctx context.Context, sqls []string) {
	if e.stmtCache == nil || e.stmtCache.entries == nil {
		return
	}
	for _, sql := range sqls {
		if sql == "" {
			continue
		}
		if e.getCachedStmt(sql) != nil {
			continue // already cached
		}
		parser := PS.NewParser(sql)
		stmt, err := parser.Parse()
		parser.Close()
		if err != nil {
			continue
		}
		e.putCachedStmt(sql, stmt)
	}
}

// propagatePlanner walks the operator tree rooted at root and
// calls WithPlanner(p) on every node that supports it. See
// REQ000366.
func propagatePlanner(root DT.Operator, p *Planner) {
	if root == nil {
		return
	}
	if w, ok := root.(interface {
		WithPlanner(pl.QueryPlanner) pl.Operator
	}); ok {
		w.WithPlanner(p)
	}
	type childer interface {
		Child() DT.Operator
	}
	if c, ok := root.(childer); ok {
		propagatePlanner(c.Child(), p)
	}
	type leftRighter interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}
	if lr, ok := root.(leftRighter); ok {
		propagatePlanner(lr.LeftChild(), p)
		propagatePlanner(lr.RightChild(), p)
	}
}

// propagateExecContext walks the operator tree and sets execCtx
// on operators that evaluate expressions (Filter, Project, etc.)
// so that subquery eval can find the planner via DT.ExecContextFromRow.
// Also propagates execCtx to Insert/Update/Delete for change
// tracking (REQ000812).
func propagateExecContext(root DT.Operator, ec *DT.ExecContext) {
	if root == nil || ec == nil {
		return
	}
	// REQ001233: create RowArena once per query, shared across all
	// operators in the tree. REQ001260: Init is deferred to first
	// AllocRow call where nCols is known.
	// REQ001421: pre-size to engineBatchSize rows × 8 columns to
	// eliminate per-query geometric grow cycles (6+ grows per query
	// without Init for 50-row UPDATE/Scan workloads).
	// REQ001419: if the ExecContext already carries a persistent
	// RowArena (from the Engine), reuse it — just reset the offset
	// instead of allocating a fresh slab and Init.
	if ec.RowArena == nil {
		ec.RowArena = &DT.RowArena{}
		if arena, ok := ec.RowArena.(*DT.RowArena); ok {
			arena.Init(OP.EngineBatchSize(), 8)
		}
	} else if arena, ok := ec.RowArena.(*DT.RowArena); ok && arena != nil {
		arena.ResetOffset()
	}
	if f, ok := root.(*OP.Filter); ok {
		f.SetExecCtx(ec)
	}
	if fp, ok := root.(*OP.FilterProject); ok {
		fp.SetExecCtx(ec)
	}
	if p, ok := root.(*OP.Project); ok {
		p.SetExecCtx(ec)
	}
	if ins, ok := root.(*WT.Insert); ok {
		ins.SetExecCtx(ec)
	}
	if upd, ok := root.(*WT.Update); ok {
		upd.SetExecCtx(ec)
	}
	if del, ok := root.(*WT.Delete); ok {
		del.SetExecCtx(ec)
	}
	if val, ok := root.(*OP.Values); ok {
		val.SetExecCtx(ec)
	}
	type childer interface {
		Child() DT.Operator
	}
	if c, ok := root.(childer); ok {
		propagateExecContext(c.Child(), ec)
	}
	type leftRighter interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}
	if lr, ok := root.(leftRighter); ok {
		propagateExecContext(lr.LeftChild(), ec)
		propagateExecContext(lr.RightChild(), ec)
	}
}

// resetRowArena resets the RowArena in the ExecContext. Called
// via defer at the end of each query execution. REQ001233.
func resetRowArena(ec *DT.ExecContext) {
	if ec == nil {
		return
	}
	if arena, ok := ec.RowArena.(*DT.RowArena); ok {
		arena.Reset()
	}
}

// replaceLiteralsOnTree walks the operator tree and calls
// ReplaceLiterals on every node with comparison-literals (Filter).
// Params are consumed in DFS tree-walk order, matching the
// extraction order of NormalizeForMemo. REQ001195.
func replaceLiteralsOnTree(root DT.Operator, vals []any) {
	type filterNode interface {
		CountComparisonLiterals() int
		ReplaceLiterals([]any)
	}
	var walk func(DT.Operator, *[]any)
	walk = func(op DT.Operator, remaining *[]any) {
		if op == nil || len(*remaining) == 0 {
			return
		}
		if fn, ok := op.(filterNode); ok {
			n := fn.CountComparisonLiterals()
			if n > 0 && n <= len(*remaining) {
				fn.ReplaceLiterals((*remaining)[:n])
				*remaining = (*remaining)[n:]
			}
		}
		type childer interface{ Child() DT.Operator }
		if c, ok := op.(childer); ok {
			walk(c.Child(), remaining)
		}
		type leftRighter interface {
			LeftChild() DT.Operator
			RightChild() DT.Operator
		}
		if lr, ok := op.(leftRighter); ok {
			walk(lr.LeftChild(), remaining)
			walk(lr.RightChild(), remaining)
		}
	}
	walk(root, &vals)
}

// propagateParams walks the operator tree rooted at root and
// calls WithParams(args) on every node that supports it
// (R16-1..2). The walk is depth-first, children-first so the
// args reach every leaf operator. Operators without a
// WithParams method are skipped silently.
// REQ001579: use buf to avoid per-query make([]any, N).
func propagateParams(root DT.Operator, args []any, buf *[]any) {
	if root == nil {
		return
	}
	if args == nil {
		return
	}
	p := asAnySlice(args, buf)
	if w, ok := root.(interface{ WithParams([]any) DT.Operator }); ok {
		w.WithParams(p)
	}
	// Walk children via the Child() convention used elsewhere
	// in this package (explain.go).
	type childer interface {
		Child() DT.Operator
	}
	if c, ok := root.(childer); ok {
		propagateParams(c.Child(), args, buf)
	}
	// Some operators expose children via a `child` field; we
	// rely on the explain.go walk for those via Child(). Operators
	// with multiple children (HashAggregate, Join) define
	// their own WithParams and walk internally.
}

// asAnySlice converts []any to []any for type-stability
// across the WithParams interface boundary. Avoids an allocation
// when the slice is already nil. REQ001579: uses buf to avoid
// per-query make([]any, N).
func asAnySlice(args []any, buf *[]any) []any {
	if args == nil {
		return nil
	}
	if cap(*buf) < len(args) {
		*buf = make([]any, len(args))
	}
	*buf = (*buf)[:len(args)]
	for i, a := range args {
		(*buf)[i] = a
	}
	return *buf
}

// Explain plans the statement and returns a human-readable
// description of the operator tree. The plan is closed before
// returning, so Explain does not run the query.
// REQ001480: textPlanCache fast-path — bypass parse/plan/NormalizeForMemo
// on cache hit. On hit, plan.Root is borrowed from the cache and must
// not be Closed (other call paths still need it).
func (e *Executor) Explain(sql string) (string, error) {
	if e.textPlanCache != nil {
		if plan := e.getTextPlan(sql); plan != nil && plan.Root != nil {
			nodes := buildPlanNodeTree(plan.Root, e.planner)
			return formatPlanNodes(nodes), nil
		}
	}
	parser := PS.NewParser(sql)
	defer parser.Close()
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
	// REQ001480: populate textPlanCache so subsequent Explain / Query /
	// QueryAll calls with the same SQL bypass re-planning.
	if e.textPlanCache != nil {
		e.putTextPlan(sql, plan)
	}
	defer plan.Root.Close()
	nodes := buildPlanNodeTree(plan.Root, e.planner)
	return formatPlanNodes(nodes), nil
}

// formatPlanNodes renders a PlanNode tree to a human-readable string.
// Shared by the cached and uncached paths in Explain. REQ001480.
func formatPlanNodes(nodes *AD.PlanNode) string {
	var b strings.Builder
	var walk func(node *AD.PlanNode, depth int)
	walk = func(node *AD.PlanNode, depth int) {
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
	return b.String()
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

func (e *Executor) buildWriterOp(stmt PS.Stmt) (DT.Operator, error) {
	switch s := stmt.(type) {
	case *PS.Insert:
		// REQ001191: DML on views is not allowed.
		if DT.LookupView(s.Table) != nil {
			return nil, fmt.Errorf("ex: cannot modify view %s", s.Table)
		}
		// REQ000707: INSERT INTO t SELECT ...
		if s.Select != nil {
			selPlan, err := e.planner.Plan(s.Select)
			if err != nil {
				return nil, err
			}
			if selPlan == nil || selPlan.Root == nil {
				return nil, fmt.Errorf("ex: INSERT SELECT: plan produced no root")
			}
			// REQ001129: store-backed INSERT...SELECT needs a
			// store-backed Insert operator so the rows are written
			// to the engine, not just the in-memory table map.
			var op *WT.Insert
			if e.store != nil {
				op, err = WT.NewInsertWithStore(e.store, s.Table, s.Cols, nil, s.Returning, s.OnConflict)
				if err != nil {
					return nil, err
				}
			} else {
				op = WT.NewInsert(s.Table, s.Cols, nil, s.Returning, s.OnConflict)
			}
			op.SetSelectPlan(selPlan.Root)
			op.SetConflictAction(s.ConflictAction)
			propagatePlanner(selPlan.Root, e.planner)
			return op, nil
		}

		op, iErr := func() (*WT.Insert, error) {
			if e.store != nil {
				return WT.NewInsertWithStore(e.store, s.Table, s.Cols, s.Values, s.Returning, s.OnConflict)
			}
			return WT.NewInsert(s.Table, s.Cols, s.Values, s.Returning, s.OnConflict), nil
		}()
		if iErr != nil {
			return nil, iErr
		}
		op.SetConflictAction(s.ConflictAction)
		op.SetDefaultValues(s.DefaultValues)
		return op, nil
	case *PS.Update:
		targetTable := s.Table
		if viewSel := DT.LookupView(targetTable); viewSel != nil {
			// REQ001191: DML on views is not allowed (views are
			// read-only in SQLite unless they have INSTEAD OF triggers,
			// which we don't support yet).
			return nil, fmt.Errorf("ex: cannot modify view %s", targetTable)
		}
		var scan DT.Operator = OP.NewSeqScan(targetTable)
		if e.store != nil {
			ssc, err := OP.NewSeqScanWithStore(e.store, targetTable)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := OP.NewFilter(scan, s.Where, nil)
		// REQ000558: apply ORDER BY / LIMIT / OFFSET to the row
		// selection before updating.
		var current DT.Operator = filter
		if len(s.OrderBy) > 0 {
			current = OP.NewSort(current, s.OrderBy)
		}
		if s.OffsetFirst {
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = OP.NewLimit(current, n)
				}
			}
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = OP.NewOffset(current, n)
				}
			}
		} else {
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = OP.NewOffset(current, n)
				}
			}
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = OP.NewLimit(current, n)
				}
			}
		}
		if e.store != nil {
			op, err := WT.NewUpdateWithStore(e.store, targetTable, s.Set, s.Where, current, s.Returning)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return WT.NewUpdate(targetTable, s.Set, s.Where, current, s.Returning), nil
	case *PS.Delete:
		tableName := s.Table
		if viewSel := DT.LookupView(tableName); viewSel != nil {
			// REQ001191: DML on views is not allowed (views are
			// read-only in SQLite unless they have INSTEAD OF triggers).
			return nil, fmt.Errorf("ex: cannot modify view %s", tableName)
		}
		var scan DT.Operator = OP.NewSeqScan(tableName)
		if e.store != nil {
			ssc, err := OP.NewSeqScanWithStore(e.store, tableName)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := OP.NewFilter(scan, s.Where, nil)
		// REQ000475: apply ORDER BY / LIMIT / OFFSET to the row
		// selection before deleting.
		var current DT.Operator = filter
		if len(s.OrderBy) > 0 {
			current = OP.NewSort(current, s.OrderBy)
		}
		if s.OffsetFirst {
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = OP.NewLimit(current, n)
				}
			}
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = OP.NewOffset(current, n)
				}
			}
		} else {
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = OP.NewOffset(current, n)
				}
			}
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = OP.NewLimit(current, n)
				}
			}
		}
		if e.store != nil {
			op, err := WT.NewDeleteWithStore(e.store, tableName, s.Where, current, s.Returning)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return WT.NewDelete(tableName, s.Where, current, s.Returning), nil
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
		return WT.NewCreateTable(s), nil
	case *PS.DropTable:
		return WT.NewDropTable(s), nil
	case *PS.CreateIndexStmt:
		// Note: the planner's cost-based selection (REQ000156)
		// is keyed off ex.RegisterIndex, not CREATE INDEX.
		// CREATE INDEX only registers the index for writer
		// maintenance; it does not backfill existing rows into
		// the index keyspace. Tests that want cost-based
		// selection should call ex.RegisterIndex explicitly.
		return WT.NewCreateIndex(s), nil
	case *PS.DropIndexStmt:
		return WT.NewDropIndex(s), nil
	case *PS.CreateViewStmt:
		return WT.NewCreateView(s), nil
	case *PS.CreateMatViewStmt:
		return WT.NewCreateMatView(s.Name, s.As, e.store), nil
	case *PS.DropMatViewStmt:
		return WT.NewDropMatView(s.Name, e.store), nil
	case *PS.RefreshMatViewStmt:
		// Lookup the matview definition from registry
		sel := DT.LookupMatView(s.Name)
		if sel == nil {
			return nil, fmt.Errorf("ex: materialized view %q not found", s.Name)
		}
		return WT.NewRefreshMatView(s.Name, sel, e.store, e.planner), nil
	case *PS.VacuumStmt:
		return UT.NewVacuum(s), nil
	case *PS.AnalyzeStmt:
		if e.store != nil {
			op, err := UT.NewAnalyzeWithStore(e.store, s)
			if err == nil {
				return op, nil
			}
		}
		return UT.NewAnalyze(s), nil
	case *PS.AlterTableStmt:
		return WT.NewAlterTable(s), nil
	case *PS.TriggerStmt:
		return WT.NewTrigger(s), nil
	case *PS.DropViewStmt:
		return WT.NewDropView(s), nil
	case *PS.DropTriggerStmt:
		return WT.NewDropTrigger(s), nil
	case *PS.PragmaStmt:
		return WT.NewPragma(s), nil
	case *PS.ExplainStmt:
		return WT.NewExplain(s), nil
	case *PS.TruncateStmt:
		return WT.NewTruncate(s), nil
	case *PS.ReindexStmt:
		return WT.NewReindex(s), nil
	case *PS.CreateVirtualTableStmt:
		return WT.NewUnsupportedOp(s, "ex: virtual table module not supported in v1: "+s.Module), nil
	case *PS.BeginTX:
		return AD.NewNoop(), nil
	case *PS.CommitTX:
		return AD.NewNoop(), nil
	case *PS.ValuesStmt:
		return OP.NewValuesRowsOp(s.Rows), nil
	case *PS.AttachStmt:
		path, err := extractAttachPath(s.Expr)
		if err != nil {
			return nil, err
		}
		return WT.NewAttachOp(e, s.Name, path), nil
	case *PS.DetachStmt:
		return WT.NewDetachOp(e, s.Name), nil
	}
	return nil, errors.New("ex: not a writable statement")
}

func extractResult(op DT.Operator) (Result, error) {
	type affected interface {
		RowsAffected() int64
	}
	if a, ok := op.(affected); ok {
		return Result{RowsAffected: a.RowsAffected()}, nil
	}
	return Result{}, nil
}

// updateTableRowCount adjusts the planner's cached row count after DML
// execution (REQ001420). For INSERT the count increases; for DELETE it
// decreases; for UPDATE the count is unchanged.
func updateTableRowCount(op DT.Operator, planner pl.QueryPlanner) {
	if planner == nil || op == nil {
		return
	}
	type tableNamed interface {
		Table() string
	}
	type rowCounter interface {
		RowsAffected() int64
	}
	tn, hasTable := op.(tableNamed)
	rc, hasRows := op.(rowCounter)
	if !hasTable || !hasRows {
		return
	}
	delta := rc.RowsAffected()
	if delta <= 0 {
		return
	}
	switch op.(type) {
	case *WT.Insert:
		planner.UpdateTableRowCount(tn.Table(), delta)
	case *WT.Delete:
		planner.UpdateTableRowCount(tn.Table(), -delta)
		// UPDATE: row count does not change.
	}
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

// ResetGlobalStmtCache clears the global shared statement cache.
// Used in tests to prevent cross-test contamination. REQ001223.
func ResetGlobalStmtCache() {
	globalStmtCache.mu.Lock()
	defer globalStmtCache.mu.Unlock()
	clear(globalStmtCache.entries)
	globalStmtCache.lru.Init()
}
