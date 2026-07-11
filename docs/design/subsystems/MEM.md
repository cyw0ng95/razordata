# MEM — Memory Layer

## Overview

Buffer pool that caches SST blocks in memory. All reads from and writes to the LSM tree go through here. Uses clock-sweep LRU eviction with atomic pin/unpin. On clean shutdown, serializes the hot working set to a hint file for fast startup warm-up. Depends on `FIL`.

## Dependencies

- Required: `FIL`
- Consumed interfaces: `BlockDevice`, `FileManager`

## Exposed Interfaces

```go
// BufferPool manages in-memory block caching
type BufferPool interface {
    Get(ctx context.Context, blockID uint64) (*Page, bool, error) // found, wasCached, error
    Pin(page *Page)
    Unpin(page *Page)
    SetCapacity(n int64) error
    Stats() BufferStats
    Close() error
}

// Page is a cached block
type Page struct {
    ID    uint64
    Data  []byte // borrowed from sync.Pool, never copied
    Dirty bool   // true if modified since load
}

// BufferStats reports buffer pool state
type BufferStats struct {
    Hits    atomic.Int64
    Misses  atomic.Int64
    Pins    atomic.Int64
    Evicts  atomic.Int64
    Capacity int64
    Used    int64
}

// SyncPool provides reusable objects
type SyncPool interface {
    Get(size int) []byte
    Put(buf []byte)
}
```

## Data Structures

### BufferSlot

```go
type bufferSlot struct {
    blockID   uint64
    data      []byte // fixed-size: BlockSize bytes
    pinCount  atomic.Int32
    dirty     atomic.Bool
    refKey    atomic.Uint64 // clock hand reference
    loading   atomic.Bool   // true while loading from disk
    wait      chan struct{} // closed when loaded
}
```

- `data` is a fixed-size `[]byte` borrowed from `SP`. Never allocated per-slot.
- `pinCount` is atomically incremented on `Pin`, decremented on `Unpin`. Eviction only allowed when `pinCount == 0`.
- `loading` prevents concurrent loads of the same block (only one goroutine loads; others wait on `wait`).
- `wait` is created on first load, closed when data is ready. Waiting goroutines receive the loaded `*Page`.

### BufferHashTable

```go
type bufferHashTable struct {
    slots map[uint64]*bufferSlot
    mu    sync.RWMutex
}
```

- O(1) lookup by `blockID`.
- `RLock` for reads (`Get`), `Lock` for writes (eviction, insertion).
- The map stores only cached blocks; `nil` means block is not in memory.

### ClockSweep Integration

The clock sweep runs concurrently with `Get`/`Pin`/`Unpin` operations:

- `hand` is an `atomic.Uint64` — no mutex needed to read or increment it.
- Each slot's `refKey` is atomically updated on access (after a successful `Get`, set `refKey = hand.Load()`).
- Eviction loop: read `hand` → check slot's `refKey` → if `refKey < hand - N` (N = clock interval), candidate for eviction.
- Eviction is gated by `mu.Lock()` on the hash table — it acquires write lock, checks `pinCount == 0`, flushes if dirty, removes from map, releases lock.
- This means eviction may briefly stall a `Get` that needs to insert a new slot, but `Get` itself is never blocked on a pending eviction.
- `loading` flag on a slot prevents multiple goroutines from loading the same block simultaneously — only the first acquires the write lock; others wait on `wait` channel.

### SyncPool

```go
type syncPool struct {
    pagePool  sync.Pool // make([]byte, BlockSize)
    iterPool  sync.Pool // make([]byte, iterBufferSize)
}
```

- `pagePool.New` allocates `make([]byte, BlockSize)` — pre-sized, zero allocation on `Get`.
- `iterPool` reuses iterator buffers (for LSM tree traversal).
- `Put` returns buffers to the pool; if the buffer is the wrong size, it is dropped (not returned).

### HintFile

```go
type hintEntry struct {
    BlockID     uint64
    LastAccess  int64 // Unix timestamp in seconds
}
```

- Serialized as a binary list: `[count:varint][entry_0][entry_1]...[entry_N]`
- Each entry: `[blockID:varint][lastAccess:varint]`
- Both fields are varint-encoded (1-10 bytes each), making the file compact.
- No checksum on the hint file — it is advisory only. On corruption, the buffer pool starts cold.
- **Compression:** not yet implemented in the current code (`internal/MEM/BF/bf.go:472-603` writes raw binary). The REQ000024 row in the shipped table should be moved to FUTURE until gzip-on-write lands.
- Written on clean `Close()` via `Engine.Close`.
- On startup, read from `<name>.razor/hint` and eagerly loaded into the buffer pool before serving queries.

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `BF` | Buffer pool: sharded clock-sweep eviction, W-TinyLFU admission (4-row count-min sketch, REQ000303), pin/unpin, O(1) hash lookup, NUMA-aware shard allocation (REQ000309), warm-start hint file |
| `SP` | Sync pool: goroutine-safe object pooling for pages and iterators |
| `OF` | Overflow: handling values that exceed page size via overflow blocks chained in the WAL (REQ000308) |

> **Note:** The `PC` cluster listed in earlier revisions was folded into `BF` after the page-slot logic, checksum verification, and hint-file routines were unified under the buffer pool implementation (`internal/MEM/BF/`). No separate `internal/MEM/PC/` directory exists in the current code.

## Clusters

### BF — Buffer Pool

**Responsibility:** Sharded clock-sweep eviction, W-TinyLFU admission, pin/unpin, hash table lookup, NUMA-aware allocation, warm-start hint file.

**Key behaviors:**
- **Sharded hash table:** global table split into N independent shards (default 32). Each shard has its own clock-sweep hand and `sync.RWMutex`. Shard selection: `blockID % numShards`. This eliminates global mutex contention under concurrent reads.
- `Get`: look up in shard-local hash table. If found and not loading, return immediately. If not found, load from `FIL/DF` (via `BlockDevice.ReadBlock`), insert into hash table, return.
- `Pin`: increment `pinCount`. Page cannot be evicted while `pinCount > 0`.
- `Unpin`: decrement `pinCount`. Eviction may proceed once `pinCount == 0`.
- `SetCapacity`: resize the slot ring. If shrinking, evict oldest clean slots.
- `Stats`: `Hits` / `Misses` ratio, `Pins`, `Evicts`, capacity, used.
- **W-TinyLFU admission (REQ000303):** Implemented in `internal/MEM/BF/wtinylfu.go`. Frequency tracking via a 4-row Count-Min Sketch with 4-bit counters; admission compares the new block's CMS frequency against the eviction candidate's. (The "Cuckoo filter + TinyLFU hybrid" wording in earlier revisions is incorrect — W-TinyLFU uses only the CMS, no Cuckoo filter.)
- **NUMA-aware (REQ000309):** On Linux, `internal/MEM/BF/sharded_bp.go` reads NUMA topology from `internal/ENG/NM/numa.go` and uses first-touch allocation so each shard's pages land on its home NUMA node.
- **Warm-start hint file:** `internal/MEM/BF/bf.go:472-603` (`Warm`, `writeHintFile`, `readHintFile`) — serializes cached block IDs + last-access timestamps to `<name>.razor/hint` on clean shutdown; on startup, eagerly loads them into the buffer pool.

### OF — Overflow

**Responsibility:** Large value handling when values exceed the page size.

**Key behaviors:**
- Values larger than `PageSize` (4 KB) are split into overflow blocks.
- The primary row stores a pointer to the first overflow block; subsequent blocks are chained via the WAL.
- `of.go` manages overflow block allocation, chain traversal, and cleanup on delete.

### SP — Sync Pool

**Responsibility:** Goroutine-safe object pooling for page buffers and iterator objects.

**Key behaviors:**
- `Get(size int) []byte`: fetch from pool if available and `len >= size`; otherwise `make([]byte, size)`.
- `Put(buf []byte)`: return to pool if `cap(buf) == BlockSize` or `cap(buf)` matches a known iterator buffer size; otherwise drop.
- All `New` functions pre-allocate to avoid per-request `make` calls.

## Implementation Plan

1. **`internal/MEM/SP/sp.go`** — `SyncPool` implementation with `sync.Pool` for page-sized and iterator-sized buffers.
2. **`internal/MEM/BF/bf.go`** — buffer pool with hash table, clock-sweep eviction, `Get`/`Pin`/`Unpin`/`SetCapacity`/`Stats`, hint file routines (`writeHintFile`, `readHintFile`, `Warm`).
3. **`internal/MEM/BF/sharded_bp.go`** — sharded buffer pool with NUMA-aware shard allocation.
4. **`internal/MEM/BF/wtinylfu.go`** — W-TinyLFU admission policy (CMS + window LRU).
5. **`internal/MEM/OF/of.go`** — overflow block allocation, chain traversal, cleanup on delete.
6. **`internal/MEM/BF/bf_bench.go`** — benchmarks: concurrent `Get`/`Pin`/`Unpin`, eviction rate under pressure.
7. **Tests:** `bf_test.go` (concurrent pin/unpin, eviction correctness), `wtinylfu_test.go`, `sharded_bp_test.go`, `sp_test.go` (pool bounds), `of_test.go`.

## Shipped Requirements

The following requirements have been implemented and shipped; they are now part of the design baseline.

### BF / PC / SP / OF — Memory Layer

| ID | Requirement | Iteration |
|---|---|---|
| REQ000019 | `MADV_DONTNEED` hints for buffer eviction | shipped |
| REQ000020 | Buffer pool: clock-sweep LRU eviction | shipped |
| REQ000021 | Atomic `Pin`/`Unpin` with eviction gating | shipped |
| REQ000022 | O(1) hash table lookup by `blockID` | shipped |
| REQ000023 | Hint file for warm startup | shipped |
| REQ000024 | Hint file compression (gzip) when > 1 MB | **deferred** — current code writes raw binary; gzip-on-write not yet implemented |
| REQ000025 | `sync.Pool` for page/iterator buffers | shipped |
| REQ000161 | Clock-sweep integration details (atomic hand, refKey update, eviction gating) | shipped |
| REQ000199 | Sharded buffer pool mutex (reduce hash table contention) | shipped |
| REQ000302 | PMem-aware buffer pool (MADV_HUGEPAGE, PMemFile, slot tier field) | shipped |
| REQ000303 | W-TinyLFU admission policy (Count-Min Sketch, 32KB footprint) | shipped |
| REQ000304 | Off-heap large object pool (16 size classes, sync.Pool) | shipped |

## Open Issues

- Should the buffer pool use `mmap` instead of `read`/`write` for even lower overhead?
- How to handle huge databases that exceed the buffer pool? Memory-mapped files with OS-managed eviction?