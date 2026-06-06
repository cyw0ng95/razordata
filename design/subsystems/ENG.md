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
| `LS` | LSM tree: memtable, SST writer, SST reader, bloom filter, compaction, manifest |
| `ID` | Index: primary key index (v1: primary key is the table key) |
| `TB` | Table: create table, drop table, schema management |
| `SC` | Schema: column types, constraints, table definitions |
| `DP` | Deparser: row serialization, SST block encoding, value encoding |

## Clusters

### LS — LSM Tree

**Responsibility:** Memtable, SST flush, leveled compaction, bloom filter, file manifest.

**Key behaviors:**
- `Insert`: write to active memtable. If memtable is frozen, create a new active memtable and write there.
- `Get`: check active memtable → frozen memtables (newest first) → L0 (newest first) → L1+ (binary search via index + bloom).
- `NewIterator`: merge iterators from all sources (memtable + all SST files) in sorted key order using a min-heap.
- `Flush`: freeze active memtable, write it as an SST to L0, update manifest.
- `Compact`: trigger background compaction goroutine. Runs in a separate goroutine, rate-limited.

### ID — Index

**Responsibility:** Primary key index. In v1, the primary key IS the table key in the LSM tree. Secondary indexes are future work.

**Key behaviors:**
- `__primary__:<tableID>:<pk>` → row data (the table key itself).
- No separate index structure needed for v1.
- Future: secondary index via `__idx__:<tableID>:<idxName>:<col>` → list of primary keys.

### TB — Table

**Responsibility:** Table metadata operations: `CREATE TABLE`, `DROP TABLE`, schema management.

**Key behaviors:**
- `CREATE TABLE`: allocate `tableID`, serialize `TableSchema`, insert into system catalog LSM.
- `DROP TABLE`: mark the table's key range as deleted (tombstone) in the system catalog, remove schema from registry.
- `GetSchema(tableID)`: look up from in-memory `map[tableID]*TableSchema`, or load from catalog if not cached.

### SC — Schema

**Responsibility:** Column types, constraints, table definitions.

**Key behaviors:**
- `ValidateRow(row, schema)`: check that all non-nullable columns have values, types match, constraints satisfied.
- `Compare(a, b ColumnDef) bool`: compare two column definitions for equality (used in schema versioning).

### DP — Deparser

**Responsibility:** Row serialization, SST block encoding, value encoding.

**Key behaviors:**
- `EncodeValue(v Value, t ColumnType) ([]byte, error)`: encode a scalar value to bytes.
- `DecodeValue(data []byte, t ColumnType) (Value, error)`: decode bytes to a scalar value.
- `EncodeRow/DecodeRow`: apply the per-column encoding based on schema.
- `EncodeBlock/DecodeBlock`: SST block delta encoding with restart points.

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

## Open Issues

- How to estimate the number of levels and size budget per level? Start with: L0 = 4 MB, L1 = 32 MB, each subsequent level 10x larger.
- Should we support prefix bloom filters (per-column prefix) for range scans?