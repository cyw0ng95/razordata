# Iteration 4 — ENG/Memtable (Lock-Free Skiplist + Memtable)

**Subsystem:** `ENG`
**Status:** pending
**Est. LOC:** ~2,000

## Overview

In-memory write buffer. Lock-free skiplist as backing structure. Memtable with size tracking and freeze trigger. Depends on MEM, WAL, LOG.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `skipList` struct: head (atomic.Pointer), level (atomic.Int32), maxLevel=12 | pending |
| R02 | `node` struct: key/value ([]byte), next array (maxLevel atomic.Pointers) | pending |
| R03 | CAS-based insertion: find predecessor at each level, CAS `next` pointer | pending |
| R04 | Lock-free search: read `next`, compare keys, descend | pending |
| R05 | `Iterator`: lock-free traversal via CAS-ordered next pointers | pending |
| R06 | `Memtable` struct: skiplist, size (atomic.Int64), refs (atomic.Int64), frozen (atomic.Bool) | pending |
| R07 | `Insert`: write to active skiplist, update size atomically | pending |
| R08 | `Get`: search skiplist, return value or `ErrNotFound` | pending |
| R09 | `Iterator`: lock-free traversal of skiplist | pending |
| R10 | `size >= MemTableSize (default 64 MB)` triggers freeze signal | pending |
| R11 | Frozen memtables reject new writes (new writes go to active memtable) | pending |
| R12 | Background goroutine flushes frozen memtable to SST (signaled, not blocking) | pending |
| R13 | `Flush` returns an SST file ready for ENG to ingest | pending |
| R14 | `go vet ./internal/ENG/...` zero warnings | pending |
| R15 | `go test ./internal/ENG/... -race -count=1` all green | pending |
| R16 | Benchmark: concurrent skiplist insert/find throughput | pending |

## Implementation

```
internal/ENG/
├── skiplist.go    # lock-free skiplist, CAS insertion, lock-free search/iterator
├── memtable.go    # Memtable, Insert/Get/Iterator, size tracking, freeze trigger
└── flush.go       # flush goroutine, signal channel
```

## Deferred

- Lock-free delete (tombstones) — deferred to TXN iteration
- Compaction — deferred to v2