# Iteration 6 — TXN/Protocol (Transaction Slot + Commit + WAL Integration)

**Subsystem:** `TXN`
**Status:** pending
**Est. LOC:** ~2,000

## Overview

Full commit protocol. Transaction slot management. Write-write conflict detection. WAL integration. Depends on TXN/MVCC.

## Dependencies

- Required: `ENG`, `LOG`, `TXN/MVCC`
- Consumed interfaces: `Store`, `Writer`, `Logger`, `VersionChain`

## Design Alignment

Directory structure matches `design/subsystems/TXN.md`:
```
internal/TXN/
└── VL/               # Validation cluster
    ├── slot.go       # TransactionSlot, free list, Begin/AllocateSlot/ReleaseSlot
    ├── slot_test.go
    ├── validate.go   # write-write conflict detection
    ├── validate_test.go
    ├── protocol.go   # commit protocol: Begin → Write → Pre-commit → Commit → Post-commit
    ├── protocol_test.go
    └── wal.go        # WAL integration: RTCommit/RTRollback/RTCheckpoint
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `TransactionSlot` array: `[MaxConcurrentTXNs=1024]`, pre-allocated, no GC pressure | pending |
| R02 | Slot status: 0=inactive, 1=active, 2=committed, 3=aborted (atomic.Int32) | pending |
| R03 | Mutex-protected free list for slot allocation (not CAS — low contention) | pending |
| R04 | `KeyRange` struct: Start ([]byte), End ([]byte, exclusive) | pending |
| R05 | `Begin(ctx) (Tx, error)`: allocate slot from free list, assign beginTS from global atomic counter | pending |
| R06 | `WriteSet`: track key ranges modified by transaction | pending |
| R07 | Write: allocate `VersionNode` from thread-local arena, CAS-insert, add key range to `writeSet` | pending |
| R08 | Pre-commit validation: scan committed slots with `commitTS > myBeginTS`, check `writeSet` overlap | pending |
| R09 | Write-write conflict: if overlap detected, abort this transaction | pending |
| R10 | `Commit(ctx) error`: assign `commitTS` (atomic counter++), CAS-update all `endTS`, write WAL `RTCommit` | pending |
| R11 | `Abort`: mark slot as aborted, discard write set, release slot to free list | pending |
| R12 | Post-commit: release slot to free list | pending |
| R13 | Savepoint: `Savepoint(name)` stores `readTS` under name in `map[string]uint64` | pending |
| R14 | `RTCommit` payload: `[commitTS:8]` | pending |
| R15 | `RTRollback` payload: `[]` (empty) | pending |
| R16 | `RTCheckpoint` payload: `[checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4][activeTXNCount:varint]` | pending |
| R17 | Simulated write-write conflict test: two concurrent txns modifying same key — one must abort | pending |
| R18 | `go vet ./internal/TXN/...` zero warnings | pending |
| R19 | `go test ./internal/TXN/... -race -count=1` all green | pending |
| R20 | Benchmark: concurrent transaction throughput with conflict rate measured | pending |

## Implementation

### Phase 1: Transaction Slot (`VL/slot.go`)

1. `transactionSlots [MaxConcurrentTXNs]transactionSlot` — pre-allocated array (no `make`)
2. `freeList []int` — indices of available slots, protected by `sync.Mutex`
3. `AllocateSlot() int`: pop from freeList, initialize slot (status=active, beginTS=atomic counter++)
4. `ReleaseSlot(idx int)`: reset slot, push idx onto freeList
5. `Begin`: call AllocateSlot, create ReadView, return Tx handle

### Phase 2: Validation (`VL/validate.go`)

1. `writeSetOverlap(a, b []KeyRange) bool`: O(n*m) check, acceptable since write sets are small
2. `Validate() bool`: iterate all slots with status=committed and commitTS > myBeginTS, check overlap
3. On conflict: call Abort, return error

### Phase 3: Commit Protocol (`VL/protocol.go`)

1. `Tx` interface: `Get/Insert/Delete/Commit/Rollback/Savepoint/RollbackTo`
2. `Insert`: allocate version node from arena, CAS-insert at chain head, track key range
3. `Commit`: Validate → assign commitTS → CAS-update endTS on all nodes → write WAL → ReleaseSlot
4. `Abort`: status=aborted, discard write set, ReleaseSlot
5. Savepoints: `savepoints map[string]uint64` in Tx, `Savepoint(name)` stores readTS, `RollbackTo(name)` restores

### Phase 4: WAL Integration (`VL/wal.go`)

1. `WriteRTCommit(txnID, commitTS uint64)` → WAL `RTCommit`
2. `WriteRTRollback(txnID uint64)` → WAL `RTRollback`
3. `WriteRTCheckpoint(cp *Checkpoint)` → WAL `RTCheckpoint`
4. Replayer calls `onCommit`/`onRollback` during recovery

## Deferred to v2

- Hazard pointers + epoch-based reclamation
- Full savepoint rollback (restore readTS from name — stub in this iteration)
- Version GC
- Generational arena