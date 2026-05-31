# Iteration 8 — TXN/Protocol (Transaction Slot + Commit + WAL Integration)

**Subsystem:** `TXN`
**Status:** pending
**Est. LOC:** ~2,000

## Overview

Full commit protocol. Transaction slot management. Write-write conflict detection. WAL integration. Depends on TXN/MVCC.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `TransactionSlot` array: `[MaxConcurrentTXNs=1024]`, pre-allocated, no GC pressure | pending |
| R02 | Mutex-protected free list for slot allocation (not CAS — low contention) | pending |
| R03 | `Begin`: allocate slot from free list, assign `beginTS` from global atomic counter | pending |
| R04 | `WriteSet`: track key ranges modified by transaction | pending |
| R05 | Write: allocate `VersionNode` from thread-local arena, CAS-insert, add key range to `writeSet` | pending |
| R06 | Pre-commit validation: scan committed slots with `commitTS > myBeginTS`, check `writeSet` overlap | pending |
| R07 | Write-write conflict: if overlap detected, abort this transaction | pending |
| R08 | `Commit`: assign `commitTS` (atomic counter++), CAS-update all `endTS`, write WAL `RTCommit` | pending |
| R09 | `Abort`: mark slot as aborted, discard write set, release slot to free list | pending |
| R10 | Post-commit: release slot to free list | pending |
| R11 | Savepoint stub: `Savepoint(name)` stores `readTS` under name | pending |
| R12 | `RTCommit` payload: `[commitTS:8]` | pending |
| R13 | `RTRollback` payload: `[]` (empty) | pending |
| R14 | `RTCheckpoint` payload: `[checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4][activeTXNCount:varint]` | pending |
| R15 | Simulated write-write conflict test: two concurrent txns modifying same key — one must abort | pending |
| R16 | `go vet ./internal/TXN/...` zero warnings | pending |
| R17 | `go test ./internal/TXN/... -race -count=1` all green | pending |
| R18 | Benchmark: concurrent transaction throughput with conflict rate measured | pending |

## Implementation

```
internal/TXN/
├── slot.go        # TransactionSlot, free list, Begin/AllocateSlot/ReleaseSlot
├── validate.go    # write-write conflict detection
├── protocol.go    # commit protocol: Begin → Write → Pre-commit → Commit → Post-commit
├── wal.go         # WAL integration: RTCommit/RTRollback/RTCheckpoint
└── savepoint.go   # Savepoint stub
```

## Deferred

- Hazard pointers + epoch-based reclamation
- Full savepoint rollback (restore readTS from name)
- Version GC
- Generational arena