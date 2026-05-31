# Iteration 6 — ENG/Schema + Read Path

**Subsystem:** `ENG`
**Status:** pending
**Est. LOC:** ~2,000

## Overview

Row serialization, table management, read path with merge iterator. Depends on ENG/SST.

## Dependencies

- Required: `ENG/SST`
- Consumed interfaces: `SSTReader`, `SSTWriter`, `Manifest`

## Design Alignment

Directory structure matches `design/subsystems/ENG.md`:
```
internal/ENG/
├── SC/               # Schema cluster
│   └── sc.go         # ColumnType, ColumnDef, TableSchema, ValidateRow
├── DP/               # Deparser cluster
│   ├── dp.go         # row serialization, block encoding, value encode/decode
│   ├── dp_test.go
│   └── row.go        # row encode/decode helpers
├── TB/               # Table cluster
│   ├── tb.go         # CREATE TABLE, DROP TABLE, schema registry
│   └── tb_test.go
├── ID/               # Index cluster
│   └── id.go         # primary key index (placeholder: pk IS table key)
├── LS/               # LSM tree cluster (part 3: read path)
│   ├── read.go       # read path, search order, merge iterator
│   └── read_test.go
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `ColumnType` enum: CTInt=0, CTBigInt=1, CTVarchar=2, CTFloat=3, CTBool=4, CTText=5, CTBlob=6, CTTimestamp=7 | pending |
| R02 | `ColumnDef`: Name (string), Type (ColumnType), Nullable (bool), Default (Value), PK (bool) | pending |
| R03 | `TableSchema`: TableID (uint64), Name (string), Columns ([]ColumnDef), PrimaryKey ([]int, column indices) | pending |
| R04 | `ValidateRow(row, schema) error`: check NOT NULL, type match, constraints | pending |
| R05 | Row serialization: INT (8 bytes), BIGINT (8 bytes), FLOAT (8 bytes), BOOL (1 byte) inline | pending |
| R06 | Row serialization: VARCHAR/TEXT/BLOB as `[length:varint][data:blob]` | pending |
| R07 | Row serialization: null bitmap in header (one bit per column) | pending |
| R08 | `encodeRow(row Row, schema *TableSchema) ([]byte, error)` | pending |
| R09 | `decodeRow(data []byte, schema *TableSchema) (Row, error)` | pending |
| R10 | `encodeValue(v Value, t ColumnType) ([]byte, error)` for scalar values | pending |
| R11 | `decodeValue(data []byte, t ColumnType) (Value, error)` for scalar values | pending |
| R12 | `CREATE TABLE`: allocate tableID, serialize TableSchema, insert into catalog | pending |
| R13 | `DROP TABLE`: insert tombstone in catalog, remove from registry | pending |
| R14 | Schema registry: `map[tableID]*TableSchema` with `sync.RWMutex` | pending |
| R15 | Primary key IS the table key: `__pk:<tableID>:<pkValue>` → row data (no secondary index in v1) | pending |
| R16 | Read path search order: active memtable → frozen memtables → L0 (newest first) → L1+ (binary search + bloom) | pending |
| R17 | `MergeIterator`: min-heap over all sources (memtable + SST files) in sorted key order | pending |
| R18 | End-to-end read: insert data, flush to SST, read back — data matches | pending |
| R19 | `go vet ./internal/ENG/...` zero warnings | pending |
| R20 | `go test ./internal/ENG/... -race -count=1` all green | pending |

## Implementation

### Phase 1: Schema (`SC/sc.go`)

1. Define all `ColumnType` constants as in design
2. `ColumnDef` and `TableSchema` structs
3. `ValidateRow`: iterate columns, check NOT NULL (null bitmap), check type match

### Phase 2: Deparser (`DP/dp.go` + `row.go`)

1. `encodeValue`: switch on ColumnType — inline fixed-size for int/bool/float, length+blob for varlen
2. `decodeValue`: reverse of encodeValue
3. Null bitmap: `[(numCols + 7) / 8]` bytes header, then column data
4. `encodeRow`: build null bitmap, encode each column sequentially
5. `decodeRow`: read null bitmap, decode each column sequentially

### Phase 3: Table (`TB/tb.go` + `ID/id.go`)

1. `tableRegistry sync.RWMutex` — `map[uint64]*TableSchema`
2. `NextTableID() uint64` — atomic counter
3. `CreateTable(schema *TableSchema) (uint64, error)`: assign ID, serialize (MessagePack or custom), insert into catalog LSM
4. `GetSchema(tableID uint64) (*TableSchema, error)`: lookup in registry or load from catalog
5. `DropTable(tableID uint64) error`: insert tombstone, remove from registry
6. Primary key key format: tableKey = `encodeTableKey(tableID, pkValue)` — encode as `[tableID:varint][pkLen:varint][pkValue]`

### Phase 4: Read Path (`LS/read.go`)

1. `Store` interface: `Insert/Get/Delete/NewIterator/Flush/Compact/Close`
2. Search order: active memtable → frozen memtables (newest first) → L0 (newest first) → L1+ (binary search in index block, bloom check)
3. `MergeIterator`: maintain min-heap of current position from each source. `Next()` pops min, advances that source, re-heapifies.
4. `NewIterator(prefix)`: create merge iterator over all sources, seek to prefix

## Deferred to v2

- Compaction
- Secondary indexes (`__idx__` structure)
- Statistics for selectivity estimation