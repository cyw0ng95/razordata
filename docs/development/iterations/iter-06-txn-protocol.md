# Iteration 6 — TXN/Protocol (Transaction Slot + Commit + WAL)

**Subsystem:** `TXN`
**Status:** done
**Est. LOC:** ~2,000
**Test Coverage:** 89.5% (VL), 98% (LC), 89.6% (MV), 82.4% (SN)

## Overview

Transaction commit protocol with write-write conflict detection, transaction slot management, WAL integration, and version garbage collection. Built on iter-05 (MVCC core).

## Dependencies

- Required: `TXN/MV`, `TXN/LC`, `TXN/SN`, `WAL`
- Consumed interfaces: `WriteAheadLog`, `VersionChain`

## Design Alignment

Directory structure matches `docs/design/subsystems/TXN.md`:
```
internal/TXN/
├── VL/               # Validation: commit protocol, write-write conflict detection
│   ├── slot.go       # Transaction slot: Begin, AllocateSlot, ReleaseSlot
│   ├── slot_test.go
│   ├── validate.go   # Validate: write-write conflict detection
│   ├── validate_test.go
│   ├── protocol.go   # Full commit protocol integration
│   ├── protocol_test.go
│   ├── gc.go         # Garbage collection of obsolete version nodes
│   └── gc_test.go
```

## Design Corrections Applied

Per `docs/design/subsystems/TXN.md` §TransactionSlot and §Commit Protocol:

1. `transactionSlot.status` MUST be `atomic.Int32` (not plain `int32`)
2. `transactionSlot` MUST have `arena *arena` field
3. Global monotonic `txnCounter` (atomic Uint64) for beginTS/commitTS assignment
4. `protocol.go` implements `Begin()`, `Commit()`, `Abort()` per commit protocol
5. WAL integration: write Begin, Insert, Delete, Commit, Abort records via `WAL/Writer`
6. `gc.go` implements epoch-based version reclamation

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | Global `txnCounter` atomic Uint64, `NextTS() uint64` for beginTS/commitTS | done |
| R02 | `transactionSlot.status` as `atomic.Int32` | done |
| R03 | `transactionSlot.arena *arena` field for per-txn allocation | done |
| R04 | `Begin(ctx) (Tx, error)`: allocate slot, assign beginTS, create read view, write WAL Begin | done |
| R05 | `Insert(ctx, key, value)`: allocate VersionNode, CAS insert, add KeyRange, write WAL Insert | done |
| R06 | `Delete(ctx, key)`: allocate VersionNode (deleted=true), CAS insert, add KeyRange, write WAL Delete | done |
| R07 | `Get(ctx, key) ([]byte, error)`: read via MV snapshot | done |
| R08 | `Validate(slot) bool`: write-write conflict detection, scan committed slots | done |
| R09 | `Commit(ctx) error`: assign commitTS, CAS update version nodes, update slot, write WAL Commit | done |
| R10 | `Abort(ctx) error`: mark slot aborted, write WAL Abort | done |
| R11 | WAL record types: `Begin=1, Insert=2, Delete=3, Commit=4, Abort=5` | done |
| R12 | `Commit` WAL record format: `[type:1][txnID:8][commitTS:8][keyCount:4][keys...]` | done |
| R13 | `gc.go`: `ReclaimVersionNodes(batch)`, epoch barrier wait, bulk free | done |
| R14 | Background epoch advancement goroutine: `Start()`, `Stop()`, ~100ms interval | done |
| R15 | `go vet ./internal/TXN/...` zero warnings | done |
| R16 | `go test ./internal/TXN/... -race -count=1` all green | done |
| R17 | Benchmark: concurrent commit throughput | done |

## Commit Protocol (Full)

```
1. Begin:
   - allocate slot from pre-allocated array
   - assign beginTS from global atomic counter
   - take snapshot of all version chain heads (ReadView)

2. Read:
   - for each Get(key):
     a. get version chain head
     b. publish head to hazard pointer
     c. traverse chain, find first version where beginTS < readTS and endTS >= readTS
     d. if found and not deleted, return value
     e. else return ErrNotFound

3. Write (Insert/Delete):
   - allocate version node from thread-local arena
   - set txnID, beginTS, endTS=MaxUint64, key, value, deleted flag
   - insert into version chain via CAS on head pointer
   - add key range to writeSet
   - write WAL record (Insert/Delete)

4. Pre-commit (Validation):
   - scan all transaction slots
   - for any committed transaction with commitTS > myBeginTS:
     - check if any key in my writeSet overlaps with their writeSet
   - if overlap found: abort this transaction

5. Commit:
   - assign commitTS = atomic counter++
   - for each version node in writeSet:
     - CAS update endTS from MaxUint64 to commitTS
   - update slot status to committed
   - write Commit record to WAL

6. Post-commit:
   - release transaction slot
   - write Commit record to WAL
```

## Key Data Structures

### TransactionSlot

```go
type transactionSlot struct {
    txnID     uint64
    status    atomic.Int32 // 0=inactive, 1=active, 2=committed, 3=aborted
    beginTS   uint64
    commitTS  uint64
    writeSet  []KeyRange
    arena     *arena
}

type KeyRange struct {
    Start []byte
    End   []byte // exclusive
}

const MaxConcurrentTXNs = 1024
```

### WAL Records

```go
const (
    WALRecordBegin   = 1
    WALRecordInsert  = 2
    WALRecordDelete  = 3
    WALRecordCommit  = 4
    WALRecordAbort   = 5
)
```

## Implementation Order

1. `VL/slot.go` — Transaction slot array, free list, Begin/End
2. `VL/validate.go` — Write-write conflict detection, Validate
3. `VL/protocol.go` — Full commit protocol integration
4. WAL integration — write Commit/Abort records
5. `VL/gc.go` — Epoch-based reclamation, Reclaim batch
6. Tests for concurrent commits, conflicts, aborts

## Deferred to v2

- Generational arena
- Long-running read transaction handling
- Secondary indexes
