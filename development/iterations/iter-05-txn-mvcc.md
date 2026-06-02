# Iteration 5 — TXN/MVCC (Version Chain + Per-Thread Arena)

**Subsystem:** `TXN`
**Status:** pending
**Est. LOC:** ~3,000

## Overview

MVCC version chain per key. Per-thread arena for zero-allocation writes. Snapshot isolation for readers. Depends on ENG (done), LOG.

## Dependencies

- Required: `ENG`, `LOG`
- Consumed interfaces: `Store`, `Logger`

## Design Alignment

Directory structure matches `design/subsystems/TXN.md`:
```
internal/TXN/
├── MV/               # MVCC cluster
│   ├── version.go    # VersionNode, VersionChain, CAS insertion, commit
│   ├── version_test.go
│   └── arena.go      # per-thread arena, lazy init, Alloc
├── LC/               # Lock cluster (deferred — hazard/epoch stub)
│   └── lc.go         # stub: thread registration placeholder
├── SN/               # Snapshot cluster
│   └── snapshot.go   # ReadView, Get, Close
└── VL/               # Validation cluster (preparation — slot/validate/protocol in iter-06)
    └── snapshot_test.go
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `VersionNode` format: `[txnID:8][beginTS:8][endTS:8][key:blob][value:blob][deleted:1][next:8]` | pending |
| R02 | `endTS = math.MaxUint64` denotes uncommitted version | pending |
| R03 | `deleted = true` denotes logical deletion (tombstone) | pending |
| R04 | CAS-insert new version at head of chain (no mutex in write hot path) | pending |
| R05 | CAS-update `endTS` from `MaxUint64` to `commitTS` on commit | pending |
| R06 | Per-thread arena: 1 MB default, lazy init via `sync.Pool` | pending |
| R07 | Arena `Alloc(n int) []byte`: CAS loop on offset, return slice, nil if exhausted | pending |
| R08 | Arena reused across transactions for the same goroutine | pending |
| R09 | `ReadView` struct: readTS (uint64), snapshot ([]versionChainSnapshot), arena (*arena), mv (*MV) | pending |
| R10 | `ReadView.Get(key)`: traverse chain, find first version where `beginTS < readTS` and `endTS >= readTS` | pending |
| R11 | Skip version if `deleted = true` (return `ErrNotFound`) | pending |
| R12 | Snapshot isolation: readers never see uncommitted or later-committed writes | pending |
| R13 | `ReadView.Close()`: release arena to pool, deregister | pending |
| R14 | Thread registration stub (LC cluster): `RegisterThread/UnregisterThread` no-ops for now | pending |
| R15 | `go vet ./internal/TXN/...` zero warnings | pending |
| R16 | `go test ./internal/TXN/... -race -count=1` all green | pending |
| R17 | Benchmark: concurrent version chain insert/find throughput | pending |

## Implementation

### Phase 1: Version Chain (`MV/version.go`)

1. `versionNode` struct as described — all fields laid out for zero-overhead access
2. `VersionChain`: head pointer (atomic.Pointer[versionNode])
3. `InsertVersion(key, node)`: CAS-insert at head. No mutex in hot path.
4. `CommitVersion(node, commitTS)`: CAS-update `endTS` from MaxUint64 to commitTS
5. `FindVisible(head *versionNode, readTS uint64)`: traverse chain, return first visible version

### Phase 2: Arena (`MV/arena.go`)

1. Per-thread arena pool: `[]*arena` sized by `runtime.GOMAXPROCS(0)`, index by goroutine ID % size
2. `arena` struct: `buf []byte`, `offset atomic.Int64`, `size int64`
3. `Alloc(n int)`: CAS loop on offset. Return `buf[old:old+n]`. nil if `old + n > size`.
4. `sync.Pool` for arena reuse: fetch on first transaction per goroutine, return on Close
5. On exhaustion: allocate new arena (fallback to `make([]byte, 1<<20)`)

### Phase 3: Snapshot (`SN/snapshot.go`)

1. `versionChainSnapshot`: key, head pointer
2. `readView`: readTS, snapshots, arena, mv
3. `NewReadView(mv *MV, readTS uint64)`: snapshot all version chain heads at this readTS
4. `Get(key)`: find matching snapshot, traverse chain, return visible version
5. `Close`: return arena to pool, deregister thread

## Deferred to v2

- Hazard pointers + epoch reclamation (LC cluster full implementation)
- Generational arena (vs single 1 MB allocation)
- Version GC (compact old versions where all active transactions have `endTS < oldestReadTS`)
- Long-running read transaction handling
