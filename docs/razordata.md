# Razordata: Beyond SQLite — An Analytical Deep Dive

> An embedded database built from scratch in pure Go, designed for the multicore era.

## 1. Motivation

SQLite is the most widely deployed database engine in the world. Its C codebase is battle-tested, but it carries architectural decisions from the early 1990s that constrain modern workloads:

- **Single-writer serialization.** SQLite's WAL mode allows concurrent reads, but writers are serialized at the WAL file level. Under write-heavy loads, this becomes a throughput bottleneck.
- **B-tree page contention.** The B-tree stores data in fixed-size pages (typically 4096 bytes). Concurrent writers to nearby keys must acquire the same page-level locks, creating contention even when keys are logically independent.
- **No MVCC.** SQLite uses page-level locking, not multi-version concurrency control. Readers and writers can block each other in default journal mode.
- **C codebase.** Manual memory management, no garbage collector, platform-specific build dependencies. Cross-compilation requires careful toolchain management.

Razordata is a ground-up reimplementation of an embedded database that keeps SQLite's ergonomic model — one directory, zero configuration, no server — while adopting storage and concurrency algorithms designed for modern hardware.

## 2. Design Constraints

Before examining the internals, it is worth understanding what Razordata explicitly chose *not* to do:

| Constraint | Rationale |
|---|---|
| No external C dependencies | Cross-compilation is trivial; no `CGO` portability issues; memory safety via Go's GC |
| No network server | Embedded-first. The database lives in the same process as the application. Network servers are a separate concern. |
| Go 1.22+ | Access to `slices`, `maps`, `iter`, modern `slog`. No need for backward compatibility with ancient Go. |
| Single `go.mod` | No nested modules. Dependency graph is flat and auditable. |
| Page size: 4 KB (power of 2) | Matches OS page size for `O_DIRECT` alignment and `mmap` efficiency. |

The result is a database engine that can be embedded into any Go application with `go get`, runs identically on Linux, macOS, and Windows, and has zero build-time requirements beyond the Go toolchain.

## 3. Architecture: The Eight-Layer Stack

Razordata is organized as eight subsystems with strict dependency ordering. No layer may depend on a layer above it. This is not just a convention — it is enforced by the build graph:

```
LOG → FIL → MEM → WAL → ENG → TXN → SQL → SYS
```

Each layer exposes interfaces consumed by the layer above. The layers are:

### LOG — Structured Logging

The foundation layer. Every other subsystem depends on it.

- Wraps `log/slog` with an atomic level variable for lock-free level checks in the hot path.
- Async hook dispatch via a bounded channel (non-blocking send, drop on overflow). Hooks never backpressure the logging path.
- Three built-in hooks: `TraceHook` (SQL query tracing with wall-clock timing), `MetricHook` (throughput/latency counters), `ProfileHook` (CPU/heap dumps on error events).
- Log rotation on size threshold. Compressed with gzip after rotation.

The critical design choice: the bounded channel means the logging system can never block a database operation. If the hook dispatcher is slow, events are dropped. This is a deliberate trade-off — observability should never compromise correctness.

### FIL — File I/O

The lowest I/O layer. All disk access flows through here.

- **Block I/O via `pread`/`pwrite`**: Positional reads/writes without seeking. Each block is addressed by `(fd, blockID * BlockSize)`. This avoids shared file offset state and enables concurrent reads on the same FD.
- **`O_DIRECT` with fallback**: On Linux, data files are opened with `O_DIRECT` to bypass the OS page cache. If the kernel rejects it (`EINVAL`), Razordata falls back to buffered I/O. WAL segments always use buffered I/O — `fsync` handles durability.
- **CRC32 checksums**: Every 4 KB block carries a 4-byte IEEE CRC32 in its last 4 bytes. Reads verify the checksum; mismatches return `ErrCorrupt` without attempting recovery.
- **Meta page** (`meta.razor`, always block 0): Contains magic bytes `0x5241524F` ("RAZO"), semantic version, block size, catalog root pointer, and manifest checksum. Read on startup to validate the database directory.
- **Path validation**: Rejects any path containing `..` or symlinks. All paths are resolved against the database root before use — prevents directory traversal.

File layout under `<name>.razor/`:

```
<name>.razor/
├── meta.razor          # Meta page (block 0)
├── wal/
│   ├── wal.000         # WAL segment 0 (64 MB)
│   └── wal.001         # WAL segment 1
├── sst/
│   ├── L0/             # Level 0 SSTs (flushed memtables)
│   ├── L1/             # Level 1 SSTs
│   └── ...
├── manifest            # Current LSM version
└── hint                # Buffer pool warm-start hint file
```

### MEM — Memory Management

The buffer pool that caches SST blocks in memory. All reads from the LSM tree go through here.

**Buffer pool with clock-sweep eviction:**

```go
type bufferSlot struct {
    blockID   uint64
    data      []byte        // fixed-size: BlockSize bytes, borrowed from sync.Pool
    pinCount  atomic.Int32  // eviction blocked while > 0
    dirty     atomic.Bool
    refKey    atomic.Uint64 // clock hand reference
    loading   atomic.Bool   // prevents concurrent loads of same block
    wait      chan struct{}  // closed when loaded
}
```

The clock-sweep algorithm is a classical approximation of LRU:

1. A global `hand` counter (`atomic.Uint64`) advances on each eviction pass.
2. Each slot's `refKey` is atomically updated to the current hand value on access.
3. Eviction scans slots: if `refKey < hand - N` (N = clock interval) and `pinCount == 0`, the slot is evicted.
4. The hash table uses a sharded mutex (`sync.RWMutex`) — `RLock` for reads, `Lock` for eviction/insertion.

The `loading` flag on each slot prevents duplicate loads: only one goroutine loads a block from disk; others wait on the `wait` channel. This eliminates redundant I/O for concurrent reads of the same block.

**`sync.Pool` for zero-allocation hot paths:**

```go
type syncPool struct {
    pagePool  sync.Pool // make([]byte, BlockSize) — 4 KB buffers
    iterPool  sync.Pool // make([]byte, iterBufferSize) — LSM iterator scratch
}
```

`pagePool.New` allocates exactly `BlockSize` bytes. On `Put`, buffers that don't match the expected size are dropped (not returned to the pool) — this prevents pool poisoning from caller bugs.

**Hint file for warm startup:**

On clean shutdown, the buffer pool serializes its hot working set (all slots with `LastAccess > 0`) to `<name>.razor/hint`. On startup, the hint file is read (decompressed if `.gz`) and blocks are eagerly loaded into the buffer pool *before* serving any queries. This eliminates cold-start latency for frequently accessed data.

### WAL — Write-Ahead Log

The sole write path for durability. All mutations are serialized to the WAL before the storage engine writes data.

**Record encoding:**

```
┌──────────────┬──────────┬─────────────┬──────────────────┐
│ length:varint │ txnID:varint │ type:uint8 │ payload:blob   │
└──────────────┴──────────────┴─────────────┴──────────────────┘
```

Record types:

| Type | Code | Payload |
|---|---|---|
| `RTData` | 0 | `[blockID:8][checksum:4][data:varint]` |
| `RTCommit` | 1 | `[commitTS:8]` |
| `RTRollback` | 2 | (empty) |
| `RTCheckpoint` | 3 | `[checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4][activeTXNs:varint...]` |
| `RTMerge` | 4 | `[newVersion:8][deletedFiles:varint...][addedFiles:varint...]` |

**Segment rotation:** WAL segments are 64 MB files named `wal.000`, `wal.001`, ... (zero-padded to 3 digits for lexicographic sorting). When a segment fills, it is closed and a new one is created. A pre-allocated 256 KB `writeBuffer` avoids per-record allocation — records are appended via `binary.LittleEndian` directly into the buffer.

**LSN encoding:** The log sequence number is a single `atomic.Int64`. It encodes both segment number and offset: `lsn = segmentNumber * SegSize + offset`. Readers use the LSN to detect stale reads and to order replay.

**Batch commit:** Multiple transactions can be grouped into one `fsync` call via a `sync.WaitGroup` and a single write barrier. This amortizes the cost of `fsync` (typically 1–10 ms on SSDs) across concurrent transactions.

**Corruption recovery (shipped in iter-13):**

- Segment header: 12 bytes at the start of each WAL segment.
- Envelope CRC: 4-byte CRC32-IEEE over the record body.
- Bounded resync: on CRC mismatch, scan forward up to `MaxRecordLen` bytes looking for a valid record header.
- Tail-of-segment torn writes are tolerated (expected after a crash). Mid-segment corruption surfaces `ErrCorrupt`.
- `Replayer.Stats()` exposes: `TruncatedSegments`, `UnknownRecords`, `CorruptionFailures`.

**Recovery on startup:**

1. Find the last `RTCheckpoint` record across all segments.
2. Rewind to that LSN.
3. Replay subsequent records in LSN order.
4. `RTData` records rebuild the in-memory memtable state.
5. `RTCommit` records mark transactions as committed.
6. `RTRollback` records discard uncommitted write sets.
7. Truncate clean segments before the checkpoint.

### ENG — Storage Engine (LSM Tree)

The core of the database. Implements the LSM tree: a lock-free skiplist memtable that flushes to SST files on disk, leveled compaction, bloom filters, and an atomic file manifest.

**Lock-free skiplist memtable:**

```go
type skipList struct {
    head    atomic.Pointer[node]
    level   atomic.Int32
    maxLevel int = 12   // 2^12 = 4096 levels
}

type node struct {
    key   []byte
    value []byte
    next  [maxLevel]atomic.Pointer[node]
}
```

Insertion is CAS-based from the bottom up: find the predecessor at each level, then CAS the `next` pointer. If the CAS fails (another writer inserted concurrently), the entire insertion retries. There is no mutex in the hot path — only atomic operations.

When the memtable exceeds `Options.MemTableSize` (default 64 MB), it is frozen (no new writes accepted) and a background goroutine flushes it to an L0 SST file. New writes go to a fresh active memtable.

**SST file format:**

```
┌────────────────────────────────────────────────────────┐
│ [DataBlock_0]                                          │
│ [DataBlock_1]                                          │
│ ...                                                    │
│ [DataBlock_N]                                          │
│ [IndexBlock]  — one entry per data block               │
│ [BloomFilter]  — bitset, 10 bits per key               │
│ [Footer]                                               │
└────────────────────────────────────────────────────────┘
```

- **Data blocks** (default 4 KB): K-V pairs are delta-encoded — each key stores only the delta from the previous key. Restart points every 16 K-V pairs enable O(1) binary search within the block.
- **Index block**: one entry per data block: `[largestKey:varint][blockOffset:varint][blockSize:varint]`. Binary search on `largestKey` locates the target block.
- **Bloom filter**: FNV-1a double-hash with two independent seeds (`0x811C9DC5` and `0x01000193`). 10 bits per key yields ~1% false positive rate. Dynamic sizing: `(N * 10 + 7) / 8` bytes.
- **Footer** (28 bytes): `[indexOffset:8][indexSize:4][bloomOffset:8][bloomSize:4][magic:4]`

**Read path:**

```
active memtable → frozen memtable(s) (newest first) → L0 SSTs (newest first) → L1+ SSTs
```

For L1+, the index block is binary-searched to locate the target data block, then the bloom filter is checked before reading the block. If the bloom says "definitely not present," the SST file is skipped entirely.

**Leveled compaction:**

When L_k exceeds its size budget (L0 = 4 MB, L1 = 32 MB, each subsequent level 10× larger), a compaction job is created:

1. Pick the oldest files from L_k.
2. Identify overlapping files from L_{k+1}.
3. Multi-way merge sort all inputs in sorted key order.
4. Write output to a temp directory.
5. Atomically rename temp files to L_{k+1}, update the manifest, delete old input files.

**Atomic manifest versioning:**

```
write to temp file → fsync temp → rename to final path → fsync directory
```

The manifest is the single source of truth for which SST files are live. `Version` is immutable once created — new versions are produced by applying a `VersionDiff`.

### TXN — Transaction Layer

Provides MVCC snapshot isolation for readers and serializable writes. This is where Razordata diverges most significantly from SQLite.

**Version chain:**

```go
type versionNode struct {
    txnID    uint64
    beginTS  uint64
    endTS    uint64  // math.MaxUint64 = uncommitted
    key      []byte
    value    []byte
    deleted  bool    // tombstone
    next     atomic.Pointer[versionNode]
}
```

Each primary key in the storage engine points to a singly-linked list of version nodes (newest first). A reader traverses the chain, skipping versions where `beginTS >= readTS` or `endTS < readTS`.

**Per-transaction arena:**

```go
type arena struct {
    buf    []byte
    offset atomic.Int64
    size   int64
}

func (a *arena) Alloc(n int) []byte {
    for {
        old := a.offset.Load()
        new := old + int64(n)
        if new > a.size {
            return nil // exhausted
        }
        if a.offset.CompareAndSwap(old, new) {
            return a.buf[old:new]
        }
    }
}
```

Each transaction gets its own 1 MB arena (pooled via `sync.Pool`). Version nodes are bump-pointer allocated from the arena — no individual `make` calls, no GC pressure. On commit or abort, the entire arena is returned to the pool.

The design initially considered per-goroutine arenas, but Go does not expose goroutine IDs in a portable way. Per-transaction arenas are simpler: lifecycle aligns with transaction boundaries, and rollback is O(1) (just release the arena).

**Hazard pointers:**

```go
type hazardPointerSet struct {
    ptrs [2]atomic.Value // [current, next]
}
```

Before dereferencing a version node pointer, the reader publishes it to one of two hazard pointer slots via `atomic.Store`. The reclamation pass scans all registered hazard pointers before freeing any node. The double-slot design allows readers to prefetch the next node while holding the current node in the other slot.

**Epoch-based reclamation:**

A background goroutine increments a global epoch counter every ~100 ms. Each reader registers with the epoch manager on first read. Old version nodes are only freed when all readers have advanced past the epoch at which the nodes were deprecated. This guarantees no reader sees a freed node.

**Transaction slot array:**

```go
const MaxConcurrentTXNs = 1024

type transactionSlot struct {
    txnID     uint64
    status    atomic.Int32 // 0=inactive, 1=active, 2=committed, 3=aborted
    beginTS   uint64
    commitTS  uint64
    writeSet  []KeyRange
    arena     *arena
}
```

A pre-allocated fixed-size array of 1024 slots. Allocation uses a mutex-protected free list — no GC pressure, O(1) allocation.

**Commit protocol (6 phases):**

1. **Begin**: Allocate slot, assign `beginTS = globalAtomicCounter++`, register with epoch manager, take read view.
2. **Read**: Traverse version chain via hazard pointers. No locks acquired.
3. **Write**: Allocate version node from arena, CAS-insert at chain head. Write `RTData` to WAL.
4. **Pre-commit (validate)**: Scan all committed slots. If any slot with `commitTS > myBeginTS` modified a key in my `writeSet`, abort. This is write-write conflict detection.
5. **Commit**: Assign `commitTS = globalAtomicCounter++`, write `RTCommit` to WAL, `fsync`, CAS-update `endTS` on all version nodes from `MaxUint64` to `commitTS`.
6. **Post-commit**: Release arena, deregister from epoch manager.

**Concurrency guarantees:**

| Interaction | Mechanism |
|---|---|
| Read-Write | Lock-free. Readers traverse version chains without blocking writers. |
| Write-Write | Detected at pre-commit. Conflicting transactions are aborted. |
| Write-Read | Writers never block readers. Old versions remain visible until epoch reclamation. |

### SQL — SQL Processing Layer

Receives raw SQL text, tokenizes it, builds an AST, rewrites and plans it, then executes the operator tree to return rows. It never touches the disk directly.

**Lexer:**

- Token types are value types (`struct { Type TokenType; Lexeme string; Literal any; Line int; Col int }`) — no interface allocations.
- Keyword lookup via a static `map[string]TokenType` for O(1) recognition.
- Error recovery: on malformed input, the lexer advances to the next delimiter and emits `T_EOF` with an error, allowing the parser to collect all errors in one pass.

**Parser:**

- LL(1) recursive descent. `parseSelect()`, `parseInsert()`, `parseUpdate()`, `parseDelete()`, `parseCreateTable()`, `parseDropTable()`.
- Expression parsing uses operator precedence: comparison > add/sub > mul/div > unary > primary.
- AST nodes are concrete structs with no interface fields (except the `Expr` and `Stmt` marker interfaces). This avoids interface dispatch overhead in the rewriter and planner.

**Rewriter:**

- **Constant folding**: `1 + 2 * 3` → `7` at parse time.
- **Predicate pushdown**: moves `WHERE` conditions as close to the data source as possible.
- **Subquery flattening**: merges single-row subqueries in `WHERE IN` into a join or list lookup.

**Planner:**

- Cost-based: estimates I/O cost from key selectivity (uniform distribution initially, histogram support via `ANALYZE`).
- Index selection: if a `WHERE` column has an index, `IndexScan`; otherwise `SeqScan`.
- Plan memoization: equivalent query shapes share sub-plans. Memo key = `SHA256(canonical_binary_encoding(AST))`.
- Sort ordering: if `ORDER BY` matches primary key order, the explicit sort is eliminated.

**Executor (streaming operator tree):**

```go
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}
```

The executor is a pull-based tree traverser. Parent calls `child.Next()`, processes the row, yields to its parent. There is no bytecode VM, no code generation — pure Go struct interpretation.

Operators shipped: `SeqScan`, `IndexScan`, `Filter`, `Project`, `Sort`, `Limit`, `Aggregate`, `HashAggregate`, `NestedLoopJoin` (INNER/CROSS/LEFT/RIGHT/FULL), `Distinct`, `Insert`, `Update`, `Delete`, `WindowOperator`.

**SIMD vectorized execution:**

For filter-heavy queries, operators process 1024-row columnar batches:

```go
func evaluateBatch(pred Expr, cols [][]byte, mask []uint16) int {
    count := 0
    for i := 0; i < len(cols[0]); i += 4 {
        v0, v1, v2, v3 := cols[0][i], cols[0][i+1], cols[0][i+2], cols[0][i+3]
        if pred(v0) { mask[count] = uint16(i); count++ }
        if pred(v1) { mask[count] = uint16(i+1); count++ }
        if pred(v2) { mask[count] = uint16(i+2); count++ }
        if pred(v3) { mask[count] = uint16(i+3); count++ }
    }
    return count
}
```

- Columnar layout (`[]int64`, `[]float64`, `[]string`) instead of row-by-row.
- 4-wide manual unrolling for cache-line-friendly batch evaluation.
- Selection vectors (`[]uint16`) mask which rows pass the filter.
- Adaptive threshold: auto-fallback to row-at-a-time for tables < 100K rows.

**Parallel execution:**

- Table scans are split into key-range partitions, each processed by a worker.
- Worker pool sized to `runtime.GOMAXPROCS(0)`.
- Parallel sort: sample sort for top-k, external merge sort for large datasets.
- Results merged via bounded channels (non-blocking send, drop on overflow).

### SYS — System Layer

The top-level entry point that manages the database lifecycle.

**Engine lifecycle:**

```
Open → validate Options → construct subsystems in order → WAL replay → ready
Close → set closed flag → flush pending writes → stop background goroutines → close subsystems in reverse order
```

**Graceful shutdown (6 phases):**

1. Stop accepting new operations (closed flag flips first).
2. Wait for active transactions (30s timeout, force-abort on expiry).
3. Flush: `eng.Sync` + `wal.Sync` + `fl.Sync`.
4. Stop background goroutines (compaction, flush, epoch manager; 5s budget each).
5. Close subsystems in reverse dependency order.
6. Log final stats.

**Session pooling:** `sync.Pool` for `Session` objects. Avoids allocation on every `Begin`.

**Read-only mode:** When `Options.ReadOnly = true`, WAL writes are skipped, data files are opened with `O_RDONLY`, and DML returns `ErrReadOnly`.

**Error taxonomy:**

```go
var retryable = []error{ErrIO, ErrLocked}
var fatal = []error{ErrTxAborted, ErrCorrupt, ErrSyntax, ErrTypeMismatch, ErrUpgradeRequired, ErrReadOnly}
```

All errors wrap: I/O errors → structural errors → API-level errors. Messages are lowercase, no trailing punctuation.

## 4. Comparative Analysis: Razordata vs. SQLite

| Dimension | SQLite | Razordata |
|---|---|---|
| **Storage engine** | B-tree | LSM tree (skiplist memtable + SST + leveled compaction) |
| **Write concurrency** | WAL mode: single writer | Lock-free skiplist: multiple concurrent writers via MVCC |
| **Read concurrency** | Shared cache: readers block writers in journal mode | MVCC version chains: readers never block writers |
| **Isolation** | Serializable (WAL) | Read-uncommitted (v1); read-committed planned |
| **Language** | C (~150K LOC) | Go |
| **Memory safety** | Manual (SQLITE_MALLOCS) | GC-managed; arenas for hot paths |
| **SIMD/vectorization** | None | 4-wide unrolling, columnar batches, selection vectors |
| **Parallelism** | None (single-writer serialization) | Parallel scan, parallel sort, worker pool |
| **Observability** | Extension-dependent | Built-in hooks: tracing, metrics, profiling |
| **File format** | Single `.sqlite` file | Directory: `meta.razor`, `wal/`, `sst/`, `manifest` |
| **Build dependencies** | C compiler, platform-specific | Go toolchain only |
| **Cross-compilation** | Requires target-specific build | `GOOS=... GOARCH=... go build` |
| **Startup** | Open file, read header | WAL replay, hint file warm-up, manifest load |

### Where SQLite Wins

- **Maturity**: 25+ years of production hardening. Razordata is pre-1.0.
- **Single-file portability**: One `.sqlite` file vs. a directory tree.
- **Read performance on small datasets**: B-tree point reads are O(log N) with excellent cache behavior. LSM reads must check memtable + L0 + L1 + ... (mitigated by bloom filters).
- **Write-ahead logging**: SQLite's WAL is simpler and battle-tested. Razordata's WAL has known gaps (REQ000171: commit records not yet written to WAL).

### Where Razordata Wins

- **Write throughput under concurrency**: Lock-free skiplist + MVCC eliminates writer serialization.
- **Large dataset performance**: LSM trees excel at write-heavy workloads with large datasets. Compaction amortizes write amplification.
- **Concurrent read-write**: Readers never block writers, writers never block readers.
- **Embedded parallelism**: SIMD vectorization and parallel query execution are not retrofits — they are designed into the executor from the start.
- **Go ecosystem integration**: No CGO. Native Go types in the API. Goroutine-safe by construction.

## 5. What's Shipped and What's Coming

By v0.22, Razordata has completed 24 iterations. The shipped feature set:

**DDL**: CREATE TABLE, DROP TABLE, CREATE INDEX, DROP INDEX

**DML**: INSERT (with ON CONFLICT DO NOTHING/UPDATE), UPDATE, DELETE, RETURNING

**Queries**: SELECT, WHERE, ORDER BY, LIMIT/OFFSET, GROUP BY, HAVING, DISTINCT, EXPLAIN, EXPLAIN QUERY PLAN

**Joins**: INNER, CROSS, LEFT/RIGHT/FULL OUTER

**Subqueries**: IN, EXISTS, scalar, CTE (WITH)

**Window functions**: ROW_NUMBER, RANK, DENSE_RANK, LAG, LEAD, SUM/AVG OVER with PARTITION BY, ORDER BY, ROWS frame

**Types**: INTEGER, BIGINT, FLOAT, DECIMAL, BOOLEAN, TEXT, VARCHAR, BLOB, DATE, TIME, TIMESTAMP, JSON

**Constraints**: PRIMARY KEY, NOT NULL, DEFAULT, CHECK, UNIQUE

**Transactions**: BEGIN, COMMIT, ROLLBACK, SAVEPOINT, RELEASE, ROLLBACK TO

**Utilities**: VACUUM, ANALYZE, integrity_check, backup/restore, pragma, `razor` CLI

**Planned (iter-24)**: Read-committed isolation, foreign keys, ALTER TABLE, CREATE VIEW, triggers, pragmas (cache_size, journal_mode, synchronous).

## 6. Lessons from Building It

**LSM vs B-tree is not a clear winner.** LSM trees win on write throughput and large dataset performance. B-tree wins on point reads and small datasets. Razordata chose LSM because the target use case is write-heavy embedded workloads. For read-heavy workloads, a B-tree secondary index (shipped in v0.22) mitigates the LSM read penalty.

**Lock-free is correct but subtle.** The skiplist, version chain, and buffer pool all use CAS-based algorithms. Each one required iterative debugging: hazard pointer publication (REQ000175: publish to single slot, not all), epoch manager goroutine tracking (REQ000181: Go has no portable goroutine ID). These bugs were discovered and documented iteratively — the design documents describe the correct protocol; the implementation caught up over multiple iterations.

**Zero C dependencies is a feature, not a constraint.** Cross-compilation is `GOOS=linux GOARCH=arm64 go build`. Memory safety is guaranteed by the GC. No need to manage `malloc`/`free` lifetimes. The trade-off is that Go's GC must be accommodated — hence the arenas, `sync.Pool`, and careful avoidance of `map[string]interface{}` in hot paths.

**Testing discipline matters more than test count.** The rule is `go test ./... -race -count=1` must always pass. Every storage component requires benchmarks. Property-based tests verify crash/recovery. Error paths, edge cases, and boundary conditions are tested as aggressively as happy paths.

**Iterative integration prevents architecture drift.** The 8-layer build order is not just a development convenience — it is the architecture. Each iteration integrates with already-implemented layers, cross-checking interfaces, error types, and naming conventions before writing code. This catches incompatibilities early.

## 7. Conclusion

Razordata is not a SQLite replacement — it is a different tool for a different problem. SQLite excels at simple, reliable, single-file embedded storage. Razordata targets the space where write concurrency, parallel execution, and modern language ergonomics matter more than battle-tested maturity.

The eight-layer architecture, lock-free MVCC, LSM storage engine, and SIMD vectorized executor represent a deliberate set of trade-offs: complexity in exchange for concurrency, GC integration in exchange for memory safety, directory-based storage in exchange for richer metadata.

The project is pre-1.0 and has known gaps (WAL commit durability, read-committed isolation, foreign keys). But the foundation is sound, the architecture is auditable, and the build order ensures that each new feature integrates cleanly with what came before.
