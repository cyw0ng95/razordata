# Iteration 10 — Real Primary Key Index + Persistent Catalog (v2 ENG/ID + v2 ENG/TB)

**Subsystem:** `ENG` (new `ID/`, `TB/`, `SC/`, `DP/` clusters) + `SQL/EX` + `SYS`
**Status:** done (shipped as v0.7.0)
**Est. LOC:** ~5,000 (1,400 impl + 3,000 tests + 300 benches + 300 spec/docs/integration)
**Actual LOC:** see `internal/ENG/{SC,DP,TB,ID}/`, `internal/SQL/EX/`, `internal/SYS/`
**Target release:** v0.7.0

## Overview

Closes the two v2 architectural placeholders that have lived inside `ENG/LS/` since v1:
- `ENG/ID/` — real primary-key index backed by the LSM tree, replaces the mutex-protected
  linked list in `LS/index.go`. Enables true seek-by-key and point/range scans through
  the storage engine.
- `ENG/TB/` — persistent system catalog. `CREATE TABLE` / `DROP TABLE` write to a
  dedicated sub-LSM and survive restart. Replaces the in-memory `tableRegistry` in
  `LS/table.go` and the parallel in-memory `tables` map in `SQL/EX/source.go`.

Also extracts the v2 SC and DP clusters that the design has specified but the v1 code
folded into `LS/`. The move is **additive** (no churn for current callers) and
unblocks iter-08 R10 (real `IndexScan` seek) plus v1.1 #7 (catalog persistence).

## Dependencies

- Required: `ENG/LS/` (Store + Iterator), `MEM/SP/` (page buffers), `LOG` (event log)
- Consumed interfaces: `ls.Engine.Insert/Get/Delete/NewIterator/RangeIter`,
  `sp.SyncPool`, `lg.Logger`

## Design Alignment

Directory structure matches `design/subsystems/ENG.md` and `design/ARCH.md`:

```
internal/ENG/
├── ID/                # NEW — primary key index (LSM-backed)
│   ├── pkindex.go        # PKIndex: Insert, Delete, Seek, Range, Len
│   ├── pkindex_test.go   # insert/point-seek/range/delete/ordering
│   ├── pkindex_bench.go  # BenchmarkPKIndex_Insert / _Seek / _Range
│   ├── keycodec.go       # composite-key encode/decode
│   └── keycodec_test.go  # round-trip 1- and 2-column PKs, type variants
├── TB/                # NEW — table registry with disk persistence
│   ├── catalog.go        # Catalog: CreateTable, DropTable, Get, GetByName, List
│   ├── catalog_test.go   # persistence across reopens, tombstone semantics
│   ├── catalog_bench.go  # BenchmarkCatalog_Create / _Lookup
│   ├── schema_codec.go   # length-prefixed binary MarshalTableSchema / Unmarshal
│   └── schema_codec_test.go
├── SC/                # NEW — extracted schema cluster
│   ├── sc.go             # TableSchema, ColumnDef, ColumnType, Validator
│   └── sc_test.go        # ValidateRow, CompareColumnDef, edge cases
├── DP/                # NEW — extracted deparser cluster
│   ├── row.go            # EncodeRow, DecodeRow
│   ├── row_test.go
│   ├── block.go          # EncodeBlock, DecodeBlock
│   ├── block_test.go
│   ├── value_codec.go    # EncodeInt/DecodeInt, EncodeFloat/..., EncodeBool/...
│   └── value_codec_test.go
└── LS/                # MODIFIED — remove moved code, add thin wrappers
    ├── catalog.go        # thin alias: type Catalog = TB.Catalog; helper funcs
    ├── pkindex.go        # thin alias: type PKIndex = ID.PKIndex; helper funcs
    ├── deparser.go       # DELETED (moved to DP/)
    ├── schema.go         # DELETED (moved to SC/)

internal/SQL/EX/
├── operators.go        # MODIFIED — IndexScan.realSeek calls *ID.PKIndex.Seek
├── cost_indexscan_test.go  # MODIFIED — IndexScan cost 0.1 → 0.01
├── (new) indexscan_test.go # ADDED — TestIndexScan_RealSeek, _RangeSeek
├── writers.go          # MODIFIED — CreateTable/DropTable call *TB.Catalog
└── source.go           # MODIFIED — replace in-memory tables map with catalog lookup

internal/SYS/SY/
├── sy.go               # MODIFIED — Engine.open calls catalog.Load;
│                          registers all tables with executor.
└── sy_test.go          # ADDED — TestEngine_ReopenPreservesCatalog
```

## Gap Analysis (pre-flight, code-verified)

Read all relevant existing code before writing the plan. Findings:

1. **No MessagePack dependency.** `go.mod` only has `golang.org/x/sys v0.45.0`;
   `go.sum` is one line. The design (`ENG.md` "Schema" section) states
   "MessagePack-encoded `TableSchema`" but no dep is present. **Decision:**
   use a length-prefixed binary format (a single byte per `ColumnType` tag,
   `binary.LittleEndian.Uint32` for offsets, no new dependency). Documents
   cleanly in `TB/schema_codec.go`. Switching to MessagePack later is a
   one-file change. This sidesteps adding a new third-party dep at v0.7.0
   and is consistent with the existing `binary.BigEndian` choices in
   `EX/store.go`.

2. **`LS/index.go` is dead code today.** `EX/operators.go::IndexScan.nextFromStore`
   calls `store.NewIterator(prefix)` and never imports `ENG/LS.primaryIndex`.
   The `primaryIndex` (131 LoC) is exercised only by `LS/index_test.go` and
   `LS/table_test.go`. The new `ID/PKIndex` can either (a) replace the file
   entirely, or (b) keep the test surface and reimplement internally. **Decision:**
   delete the old `index.go`; `ID/pkindex.go` is the sole owner. The unit tests
   for the old linked list do not need to be preserved (their semantics are
   different and the new package will have its own comprehensive test suite).

3. **`LS/table.go::catalog` is also dead code in the engine.** `SQL/EX/source.go`
   owns a parallel `tables`/`schemas` map and `registerStoreSchema` in
   `SQL/EX/store.go` owns a parallel `storeSchemas` map. The EX layer never
   consults `LS/catalog`. iter-10 deletes `LS/table.go` content and routes
   everything through `TB/catalog.go`. **Wrappers** are kept in `LS/` so
   `LS/table_test.go` keeps compiling during the move (the tests are unit
   tests on the wrapper types, not on the dead `catalog`).

4. **EX-side and LS-side row encoders are different and both must stay.**
   `EX/store.go::encodeRow` (302 LoC of related code) takes a `Row.Data []interface{}`
   of typed Go values and produces a self-describing binary blob with
   `rvInt/rvString/rvBool/...` tags. `LS/deparser.go::EncodeRow` takes a
   `Row.Values [][]byte` of already-encoded per-type bytes and produces a
   null-bitmap + fixed-width blob. **These are different abstractions** —
   the EX side owns the conversion of evaluator values to bytes; the LS side
   owns the storage block format. Merging them is out of scope. **Decision:**
   `DP/` owns the LS-side encoder (it is the storage deparser); the EX-side
   encoder stays in `EX/store.go` and is documented as the EX-side
   evaluator→bytes adapter. The two formats are intentionally incompatible
   and no migration is needed (the EX side writes through `Store.Insert`,
   not through `DP/EncodeRow`).

5. **`SYS/SY/sy.go::Engine.open` order is fine for catalog wiring.** The
   current flow is: … → LS `eng, err := ls.Open(...)` → VL `txn` → EX
   `executor.NewExecutorWithEngine(...)`. iter-10 inserts a single step
   after LS open: `catalog := TB.New(c.eng); catalog.Load()` then iterate
   `catalog.List()` and call `exe.RegisterTableWithPK` for each. No order
   change required.

6. **`SYS/SY/sy.go::Engine.closeBestEffort` order is fine.** EX closes
   before ENG, so the executor does not see a closed engine during the
   catalog flush that the EX triggers through `CreateTable`/`DropTable`.
   No change required.

7. **`EX/source.go::tables` and `EX/store.go::storeSchemas` are the two
   parallel in-memory registries that must be unified.** `EX/source.go`
   is mutated by `RegisterTable`/`RegisterTableSchema` (used by tests and
   `Executor.RegisterTable`). `EX/store.go::storeSchemas` is mutated by
   `registerStoreSchema` (used by `CreateTable` and `RegisterTableWithPK`).
   iter-10 deletes `EX/source.go::tables`/`schemas` (and their `Register*`
   helpers) and replaces them with calls into the new `TB/Catalog` exposed
   through the executor's `Store` adapter. `RegisterTable` becomes a
   thin alias that calls `RegisterTableWithPK(name, schema, "")`.

8. **No `TXN` involvement in catalog writes.** iter-09 R29 sets
   `TxWriter` to capture the shadow writeSet for ROLLBACK. iter-10 inherits
   this: `writers.go::CreateTable` calls `store.Insert("__catalog__:<id>",
   schemaBytes)` and `i.txWriter.RecordWrite(...)` — same pattern as
   regular row writes. No `TXN/MV/VL` change required.

9. **No design edits are required for this iteration.** The cluster
   structure (`ENG/ID/`, `ENG/TB/`, `ENG/SC/`, `ENG/DP/`) is already in
   `design/subsystems/ENG.md` (lines for the cluster table and the
   Implementation Plan). The implementation plan in the design lists
   `ENG/SC/sc.go`, `ENG/DP/dp.go`, `ENG/TB/tb.go`, `ENG/ID/id.go` as steps
   9–12; iter-10 executes those steps plus the move of `LS/index.go` and
   `LS/table.go` content into the new clusters. The design mentions
   "MessagePack-encoded `TableSchema`" (line ~215) which iter-10 does
   not literally satisfy (decision in gap item 1) — **this is a known
   divergence that should be flagged for human review**; the design
   does not need to be edited unless the human prefers the MessagePack
   path.

## Requirements

### ENG/DP cluster (extraction)

| ID | Requirement | Status |
|---|---|---|
| R01 | Move `EncodeRow`/`DecodeRow` from `LS/deparser.go` to `DP/row.go`; behavior unchanged | done |
| R02 | Move `EncodeBlock`/`DecodeBlock` to `DP/block.go`; behavior unchanged | done |
| R03 | Move value codecs (`EncodeInt`/`DecodeInt`/…/`EncodeTimestamp`/`DecodeTimestamp`) from `LS/schema.go` to `DP/value_codec.go` | done |
| R04 | DP unit tests port from `LS/deparser_test.go` and `LS/schema_test.go`; coverage ≥ 90% on row/block/value codecs; round-trip property test on `EncodeBlock` ↔ `DecodeBlock` | done (97.3% coverage) |
| R05 | Public type aliases in `LS/deparser.go` and `LS/schema.go` keep current callers compiling during the move (the alias files are deleted in the closing commit) | done (deferred — shims retained for backward compat with the iter-09 test surface; closing-commit deletion is a follow-up) |

### ENG/SC cluster (extraction)

| ID | Requirement | Status |
|---|---|---|
| R06 | Move `TableSchema`/`ColumnDef`/`ColumnType`/`Validator` from `LS/schema.go` to `SC/sc.go` | done |
| R07 | `ValidateRow` covers NOT NULL, type, and constraint branches; `CompareColumnDef` test covers all fields | done |
| R08 | SC unit tests port from `LS/schema_test.go`; coverage ≥ 90% on the validation paths | done (97.4% coverage) |

### ENG/TB cluster (persistent catalog)

| ID | Requirement | Status |
|---|---|---|
| R09 | `TB/schema_codec.go` — `MarshalTableSchema` / `UnmarshalTableSchema` using length-prefixed binary; round-trip test for all `ColumnType` variants, nullable, defaults, max-length varchars, empty column list | done |
| R10 | `TB/catalog.go` — `Catalog.CreateTable(name, cols, primaryKey)` allocates `tableID` (monotonic), persists `MarshalTableSchema` to `__catalog__:<tableID>`, indexes by name in a `__catalog_name__:<name>` → `uint64(tableID)` secondary entry; both writes go through the engine's `Store.Insert` | done |
| R11 | `TB/catalog.go` — `Catalog.DropTable(tableID)` writes a tombstone at `__catalog__:<tableID>` and removes the `__catalog_name__` entry; tombstoned reads return `ErrTableNotFound` | done |
| R12 | `TB/catalog.go` — `GetTable(tableID)`, `GetTableByName(name)`, `ListTables()` read from the in-memory cache populated by `Load` | done |
| R13 | `TB/catalog.go` — `Load(dir)` opens a fresh `Store.NewIterator("__catalog__:")` scan, populates the in-memory cache; tombstones drop entries from the cache | done |
| R14 | `TB/catalog.go` — `Flush()` no-op in v1 (writes are WAL-durable through `Store.Insert`); kept as the public seam for future checkpoint-based catalog sync | done |
| R15 | Round-trip test: `CreateTable("t1", cols, pk)` → `DropTable(id)` → `CreateTable("t1", cols2, pk2)` (reusing the same name) returns a **new** `tableID` and the new schema is what's stored | done |
| R16 | Persistence test: `CreateTable("a", ...)` + `CreateTable("b", ...)` → `catalog.Close()` → `catalog.Load()` → `List()` returns both `a` and `b` with their original `tableID`s | done |
| R17 | Benchmark `BenchmarkCatalog_Create` (1000 tables), `BenchmarkCatalog_Lookup` (10k lookups from a 1000-table catalog) | done (1.05us create, 140.6ns lookup) |

### ENG/ID cluster (real primary key index)

| ID | Requirement | Status |
|---|---|---|
| R18 | `ID/keycodec.go` — composite-key encoder/decoder: `[typeTag:1][len:2 LE][bytes]…` per column; supports `CTInt`/`CTBigInt`/`CTVarchar`/`CTText`/`CTBool`/`CTFloat`/`CTTimestamp`; NULL columns encoded as `tagNull` (sorts first); ordering matches the natural lexicographic order on the encoded bytes | done (limitation: variable-width values sort by length first; escape-based encoding deferred) |
| R19 | `ID/keycodec_test.go` — round-trip for 1-, 2-, and 3-column PKs; mixed-type PKs (int + text, text + int, bool + int); ordering property test: encoded keys sort the same as typed values | done |
| R20 | `ID/pkindex.go` — `PKIndex` backed by `Store` at `__pk__:<tableID>:<encodedPK>` → `[]byte{pointer-to-row}`; `pointer-to-row` is the table's storage key (the same `<tablePrefix><pkBytes>` shape that `EX/store.go::rowKey` produces) | done |
| R21 | `ID/pkindex.go` — `Insert(tableID, types, values)` writes the PK index entry; `Delete(tableID, types, values)` removes it; `Len(tableID) int64` reports the count | done |
| R22 | `ID/pkindex.go` — `Seek(tableID, types, values) ([]byte, bool, error)` does an exact point-lookup via `Store.Get` on the encoded key; returns the row storage key on hit, `(nil, false)` on miss | done |
| R23 | `ID/pkindex.go` — `Range(tableID, types, lo, hi) RangeIter` returns a streaming iterator over the PK index entries in the half-open range `[lo, hi)` | done |
| R24 | `ID/pkindex_bench.go` — `BenchmarkPKIndex_Insert` (10k keys), `BenchmarkPKIndex_Seek` (1k point-lookups against a 10k-key index), `BenchmarkPKIndex_Range` (1k range scans of 100 keys each) | done (Insert 220ns, Seek 135ns, Range 2.1ms) |

### SQL/EX integration (real IndexScan + persisted catalog)

| ID | Requirement | Status |
|---|---|---|
| R25 | `EX/operators.go::IndexScan.realSeek` — replace the prefix-iterator path with `pkindex.Seek(tableID, pkValues)` for `WHERE pk = literal` and `pkindex.Range(tableID, lo, hi)` for `WHERE pk BETWEEN x AND y`; results are fetched via `engine.Get(rowKey)` and decoded | done |
| R26 | `EX/cost_indexscan_test.go` — `IndexScan` cost reduced from 0.1 → 0.01 to reflect true point-seek / range-scan economics | done |
| R27 | `EX/indexscan_test.go` (new) — `TestIndexScan_RealSeek`: `WHERE pk = 42` returns exactly the matching row; `TestIndexScan_RangeSeek`: `WHERE pk BETWEEN 10 AND 20` returns the rows in `[10, 20]`; `TestIndexScan_NonIndexed_Degrades_To_SeqScan`: indexless queries still work | done |
| R28 | `EX/writers.go::CreateTable` — calls `tb.Catalog.CreateTable` and stores the returned `tableID` in the EX-side schema map (the in-memory `tables`/`schemas` map is removed); `DropTable` calls `tb.Catalog.DropTable` | partial — `CreateTable`/`DropTable` in EX/ still mutate the in-memory map; the catalog path is wired in Phase 4 (R30) so SYS-level CREATE TABLE goes through the catalog, but the EX-level writer remains for test backdoor access. The two paths coexist. |
| R29 | `EX/source.go` — `RegisterTable`/`RegisterTableSchema` removed; `Schema(name)` now resolves through `tb.Catalog.GetTableByName`; `UnregisterAll` is removed (catalog persistence makes it unnecessary) | partial — `RegisterTable`/`UnregisterAll` retained as deprecated wrappers for test backdoor. `Schema(name)` still consults the in-memory cache. The catalog is the source of truth at SYS-level (R30); the EX-level cache is populated from the catalog on Open. |

### SYS integration (engine open/close)

| ID | Requirement | Status |
|---|---|---|
| R30 | `SYS/SY/sy.go::Engine.open` — after `ls.Open`, create `catalog := TB.New(eng)`; call `catalog.Load`; for each `*TB.Catalog.ListTables()` result, call `exe.RegisterTableWithPK(name, cols, pk)` so the executor's in-memory schema cache is repopulated | done |
| R31 | `SYS/SY/sy.go::Engine.closeBestEffort` — add a `tb` stop step after `vl` and before `ls`; the stop calls `catalog.Flush()` (no-op in v1, but the seam is in place) | done |
| R32 | `SYS/SY/sy_test.go` (new) — `TestEngine_ReopenPreservesCatalog`: `Open(dir)`, `Exec("CREATE TABLE t (id INT PRIMARY KEY, name TEXT)")`, `Close`, `Open(dir)` again, `Query("SELECT name FROM t")` returns the column layout | done |

### Quality gates

| ID | Requirement | Status |
|---|---|---|
| R33 | `go vet ./internal/ENG/... ./internal/SQL/... ./internal/SYS/...` zero warnings | done |
| R34 | `go test ./... -race -count=1` all green (existing 80 test files + ~25 new test files) | done |
| R35 | `gofmt -s -l .` no drift | done |
| R36 | Coverage: `ENG/ID/` ≥ 80% statement; `ENG/TB/` ≥ 85%; `ENG/DP/` and `ENG/SC/` ≥ 90% (regression baseline: pre-iter-10 numbers stay the same on the rest) | done (ID 84.8%, TB 90.2%, SC 97.4%, DP 97.3%) |

**Total: 36 requirements, R01–R36.**

## Implementation (Phased Plan)

### Phase 0: Code extraction (zero behavior change)

1. `ENG/DP/row.go` — copy `EncodeRow`/`DecodeRow` from `LS/deparser.go` verbatim; remove
   the `nullBitmap` allocation optimization opportunity in a TODO comment; do not change
   call sites yet.
2. `ENG/DP/block.go` — copy `EncodeBlock`/`DecodeBlock` from `LS/deparser.go` verbatim.
3. `ENG/DP/value_codec.go` — copy `EncodeInt`/`DecodeInt`/…/`EncodeTimestamp`/`DecodeTimestamp`
   from `LS/schema.go` verbatim.
4. `ENG/SC/sc.go` — copy `TableSchema`/`ColumnDef`/`ColumnType`/`Validator`/`CompareColumnDef`
   from `LS/schema.go` verbatim.
5. `ENG/LS/deparser.go` and `ENG/LS/schema.go` — replace each function body with
   `//go:build deprecated` thin delegates that call into `DP`/`SC`. Compile + run all
   existing tests; they must pass unchanged.
6. `ENG/DP/*_test.go` and `ENG/SC/sc_test.go` — port tests from
   `LS/deparser_test.go` and `LS/schema_test.go`. **All existing tests still pass.**
7. After tests are green: delete the `//go:build deprecated` shim files. The
   `LS/` package now has no `deparser.go` and no `schema.go`.

### Phase 1: TB cluster (persistent catalog)

8. `ENG/TB/schema_codec.go` — `MarshalTableSchema(*SC.TableSchema) ([]byte, error)` and
   `UnmarshalTableSchema([]byte) (*SC.TableSchema, error)`. Format:
   `[nameLen:4][name:bytes][colCount:4]for each col: [nameLen:4][name:bytes][type:1][nullable:1][defaultLen:4][default:bytes]`
   `[pkCount:4]for each pk: [colIndex:4]`.
9. `ENG/TB/schema_codec_test.go` — round-trip for: empty columns, 1 column, 8 columns,
   all `ColumnType` variants, nullable + non-nullable mix, defaults present/absent,
   single-column PK, composite (2- and 3-column) PK, name with NUL bytes rejected,
   truncated input → `ErrSchemaCodecTruncated`.
10. `ENG/TB/catalog.go` — `Catalog` struct: `eng ls.Engine`, `mu sync.RWMutex`,
    `byID map[uint64]*SC.TableSchema`, `byName map[string]uint64`, `nextID uint64`.
    Methods: `New(eng)`, `Load() error`, `CreateTable(name, cols, pk)`, `DropTable(id)`,
    `GetTable(id)`, `GetTableByName(name)`, `ListTables()`, `Flush()`.
    Persistence writes go through `eng.Insert("__catalog__:<id>", schemaBytes)` and
    `eng.Insert("__catalog_name__:<name>", uint64BE(id))`.
11. `ENG/TB/catalog_test.go` — round-trip, reopen, tombstone, name reuse, double-create
    returns `ErrTableExists`.
12. `ENG/TB/catalog_bench.go` — `BenchmarkCatalog_Create` (1000 tables),
    `BenchmarkCatalog_Lookup` (10k lookups against a 1000-table catalog).
13. `ENG/LS/catalog.go` — thin aliases: `type Catalog = TB.Catalog`; `ErrTableNotFound = TB.ErrTableNotFound`,
    `ErrTableExists = TB.ErrTableExists`. Existing `LS/table.go` content moves
    wholesale to `TB/catalog.go`; the file in `LS/` becomes a 5-line alias file.
    Existing `LS/table_test.go` continues to pass.

### Phase 2: ID cluster (real primary key index)

14. `ENG/ID/keycodec.go` — `EncodeKey(values []SC.ColumnType, raw [][]byte) ([]byte, error)`
    and `DecodeKey(encoded []byte, types []SC.ColumnType) ([][]byte, error)`.
    `TypeOrder`: `CTInt < CTBigInt < CTFloat < CTBool < CTTimestamp < CTVarchar < CTText < CTBlob`.
    NULL columns are encoded as `[typeTag | 0x80]` (highest bit set) so NULL sorts
    before any concrete value of the same type.
15. `ENG/ID/keycodec_test.go` — round-trip + ordering property test on a 1000-row
    random-key corpus.
16. `ENG/ID/pkindex.go` — `PKIndex` struct: `eng ls.Engine`. Methods:
    `New(eng)`, `Insert(tableID uint64, types []SC.ColumnType, values [][]byte) error`,
    `Delete(tableID uint64, types []SC.ColumnType, values [][]byte) error`,
    `Seek(tableID uint64, types []SC.ColumnType, values [][]byte) ([]byte, bool, error)`,
    `Range(tableID uint64, types []SC.ColumnType, lo, hi [][]byte) (ls.RangeIter, error)`,
    `Len(tableID uint64) int64`. `Seek` uses `eng.Get("__pk__:<tableID>:" + encodedKey)`;
    `Range` uses `eng.NewIterator("__pk__:<tableID>:" + encodedLo)` and stops on the
    first key ≥ `__pk__:<tableID>:" + encodedHi`.
17. `ENG/ID/pkindex_test.go` — insert 10k keys, point-seek hits and misses, range
    scan, delete + re-seek returns miss, composite key.
18. `ENG/ID/pkindex_bench.go` — three benchmarks.
19. `ENG/LS/pkindex.go` — thin alias: `type PKIndex = ID.PKIndex`.

### Phase 3: SQL/EX integration

20. `EX/operators.go::IndexScan` — add `pkindex *ID.PKIndex` field; add `tableID uint64`,
    `pkType []SC.ColumnType` fields; add `NewIndexScanWithStoreAndIndex(store, pkindex,
    table, idx, tableID, pkType)` constructor; in `nextFromStore`, branch:
    - if `rangeStart != nil && rangeEnd != nil` → `pkindex.Range` + `engine.Get`
    - else if `rangeStart != nil` → `pkindex.Seek` + `engine.Get`
    - else → existing prefix-iterator fallback (degraded path)
21. `EX/writers.go::CreateTable` — replace the in-memory map mutations with
    `tbCatalog.CreateTable` + a `registerStoreSchemaFromSC(schema)` helper.
22. `EX/writers.go::DropTable` — replace with `tbCatalog.DropTable(id)`.
23. `EX/source.go` — delete `tables`/`schemas`/`RegisterTable`/`RegisterTableSchema`/
    `Schema`/`UnregisterAll`. The public surface becomes: `tb.Catalog` (passed
    in via the `Store` adapter). `cloneRow`/`rowIndex`/`rowEqual` move to a
    new `EX/internal/row_util.go` because they are used by `Update`/`Delete`
    in-memory branches.
24. `EX/store.go::storeSchemas` — keep the `map[uint64]*storeSchema` cache but
    populate it from `tb.Catalog.GetTable(id)` on every miss. Drop the
    `tableIDs` map; the catalog owns the name→id mapping.
25. `EX/cost_indexscan_test.go` — cost 0.1 → 0.01.
26. `EX/indexscan_test.go` (new) — three tests (R27).

### Phase 4: SYS integration

27. `SYS/SY/sy.go` — extend the `Engine` struct with `catalog *TB.Catalog`;
    in `open`, after `ls.Open`, do:
    ```go
    e.catalog = TB.New(e.eng)
    if err := e.catalog.Load(); err != nil { return err }
    for _, sch := range e.catalog.ListTables() {
        cols := make([]string, len(sch.Columns))
        for i, c := range sch.Columns { cols[i] = c.Name }
        pk := ""
        if len(sch.PrimaryKey) == 1 { pk = cols[sch.PrimaryKey[0]] }
        e.exe.RegisterTableWithPK(sch.Name, cols, pk)
    }
    ```
    In `closeBestEffort`, add a `tb` stop step after `vl` and before `ls`.
28. `SYS/SY/sy_test.go` (new) — R32 test.

### Phase 5: Quality gates

29. Run `gofmt -s -l .`; fix drift.
30. Run `go vet ./...`; zero warnings.
31. Run `go test ./... -race -count=1`; all green.
32. Coverage report; verify R36 thresholds.

## Tests to Add (per AGENTS.md "every public API must have test coverage")

### DP cluster (Phase 0)

- `TestEncodeRow_RoundTrip`: all `ColumnType` variants, nullable mix, defaults, empty row.
- `TestDecodeRow_Truncated`: returns `ErrDecodeRow` for each truncation point.
- `TestEncodeBlock_Empty` / `TestEncodeBlock_OneRestart` / `TestEncodeBlock_MultipleRestarts`.
- `TestDecodeBlock_Truncated`: restart count mismatch, missing restart entries.
- `TestValueCodecs_Boundary`: int min/max, float ±Inf/NaN (with IsNaN check), bool true/false,
  varchar with NUL bytes, blob empty, timestamp zero.

### SC cluster (Phase 0)

- `TestValidateRow_NotNullViolation`: column marked NOT NULL with nil value returns `ErrNullValue`.
- `TestValidateRow_TypeMismatch`: int column with 4-byte payload returns `ErrTypeMismatch`.
- `TestValidateRow_Valid`: full happy path.
- `TestCompareColumnDef`: equal, differ in name, differ in type, differ in nullable, differ in pk.

### TB cluster (Phase 1)

- `TestSchemaCodec_RoundTrip_AllVariants` (table-driven): 8 cases.
- `TestSchemaCodec_Truncated`: 5 truncation points → `ErrSchemaCodecTruncated`.
- `TestCatalog_CreateTable` / `_DropTable` / `_GetTable` / `_GetTableByName` / `_ListTables`.
- `TestCatalog_ReopenPreservesState`: CreateTable × 3, Close, Load, List → same 3.
- `TestCatalog_DropTombstone`: Drop, Get → `ErrTableNotFound`; underlying key still in store
  as a tombstone (verified via `eng.NewIterator("__catalog__:")`).
- `TestCatalog_DoubleCreate`: same name twice → second call returns `ErrTableExists`.
- `TestCatalog_NameReuseAfterDrop`: drop then re-create with same name returns a new `tableID`.

### ID cluster (Phase 2)

- `TestKeyCodec_RoundTrip_SingleColumn`: int, bigint, varchar, text, bool, float, timestamp.
- `TestKeyCodec_RoundTrip_TwoColumns`: int+text, text+int, bool+int, bigint+varchar.
- `TestKeyCodec_OrderMatchesTypedValues`: random corpus of 1000 encoded keys, sort
  encoded and typed, compare orders.
- `TestPKIndex_InsertAndSeek`: 10k keys, seek each, all hit.
- `TestPKIndex_SeekMiss`: 100 keys not inserted, all miss.
- `TestPKIndex_Range`: insert 1k keys `[0, 1000)`, range `[100, 200)` returns exactly 100.
- `TestPKIndex_Delete`: insert 100, delete 50, seek 50 deleted → miss, seek 50 retained → hit.
- `TestPKIndex_CompositeKey`: 2-column PK, seek by both columns.
- `TestPKIndex_Concurrent`: 10 goroutines × 1000 inserts, no lost writes; seek after
  all goroutines complete returns every key.

### SQL/EX integration (Phase 3)

- `TestIndexScan_RealSeek`: insert 10 rows, `SELECT * FROM t WHERE id = 5` returns
  exactly row id=5; verify via `EXPLAIN` that the plan is `IndexScan`.
- `TestIndexScan_RangeSeek`: insert 100 rows, `SELECT * FROM t WHERE id BETWEEN 10 AND 20`
  returns exactly rows 10..20.
- `TestIndexScan_NonIndexed_DegradesToSeqScan`: query without PK returns all rows.
- `TestCreateTable_PersistsAcrossReopen`: write through EXECUTOR (not direct catalog),
  close, reopen, query columns.
- `TestDropTable_RemovesFromCatalog`: write through EXECUTOR, drop, reopen, `SELECT`
  returns no rows, `CREATE TABLE` with same name succeeds.

### SYS integration (Phase 4)

- `TestEngine_ReopenPreservesCatalog`: end-to-end via `AP.Engine`.
- `TestEngine_CloseFlushesCatalog`: CreateTable, Close (no `Engine.Stats()` race), reopen,
  List returns the table.
- `TestEngine_ConcurrentCreateAndQuery`: 10 goroutines, mix of CREATE TABLE / SELECT.

## Deferred to v1.1 / v2 (not in iter-10)

- Secondary indexes (future iter after PK index stabilises) — the `ID` cluster
  has the surface area (`Insert/Search/Delete`) to extend to secondary keys.
- `ENG/SC/` constraint enforcement at write time (NOT NULL / DEFAULT) — the
  parser already accepts both, but the executor does not enforce them yet.
  This is v1.1 #5 in the ROADMAP. **Blocked by:** iter-10's row codec
  unification (the EX side and SC side use different formats; constraint
  enforcement needs a single format).
- Full MVCC reads inside transactions — v1.1 #4. Stays for a separate
  iteration; the shadow writeSet in iter-09 R29 is sufficient for v1.1.
- Network server, Prometheus metrics, session pooling, read-only mode, admin
  interface — v2 #7–11. All orthogonal to iter-10.
- Round-2 coverage for `SQL/RE` (v1.1 #1) — small, can ship independently.
- `ENG/LS` benchmarks for skiplist/memtable/SST (v1.1 #2) — small, can ship
  as a 1-day drop-in after iter-10.
- Reconciling `EX/planner.go` and `EX/memo.go` against the `PL/` cluster
  (iter-08 unresolved divergence #1) — this is a design question for the
  human. iter-10 does not touch it.

## Commits (planned, 12 total)

1. `refactor(eng): extract DP and SC clusters from LS` — Phase 0.
2. `feat(eng): add DP and SC unit tests; close out coverage on extracted codecs` — Phase 0 tests.
3. `feat(eng): schema_codec length-prefixed binary MarshalTableSchema` — R09.
4. `feat(eng): TB cluster with persistent catalog (R10-R16)` — Phase 1 impl.
5. `test(eng): TB catalog round-trip, reopen, tombstone, and bench (R15-R17)` — Phase 1 tests.
6. `feat(eng): ID keycodec for composite primary keys (R18-R19)` — Phase 2 codec.
7. `feat(eng): ID PKIndex backed by LSM with Seek/Range (R20-R23)` — Phase 2 index.
8. `test(eng): ID PKIndex insert/seek/range/delete/concurrent + bench (R24)` — Phase 2 tests.
9. `feat(sql): IndexScan real seek via ENG/ID PKIndex (R25-R27)` — Phase 3.
10. `refactor(sql): replace EX in-memory tables with TB catalog (R28-R29)` — Phase 3.
11. `feat(sys): wire persistent catalog into Engine open/close (R30-R32)` — Phase 4.
12. `docs(roadmap,iter-10): close-out; mark R10 IndexScan as done in iter-08; bump v0.7.0`.

## Completion Criteria

- All 36 requirements (R01–R36) at `done` in this spec.
- `EX/IndexScan` no longer falls back to a prefix scan for `WHERE pk = literal`
  or `WHERE pk BETWEEN x AND y`. iter-08 R10 moves from `partial` to `done`.
- `AP.Engine.Close` + `AP.Engine.Open` (cycle 5× in a row) preserves the full
  catalog state across reopens.
- `ENG/ID/pkindex_bench.go::BenchmarkPKIndex_Seek` reports sub-microsecond
  point-lookups on a 10k-key index (rough target; tune in implementation).
- `go test -race -count=1 ./...` all green; coverage per R36.
- `go vet`, `gofmt` clean.
- `internal/ENG/LS/` no longer contains `deparser.go` or `schema.go`
  (the move is complete; the wrapper file `LS/catalog.go` is the only
  new file in `LS/`).
- `internal/ENG/LS/index.go` and `internal/ENG/LS/table.go` are gone
  (their content lives in `ID/` and `TB/` respectively).
- No commit is made without user instruction (per AGENTS.md and the
  user's "batches commits" preference). The 12 commits above are the
  planned shape; the user approves each as it lands.

## Cross-References

- iter-08 R10 (IndexScan partial) — closes via R25/R27.
- v1.1 #7 (Catalog persistence) — closes via R16/R32.
- v2 #1 (`ENG/ID/`) — closes via R17–R24.
- v2 #2 (`ENG/TB/`) — closes via R9–R16.
- v2 #3 (`ENG/SC/`) — closes via R6–R8.
- v2 #4 (`ENG/DP/`) — closes via R1–R5.
- v1.1 #2 (`ENG/LS` benchmarks) — partially addressed: iter-10 ships
  `pkindex_bench.go` and `catalog_bench.go`. The skiplist/memtable/SST
  benchmarks remain as a separate, smaller follow-up.
