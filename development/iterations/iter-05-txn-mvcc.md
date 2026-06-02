# Iteration 5 — TXN/MVCC (Version Chain + Per-Thread Arena)

**Subsystem:** `TXN`
**Status:** in_progress
**Est. LOC:** ~3,000
**Test Coverage:** ~75% (MV/LC/SN)

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
│   ├── version.go     # VersionNode, VersionChain, CAS insertion, commit
│   ├── version_test.go
│   ├── arena.go       # Per-thread arena: Alloc, exhaustion, lazy init
│   ├── arena_test.go
│   ├── mv.go          # MV struct: GetVersionChain, GetOrCreateVersionChain, Insert
│   └── errors.go      # ErrNotFound, ErrDuplicateKey, ErrTxAborted, etc.
├── LC/               # Lock: hazard pointers, epoch-based reclamation
│   ├── hazard.go      # Hazard pointer set: Publish, Clear, Scan
│   ├── hazard_test.go
│   ├── epoch.go       # Epoch manager: EnterEpoch, ExitEpoch, Reclaim
│   └── epoch_test.go
├── SN/               # Snapshot: read view management per session
│   ├── snapshot.go    # ReadView: creation, Get, Close
│   └── snapshot_test.go
└── VL/               # Validation: commit protocol, write-write conflict detection
    ├── slot.go       # Transaction slot: Begin, AllocateSlot, ReleaseSlot
    └── slot_test.go
```

## Requirements

### MV — MVCC Cluster

| ID | Requirement | Status |
|---|---|---|
| R01 | `VersionNode` struct: `txnID uint64`, `beginTS uint64`, `endTS uint64`, `key []byte`, `value []byte`, `deleted bool`, `next atomic.Pointer[versionNode]` | done |
| R02 | Version format: `[txnID:8][beginTS:8][endTS:8][rowData:blob]` as sequential fields in memory | done |
| R03 | `endTS = math.MaxUint64` denotes uncommitted version; `deleted = true` is tombstone | done |
| R04 | Lock-free insertion: new version nodes inserted via CAS on `next` pointer, no mutex in hot path | done |
| R05 | `VersionChain` struct: head pointer, key mapping to chain head | done |
| R06 | `GetVersionChain(key []byte) *VersionNode`: return head of chain for key | done |
| R07 | `InsertVersion(key []byte, node *VersionNode) error`: CAS-insert at chain head, return error on CAS failure | done |
| R08 | `CommitVersion(node *VersionNode, commitTS uint64) bool`: CAS-update endTS from MaxUint64 to commitTS, return success | done |
| R09 | `GCVersionChain(key []byte, oldestReadTS uint64)`: compact versions where all active txns have endTS < oldestReadTS | pending |
| R10 | Per-thread arena struct: `buf []byte`, `offset atomic.Int64`, `size int64` | done |
| R11 | Arena `Alloc(n int) []byte`: CAS loop on offset, return slice at old offset, nil if exhausted | done |
| R12 | Arena lazy init: allocate 1MB buffer on first Alloc, via sync.Pool for reuse | done |
| R13 | Arena pool: `runtime.GOMAXPROCS(0)`-sized `[]*arena`, indexed by goroutine ID modulo length | done |
| R14 | When arena exhausted: fetch new from sync.Pool or allocate; old arenas freed by epoch reclamation | done |
| R15 | `VersionNode` fields accessed only via atomic operations or within single CAS window | done |

### LC — Lock (Hazard) Cluster

| ID | Requirement | Status |
|---|---|---|
| R16 | `hazardPointerSet` struct: `[MaxHazardPtrs]atomic.Value` storing `*versionNode` | done |
| R17 | `MaxHazardPtrs = 2`: one slot for current read, one for next pointer | done |
| R18 | `Publish(ptr unsafe.Pointer)`: atomic store of pointer in hazard set before dereference | done |
| R19 | `Clear()`: clear all hazard pointer slots when read is done | done |
| R20 | `Scan() []unsafe.Pointer`: collect all pointers currently in hazard sets (for reclamation) | done |
| R21 | `epochManager` struct: `epoch atomic.Int64`, `threads sync.Map`, `drainCh chan struct{}` | done |
| R22 | `threadRecord` struct: `goroutineID uint64`, `enteredAt atomic.Int64` | done |
| R23 | `RegisterThread(goroutineID uint64)`: add thread record to sync.Map on first read | done |
| R24 | `UnregisterThread(goroutineID uint64)`: remove thread record on read end | done |
| R25 | `EnterEpoch() uint64`: atomically read current epoch, store in thread record, return epoch | done |
| R26 | `ExitEpoch(goroutineID uint64)`: mark thread as having exited current epoch | done |
| R27 | `Reclaim(batch []unsafe.Pointer)`: wait for all threads to exit old epoch, then bulk free | done |
| R28 | Epoch advancement: background goroutine increments epoch every ~100ms or on demand | done |
| R29 | Hazard pointer protocol: reader must Publish pointer before dereferencing any versionNode field | done |

### SN — Snapshot Cluster

| ID | Requirement | Status |
|---|---|---|
| R30 | `versionChainSnapshot` struct: `key []byte`, `head *versionNode` (chain head at readTS) | done |
| R31 | `readView` struct: `readTS uint64`, `snapshot []versionChainSnapshot`, `arena *arena`, `mv *MV` | done |
| R32 | `NewReadView(mv *MV, readTS uint64) *readView`: create read view with timestamp, snapshot of chain heads | done |
| R33 | Read view snapshot: for each key read, store copy of chain head pointer (not entire chain) | done |
| R34 | `Get(key []byte) ([]byte, error)`: traverse chain from snapshot head, find first version where beginTS < readTS and endTS >= readTS | done |
| R35 | Skip deleted versions: if found version has `deleted = true`, return ErrNotFound | done |
| R36 | `Close()`: release read view's arena, deregister from epoch manager | done |
| R37 | Visibility rule: version visible if `beginTS < readTS && endTS >= readTS` | done |
| R38 | Uncommitted versions (endTS = MaxUint64) invisible to readers with readTS < commit time | done |
| R39 | `Savepoint(name string)`: save current readTS and snapshot to named savepoint | pending |
| R40 | `RollbackTo(name string)`: restore readTS and snapshot from savepoint | pending |

### VL — Validation Cluster

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
| R52 | `ReadView` interface: `Get`, `Close` | done |
| R53 | `TxnManager` interface: `Begin`, `Stats` | pending |
| R54 | `Insert` allocates version node from thread-local arena, sets txnID, beginTS, endTS=MaxUint64, deleted=false | pending |
| R55 | `Delete` allocates version node with `deleted=true`, same arena allocation | pending |
| R56 | On Insert/Delete: CAS-insert version node at chain head, add key range to writeSet | pending |
| R57 | Error types: `ErrNotFound`, `ErrDuplicateKey`, `ErrTxAborted`, `ErrArenaExhausted` | done |
| R58 | `go vet ./internal/TXN/...` zero warnings | pending |
| R59 | `go test ./internal/TXN/... -race -count=1` all green | pending |
| R60 | Benchmark: version chain concurrent insert throughput | pending |

## Commits

- `1914e98` - feat(TXN/MV): implement MV cluster - arena, version chain, CAS insertion
- `c0b123c` - feat(TXN/LC): implement LC cluster - hazard pointers, epoch manager
- `6089df2` - feat(TXN): add SN snapshot and MV core
- `663b3f0` - test(TXN/SN): add snapshot tests with race safety

## Deferred to v2

- Generational arena
- Long-running read transaction handling
- Secondary indexes
- Savepoint/RollbackTo (R39-R40)
