# Iteration 6 — TXN/Protocol (Transaction Slot + Commit + WAL)

**Subsystem:** `TXN`
**Status:** pending
**Est. LOC:** ~2,000
**Test Coverage:** 0%

## Overview

Transaction commit protocol with write-write conflict detection, transaction slot management, WAL integration, and version garbage collection. Built on iter-05 (MVCC core).

## Dependencies

- Required: `TXN/MV`, `TXN/LC`, `TXN/SN`, `WAL`
- Consumed interfaces: `WriteAheadLog`, `VersionChain`

## Design Alignment

Directory structure matches `design/subsystems/TXN.md`:
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

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | Transaction slot array: fixed-size `MaxConcurrentTXNs=1024`, mutex-protected free list | pending |
| R02 | `Begin()`: allocate slot, assign beginTS, create read view | pending |
| R03 | `AllocateSlot()`: pop from free list, initialize slot fields | pending |
| R04 | `ReleaseSlot()`: reset slot, push to free list | pending |
| R05 | Write-write conflict detection: scan slots, check writeSet overlap | pending |
| R06 | `Validate()`: for committed txn with commitTS > myBeginTS, check writeSet overlap | pending |
| R07 | `Commit()`: assign commitTS, CAS update version nodes, update slot status | pending |
| R08 | `Abort()`: mark slot aborted, return to free list | pending |
| R09 | WAL integration: write Begin, Insert, Delete, Commit, Abort records | pending |
| R10 | `Commit` record format: `[type:1][txnID:8][commitTS:8][keyCount:4][keys...]` | pending |
| R11 | Garbage collection: `Reclaim(batch)`, wait for epoch barrier, bulk free | pending |
| R12 | Epoch advancement: background goroutine increments epoch every ~100ms | pending |
| R13 | `go vet ./internal/TXN/...` zero warnings | pending |
| R14 | `go test ./internal/TXN/... -race -count=1` all green | pending |
| R15 | Benchmark: concurrent commit throughput | pending |

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
