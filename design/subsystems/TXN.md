# TXN — Transaction Layer

## Overview

Provides MVCC snapshot isolation for readers and serializable writes. Built on top of `ENG` — it wraps the storage engine and adds versioning. Writers allocate version nodes via CAS. Readers traverse the version chain using hazard pointers. The validation cluster detects write-write conflicts before commit. Depends on `ENG` and `LOG`.

## Dependencies

- Required: `ENG`, `LOG`
- Consumed interfaces: `Store`, `Iterator`

## Exposed Interfaces

```go
// Tx is a transaction handle
type Tx interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
    Insert(ctx context.Context, key, value []byte) error
    Delete(ctx context.Context, key []byte) error
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
    Savepoint(ctx context.Context, name string) error
    RollbackTo(ctx context.Context, name string) error
}

// ReadView is a snapshot of the database at a point in time
type ReadView interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
    Close()
}

// TxnManager creates and manages transactions
type TxnManager interface {
    Begin(ctx context.Context) (Tx, error)
    Stats() TxnStats
}
```

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

- Each primary key entry in `ENG` points to a singly-linked list of version nodes (newest first).
- Version format: `[txnID:uint64][beginTS:uint64][endTS:uint64][rowData:blob]`
- `beginTS` and `endTS` are logical timestamps from a monotonic counter.
- `endTS = math.MaxUint64` denotes an uncommitted version.
- `deleted = true` means this is a tombstone (logical deletion).
- Lock-free insertion: new version nodes are allocated from a per-thread arena, inserted via CAS on the `next` pointer. No mutex needed for writes.

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

- Each goroutine has a thread-local arena (via `sync.Map` keyed by goroutine ID, initialized lazily).
- Typically 1 MB per arena. Version nodes are allocated from the arena via pointer arithmetic — no `new` or `make` in the hot path.
- When an arena is exhausted, a new one is allocated. Old arenas are freed by the epoch reclamation pass.

### Hazard Pointer

```go
type hazardPointerSet struct {
    ptrs [MaxHazardPtrs]atomic.Value // stores *versionNode
}

const MaxHazardPtrs = 2 // one for current read, one for next
```

- Each reader goroutine holds a local `hazardPointerSet`.
- Before dereferencing a version node pointer, the reader publishes it to its hazard pointer set via `atomic.Store`.
- The reclamation pass scans all registered hazard pointers before freeing any node.
- If a node is in any hazard pointer set, it is not reclaimed.

### Epoch Manager

```go
type epochManager struct {
    epoch      atomic.Int64
    threads    sync.Map // map[goroutineID]*threadRecord
    drainCh    chan struct{}
}

type threadRecord struct {
    goroutineID uint64
    enteredAt   atomic.Int64 // epoch when this thread entered
}
```

- Global epoch counter incremented by the epoch manager goroutine (every ~100 ms or on demand).
- Each reader thread registers with the epoch manager on first read and deregisters on exit.
- `EnterEpoch()`: atomically read the current epoch, store it in the thread record.
- `ExitEpoch()`: mark the thread as having exited the epoch.
- `Reclaim(batch []unsafe.Pointer)`: wait for all registered threads to exit the old epoch, then free the batch in bulk.
- This guarantees that no reader is mid-read on a freed node.

### ReadView

```go
type readView struct {
    readTS    uint64
    snapshot  *versionChainSnapshot // snapshot of the version chain head
    arena     *arena
    mv        *MV
}
```

- A `ReadView` is created when a transaction begins.
- Contains `readTS` (the transaction's start timestamp) and a snapshot of the version chain heads.
- Readers traverse the version chain, skipping entries where `endTS < readTS` or `beginTS >= readTS` (deleted/tombstone versions).
- The hazard pointer protocol ensures readers never see a node that is being reclaimed.
- `Close()` releases the arena and deregisters from the epoch manager.

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

- On transaction begin, a slot is allocated from a pre-allocated fixed-size array (no GC pressure). `MaxConcurrentTXNs = 1024`.
- `status`: 0=inactive, 1=active, 2=committed, 3=aborted.
- `writeSet` tracks the key ranges modified by this transaction.
- Write-write conflict detection: before commit, check that no other committed transaction with `commitTS > myBeginTS` modified any key in `writeSet`.

### Commit Protocol

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

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `MV` | MVCC: version chain, CAS insertion, version format, per-thread arena |
| `LC` | Lock: hazard pointers, epoch-based reclamation, read coordination |
| `SN` | Snapshot: read view management per session, epoch registration |
| `VL` | Validation: commit protocol, write-write conflict detection, transaction slot management |

## Clusters

### MV — MVCC

**Responsibility:** Version chain management, CAS insertion, version format, per-thread arena.

**Key behaviors:**
- `GetVersionChain(key)`: return the head of the version chain for a given key.
- `InsertVersion(key, node)`: CAS-insert a new version node at the head of the chain.
- `CommitVersion(node, commitTS)`: CAS-update `endTS` from `MaxUint64` to `commitTS`.
- `GCVersionChain(key)`: compact old versions (all active transactions have `endTS < oldestReadTS`).

### LC — Lock (Hazard)

**Responsibility:** Hazard pointer management, epoch-based reclamation, lock-free read coordination.

**Key behaviors:**
- `Publish(ptr *versionNode)`: store the pointer in the thread's hazard set.
- `Clear()`: clear the hazard set (called when the read is done).
- `Reclaim(batch)`: called by the epoch manager to free old version nodes.
- `RegisterThread()` / `UnregisterThread()`: epoch registration.

### SN — Snapshot

**Responsibility:** Read view management per session, epoch registration, per-thread arena.

**Key behaviors:**
- `NewReadView()`: create a new read view with the current `readTS` and a snapshot of version chain heads.
- `Get(key)`: traverse the version chain for this key, find visible version.
- `Close()`: release the read view's resources.

### VL — Validation

**Responsibility:** Commit protocol, write-write conflict detection, transaction slot management.

**Key behaviors:**
- `Begin()`: allocate slot, assign beginTS, create read view.
- `Validate()`: scan slots, check write-write conflicts.
- `Commit()`: assign commitTS, update version nodes, write WAL.
- `Abort()`: mark slot as aborted, release resources.

## Implementation Plan

1. **`internal/TXN/MV/version.go`** — `VersionNode`, `VersionChain`, CAS insertion, commit.
2. **`internal/TXN/MV/arena.go`** — per-thread arena: `Alloc`, exhaustion handling, lazy initialization.
3. **`internal/TXN/LC/hazard.go`** — hazard pointer set: `Publish`, `Clear`, `Scan`.
4. **`internal/TXN/LC/epoch.go`** — epoch manager: `EnterEpoch`, `ExitEpoch`, `Reclaim`, background goroutine.
5. **`internal/TXN/SN/snapshot.go`** — `ReadView`: creation, `Get`, `Close`.
6. **`internal/TXN/VL/slot.go`** — transaction slot: `Begin`, `AllocateSlot`, `ReleaseSlot`.
7. **`internal/TXN/VL/validate.go`** — `Validate`: write-write conflict detection, `Commit`, `Abort`.
8. **`internal/TXN/VL/protocol.go`** — full commit protocol: Begin → Read → Write → Pre-commit → Commit → Post-commit.
9. **`internal/TXN/VL/gc.go`** — garbage collection of obsolete version nodes.
10. **Tests:** `version_test.go` (concurrent insert/commit), `hazard_test.go` (hazard pointer correctness), `epoch_test.go` (reclamation), `commit_test.go` (full protocol, write-write conflict), `snapshot_test.go` (MVCC read correctness).

## Open Issues

- Should we use a generational arena instead of a single large allocation? Generational arenas reduce GC pressure but add complexity.
- What is the maximum number of concurrent transactions? 1024 is a compile-time constant — should it be configurable?
- How to handle very long-running read transactions? They may prevent GC of many versions. Consider periodic refresh of the read view.
- Should the transaction slot array be lock-free (using atomic slot allocation) or use a mutex-protected free list?