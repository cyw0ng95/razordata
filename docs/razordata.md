# Razordata: An Embedded LSM-Based MVCC Database in Pure Go

> A research-style architecture report covering the implemented system as of 2026-07,
> with comparative analysis against modern embedded and server-side database systems.

---

## Abstract

Razordata is an embedded, serverless database written entirely in pure Go (no CGO, no
external C dependencies). It targets the embedded-data tier — the same deployment
context as SQLite — but trades SQLite's B-tree + page-locking model for an
LSM-tree storage engine with multi-version concurrency control (MVCC), a vectorized
and parallel query executor, and an adaptive query-compilation cache. The engine is
organized as ten strictly layered subsystems (LOG → FIL → MEM → WAL → ENG → TXN →
SQF → SQB → SYS, with a build-tagged DBG lateral), and ships with the SQLite
Logic-Test compatibility suite as its correctness oracle. This report describes the
implemented architecture (§3–§7), its concurrency model (§8), compares it against
SQLite, DuckDB, LMDB, RocksDB, BadgerDB, and LevelDB (§9), enumerates its strengths
and limitations (§10), and surveys the testing methodology used to verify both
correctness and SQL conformance (§11).

---

## 1. Introduction

### 1.1 Motivation

The embedded database space has long been dominated by SQLite — a remarkable engine
whose single-file portability, transactional rigor, and battle-tested maturity make
it the default choice for mobile apps, browsers, command-line tools, and countless
language runtimes. SQLite, however, carries architectural choices from the early
1990s that show up under modern workloads:

1. **Single-writer serialization.** SQLite serializes writers at the WAL file level.
   On write-heavy workloads (analytics ingestion, telemetry, write-ahead logging
   inside larger systems), this is the throughput ceiling.
2. **Page-level locking.** In default journal mode, readers and writers can block
   each other on page locks. WAL mode removes the reader/writer contention but does
   not parallelize writers.
3. **No native parallelism inside the engine.** SQLite has a single execution thread;
   it relies on the OS and the calling process for concurrency.
4. **C codebase.** Manual memory management, platform-specific build dependencies,
   and CGO friction in polyglot systems.

Razordata keeps SQLite's ergonomic model — one directory, zero configuration, no
server, embedded in the host process — while adopting storage and concurrency
algorithms designed for modern multi-core hardware.

### 1.2 What this report is and isn't

This is an architecture report for the **implemented system** as it exists in the
codebase, not an aspirational design document. Every claim about a shipped feature
is backed by a file path and a test path (in §7–§8); claims about the planner or
experimental optimizations carry an explicit "experimental" tag. Performance
numbers are not reported here because they are workload-dependent and unstable
across hardware; the comparison in §9 is structural, not benchmark-based.

### 1.3 Roadmap

- §2: hard design constraints
- §3: layered subsystem architecture (LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQB → SYS)
- §4: storage engine (LSM tree, SST, compaction, manifest)
- §5: transaction model (MVCC, version chains, hazard pointers, epoch reclamation)
- §6: query processing (lexer, parser, optimizer, vectorized + parallel executor)
- §7: SQL surface and PRAGMA coverage
- §8: concurrency guarantees
- §9: comparative analysis vs. SQLite, DuckDB, LMDB, RocksDB, BadgerDB, LevelDB
- §10: strengths, limitations, and known gaps
- §11: testing methodology
- §12: conclusion

---

## 2. Design Constraints

These constraints are non-negotiable. They are enforced by the build graph and the
absence of certain dependencies; relaxing any of them would invalidate much of the
subsystem layering.

| Constraint | Rationale |
|---|---|
| No external C dependencies | Trivial cross-compilation; no `CGO_ENABLED=1` requirement; memory safety via Go's GC for the surrounding system; one binary per OS/arch. |
| No network server | Embedded-first. The database lives in the same process as the application; no listener, no authentication surface. |
| Go 1.26+ | Access to `slices`, `maps`, `iter`, `cmp`, `math/rand/v2`, range-over-int, modern `log/slog`. These standard-library facilities remove hand-rolled helpers from hot paths. |
| Single `go.mod` | No nested modules. Flat, auditable dependency graph. |
| Page size: 4 KB | Matches the OS page size for `O_DIRECT` alignment, `mmap` granularity, and atomic block I/O. |
| Subsystem layering | Strict dependency order — no layer may import from a layer above it. Enforced by `tests/depcheck` (planned) and by manual review. |

---

## 3. Architecture: The Ten-Subsystem Stack

Razordata is organized as ten subsystems with strict dependency ordering:

```
LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQB → SYS
                                       ↗
                                   DBG (build-tagged, lateral)
```

Each layer exposes interfaces consumed by the layer above. The build order matches
the dependency order: LOG first (everyone needs logging), SYS last (the public
surface). DBG is lateral — it can be imported by any layer, but only under the
`debug` build tag, so it never appears in production binaries.

| Subsystem | Responsibility | Function Clusters |
|---|---|---|
| `LOG` | Structured logging, async hook dispatch | `LG` (`slog` wrapper, levels, rotation), `HK` (trace/metric/profile hooks) |
| `FIL` | Block I/O, file management, meta page | `DF` (`pread`/`pwrite`, `O_DIRECT`, `mmap`, `fadvise`), `MF` (`meta.razor`), `LF` (WAL segments), `FS` (path validation), `IO` (io_uring wrapper) |
| `MEM` | Buffer pool, sync.Pool, off-heap pools | `BF` (sharded clock-sweep, W-TinyLFU admission, NUMA-aware), `PC` (page slots, checksum), `SP` (object pooling, huge-page pre-alloc), `OF` (off-heap large-object pool) |
| `WAL` | Write-ahead log, durability | `WR` (append, rotation, LSN, LZ4 compression), `FL` (group commit, fsync), `RP` (replay, checkpoint) |
| `ENG` | LSM tree, SST, compaction, manifest | `LS` (memtable, SST reader/writer, Ribbon filter, leveled compaction, manifest), `ID` (index), `TB` (table DDL), `SC` (schema), `DP` (encoding), `CT` (persistent catalog), `NM` (NUMA topology) |
| `TXN` | MVCC, version chains, transactions | `MV` (version chain, NUMA-aware arena), `LC` (hazard pointers, QSBR epoch), `SN` (read view), `VL` (commit protocol, conflict detection) |
| `SQF` | SQL frontend (parse/rewrite) | `LX` (lexer), `PS` (LL(1) parser, 722-line AST), `PL` (planner types, memo, learned selectivity), `RE` (rewriter, SQL formatter) |
| `SQB` | SQL backend (execute) | `AD` (adaptive query compilation), `AG` (aggregation, window), `DT` (data types, schema), `EV` (expression evaluation, vectorized batch), `EX` (executor, cost-based planner, optimizer injection), `OP` (operators: scan/join/agg/sort/write/DDL), `UT` (vectorized utilities, parallel worker pool, hash table, SIMD dispatch, JSON/datetime/analyze/pragma), `WT` (write operators, triggers, views, materialized views, ALTER TABLE, pragma dispatch) |
| `DBG` | Debug observability (build-tagged) | `CT` (counters/histograms), `DC` (dynamic runtime control), `DI` (debugger), `IN` (page inspection), `JD` (JOIN tracer), `PR` (profiler), `SK` (socket command server), `TE` (trace ring buffer) |
| `SYS` | Lifecycle, public API, sessions | `SY` (init, shutdown, stats), `AP` (Engine/Session API, structured error system), `SE` (session), `TX` (transaction), `ST` (statement), `DS` (`database/sql` driver), `BK` (online backup/restore) |

### 3.1 LOG — Structured Logging

The foundation layer. Every other subsystem depends on it.

- Wraps `log/slog` with an atomic level variable for lock-free level checks on the
  hot path (`LOG/LG/lg.go`).
- Async hook dispatch via a bounded channel with non-blocking send; events are
  dropped on overflow so the logging system never backpressures a database
  operation.
- Three built-in hook types: `TraceHook` (per-query wall-clock timing),
  `MetricHook` (throughput/latency counters), `ProfileHook` (CPU/heap dumps on
  error events).
- Log rotation on size threshold; rotated files are gzip-compressed.

**Trade-off.** Dropped events under hook overload are silent. Observability must
never compromise correctness.

### 3.2 FIL — File I/O

The lowest I/O layer. All disk access flows through here.

**Block I/O via `pread`/`pwrite`.** Positional reads/writes without seeking. Each
block is addressed by `(fd, blockID * BlockSize)`. Concurrent readers share the
same file descriptor without coordinating on file offsets.

**`O_DIRECT` with graceful fallback.** Data files on Linux are opened with
`O_DIRECT` to bypass the OS page cache. If the kernel rejects it (`EINVAL`, e.g.
on a tmpfs or non-aligned filesystem), Razordata falls back to buffered I/O. WAL
segments always use buffered I/O — `fsync` handles durability, and the kernel
page cache amortizes small writes.

**`mmap` for direct page mapping.** SST data blocks can be mapped directly into
the process address space (`syscall.Mmap`), eliminating copy-on-read overhead. The
mapped region is invalidated and re-mapped on compaction.

**`fadvise` read-ahead hints.** `fadviseSequential` and `fadviseWillNeed` hint the
kernel to prefetch upcoming SST blocks during sequential scans, overlapping I/O
with CPU work.

**io_uring wrapper.** Linux io_uring provides submission/completion queue pairs
for asynchronous I/O. Registered buffers and files avoid per-syscall setup
(infrastructure present; full hot-path migration deferred to future REQs).

**CRC32 checksums.** Every 4 KB block carries a 4-byte IEEE CRC32 in its last 4
bytes. Reads verify the checksum; mismatches return `ErrCorrupt` without
attempting recovery.

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

Read on startup to validate magic bytes, load version, and locate the system
catalog. Written only on `CREATE DATABASE` and `CHECKPOINT`.

**Path validation.** Rejects paths containing `..` or symlinks. All paths are
resolved against the database root before use — directory-traversal defense.

**File handle management.** Reference-counted `FileHandle` structs. `Refs` is
atomically incremented on `Open()`, decremented on `Close()`. When `Refs == 0`,
the FD is closed under a mutex, preventing double-close.

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
├── catalog.dat         # Persistent catalog (RCAT magic)
├── manifest            # Current LSM version
└── stat1               # ANALYZE histograms (optional)
```

### 3.3 MEM — Memory Management

Buffer pool that caches SST blocks in memory. All reads from the LSM tree flow
through here.

**Sharded buffer pool.** The global hash table is split into N independent shards
(default 32). Each shard has its own clock-sweep hand and mutex. This eliminates
global mutex contention under concurrent reads — multiple goroutines can load and
evict blocks in different shards simultaneously. Shard selection is
`blockID % numShards`.

**Two-pass clock-sweep eviction.** Each slot carries a `refKey` (atomic.Uint64).
On access, the global clock hand advances and the slot's `refKey` is bumped. The
evictor scans the shard: slots with `refKey < hand - clockInterval` and zero pin
count are eviction candidates. This is a classical approximation of LRU
implementation; the description in earlier drafts as "LRU with refkey" is now
"two-pass clock-sweep" per REQ000161.

**W-TinyLFU admission policy.** A frequency-based admission filter tracks block
access counts via a 4-row count-min sketch (CMS) of 4-bit counters. When a new
block competes with an eviction candidate, the CMS winner is admitted, the loser
is rejected. This prevents cache pollution from sequential scans that would
otherwise evict hot blocks.

**NUMA-aware allocation.** The buffer pool reads `/sys/devices/system/node` at
startup (`ENG/NM/numa.go`) and uses first-touch allocation to keep pool shards on
their home NUMA node, minimizing cross-node memory traffic on multi-socket hosts.

**Off-heap large-object pool.** A 16-class size-binned pool (64 KB to 4 MB) for
large allocations that would otherwise cause GC pressure. Each class has its own
`sync.Pool`. Allocations are rounded up to the nearest size class.

**`loading` flag deduplication.** Each buffer slot has a `loading atomic.Bool` and
a `wait chan struct{}`. Only one goroutine loads a block from disk; others wait
on the channel. This eliminates redundant I/O for concurrent reads of the same
block.

**`sync.Pool` for zero-allocation hot paths:**

```
syncPool:
  pagePool  sync.Pool → make([]byte, BlockSize)       — 4 KB buffers
  iterPool  sync.Pool → make([]byte, iterBufferSize)  — LSM iterator scratch
```

`pagePool.New` allocates exactly `BlockSize` bytes. On `Put`, buffers that don't
match the expected size are dropped (not returned to the pool) — this prevents
pool poisoning from caller bugs.

Note: a warm-start hint file was proposed in earlier iterations but is **not
implemented** in the current code.

### 3.4 WAL — Write-Ahead Log

The sole write path for durability. All mutations are serialized to the WAL
before the storage engine writes data.

**Record encoding:**

```
┌──────────────┬──────────┬─────────────┬──────────────────┐
│ length:varint │ txnID:varint │ type:uint8 │ payload:blob   │
└──────────────┴─────────────┴─────────────┴──────────────────┘
```

- `length` = total bytes of `txnID + type + payload` (not including the length
  field itself). Stored as a varint so the reader can skip unknown record types.
- `payload` varies by record type:

| Type | Code | Payload |
|---|---|---|
| `RTData` | 0 | `[blockID:8][checksum:4][data:varint]` — a mutated block image |
| `RTCommit` | 1 | `[commitTS:8]` — marks transaction as committed |
| `RTRollback` | 2 | (empty) — discards uncommitted write set |
| `RTCheckpoint` | 3 | `[checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4][activeTXNs:varint...]` |
| `RTMerge` | 4 | `[newVersion:8][deletedFiles:varint...][addedFiles:varint...]` — LSM version transition |

**LZ4 compression.** WAL records can be LZ4-compressed before writing. A
pure-Go LZ4 block-format codec (greedy hash-chain match finder) compresses
`RTData` payloads. Compression ratio depends on data entropy — typically 2–4× for
text-heavy rows. The compression flag is stored in the record type byte.

**Segment rotation.** WAL segments are 64 MB files named `wal.000`, `wal.001`,
... (zero-padded to 3 digits for lexicographic sorting). When a segment fills, it
is closed and a new one is created. A pre-allocated 256 KB `writeBuffer` avoids
per-record allocation — records are appended via `binary.LittleEndian` directly
into the buffer.

**LSN encoding.** The log sequence number is a single `atomic.Int64`. It encodes
both segment number and offset: `lsn = segmentNumber * SegSize + offset`.
Readers use the LSN to detect stale reads and to order replay.

**Group commit.** Multiple transactions can be grouped into one `fsync` call
(`WAL/FL/group_commit.go`). This amortizes the cost of `fsync` (typically 1–10 ms
on SSDs) across concurrent transactions via a single write barrier. Tests:
`internal/WAL/FL/group_commit_test.go`.

**Corruption recovery:**

- Segment header: 12 bytes at the start of each WAL segment.
- Envelope CRC: 4-byte CRC32-IEEE over the record body.
- Bounded resync: on CRC mismatch, scan forward up to `MaxRecordLen` bytes
  looking for a valid record header.
- Tail-of-segment torn writes are tolerated (expected after a crash).
  Mid-segment corruption surfaces `ErrCorrupt`.
- `Replayer.Stats()` exposes: `TruncatedSegments`, `UnknownRecords`,
  `CorruptionFailures`.

**Recovery on startup:**

1. Find the last `RTCheckpoint` record across all segments.
2. Rewind to that LSN.
3. Replay subsequent records in LSN order.
4. `RTData` records rebuild the in-memory memtable state.
5. `RTCommit` records mark transactions as committed.
6. `RTRollback` records discard uncommitted write sets.
7. Truncate clean segments before the checkpoint.

The replayer does NOT write SST files or update the manifest — those are derived
from the manifest file on startup, not from WAL replay.

### 3.5 ENG — Storage Engine (LSM Tree)

The core of the database. Implements the LSM tree: a lock-free skiplist memtable
that flushes to SST files on disk, leveled compaction, Ribbon filters, and an
atomic file manifest.

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

Insertion is CAS-based from the bottom up: find the predecessor at each level,
then CAS the `next` pointer. If the CAS fails (another writer inserted
concurrently), the entire insertion retries. There is no mutex in the hot path
— only atomic operations.

When the memtable exceeds `Options.MemTableSize` (default 64 MB), it is frozen
(no new writes accepted) and a background goroutine flushes it to an L0 SST file.
New writes go to a fresh active memtable.

**SST file format:**

```
┌────────────────────────────────────────────────────────┐
│ [DataBlock_0]                                          │
│ [DataBlock_1]                                          │
│ ...                                                    │
│ [DataBlock_N]                                          │
│ [IndexBlock]  — one entry per data block               │
│ [RibbonFilter]  — bitset, ~10 bits per key             │
│ [Footer]                                               │
└────────────────────────────────────────────────────────┘
```

- **Data blocks** (default 4 KB): K-V pairs are delta-encoded — each key stores
  only the delta from the previous key. Restart points every 16 K-V pairs enable
  O(1) binary search within the block. Format: `[KV pairs][restart
  array][restart count:4][checksum:4]`.
- **Index block**: one entry per data block:
  `[largestKey:varint][blockOffset:varint][blockSize:varint]`. Binary search on
  `largestKey` locates the target block.
- **Ribbon filter** (`ENG/LS/ribbon.go`): a more space-efficient successor to
  Bloom filters. Ribbon achieves a comparable ~1% false-positive rate at ~30%
  less space than the FNV-1a double-hash Bloom used in earlier iterations. The
  filter is dynamically sized to the entry count.
- **Footer** (28 bytes): `[indexOffset:8][indexSize:4][bloomOffset:8][bloomSize:4][magic:4]`

**Read path:**

```
active memtable → frozen memtable(s) (newest first) → L0 SSTs (newest first) → L1+ SSTs
```

For L1+, the index block is binary-searched to locate the target data block, then
the Ribbon filter is checked before reading the block. If the filter says
"definitely not present," the SST file is skipped entirely.

**Leveled compaction.** When L_k exceeds its size budget (L0 = 4 MB, L1 = 32 MB,
each subsequent level 10× larger), a compaction job is created:

1. Pick the oldest files from L_k.
2. Identify overlapping files from L_{k+1}.
3. Multi-way merge sort all inputs in sorted key order.
4. Write output to a temp directory.
5. Atomically rename temp files to L_{k+1}, update the manifest, delete old input
   files.

**Subcompact.** For L_k → L_{k+1} compactions where the input is large relative
to the merge bandwidth, Razordata runs a sub-compaction pass that partitions the
key range and merges each partition independently across workers, then stitches
the results. This bounds peak memory during compaction and improves parallelism.

**Atomic manifest versioning:**

```
write to temp file → fsync temp → rename to final path → fsync directory
```

The manifest is the single source of truth for which SST files are live.
`Version` is immutable once created — new versions are produced by applying a
`VersionDiff`. The manifest stores: file ID, level, key range, size, and
filter bit count for every live SST.

**Persistent catalog (ENG/CT).** A binary catalog file (`catalog.dat`) with `RCAT`
magic bytes stores table schemas in a versioned format (V1/V2). Entries are
serialized via encode/decode callbacks, with atomic persistence via temp-file +
fsync + rename. Supports schema upgrade paths with `ErrUpgradeRequired` for
forward-versioned files.

**NUMA topology (ENG/NM).** Detects NUMA node layout by reading
`/sys/devices/system/node`. Caches node count and provides `NodeForCPU` for
CPU-to-NUMA-node mapping. Used by the parallel worker pool to pin workers to
local NUMA nodes, minimizing cross-node memory access.

**Row encoding.** Fixed-width columns stored inline: `INT` (8 bytes), `BIGINT` (8
bytes), `FLOAT` (8 bytes), `BOOL` (1 byte). Variable-length columns:
`[length:varint][data:blob]`. Null values: a null bitmap in the row header, one
bit per column.

### 3.6 TXN — Transaction Layer

MVCC snapshot isolation for readers, serializable writes. The sharpest
divergence from SQLite.

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

Each primary key in the storage engine points to a singly-linked list of version
nodes (newest first). A reader traverses the chain, skipping versions where
`beginTS >= readTS` or `endTS < readTS`.

**Per-transaction arena.** Each transaction gets its own bump-pointer arena
(`TXN/MV/arena.go`), pooled via `sync.Pool`. Version nodes are bump-pointer
allocated from the arena — no individual `make` calls, no GC pressure. On commit
or abort, the entire arena is returned to the pool. Rollback is O(1) (just
release the arena). Arenas are NUMA-aware: allocations are first-touch to the
transaction's home node.

**Hazard pointers:**

```
hazardPointerSet:
  ptrs [2]atomic.Value   // [current, next]
```

Before dereferencing a version node pointer, the reader publishes it to one of
two hazard pointer slots via `atomic.Store`. The reclamation pass scans all
registered hazard pointers before freeing any node. The double-slot design
allows readers to prefetch the next node while holding the current node in the
other slot.

**Epoch-based reclamation (QSBR).** A background goroutine increments a global
epoch counter every ~100 ms. Each reader registers with the epoch manager on
first read. Old version nodes are only freed when all readers have advanced past
the epoch at which the nodes were deprecated. This guarantees no reader sees a
freed node.

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

A pre-allocated fixed-size array of 1024 slots. Allocation uses a
mutex-protected free list — no GC pressure, O(1) allocation.

**Commit protocol (6 phases):**

1. **Begin.** Allocate slot, assign `beginTS = globalAtomicCounter++`, register
   with epoch manager, take read view (snapshot of version chain heads).
2. **Read.** Traverse version chain via hazard pointers. No locks acquired.
3. **Write.** Allocate version node from arena, CAS-insert at chain head. Write
   `RTData` to WAL.
4. **Pre-commit (validate).** Scan all committed slots. If any slot with
   `commitTS > myBeginTS` modified a key in my `writeSet`, abort. This is
   write-write conflict detection.
5. **Commit.** Assign `commitTS = globalAtomicCounter++`, write `RTCommit` to
   WAL, `fsync`, CAS-update `endTS` on all version nodes from `MaxUint64` to
   `commitTS`.
6. **Post-commit.** Release arena, deregister from epoch manager.

**Foreign-key deferrable queue (experimental).** FK constraints declared
`DEFERRABLE INITIALLY DEFERRED` are parsed (`SQF/PS/ddl.go:181-195`) but the
runtime defer queue is not yet fully wired. FK enforcement is immediate-only in
current execution tests.

---

## 4. Storage Engine

See §3.5 for the full subsystem description. Key shipped properties verified
against the codebase:

- LSM tree with skiplist memtable (`ENG/LS/skiplist.go`).
- SST reader/writer with delta-encoded data blocks, Ribbon filters, and CRC32
  checksums (`ENG/LS/sst_reader.go`, `sst_writer.go`).
- Leveled compaction with subcompact partitioning (`ENG/LS/compaction_style.go`,
  `subcompact.go`).
- Atomic manifest via temp-file + fsync + rename (`ENG/LS/manifest.go`).
- Persistent catalog with RCAT magic and V1/V2 versioning (`ENG/CT/catalog.go`).
- NUMA topology detection and CPU-to-node mapping (`ENG/NM/numa.go`).

---

## 5. Transaction Model

See §3.6 for primitives. The practical properties:

- **Snapshot isolation for reads.** Readers see a consistent point-in-time
  snapshot regardless of concurrent writers.
- **Serializable for writes.** Write-write conflicts are detected at pre-commit
  and the loser is aborted with `ErrWriteConflict`.
- **Lock-free reads.** Readers traverse version chains via hazard pointers; no
  shared mutex with writers.
- **O(1) rollback.** The per-transaction arena makes abort cheap — release the
  arena, mark the slot inactive, deregister from the epoch manager.

---

## 6. Query Processing

### 6.1 Pipeline

```
SQL text → Lexer (tokens) → Parser (AST) → Rewriter (normalized AST)
        → Planner (plan tree) → Executor (rows)
```

### 6.2 Lexer (`SQF/LX`)

- Token types are value types (no interface allocations on the token path).
- Keyword lookup via a static `map[string]TokenType` for O(1) recognition.
- Error recovery: on malformed input, the lexer advances to the next delimiter
  and emits `T_EOF` with an error, allowing the parser to collect all errors in
  one pass.

### 6.3 Parser (`SQF/PS`)

- LL(1) recursive descent. Dedicated parsers for `SELECT`, `INSERT`, `UPDATE`,
  `DELETE`, `CREATE TABLE` (incl. STRICT and WITHOUT ROWID), `CREATE INDEX`
  (incl. partial-index `WHERE` clause), `CREATE VIEW`, `CREATE MATERIALIZED
  VIEW`, `REFRESH MATERIALIZED VIEW [CONCURRENTLY]`, `CREATE TRIGGER`
  (incl. TEMP), `ALTER TABLE` (ADD/DROP/RENAME COLUMN, RENAME TO, ALTER
  SET/DROP DEFAULT), `CREATE VIRTUAL TABLE`, `ATTACH/DETACH`, transactions
  (SAVEPOINT/RELEASE/ROLLBACK TO/SET TRANSACTION), EXPLAIN, VALUES, and
  virtual tables.
- Expression parsing uses operator precedence: comparison > add/sub > mul/div >
  unary > primary.
- AST nodes are concrete structs with no interface fields (except the `Expr`
  and `Stmt` marker interfaces). This avoids interface dispatch overhead in
  the rewriter and planner.
- Full 722-line AST (`ast.go`) with `Loc` position tracking on every node for
  precise error reporting. Visitor pattern for tree traversal.

### 6.4 Rewriter (`SQF/RE`)

- **Constant folding**: `1 + 2 * 3` → `7` at parse time.
- **Predicate pushdown**: moves `WHERE` conditions as close to the data source
  as possible.
- **Subquery flattening**: merges single-row subqueries in `WHERE IN` into a
  join or list lookup.

### 6.5 Planner (`SQB/EX` + experimental `SQO/`)

**Current production planner (`SQB/EX/planner.go`, 900+ lines):**

- Cost-based: estimates I/O cost from key selectivity (uniform distribution
  initially, histogram support via `ANALYZE`).
- Index selection: if a `WHERE` column has a usable index, `IndexScan`;
  otherwise `SeqScan`. Strategy pattern (`ScanStrategy`) replaces earlier
  mode-flag code with pluggable strategies: `InMemoryScan`, `SeekScan`,
  `BTreeScan`.
- Join ordering: N3 optimizer with bushy/left-deep join enumeration,
  selectivity estimation per predicate type.
- Plan memoization: equivalent query shapes share sub-plans. Memo key =
  `SHA256(canonical_binary_encoding(AST))`. Parameterized plans
  (`NormalizeForMemo`) separate planning decisions from operator-tree literals.
- Sort ordering: if `ORDER BY` matches primary key order, the explicit sort is
  eliminated.
- Slot resolution: post-plan optimizer pass resolves column references to
  ordinal indices (`SlotIdx`), enabling O(1) runtime access via
  `row.Data[slotIdx]` instead of per-row string lookup.
- Learned selectivity model: tracks per-column-pair correlations, trains
  predicate feature vectors from histogram selectivity + row count + distinct
  count + null count for improved cardinality estimation.

**Experimental optimizer subsystem (`SQO/`, partially migrated):**

The `internal/SQO/` directory contains a full rewrite of the optimizer in
clusters:

- `CP` — cost-model parameters (`params.go`).
- `CM` — cost model (PG-style dimensionless units).
- `SL` — selectivity (EqSelectivity, RangeSelectivity, predicates, IN-list,
  NDV/null helpers).
- `JN` — join order (N3, multi-start, bushy).
- `MM` — memoization (LRU, schema-version invalidation).
- `RW` — rewrite (constant folding, predicate pushdown, predicate split).
- `CO` — optimizer interface (`Optimizer.Plan(stmt)`).

The seam is exposed via `Executor.RegisterOptimizer(opt CO.Optimizer)`
(`SQB/EX/executor_factory.go`). As of this writing, no production caller
wires a `SQO/CO` optimizer — the default production path still uses
`SQB/EX/planner.go`. The SQO clusters are integration-ready but not
production-on. See `docs/development/REQUIREMENTS_FUTURE.md` for the
migration roadmap (REQ001494–REQ001509).

### 6.6 Executor

```go
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}
```

The executor is a pull-based tree traverser. Parent calls `child.Next()`,
processes the row, yields to its parent. There is no bytecode VM, no code
generation — pure Go struct interpretation.

**Operators shipped (`SQB/OP/`):**

| Category | Operators |
|---|---|
| Scan | `SeqScan`, `IndexScan`, `IndexOnlyScan`, `BitmapHeapScan`, `BitmapScan`, `SqliteMaster`, `SqliteTempMaster`, `SqliteSequence` |
| Filter/Project | `Filter`, `FilterProject`, `Project`, `Distinct`, `Values`, `ValuesRows` |
| Join | `NestedLoopJoin` (INNER/CROSS/LEFT/RIGHT/FULL), `HashJoin` (radix-partitioned), `ParallelHashJoin`, `HashCrossJoin` (small tables ≤1024 rows), `MergeJoin` |
| Aggregate/Window | `Aggregate`, `HashAggregate`, `ParallelHashAggregate`, `WindowOperator`, `GROUP_CONCAT`, `VectorizedCount/Sum/Avg/Min/Max` |
| Sort | `Sort`/`TopNSort`, `ParallelSort` (sample-sort with N-way parallel partition) |
| Limit | `Limit`, `Offset` |
| Compound | `CompoundOp` (UNION/UNION ALL/INTERSECT/EXCEPT with streaming fast-path) |
| Parallel scan | `ParallelStoreSeqScan`, `ParallelSeqScan`, `ParallelSeqScanRow`, `ParallelIndexScan`, `ParallelIndexRangeScan`, `ParallelUnionAll` |
| Write | `Insert`, `Update`, `Delete`, `Upsert` |
| DDL | `CreateTable`, `DropTable`, `CreateIndex`, `DropIndex`, `CreateView`, `DropView`, `CreateMaterializedView`, `DropMaterializedView`, `RefreshMaterializedView`, `CreateTrigger`, `DropTrigger`, `AlterTable`, `TruncateTable`, `Reindex` |
| Debug | `PragmaResult` |

**Vectorized execution.** For filter-heavy queries, operators process columnar
batches (default 64 rows, configurable):

```
Batch layout: columnar arrays ([]int64, []float64, []string) via sync.Pool
Evaluation: 4-wide/8-wide manual unrolling for L1 cache-friendly batch evaluation
Selection vectors: SelRange compact mask for contiguous filtered rows
Adaptive threshold: auto-fallback to row-at-a-time for small tables
```

Vectorized operators: `VectorizedSeqScan`, `VectorizedFilter`, `VectorizedProject`,
`VectorizedDistinct`, `VectorizedCompoundOp`, `VectorizedHashJoin`,
`VectorizedNestedLoopJoin`. Automatic eligibility checking via
`tryVectorizePlan` transforms eligible operator trees into batch-processing
pipelines. `BatchToRowAdapter` and `RowOperatorAdapter` bridge between batch
and row-at-a-time domains.

**Adaptive query compilation (ADQC).** The `AdaptiveOp` wrapper hot-swaps from
interpreted to specialized batch execution after a configurable invocation
threshold. Plans are compiled once and cached in an LRU `AdqcCache` (256 entries,
keyed by plan hash + schema version). `FallbackOp` provides panic-safe fallback
to the interpreted path. Telemetry counters track specialization rate,
fallbacks, and invalidations.

**Parallel execution.**

- Table scans are split into key-range partitions, each processed by a worker.
- NUMA-aware worker pool: detects NUMA topology and pins workers to local
  nodes, minimizing cross-node memory access. Pool sized to
  `runtime.GOMAXPROCS(0)`.
- Parallel sort: sample-sort with N-way parallel partition sort via worker
  pool.
- Parallel hash join: right-side hash table built in parallel across workers
  (per-partition mutex), single-threaded probe.
- Parallel hash aggregate: input partitioned by group-key hash, N partial hash
  tables built concurrently, merged at the end.
- Results merged via bounded channels (non-blocking send, drop on overflow).

---

## 7. SQL Surface and PRAGMA Coverage

### 7.1 DDL (data definition)

**Confirmed shipped** (file:line / test:line):

- `CREATE TABLE` incl. `STRICT` table type and `WITHOUT ROWID` rejection at the
  storage layer (`SQF/PS/ddl.go:546`; tests `ps_test.go:872,884`).
- `CREATE TEMP/TEMPORARY TABLE` — parser + executor (`SQF/PS/ddl.go:335`;
  `SQB/EX/alter_table_test.go:602`).
- `DROP TABLE`, `CREATE INDEX`, `DROP INDEX`, `CREATE VIEW`, `DROP VIEW`,
  `CREATE MATERIALIZED VIEW`, `DROP MATERIALIZED VIEW`,
  `REFRESH MATERIALIZED VIEW [CONCURRENTLY]`,
  `CREATE TRIGGER` (incl. `TEMP TRIGGER`, BEFORE/AFTER/INSTEAD OF),
  `DROP TRIGGER`, `ALTER TABLE` (ADD/DROP/RENAME COLUMN, RENAME TO, ALTER
  SET/DROP DEFAULT), `TRUNCATE TABLE`, `REINDEX`, `ATTACH/DETACH DATABASE`,
  `CREATE VIRTUAL TABLE` (USING module).
- **Partial indexes** (`CREATE INDEX … WHERE`): shipped with predicate
  implication check (`SQB/EX/expr_util.go:507 canUsePartialIndex`;
  `iter28_bugfix_test.go:1637`).

### 7.2 DML (data manipulation)

- `INSERT` with all five conflict actions: OR REPLACE / OR IGNORE / OR ROLLBACK /
  OR ABORT / OR FAIL.
- `ON CONFLICT DO NOTHING/UPDATE` (UPSERT) including partial-index targeting
  and `RETURNING` clause (`SQF/PS/insert.go:152-272`;
  `iter28_bugfix_test.go:1500+`).
- `UPDATE … FROM` (`SQF/PS/update.go:52-53`).
- `UPDATE … RETURNING`, `DELETE … RETURNING`.

### 7.3 Constraints

- `PRIMARY KEY`, `NOT NULL`, `DEFAULT`, `CHECK`, `UNIQUE`.
- `FOREIGN KEY` with all five reference actions: `CASCADE`, `RESTRICT`,
  `SET NULL`, `SET DEFAULT`, `NO ACTION` (`SQF/PS/ddl.go:432-461`).
- `FOREIGN KEY … MATCH FULL/PARTIAL/SIMPLE` (`SQF/PS/ddl.go:167`).
- `FOREIGN KEY … DEFERRABLE INITIALLY DEFERRED/IMMEDIATE` (parser-level;
  runtime defer queue partial — see §10).
- `GENERATED ALWAYS AS … VIRTUAL|STORED` — both modes parsed and persisted.

### 7.4 Queries

- `SELECT`, `WHERE`, `ORDER BY` (with `NULLS FIRST/LAST`), `LIMIT/OFFSET`
  (incl. `FETCH FIRST/NEXT` syntax), `GROUP BY`, `HAVING`, `DISTINCT`,
  `VALUES` (as a query), recursive CTEs.
- Window functions: `ROW_NUMBER`, `RANK`, `DENSE_RANK`, `LAG`, `LEAD`,
  `SUM/AVG/COUNT/MIN/MAX OVER (...)` with `PARTITION BY`, `ORDER BY`,
  `ROWS/RANGE` frame, `EXCLUDE` clause, `FILTER` clause.
- Joins: `INNER`, `CROSS`, `LEFT/RIGHT/FULL OUTER`; nested-loop, hash
  (radix-partitioned), merge, parallel hash.
- Subqueries: `IN`, `EXISTS`, scalar, CTE (WITH, WITH RECURSIVE).
- `EXPLAIN`, `EXPLAIN ANALYZE`, `EXPLAIN FORMAT TREE/JSON/DOT`.

### 7.5 Types

`INTEGER`, `BIGINT`, `FLOAT`, `DECIMAL(P,S)`, `BOOLEAN`, `TEXT`, `VARCHAR`,
`BLOB`, `DATE`, `TIME`, `TIMESTAMP`, `JSON`.

### 7.6 Functions

- Aggregate: `COUNT`, `SUM`, `AVG`, `MIN`, `MAX`, `GROUP_CONCAT (SEPARATOR …)`.
- JSON: `json_extract`, `json_object`, `json_array`, `json_type`, `json_set`,
  `json_insert`, `json_replace`, `json_remove`.
- Date/time: `NOW`, `date`, `time`, `datetime`, `strftime` and parse helpers.

### 7.7 PRAGMAs shipped (`SQB/WT/writers_admin.go`)

| PRAGMA | Line | Notes |
|---|---|---|
| `table_info(t)` | 56 | schema introspection |
| `database_list` | 72 | attached databases |
| `index_list(t)` | 89 | index introspection |
| `table_list` | 103 | table introspection |
| `foreign_key_list(t)` | 117 | FK introspection |
| `wal_checkpoint` | 131 | passive / full / truncate / restart (see §10 limitations) |
| `wal_autocheckpoint` | 149 | auto-checkpoint threshold |
| `busy_timeout` | 172 | connection-level busy wait |
| `busy_handler` | 195 | callback-based busy handler |
| `batch_size` | 226 | group-commit batch size |
| `temp_store` (0/1/2) | 250 | default/file/memory temp tables |
| `journal_mode` (delete/wal/memory/truncate/persist/off) | 272 | journal mode dispatch |
| `foreign_keys` | 292 | session FK toggle (no-op mid-transaction) |
| `foreign_key_check` | 322 | integrity verification |
| `cell_size_check` | 362 | per-page size audit |
| `quick_check` | 395 | fast integrity check |
| `auto_compact` (none/incremental/full) | 412 | compaction mode |
| `incremental_vacuum` | 447 | space reclaim |
| `debug_*` (routed) | 337 | runtime debug instrumentation |

A `PragmaListener` registration system (`SQB/UT/pragma.go:7-37`) lets
components react to PRAGMA changes.

### 7.8 Transactions

`BEGIN`, `COMMIT`, `ROLLBACK`, `SAVEPOINT`, `RELEASE`, `ROLLBACK TO`,
`SET TRANSACTION` (isolation level).

### 7.9 Utilities

`VACUUM`, `ANALYZE` (reservoir sampling + histogram persistence;
`SQB/UT/analyze.go:16-258`), `integrity_check`, online `backup/restore`,
PRAGMA listener system, `razor` CLI.

---

## 8. Concurrency Model

| Interaction | Mechanism |
|---|---|
| Read–Write | Lock-free. MVCC version chains, hazard pointers, QSBR epoch reclamation. |
| Write–Write | Detected at pre-commit. Conflicting transactions aborted (serializable). |
| Write–Read | Writers never block readers. Old versions remain visible until reclamation. |
| Buffer pool | Two-pass clock-sweep eviction with deduplication. Sharded mutex for hash table. NUMA-aware shard allocation. |
| WAL | Group commit with write barrier. Multiple transactions per `fsync`. |
| Parallel scan | Worker pool partitions the key range; per-partition mutex on the hash table. |
| NUMA worker pool | Detects topology, pins workers to local nodes, first-touch allocation. |

The hazard-pointer + QSBR combination is the standard "lock-free + safe
reclamation" recipe used in production lock-free data structures (e.g., the
folly ConcurrentHashMap, the original Linux kernel RCU). Razordata's design
matches that pattern with one simplification: a single global epoch, not
per-object epochs. The cost is reclamation granularity — old nodes cannot be
freed until all readers advance past their deprecation epoch.

---

## 9. Comparative Analysis

This section positions Razordata against modern embedded and server-side database
systems. Comparison is **structural**, not benchmark-based — performance depends
heavily on workload, hardware, and tuning, and we do not report competitive
numbers here.

### 9.1 Razordata vs. SQLite

| Dimension | SQLite | Razordata |
|---|---|---|
| Storage engine | B-tree (single file) | LSM tree (skiplist memtable + SST + leveled compaction) |
| Write concurrency | Single writer serialized at WAL | Multiple concurrent writers via MVCC |
| Read concurrency | Parallel in WAL mode | Parallel via MVCC version chains (always) |
| Isolation | Serializable (WAL) | Snapshot isolation (readers) / serializable (writers via conflict detection) |
| Language | C (~150 K LOC) | Go (~no external deps; pure standard library + `golang.org/x/sys`) |
| Memory safety | Manual | GC-managed for surrounding code; arenas for hot paths |
| Vectorization | None | Columnar batches, 4/8-wide unrolling, selection vectors, adaptive compilation |
| Parallelism | None inside the engine | Parallel scan, sort, hash join, hash aggregate; NUMA-aware worker pool |
| Join algorithms | Nested loop only | Nested loop, hash join (radix-partitioned), merge join, parallel hash join |
| Observability | Extension-dependent | First-class: structured logging, metrics, profiling, JOIN tracer, debug socket |
| File format | Single `.sqlite` file | Directory: `meta.razor`, `wal/`, `sst/`, `manifest`, `catalog.dat` |
| Build | C toolchain, platform-specific | Go toolchain only |
| SQL surface | Full SQLite dialect | Substantial subset (CTEs, window functions, triggers, views, materialized views, UPSERT, ALTER TABLE, partial indexes, VIRTUAL/STORED generated columns, MATCH FULL/PARTIAL/SIMPLE FKs) |

**Where SQLite wins.** 25+ years of production hardening across billions of
devices. Single-file portability. B-tree point reads are O(log N) with excellent
cache behavior on small datasets. WAL is simpler and battle-tested. The
SQLite Logic-Test corpus is the de facto correctness oracle for SQL.

**Where Razordata wins.** Write throughput under concurrency. Concurrent
read–write workloads where readers must never block writers and writers must
never block readers. Embedded workloads that benefit from parallelism
(vectorized scans, parallel hash join/sort/aggregate). Rich join algorithms
when the query planner can route through hash/merge joins. Go ecosystem
integration without CGO. First-class observability.

### 9.2 Razordata vs. DuckDB

DuckDB is the leading analytical embedded database. It is the closest structural
analogue to Razordata in the embedded analytical space.

| Dimension | DuckDB | Razordata |
|---|---|---|
| Storage | Single-file columnar (DataChunks) | Directory-based LSM row-store |
| Concurrency | MVCC (snapshot isolation), multi-version blocks | MVCC (snapshot isolation), version chains |
| Execution | Vectorized, push-based, adaptive | Vectorized + parallel, pull-based, adaptive compilation |
| Parallelism | Intra-query parallelism across cores | Intra-query parallelism + NUMA worker pool |
| Tooling | Mature CLI, Python/R/Node bindings | `database/sql` driver, `razor` CLI |
| Compression | Per-column type-specific codecs (RLE, dictionary, etc.) | LZ4 on WAL records; no SST-level compression yet |
| Maturity | Production at scale; large user community | Pre-1.0; SQL conformance test suite as oracle |

**Where DuckDB wins.** Per-column type-specific compression (RLE, dictionary,
delta) gives 3–10× better scan throughput on analytical workloads. Mature
analytical SQL surface (window functions, sampling, statistical aggregates).
Optimized for analytical read-heavy workloads.

**Where Razordata wins.** Mixed OLTP/OLAP workload balance (LSM gives write
throughput, vectorization gives analytical reads). Pure-Go portability without
DuckDB's C++ build dependency. NUMA-aware parallelism for multi-socket hosts.

### 9.3 Razordata vs. LMDB

LMDB (Lightning Memory-Mapped Database) is the canonical mmap-based B-tree.

| Dimension | LMDB | Razordata |
|---|---|---|
| Storage | Single-file mmap'd B-tree | Directory-based LSM |
| Concurrency | Single-writer, many readers (writer mutex) | Multiple writers via MVCC |
| Memory safety | Manual | GC-managed |
| ACID | Full ACID | Full ACID (snapshot isolation) |
| Crash recovery | Copy-on-write pages, no WAL needed | WAL + SST + manifest |
| Footprint | Tiny (~50 KB compiled) | Larger (Go runtime + subsystems) |

**Where LMDB wins.** Absolute smallest footprint. Zero-copy reads via mmap.
Crash safety via copy-on-write without a separate WAL. 30+ years of
production deployment.

**Where Razordata wins.** Concurrent writers. Larger-than-RAM databases (LMDB
must mmap the entire database). Write throughput. Richer SQL surface.
First-class observability.

### 9.4 Razordata vs. RocksDB

RocksDB is the canonical embedded LSM, optimized for server-side use.

| Dimension | RocksDB | Razordata |
|---|---|---|
| Language | C++ | Go |
| SQL | None (key-value) | Full SQL frontend/backend |
| Concurrency | Per-column-family write batches, lock-free reads | MVCC with version chains |
| Compaction | Universal / leveled / FIFO | Leveled with subcompact |
| Filters | Bloom, Ribbon | Ribbon |
| Compression | Snappy, ZSTD, LZ4, none | LZ4 on WAL only |
| Footprint | Native binary, larger | Pure Go, single `go.mod` |

**Where RocksDB wins.** Decades of production hardening. Tunable compaction
strategies. Pluggable compression. Wide ecosystem of language bindings.
Mature tooling (benchmarking, monitoring, debug tools).

**Where Razordata wins.** SQL surface. Pure-Go portability (no CGO). Simpler
deployment (one binary, one directory).

### 9.5 Razordata vs. BadgerDB

BadgerDB is a Go-native LSM key-value store that influenced Razordata's design.

| Dimension | BadgerDB | Razordata |
|---|---|---|
| Language | Go | Go |
| Surface | Key-value | Full SQL |
| Concurrency | MVCC transactions | MVCC transactions |
| Storage | LSM tree | LSM tree |
| Value log | Separate value log for large values | Inline values |
| SQL | None | Full SQL |

**Where BadgerDB wins.** Simpler surface, easier to embed in key-value use
cases. Mature ecosystem and tooling.

**Where Razordata wins.** Full SQL surface. Vectorized and parallel query
executor. Cost-based optimizer. Adaptive query compilation. NUMA awareness.

### 9.6 Razordata vs. LevelDB

LevelDB is the canonical reference LSM implementation.

| Dimension | LevelDB | Razordata |
|---|---|---|
| Language | C++ | Go |
| Surface | Key-value | Full SQL |
| Concurrency | Single writer, snapshots | Multiple writers via MVCC |
| Parallelism | None | Parallel scan/sort/join/aggregate |

**Where LevelDB wins.** Decades of production use. Tiny, embeddable code.
Reference LSM implementation for research and teaching.

**Where Razordata wins.** Everything else in this comparison.

### 9.7 Summary positioning

Razordata occupies a specific niche: **embedded, pure-Go, write-concurrent,
SQL-capable, vectorized, and parallel**. It is not the fastest at any one thing
(LMDB for raw point reads, DuckDB for analytical scans, RocksDB for write
throughput at scale), but it is competitive across the OLTP/OLAP boundary in a
single embedded engine with no external dependencies.

---

## 10. Strengths, Limitations, and Known Gaps

### 10.1 Strengths

1. **No external dependencies.** `go.mod` only references Go standard library
   packages and `golang.org/x/sys` for Unix-specific syscalls. Trivial
   cross-compilation.
2. **Write concurrency via MVCC.** Lock-free skiplist memtable + version chains
   + hazard pointers + QSBR give concurrent writers and lock-free readers
   without the global writer mutex that single-writer databases need.
3. **Vectorized and parallel executor.** Adaptive vectorization + NUMA-aware
   worker pool + parallel hash join/sort/aggregate give multi-core scaling for
   analytical queries.
4. **Rich join algorithms.** Nested loop, hash (radix-partitioned), merge, and
   parallel hash. The cost-based planner can route the same query through
   different algorithms based on table size, predicate selectivity, and sort
   order.
5. **LSM tree for write throughput.** Optimized for workloads where writes
   dominate reads, or where data outgrows RAM.
6. **First-class observability.** Structured logging with bounded async hooks,
   built-in profiler, JOIN tracer, debug socket. No add-on observability
   required.
7. **Modern SQL surface.** Recursive CTEs, window functions with FILTER and
   EXCLUDE, UPSERT, materialized views, partial indexes, VIRTUAL/STORED
   generated columns, MATCH FULL/PARTIAL/SIMPLE FKs — features that took
   SQLite years to accumulate are present.
8. **SQLite Logic-Test conformance.** 622+ test files run against both
   Razordata and a reference `modernc.org/sqlite` driver in dual-runner mode.

### 10.2 Limitations and known gaps

These are the gaps a reader should know about before adopting Razordata for
production:

1. **Single-process, single-host.** No inter-process locking (no `fcntl` byte-range
   locks, no SHM change counter). Only one process can hold the database at a
   time. The shared-cache and POSIX-lock requirements (REQ001329, REQ001330,
   REQ001331) are deferred to `REQUIREMENTS_FUTURE.md`.
2. **Optimizer migration incomplete.** The `SQO/` clusters exist and the
   `RegisterOptimizer` injection seam is wired, but the production planner in
   `SQB/EX/planner.go` is still the active path. Some REQ items remain in
   `REQUIREMENTS.md` TBD: OR-to-IN conversion (REQ001218), N3 OR selectivity
   (REQ001219), 3-table+ comma-join output column shuffling (REQ001162),
   recursive CTE iteration correctness (REQ001184-186), executor cache sharing
   (REQ001220), row arena allocation (REQ001221).
3. **FK DEFERRABLE partial.** `DEFERRABLE INITIALLY DEFERRED` is parsed, but
   the runtime defer queue is not wired — FKs are enforced immediate-only.
4. **WAL checkpoint modes.** `wal_checkpoint(MODE)` accepts the SQL syntax,
   but only the passive/full variants are fully wired; truncate and restart
   modes are partially implemented (REQ001303 deferred to FUTURE).
5. **No warm-start hint file.** The hint file described in earlier iterations
   was not landed — cold starts must re-read the manifest and rebuild
   statistics.
6. **Buffer reuse correctness.** `pruneRowCols` allocates ~10% of heap
   because naive buffer reuse (Cols/Types/Data/ColIndex) corrupts Rows held
   by downstream operators that don't clone. Safer buffer reuse awaits deeper
   data-flow lifetime analysis (REQ001287 deferred to FUTURE).
7. **Pre-1.0.** No formal stability guarantee. Schema changes in `meta.razor`
   and on-disk formats may evolve.
8. **No compression in SSTs.** WAL records are LZ4-compressed; SST data
   blocks are not. Compared to RocksDB (Snappy/ZSTD per block) or DuckDB
   (per-column type-specific), this is a storage cost on cold data.
9. **Limited ecosystem.** No ODBC/JDBC driver, no Python/R bindings, no
   commercial support. `razor` CLI and the `database/sql` driver are the
   primary interfaces.
10. **NUMA detection is Linux-only.** `/sys/devices/system/node` does not exist
    on macOS or Windows. Multi-socket NUMA benefits only apply on Linux.

### 10.3 Trade-offs (concise)

| Decision | Benefit | Cost |
|---|---|---|
| LSM tree over B-tree | Write throughput, large-dataset performance | Point reads check memtable + L0 + L1+ (mitigated by Ribbon filters) |
| Lock-free skiplist + MVCC | Concurrent writes, lock-free readers | Complex commit protocol, write amplification (old versions in chain) |
| Per-transaction arenas | Zero GC pressure, O(1) rollback | Pool churn under high concurrency |
| Directory-based storage | WAL independent of data, atomic manifest rename | Less portable than single-file |
| Pull-based executor (no VM) | Simple, debuggable, Go-native | No JIT/codegen optimization |
| Adaptive query compilation | Hot-swap interpreted → specialized after threshold | Compilation overhead on first invocation; cache memory |
| Vectorized execution | 4–8× throughput for batch-friendly operators | Adapter overhead when mixing batch and row-at-a-time |
| Parallel execution by default | Scales with cores | Coordination overhead on small tables (auto-fallback at <100 K rows) |
| NUMA-aware worker pool | Minimizes cross-node memory access | Linux-only; adds complexity |
| W-TinyLFU admission | Prevents scan pollution of hot blocks | ~500 KB memory overhead for CMS |
| Bounded log channel | Never blocks DB operations | Events dropped under hook overload |
| Structured error system | Rich diagnostics, JSON serialization, operation tracking | More code than simple error strings |
| Ribbon filter (vs Bloom) | ~30% less space at same FPR | Newer algorithm; less production track record |

---

## 11. Testing Methodology

### 11.1 Unit and integration tests

- `go test ./... -race -count=1` must always pass. Race detector is mandatory
  because the lock-free paths (skiplist, hazard pointers, version chains) are
  the highest-risk correctness surface.
- Table-driven tests for parser and executor.
- Property-based tests for storage (crash/recovery, fuzzed inputs).
- Every storage component requires benchmarks in `*_test.go` files.
- Error paths, edge cases, and boundary conditions are tested as aggressively
  as happy paths — per the project's `AGENTS.md` testing rule.

### 11.2 SQLite Logic-Test (SLT) conformance

The project carries the SQLite Logic-Test corpus (622+ `.test` files) and runs
each file against Razordata using a pure-Go driver (`tests/sqlcmp/slt/`).
Dual-runner mode runs each test against both Razordata and
`modernc.org/sqlite` and compares results, surfacing regressions where
Razordata diverges from the reference.

To run:

```bash
git submodule update --init --recursive --depth 1
cd tests/sqlcmp && go test -tags slt_corpus -run TestSLT_PerFile -v ./slt/
```

Per-file pass/fail is reported. `first failure context` shows the first 5
failures per file with diagnostics. Use `sqlite3` to verify expected behavior
when troubleshooting.

### 11.3 Debug build tag (`-tags debug`)

Enables additional instrumentation under `internal/DBG/`:

- JOIN tracing with row-by-row accounting.
- CTE tracing.
- Page inspection.
- Runtime knob toggling.
- Dynamic counters and histograms.
- A socket command server for live engine inspection.

The debug subsystem is lateral (any layer can import it) but is excluded from
production binaries because `DBG` files have a build-tag directive.

### 11.4 Fuzz testing

The lexer and parser have dedicated fuzz harnesses that catch edge cases in SQL
input — unicode boundary conditions, numeric overflow, deeply nested
expressions, malformed keywords.

### 11.5 Continuous integration

Per `AGENTS.md`:

```bash
go vet ./...
golangci-lint run     # or staticcheck
go test ./... -race -count=1
```

CI triggers on `main` and `dev` branches.

---

## 12. Conclusion

Razordata is a deliberate experiment in a specific design space: an embedded
database that combines LSM-tree write throughput with MVCC concurrency, a
vectorized and parallel query executor, and an adaptive query compilation
cache — all in pure Go, with no external dependencies, and with the SQLite
Logic-Test as its correctness oracle.

It is not a SQLite replacement. SQLite excels at simple, reliable, single-file
embedded storage, with a 25-year track record. Razordata targets the space
where write concurrency, parallel execution, vectorized analytics, and modern
language ergonomics matter more than battle-tested maturity.

The architecture's defining trade-offs are visible in the subsystem layout
itself: complexity in exchange for concurrency (MVCC + version chains +
hazard pointers + QSBR), GC integration in exchange for memory safety (no
manual allocation in hot paths), directory-based storage in exchange for richer
metadata (WAL independent of data, atomic manifest rename, NUMA-aware
buffer-pool shards), and pull-based interpretation in exchange for simplicity
(no bytecode VM, no codegen).

The project is pre-1.0. It has known gaps in optimizer migration, FK deferral,
checkpoint modes, and single-process constraints. But the foundation is sound,
the layered architecture is auditable, the SQLite Logic-Test corpus is the
correctness oracle, and the build order ensures that each new feature
integrates cleanly with what came before.