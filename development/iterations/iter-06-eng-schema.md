# Iteration 6 — ENG/Schema + Read Path

**Subsystem:** `ENG`
**Status:** pending
**Est. LOC:** ~2,000

## Overview

Row serialization, table management, read path with merge iterator. Depends on ENG/SST.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `ColumnType` enum: CTInt, CTVarchar, CTBool, CTFloat, CTText, CTBlob, CTTimestamp | pending |
| R02 | `ColumnDef`: Name, Type, Nullable, Default, PrimaryKey | pending |
| R03 | `TableSchema`: TableID, Name, Columns, PrimaryKey ([]int) | pending |
| R04 | `ValidateRow`: check NOT NULL, type match, constraints | pending |
| R05 | Row serialization: INT (8 bytes), BIGINT (8 bytes), FLOAT (8 bytes), BOOL (1 byte) inline | pending |
| R06 | Row serialization: VARCHAR/TEXT/BLOB as `[length:varint][data:blob]` | pending |
| R07 | Row serialization: null bitmap in header (one bit per column) | pending |
| R08 | `encodeRow`/`decodeRow`: apply per-column encoding based on schema | pending |
| R09 | `encodeBlock`/`decodeBlock`: SST block delta encoding with restart points | pending |
| R10 | `encodeValue`/`decodeValue` for scalar values per `ColumnType` | pending |
| R11 | `CREATE TABLE`: allocate tableID, serialize TableSchema, insert into catalog | pending |
| R12 | `DROP TABLE`: insert tombstone in catalog, remove from registry | pending |
| R13 | Schema registry: `map[tableID]*TableSchema` with `sync.RWMutex` | pending |
| R14 | Primary key IS the table key (`__pk:<tableID>:<pk>` → row data) | pending |
| R15 | Read path search order: active memtable → frozen memtables → L0 (newest first) → L1+ (binary search + bloom) | pending |
| R16 | Merge iterator: min-heap over all sources in sorted key order | pending |
| R17 | End-to-end read: insert data, flush, read back — data matches | pending |
| R18 | `go vet ./internal/ENG/...` zero warnings | pending |
| R19 | `go test ./internal/ENG/... -race -count=1` all green | pending |

## Implementation

```
internal/ENG/
├── schema.go      # ColumnType, ColumnDef, TableSchema, ValidateRow
├── row.go         # row serialization, encode/decode
├── table.go       # CREATE TABLE, DROP TABLE, schema registry
├── read.go        # read path, search order, merge iterator
└── value.go       # value encode/decode per ColumnType
```

## Deferred

- Compaction
- Secondary indexes (`__idx__` structure)
- Statistics for selectivity estimation