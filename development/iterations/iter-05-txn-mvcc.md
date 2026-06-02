# Iteration 5 — TXN/MVCC (Version Chain + Per-Thread Arena)

**Subsystem:** `TXN`
**Status:** pending
**Est. LOC:** ~3,000
**Test Coverage:** 0%

## Overview

MVCC snapshot isolation for readers and serializable writes. Built on `ENG` — wraps the storage engine and adds versioning. Writers allocate version nodes via CAS. Readers traverse version chain using hazard pointers. The validation cluster detects write-write conflicts before commit.

## Dependencies

- Required: `ENG`, `LOG`
- Consumed interfaces: `Store`, `Iterator`

## Design Alignment

Directory structure matches `design/subsystems/TXN.md`:
```
internal/TXN/
├── MV/               # MVCC: version chain, CAS insertion, per-thread arena
│   ├── version.go    # VersionNode, VersionChain, CAS insertion, commit
│   ├── version_test.go
│   ├── arena.go      # Per-thread arena: Alloc, exhaustion, lazy init
│   └── arena_test.go
├── LC/               # Lock: hazard pointers, epoch-based reclamation
│   ├── hazard.go     # Hazard pointer set: Publish, Clear, Scan
│   ├── hazard_test.go
│   ├── epoch.go      # Epoch manager: EnterEpoch, ExitEpoch, Reclaim
│   └── epoch_test.go
├── SN/               # Snapshot: read view management per session
│   ├── snapshot.go   # ReadView: creation, Get, Close
│   └── snapshot_test.go
└── VL/               # Validation: commit protocol, write-write conflict detection
    ├── slot.go       # Transaction slot: Begin, AllocateSlot, ReleaseSlot
    ├── slot_test.go
    ├── validate.go    # Validate: write-write conflict detection
    ├── validate_test.go
    ├── protocol.go    # Full commit protocol
    └── protocol_test.go
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `VersionNode` struct: txnID, beginTS, endTS, key, value, deleted, next | pending |
| R02 | `VersionChain`: GetVersionChain, InsertVersion via CAS, CommitVersion | pending |
| R03 | Per-thread arena: `Alloc` with CAS, exhaustion handling, lazy init via `sync.Pool` | pending |
| R04 | Hazard pointer set: `Publish`, `Clear`, `Scan` | pending |
| R05 | Epoch manager: `EnterEpoch`, `ExitEpoch`, `Reclaim`, background goroutine | pending |
| R06 | `ReadView`: creation with readTS, `Get` traverses chain, `Close` releases resources | pending |
| R07 | `TransactionSlot`: allocate from fixed array, free list with mutex | pending |
| R08 | Begin: allocate slot, assign beginTS, create read view | pending |
| R09 | Read: publish head to hazard, traverse chain, find visible version | pending |
| R10 | Write: allocate version node from arena, CAS insert into chain | pending |
| R11 | Pre-commit validation: scan slots, check write-write conflicts | pending |
| R12 | Commit: assign commitTS, CAS update endTS, update slot status | pending |
| R13 | Abort: mark slot aborted, return to free list | pending |
| R14 | WAL integration: write Commit record on post-commit | pending |
| R15 | `go vet ./internal/TXN/...` zero warnings | pending |
| R16 | `go test ./internal/TXN/... -race -count=1` all green | pending |
| R17 | Benchmark: version chain insert/commit throughput | pending |

## Data Structures

### VersionNode

```go
type versionNode struct {
    txnID    uint64
    beginTS  uint64
    endTS    uint64  // math.MaxUint64 = uncommitted
    key      []byte  // the key this version belongs to
    value    []byte  // the row data
    deleted  bool    // true if this is a deletion marker (tombstone)
    next     atomic.Pointer[versionNode] // next older version
}
```

### Per-Thread Arena

```go
type arena struct {
    buf    []byte
    offset atomic.Int64
    size   int64
}
```

### Hazard Pointer

```go
type hazardPointerSet struct {
    ptrs [MaxHazardPtrs]atomic.Value // stores *versionNode
}

const MaxHazardPtrs = 2 // one for current read, one for next
```

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

const MaxConcurrentTXNs = 1024
```

## Commit Protocol

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

6. Post-commit:
   - release transaction slot
   - write Commit record to WAL
```

## Implementation Order

1. `MV/version.go` — VersionNode, VersionChain, CAS insertion, CommitVersion
2. `MV/arena.go` — Per-thread arena: Alloc, exhaustion handling, lazy initialization
3. `LC/hazard.go` — Hazard pointer set: Publish, Clear, Scan
4. `LC/epoch.go` — Epoch manager: EnterEpoch, ExitEpoch, Reclaim, background goroutine
5. `SN/snapshot.go` — ReadView: creation, Get, Close
6. `VL/slot.go` — Transaction slot: Begin, AllocateSlot, ReleaseSlot
7. `VL/validate.go` — Validate: write-write conflict detection, Commit, Abort
8. `VL/protocol.go` — Full commit protocol integration
9. WAL integration tests

## Deferred to v2

- Generational arena (vs single large allocation)
- Long-running read transaction handling
- Secondary indexes
