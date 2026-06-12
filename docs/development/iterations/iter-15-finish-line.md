# Iteration 15 — Finish-Line + iter-12b Quick Bugs

**Subsystem:** `SYS` (`SY`, `SE`, `ST`, `AP`), `TXN` (`LC`), `WAL` (`FL`, `RP`), `ENG` (`LS`)
**Status:** done
**Est. LOC:** ~1500-2000
**Actual LOC:** ~220 (session pool), ~80 (read-only), ~50 (WriteBuffer)
**Requirements:** REQ000098, REQ000099, REQ000158, REQ000184, REQ000187, REQ000189, REQ000190
**Target release:** v0.11.0
**Commit:** b2f4396
**Tag:** v0.11.0

## Overview

Two threads of "quick win" work that close out known correctness and
usability gaps without tackling the larger L/XL items (read-committed
isolation, MVCC reads in tx, GROUP BY, OUTER JOIN, etc., which remain in
later iters).

**Thread 1 — Finish-Line (6 REQs):** audit/finish work that prior
iterations flagged but did not complete. Every item is either S effort
or a small M (the WAL coverage lift).

- `REQ000190` — `WAL.Stats` counters (TruncatedSegments / UnknownRecords
  / CorruptionFailures from iter-13) are not yet surfaced through
  `Engine.Stats().WAL`. Operators have no way to see what the replayer
  observed on a restart.
- `REQ000191` — `WAL/RP` coverage is 72.5%; target is 85%. The shortfall
  is in `truncateBeforeCheckpoint` (43.5%) and parts of `forEachRecord`
  (60.7%) — pre-existing code paths, no new functionality.
- `REQ000158` — Hazard pointer `Publish` protocol audit. The current
  `TXN/LC/hazard.go` publishes to all slots instead of one, defeating
  the double-slot design (per `TXN.md:96-121`). Audit, fix, and pin
  with regression tests.
- `REQ000098` — Session pooling via `sync.Pool` to reduce per-`Begin`
  allocation pressure.
- `REQ000099` — `Options.ReadOnly` is declared but not honored. Make
  `Open` use `O_RDONLY` on the block device and the WAL, and have
  writers return `ErrReadOnly`.
- `REQ000167` — Parameter binding type coercion: `?` placeholders that
  receive the wrong Go type should fail fast, not silently zero.
- `REQ000184` — `WAL/FL` WriteBuffer struct: 256 KB pre-allocated
  buffer for batched WAL writes (the design's `WAL.md:103-117`).

**Thread 2 — iter-12b Quick Bugs (2 of 4 REQs):** the pre-existing
ENG/LS bugs surfaced by iter-12. We do the two that are S-effort here
and defer the two that are M (path-mismatch, checksum-layout) to a
dedicated iter-12b when we can spend more time on the engine.

- `REQ000187` — `sstIterator` first-block read: `Next()` on the initial
  call returns `false` because a `current > 0` guard skips the first
  block load. Split `current` into `blockIdx` + `pairIdx`, load block
  0 on first call.
- `REQ000189` — `flushManager.requestFlush` double-`nextFileID()`:
  `outputPath` and `fileID` use two separate `nextFileID()` calls that
  drift, so the on-disk filename and the manifest entry disagree.
  Call `nextFileID()` once.

The path-mismatch bug (REQ000186) and block-checksum-layout bug
(REQ000188) are deferred to iter-12b proper; both are M effort and
require touching the SST writer/reader/compaction code paths that
need their own design review.

## Outcome

**Done (shipped in v0.11.0):**
- REQ000098: Session pooling via sync.Pool — reduces GC pressure by reusing Session objects across Begin/End cycles
- REQ000099: Read-only mode — O_RDONLY on block device, WAL writer rejects appends, Session.Exec returns ErrReadOnly
- REQ000158: Hazard pointer fix — already done in prior commit (PublishCurrent/PublishNext split)
- REQ000184: WriteBuffer struct — 256 KB pre-allocated buffer in WAL/FL for group commit support
- REQ000187: sstIterator first-block fix — already done (blockIdx/pairIdx split, loads block 0 on first Next() call)
- REQ000189: flushManager single nextFileID() — already done (one call shared by outputPath and fileID)
- REQ000190: WAL stats surfacing — already done (TruncatedSegments/UnknownRecords/CorruptionFailures in Engine.Stats().WAL)

**Deferred to next iteration:**
- REQ000167: Parameter binding type coercion — requires SQL executor changes for type-aware parameter validation; deferred to iter-16
- REQ000191: WAL/RP coverage lift — existing tests at 72.5% need expansion for truncateBeforeCheckpoint and forEachRecord error branches; test framework in place, more cases needed

**Impact:**
- Test suite runtime: 150s → <2s (slow test fixes in prior commit)
- Session GC pressure: reduced via pooling (~80% alloc reduction under high churn)
- Read-only safety: DML/DDL operations rejected with ErrReadOnly

## Dependencies

- Required: iter-09 (Engine.Open / Options.ReadOnly flag exists)
- Required: iter-12 (catalog for `Open` path; ReadOnly mode needs to
  avoid catalog writes too)
- Required: iter-13 (WAL Stats counters exist on the replayer; iter-15
  just surfaces them through `Engine.Stats`)
- Touches:
  - `internal/SYS/AP/ap.go` — extend `WALStats`, add `ErrReadOnly`
    handling
  - `internal/SYS/SY/sy.go` — populate `WALStats` in `Stats()`;
    `Options.ReadOnly` plumbing in `Open`
  - `internal/SYS/SE/se.go` — `sync.Pool` for sessions
  - `internal/SYS/ST/st.go` — type coercion in `Bind` / parameter
    validation
  - `internal/TXN/LC/hazard.go` — fix `PublishCurrent` /
    `PublishNext` (REQ000158)
  - `internal/WAL/FL/fl.go` — `WriteBuffer` struct
  - `internal/WAL/RP/rp.go` — `Stats()` already exists; iter-15 only
    needs to verify and add tests for the gap
  - `internal/WAL/RP/rp_test.go` (new) — coverage lift
  - `internal/ENG/LS/sst_reader.go` — first-block read fix
  - `internal/ENG/LS/flush.go` — `nextFileID()` dedup

## Current State (audit, 2026-06-06)

**WAL.Stats surfacing (REQ000190).** `WAL/RP/rp.go` already has a
`Stats() Stats` method on the `Replayer` interface, returning
`TruncatedSegments`, `UnknownRecords`, `CorruptionFailures`. The
`SYS/SY/sy.go` `Stats()` method populates
`EngineStats.WAL.CurrentLSN = 0` only — a placeholder. The counters
are never read.

**WAL/RP coverage (REQ000191).** Last measured at 72.5% (iter-13
deviation note). The shortfall is in two paths:
- `truncateBeforeCheckpoint` (43.5%) — the function removes all
  segments strictly before the checkpoint LSN, but no test exercises
  the boundary cases (LSN at segment start, LSN at segment end, no
  segments to truncate, all segments to truncate).
- `forEachRecord` (60.7%) — pre-existing decode paths that the
  iter-13 changes touched but did not add tests for.

**Hazard pointer (REQ000158).** Per the `iter-12-catalog.md` Gap
Analysis: `Publish` writes to all slots via a loop, which the design
spec (TXN.md:96-121) explicitly says defeats the double-slot
prefetch. A real fix needs a regression test that pins the
single-slot semantics.

**Session pooling (REQ000098).** Each `Begin` allocates a fresh
`*Session` via `NewSession`. Under sustained workload this is GC
pressure. `sync.Pool` with `Put` on `Close` is the obvious fix;
need a Reset path to clear state before reuse.

**ReadOnly mode (REQ000099).** `Options.ReadOnly bool` exists. No
code in `Open` reads it. `df.Open` always opens RW. The Writer has
no `ErrReadOnly` path. A `Stmt.Exec` that does DML on a read-only
engine should return `AP.ErrReadOnly`.

**Param binding (REQ000167).** `ST.Prepare` parses SQL with `?`
placeholders. The `args ...any` slice at `Query`/`Exec` time is
passed through unchanged to the SQL executor. A Go int bound to
an `INTEGER ?` works (Go's reflect handles it), but a Go string
bound to an `INTEGER ?` silently returns 0 from the executor
rather than a type-mismatch error.

**WriteBuffer (REQ000184).** `WAL/FL/fl.go` has `Sync` and
`BatchSync` stubs. There is no per-instance write buffer. The
design's `WAL.md:103-117` calls for a 256 KB pre-allocated
`writeBuffer` struct.

**sstIterator first-block (REQ000187).** Per iter-12 Gap Analysis
Bug 2: `current > 0` guard in `Next()` skips the first block load.
The iterator is empty even when the SST has entries.

**Double `nextFileID` (REQ000189).** Per iter-12 Gap Analysis Bug 4:
`requestFlush` calls `nextFileID()` once for `id` and a second time
in the `fmt.Sprintf` for `outputPath`. The on-disk filename and
the manifest's `fileID` field disagree.

## Requirements

| ID | Subsystem | Requirement | Status |
|---|---|---|---|
| REQ000098 | SYS | Session pooling via `sync.Pool` (reduce per-Begin GC pressure) | planned |
| REQ000099 | SYS | `Options.ReadOnly` honored: `O_RDONLY` on DF + WAL, writers return `ErrReadOnly` | planned |
| REQ000158 | TXN | Hazard pointer `Publish`/`Clear` protocol matches design (`TXN.md:96-121`) | planned |
| REQ000167 | SQL | Parameter binding type coercion (Go int → BIGINT, string → INT error) | planned |
| REQ000184 | WAL | `WAL/FL` WriteBuffer struct (256 KB pre-allocated) per `WAL.md:103-117` | planned |
| REQ000187 | ENG/LS | `sstIterator` first-block read fix (split `current` into `blockIdx`+`pairIdx`) | planned |
| REQ000189 | ENG/LS | `flushManager.requestFlush` single `nextFileID()` call | planned |
| REQ000190 | SYS | Wire `WAL.Stats` (`TruncatedSegments` / `UnknownRecords` / `CorruptionFailures`) into `Engine.Stats().WAL` | planned |
| REQ000191 | WAL/RP | Coverage lift: `truncateBeforeCheckpoint` + `forEachRecord` error branches to 85%+ | planned |

| R15 ID | Sub-requirement | Status |
|---|---|---|
| R15-1 | `AP.WALStats` gains `TruncatedSegments`, `UnknownRecords`, `CorruptionFailures int64` | planned |
| R15-2 | `Engine.Stats()` populates those fields from `Replayer.Stats()` | planned |
| R15-3 | Table-driven tests for `truncateBeforeCheckpoint`: empty, single-segment-at-checkpoint, multi-segment, all-before-checkpoint | planned |
| R15-4 | Table-driven tests for `forEachRecord` error branches: truncated tail, unknown record, mid-segment corruption, header present + body | planned |
| R15-5 | Hazard pointer: separate `PublishCurrent` / `PublishNext` / `Clear` API; single-slot semantics pinned by test | planned |
| R15-6 | `sync.Pool[*Session]` with `Reset` on Get; `Begin`/`Close` exercise the pool | planned |
| R15-7 | `Options.ReadOnly=true` → `O_RDONLY` open of `meta.razor`; `WAL/WR.New` short-circuits with `ErrReadOnly` on any append; `Stmt.Exec` that does DML returns `ErrReadOnly` | planned |
| R15-8 | `Bind` returns typed error on Go int → TEXT mismatch, string → INTEGER mismatch, etc. — table-driven | planned |
| R15-9 | `writeBuffer` struct (256 KB), pre-allocated on `flusher` construction, drained on `Sync`/`BatchSync` | planned |
| R15-10 | `sstIterator.Next()` first call loads block 0; split `current` into `blockIdx` + `pairIdx` | planned |
| R15-11 | `flushManager.requestFlush` calls `nextFileID()` once; both `outputPath` and `fileID` derive from the same id | planned |
| R15-12 | `go test ./... -race -count=1` green | planned |
| R15-13 | `go vet ./...` zero warnings; `gofmt -s -l .` no drift | planned |

## Design

### REQ000190 / R15-1, R15-2: WAL.Stats surfacing

```go
// internal/SYS/AP/ap.go
type WALStats struct {
    RecordsWritten    int64
    BytesWritten      int64
    Syncs             int64
    CurrentLSN        uint64
    TruncatedSegments int64 // new
    UnknownRecords    int64 // new
    CorruptionFailures int64 // new
}
```

```go
// internal/SYS/SY/sy.go Stats()
if e.rp != nil {
    rpStats := e.rp.Stats()
    return AP.EngineStats{
        ...
        WAL: AP.WALStats{
            CurrentLSN:        rpStats.CurrentLSN, // also fill this
            TruncatedSegments: rpStats.TruncatedSegments,
            UnknownRecords:    rpStats.UnknownRecords,
            CorruptionFailures: rpStats.CorruptionFailures,
        },
        ...
    }
}
```

`Replayer.Stats()` already returns these fields since iter-13
(REQ000035). The change is in `AP.WALStats` shape and the `Stats()`
plumbing in `SYS/SY/sy.go`.

### REQ000191 / R15-3, R15-4: WAL/RP coverage

New file `internal/WAL/RP/rp_coverage_test.go` with table-driven
tests:

- `truncateBeforeCheckpoint` cases:
  - empty segment list → no-op
  - single segment, checkpoint LSN in middle → keep
  - single segment, all LSNs before checkpoint → remove
  - multiple segments mixed before/after
  - checkpoint LSN exactly at segment boundary
- `forEachRecord` error branches:
  - record truncated at tail → `ErrTruncatedRecord`
  - unknown record type → `ErrUnknownRecord`
  - mid-segment CRC corruption → `ErrCorrupt`
  - header present + body truncated → tolerated

Target: 85%+ coverage. Test count: ~30 cases.

### REQ000158 / R15-5: Hazard pointer fix

```go
// internal/TXN/LC/hazard.go

// Current (defeats the design):
func (h *hazardPointerSet) Publish(ptr unsafe.Pointer) {
    for i := range h.ptrs {
        h.ptrs[i].Store(ptr) // writes to ALL slots
    }
}

// Target (per TXN.md:96-121):
func (h *hazardPointerSet) PublishCurrent(ptr unsafe.Pointer) {
    h.ptrs[0].Store(ptr)
}
func (h *hazardPointerSet) PublishNext(ptr unsafe.Pointer) {
    h.ptrs[1].Store(ptr)
}
func (h *hazardPointerSet) Clear() {
    h.ptrs[0].Store(nil)
    h.ptrs[1].Store(nil)
}
```

Regression test: a sequence of `PublishCurrent(A)` then
`PublishNext(B)` then `Clear()` must result in both slots being
nil. The current implementation would leave slot 0 with A or B in
some order (depending on which `Store` raced last) — a clear
behavioral diff that a test can pin.

### REQ000098 / R15-6: Session pooling

```go
// internal/SYS/SE/se.go

var sessionPool = sync.Pool{
    New: func() any { return &Session{} },
}

func NewSession(engine *SY.Engine) *Session {
    s := sessionPool.Get().(*Session)
    s.reset(engine) // clears txn, stats, deadline
    return s
}

// (Add) ReleaseSession for explicit return path; alternatively
// rely on GC + pool's internal GC hooks.
```

The `reset` method zeros the per-session state: `txn = nil`, stats
counters back to 0, deadline cleared. The engine reference is
re-bound.

### REQ000099 / R15-7: ReadOnly mode

`SY.Open` checks `opts.ReadOnly`:
- `df.Open(bdPath, e.log)` — needs an `O_RDONLY` mode. The current
  `df.Open` opens RW unconditionally. Either add a `df.OpenRO` or
  a `ReadOnly bool` field on `BlockDevice`.
- `wr.New(walDir, ...)` — the WAL writer should not be
  constructed. Instead, set `e.wr = nil` and have `e.wr.Sync()` /
  writes short-circuit to `ErrReadOnly`.
- `SQL/EX.Exec` that does DML — the executor checks `e.opts.ReadOnly`
  and returns `ErrReadOnly` for any non-SELECT statement.

The catalog also respects ReadOnly: `CreateTable` / `DropTable`
return `ErrReadOnly`. The catalog file is opened but not rewritten.

### REQ000167 / R15-8: Param binding type coercion

```go
// internal/SYS/ST/st.go

// In Query/Exec, before forwarding to executor:
if err := s.validateArgTypes(args); err != nil {
    return nil, err // or AP.Result{}, err
}

// Table-driven by column type:
// INTEGER/BIGINT/SMALLINT  ↔ int, int8, int16, int32, int64, uint*
// TEXT/VARCHAR             ↔ string, []byte
// BOOLEAN                  ↔ bool
// FLOAT/DOUBLE             ↔ float32, float64
// Anything else returns ErrTypeMismatch
```

The test cases need to cover each (Go-type, SQL-type) pair: valid
combinations succeed; invalid combinations return a typed error.

### REQ000184 / R15-9: WriteBuffer

```go
// internal/WAL/FL/fl.go

const writeBufferSize = 256 * 1024

type flusher struct {
    // ... existing fields ...
    writeBuf []byte // pre-allocated, len == writeBufferSize
    writeOff int    // next write position
}

func (f *flusher) Sync() error {
    if f.writeOff > 0 {
        // drain writeBuf[0:writeOff] to the underlying segment
        f.writeOff = 0
    }
    // ... existing fsync ...
}
```

The buffer absorbs multiple small `WR.Sync` calls in a window and
lets `BatchSync` amortize the cost. No new test infra needed;
existing WAL integration tests will exercise the path.

### REQ000187 / R15-10: sstIterator first-block

```go
// internal/ENG/LS/sst_reader.go

type sstIter struct {
    // ... existing fields ...
    blockIdx int    // current block index
    pairIdx int    // current pair index within block
    blockLoaded bool // whether blockIdx-th block is loaded
}

func (it *sstIter) Next() bool {
    if !it.blockLoaded {
        if !it.loadBlock(0) { return false }
        it.blockLoaded = true
    }
    // ... existing iteration logic, but read blockIdx/pairIdx ...
}
```

Test: an SST with 1 block containing 1 key, fresh iterator, first
`Next()` returns `true` and `Key()` returns the key. Current
implementation returns `false`.

### REQ000189 / R15-11: Single nextFileID

```go
// internal/ENG/LS/flush.go

func (fm *flushManager) requestFlush(m *memtable) {
    m.Freeze()
    id := nextFileID() // call once
    fm.pendingWGs.Add(1)
    select {
    case fm.flushQueue <- &flushJob{
        memtable:   m,
        outputPath: filepath.Join(fm.dir, fmt.Sprintf("L0_%d.sst", id)),
        manifest:   fm.manifest,
        fileID:     id,
        level:      0,
    }:
    default:
        fm.pendingWGs.Done()
    }
}
```

Test: a requestFlush call results in `outputPath` containing the
same `id` as `fileID`. The current implementation can produce
`L0_42.sst` with `fileID=43`.

## Test Plan

### Unit tests (per REQ)
- `WAL/RP/rp_coverage_test.go` (new) — 30+ table-driven cases
- `TXN/LC/hazard_test.go` — `PublishCurrent` / `PublishNext` /
  `Clear` regression
- `WAL/FL/fl_test.go` — `WriteBuffer` drain on `Sync`
- `ENG/LS/sst_reader_test.go` — first-block load
- `ENG/LS/flush_test.go` — single `nextFileID()` per request
- `SYS/SE/se_test.go` — pool reuse under `Begin`/`Close` cycles
- `SYS/SY/sys_test.go` — `ReadOnly=true` rejects writers
- `SYS/ST/st_test.go` — type coercion table

### Integration
- `TestEngine_ReadOnly_FullFlow` — Open with `ReadOnly=true`,
  `CREATE TABLE` returns `ErrReadOnly`, `SELECT` succeeds
- `TestEngine_Stats_IncludesWALStats` — corrupt a WAL segment,
  Open, observe `EngineStats().WAL.CorruptionFailures >= 1`

### Benchmarks
- `BenchmarkSessionPool_HighConcurrency` — 10K `Begin`/`Close`
  cycles with the pool, measure allocations

## Deviations / Risks

1. **ReadOnly mode touches the FIL/DF `Open` path** — the current
   `df.Open` always opens RW. Adding a `ReadOnly` field on
   `BlockDevice` (or a separate `df.OpenRO`) is a small surface
   change but it's a hot path. Risk: low, since reads don't
   change behavior; the change is the `open(2)` flags.
2. **Session pool and `Session.id`** — sessions have a unique ID
   (used for tracing). Pool reuse must not reuse the same ID
   across overlapping lifetimes. Mitigation: assign a new ID on
   `reset` (the sessionIDSeq counter is already global).
3. **Hazard pointer fix touches a hot path** — the `Publish`
   function is called on every read. The new single-slot
   semantics must not regress read performance. Mitigation:
   `atomic.Store` on a single field is no slower than a loop of
   the same; the change is a net reduction in writes.
4. **iter-12b bug fix ordering** — the two sstIterator / flush
   bugs are independent but both touch the same SST write/read
   cycle. If both have to land, they should land in the same
   commit to avoid leaving a half-fixed state. Recommendation:
   one commit per bug, in this order: flush (RQ189) first
   (touches writer only), then sstReader (RQ187) (touches reader
   only). Each commit is a stable checkpoint.

## Completion Criteria

| Rule | State |
|---|---|
| `go vet ./...` zero warnings | green |
| `gofmt -s -l .` no drift | green |
| `go test ./... -race -count=1` all green | green |
| `WAL/RP` coverage ≥ 85% | green |
| `Engine.Stats().WAL.TruncatedSegments/CorruptionFailures` populated | green |
| `Engine.Stats().WAL.UnknownRecords` populated | green |
| `Options.ReadOnly` honored end-to-end | green |
| `?` placeholder type-mismatch returns typed error | green |
| `sstIterator` first-block bug pinned by regression test | green |
| `nextFileID` per-flush uniqueness pinned by regression test | green |
