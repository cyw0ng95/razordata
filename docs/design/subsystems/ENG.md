# ENG — Storage Engine

## Overview

The core of the database. Implements the LSM tree: a sharded lock-free skiplist memtable that flushes to SST files on disk, leveled compaction with subcompact, Ribbon filters (v2 SST) with Bloom (v1) backward compat, an L0 page cache, and an atomic file manifest. Manages schemas (incl. STRICT and WITHOUT ROWID), the persistent RCAT catalog, secondary B-tree indexes, NUMA topology, and row encoding. All reads and writes go through here; it calls down into `WAL` for durability and `MEM` for buffering. Depends on `MEM`, `WAL`, and `LOG`.

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
- Each writer inserts nodes via CAS on the head pointer. No mutex in the hot path.
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
- Per-skiplist random source (`rand.New(rand.NewPCG(...))`) eliminates global `math/rand` lock contention.

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
- **RibbonFilter (v2 SST, primary):**
  - Implemented in `internal/ENG/LS/ribbon.go` (`sstVersionRibbon = 2`). Ribbon is a successor to Bloom filters that achieves comparable false-positive rate at ~30% less space.
  - **False positive rate:** ≈ 0.39% at default sizing (vs ≈ 1.5% for Bloom at 10 bits/key).
  - **Query:** 4-probe lookup. If the filter rejects, the SST file is skipped without a block read.
  - **v1 Bloom compatibility:** legacy Bloom filters remain readable (`sst_version = 1`); new SSTs default to Ribbon (`sst_writer.go:209` sets `w.version = sstVersionRibbon`).
  - On read: check filter first. If filter says "definitely not present", skip the SST file entirely.
- **Footer (28 bytes):**
  ```
  [indexOffset:8][indexSize:4][filterOffset:8][filterSize:4][magic:4]
  ```
  (`bloomOffset`/`bloomSize` is the legacy v1 name; the byte layout is shared between Bloom and Ribbon.)
- **File naming:** `L<level>_<minKeyHex>_<maxKeyHex>_<fileID>.sst`
- **Columnar layout:** SST blocks can be written in column-major layout (all keys packed, then all values). Block layout is detected by first byte (0=row-major, 1=columnar). Saves 50%+ I/O for key-only scans.

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

### SST Page Cache

```go
type PageCache struct {
    slots   []*pageEntry
    cap     int // max number of pages
    size    int // current number of valid pages
    hand    int // clock-sweep hand position
    bufPool sync.Pool
}
```

- Fixed-size block-level cache for SST file pages (default 256 MB).
- 4 KB blocks keyed by `(fileID, blockOffset)`.
- Clock-sweep eviction policy.
- `sync.Pool` for page buffers to reduce allocation pressure.
- Integrated into `readFromSST` and `MayContain` via `readSSTFile` helper.

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `LS` | LSM tree: sharded lock-free skiplist memtable, SST writer/reader, Ribbon filter (v2) + Bloom (v1), leveled/tiered/hybrid compaction with rate limiter, columnar SST block layout with per-block dictionary compression, subcompaction for L4+, storage policy with tiered device placement, SST page cache, **L0 cache** (`l0_cache.go`), borrowed/epoch iterators, secondary-index store, page cache |
| `ID` | Index: persistent B-tree for secondary indexes (btree.razor), cursor-based scan, page-level CRC |
| `TB` | Table: create/drop/alter table metadata. **FK enforcement delegated to `SQB/UT/fk.go` + `SQB/DT/fk_queue.go` (DEFERRABLE queue); views and triggers to `SQB/WT/`.** Catalog persistence in CT. |
| `CT` | Catalog: persistent table metadata storage, schema versioning, bootstrap, encode/decode. Shared by TB and LS. |
| `SC` | Schema: column types, constraints (NOT NULL, DEFAULT, PRIMARY KEY, UNIQUE, CHECK, FOREIGN KEY), table definitions, integrity checks |
| `DP` | Deparser: row serialization, SST block encoding, value encoding, delta-key encoding in blocks |
| `NM` | NUMA: topology detection via `/sys/devices/system/node`, worker pinning via `runtime.LockOSThread` for first-touch allocation |

## Clusters

### LS — LSM Tree

**Responsibility:** Memtable, SST flush, leveled/tiered/hybrid compaction, Ribbon filter (v2) + Bloom (v1), file manifest, columnar SST, rate limiter, subcompaction, storage policy, page cache, L0 cache.

**Key behaviors:**
- `Insert`: write to active memtable. If memtable is frozen, create a new active memtable and write there.
- `Get`: check active memtable → frozen memtables (newest first) → L0 cache → L0 SSTs (newest first) → L1+ (binary search via index + Ribbon/Bloom filter). Page cache consulted before file read.
- `NewIterator`: merge iterators from all sources (memtable + all SST files) in sorted key order using a min-heap.
- `Flush`: freeze active memtable, write it as an SST to L0, update manifest.
- `Compact`: trigger background compaction goroutine. Runs in a separate goroutine, rate-limited via `RateLimiter`.
- **Compaction styles:** `CompactionStyleLeveled` (default), `CompactionStyleTiered` (write-heavy), `CompactionStyleHybrid` (tiered L0 + leveled L1+). `SetCompactionStyle()` changes at runtime.
- **Rate limiter:** Token-bucket throttling on compaction write throughput.
- **Subcompaction:** For L4+, partitions input key ranges into N sub-jobs, runs them in parallel via a worker pool, then merges the output SSTs.
- **Dictionary compression:** Per-block frequency-based dictionary (4-8 byte substrings, max 4 KB) with `flate.NewWriterDict`. Falls back to plain flate if dictionary is empty or not effective.
- **Storage policy:** `StoragePolicyUniform` (default) vs `StoragePolicyTiered` — maps output levels to device paths via `PlacementPolicy` symlinks.

### ID — Index

**Responsibility:** Persistent B-tree for secondary indexes.

**Key behaviors:**
- `btree.razor` file in the database directory stores a page-oriented B-tree with CRC32 integrity on each page.
- `Insert(key, value)`, `Get(key)`, `Delete(key)` with page cache in memory.
- `Cursor()` provides seek and forward scan over the B-tree.
- Page size: 4096 bytes; max 200 keys per page.
- Delete rebalancing: merge/redistribute after delete.

### TB — Table

**Responsibility:** Table metadata operations: `CREATE TABLE`, `DROP TABLE`, `ALTER TABLE`, schema management.

**Key behaviors:**
- `CREATE TABLE`: allocate `tableID`, serialize `TableSchema`, insert into system catalog LSM. Supports `STRICT` table type (REQ001369) and `WITHOUT ROWID` (rejected at this layer — REQ001312 deferred).
- `DROP TABLE`: mark the table's key range as deleted (tombstone) in the system catalog, remove schema from registry.
- `ALTER TABLE`: `ADD COLUMN`, `DROP COLUMN` (with cascade rules — REQ001384), `RENAME TO`, `RENAME COLUMN`, `ALTER SET/DROP DEFAULT` — online schema migration without table copy.
- `GetSchema(tableID)`: look up from in-memory `map[tableID]*TableSchema`, or load from catalog if not cached.
- **Foreign key validation** (MATCH FULL/PARTIAL/SIMPLE, all 5 reference actions, DEFERRABLE queue): delegated to `internal/SQB/UT/fk.go` and `internal/SQB/DT/fk_queue.go`.
- **Views and triggers:** delegated to `internal/SQB/WT/` (`view.go`, `matview.go`, `writers_dml.go`). Triggers are stored as named action definitions (BEFORE/AFTER/INSTEAD OF INSERT/UPDATE/DELETE) and fired by the executor; TEMP triggers are supported (REQ001370).

### SC — Schema

**Responsibility:** Column types, constraints, table definitions, integrity checks.

**Key behaviors:**
- `ValidateRow(row, schema)`: check that all non-nullable columns have values, types match, constraints satisfied.
- `Compare(a, b ColumnDef) bool`: compare two column definitions for equality (used in schema versioning).
- Constraints supported: NOT NULL, DEFAULT, PRIMARY KEY, UNIQUE, CHECK, FOREIGN KEY.
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
3. **`internal/ENG/LS/sst_writer.go`** — SST file writer: block encoding with restart points, Ribbon filter (v2) with Bloom (v1) fallback, footer.
4. **`internal/ENG/LS/sst_reader.go`** — SST file reader: block decoding, Ribbon/Bloom filter check, index binary search.
5. **`internal/ENG/LS/ribbon.go`** — Ribbon filter implementation (4 probes, ~30% less space than Bloom at the same FPR).
6. **`internal/ENG/LS/l0_cache.go`** — L0 page cache for hot L0 SSTs.
7. **`internal/ENG/LS/sharded_memtable.go`** — sharded lock-free skiplist memtable.
8. **`internal/ENG/LS/borrowed_iter.go`**, **`epoch_iter.go`**, **`index_store.go`** — iterator variants and secondary-index store.
5. **`internal/ENG/LS/manifest.go`** — `Manifest`: versioning, apply diff, atomic rename, checkpoint.
6. **`internal/ENG/LS/flush.go`** — memtable flush: freeze, write SST, update manifest.
7. **`internal/ENG/LS/compaction.go`** — leveled compaction: pick job, merge sort, write output, update manifest.
8. **`internal/ENG/LS/read.go`** — read path: memtable → L0 → L1+, merge iterator.
9. **`internal/ENG/LS/page_cache.go`** — SST page cache: clock-sweep eviction, `sync.Pool` for page buffers.
10. **`internal/ENG/SC/sc.go`** — schema types, `ValidateRow`, `Compare`.
11. **`internal/ENG/DP/dp.go`** — row serialization, block encoding, value encode/decode.
12. **`internal/ENG/TB/tb.go`** — `CREATE TABLE`, `DROP TABLE`, schema registry.
13. **`internal/ENG/ID/id.go`** — B-tree secondary index.
14. **Tests:** `skiplist_test.go` (concurrent insert/find), `memtable_test.go` (flush trigger), `sst_test.go` (round-trip write/read), `compaction_test.go` (data not lost after compaction), `manifest_test.go` (version atomicity), `page_cache_test.go` (clock-sweep eviction, concurrent access).

## Open Issues

- How to estimate the number of levels and size budget per level? Start with: L0 = 4 MB, L1 = 32 MB, each subsequent level 10x larger.
- Should we support prefix bloom filters (per-column prefix) for range scans?
