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
- Lock-free insertion: new version nodes are allocated from a per-transaction arena, inserted via CAS on the `next` pointer. No mutex needed for writes.

### Generational Arena

```go
type Arena struct {
    young     []byte      // 16 KB young generation
    youngOff  atomic.Int64
    old       []byte      // 1 MB old generation
    oldOff    atomic.Int64
    promoted  atomic.Bool
    initOldMu sync.Mutex
}
```

- Two-tier generational arena: young generation (16 KB) for fresh allocations, old generation (1 MB) for promoted data.
- When young fills, contents are copied to old and young is reset. After promotion, all allocations go to old.
- Both generations reset on pool return. Reduces GC pressure when transactions abort after only using the young generation.
- **Per-NUMA arena pools:** `numaArenaPool` with per-node `sync.Pool`. `GetArena`/`PutArena` use `nm.CurrentNode()` for first-touch policy. Eliminates contention on global pools under high transaction throughput on multi-socket hosts.
- Arenas pooled via `sync.Pool` for reuse across transactions.
- Old generation bytes are moved to a global `pendingOlds` list on `PutArena`, drained asynchronously by the epoch manager on each 100ms tick.

### Hazard Pointer

```go
type hazardPointerSet struct {
    ptrs [MaxHazardPtrs]atomic.Value // stores *versionNode
}

const MaxHazardPtrs = 2 // one for current read, one for next
```

- Each reader goroutine holds a local `hazardPointerSet`. Before dereferencing a version node pointer, the reader publishes it to one slot (current or next) via `atomic.Store`.
- The reclamation pass scans all registered hazard pointers before freeing any node.
- `PublishCurrent(ptr)` stores in slot 0, `PublishNext(ptr)` stores in slot 1, `Clear()` clears both.
- If a node is in any hazard pointer set, it is not reclaimed.
- The double-slot design allows readers to prefetch the next node while holding the current node in the other slot.

### Epoch Manager

```go
type epochManager struct {
    epoch      atomic.Int64
    threads    sync.Map // map[goroutineID]*threadRecord
    drainCh    chan struct{}
}
```

- Global epoch counter incremented by the epoch manager goroutine (every ~100 ms or on demand).
- Each reader thread registers with the epoch manager on first read and deregisters on exit.
- `Reclaim()` waits for all threads to exit the old epoch before freeing memory.
- QSBR (Quiescent State-Based Reclamation) provides a lighter-weight alternative: threads signal quiescence, and the QSBR manager reclaims when all threads have passed a quiescent barrier.
- Reclaim pool provides deferred cleanup for nodes that cannot be freed immediately (e.g., still referenced by in-flight readers).

### ReadView

```go
type readView struct {
    readTS    uint64
    snapshot  []versionChainSnapshot // one snapshot per key read
    arena     *arena
    mv        *MV
}
```

- A `ReadView` is created when a transaction begins.
- Contains `readTS` (the transaction's start timestamp) and snapshots of the version chain heads for all keys read.
- Readers traverse the version chain, skipping entries where `endTS < readTS` or `beginTS >= readTS` (deleted/tombstone versions).
- The hazard pointer protocol ensures readers never see a node that is being reclaimed.
- `Close()` releases the arena and deregisters from the epoch manager.
- Version stack maintains a stack of version nodes for a key, supporting multi-key reads with consistent snapshot semantics.

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
```

- On transaction begin, a slot is allocated from a lock-free Treiber stack (`atomic.Pointer` CAS). `MaxConcurrentTXNs = 1024`.
- `status`: 0=inactive, 1=active, 2=committed, 3=aborted.
- `writeSet` tracks the key ranges modified by this transaction.
- Write-write conflict detection: before commit, check that no other committed transaction with `commitTS > myBeginTS` modified any key in `writeSet`.

### Commit Protocol

```
1. Begin:
   - allocate slot from lock-free Treiber stack
   - assign beginTS = globalAtomicCounter++
   - register thread with epoch manager
   - take snapshot of all version chain heads for keys that will be read (ReadView)
   - slot.status = ACTIVE

2. Read:
   - for each Get(key):
     a. get version chain head via ENG.Store
     b. publish head to hazard pointer set (atomic.Store)
     c. traverse chain: find first version where beginTS < readTS AND endTS >= readTS
     d. if found AND version.deleted == false: return value
     e. else: return ErrNotFound
     f. clear hazard pointer after read completes

3. Write (Insert/Delete):
   - allocate version node from per-transaction arena (CAS-based Alloc)
   - set version fields: txnID, beginTS, endTS=MaxUint64, key, value, deleted flag
   - insert into version chain via CAS loop
   - add key (or key range) to writeSet

4. Pre-commit (Validation) — Serializability Check:
   - for each slot S in transaction array:
     - if S.status == COMMITTED AND S.commitTS > myBeginTS:
       - for each key K in my writeSet:
         - if K overlaps with S.writeSet:
           - CONFLICT DETECTED: abort this transaction

5. Commit:
   - assign commitTS = globalAtomicCounter++
   - for each version node in writeSet:
     - CAS update endTS from MaxUint64 to commitTS
   - update slot.status = COMMITTED (atomic store)
   - release transaction slot (return to free list)
   - unregister thread from epoch manager

6. Post-commit:
   - release all resources (arena, read view)
   - epoch manager will eventually reclaim old version nodes

7. Abort (if validation fails or user calls Rollback):
   - for each version node in writeSet:
     - CAS to remove from version chain (or mark as aborted)
   - update slot.status = ABORTED
   - release slot to free list
   - unregister thread from epoch manager

8. Savepoint (optional):
   - save current readTS and writeSet snapshot under a name
   - on RollbackTo(name): restore readTS, discard writeSet entries added after savepoint
```

**Concurrency Guarantees:**
- Read-Write: Lock-free — readers traverse version chains without blocking writers.
- Write-Write: Detected at pre-commit — serializable isolation.
- Write-Read: Writers never block readers — MVCC ensures old versions remain visible.

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `MV` | MVCC: version chain, CAS insertion, version format, per-transaction arena with per-NUMA pools, GC of obsolete versions |
| `LC` | Lock: hazard pointers, epoch-based reclamation, QSBR protocol, reclaim pool for deferred cleanup, goid tracking, epoch gosched for cooperative yielding |
| `SN` | Snapshot: read view management per session, epoch registration, per-thread arena, version stack for multi-key reads |
| `VL` | Validation: commit protocol, write-write conflict detection, transaction slot management, savepoint support |

## Clusters

### MV — MVCC

**Responsibility:** Version chain management, CAS insertion, version format, per-transaction arena with per-NUMA pools, GC of obsolete versions.

**Key behaviors:**
- `GetVersionChain(key)`: return the head of the version chain for a given key.
- `InsertVersion(key, node)`: CAS-insert a new version node at the head of the chain.
- `CommitVersion(node, commitTS)`: CAS-update `endTS` from `MaxUint64` to `commitTS`.
- `GCVersionChain(key)`: compact old versions (all active transactions have `endTS < oldestReadTS`). Background GC with time-traveling snapshot pruning.

### LC — Lock (Hazard / QSBR)

**Responsibility:** Hazard pointer management, epoch-based reclamation, QSBR protocol, reclaim pool, goid tracking.

**Key behaviors:**
- `Publish(ptr *versionNode)`: store the pointer in the thread's hazard set (single slot, not all).
- `Clear()`: clear the hazard set (called when the read is done).
- `Reclaim(batch)`: called by the epoch manager to free old version nodes.
- `RegisterThread()` / `UnregisterThread()`: epoch registration.
- QSBR: Quiescent State-Based Reclamation — a lighter-weight alternative to hazard pointers.
- Reclaim pool: deferred cleanup for nodes that cannot be freed immediately.

### SN — Snapshot

**Responsibility:** Read view management per session, epoch registration, per-thread arena, version stack for multi-key reads.

**Key behaviors:**
- `NewReadView()`: create a new read view with the current `readTS` and a snapshot of version chain heads.
- `Get(key)`: traverse the version chain for this key, find visible version.
- `Close()`: release the read view's resources.
- Version stack: maintains a stack of version nodes for a key, supporting multi-key reads with consistent snapshot semantics.

### VL — Validation

**Responsibility:** Commit protocol, write-write conflict detection, transaction slot management.

**Key behaviors:**
- `Begin()`: allocate slot from lock-free Treiber stack, assign beginTS, create read view.
- `Validate()`: scan slots, check write-write conflicts.
- `Commit()`: assign commitTS, update version nodes.
- `Abort()`: mark slot as aborted, return slot to free list.
- Savepoint: nested transaction markers with `Savepoint`/`RollbackTo`.

## Implementation Plan

1. **`internal/TXN/MV/version.go`** — `VersionNode`, `VersionChain`, CAS insertion, commit.
2. **`internal/TXN/MV/arena.go`** — generational arena with per-NUMA pools: `Alloc`, exhaustion handling, lazy initialization.
3. **`internal/TXN/MV/gc.go`** — garbage collection of obsolete version nodes.
4. **`internal/TXN/LC/hazard.go`** — hazard pointer set: `Publish`, `Clear`, `Scan`.
5. **`internal/TXN/LC/epoch.go`** — epoch manager: `EnterEpoch`, `ExitEpoch`, `Reclaim`, background goroutine.
6. **`internal/TXN/LC/qsbr.go`** — QSBR read path.
7. **`internal/TXN/SN/snapshot.go`** — `ReadView`: creation, `Get`, `Close`.
8. **`internal/TXN/VL/slot.go`** — transaction slot: `Begin`, `AllocateSlot`, `ReleaseSlot`.
9. **`internal/TXN/VL/validate.go`** — `Validate`: write-write conflict detection, `Commit`, `Abort`.
10. **`internal/TXN/VL/protocol.go`** — full commit protocol: Begin → Read → Write → Pre-commit → Commit → Post-commit.
11. **Tests:** `version_test.go` (concurrent insert/commit), `arena_test.go` (generational arena, per-NUMA pools), `gc_test.go` (background GC), `hazard_test.go` (hazard pointer correctness), `epoch_test.go` (reclamation), `commit_test.go` (full protocol, write-write conflict), `snapshot_test.go` (MVCC read correctness).

## Open Issues

- How to handle very long-running read transactions? They may prevent GC of many versions. Consider periodic refresh of the read view.
