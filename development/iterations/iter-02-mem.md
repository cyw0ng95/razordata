# Iteration 2 — MEM (Buffer Pool)

**Subsystem:** `MEM`
**Status:** pending
**Est. LOC:** ~2,000

## Overview

In-memory cache for SST blocks. Hash table + clock-sweep LRU. Depends on FIL.

## Dependencies

- Required: `FIL`
- Consumed interfaces: `BlockDevice`, `FileManager`

## Design Alignment

Directory structure matches `design/subsystems/MEM.md`:
```
internal/MEM/
├── SP/               # SyncPool cluster
│   ├── sp.go         # SyncPool, page/iterator buffer pools
│   └── sp_test.go
├── BF/               # Buffer pool cluster
│   ├── bf.go         # BufferPool, hash table, clock-sweep LRU
│   ├── bf_test.go
│   └── bf_bench.go
└── PC/               # Page cache cluster
    ├── pc.go         # bufferSlot, loading flag, wait channel, hint file
    └── pc_test.go
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `SyncPool` interface: `Get(size int) []byte`, `Put(buf []byte)` | pending |
| R02 | `SyncPool` pre-allocates via `sync.Pool` — no `make` on hot path | pending |
| R03 | `BufferPool` interface: `Get/Pin/Unpin/SetCapacity/Stats/Close` | pending |
| R04 | `Page` struct: `ID uint64`, `Data []byte` (borrowed from SP, never copied), `Dirty bool` | pending |
| R05 | `BufferStats` struct: Hits/Misses/Pins/Evicts (atomic.Int64), Capacity/Used (int64) | pending |
| R06 | `bufferSlot`: blockID, data ([]byte fixed-size), pinCount (atomic.Int32), dirty (atomic.Bool), refKey (atomic.Uint64), loading (atomic.Bool), wait (chan struct{}) | pending |
| R07 | Hash table O(1) lookup by blockID (`slots map[uint64]*bufferSlot`, `sync.RWMutex`) | pending |
| R08 | `loading` flag prevents concurrent loads of same block (first waiter creates, others wait on `wait`) | pending |
| R09 | `Pin`: increment pinCount — page cannot be evicted while pinned | pending |
| R10 | `Unpin`: decrement pinCount — eviction proceeds when pinCount == 0 | pending |
| R11 | Clock-sweep LRU: atomic hand, slot refKey updated on access, eviction gated by pinCount == 0 | pending |
| R12 | `Get`: return immediately if found (and not loading), else load from `BlockDevice` and insert | pending |
| R13 | Checksum verification on load — return `ErrCorrupt` on mismatch | pending |
| R14 | Hint file: `hintEntry` (BlockID varint + LastAccess varint), serialize to `<name>.razor/hint` on close | pending |
| R15 | Hint file: read on startup, eagerly load blocks into buffer pool | pending |
| R16 | `go vet ./internal/MEM/...` zero warnings | pending |
| R17 | `go test ./internal/MEM/... -race -count=1` all green | pending |
| R18 | Benchmark: concurrent `Get/Pin/Unpin` under race detector | pending |

## Implementation

### Phase 1: SyncPool (`SP/sp.go`)

1. `syncPool` struct: `pagePool sync.Pool`, `iterPool sync.Pool`
2. `pagePool.New`: `make([]byte, BlockSize)` — pre-sized, zero allocation on `Get`
3. `iterPool.New`: `make([]byte, iterBufferSize)` — for LSM tree traversal
4. `Get(size int)`: fetch from pool if available and `cap >= size`, else `make([]byte, size)`
5. `Put(buf []byte)`: return to pool only if `cap(buf) == BlockSize` or matches iterator buffer size

### Phase 2: BufferPool (`BF/bf.go`)

1. `BufferPool` interface: `Get/Pin/Unpin/SetCapacity/Stats/Close`
2. `Get(ctx, blockID) (*Page, bool, error)`: found, wasCached, error
3. `bufferHashTable`: `slots map[uint64]*bufferSlot`, `mu sync.RWMutex`
4. Lock on write (eviction, insertion), lock on read only if key not found (then load)
5. `loading` flag: `atomic.Bool` on slot. First goroutine to load acquires write lock; others wait on `wait` channel
6. `wait` channel: created on first load, closed when data ready. Waiting goroutines receive nil and retry Get
7. Clock sweep: atomic hand, each slot's refKey updated after successful Get. Eviction: check `refKey < hand - N`, gated by `pinCount == 0`
8. `Stats`: return `BufferStats` with all atomic counters

### Phase 3: PageCache (`PC/pc.go`)

1. `bufferSlot` struct as described above
2. Checksum verification on block load from `BlockDevice`: compute CRC32, compare with stored last 4 bytes
3. Hint file format: `[count:varint][entry_0][entry_1]...[entry_N]` where each entry = `[blockID:varint][lastAccess:varint]`
4. Serialize on `Close`: iterate all slots, write to hint file
5. Deserialize on startup: read hint file, call `Get` for each BlockID to eagerly load

## Deferred to v2

- `mmap` for lower overhead buffer management
- Hint file gzip compression (> 1 MB threshold)