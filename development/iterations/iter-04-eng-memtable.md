# Iteration 4 — ENG/Memtable (Lock-Free Skiplist + Memtable)

**Subsystem:** `ENG`
**Status:** pending
**Est. LOC:** ~2,000

## Overview

In-memory write buffer. Lock-free skiplist as backing structure. Memtable with size tracking and freeze trigger. Depends on MEM, WAL, LOG.

## Dependencies

- Required: `MEM`, `WAL`, `LOG`
- Consumed interfaces: `BufferPool`, `Writer`, `Logger`

## Design Alignment

Directory structure matches `design/subsystems/ENG.md`:
```
internal/ENG/
└── LS/               # LSM tree cluster (part 1: memtable)
    ├── skiplist.go   # lock-free skiplist, CAS insertion, lock-free search/iterator
    ├── skiplist_test.go
    ├── memtable.go   # Memtable, Insert/Get/Iterator, size tracking, freeze trigger
    └── memtable_test.go
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `skipList` struct: head (atomic.Pointer to node), level (atomic.Int32), maxLevel=12 | pending |
| R02 | `node` struct: key ([]byte), value ([]byte), next ([maxLevel]atomic.Pointer to node) | pending |
| R03 | CAS-based insertion: find predecessor at each level, CAS `next` pointer from nil to new node | pending |
| R04 | Lock-free search: read `next`, compare keys, descend | pending |
| R05 | `Iterator`: lock-free traversal via CAS-ordered next pointers | pending |
| R06 | `Memtable` struct: skiplist (*skipList), size (atomic.Int64), refs (atomic.Int64), frozen (atomic.Bool) | pending |
| R07 | `Insert(key, value []byte) error`: write to active skiplist, update size atomically | pending |
| R08 | `Get(key []byte) ([]byte, error)`: search skiplist, return value or `ErrNotFound` | pending |
| R09 | `Iterator(prefix []byte) Iterator`: lock-free traversal of skiplist | pending |
| R10 | `size >= MemTableSize (default 64 MB)` triggers freeze signal | pending |
| R11 | Frozen memtables reject new writes (writes go to new active memtable) | pending |
| R12 | Background goroutine flushes frozen memtable to SST (signaled, not blocking on write path) | pending |
| R13 | `Flush() (*SSTFile, error)`: returns an SST file ready for ENG to ingest | pending |
| R14 | `go vet ./internal/ENG/...` zero warnings | pending |
| R15 | `go test ./internal/ENG/... -race -count=1` all green | pending |
| R16 | Benchmark: concurrent skiplist insert/find throughput | pending |

## Implementation

### Phase 1: SkipList (`LS/skiplist.go`)

1. `maxLevel = 12` — 2^12 = 4096 levels, sufficient for 10^9 items
2. `node`: key/value as `[]byte`, `next [maxLevel]atomic.Pointer[node]`
3. `randomLevel()`: geometric distribution, P(drop) = 0.5, max = maxLevel-1
4. `Insert(key, value)`: generate level, allocate node, find predecessors (lock-free traversal), CAS each level from bottom to top
5. `Find(key)`: lock-free traversal, compare at each level. Return value or nil.
6. `Iterator`: struct holding current node pointer, `Next()` advances via atomic load of `next[0]`, `Key()`/`Value()` return current node's fields

### Phase 2: Memtable (`LS/memtable.go`)

1. `Memtable` struct as described
2. `NewMemtable() *Memtable`: initialize skiplist head (dummy node with maxLevel next pointers), size=0, frozen=false
3. `Insert`: if frozen, return error. Else insert into skiplist, update size atomically.
4. `Get`: delegate to skiplist.Find
5. `Iterator`: delegate to skiplist.Iterator
6. `shouldFreeze()`: check size >= MemTableSize
7. `Freeze()`: set frozen=true atomically, return self (immutable now)
8. Background goroutine (started by Engine): select on freezeCh, when signaled call Flush(), send result to ENG

## Deferred to v2

- Lock-free delete (tombstones in skiplist) — deferred to TXN iteration
- Compaction — deferred to v2