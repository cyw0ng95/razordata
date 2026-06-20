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

### Per-Transaction Arena (v1 Decision: per-transaction vs. per-goroutine)

The original design (iter-05) specified per-goroutine arenas indexed by goroutine ID. However, the implementation uses **per-transaction arenas** for the following reasons:

1. **Simpler lifecycle**: Arena allocation/deallocation aligns with transaction boundaries (Begin/Commit/Abort).
2. **No goroutine ID required**: Go doesn't expose goroutine IDs; simulating them with atomic counters defeats the purpose.
3. **Better memory isolation**: Each transaction's writes are isolated to its own arena, simplifying rollback.

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

- Allocated in `TXN/VL/manager.go` on `Begin()`: `arena := MV.NewArena(1 * MB)`.
- Released in `TXN/VL/protocol.go` on `Commit()`/`Abort()`: `MV.ReleaseArena(arena)`.
- Arenas are pooled via `sync.Pool` for reuse across transactions.
- **Trade-off**: Per-transaction arenas may allocate more frequently than per-goroutine arenas under high concurrency, but the simplicity and correct lifecycle semantics outweigh the cost.

**Future work (post-v1)**: If profiling shows arena allocation is a hotspot, consider hybrid approach: per-goroutine scratch arenas that feed into per-transaction committed arenas.

### Hazard Pointer

```go
type hazardPointerSet struct {
    ptrs [MaxHazardPtrs]atomic.Value // stores *versionNode
}

const MaxHazardPtrs = 2 // one for current read, one for next
```

**Design intent:** Each reader goroutine holds a local `hazardPointerSet`. Before dereferencing a version node pointer, the reader publishes it to **one slot** (current or next) via `atomic.Store`. The reclamation pass scans all registered hazard pointers before freeing any node.

**Current implementation gap (REQ000175):** The current `TXN/LC/hazard.go` publishes to **all slots** instead of a single slot, which defeats the double-slot design. This is a known misalignment that should be fixed in a future iteration.

**Correct protocol:**
```go
func (h *hazardPointerSet) PublishCurrent(ptr unsafe.Pointer) {
    h.ptrs[0].Store(ptr) // current read slot
}

func (h *hazardPointerSet) PublishNext(ptr unsafe.Pointer) {
    h.ptrs[1].Store(ptr) // prefetch slot for next node
}

func (h *hazardPointerSet) Clear() {
    h.ptrs[0].Store(nil)
    h.ptrs[1].Store(nil)
}
```

- If a node is in any hazard pointer set, it is not reclaimed.
- The double-slot design allows readers to prefetch the next node while holding the current node in the other slot.

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

**Design intent:** Global epoch counter incremented by the epoch manager goroutine (every ~100 ms or on demand). Each reader thread registers with the epoch manager on first read and deregisters on exit.

**Current implementation gaps:**
1. **REQ000175**: The `Reclaim()` function does not wait for threads to exit the old epoch and does not actually free memory (stub implementation).
2. **REQ000181**: Go does not expose goroutine IDs; the current implementation uses an atomic counter which can produce duplicate IDs. A proper solution would use `runtime.Callers` or context-based tracking.

**Correct protocol (design):**
```go
func (em *epochManager) EnterEpoch() {
    curr := em.epoch.Load()
    tr := em.getThreadRecord()
    tr.enteredAt.Store(curr)
}

func (em *epochManager) ExitEpoch() {
    tr := em.getThreadRecord()
    tr.enteredAt.Store(math.MaxInt64) // mark as exited
}

func (em *epochManager) Reclaim(batch []unsafe.Pointer) {
    // Wait for all threads to exit the current epoch
    target := em.epoch.Load() + 1
    for {
        allExited := true
        em.threads.Range(func(_, v any) bool {
            tr := v.(*threadRecord)
            if tr.enteredAt.Load() < target {
                allExited = false
                return false
            }
            return true
        })
        if allExited {
            break
        }
        time.Sleep(1 * ms)
    }
    // Now safe to free the batch
    freeBatch(batch)
}
```

- This guarantees that no reader is mid-read on a freed node.
- The epoch manager goroutine increments the epoch counter periodically (~100 ms).

### ReadView

```go
type versionChainSnapshot struct {
    key   []byte
    head  *versionNode // snapshot of the chain head at readTS
}

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

- On transaction begin, a slot is allocated from a pre-allocated fixed-size array (no GC pressure). `MaxConcurrentTXNs = 1024`. The array itself is fixed; allocation uses a mutex-protected free list to pick a free slot from the array.
- `status`: 0=inactive, 1=active, 2=committed, 3=aborted.
- `writeSet` tracks the key ranges modified by this transaction.
- Write-write conflict detection: before commit, check that no other committed transaction with `commitTS > myBeginTS` modified any key in `writeSet`.

### Commit Protocol

```
1. Begin:
   - allocate slot from pre-allocated array (mutex-protected free list)
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
   - record all read keys in readSet (for SSI if enabled in future)

3. Write (Insert/Delete):
   - allocate version node from thread-local arena (CAS-based Alloc)
   - set version fields: txnID, beginTS, endTS=MaxUint64, key, value, deleted flag
   - insert into version chain via CAS loop:
     ```go
     for {
         oldHead := loadHead(key)
         newVersion.next = oldHead
         if CAS(&head, oldHead, newVersion) {
             break
         }
         // CAS failed: another writer inserted concurrently, retry
     }
     ```
   - add key (or key range) to writeSet
   - **write RTData record to WAL (not yet synced)** (CRITICAL GAP REQ000171: not implemented)

4. Pre-commit (Validation) — Serializability Check:
   - acquire read lock on transaction slot array
   - for each slot S in transaction array:
     - if S.status == COMMITTED AND S.commitTS > myBeginTS:
       - for each key K in my writeSet:
         - if K overlaps with S.writeSet:
           - CONFLICT DETECTED: abort this transaction
   - release read lock
   - if validation passed: proceed to commit
   - if validation failed: goto Abort

5. Commit:
   - assign commitTS = globalAtomicCounter++
   - **write RTCommit record to WAL: [txnID][commitTS]** (CRITICAL GAP REQ000171: not implemented)
   - **call WAL.Sync() to fsync the commit record (durability point)** (not implemented)
   - for each version node in writeSet:
     - CAS update endTS from MaxUint64 to commitTS:
       ```go
       for {
           expected := MaxUint64
           if CAS(&version.endTS, expected, commitTS) {
               break
           }
           // CAS failed: another commit already updated, should not happen
       }
       ```
   - update slot.status = COMMITTED (atomic store)
   - release transaction slot (return to free list)
   - unregister thread from epoch manager

**Current implementation gap (REQ000171):** The `TXN/VL/protocol.go` Commit() function does NOT write RTCommit records to WAL and does NOT call WAL.Sync(). This is a critical durability gap — committed data will be lost on crash.

6. Post-commit:
   - release all resources (arena, read view)
   - epoch manager will eventually reclaim old version nodes
   - notify waiting readers (if any) via LOG/TraceHook

7. Abort (if validation fails or user calls Rollback):
   - for each version node in writeSet:
     - CAS to remove from version chain (or mark as aborted)
   - write RTRollback record to WAL
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
- Durability: WAL fsync before commitTS assignment — no commit acknowledged until durable.

**Failure Scenarios:**
- CAS failure on write insertion: retry with new head pointer.
- CAS failure on commit (endTS update): should never happen — indicates bug.
- Validation conflict: abort immediately, return `ErrTxAborted` to caller.
- WAL write failure: abort transaction, return `ErrIO` (retryable).

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `MV` | MVCC: version chain, CAS insertion, version format, per-transaction arena, GC of obsolete versions (gc.go) |
| `LC` | Lock: hazard pointers, epoch-based reclamation (REQ000175), QSBR protocol (REQ000302), reclaim pool for deferred cleanup (reclaim_pool.go), goid tracking via atomic counter (REQ000181, goid.go), epoch gosched for cooperative yielding (epoch_gosched.go) |
| `SN` | Snapshot: read view management per session, epoch registration, thread-local arena, version stack for multi-key reads (version_stack.go) |
| `VL` | Validation: commit protocol, write-write conflict detection, transaction slot management, savepoint support |

## Clusters

### MV — MVCC

**Responsibility:** Version chain management, CAS insertion, version format, per-thread arena.

**Key behaviors:**
- `GetVersionChain(key)`: return the head of the version chain for a given key.
- `InsertVersion(key, node)`: CAS-insert a new version node at the head of the chain.
- `CommitVersion(node, commitTS)`: CAS-update `endTS` from `MaxUint64` to `commitTS`.
- `GCVersionChain(key)`: compact old versions (all active transactions have `endTS < oldestReadTS`).

### MV — MVCC

**Responsibility:** Version chain management, CAS insertion, version format, per-transaction arena, GC of obsolete versions.

**Key behaviors:**
- `GetVersionChain(key)`: return the head of the version chain for a given key.
- `InsertVersion(key, node)`: CAS-insert a new version node at the head of the chain.
- `CommitVersion(node, commitTS)`: CAS-update `endTS` from `MaxUint64` to `commitTS`.
- `GCVersionChain(key)`: compact old versions (all active transactions have `endTS < oldestReadTS`). `gc.go` implements background GC.
- Arena allocation: `arena.go` — per-transaction arena with CAS-based `Alloc()`. Arenas pooled via `sync.Pool`.

### LC — Lock (Hazard / QSBR)

**Responsibility:** Hazard pointer management, epoch-based reclamation, QSBR protocol, reclaim pool, goid tracking.

**Key behaviors:**
- `Publish(ptr *versionNode)`: store the pointer in the thread's hazard set.
- `Clear()`: clear the hazard set (called when the read is done).
- `Reclaim(batch)`: called by the epoch manager to free old version nodes.
- `RegisterThread()` / `UnregisterThread()`: epoch registration.
- **QSBR (REQ000302):** `qsbr.go` implements Quiescent State-Based Reclamation — a lighter-weight alternative to hazard pointers. Threads signal quiescence, and the QSBR manager reclaims when all threads have passed a quiescent barrier.
- **Reclaim pool (REQ000301):** `reclaim_pool.go` provides a deferred cleanup pool for nodes that cannot be freed immediately (e.g., still referenced by in-flight readers).
- **Goid tracking (REQ000181):** `goid.go` uses an atomic counter as a goroutine ID substitute (acknowledged limitation: possible collisions under extreme concurrency).
- **Epoch gosched (REQ000182):** `epoch_gosched.go` allows the epoch manager goroutine to yield via `runtime.Gosched()` to avoid blocking the Go scheduler.

### SN — Snapshot

**Responsibility:** Read view management per session, epoch registration, per-thread arena, version stack for multi-key reads.

**Key behaviors:**
- `NewReadView()`: create a new read view with the current `readTS` and a snapshot of version chain heads.
- `Get(key)`: traverse the version chain for this key, find visible version.
- `Close()`: release the read view's resources.
- **Version stack (REQ000304):** `version_stack.go` maintains a stack of version nodes for a key, supporting multi-key reads with consistent snapshot semantics. The stack allows rolling back individual keys without traversing the full chain.

### VL — Validation

**Responsibility:** Commit protocol, write-write conflict detection, transaction slot management.

**Key behaviors:**
- `Begin()`: allocate slot from a mutex-protected free list (no CAS contention — the free list itself is protected by `sync.Mutex`), assign beginTS, create read view.
- `Validate()`: scan slots, check write-write conflicts.
- `Commit()`: assign commitTS, update version nodes, write WAL.
- `Abort()`: mark slot as aborted, return slot to free list.

## Implementation Plan

1. **`internal/TXN/MV/version.go`** — `VersionNode`, `VersionChain`, CAS insertion, commit.
2. **`internal/TXN/MV/arena.go`** — per-thread arena: `Alloc`, exhaustion handling, lazy initialization. Per-NUMA arena pools (REQ000547) eliminate contention on multi-socket hosts.
3. **`internal/TXN/LC/hazard.go`** — hazard pointer set: `Publish`, `Clear`, `Scan`.
4. **`internal/TXN/LC/epoch.go`** — epoch manager: `EnterEpoch`, `ExitEpoch`, `Reclaim`, background goroutine.
5. **`internal/TXN/SN/snapshot.go`** — `ReadView`: creation, `Get`, `Close`.
6. **`internal/TXN/VL/slot.go`** — transaction slot: `Begin`, `AllocateSlot`, `ReleaseSlot`.
7. **`internal/TXN/VL/validate.go`** — `Validate`: write-write conflict detection, `Commit`, `Abort`.
8. **`internal/TXN/VL/protocol.go`** — full commit protocol: Begin → Read → Write → Pre-commit → Commit → Post-commit.
9. **`internal/TXN/VL/gc.go`** — garbage collection of obsolete version nodes.
10. **Tests:** `version_test.go` (concurrent insert/commit), `hazard_test.go` (hazard pointer correctness), `epoch_test.go` (reclamation), `commit_test.go` (full protocol, write-write conflict), `snapshot_test.go` (MVCC read correctness).

## Shipped Requirements

The following requirements have been implemented and shipped; they are now part of the design baseline.

### MV / LC / SN / VL — Transaction Layer

| ID | Requirement | Iteration |
|---|---|---|
| REQ000051 | MVCC version chain (lock-free, CAS insertion) | iter-05 |
| REQ000052 | Per-thread arena allocation | iter-05 |
| REQ000053 | Hazard pointer coordination | iter-05 |
| REQ000054 | Epoch-based reclamation | iter-05 |
| REQ000055 | Read view per transaction | iter-05 |
| REQ000056 | Commit protocol: validate → assign `commitTS` → CAS `endTS` | iter-06 |
| REQ000057 | Write-write conflict detection | iter-06 |
| REQ000058 | Transaction slots (max 1024 concurrent) | iter-06 |
| REQ000059 | Shadow writeSet for ROLLBACK | iter-06 |
| REQ000060 | Read-uncommitted isolation (v1) | iter-09 |
| REQ000061 | Read-committed as default isolation level | iter-24 |
| REQ000062 | MVCC own-writes visibility in transactions | iter-24 |
| REQ000063 | Savepoint support | iter-09 |
| REQ000064 | Generational arena — `TXN/MV/arena.go` now has two tiers: young generation (16 KB) for fresh allocations, old generation (1 MB) for promoted data; when young fills, contents are copied to old and young is reset; after promotion, all allocations go to old; both generations reset on pool return; reduces GC pressure when transactions abort after only using the young generation | iter-27 |
| REQ000123 | TXN-API — Configurable isolation levels (SET TRANSACTION) | iter-24 |
| REQ000147 | Complete commit protocol (6 phases) | iter-20 |
| REQ000158 | Hazard pointer publication/clear protocol (split into PublishCurrent/PublishNext) | iter-15 |
| REQ000159 | Per-thread arena lazy initialization via `sync.Pool` (design specifies, verify implementation) | iter-05 (arena) |
| REQ000164 | Epoch manager background goroutine (100ms interval, drain coordination) | iter-27 |
| REQ000171 | TXN/VL — WAL integration in commit protocol | iter-20 |
| REQ000174 | ENG/LS — BloomFilter FNV-1a double-hash | iter-20 |
| REQ000175 | TXN/LC — Fix hazard pointer Publish (store to single slot, not all) and implement actual memory reclamation | iter-27 |
| REQ000179 | TXN/VL — Add arena field to transactionSlot struct for per-transaction tracking | iter-16 |
| REQ000181 | TXN/LC — Fix goroutine ID tracking (use real goroutine identity, not atomic counter) | iter-27 |
| REQ000239 | TXN/VL — Savepoint implementation (nested transaction markers) | iter-21 |
| REQ000255 | Read-committed per-statement snapshot | iter-24 |
| REQ000305 | TXN/MV — Generational arena with old generation epoch-reclaim — lazy old allocation on first promotion (1 MB, guarded by `initOldMu`); `PutArena` moves promoted old to global `pendingOlds` list instead of retaining in arena pool; `ReclaimOldGenerations()` drains list, called by LC epoch manager on each 100ms tick; reduces epoch manager pressure by reclaiming old generations asynchronously | iter-27 |
| REQ000306 | TXN/MV — Stack-allocate version nodes (escape analysis hints, 0 allocs/op) | iter-27 |
| REQ000308 | TXN/LC — QSBR read path (64-shard quiescent reclamation) | iter-27 |
| REQ000532 | TXN/MV — Arena.promote() initOldMu taken before nil check | iter-28 (audit) |
| REQ000307 | MV-OCC timestamp ordering (Silo-style, O(1) per-txn read-set validation; targets 1M+ txn/s on 16 cores) | iter-28 |
| REQ000551 | Time-traveling snapshot pruning — `GCVersionChain`/`GCAllChains` return prune candidates below `oldestActiveReadTS` | iter-28.2 |
| REQ000555 | Lock-free transaction slot recycling — Treiber stack (`atomic.Pointer` CAS) replaces mutex-guarded freeList | iter-28.2 |
| REQ000547 | Per-NUMA arena pools — `numaArenaPool` with per-node `sync.Pool`; `GetArena`/`PutArena` use `nm.CurrentNode()` for first-touch policy; eliminates contention on global `reclaimMu` and `arenaPool` under high transaction throughput on multi-socket hosts | iter-29 |
| REQ000121 | TXN-API — `BEGIN` / `COMMIT` / `ROLLBACK` | iter-08 |

## Open Issues

- Should we use a generational arena instead of a single large allocation? Generational arenas reduce GC pressure but add complexity. **(Resolved — see REQ000064)**
- How to handle very long-running read transactions? They may prevent GC of many versions. Consider periodic refresh of the read view.