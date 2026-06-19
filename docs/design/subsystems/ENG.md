# ENG — Storage Engine

## Overview

The core of the database. Implements the LSM tree: a lock-free skiplist memtable that flushes to SST files on disk, leveled compaction, bloom filters, and a file manifest. Manages schemas and row encoding. All reads and writes go through here; it calls down into `WAL` for durability and `MEM` for buffering. Depends on `MEM`, `WAL`, and `LOG`.

## Dependencies

- Required: `MEM`, `WAL`, `LOG`
- Consumed interfaces: `BufferPool`, `Writer`

## Exposed Interfaces

```go
// Store is the storage engine interface
type Store interface {
    Insert(key, value []byte) error
    Get(key []byte) ([]byte, error)
    Delete(key []byte) error
    NewIterator(prefix []byte) Iterator
    Flush() error // flush memtable to SST
    Compact() error // trigger compaction
    Close() error
}

// Iterator iterates over key-value pairs
type Iterator interface {
    Next() bool
    Key() []byte
    Value() []byte
    Err() error
    Close()
}

// Manifest tracks all SST files across levels
type Manifest interface {
    Current() Version
    Apply(v Version) error
    Checkpoint() (*ManifestCheckpoint, error)
}
```

## Data Structures

### Memtable

```go
type memtable struct {
    skiplist *skipList // lock-free skiplist, keyed by []byte
    size     atomic.Int64 // current approximate size
    refs     atomic.Int64 // reference count (Flush vs active writers)
    frozen   atomic.Bool  // true when flush has started
}
```

- Lock-free skiplist in memory (`sync/atomic` CAS-based insertion).
- Each writer inserts nodes via CAS on the head pointer. No mutex, no lock-free array.
- When `size >= Options.MemTableSize` (default 64 MB), the memtable is frozen and a background goroutine flushes it to L0.
- Frozen memtables are immutable — no new writes accepted. New writes go to the active memtable.
- On flush, the catalog root pointer is updated in the WAL as an `RTCheckpoint` record so recovery can locate the system catalog.

### SkipList

```go
type skipList struct {
    head    atomic.Pointer[node]
    level   atomic.Int32 // current max level, starts at 1
    maxLevel int = 12   // 2^12 = 4096 levels, sufficient for 10^9 items
}

type node struct {
    key   []byte
    value []byte
    next  [maxLevel]atomic.Pointer[node]
}
```

- CAS-based insertion from the bottom up. Find the predecessor at each level, then CAS the `next` pointer.
- Search is also lock-free: read the `next` pointer, compare keys, descend.
- No mutex in the hot path — only `atomic` operations.

### SSTFile

```
┌────────────────────────────────────────────────────────┐
│ [DataBlock_0]                                          │
│ [DataBlock_1]                                          │
│ ...                                                    │
│ [DataBlock_N]                                          │
│ [IndexBlock]  // one entry per data block             │
│ [BloomFilter]  // bitset, 10 bits per key              │
│ [Footer]                                              │
└────────────────────────────────────────────────────────┘
```

- **DataBlock (default 4 KB):**
  - Format: `[KV pairs][restart array][restart count:4][checksum:4]`
  - K-V pairs are delta-encoded: each key stores only the delta from the previous key.
  - Restart points every 16 K-V pairs — binary search within the block is O(1) per restart.
- **IndexBlock:**
  - One entry per data block: `[largestKey:varint][blockOffset:varint][blockSize:varint]`
  - Sorted by `largestKey`. Binary search for the target block.
- **BloomFilter:**
  - **Structure:** `[]byte` bitset with 10 bits per key (expected). For N keys, bit array size = `(N * 10 + 7) / 8` bytes.
  - **Hash functions:** Double hashing using two independent FNV-1a hashes with different seeds:
    - `h1 = FNV1a(key, seed=0x811C9DC5)`
    - `h2 = FNV1a(key, seed=0x01000193)`
    - Combined position: `pos = (h1 + i * h2) % (bitSize * 8)` for i in [0, 1] (2 probes per key)
  - **Insert:** For each key, compute h1 and h2, then set 2 bits:
    ```go
    for i := 0; i < 2; i++ {
        pos := (h1 + uint64(i) * h2) % uint64(len(bits) * 8)
        bits[pos/8] |= 1 << (pos % 8)
    }
    ```
  - **Query:** Check both bit positions. If either bit is 0, key is definitely not present. If both bits are 1, key is probably present.
  - **False positive rate:** With 10 bits per key and 2 hash functions, expected false positive rate ≈ `(1 - e^(-2N/M))^2` where M = bit array size, N = key count. For 10 bits/key, this yields ~1% false positive rate.
  - On read: check bloom filter first. If bloom says "definitely not present", skip the SST file entirely.
- **Footer (28 bytes):**
  ```
  [indexOffset:8][indexSize:4][bloomOffset:8][bloomSize:4][magic:4]
  ```

- **File naming:** `L<level>_<minKeyHex>_<maxKeyHex>_<fileID>.sst`
- `minKey` and `maxKey` are hex-encoded first and last key in the file, used for range overlap checks during compaction and reads.

### Manifest

```go
type manifest struct {
    version    atomic.Int64
    current    Version
    dir        string
    changes    chan VersionDiff // for background compaction
}

type Version struct {
    num     int64
    levels  [][]SSTFileMeta
    created time.Time
}

type SSTFileMeta struct {
    FileID    uint64
    Level     int
    MinKey    []byte
    MaxKey    []byte
    Size      int64
    BloomBits int
}
```

- The manifest stores the current version of the LSM tree: which SST files exist at each level, their key ranges, and their sizes.
- On every flush or compaction, a new version is created and written to `manifest` atomically: write to a temp file → `fsync` the temp file → `rename` to the final path → `fsync` the directory.
- The manifest is the single source of truth for which SST files are live. Compaction and reads consult the manifest.
- `Version` is immutable once created — new versions are created by applying a `VersionDiff`.

### CompactionJob

```go
type compactionJob struct {
    level     int
    inputs    []*SSTFileMeta // files from L_k
    outputs   []*SSTFileMeta // result written to L_{k+1}
    overlap   []SSTFileMeta  // overlapping files from L_{k+1}
}
```

- When L_k exceeds its size budget, a `compactionJob` is created.
- The job picks the oldest files from L_k, merges them with overlapping files from L_{k+1}.
- The merge is a multi-way merge sort: read all input files, iterate in sorted key order, write output to a temp directory.
- On completion: atomically rename temp files to L_{k+1}, update the manifest, delete old input files.

### Schema

```go
type TableSchema struct {
    TableID   uint64
    Name      string
    Columns   []ColumnDef
    PrimaryKey []int // column indices of the primary key
}

type ColumnDef struct {
    Name      string
    Type      ColumnType
    Nullable  bool
    Default   Value // nil means no default
    PrimaryKey bool
}

type ColumnType uint8

const (
    CTInt     ColumnType = 0
    CTBigInt  ColumnType = 1
    CTVarchar ColumnType = 2
    CTFloat   ColumnType = 3
    CTBool    ColumnType = 4
    CTText    ColumnType = 5
    CTBlob    ColumnType = 6
    CTTimestamp ColumnType = 7
)
```

- The system catalog is a special LSM tree (catalog SST files stored under `sst/catalog/`).
- Schema data is stored as key-value pairs: `__catalog:<tableID>` → `MessagePack`-encoded `TableSchema`.
- Table registry: `map[tableID]*TableSchema`, protected by `sync.RWMutex`.

### Deparser

```go
// Row encoding for memtable and SST blocks
func encodeRow(row Row, schema *TableSchema) ([]byte, error)
func decodeRow(data []byte, schema *TableSchema) (Row, error)

// Block encoding for SST data blocks
func encodeBlock(kvs []Pair, restartInterval int) ([]byte, error)
func decodeBlock(data []byte) ([]KV, []int, error) // KVs, restart positions
```

- Fixed-width columns stored inline: `INT` (8 bytes), `BIGINT` (8 bytes), `FLOAT` (8 bytes), `BOOL` (1 byte).
- Variable-length columns: `[length:varint][data:blob]`.
- Null values: a null bitmap in the row header. One bit per column.

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `LS` | LSM tree: memtable, SST writer, SST reader, bloom filter, leveled/tiered/hybrid compaction (REQ000320), rate-limited compaction (REQ000318), columnar SST block layout (REQ000314), per-block dictionary compression (REQ000297), subcompaction for L4+ (REQ000319), storage policy with tiered device placement (REQ000300), index store (sst_dict), index reader, catalog bootstrap |
| `ID` | Index: persistent B-tree for secondary indexes (btree.razor), cursor-based scan, page-level CRC |
| `TB` | Table: create/drop/alter table, schema catalog, foreign key enforcement, views, triggers |
| `SC` | Schema: column types, constraints (NOT NULL, DEFAULT, PRIMARY KEY, UNIQUE, CHECK, FOREIGN KEY), table definitions, integrity checks (REQ000124), columnar table support |
| `DP` | Deparser: row serialization, SST block encoding, value encoding, delta-key encoding in blocks |
| `NM` | NUMA: topology detection via `/sys/devices/system/node`, worker pinning via `runtime.LockOSThread` for first-touch allocation (REQ000309) |

## Clusters

### LS — LSM Tree

**Responsibility:** Memtable, SST flush, leveled/tiered/hybrid compaction, bloom filter, file manifest, columnar SST, rate limiter, subcompaction, storage policy.

**Key behaviors:**
- `Insert`: write to active memtable. If memtable is frozen, create a new active memtable and write there.
- `Get`: check active memtable → frozen memtables (newest first) → L0 (newest first) → L1+ (binary search via index + bloom).
- `NewIterator`: merge iterators from all sources (memtable + all SST files) in sorted key order using a min-heap.
- `Flush`: freeze active memtable, write it as an SST to L0, update manifest.
- `Compact`: trigger background compaction goroutine. Runs in a separate goroutine, rate-limited via `RateLimiter`.
- **Compaction styles (REQ000320):** `CompactionStyleLeveled` (default), `CompactionStyleTiered` (write-heavy), `CompactionStyleHybrid` (tiered L0 + leveled L1+). `SetCompactionStyle()` changes at runtime.
- **Rate limiter (REQ000318):** Token-bucket throttling on compaction write throughput. `SetRateLimiter()` configures bytes/sec and burst.
- **Subcompaction (REQ000319):** For L4+, `SubCompactor` partitions input key ranges into N sub-jobs, runs them in parallel via a worker pool, then merges the output SSTs.
- **Columnar SST (REQ000314):** `columnar.go` writes SST blocks in column-major layout (all keys packed, then all values). Block layout is detected by first byte (0=row-major, 1=columnar). Saves 50%+ I/O for key-only scans.
- **Dictionary compression (REQ000297):** `sst_dict.go` trains a per-block frequency-based dictionary (4-8 byte substrings, max 4 KB) and uses `flate.NewWriterDict` for compression. Falls back to plain flate if dictionary is empty or not effective.
- **Storage policy (REQ000300):** `StoragePolicyUniform` (default) vs `StoragePolicyTiered` — maps output levels to device paths via `PlacementPolicy` symlinks.

### ID — Index

**Responsibility:** Persistent B-tree for secondary indexes.

**Key behaviors:**
- `btree.razor` file in the database directory stores a page-oriented B-tree with CRC32 integrity on each page.
- `Insert(key, value)`, `Get(key)`, `Delete(key)` with page cache in memory.
- `Cursor()` provides seek and forward scan over the B-tree.
- Page size: 4096 bytes; max 200 keys per page.
- Unlike the original design (primary key = table key), this is a dedicated secondary index structure using a separate B-tree.

### TB — Table

**Responsibility:** Table metadata operations: `CREATE TABLE`, `DROP TABLE`, `ALTER TABLE`, schema management, foreign key enforcement, views, triggers.

**Key behaviors:**
- `CREATE TABLE`: allocate `tableID`, serialize `TableSchema`, insert into system catalog LSM.
- `DROP TABLE`: mark the table's key range as deleted (tombstone) in the system catalog, remove schema from registry.
- `ALTER TABLE`: `ADD COLUMN`, `DROP COLUMN`, `RENAME` — online schema migration.
- `GetSchema(tableID)`: look up from in-memory `map[tableID]*TableSchema`, or load from catalog if not cached.
- Foreign key validation is delegated to `SQL/EX/fk.go`.
- Views are materialized as stored `SELECT` queries resolved at query planning time.
- Triggers are stored as named action definitions (BEFORE/AFTER INSERT/UPDATE/DELETE) and fired by the executor.

### SC — Schema

**Responsibility:** Column types, constraints, table definitions, integrity checks, columnar table support.

**Key behaviors:**
- `ValidateRow(row, schema)`: check that all non-nullable columns have values, types match, constraints satisfied.
- `Compare(a, b ColumnDef) bool`: compare two column definitions for equality (used in schema versioning).
- Constraints supported: NOT NULL, DEFAULT, PRIMARY KEY, UNIQUE, CHECK, FOREIGN KEY (REQ000124).
- Integrity checks: `IntegrityTable` verifies catalog consistency, row counts, and data corruption.

### DP — Deparser

**Responsibility:** Row serialization, SST block encoding, value encoding.

**Key behaviors:**
- `EncodeValue(v Value, t ColumnType) ([]byte, error)`: encode a scalar value to bytes.
- `DecodeValue(data []byte, t ColumnType) (Value, error)`: decode bytes to a scalar value.
- `EncodeRow/DecodeRow`: apply the per-column encoding based on schema.
- `EncodeBlock/DecodeBlock`: SST block delta encoding with restart points.

### NM — NUMA

**Responsibility:** NUMA topology detection and worker affinity for first-touch allocation.

**Key behaviors:**
- `detect()` reads `/sys/devices/system/node` to count NUMA nodes.
- `NodeCount()` returns the count (cached), or 1 on non-NUMA hosts.
- `CurrentNode()` returns a heuristic NUMA node ID for the calling goroutine.
- `PinWorker()` calls `runtime.LockOSThread` to pin a goroutine to an OS thread, ensuring first-touch memory allocations land on the correct NUMA node.
- On non-NUMA hosts, all functions return 0/1 and behave identically to the pre-NUMA code path.

## Implementation Plan

1. **`internal/ENG/LS/skiplist.go`** — lock-free skiplist: `Insert`, `Find`, `Iterator`. Test against a reference implementation.
2. **`internal/ENG/LS/memtable.go`** — `Memtable` struct: `Insert`, `Get`, `Iterator`, size tracking, freeze trigger.
3. **`internal/ENG/LS/sst_writer.go`** — SST file writer: block encoding with restart points, bloom filter, footer.
4. **`internal/ENG/LS/sst_reader.go`** — SST file reader: block decoding, bloom check, index binary search.
5. **`internal/ENG/LS/manifest.go`** — `Manifest`: versioning, apply diff, atomic rename, checkpoint.
6. **`internal/ENG/LS/flush.go`** — memtable flush: freeze, write SST, update manifest.
7. **`internal/ENG/LS/compaction.go`** — leveled compaction: pick job, merge sort, write output, update manifest.
8. **`internal/ENG/LS/read.go`** — read path: memtable → L0 → L1+, merge iterator.
9. **`internal/ENG/SC/sc.go`** — schema types, `ValidateRow`, `Compare`.
10. **`internal/ENG/DP/dp.go`** — row serialization, block encoding, value encode/decode.
11. **`internal/ENG/TB/tb.go`** — `CREATE TABLE`, `DROP TABLE`, schema registry.
12. **`internal/ENG/ID/id.go`** — (placeholder for v1: primary key is the table key).
13. **Tests:** `skiplist_test.go` (concurrent insert/find), `memtable_test.go` (flush trigger), `sst_test.go` (round-trip write/read), `compaction_test.go` (data not lost after compaction), `manifest_test.go` (version atomicity).

## Shipped Requirements

The following requirements have been implemented and shipped; they are now part of the design baseline.

### LS — LSM Tree

| ID | Requirement | Iteration |
|---|---|---|
| REQ000036 | Lock-free skiplist memtable | iter-04 |
| REQ000037 | Memtable freeze + flush to L0 SST | iter-04 |
| REQ000038 | SST writer: data blocks, index, bloom, footer | iter-04 |
| REQ000039 | SST reader: block decode, bloom check, index binary search | iter-04 |
| REQ000040 | Leveled compaction with multi-way merge sort | iter-04 |
| REQ000041 | Atomic manifest versioning (temp + rename + `fsync`) | iter-04 |
| REQ000042 | Bloom filter (10 bits/key, double-hashing) | iter-04 |
| REQ000043 | Delta-encoded data blocks with restart points | iter-04 |
| REQ000044 | `ENG/LS` benchmarks (skiplist insert/find, SST write/read, flush, compaction) | iter-17 |
| REQ000046 | IndexScan operator exists (prefix-scan fallback) | iter-08 |
| REQ000047 | Prefix bloom filters for range scans | iter-23 |
| REQ000148 | BloomFilter double-hashing with FNV-1a seeds | iter-04 (bloom) |
| REQ000165 | Compaction job scheduling based on level size budget | iter-04 (compaction) |
| REQ000174 | BloomFilter FNV-1a double-hash | iter-20 |
| REQ000180 | Dynamic BloomFilter sizing `((N * 10 + 7) / 8)` bytes | iter-16 |
| REQ000186 | SST file path unification (flush/compaction emit the same shape) | iter-16 |
| REQ000198 | Skiplist `sync.Pool` for scratch predecessor/successor arrays | iter-20 |
| REQ000271 | Compression for SST blocks (flate) | iter-23 |
| REQ000297 | SST block-level dictionary compression (frequency-based, no C deps) | iter-27 |
| REQ000298 | LSM-aware cross-block shared dictionary (multiple blocks in one SST share a trained dict) | iter-27 |
| REQ000300 | Tier-aware storage scheduler (PlacementPolicy, StoragePolicy, per-level device routing) | iter-27 |
| REQ000309 | NUMA-aware placement (`NodeCount`, `IsAvailable`, `CurrentNode`, `PinWorker`, `bufferSlot.nodeID`, subcompaction worker `LockOSThread`) | iter-27 (Phase 6) |
| REQ000314 | Columnar SST layout (PAX / hybrid row-columnar) | iter-27 |
| REQ000318 | Write rate-limited compactor (token bucket, `Options.CompactionRateLimit`) | iter-27 |
| REQ000319 | Sub-compaction parallelism (key-range sub-jobs, worker pool) | iter-27 |
| REQ000320 | Configurable compaction style (leveled/tiered/hybrid) | iter-27 |
| REQ000347 | Silent data loss in LSM flush path — fixed | iter-26 |
| REQ000364 | Negative WaitGroup counter panic in `flushManager` (REQ000347 followup) — `Add(1)` before send, `enqueueMu` serializes with `Stop` | iter-26.1 (v0.26.3) |
| REQ000540 | L0 buffer cache — `l0Cache` LRU keyed by blockID; API ready for engine wiring | iter-28.2 |
| REQ000552 | Adaptive memtable size — `targetSize` atomic.Int64 + `SetTargetSize`/`TargetSize` plumbing | iter-28.2 |

### ID — Index

| ID | Requirement | Iteration |
|---|---|---|
| REQ000250 | B-tree secondary index package (`btree.razor`, foundation) | iter-23 |
| REQ000283 | Fix `Cursor.Next()` leaf boundary traversal | iter-23 |
| REQ000284 | BTree delete rebalancing — merge/redistribute after delete | iter-27 |
| REQ000285 | uint32 page ID overflow protection — wraps to 0 (sentinel for "no page") | iter-23 |

### TB / SC / DP — Schema and Deparser

| ID | Requirement | Iteration |
|---|---|---|
| REQ000045 | Secondary indexes (non-PK columns; lookup by `__idx__:<table>:<col>:<val>`) | iter-12 (catalog), iter-21 (ID) |
| REQ000048 | Table registry persistence (`ENG/TB/` package — `Registry` + `Catalog`, atomic temp-file rename) | iter-27 |
| REQ000049 | Schema cluster split from LS (`TableSchema`, `ColumnDef`, `ColumnType`, `Row`, `Validator`) | iter-27 |
| REQ000050 | Deparser cluster split from LS (`EncodeRow`/`DecodeRow`, `EncodeBlock`/`DecodeBlock`) | iter-27 |
| REQ000155 | Catalog persistence across restarts (consolidated with REQ000127) | iter-12 |
| REQ000254 | Histogram-based selectivity stats (types defined, ANALYZE pending) | iter-22 |

## Open Issues

- How to estimate the number of levels and size budget per level? Start with: L0 = 4 MB, L1 = 32 MB, each subsequent level 10x larger.
- Should we support prefix bloom filters (per-column prefix) for range scans?