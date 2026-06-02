# Iteration 5 — TXN/MVCC (Version Chain + Per-Thread Arena)

**Subsystem:** `TXN`
**Status:** pending
**Est. LOC:** ~3,000
**Test Coverage:** 0%

## Overview

MVCC snapshot isolation for readers and serializable writes. Built on `ENG` — wraps the storage engine and adds versioning. Writers allocate version nodes via CAS. Readers traverse the version chain using hazard pointers. The validation cluster detects write-write conflicts before commit.

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
    └── slot_test.go
```

## Requirements

### MV — MVCC Cluster

| ID | Requirement | Status |
|---|---|---|
| R01 | `VersionNode` struct: `txnID uint64`, `beginTS uint64`, `endTS uint64`, `key []byte`, `value []byte`, `deleted bool`, `next atomic.Pointer[versionNode]` | pending |
| R02 | Version format: `[txnID:8][beginTS:8][endTS:8][rowData:blob]` as sequential fields in memory | pending |
| R03 | `endTS = math.MaxUint64` denotes uncommitted version; `deleted = true` is tombstone | pending |
| R04 | Lock-free insertion: new version nodes inserted via CAS on `next` pointer, no mutex in hot path | pending |
| R05 | `VersionChain` struct: head pointer, key mapping to chain head | pending |
| R06 | `GetVersionChain(key []byte) *versionNode`: return head of chain for key | pending |
| R07 | `InsertVersion(key []byte, node *versionNode) error`: CAS-insert at chain head, return error on CAS failure | pending |
| R08 | `CommitVersion(node *versionNode, commitTS uint64) bool`: CAS-update endTS from MaxUint64 to commitTS, return success | pending |
| R09 | `GCVersionChain(key []byte, oldestReadTS uint64)`: compact versions where all active txns have endTS < oldestReadTS | pending |
| R10 | Per-thread arena struct: `buf []byte`, `offset atomic.Int64`, `size int64` | pending |
| R11 | Arena `Alloc(n int) []byte`: CAS loop on offset, return slice at old offset, nil if exhausted | pending |
| R12 | Arena lazy init: allocate 1MB buffer on first Alloc, via sync.Pool for reuse | pending |
| R13 | Arena pool: `runtime.GOMAXPROCS(0)`-sized `[]*arena`, indexed by goroutine ID modulo length | pending |
| R14 | When arena exhausted: fetch new from sync.Pool or allocate; old arenas freed by epoch reclamation | pending |
| R15 | `versionNode` fields accessed only via atomic operations or within single CAS window | pending |

### LC — Lock (Hazard) Cluster

| ID | Requirement | Status |
|---|---|---|
| R16 | `hazardPointerSet` struct: `[MaxHazardPtrs]atomic.Value` storing `*versionNode` | pending |
| R17 | `MaxHazardPtrs = 2`: one slot for current read, one for next pointer | pending |
| R18 | `Publish(ptr *versionNode)`: atomic store of pointer in hazard set before dereference | pending |
| R19 | `Clear()`: clear all hazard pointer slots when read is done | pending |
| R20 | `Scan() []*versionNode`: collect all pointers currently in hazard sets (for reclamation) | pending |
| R21 | `epochManager` struct: `epoch atomic.Int64`, `threads sync.Map`, `drainCh chan struct{}` | pending |
| R22 | `threadRecord` struct: `goroutineID uint64`, `enteredAt atomic.Int64` | pending |
| R23 | `RegisterThread(goroutineID uint64)`: add thread record to sync.Map on first read | pending |
| R24 | `UnregisterThread(goroutineID uint64)`: remove thread record on read end | pending |
| R25 | `EnterEpoch() uint64`: atomically read current epoch, store in thread record, return epoch | pending |
| R26 | `ExitEpoch(goroutineID uint64)`: mark thread as having exited current epoch | pending |
| R27 | `Reclaim(batch []unsafe.Pointer)`: wait for all threads to exit old epoch, then bulk free | pending |
| R28 | Epoch advancement: background goroutine increments epoch every ~100ms or on demand | pending |
| R29 | Hazard pointer protocol: reader must Publish pointer before dereferencing any versionNode field | pending |

### SN — Snapshot Cluster

| ID | Requirement | Status |
|---|---|---|
| R30 | `versionChainSnapshot` struct: `key []byte`, `head *versionNode` (chain head at readTS) | pending |
| R31 | `readView` struct: `readTS uint64`, `snapshot []versionChainSnapshot`, `arena *arena`, `mv *MV` | pending |
| R32 | `NewReadView(mv *MV, readTS uint64) *readView`: create read view with timestamp, snapshot of chain heads | pending |
| R33 | Read view snapshot: for each key read, store copy of chain head pointer (not entire chain) | pending |
| R34 | `Get(key []byte) ([]byte, error)`: traverse chain from snapshot head, find first version where beginTS < readTS and endTS >= readTS | pending |
| R35 | Skip deleted versions: if found version has `deleted = true`, return ErrNotFound | pending |
| R36 | `Close()`: release read view's arena, deregister from epoch manager | pending |
| R37 | Visibility rule: version visible if `beginTS < readTS && endTS >= readTS` | pending |
| R38 | Uncommitted versions (endTS = MaxUint64) invisible to readers with readTS < commit time | pending |
| R39 | `Savepoint(name string)`: save current readTS and snapshot to named savepoint | pending |
| R40 | `RollbackTo(name string)`: restore readTS and snapshot from savepoint | pending |

### VL — Validation Cluster (Partial)

| ID | Requirement | Status |
|---|---|---|
| R41 | `transactionSlot` struct: `txnID uint64`, `status atomic.Int32`, `beginTS uint64`, `commitTS uint64`, `writeSet []KeyRange`, `arena *arena` | pending |
| R42 | `KeyRange` struct: `Start []byte`, `End []byte` (exclusive upper bound) | pending |
| R43 | `MaxConcurrentTXNs = 1024`: fixed-size slot array, no GC pressure | pending |
| R44 | Slot status enum: `0=inactive, 1=active, 2=committed, 3=aborted` | pending |
| R45 | `AllocateSlot() *transactionSlot`: mutex-protected free list, pop and initialize slot | pending |
| R46 | `ReleaseSlot(slot *transactionSlot)`: reset slot fields, push back to free list | pending |
| R47 | `Begin() (txn *Transaction, err)`: allocate slot, assign beginTS, create read view | pending |
| R48 | `Abort(txn *Transaction)`: mark slot status=aborted, release slot, cleanup resources | pending |
| R49 | Write-write conflict: `Validate(txn *Transaction) bool`: scan committed slots with commitTS > myBeginTS, check writeSet overlap | pending |
| R50 | `writeSetOverlap(mySet, theirSet []KeyRange) bool`: check if any key range overlaps | pending |

### Integration & API

| ID | Requirement | Status |
|---|---|---|
| R51 | `Tx` interface: `Get`, `Insert`, `Delete`, `Commit`, `Rollback`, `Savepoint`, `RollbackTo` | pending |
| R52 | `ReadView` interface: `Get`, `Close` | pending |
| R53 | `TxnManager` interface: `Begin`, `Stats` | pending |
| R54 | `Insert` allocates version node from thread-local arena, sets txnID, beginTS, endTS=MaxUint64, deleted=false | pending |
| R55 | `Delete` allocates version node with `deleted=true`, same arena allocation | pending |
| R56 | On Insert/Delete: CAS-insert version node at chain head, add key range to writeSet | pending |
| R57 | Error types: `ErrNotFound`, `ErrDuplicateKey`, `ErrTxAborted`, `ErrArenaExhausted` | pending |
| R58 | `go vet ./internal/TXN/...` zero warnings | pending |
| R59 | `go test ./internal/TXN/... -race -count=1` all green | pending |
| R60 | Benchmark: version chain concurrent insert throughput | pending |

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

func (a *arena) Alloc(n int) []byte {
    for {
        old := a.offset.Load()
        new := old + int64(n)
        if new > a.size {
            return nil // arena exhausted
        }
        if a.offset.CompareAndSwap(old, new) {
            return a.buf[old:new]
        }
    }
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

1. `MV/arena.go` — Per-thread arena: Alloc, exhaustion, sync.Pool
2. `MV/version.go` — VersionNode, VersionChain, CAS insertion, CommitVersion
3. `LC/hazard.go` — Hazard pointer set: Publish, Clear, Scan
4. `LC/epoch.go` — Epoch manager: RegisterThread, EnterEpoch, ExitEpoch, Reclaim, background goroutine
5. `SN/snapshot.go` — ReadView: creation, Get, Close, visibility rules
6. `VL/slot.go` — Transaction slot: Begin, AllocateSlot, ReleaseSlot, Abort
7. `VL/validate.go` — Validate: write-write conflict detection, writeSetOverlap
8. WAL integration (in iter-06)

## Deferred to v2

- Generational arena
- Long-running read transaction handling
- Secondary indexes
