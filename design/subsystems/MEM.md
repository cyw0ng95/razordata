# MEM — Memory Layer

## Overview

Buffer pool that caches SST blocks in memory. All reads from and writes to the LSM tree go through here. Uses clock-sweep LRU eviction with atomic pin/unpin. On clean shutdown, serializes the hot working set to a hint file for fast startup warm-up. Depends on `FIL`.

## Dependencies

- Required: `FIL`
- Consumed interfaces: `BlockDevice`, `FileManager`, `PathValidator`

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

### ClockSweep

```go
type clockSweep struct {
    hand    atomic.Uint64       // global clock hand
    slots   []*bufferSlot       // ring buffer of all slots
    capacity int64
}
```

- Global `hand` is atomically incremented after each eviction attempt.
- Each slot has a `refKey` (initially the hand value at last access). When the hand passes the `refKey`, the slot becomes a candidate.
- Candidate is evicted if `pinCount == 0` and `dirty == false`. If `dirty == true`, the slot is flushed to disk first.
- No element movement in the ring — only `refKey` values change. O(1) per eviction tick.

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
    BlockID uint64
    LastAccess int64 // Unix timestamp
}
```

- Serialized as a binary list: `[count:varint][entry_0][entry_1]...[entry_N]`.
- Written on clean `Close()` via `Engine.Close`.
- On startup, read from `<name>.razor/hint` and eagerly loaded into the buffer pool before serving queries.
- `HintFile` is colocated in `MEM/PC` as it is the warm-up mechanism for the buffer pool.

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `BF` | Buffer pool: LRU eviction (clock-sweep), pin/unpin, hash table lookup, miss handling |
| `PC` | Page cache: block slot management, checksum verification on read, hint file |
| `SP` | Sync pool: goroutine-safe object pooling for pages and iterators |

## Clusters

### BF — Buffer Pool

**Responsibility:** LRU eviction, clock-sweep, pin/unpin, hash table lookup.

**Key behaviors:**
- `Get`: look up in hash table. If found and not loading, return immediately. If not found, load from `FIL/DF` (via `BlockDevice.ReadBlock`), insert into hash table, return.
- `Pin`: increment `pinCount`. Page cannot be evicted while `pinCount > 0`.
- `Unpin`: decrement `pinCount`. Eviction may proceed once `pinCount == 0`.
- `SetCapacity`: resize the slot ring. If shrinking, evict oldest clean slots.
- `Stats`: `Hits` / `Misses` ratio, `Pins`, `Evicts`, capacity, used.

### PC — Page Cache

**Responsibility:** In-memory block slots, checksum verification on read, hint file warm-up.

**Key behaviors:**
- When a block is loaded from disk, its checksum is verified against the stored CRC32.
- On checksum mismatch, the block is returned as `ErrCorrupt` and the slot is marked invalid.
- Hint file: on clean close, serialize all slots with `LastAccess > 0` to `<name>.razor/hint`.
- On startup: read hint file, call `Get` for each `BlockID` to eagerly load into buffer pool.

### SP — Sync Pool

**Responsibility:** Goroutine-safe object pooling for page buffers and iterator objects.

**Key behaviors:**
- `Get(size int) []byte`: fetch from pool if available and `len >= size`; otherwise `make([]byte, size)`.
- `Put(buf []byte)`: return to pool if `cap(buf) == BlockSize` or `cap(buf)` matches a known iterator buffer size; otherwise drop.
- All `New` functions pre-allocate to avoid per-request `make` calls.

## Implementation Plan

1. **`internal/MEM/SP/sp.go`** — `SyncPool` implementation with `sync.Pool` for page-sized and iterator-sized buffers.
2. **`internal/MEM/BF/bf.go`** — buffer pool with hash table, clock-sweep eviction, `Get`/`Pin`/`Unpin`/`SetCapacity`/`Stats`.
3. **`internal/MEM/PC/pc.go`** — page slot management, checksum verification, hint file serialization/deserialization.
4. **`internal/MEM/BF/bf_bench.go`** — benchmarks: concurrent `Get`/`Pin`/`Unpin`, eviction rate under pressure.
5. **Tests:** `bf_test.go` (concurrent pin/unpin, eviction correctness), `pc_test.go` (hint file round-trip, checksum), `sp_test.go` (pool bounds).

## Open Issues

- Should the buffer pool use `mmap` instead of `read`/`write` for even lower overhead?
- How to handle huge databases that exceed the buffer pool? Memory-mapped files with OS-managed eviction?
- Should `HintFile` be compressed (gzip) to reduce startup I/O for large working sets?