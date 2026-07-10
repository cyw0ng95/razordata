package EX

import (
	"runtime"

	"github.com/cyw0ng95/razordata/internal/SQB/AD"
	"github.com/cyw0ng95/razordata/internal/SQO/CO"
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// TxWriter is the optional hook an Executor notifies on every key
// write. Aliased from PL.
func (e *Executor) SetTxWriter(w DT.TxWriter) {
	e.txWriter = w
	DT.SetCurrentTxWriter(w)
}

// SetStatsCatalog wires a stats catalog into the executor's planner.
// REQ001318: load persisted stats at open.
func (e *Executor) SetStatsCatalog(cat DT.StatsCatalog) {
	e.planner.SetStatsCatalog(cat)
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
	return e
}

// RegisterOptimizer injects an external Optimizer. When set, Plan() and
// planWithCache delegate to the optimizer instead of the internal Planner.
func (e *Executor) RegisterOptimizer(opt CO.Optimizer) {
	e.optimizer = opt
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
	globalStmtCache.entries = make(map[string]*stmtCacheEntry, 1024)
	globalStmtCache.accessCounter = 0
}