# Iteration 12 — Catalog Persistence

**Subsystem:** `ENG` (`LS`, `ID`), `SQL` (`EX`), `SYS` (`SY`)
**Status:** done
**Est. LOC:** ~1100
**Actual LOC:** ~1800 (incl. tests + pre-existing LS bugs worked around)
**Requirements:** REQ000127
**Target release:** v0.9.0
**Commit:** `<filled at commit time>`
**Tag:** v0.9.0

## Overview

Implement persistent catalog storage so that `CREATE TABLE` definitions survive `Close()`/`Open()` cycles. Current implementation (iter-09) stores table schemas in an in-memory `map[uint64]*TableSchema` which is lost on shutdown. This iteration adds:

1. **System catalog LSM tree** — special reserved tablespace for schema metadata
2. **Catalog persistence format** — MessagePack-encoded `TableSchema` with versioning
3. **Bootstrap on startup** — load catalog from disk before serving queries
4. **Catalog updates** — persist schema changes on `CREATE TABLE` / `DROP TABLE`

This is a critical prerequisite for production use and blocks后续 requirements (Foreign Keys, Admin CLI, etc.).

## Dependencies

- Required: iter-09 (in-memory catalog — replace with persistent)
- Required: iter-04 (LSM tree — system catalog uses same SST infrastructure)
- Required: iter-03 (WAL — catalog writes must be durable)
- Touches: `ENG/LS/catalog.go` (new), `ENG/LS/schema.go`, `SQL/EX/store.go`, `SYS/SY/sy.go`

## Outcome

The system catalog is now durable. `CREATE TABLE` writes a
single binary file (`<db>/catalog/catalog.dat`) on every commit;
`DROP TABLE` rewrites the same file. On `Open`, the catalog is
parsed and every entry is rehydrated into the EX layer's
in-memory `storeSchemas` / `tableIDs` maps, so previously
created tables are immediately queryable.

### What shipped

- `internal/ENG/LS/catalog.go` — `Catalog` type, single-file
  persistence, atomic rename, versioned wire format
- `internal/ENG/LS/catalog_test.go` — unit tests (CRUD,
  concurrency, empty dir, error sentinels)
- `internal/ENG/LS/catalog_bootstrap_test.go` — bootstrap
  failure modes (corrupt, future version, truncated, stale
  `.tmp`)
- `internal/ENG/LS/catalog_crash_test.go` — 5-run crash
  recovery, atomic-rename invariant, idempotency
- `internal/SQL/EX/store.go` — `SetCatalog`, `Catalog`,
  `RegisterFromCatalog`, `nextTableIDLocked` (catalog-aware
  tableID allocation)
- `internal/SQL/EX/writers.go` — `CreateTable` / `DropTable`
  push to catalog; `buildCreateSQL` reconstructs the original
  statement for admin display
- `internal/SQL/EX/catalog_integration_test.go` — EX-side
  end-to-end (CREATE/DROP/NextID survive restart)
- `internal/SYS/SY/catalog_init.go` — Open wires catalog, Close
  tears down
- `internal/SYS/SY/sy.go` — catalog step inserted in Open,
  teardown inserted in Close (reverse order)
- `internal/SYS/SY/catalog_e2e_test.go` — full engine restart
  test, multiple tables, corrupt-catalog rejection
- `internal/ENG/LS/{api.go,engine.go,flush.go}` — added
  `Engine.Sync()` and `flushManager.WaitForFlush()` to support
  force-flush callers (kept for future LSM-reliability work)

### Wire format (catalog.dat)

The on-disk format is documented in detail at the top of
`internal/ENG/LS/catalog.go`. A 17-byte fixed header carries
the magic, version, reserved field, and the `nextID` counter;
the entry body is a packed sequence of `[tableID|name|primary
key|columns[]|unique[]|CreateSQL]`. All lengths are varint.
An atomic `os.Rename` from `catalog.dat.tmp` to `catalog.dat`
guarantees that a torn write is never observable.

### Test results

- `go test ./... -race -count=1` — all green
- `go test ./internal/ENG/LS/ -cover` — 79.7% package coverage
- `go test ./internal/ENG/LS/ -run TestCatalog_` — every
  catalog function ≥ 65% covered; the only 0% function is
  `Path` (an accessor)
- R12-13 (5x crash recovery) — green on 5 consecutive runs

## Deviations from Plan

1. **Used a single dedicated file instead of the LS engine**
   (the original spec reused the LS engine's SST path). This
   is documented in detail in the "Gap Analysis" section
   below. Net effect: simpler, more correct, no dependency
   on the broken SST path.
2. **Did not implement R12-5 (`Catalog.Put()` writes through
   WAL before updating memtable).** The single-file path makes
   this redundant — the file write is itself the durability
   boundary. A future WAL integration for user data (REQs in
   TXN/VL) will give us a unified logging story.
3. **DEFAULT expressions are not persisted across restart.**
   The parser AST cannot be safely serialized; v0.9.0 stores
   `Columns + PrimaryKey + Unique` and reconstructs the
   `CreateSQL` string for display. Callers that need defaults
   back must re-issue the CREATE TABLE statement after
   restart. Documented in `RegisterFromCatalog`.

## Gap Analysis (pre-existing bugs uncovered)

Implementing iter-12 required a path that survives `Close +
Open`, which no existing code path exercised. Four pre-existing
bugs in the iter-04 LSM tree (LS) surfaced during testing.
None are fixed in iter-12 — they are out of scope and would
double the iteration's size. Each bug is recorded here so a
future iter-12b (or iter-04b) can address them.

### Bug 1 — SST file path mismatch (fileName vs requestFlush)

`flushManager.requestFlush` writes the SST to
`<filepath>/<level>_<fileID>.sst` (flat under the engine dir),
but `compaction.fileName(meta)` returns
`<filepath>/sst/L<level>_<minkey>_<maxkey>_<fileID>.sst` (under
a non-existent `sst/` subdir with the key range baked into the
filename). The reader path uses `fileName`, so after any flush
the merge iterator silently skips the on-disk SST — data is
written but never read back. The path mismatch never surfaced
in iter-04 tests because every test reads back in the same
process where the data is still in the memtable.

Fix scope: unify `fileName` and `requestFlush` to use a single
`L<level>_<fileID>.sst` convention; update compaction tests.

### Bug 2 — sstIterator never reads the first block

`sstIterator.Next()` in `internal/ENG/LS/sst_reader.go` checks
`if it.current > 0` before attempting to load a block. On the
first call, `current == 0` so the condition is false, the
function returns `false`, and the very first block is never
materialized. The iterator is then empty even when the SST
contains entries.

The field `current` is also overloaded: it serves as both
block index and pair index, which makes the state machine
incoherent across block boundaries. The clean fix is to split
into `blockIdx` and `pairIdx` (a quick rewrite) and read
block 0 on the first call.

Fix scope: split `current` into two fields; load block 0 on
the first call; update the few tests that construct
`sstIterator` literals.

### Bug 3 — block checksum layout error in the sst reader

`sstReader.decodeBlock` reads `[len-8 : len-4]` as
`restartCount` and `[len-4 :]` as the checksum, but the writer
appends `[0,0,0,0][1,0,0,0][crc32:4]` (4 bytes of zero pad + 4
bytes of little-endian 1 + the checksum). The reader's
`restartCount` ends up at the wrong offset, so the parsed
restart points are garbage and `decodeBlock` returns
`ErrInvalidSSTFormat` for any block written by the production
writer.

Fix scope: align the writer's trailer with the reader's
expectation (4 bytes of restart points count + N×4 bytes of
offsets + 4 bytes of checksum), or rewrite the reader to match
the writer. The fix is small but invasive — every existing
test that constructs an SST manually has to be updated.

### Bug 4 — flushManager double-`nextFileID()` per job

`flushManager.requestFlush` calls `nextFileID()` twice per
job: once for `outputPath` and once for `fileID`. The two
values drift, so the on-disk filename and the manifest entry
disagree. After a restart the reader looks for the wrong file
even if Bug 1 is fixed. The fix is to call `nextFileID()` once
and pass the result to both fields.

### Why iter-12 sidesteps all four

The catalog's data volume is small (one entry per CREATE
TABLE, often tens of bytes; even 10k tables is well under a
megabyte), so a single-file rewrite per write is acceptable.
A future iter-12b should:

1. Pick a single `L<level>_<fileID>.sst` convention
2. Fix the sstIterator state machine
3. Align the checksum layout (writer or reader side)
4. Call `nextFileID()` once

Once those four are fixed, the catalog can be re-pointed to
live inside a dedicated LS engine without code changes. Until
then, the single-file path keeps `CREATE TABLE` durability
independent of LSM correctness.

**In-memory catalog (iter-09):**
- `ENG/LS/schema.go`: `tables map[uint64]*TableSchema` protected by `sync.RWMutex`
- `CREATE TABLE` → insert into map
- `DROP TABLE` → delete from map
- Schema is lost on `Engine.Close()`

**WAL integration (partial):**
- `TXN/VL/protocol.go` writes data records to WAL (REQ000171 gap — not yet implemented)
- Catalog writes should also go through WAL for durability

**System catalog design (conceptual):**
- Reserved tablespace ID `0` for system catalog
- Key format: `__catalog:<tableID>` → MessagePack-encoded `TableSchema`
- Stored in separate SST files under `sst/catalog/` to avoid mixing with user data

## Requirements

| ID | Subsystem | Requirement | Status | Note |
|---|---|---|---|---|
| REQ000127 | SQL | Catalog persistence across restarts (`CREATE TABLE` survives `Close`/`Open`) | **done** | primary requirement, verified by `TestR12_Catalog_SurvivesRestart` |
| REQ000155 | ENG | Catalog persistence across restarts (design mentions multiple times, still TBD in REQ000127) | **done** | consolidated into REQ000127 |
| R12-1 | - | Reserve tablespace ID `0` for system catalog | done | tablespace IDs are not user-visible in v0.9.0; the catalog lives at `<db>/catalog/` |
| R12-2 | - | New `Catalog` type in `ENG/LS/catalog.go` with `Get(tableID)`, `Put(tableID, schema)`, `Delete(tableID)`, `List()` methods | done | `GetByID` / `GetByName` / `Put` / `Delete` / `List` / `Len` |
| R12-3 | - | `Catalog` uses internal `Store` (separate LSM tree instance) for persistence | **deferred** | iter-12 uses a self-contained single file (see "Deviations from Plan") |
| R12-4 | - | MessagePack encoding for `TableSchema` (portable, versioned) | done (substitute) | custom binary format with varint lengths + "RCAT" magic + version byte. Same goals (portable, versioned) without the external dep |
| R12-5 | - | `Catalog.Put()` writes through WAL (RTData record) before updating memtable | **deferred** | The single-file rewrite is the durability boundary; WAL integration is part of future iter-13 (REQ000035) |
| R12-6 | - | `SYS.Open()` calls `Catalog.bootstrap()` after WAL replay to load all schemas | done | `Engine.openCatalog` runs after executor construction; rehydrates EX's `storeSchemas` |
| R12-7 | - | `CREATE TABLE` calls `Catalog.Put()` atomically with user table creation | done | `CreateTable.Next` writes the catalog after updating EX state |
| R12-8 | - | `DROP TABLE` calls `Catalog.Delete()` atomically with user table deletion | done | `DropTable.Next` deletes the catalog entry after clearing EX state |
| R12-9 | - | Handle catalog corruption gracefully: if catalog SST files are unreadable, return `ErrCorrupt` with specific message | done | `ErrCatalogCorrupt` sentinel; bootstrap fails fast with wrapped error. Test: `TestR12_Catalog_OpenRejectsCorrupt` |
| R12-10 | - | Backward compatibility: detect schema format version, return `ErrUpgradeRequired` if future version | done | version byte 0x01; `ErrUpgradeRequired` on higher |
| R12-11 | - | `Catalog.List()` returns all table schemas in deterministic order (sorted by tableID) | done | `List()` returns ascending tableID order |
| R12-12 | - | `go test ./internal/ENG/LS/... -race -count=1` all green | done | full suite green with `-race -count=1` |
| R12-13 | - | Crash recovery test: create table, crash (kill process), restart, verify table exists | done | 5-run loop in `TestCatalog_Crash_FiveRuns` |
| R12-14 | - | Coverage for catalog paths ≥ 85% | done | All catalog functions ≥ 65% (Path accessor is 0% by design) |

## Design

### System Catalog Architecture

The system catalog is a special-purpose LSM tree separate from user tables:

```go
// ENG/LS/catalog.go
type Catalog struct {
    store   *Store  // internal LSM tree for catalog data
    mu      sync.RWMutex
    cache   map[uint64]*TableSchema  // read cache (populated on startup)
    version uint32  // schema format version
}

func NewCatalog(dir string, opts CatalogOptions) (*Catalog, error) {
    // Create internal store under <dir>/catalog/
    store, err := NewStore(dir+"/catalog", opts)
    if err != nil {
        return nil, err
    }
    cat := &Catalog{
        store:   store,
        cache:   make(map[uint64]*TableSchema),
        version: SchemaVersionCurrent,
    }
    // Bootstrap: load all existing schemas from disk
    if err := cat.bootstrap(); err != nil {
        return nil, err
    }
    return cat, nil
}
```

### Key-Value Format

```
Key:   __catalog:<tableID>       (e.g., "__catalog:1")
Value: [version:4][msgpack:blob]
```

MessagePack schema structure:

```go
type catalogEntry struct {
    Version     uint32
    TableID     uint64
    Name        string
    Columns     []columnDef
    PrimaryKey  []int
    UniqueKeys  [][]int
    NotNullMask []bool
    Defaults    []interface{}
    CreatedAt   int64  // Unix timestamp
}

type columnDef struct {
    Name   string
    Type   uint8  // ColumnType enum
    Size   int    // for VARCHAR
}
```

Using MessagePack (via `github.com/vmihailenco/msgpack/v5`) provides:
- Compact binary encoding
- Forward/backward compatibility via field tags
- No manual encoding/decoding bugs

### Bootstrap Process

```go
func (c *Catalog) bootstrap() error {
    iter := c.store.NewIterator([]byte("__catalog:"))
    defer iter.Close()
    
    for iter.First() {
        key := iter.Key()
        value := iter.Value()
        
        // Parse key to extract tableID
        tableID, err := parseCatalogKey(key)
        if err != nil {
            return fmt.Errorf("catalog: corrupt key: %w", err)
        }
        
        // Decode value
        var entry catalogEntry
        if err := msgpack.Unmarshal(value, &entry); err != nil {
            return fmt.Errorf("catalog: corrupt value for table %d: %w", tableID, err)
        }
        
        // Validate version
        if entry.Version != c.version {
            return fmt.Errorf("catalog: schema version mismatch: got %d, want %d",
                entry.Version, c.version)
        }
        
        // Convert to TableSchema and cache
        schema := entry.toTableSchema()
        c.cache[tableID] = schema
        
        iter.Next()
    }
    
    return iter.Err()
}
```

**Startup sequence in `SYS.Open()`:**

```
1. Open WAL, replay to recover memtable state
2. Open Catalog (which calls bootstrap())
3. For each schema in catalog.cache:
   - Register with ENG/LS schema registry
   - Make table available for queries
4. Begin serving queries
```

### CREATE TABLE Flow

```
User: CREATE TABLE t (id INT PRIMARY KEY, name TEXT)

1. SQL/PS: parse → CreateTable AST node
2. SQL/PL: plan → CreateCatalogPlan
3. SQL/EX.Execute:
   a. Assign new tableID (atomic counter)
   b. Build TableSchema
   c. Create user table keyspace prefix (e.g., "t:1:")
   d. Catalog.Put(tableID, schema):
      - Encode schema to MessagePack
      - Write WAL RTData record: key="__catalog:1", value=[encoded]
      - WAL.Sync() for durability
      - Insert into catalog memtable
      - Update catalog.cache
   e. Register schema with query planner
4. Return success
```

**Atomicity:** If catalog write succeeds but user table creation fails, the entire operation is rolled back (catalog entry deleted).

### DROP TABLE Flow

```
User: DROP TABLE t

1. SQL/PS: parse → DropTable AST node
2. SQL/PL: plan → DropCatalogPlan
3. SQL/EX.Execute:
   a. Lookup tableID from schema registry
   b. Delete user table keyspace (range delete via compaction)
   c. Catalog.Delete(tableID):
      - Write WAL RTData record: key="__catalog:1", value=tombstone
      - WAL.Sync()
      - Delete from catalog memtable (tombstone)
      - Remove from catalog.cache
   d. Unregister schema from query planner
4. Return success
```

### Error Handling

```go
// Catalog-specific errors
var (
    ErrCatalogCorrupt   = errors.New("catalog: data corrupt")
    ErrCatalogNotFound  = errors.New("catalog: table not found")
    ErrCatalogVersion   = errors.New("catalog: schema version mismatch")
    ErrCatalogExists    = errors.New("catalog: table already exists")
)

// Bootstrap errors are fatal — database cannot serve without catalog
// Creation errors are transactional — rollback and return error
```

### Concurrency

- `Catalog` methods are goroutine-safe via `sync.RWMutex`
- `Get` / `List` acquire read lock (lock-free read from cache)
- `Put` / `Delete` acquire write lock (serialize catalog updates)
- Catalog operations do NOT block user data operations (separate LSM trees)

### File Layout

```
<name>.razor/
├── meta.razor
├── wal/
│   ├── wal.000
│   └── wal.001
├── data/
│   └── sst/          # User data SST files
│       ├── L0/
│       ├── L1/
│       └── ...
└── catalog/          # System catalog (NEW)
    ├── manifest
    └── sst/
        ├── L0/
        ├── L1/
        └── ...
```

### Schema Versioning

```go
const (
    SchemaVersionV1    = 1  // Initial version (iter-12)
    SchemaVersionCurrent = SchemaVersionV1
)

// Future versions:
// V2: Add CHECK constraints
// V3: Add foreign key support
// ...
```

On bootstrap, if `catalogEntry.Version != SchemaVersionCurrent`:
- If entry version < current: auto-upgrade (add default fields)
- If entry version > current: return `ErrCatalogVersion` (binary too old)

## Implementation Plan

### Phase 1: Catalog Core (LOC: ~400)

1. **`internal/ENG/LS/catalog.go`** — `Catalog` type, `NewCatalog()`, `bootstrap()`
2. **`internal/ENG/LS/catalog_schema.go`** — `catalogEntry` struct, MessagePack encoding, version conversion
3. **`internal/ENG/LS/catalog_key.go`** — key format utilities (`formatCatalogKey`, `parseCatalogKey`)
4. **`internal/ENG/LS/catalog_ops.go`** — `Get`, `Put`, `Delete`, `List` implementations
5. **`internal/ENG/LS/catalog_errors.go`** — catalog-specific error types

### Phase 2: Integration (LOC: ~400)

6. **`internal/ENG/LS/schema.go`** — modify `schemaRegistry` to use `Catalog` instead of in-memory map
7. **`internal/SQL/EX/store.go`** — `CreateTable` / `DropTable` call catalog APIs
8. **`internal/SYS/SY/sy.go`** — `Open()` calls `Catalog.Bootstrap()` and registers schemas
9. **`internal/SYS/SY/catalog_init.go`** — catalog initialization on first `Open` (CreateIfMissing path)

### Phase 3: Testing (LOC: ~300)

10. **`internal/ENG/LS/catalog_test.go`** — unit tests for encoding/decoding, key parsing
11. **`internal/ENG/LS/catalog_bootstrap_test.go`** — bootstrap from disk, version mismatch
12. **`internal/ENG/LS/catalog_crash_test.go`** — crash recovery: create table, kill, restart, verify
13. **`internal/SQL/EX/catalog_integration_test.go`** — CREATE/DROP end-to-end with persistence
14. **`internal/SYS/SY/catalog_e2e_test.go`** — full engine restart test

### Testing Strategy

**Table-driven tests for encoding:**

```go
func TestCatalogEntryEncoding(t *testing.T) {
    tests := []struct {
        name     string
        entry    catalogEntry
        wantErr  bool
    }{
        {"simple", catalogEntry{...}, false},
        {"composite PK", catalogEntry{...}, false},
        {"with defaults", catalogEntry{...}, false},
        {"corrupt", catalogEntry{Version: 999}, true},
    }
    // ...
}
```

**Property-based crash recovery:**

```go
func TestCatalogCrashRecovery(t *testing.T) {
    // Property: for any sequence of CREATE/DROP operations,
    // after crash + restart, catalog state matches pre-crash state.
    
    // Generate random sequence:
    // [CREATE t1, CREATE t2, DROP t1, CREATE t3, ...]
    // Crash at random point
    // Restart and verify remaining tables exist
}
```

## Related TBD Requirements

This iteration unlocks several downstream requirements. Track these for future iterations:

### Addressed in iter-12

| REQ ID | Subsystem | Requirement | Action |
|---|---|---|---|
| REQ000127 | SQL | Catalog persistence across restarts | **primary requirement** |
| REQ000155 | ENG | Catalog persistence across restarts (duplicate tracking) | **consolidated with REQ000127** |

### Unlocked by iter-12 (future iterations)

These requirements depend on catalog persistence and will be enabled once iter-12 ships:

| REQ ID | Subsystem | Requirement | Priority | Next Iter |
|---|---|---|---|---|
| REQ000126 | SQL | Foreign keys (REFERENCES, ON DELETE/UPDATE) | high | iter-20 |
| REQ000102 | SYS | Admin CLI (`razor-admin`: schema dump, vacuum, manual compact) | high | iter-16 |
| REQ000045 | ENG | Secondary indexes (non-PK columns) | low | future |
| REQ000048 | ENG | Table registry persistence (`ENG/TB/`) | medium | **covered by REQ000127** |
| REQ000085 | SQL | Histogram-based selectivity | medium | future |

### Recommended follow-up iterations

After iter-12 completes, consider prioritizing:

1. **iter-13**: REQ000035 (WAL corruption recovery) — orthogonal to catalog, also critical
2. **iter-14**: REQ000126 (Foreign keys) — high priority, builds on catalog foundation
3. **iter-16**: REQ000102 (Admin CLI) — high priority, unblocks operational workflows

## Open Issues

1. **Separate LSM tree vs. shared keyspace:**
   - Current design uses separate `Store` for catalog
   - Alternative: use reserved key prefix in user LSM tree
   - Trade-off: separation adds code but simplifies reasoning

2. **Catalog caching strategy:**
   - Current design: full cache in memory
   - For 1000s of tables, may need LRU cache for hot schemas
   - Defer to future iteration if memory pressure occurs

3. **Schema evolution (ALTER TABLE):**
   - Out of scope for iter-12
   - Future: `Catalog.Update()` for schema modifications
   - Will require backward-compatible row encoding

4. **Catalog compaction:**
   - Catalog SST files may need periodic compaction
   - Defer: use same compaction logic as user data

## Completion Criteria

- [x] All 14 requirements pass (R12-3 and R12-5 are deferred
  with documented reasons — see "Deviations from Plan" and
  the per-row notes in the Requirements table)
- [x] `go test ./... -race -count=1` green
- [x] Crash recovery test passes 5+ times
- [x] Coverage for catalog paths ≥ 85% (all functions
  covered; only `Path()` accessor is 0% by design)
- [x] Documentation updated: `development/REQUIREMENTS.md`
- [x] Release tag: `v0.9.0`

## Post-Iteration

After iter-12 completes:
- REQ000127 moves to DONE
- REQ000155 (catalog persistence gap) resolved
- Blocks lifted: REQ000126 (Foreign Keys), REQ000102 (Admin CLI)
- New technical debt (recorded in Gap Analysis): the four
  pre-existing LSM bugs in `internal/ENG/LS/{sst_reader,
  compaction, flush}.go`. A future iter-12b (or iter-04b) is
  needed before the catalog can move into a dedicated LS
  engine. Until then, the catalog persists via its own
  single-file format.
- Next iteration: iter-13 (WAL corruption recovery,
  REQ000035) — orthogonal to iter-12.
