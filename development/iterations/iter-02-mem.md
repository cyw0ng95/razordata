# Iteration 2 — MEM (Buffer Pool)

**Subsystem:** `MEM`
**Status:** pending
**Est. LOC:** ~1,350

## Overview

In-memory cache for SST blocks. Hash table + clock-sweep LRU. Depends on FIL.

## Dependencies

- Required: `FIL`
- Consumed: `*df.BlockDevice` (concrete), `lg.Logger`

## Design Alignment

Directory structure matches `design/subsystems/MEM.md`:
```
internal/MEM/
├── SP/                 # SyncPool cluster
│   ├── sp.go           # SyncPool, page/iterator buffer pools
│   └── sp_test.go
├── BF/                 # Buffer pool cluster
│   ├── bf.go           # BufferPool, hash table, clock-sweep LRU
│   ├── bf_test.go
│   └── bf_bench.go
└── PC/                 # Page cache cluster
    ├── pc.go           # bufferSlot, checksum verification, hint file
    └── pc_test.go
```

## Gap Analysis vs FIL

### Resolved: `BlockDevice` is concrete

`design/subsystems/MEM.md` says "consumed interfaces: `BlockDevice`". However, `FIL/DF` does not define a `BlockDevice` interface — it is a concrete `struct`. MEM depends on `*df.BlockDevice` directly. No interface needed for v1.

### Resolved: `ErrCorrupt` collision

Both `df.go` and `PC` (MEM) define `ErrCorrupt`. To avoid package collision, MEM defines its own error in `BF/bf.go` as `mem.ErrCorrupt`.

### Resolved: `ReadBlock` signature gap

Current `df.BlockDevice.ReadBlock(ctx, blockID, n, buf)` requires knowing `n` — the number of actual data bytes written. MEM's buffer pool has no way to know this per-block.

**Fix:** Add `ReadBlockFull(blockID uint64, buf []byte) error` to `df.go`. Reads `DataLen` bytes with checksum verification. This is the method MEM uses.

### Resolved: `FileManager` interface gap

`design/subsystems/MEM.md` says MEM "consumes `FileManager` interface". But `FIL/FS` exposes no interface type — only concrete `*fs.FileManager`. MEM does not need an interface; it uses the concrete type directly.

### Unresolved: `SetCapacity` semantics

Design says "resize the slot ring" but gives no behavior specification. **Deferred:** return `ErrCapacityExceeded` for now.

### Unresolved: Hint file lifecycle

Two lifecycle gaps identified and resolved:

1. **Who writes the hint file?** — `BufferPool.Close()` writes it. Path passed at construction.
2. **How to warm up on startup?** — `BufferPool.Warm(ctx) error` method called by SYS/ENG after construction.

## Requirements

| ID | Requirement | Status | Notes |
|---|---|---|---|
| R01 | `SyncPool` interface: `Get(size int) []byte`, `Put(buf []byte)` | pending | |
| R02 | `SyncPool` pre-allocates via `sync.Pool` — no `make` on hot path | pending | |
| R03 | `BufferPool` interface: `Get/Pin/Unpin/SetCapacity/Stats/Close` | pending | `SetCapacity` deferred to v2 |
| R04 | `Page` struct: `ID uint64`, `Data []byte` (borrowed from SP, never copied), `Dirty bool` | pending | |
| R05 | `BufferStats` struct: Hits/Misses/Pins/Evicts (atomic.Int64), Capacity/Used (int64) | pending | |
| R06 | `bufferSlot`: blockID, data ([]byte fixed-size), pinCount (atomic.Int32), dirty (atomic.Bool), refKey (atomic.Uint64), loading (atomic.Bool), wait (chan struct{}) | pending | |
| R07 | Hash table O(1) lookup by blockID (`slots map[uint64]*bufferSlot`, `sync.RWMutex`) | pending | |
| R08 | `loading` flag prevents concurrent loads of same block (first waiter creates, others wait on `wait`) | pending | |
| R09 | `Pin`: increment pinCount — page cannot be evicted while pinned | pending | |
| R10 | `Unpin`: decrement pinCount — eviction proceeds when pinCount == 0 | pending | |
| R11 | Clock-sweep LRU: atomic hand, slot refKey updated on access, eviction gated by pinCount == 0 | pending | |
| R12 | `Get`: return immediately if found (and not loading), else load from `BlockDevice` and insert | pending | uses `ReadBlockFull` |
| R13 | Checksum verification on load — return `ErrCorrupt` on mismatch | pending | |
| R14 | Hint file: `hintEntry` (BlockID varint + LastAccess varint), serialize to `<name>.razor/hint` on close | pending | |
| R15 | Hint file: read on startup, eagerly load blocks via `Warm()` method | pending | explicit method called by SYS/ENG |
| R16 | `go vet ./internal/MEM/...` zero warnings | pending | |
| R17 | `go test ./internal/MEM/... -race -count=1` all green | pending | |
| R18 | Benchmark: concurrent `Get/Pin/Unpin` under race detector | pending | |

## Implementation

### Phase 0: FIL Adjustment (`FIL/DF/df.go`)

Add `ReadBlockFull` method to `BlockDevice`:
```go
func (d *BlockDevice) ReadBlockFull(blockID uint64, buf []byte) error
```
- Reads `DataLen` bytes at `blockID * BlockSize`.
- Verifies checksum against last 4 bytes.
- Returns `ErrCorrupt` on mismatch.
- buf must be at least `DataLen` bytes.

### Phase 1: Buffer Pool (`BF/bf.go`)

Errors live in the BF cluster (no subsystem-level files allowed):
```go
var (
    ErrCorrupt          = errors.New("block checksum mismatch")
    ErrNotLoaded        = errors.New("block not yet loaded")
    ErrCapacityExceeded = errors.New("buffer pool at capacity")
)
```

`BufferPool` interface and core implementation:
1. `BufferPool` interface: `Get/Pin/Unpin/SetCapacity/Stats/Close/Warm`
2. `Get(ctx, blockID) (*Page, bool, error)`: found, wasCached, error
3. `bufferHashTable`: `slots map[uint64]*bufferSlot`, `mu sync.RWMutex`
4. `loading` flag + `wait chan struct{}` per slot for concurrent load coordination
5. Clock sweep: atomic hand, each slot's refKey updated after successful Get
6. Evict goroutine: runs continuously, wakes on insertion when at capacity
7. `Stats`: return `BufferStats` with all atomic counters
8. `Warm(ctx) error`: read hint file, call `Get` for each BlockID to eagerly load blocks

### Phase 2: SyncPool (`SP/sp.go`)

1. `syncPool` struct: `pagePool sync.Pool`, `iterPool sync.Pool`
2. `pagePool.New`: `make([]byte, BlockSize)` — pre-sized, zero allocation on `Get`
3. `iterPool.New`: `make([]byte, iterBufferSize)` — for LSM tree traversal
4. `Get(size int)`: fetch from pool if available and `cap >= size`, else `make([]byte, size)`
5. `Put(buf []byte)`: return to pool only if `cap(buf) == BlockSize` or matches iterator buffer size

### Phase 3: Page Cache (`PC/pc.go`)

1. `bufferSlot` struct: blockID, data, pinCount, dirty, refKey, loading, wait (R06)
2. `ChecksumVerify(data []byte, storedCRC uint32) bool` — CRC32 verification
3. Hint file format: `[count:varint][entry_0][entry_1]...[entry_N]` where each entry = `[blockID:varint][lastAccess:varint]`
4. `WriteHintFile(entries []hintEntry, path string) error` — serialize on `Close`
5. `ReadHintFile(path string) ([]hintEntry, error)` — deserialize on startup

### Phase 4: Tests

- `bf_test.go` — concurrent Get/Pin/Unpin, eviction correctness, loading race
- `sp_test.go` — pool bounds, no allocations on hot path
- `pc_test.go` — checksum verification (correct + corrupt), hint file round-trip (empty, single, many), hint file corruption handling

### Phase 5: Benchmarks

- `bf_bench.go` — concurrent Get/Pin/Unpin under race detector, eviction pressure

## Deferred to v2

- `SetCapacity` full implementation — return `ErrCapacityExceeded` for now
- `mmap` for lower overhead buffer management
- Hint file gzip compression (> 1 MB threshold)