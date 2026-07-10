package EX

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/SQO/CO"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
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

// stmtCacheEntry holds a cached parsed statement with LRU metadata.
// REQ001286: lastAccess tracks recency via a monotonic counter.
type stmtCacheEntry struct {
	stmt       PS.Stmt
	lastAccess uint64
}

// planCacheEntry holds a cached compiled plan with LRU metadata.
// REQ001011.
type planCacheEntry struct {
	result *pl.PlanResult
}

// globalStmtCache is the shared statement cache across all Executors.
// REQ001223: eliminates per-Executor stmtCache allocation (171 MB per query).
// Initialized lazily on first NewExecutor call.
var globalStmtCache = &stmtCache{
	entries: make(map[string]*stmtCacheEntry, 1024),
	maxSize: 1024,
}

// stmtCache is a thread-safe LRU cache for parsed statements.
// REQ001220: shared across ShallowCopy clones via pointer.
// REQ001286: monotonic access counter for allocation-free LRU.
type stmtCache struct {
	mu            sync.Mutex
	entries       map[string]*stmtCacheEntry
	accessCounter uint64
	maxSize       int
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
	planner      *Planner
	optimizer    CO.Optimizer
	store        DT.Store
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

	closed atomic.Bool
}