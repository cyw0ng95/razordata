# Iteration 7 — TXN/MVCC (Version Chain + Per-Thread Arena)

**Subsystem:** `TXN`
**Status:** pending
**Est. LOC:** ~3,000

## Overview

MVCC version chain per key. Per-thread arena for zero-allocation writes. Snapshot isolation for readers. Depends on ENG, LOG.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `VersionNode` format: `[txnID:8][beginTS:8][endTS:8][key:blob][value:blob][deleted:1][next:8]` | pending |
| R02 | `endTS = math.MaxUint64` denotes uncommitted version | pending |
| R03 | `deleted = true` denotes logical deletion (tombstone) | pending |
| R04 | CAS-insert new version at head of chain (no mutex in write hot path) | pending |
| R05 | CAS-update `endTS` from `MaxUint64` to `commitTS` on commit | pending |
| R06 | Per-thread arena: 1 MB default, lazy init via `sync.Pool` | pending |
| R07 | Arena `Alloc`: CAS loop on offset, return slice, nil if exhausted | pending |
| R08 | Arena reused across transactions for the same goroutine | pending |
| R09 | `ReadView` struct: `readTS`, snapshot of version chain heads, arena | pending |
| R10 | `ReadView.Get`: traverse chain, find first version where `beginTS < readTS` and `endTS >= readTS` | pending |
| R11 | Skip version if `deleted = true` (return `ErrNotFound`) | pending |
| R12 | Snapshot isolation: readers never see uncommitted or later-committed writes | pending |
| R13 | `Close` on ReadView: release arena to pool, deregister | pending |
| R14 | `go vet ./internal/TXN/...` zero warnings | pending |
| R15 | `go test ./internal/TXN/... -race -count=1` all green | pending |
| R16 | Benchmark: concurrent version chain insert/find throughput | pending |

## Implementation

```
internal/TXN/
├── version.go     # VersionNode, VersionChain, CAS insertion, commit
├── arena.go       # per-thread arena, lazy init, Alloc
└── snapshot.go    # ReadView, Get, Close
```

## Deferred

- Hazard pointers + epoch reclamation
- Generational arena (vs single large allocation)
- Version GC (compact old versions)
- Long-running read transaction handling