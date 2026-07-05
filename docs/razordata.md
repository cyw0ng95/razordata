# Razordata: Architecture Overview

> An embedded database built from scratch in pure Go, designed for the multicore era.

## 1. Why Razordata

SQLite is the most widely deployed database engine in the world, but its architecture carries constraints from the early 1990s:

- **Single-writer serialization.** Writers are serialized at the WAL file level. Under write-heavy loads, this becomes a throughput bottleneck.
- **No MVCC.** Page-level locking means readers and writers can block each other in default journal mode.
- **C codebase.** Manual memory management, platform-specific build dependencies, cross-compilation friction.

Razordata keeps SQLite's ergonomic model — one directory, zero configuration, no server — while adopting storage and concurrency algorithms designed for modern hardware.

## 2. Design Constraints

| Constraint | Rationale |
|---|---|
| No external C dependencies | Cross-compilation is trivial; no `CGO` portability issues; memory safety via Go's GC |
| No network server | Embedded-first. The database lives in the same process as the application. |
| Go 1.26+ | Access to `slices`, `maps`, `iter`, `cmp`, `math/rand/v2`, `for range N`, modern `slog`. |
| Single `go.mod` | No nested modules. Flat, auditable dependency graph. |
| Page size: 4 KB | Matches OS page size for `O_DIRECT` alignment and `mmap` efficiency. |

## 3. Architecture: The Ten-Subsystem Stack

Razordata is organized as ten subsystems with strict dependency ordering. No layer may depend on a layer above it — this is enforced by the build graph:

```
LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQB → SYS
                                       ↗
                                   DBG (build-tagged, lateral)
```

Each layer exposes interfaces consumed by the layer above. The subsystems are:

| Subsystem | Responsibility | Function Clusters |
|---|---|---|
| `LOG` | Structured logging, async hook dispatch | `LG` (slog wrapper, levels, rotation), `HK` (trace/metric/profile hooks) |
| `FIL` | Block I/O, file management, meta page | `DF` (pread/pwrite, O_DIRECT, mmap, fadvise), `MF` (meta.razor), `LF` (WAL segments), `FS` (path validation), `IO` (io_uring wrapper) |
| `MEM` | Buffer pool, sync.Pool, hint file | `BF` (clock-sweep LRU, W-TinyLFU admission, sharded pool, pmem spill), `PC` (page slots, checksum), `SP` (object pooling), `OF` (off-heap large-object pool) |
| `WAL` | Write-ahead log, durability | `WR` (append, rotation, LSN, LZ4 compression), `FL` (fsync, batch commit), `RP` (replay, checkpoint) |
| `ENG` | LSM tree, SST, compaction, manifest | `LS` (memtable, SST, bloom, compaction, manifest), `ID` (index), `TB` (table DDL), `SC` (schema), `DP` (encoding), `CT` (persistent catalog), `NM` (NUMA topology) |
| `TXN` | MVCC, version chains, transactions | `MV` (version chain, arena), `LC` (hazard pointers, epoch), `SN` (read view), `VL` (commit protocol, conflict detection) |
| `SQF` | SQL frontend (parse/rewrite) | `LX` (lexer with keyword trie), `PS` (LL(1) parser, 722-line AST), `PL` (planner types, memo, learned selectivity model), `RE` (rewriter, SQL formatter) |
| `SQB` | SQL backend (execute) | `AD` (adaptive query compilation), `AG` (aggregation/window), `DT` (data types/schema), `EV` (expression evaluation, vectorized batch eval), `EX` (executor, cost-based planner), `OP` (operators), `UT` (batch/vectorized utilities, parallel worker pool, hash table, SIMD dispatch), `WT` (write operators, triggers, views, materialized views, ALTER TABLE) |
| `DBG` | Debug observability (build-tagged) | `CT` (counters/histograms), `DC` (dynamic runtime control), `DI` (debugger), `IN` (page inspection), `JD` (JOIN tracer), `PR` (profiler), `SK` (socket command server), `TE` (trace ring buffer) |
| `SYS` | Lifecycle, public API, sessions | `SY` (init, shutdown, stats), `AP` (Engine/Session API, structured error system), `SE` (session), `TX` (transaction), `ST` (statement), `DS` (`database/sql` driver), `BK` (online backup/restore) |

### 3.1 LOG — Structured Logging

The foundation layer. Every other subsystem depends on it.

- Wraps `log/slog` with an atomic level variable for lock-free level checks in the hot path.
- Async hook dispatch via bounded channel (non-blocking send, drop on overflow). Hooks never backpressure the logging path.
- Three built-in hooks: `TraceHook` (SQL query tracing with wall-clock timing), `MetricHook` (throughput/latency counters), `ProfileHook` (CPU/heap dumps on error events).
- Log rotation on size threshold. Compressed with gzip after rotation.

**Trade-off:** The bounded channel means the logging system can never block a database operation. If the hook dispatcher is slow, events are dropped. Observability should never compromise correctness.

### 3.2 FIL — File I/O

The lowest I/O layer. All disk access flows through here.

**Block I/O via `pread`/`pwrite`:** Positional reads/writes without seeking. Each block is addressed by `(fd, blockID * BlockSize)`. This avoids shared file offset state and enables concurrent reads on the same file descriptor.

**`O_DIRECT` with fallback:** On Linux, data files are opened with `O_DIRECT` to bypass the OS page cache. If the kernel rejects it (`EINVAL`), Razordata falls back to buffered I/O. WAL segments always use buffered I/O — `fsync` handles durability.

**`mmap` for direct page mapping:** On Linux, SST data blocks can be mapped directly into the process address space via `syscall.Mmap`. This eliminates copy-on-read overhead — the application reads from the page cache without a `pread` system call. Combined with `O_DIRECT` for writes, this gives zero-copy reads with bypass-the-cache writes.

**`fadvise` read-ahead hints:** `fadviseSequential` and `fadviseWillNeed` via `golang.org/x/sys/unix` hint the kernel to prefetch upcoming SST blocks during sequential scans. This overlaps I/O with CPU work for large table scans.

**io_uring wrapper:** Linux io_uring provides submission/completion queue pairs (SQE/CQE rings) for asynchronous I/O. Registered buffers and files avoid per-syscall setup. This is the foundation for future async read/write paths.

**CRC32 checksums:** Every 4 KB block carries a 4-byte IEEE CRC32 in its last 4 bytes. Reads verify the checksum; mismatches return `ErrCorrupt` without attempting recovery.

**Meta page** (`meta.razor`, always block 0):

```
┌────────────────────────────────────────────────────────┐
│ Magic: 0x5241524F ("RAZO")                             │
│ Version: uint32 (semantic version)                     │
│ BlockSize: uint32 (bytes, power of 2, default 4096)    │
│ CatalogRootPtr: uint64 (root of system catalog LSM)    │
│ ManifestChecksum: uint32 (CRC32 of current manifest)   │
└────────────────────────────────────────────────────────┘
```

Read on startup to validate magic bytes, load version, and locate the system catalog. Written only on `CREATE DATABASE` and `CHECKPOINT`.

**Path validation:** Rejects any path containing `..` or symlinks. All paths are resolved against the database root before use — prevents directory traversal attacks.

**File handle management:** Reference-counted `FileHandle` structs. `Refs` atomically incremented on `Open()`, decremented on `Close()`. When `Refs == 0`, the FD is closed. Prevents double-close via mutex.

**File layout:**

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

SST files are named `L<level>_<minKeyHex>_<maxKeyHex>_<fileID>.sst`. The `minKey` and `maxKey` are hex-encoded first and last key in the file, used for range overlap checks during compaction and reads.

### 3.3 MEM — Memory Management

Buffer pool that caches SST blocks in memory. All reads from the LSM tree go through here.

**Buffer pool with clock-sweep eviction:**

```
┌─────────────────────────────────────────────────────────────┐
│ bufferSlot                                                  │
│   blockID   uint64          // which block this slot holds  │
│   data      []byte          // fixed-size: BlockSize bytes  │
│   pinCount  atomic.Int32    // eviction blocked while > 0   │
│   dirty     atomic.Bool     // modified since load?         │
│   refKey    atomic.Uint64   // clock hand reference         │
│   loading   atomic.Bool     // prevents duplicate loads     │
│   wait      chan struct{}   // closed when loaded           │
└─────────────────────────────────────────────────────────────┘
```

The clock-sweep algorithm is a classical approximation of LRU:

1. A global `hand` counter (`atomic.Uint64`) advances on each eviction pass.
2. Each slot's `refKey` is atomically updated to the current hand value on access.
3. Eviction scans slots: if `refKey < hand - N` (N = clock interval) and `pinCount == 0`, the slot is evicted.
4. The hash table uses a sharded mutex (`sync.RWMutex`) — `RLock` for reads, `Lock` for eviction/insertion.

**W-TinyLFU admission:** A frequency-based admission policy tracks block access counts via a compact count-min sketch. When a new block is loaded, it competes against the eviction candidate — the loser is rejected. This prevents cache pollution from sequential scans that would otherwise evict hot blocks. The count-min sketch uses 4-bit counters (0–15) and a 1M-entry table, consuming ~500 KB.

**Sharded buffer pool:** The global hash table is split into 32 independent shards (power-of-2). Each shard has its own clock-sweep hand and mutex. This eliminates global mutex contention under concurrent reads — multiple goroutines can load and evict blocks in different shards simultaneously. Shard selection: `blockID % numShards`.

**Persistent memory spill:** For databases larger than the buffer pool, cold pages can be spilled to a persistent memory file (`pmem`). This avoids re-reading from disk for pages that were recently evicted but may be needed again. The pmem file is memory-mapped and uses a simple LRU for its own pages.

**Off-heap large-object pool:** A 16-class size-binned pool (64KB to 4MB) for large allocations that would otherwise cause GC pressure. Each class has its own `sync.Pool`. Allocations are rounded up to the nearest size class.

The `loading` flag on each slot prevents duplicate loads: only one goroutine loads a block from disk; others wait on the `wait` channel. This eliminates redundant I/O for concurrent reads of the same block.

**`sync.Pool` for zero-allocation hot paths:**

```
syncPool:
  pagePool  sync.Pool → make([]byte, BlockSize)       — 4 KB buffers
  iterPool  sync.Pool → make([]byte, iterBufferSize)  — LSM iterator scratch
```

`pagePool.New` allocates exactly `BlockSize` bytes. On `Put`, buffers that don't match the expected size are dropped (not returned to the pool) — this prevents pool poisoning from caller bugs.

**Hint file for warm startup:**

On clean shutdown, the buffer pool serializes its hot working set (all slots with `LastAccess > 0`) to `<name>.razor/hint`. Format: `[count:varint][entry_0][entry_1]...[entry_N]`, where each entry is `[blockID:varint][lastAccess:varint]`. On startup, the hint file is read (decompressed if `.gz`) and blocks are eagerly loaded into the buffer pool *before* serving any queries. This eliminates cold-start latency for frequently accessed data.

The hint file is advisory — no checksum. On corruption, the buffer pool starts cold.

### 3.4 WAL — Write-Ahead Log

The sole write path for durability. All mutations are serialized to the WAL before the storage engine writes data.

**Record encoding:**

```
┌──────────────┬──────────┬─────────────┬──────────────────┐
│ length:varint │ txnID:varint │ type:uint8 │ payload:blob   │
└──────────────┴──────────────┴─────────────┴──────────────────┘
```

- `length` = total bytes of `txnID + type + payload` (not including the length field itself). Stored as a varint so the reader can skip unknown record types.
- `payload` varies by record type:

| Type | Code | Payload |
|---|---|---|
| `RTData` | 0 | `[blockID:8][checksum:4][data:varint]` — a mutated block image |
| `RTCommit` | 1 | `[commitTS:8]` — marks transaction as committed |
| `RTRollback` | 2 | (empty) — discards uncommitted write set |
| `RTCheckpoint` | 3 | `[checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4][activeTXNs:varint...]` |
| `RTMerge` | 4 | `[newVersion:8][deletedFiles:varint...][addedFiles:varint...]` — LSM version transition |

**LZ4 compression:** WAL records can be LZ4-compressed before writing. A pure-Go LZ4 block-format codec (greedy hash-chain match finder) compresses `RTData` payloads. Compression ratio depends on data entropy — typically 2–4x for text-heavy rows. The compression flag is stored in the record type byte.

**Segment rotation:** WAL segments are 64 MB files named `wal.000`, `wal.001`, ... (zero-padded to 3 digits for lexicographic sorting). When a segment fills, it is closed and a new one is created. A pre-allocated 256 KB `writeBuffer` avoids per-record allocation — records are appended via `binary.LittleEndian` directly into the buffer.

**LSN encoding:** The log sequence number is a single `atomic.Int64`. It encodes both segment number and offset: `lsn = segmentNumber * SegSize + offset`. Readers use the LSN to detect stale reads and to order replay.

**Batch commit:** Multiple transactions can be grouped into one `fsync` call via a `sync.WaitGroup` and a single write barrier. This amortizes the cost of `fsync` (typically 1–10 ms on SSDs) across concurrent transactions.

**Corruption recovery:**

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

The replayer does NOT write SST files or update the manifest — those are derived from the manifest file on startup, not from WAL replay.

### 3.5 ENG — Storage Engine (LSM Tree)

The core of the database. Implements the LSM tree: a lock-free skiplist memtable that flushes to SST files on disk, leveled compaction, bloom filters, and an atomic file manifest.

**Lock-free skiplist memtable:**

```
skipList:
  head    atomic.Pointer[node]   // sentinel node
  level   atomic.Int32           // current max level, starts at 1
  maxLevel int = 12              // 2^12 = 4096 levels

node:
  key   []byte
  value []byte
  next  [maxLevel]atomic.Pointer[node]
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

- **Data blocks** (default 4 KB): K-V pairs are delta-encoded — each key stores only the delta from the previous key. Restart points every 16 K-V pairs enable O(1) binary search within the block. Format: `[KV pairs][restart array][restart count:4][checksum:4]`.
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

The manifest is the single source of truth for which SST files are live. `Version` is immutable once created — new versions are produced by applying a `VersionDiff`. The manifest stores: file ID, level, key range, size, and bloom bit count for every live SST.

**System catalog:** The catalog is a special LSM tree stored under `sst/catalog/`. Schema data is key-value pairs: `__catalog:<tableID>` → MessagePack-encoded `TableSchema`. The catalog root pointer is stored in the meta page.

**Persistent catalog (ENG/CT):** A binary catalog file (`catalog.dat`) with `RCAT` magic bytes stores table schemas in a versioned format (V1/V2). Entries are serialized via encode/decode callbacks, with atomic persistence via temp-file + fsync + rename. Supports schema upgrade paths with `ErrUpgradeRequired` for forward-versioned files.

**NUMA topology (ENG/NM):** Detects NUMA node layout by reading `/sys/devices/system/node`. Caches node count and provides `NodeForCPU` for CPU-to-NUMA-node mapping. Used by the parallel worker pool to pin workers to local NUMA nodes, minimizing cross-node memory access.

**Row encoding:** Fixed-width columns stored inline: `INT` (8 bytes), `BIGINT` (8 bytes), `FLOAT` (8 bytes), `BOOL` (1 byte). Variable-length columns: `[length:varint][data:blob]`. Null values: a null bitmap in the row header, one bit per column.

### 3.6 TXN — Transaction Layer

MVCC snapshot isolation for readers, serializable writes. The sharpest divergence from SQLite.

**Version chain:**

```
versionNode:
  txnID    uint64
  beginTS  uint64
  endTS    uint64          // math.MaxUint64 = uncommitted
  key      []byte          // the key this version belongs to
  value    []byte          // the row data
  deleted  bool            // tombstone (logical deletion)
  next     atomic.Pointer[versionNode]  // next older version
```

Each primary key in the storage engine points to a singly-linked list of version nodes (newest first). A reader traverses the chain, skipping versions where `beginTS >= readTS` or `endTS < readTS`.

**Per-transaction arena:**

```
arena:
  buf    []byte            // 1 MB bump-pointer buffer
  offset atomic.Int64      // current allocation offset
  size   int64             // total size

Alloc(n):
  loop:
    old = offset.Load()
    new = old + n
    if new > size: return nil (exhausted)
    if offset.CompareAndSwap(old, new): return buf[old:new]
```

Each transaction gets its own 1 MB arena (pooled via `sync.Pool`). Version nodes are bump-pointer allocated from the arena — no individual `make` calls, no GC pressure. On commit or abort, the entire arena is returned to the pool. Rollback is O(1) (just release the arena).

**Hazard pointers:**

```
hazardPointerSet:
  ptrs [2]atomic.Value   // [current, next]
```

Before dereferencing a version node pointer, the reader publishes it to one of two hazard pointer slots via `atomic.Store`. The reclamation pass scans all registered hazard pointers before freeing any node. The double-slot design allows readers to prefetch the next node while holding the current node in the other slot.

**Epoch-based reclamation:**

A background goroutine increments a global epoch counter every ~100 ms. Each reader registers with the epoch manager on first read. Old version nodes are only freed when all readers have advanced past the epoch at which the nodes were deprecated. This guarantees no reader sees a freed node.

**Transaction slot array:**

```
const MaxConcurrentTXNs = 1024

transactionSlot:
  txnID     uint64
  status    atomic.Int32   // 0=inactive, 1=active, 2=committed, 3=aborted
  beginTS   uint64
  commitTS  uint64
  writeSet  []KeyRange     // key ranges modified by this transaction
  arena     *arena
```

A pre-allocated fixed-size array of 1024 slots. Allocation uses a mutex-protected free list — no GC pressure, O(1) allocation.

**Commit protocol (6 phases):**

1. **Begin**: Allocate slot, assign `beginTS = globalAtomicCounter++`, register with epoch manager, take read view (snapshot of version chain heads).
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
| Buffer Pool | Clock-sweep eviction with deduplication. Sharded mutex for hash table. |
| WAL | Batch commit with write barrier. Multiple transactions per `fsync`. |

### 3.7 SQL — SQL Processing Layer

Receives raw SQL text, tokenizes it, builds an AST, rewrites and plans it, then executes the operator tree to return rows. Never touches the disk directly.

**Pipeline:**

```
SQL text → Lexer (tokens) → Parser (AST) → Rewriter (normalized AST) → Planner (plan tree) → Executor (rows)
```

**Lexer:**

- Token types are value types (`struct { Type TokenType; Lexeme string; Literal any; Line int; Col int }`) — no interface allocations.
- Keyword lookup via a static `map[string]TokenType` for O(1) recognition.
- Error recovery: on malformed input, the lexer advances to the next delimiter and emits `T_EOF` with an error, allowing the parser to collect all errors in one pass.

**Parser:**

- LL(1) recursive descent. `parseSelect()`, `parseInsert()`, `parseUpdate()`, `parseDelete()`, `parseCreateTable()`, `parseDropTable()`, plus dedicated parsers for window functions, transactions, EXPLAIN, VALUES, and virtual tables.
- Expression parsing uses operator precedence: comparison > add/sub > mul/div > unary > primary.
- AST nodes are concrete structs with no interface fields (except the `Expr` and `Stmt` marker interfaces). This avoids interface dispatch overhead in the rewriter and planner.
- Full 722-line AST (`ast.go`) with `Loc` position tracking on every node for precise error reporting. Visitor pattern for tree traversal.

**Rewriter:**

- **Constant folding**: `1 + 2 * 3` → `7` at parse time.
- **Predicate pushdown**: moves `WHERE` conditions as close to the data source as possible.
- **Subquery flattening**: merges single-row subqueries in `WHERE IN` into a join or list lookup.

**Planner:**

- Cost-based: estimates I/O cost from key selectivity (uniform distribution initially, histogram support via `ANALYZE`).
- Index selection: if a `WHERE` column has an index, `IndexScan`; otherwise `SeqScan`. Strategy pattern (`ScanStrategy`) replaces the 5-mode god-struct with pluggable strategies: `InMemoryScan`, `SeekScan`, `BTreeScan`, etc.
- Join ordering: N3 optimizer with bushy/left-deep join enumeration, cost model with selectivity estimation per predicate type.
- Plan memoization: equivalent query shapes share sub-plans. Memo key = `SHA256(canonical_binary_encoding(AST))`. Parameterized plans (`NormalizeForMemo`) separate planning decisions from operator-tree literals.
- Sort ordering: if `ORDER BY` matches primary key order, the explicit sort is eliminated.
- Slot resolution: post-plan optimizer pass resolves column references to ordinal indices (`SlotIdx`), enabling O(1) runtime access via `row.Data[slotIdx]` instead of per-row string lookup.
- Learned selectivity model: tracks per-column-pair correlations, trains predicate feature vectors from histogram selectivity + row count + distinct count + null count for improved cardinality estimation.

**Executor (streaming operator tree):**

```go
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}
```

The executor is a pull-based tree traverser. Parent calls `child.Next()`, processes the row, yields to its parent. There is no bytecode VM, no code generation — pure Go struct interpretation.

**Operators shipped:**

| Category | Operators |
|---|---|
| Scan | `SeqScan`, `IndexScan` (with strategy pattern), `BitmapHeapScan`, `IndexOnlyScan`, `SqliteMaster` (virtual table) |
| Filter/Project | `Filter`, `Project`, `Distinct` |
| Join | `NestedLoopJoin` (INNER/CROSS/LEFT/RIGHT/FULL), `HashJoin` (radix-partitioned, INNER/LEFT/RIGHT/FULL), `ParallelHashJoin`, `HashCrossJoin` (small tables ≤1024 rows), `MergeJoin` (sort-merge) |
| Aggregate/Window | `Aggregate`, `HashAggregate`, `ParallelHashAggregate`, `WindowOperator`, `GROUP_CONCAT` |
| Sort | `Sort`, `ParallelSort` (sample-sort with N-way parallel partition) |
| Limit | `Limit` |
| Compound | `CompoundOp` (UNION/UNION ALL/INTERSECT/EXCEPT with streaming fast-path) |
| Write | `Insert`, `Update`, `Delete`, `Upsert` (ON CONFLICT DO NOTHING/UPDATE) |
| DDL | `CreateTable`, `DropTable`, `CreateIndex`, `DropIndex`, `CreateView`, `DropView`, `CreateMaterializedView`, `DropMaterializedView`, `RefreshMaterializedView`, `CreateTrigger`, `DropTrigger`, `AlterTable`, `TruncateTable`, `Reindex` |
| Debug | `PragmaResult` (single-row PRAGMA output) |

**Vectorized execution:**

For filter-heavy queries, operators process columnar batches (default 64 rows, configurable):

```
Batch layout: columnar arrays ([]int64, []float64, []string) via sync.Pool
Evaluation: 4-wide/8-wide manual unrolling for L1 cache-friendly batch evaluation
Selection vectors: SelRange compact mask for contiguous filtered rows
Adaptive threshold: auto-fallback to row-at-a-time for small tables
```

**Vectorized operators:** `VectorizedSeqScan`, `VectorizedFilter`, `VectorizedProject`, `VectorizedHashJoin`, `VectorizedCount`, `VectorizedSum`, `VectorizedAvg`, `VectorizedMin`, `VectorizedMax`. Automatic eligibility checking via `tryVectorizePlan` transforms eligible operator trees into batch-processing pipelines. `BatchToRowAdapter` and `RowOperatorAdapter` bridge between batch and row-at-a-time domains.

**Adaptive query compilation (ADQC):** The `AdaptiveOp` wrapper hot-swaps from interpreted to specialized batch execution after a configurable invocation threshold. Plans are compiled once and cached in an LRU `AdqcCache` (256 entries, keyed by plan hash + schema version). `FallbackOp` provides panic-safe fallback to the interpreted path. Telemetry counters track specialization rate, fallbacks, and invalidations.

**Parallel execution:**

- Table scans are split into key-range partitions, each processed by a worker.
- NUMA-aware worker pool: detects NUMA topology and pins workers to local nodes, minimizing cross-node memory access. Pool sized to `runtime.GOMAXPROCS(0)`.
- Parallel sort: sample-sort with N-way parallel partition sort via worker pool.
- Parallel hash join: right-side hash table built in parallel across workers (per-partition mutex), single-threaded probe.
- Parallel hash aggregate: input partitioned by group-key hash, N partial hash tables built concurrently, merged at the end.
- Results merged via bounded channels (non-blocking send, drop on overflow).

### 3.8 SYS — System Layer

Top-level entry point managing the database lifecycle.

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

**Session pooling:** `sync.Pool` for `Session` objects. Avoids allocation on every `Begin`. Goroutine-safe via mutex — sessions are not shareable between goroutines.

**Read-only mode:** When `Options.ReadOnly = true`, WAL writes are skipped, data files are opened with `O_RDONLY`, and DML returns `ErrReadOnly`.

**Structured error system:** The `Error` type carries Kind (18 kinds: NotFound, Constraint, DuplicateKey, etc.), Code (SQLSTATE), Module, Layer, Op, Fields map, and wrapped cause. Predefined operation constants cover all SQL and engine operations. Errors serialize to JSON for logging and diagnostics. `ConstraintError` provides structured constraint violation details (table, constraint name, columns, values, operation).

**Database/sql driver:** Registers as the `"razor"` driver. DSN-based engine cache with reference counting implements `driver.Driver`/`Connector`/`Conn`/`Stmt`/`Rows`/`Tx`. Enables standard `database/sql` usage with connection pooling.

**Online backup/restore:** Point-in-time consistent backup via read-lock acquisition, file copy, and integrity verification.

## 4. Concurrency Model

| Interaction | Mechanism |
|---|---|
| Read-Write | Lock-free. MVCC version chains, hazard pointers, epoch-based reclamation. |
| Write-Write | Detected at pre-commit. Conflicting transactions aborted (serializable). |
| Write-Read | Writers never block readers. Old versions remain visible until reclamation. |
| Buffer Pool | Clock-sweep eviction with deduplication. Sharded mutex for hash table. |
| WAL | Batch commit with write barrier. Multiple transactions per `fsync`. |

## 5. What's Shipped vs. Planned

### Shipped

**DDL:** CREATE TABLE, DROP TABLE, CREATE INDEX, DROP INDEX, CREATE VIEW, DROP VIEW, CREATE MATERIALIZED VIEW, DROP MATERIALIZED VIEW, REFRESH MATERIALIZED VIEW, CREATE TRIGGER, DROP TRIGGER, ALTER TABLE (ADD/DROP/RENAME COLUMN), TRUNCATE TABLE, REINDEX, ATTACH/DETACH DATABASE, CREATE VIRTUAL TABLE

**DML:** INSERT (with ON CONFLICT DO NOTHING/UPDATE), INSERT OR REPLACE/IGNORE/ROLLBACK/ABORT/FAIL, UPDATE (with FROM), DELETE, RETURNING, UPSERT

**Queries:** SELECT, WHERE, ORDER BY (with NULLS FIRST/LAST), LIMIT/OFFSET (FETCH FIRST syntax), GROUP BY, HAVING, DISTINCT, EXPLAIN, EXPLAIN ANALYZE, EXPLAIN FORMAT (TEXT/TREE/JSON/DOT), SELECT ... VALUES

**Joins:** INNER, CROSS, LEFT/RIGHT/FULL OUTER, hash join (radix-partitioned), merge join, parallel hash join

**Subqueries:** IN, EXISTS, scalar, CTE (WITH, WITH RECURSIVE)

**Window functions:** ROW_NUMBER, RANK, DENSE_RANK, LAG, LEAD, SUM/AVG/COUNT/MIN/MAX OVER with PARTITION BY, ORDER BY, ROWS/RANGE frame, EXCLUDE clause, FILTER clause

**Types:** INTEGER, BIGINT, FLOAT, DECIMAL(P,S), BOOLEAN, TEXT, VARCHAR, BLOB, DATE, TIME, TIMESTAMP, JSON

**Constraints:** PRIMARY KEY, NOT NULL, DEFAULT, CHECK, UNIQUE, FOREIGN KEY (ON DELETE/UPDATE CASCADE, RESTRICT, SET NULL, SET DEFAULT, NO ACTION), stored generated columns

**Aggregate functions:** COUNT, SUM, AVG, MIN, MAX, GROUP_CONCAT (with SEPARATOR)

**JSON functions:** json_extract, json_object, json_array, json_type, and more

**Date/time functions:** NOW, date/time parsing and formatting

**Transactions:** BEGIN, COMMIT, ROLLBACK, SAVEPOINT, RELEASE, ROLLBACK TO, SET TRANSACTION

**Utilities:** VACUUM, ANALYZE (with reservoir sampling and histogram persistence), integrity_check, backup/restore, PRAGMA (with listener system), `razor` CLI

**Testing:** SQLite Compatibility Test Suite (pure-Go SQLLogicTest driver, dual-runner with modernc.org/sqlite)

### Planned / Known Gaps

- OR-to-IN predicate conversion for point lookups (REQ001218)
- N3 cost model OR selectivity (REQ001219)
- 3-table+ comma-join output column shuffling (REQ001162)
- Recursive CTE iteration bugs (REQ001184, REQ001185, REQ001186)
- Executor cache sharing for per-query allocation reduction (REQ001220)
- Row arena allocation for decode buffer optimization (REQ001221)

## 6. Key Design Trade-offs

| Decision | Benefit | Cost |
|---|---|---|
| LSM tree over B-tree | Write throughput, large dataset performance | Point reads check memtable + L0 + L1+ (mitigated by bloom filters) |
| Lock-free skiplist + MVCC | Concurrent writes, readers never block | Complex commit protocol, write amplification (old versions in chain) |
| Per-transaction arenas | Zero GC pressure, O(1) rollback | More frequent allocation under high concurrency |
| Directory-based storage | WAL independent of data, atomic manifest rename, warm-start hints | Less portable than single-file |
| Pull-based executor (no VM) | Simple, debuggable, Go-native | No JIT/codegen optimization |
| Adaptive query compilation | Hot-swap interpreted→specialized after threshold | Compilation overhead on first invocation; cache memory |
| Vectorized execution | 4-8x throughput for batch-friendly operators | Adapter overhead when mixing batch and row-at-a-time |
| Parallel execution by default | Scales with cores | Coordination overhead on small tables (auto-fallback at <100K rows) |
| NUMA-aware worker pool | Minimizes cross-node memory access | Platform-specific (Linux only); adds complexity |
| W-TinyLFU admission | Prevents scan pollution of hot blocks | 500 KB memory overhead for count-min sketch |
| Bounded log channel | Never blocks DB operations | Events dropped under hook overload |
| Structured error system | Rich diagnostics, JSON serialization, operation tracking | More code than simple error strings |

## 7. Comparative Analysis: Razordata vs. SQLite

| Dimension | SQLite | Razordata |
|---|---|---|
| **Storage engine** | B-tree | LSM tree (skiplist memtable + SST + leveled compaction) |
| **Write concurrency** | Single writer | Multiple concurrent writers via MVCC |
| **Read concurrency** | Parallel in WAL mode | Parallel via MVCC version chains |
| **Isolation** | Serializable (WAL) | Snapshot isolation (MVCC) |
| **Language** | C (~150K LOC) | Go |
| **Memory safety** | Manual | GC-managed; arenas for hot paths |
| **Vectorization** | None | Columnar batches, 4-8x unrolling, selection vectors, adaptive compilation |
| **Parallelism** | None | Parallel scan, sort, hash join, hash aggregate; NUMA-aware worker pool |
| **Join algorithms** | Nested loop only | Nested loop, hash join (radix-partitioned), merge join, parallel hash join |
| **Observability** | Extension-dependent | Built-in: tracing, metrics, profiling, JOIN tracer, debug socket |
| **File format** | Single `.sqlite` file | Directory: `meta.razor`, `wal/`, `sst/`, `manifest`, `catalog.dat` |
| **Build** | C compiler, platform-specific | Go toolchain only |
| **SQL surface** | Full SQLite dialect |大部分 SQLite dialect (CTEs, window functions, triggers, views, materialized views, UPSERT, ALTER TABLE) |

### Where SQLite Wins
- **Maturity:** 25+ years of production hardening.
- **Single-file portability:** One file you can email.
- **Read performance on small datasets:** B-tree point reads are O(log N) with excellent cache behavior.
- **WAL simplicity:** Battle-tested, simpler format.

### Where Razordata Wins
- **Write throughput under concurrency:** Lock-free skiplist + MVCC eliminates writer serialization.
- **Large dataset performance:** LSM trees excel at write-heavy workloads with large datasets.
- **Concurrent read-write:** Readers never block writers, writers never block readers.
- **Embedded parallelism:** Vectorized execution, parallel hash join/sort/aggregate, NUMA-aware worker pool.
- **Rich join algorithms:** Hash join (radix-partitioned), merge join, parallel hash join — not just nested loop.
- **Adaptive query compilation:** Hot-swap from interpreted to specialized batch execution for repeated queries.
- **Go ecosystem integration:** No CGO. Native Go types. Goroutine-safe by construction. `database/sql` driver.
- **Observability:** First-class structured logging, metrics, profiling, JOIN tracer, debug socket — not add-ons.
- **Modern SQL:** CTEs (recursive), window functions (with FILTER/EXCLUDE), triggers, materialized views, UPSERT, JSON functions.

## 8. Testing Methodology

- `go test ./... -race -count=1` must always pass.
- Table-driven tests for parser and executor.
- Property-based tests for storage (crash/recovery).
- Every storage component requires benchmarks.
- Error paths, edge cases, and boundary conditions tested as aggressively as happy paths.
- SQLite Compatibility Test Suite (622+ test files) validates SQL correctness against a reference implementation.
- Dual-runner mode: runs each SLT test against both Razordata and `modernc.org/sqlite`, comparing results.
- Fuzz tests for lexer and parser to catch edge cases in SQL input.
- Debug build tag (`-tags debug`) enables additional instrumentation: JOIN tracing, CTE tracing, page inspection, runtime knob toggling.

## 9. Conclusion

Razordata is not a SQLite replacement — it is a different tool for a different problem. SQLite excels at simple, reliable, single-file embedded storage. Razordata targets the space where write concurrency, parallel execution, and modern language ergonomics matter more than battle-tested maturity.

The ten-subsystem architecture (LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQB → DBG → SYS), lock-free MVCC, LSM storage engine, vectorized executor with adaptive compilation, and NUMA-aware parallel execution represent a deliberate set of trade-offs: complexity in exchange for concurrency, GC integration in exchange for memory safety, directory-based storage in exchange for richer metadata.

The project is pre-1.0 and has known gaps. But the foundation is sound, the architecture is auditable, and the build order ensures that each new feature integrates cleanly with what came before.
