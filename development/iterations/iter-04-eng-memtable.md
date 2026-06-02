# Iteration 4 — ENG/LSM (Lock-Free Skiplist + Memtable + SST + Flush)

**Subsystem:** `ENG`
**Status:** in_progress
**Est. LOC:** ~8,000
**Test Coverage:** 75.2% (ENG/LS)

## Overview

Complete LSM tree implementation with lock-free skiplist memtable, SST storage, and flush. Compaction and full read path remain pending.

## Dependencies

- Required: `MEM`, `WAL`, `LOG`
- Consumed interfaces: `BufferPool`, `Writer`, `Logger`

## Design Alignment

Directory structure matches `design/subsystems/ENG.md`:
```
internal/ENG/
└── LS/               # LSM tree cluster
    ├── skiplist.go       # lock-free skiplist, CAS insertion, lock-free search/iterator
    ├── skiplist_test.go
    ├── memtable.go       # Memtable, Insert/Get/Iterator, size tracking, freeze trigger
    ├── memtable_test.go
    ├── sst_writer.go     # SST writer: bloom filter, block encoding, footer
    ├── sst_writer_test.go
    ├── sst_reader.go     # SST reader: block decoding, bloom check, index binary search
    ├── sst_reader_test.go
    ├── manifest.go       # Version management, Apply, Checkpoint, persistence
    ├── manifest_test.go
    ├── flush.go          # Flush manager: freeze, write SST, update manifest
    ├── compaction.go     # Leveled compaction: key heap merge, level budgets
    ├── engine.go         # Read path: memtable → L0 → L1+, merge iterator
    ├── engine_test.go
    ├── index.go          # Primary key index (pkEntry, pkIterator)
    ├── index_test.go
    ├── table.go          # Table registry, catalog (create/drop/get by name)
    ├── table_test.go
    ├── schema.go         # Column types, validation, type encoding
    ├── schema_test.go
    ├── deparser.go       # Row encoding, block encoding/decoding
    └── deparser_test.go
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `skipList` struct: head (atomic.Pointer to node), level (atomic.Int32), maxLevel=12 | done |
| R02 | `node` struct: key ([]byte), value (atomic.Value for mutex-free updates), next ([maxLevel]atomic.Pointer to node) | done |
| R03 | CAS-based insertion: find predecessor at each level, CAS `next` pointer from nil to new node | done |
| R04 | Lock-free search: read `next`, compare keys, descend | done |
| R05 | `Iterator`: lock-free traversal via atomic load of `next[0]` | done |
| R06 | `Memtable` struct: skiplist (*skipList), size (atomic.Int64), refs (atomic.Int64), frozen (atomic.Bool) | done |
| R07 | `Insert(key, value []byte) error`: write to active skiplist, update size atomically | done |
| R08 | `Get(key []byte) ([]byte, bool)`: search skiplist, return value or not found | done |
| R09 | `Iterator()`: lock-free traversal of skiplist | done |
| R10 | `Size() >= maxSize` triggers freeze signal | done |
| R11 | Frozen memtables reject new writes (writes go to new active memtable) | done |
| R12 | `Flush()`: freeze memtable, write SST file, update manifest | done |
| R13 | SST writer: bloom filter, block encoding with restart points, footer | done |
| R14 | SST reader: bloom check, binary search index, block decoding | done |
| R15 | Manifest: versioning, Apply, Checkpoint, atomic rename, persistence | done |
| R16 | Compaction: leveled compaction with key heap merge sort | partial |
| R17 | Read path: memtable → L0 → L1+, bloom filter check | partial |
| R18 | Primary key index: pkEntry, pkIterator, Insert/Find/Delete | done |
| R19 | Table registry: Create/Get/Drop/List tables | done |
| R20 | Table catalog: CreateTable/DropTable/GetTableByName | done |
| R21 | Schema validation: null checks, type checks, constraint validation | done |
| R22 | Type encoding: EncodeInt/DecodeInt, EncodeFloat/DecodeFloat, etc. | done |
| R23 | Row encoding: EncodeRow/DecodeRow with null bitmap | done |
| R24 | Block encoding: EncodeBlock/DecodeBlock with restart points | done |
| R25 | `go vet ./internal/ENG/...` zero warnings | done |
| R26 | `go test ./internal/ENG/... -race -count=1` all green | done |

## Commits

- `e8512c2` - feat(ENG/LS): implement SkipList + Memtable
- `211e514` - feat(ENG/LS): implement SST writer/reader scaffolding
- `8d6ac8e` - feat(ENG/LS): implement Manifest versioning
- `6ac8393` - feat(ENG/LS): implement Flush manager
- `0d549ec` - feat(ENG/LS): implement Compaction (R08)
- `68cbc63` - feat(ENG/LS): implement R08 compaction + R09 read path + fix skiplist race condition
- `e3bbb66` - feat(ENG/LS): implement R10 primary index
- `886a648` - feat(ENG/LS): implement R11 table catalog and schema registry
- `45913e4` - feat(ENG/LS): implement R12 schema validation and type encoding
- `422371f` - feat(ENG/LS): implement R13 row/block encoding and deparser
- `a62424b` - fix(ENG/LS): close flushQueue channel before manifest.Close to prevent panic
- `50fd37e` - test(ENG/LS): add engine Write/Read/Close/MayContain coverage tests (reverted)
- `b7f1032` - Revert "test(ENG/LS): add engine Write/Read/Close/MayContain coverage tests"
- `12cf0a2` - test: improve coverage for FIL/DF openFile, ENG/LS index and flush
- `386a7de` - test: improve coverage for ENG/LS compaction, FIL/LF, LOG/LG
- `ff9e5de` - test: add keyHeap, compactionJob, and WAL writer path coverage
- `4691202` - test(bf): add coverage for Get, Pin, eviction paths
- `71f6a07` - test(bf): add Close path coverage tests
- `67fcd99` - test: add boundary coverage cases for BF, DF, ENG, WAL clusters

## Test Results

```
ok  github.com/cyw0ng95/razordata/internal/ENG/LS  1.278s
PASS (race detection green)
Coverage: 74.1%
```

## Deferred to v2

- Secondary indexes
- Lock-free delete (tombstones)
