# Iteration 2 — MEM (Buffer Pool)

**Subsystem:** `MEM`
**Status:** pending
**Est. LOC:** ~2,000

## Overview

In-memory cache for SST blocks. Hash table + clock-sweep LRU. Depends on FIL.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `SyncPool` interface: `Get/Put` for page-sized buffers (BlockSize) | pending |
| R02 | `SyncPool` pre-allocates via `sync.Pool` — no `make` on hot path | pending |
| R03 | `BufferPool` interface: `Get/Pin/Unpin/SetCapacity/Stats/Close` | pending |
| R04 | Hash table O(1) lookup by blockID (`sync.RWMutex` for writes) | pending |
| R05 | `bufferSlot` with pinCount (atomic), dirty (atomic), loading (atomic), wait channel | pending |
| R06 | `loading` flag prevents concurrent loads of the same block (first waiter creates, others wait on `wait`) | pending |
| R07 | `Pin`: increment pinCount atomically — page cannot be evicted while pinned | pending |
| R08 | `Unpin`: decrement pinCount atomically — eviction proceeds when pinCount == 0 | pending |
| R09 | Clock-sweep LRU: atomic hand, slot refKey updated on access, eviction gated by pinCount | pending |
| R10 | `Get`: return immediately if found, otherwise load from `BlockDevice` and insert | pending |
| R11 | Checksum verification on load from disk — return `ErrCorrupt` on mismatch | pending |
| R12 | Hint file: serialize blockID (varint) + lastAccess (varint) to `<name>.razor/hint` on close | pending |
| R13 | Hint file: read on startup and eagerly load blocks into buffer pool | pending |
| R14 | `go vet ./internal/MEM/...` zero warnings | pending |
| R15 | `go test ./internal/MEM/... -race -count=1` all green | pending |
| R16 | Benchmark: concurrent `Get/Pin/Unpin` under race detector | pending |

## Implementation

```
internal/MEM/
├── sp.go          # SyncPool, page/iterator buffer pools
├── bf.go          # BufferPool, hash table, clock-sweep LRU
├── slot.go        # bufferSlot, loading flag, wait channel
└── hint.go        # hint file serialization/deserialization
```

## Deferred

- `mmap` for lower overhead buffer management
- Hint file gzip compression (> 1 MB threshold)