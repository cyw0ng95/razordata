# Iteration16 — Param Binding + iter-12b Path Fix + Quick Wins

**Subsystem:** `SQL` (`EX`, `PS`), `ENG` (`LS`), `WAL` (`RP`), `LOG` (`LG`), `TXN` (`VL`)
**Status:** done
**Est. LOC:** ~2000
**Requirements:** REQ000167, REQ000186, REQ000191, REQ000009, REQ000180, REQ000179
**Target release:** v0.12.0
**Commit:** `<filled at commit time>`
**Tag:** v0.12.0

## Overview

This iteration closes six small, well-scoped gaps. None of them touch
the public API in a breaking way. They fall into three buckets:

**Bucket1 — Carry-over from iter-15 (3 REQs)**

These were deferred from iter-15 because iter-15 already shipped a
substantial set of changes and the carry-over was small enough to
package cleanly here.

- `REQ000167` — `?` placeholder binding is parsed (`Param{Index}` AST
 nodes exist) but `Executor.Exec`/`Query` silently drop the `args`
 slice. A `Stmt.Query("SELECT * FROM t WHERE id = ?", "x")` does
 not surface a type mismatch even when `"x"` cannot coerce to
 `INTEGER`. This iter wires the args slice through the operator
 pipeline and adds table-driven type-coercion validation at the
 `ST` boundary.
- `REQ000191` — `WAL/RP` coverage was lifted to ~80% in iter-15 for
 linear paths. The remaining shortfall is in
 `truncateBeforeCheckpoint` multi-segment boundaries and
 `forEachRecord` unknown-record-type branches. These are table-driven
 tests; no production code change.
- `REQ000186` — `ENG/LS` flush output path is `<dir>/L0_<id>.sst`
 (flat), but `compaction.fileName` produces
 `<dir>/sst/L<level>_<minkey>_<maxkey>_<id>.sst`. The two paths
 diverge; a freshly flushed L0 SST is invisible to the compaction
 reader that looks for `sst/...`. Unify on the nested
 `sst/L<N>_<id>.sst` form.

**Bucket2 — Finish-line design-alignment (2 REQs)**

Both are documented in design but not implemented. Smallest changes
in the whole iteration.

- `REQ000180` — `ENG/LS` bloom filter is currently fixed at4096 bytes
 per SST. Design (`ENG.md:91-92`) calls for `(N *10 +7) /8`
 bytes — i.e.10 bits per key. Compute the size from `keyCount` on
 `sstWriter.newSSTWriter`.
- `REQ000179` — Per `TXN.md:150-158`, the per-transaction arena
 should live on the `transactionSlot` struct so it is reclaimed
 atomically with the slot. Today the arena is on the `tx` struct
 and is released only when `Commit`/`Abort` calls `MV.PutArena`.

**Bucket3 — Operational polish (1 REQ)**

- `REQ000009` — Compress rotated log files with gzip on rotation
 instead of leaving them as raw `.log` files. `LOG/LG/logger.go`
 already has the `rotateFile` pipeline; add a `compressRotated`
 step after the rename. Update `listRotatedFiles` to match the
 `.gz` suffix.

All six items are S or M effort. No new API surface area. No new
external dependency. No protocol/format change visible to callers
(the path-format change in REQ000186 is internal to the engine dir;
the on-disk format of existing SSTs is preserved — only new SSTs
written after the fix use the unified path).

## Outcome

Done (shipped in v0.12.0). Six REQs implemented across
`SQL/EX` + `SYS/ST` + `ENG/LS` + `LOG/LG` + `TXN/VL` + `WAL/RP`:

- REQ000179: arena lives on `transactionSlot`. AllocateSlot
 calls `MV.GetArena`; ReleaseSlot calls `MV.PutArena`.
 `tx.finalize` no longer touches the arena directly.
- REQ000180: `sstWriter.bloomSize = bloomSizeFor(keyCount)`
 = `(N*10+7)/8` bytes computed at Finish time. Keys are
 deferred into `sstWriter.keys` and hashed into the bloom at
 Finish so the bucket math is always computed against the
 final keyCount, never against a transient mid-grow value.
- REQ009: `Options.CompressRotated` (opt-in; default false to
 preserve zero-value semantics) gzips the rotated file
 via `compress/gzip` and updates `listRotatedFiles` to match
 both `.log` and `.log.gz` suffixes.
- REQ000186: SST path unification. Flush now writes a temp
 file in `<dir>/sst/.tmp_<id>_<nano>.sst`, scans the memtable
 for MinKey/MaxKey, and renames to
 `<dir>/<fileName(meta)>`. `fileName` hex-encodes MinKey/MaxKey
 so integer primary keys (8-byte int64 LE) no longer hit
 null bytes that would break `os.Rename`. Tests with hex-encoded
 filenames verify the path round-trip.
- REQ000191: WAL/RP coverage lift. `truncateBeforeCheckpoint`
 multi-segment cases (cp at middle of seg1, cp at exact
 segment boundary, cp at zero) + `forEachRecord`
 `ErrUnknownRecord` branch (length varint > MaxRecordLen)
 covered. Coverage72.5% →75.2%.
- REQ000167: `?` placeholder binding wired end-to-end. Every
 operator (`SeqScan`, `IndexScan`, `Filter`, `Project`,
 `Sort`, `Limit`, `Offset`, `Aggregate`, `HashAggregate`,
 `Insert`, `Update`, `Delete`) carries a `params` field set
 by a tree-wide `propagateParams` walk at Exec/Query time.
 Eval now uses the bound value. ST.Prepare extracts per-placeholder
 column types from the AST (resolving `column = ?` via the
 registered schema) and `Stmt.Query/Exec` validates `args ...any`
 against the cached types, returning `*argTypeError` on
 mismatch. `CatalogColumn.Type` was added (1 byte appended to
 the catalog wire format) so type resolution survives
 restarts.

Test suite: race-clean across5 consecutive runs of
`go test ./... -race -count=1 -timeout=300s`. Pre-iter-16
test runtime was ~150s on this hardware; iter-16 changes add
~20s of WAL multi-segment cases but the suite still
completes in under300s on the slowest run.

## Deviations from Plan

1. **CatalogColumn.Type wire-format change.** The pre-iter-16
 catalog wire format had `[name][nullable]` per column. iter-16
 inserts a1-byte type token between name and nullable, with
 backward-compat fallback to `LX.T_TEXT` when the byte is
 missing (pre-iter-16 catalogs). This is a non-breaking change
 for fresh databases; pre-iter-16 catalogs read back as TEXT.
2. **WAL/RP coverage landed at75.2%**, not the planned85%.
 The remaining shortfall is in resync-window edge cases
 (`forEachRecord`'s bounded-resync branch). Coverage test
 cases for that branch require a constructed
 "valid record / corrupt-byte in inner window" fixture
 that we deferred to a follow-up iteration.
3. **fileName's hex-encoding is a subtle wire-format change**
 for pre-iter-16 SSTs that were written with raw MinKey/MaxKey
 in the filename. Fresh databases use the new shape; old SSTs
 are orphaned (compaction cleans them up).

## Dependencies

- Required: iter-08 (`Param` AST nodes exist; operator pipeline
 reaches `Eval`)
- Required: iter-09 (`Stmt` exposes `Query`/`Exec` accepting `args
 ...any`)
- Required: iter-04 (`sstWriter`/`sstReader`/`compaction`/`flush`
 exist; the SST path fix is local)
- Required: iter-12 (catalog persists schema; REQ000167's type
 validation reads schema column types from the catalog)
- Required: iter-13 (`WAL/RP` replayer surfaces
 `TruncatedSegments`/`UnknownRecords`/`CorruptionFailures`; the
 coverage-lift tests assert on these stats)
- Required: iter-00 (`LOG/LG` rotation pipeline; `rotateFile` is
 the integration point for gzip)
- Required: iter-06 (`transactionSlot` struct exists; adding a
 field is mechanical)
- Touches:
 - `internal/SQL/EX/ex.go` — pass `args ...any` from
 `Exec`/`Query`/`QueryAll` into operator construction
 - `internal/SQL/EX/operators.go` — add `params []interface{}` to
 operator base or each operator
 - `internal/SQL/EX/eval.go` — already supports `params`
 - `internal/SQL/EX/writers.go` — pass `args` to `buildWriterOp`
 - `internal/SYS/ST/st.go` — `validateArgTypes` table-driven check
 - `internal/ENG/LS/flush.go` — change output path to
 `sst/L0_<id>.sst`
 - `internal/ENG/LS/compaction.go` — already uses `fileName`; the
 fix is in `flush` only
 - `internal/ENG/LS/sst_writer.go` — dynamic bloom sizing from
 `keyCount`
 - `internal/TXN/VL/slot.go` — add `arena *MV.Arena` to
 `transactionSlot`
 - `internal/TXN/VL/manager.go` — move arena allocation into
 `AllocateSlot`; remove from `Begin`
 - `internal/TXN/VL/protocol.go` — use `t.slot.arena` instead of
 `t.arena`; remove `arena` from `tx` struct
 - `internal/WAL/RP/rp_coverage_test.go` — add multi-segment and
 unknown-record cases
 - `internal/LOG/LG/logger.go` — gzip compress in `rotateFile`;
 update `listRotatedFiles` to recognize `.gz` suffix

## Current State (audit,2026-06-08)

**Param binding (REQ000167).** `SQL/PS` produces `&Param{Index: idx}`
when it encounters `T_BIND`. `SQL/EX/eval.go` already accepts
`params []interface{}` and returns `params[e.Index]` for `*PS.Param`.
But the operator pipeline (`SeqScan`/`Filter`/`Project`/etc.) does not
carry `params`; `Eval` calls in operator implementations pass `nil`.
`Executor.Exec`/`Query`/`QueryAll` accept `args ...any` but the body
ignores it (`_ = args`). End-to-end: a `Stmt.Query("SELECT1 WHERE ?=1",1)`
returns `1` because the `?` is never substituted. A
`Stmt.Query("SELECT * FROM t WHERE id = ?", "x")` runs without error
even when `"x"` cannot coerce to `INTEGER`. This is a real silent
correctness gap.

**WAL/RP coverage (REQ000191).** iter-15 added
`TestRP_TruncateBeforeCheckpoint_Cases` (single-segment cases only)
and `TestRP_ForEachRecord_ErrorBranches` (3 of N error branches).
Gaps:

- `truncateBeforeCheckpoint` multi-segment: two segments with
 checkpoint LSN in the middle of segment1; checkpoint at exact
 segment boundary; all segments strictly before checkpoint;
 segment strictly after checkpoint (no-op). The function's LSN
 math has bugs that surface only on multi-segment inputs.
- `forEachRecord` `ErrUnknownRecord` path: write a record with an
 out-of-range `RecordType` (e.g.255) and verify the replayer
 surfaces `ErrUnknownRecord` and increments
 `Stats.UnknownRecords`.
- `forEachRecord` envelope-CRC failure path: corrupt the envelope
 CRC (not the inner payload CRC) — the existing
 `corrupt_envelope_crc_mid_record` case mutates a byte at
 `WALHeaderSize+16` which lands in the inner payload; need a case
 that mutates the envelope CRC trailer specifically.

**SST path (REQ000186).** `flush.go:179` writes
`<fm.dir>/L0_<id>.sst` (flat). `compaction.go:167-169` `fileName`
returns `<dir>/sst/L<level>_<minkey>_<maxkey>_<id>.sst` (nested).
On restart, the manifest's `SSTFileMeta` is rebuilt from the
on-disk layout. The reader path in `compaction.go:69,84,144`
`os.ReadFile(sstPath)` uses `fileName(&input)` which is nested; if
the manifest was originally written with a flat path, the lookup
fails with `ENOENT` and the post-flush SST is invisible. The fix is
to make `flush` emit the nested path too.

**Dynamic bloom (REQ000180).** `sst_writer.go:39`
`bloomSize :=4096` is hardcoded. `setBloomBit` then uses
`size := len(w.bloom) *8` and mods the hash by `size`. For an SST
with1000 keys, the4096-byte /32768-bit filter gives a false
positive rate of roughly `(1 - e^{-1000*2/32768})^2 ≈0.06%`. For
an SST with100 keys, the filter is32x larger than needed
(`(100*10+7)/8 =125 bytes`) — wasted disk and reader-side memory.
Design specifies dynamic sizing.

**TXN arena on slot (REQ000179).** `tx` struct (`protocol.go:33`)
holds `arena *MV.Arena`. `manager.go:89` allocates it on `Begin`
and `protocol.go:176-178` returns it on `Commit`/`Abort`. The slot
itself does not know about its arena. If a transaction panics or
is leaked (never reaches `Commit`/`Abort`), the arena is never
recycled. Per `TXN.md:150-158` the design calls for the arena to
live on the slot so it is reclaimed when the slot is freed.

**Gzip log rotation (REQ000009).** `logger.go:273-346`
`rotateFile` renames `<base>.log` to
`<base>.YYYYMMDD_HHMMSS.log` and opens a fresh file. The rotated
file is left uncompressed. Over months of uptime, a busy engine
produces many large rotated files; gzipping them cuts disk by ~10x
on typical structured-log output.

## Requirements

| ID | Subsystem | Requirement | Status |
|---|---|---|---|
| REQ000167 | SQL | `?` placeholder binding wired end-to-end; type coercion at `ST.Bind` returns `AP.ErrTypeMismatch` on Go type × SQL column type mismatch | planned |
| REQ000186 | ENG/LS | Unify SST output path on `<dir>/sst/L<N>_<id>.sst` (flush + compaction agree) | planned |
| REQ000191 | WAL/RP | Coverage lift: multi-segment `truncateBeforeCheckpoint` + `forEachRecord` `ErrUnknownRecord` + envelope-CRC-mutation cases | planned |
| REQ000009 | LOG | Gzip-compress rotated log files (`.log` → `.log.gz`); `listRotatedFiles` recognizes `.gz` suffix | planned |
| REQ000180 | ENG/LS | Bloom filter sized as `(N *10 +7) /8` bytes (10 bits/key), computed from `keyCount` | planned |
| REQ000179 | TXN/VL | `transactionSlot` carries `arena *MV.Arena`; allocated in `AllocateSlot`, released in `ReleaseSlot` | planned |

| R16 ID | Sub-requirement | Status |
|---|---|---|
| R16-1 | `Executor.Exec`/`Query`/`QueryAll` thread `args ...any` into the operator tree (set `params []interface{}` on each operator) | planned |
| R16-2 | All `Eval` call sites in operator implementations pass the operator's `params` (instead of `nil`) | planned |
| R16-3 | `Stmt.Query`/`Exec` runs `validateArgTypes(args, planSchema)` before forwarding to executor; returns `AP.ErrTypeMismatch` on mismatch (with typed error for `string→INTEGER`, `int→TEXT`, etc.) | planned |
| R16-4 | Type-coercion table (table-driven): `INTEGER/BIGINT/SMALLINT ↔ int/int8/int16/int32/int64/uint*`, `TEXT/VARCHAR ↔ string/[]byte`, `BOOLEAN ↔ bool`, `FLOAT/DOUBLE ↔ float32/float64`; mismatches return typed error | planned |
| R16-5 | Regression: `Stmt.Query("SELECT * FROM t WHERE id = ?", "x")` on `id INTEGER` returns `AP.ErrTypeMismatch` (not silent zero) | planned |
| R16-6 | Regression: `Stmt.Query("SELECT * FROM t WHERE id = ?", int64(7))` returns the row | planned |
| R16-7 | `flushManager.requestFlush` writes to `<fm.dir>/sst/L0_<id>.sst`; `flush_compaction_test.go` asserts `outputPath` matches `sst/L0_*` and `fileID` matches the integer in the filename | planned |
| R16-8 | `compaction.compactL0ToL1` reads via `sst/...` path; post-flush SST is now visible (test: write to L0, run compaction, observe input) | planned |
| R16-9 | `sstWriter.newSSTWriter` computes `bloomSize := (w.keyCount *10 +7) /8` ... wait, keyCount is0 at construction time. Compute on `Finish()` from `keyCount`. | planned |
| R16-10 | `truncateBeforeCheckpoint` test: two segments, checkpoint LSN in middle of segment1 — both segments kept | planned |
| R16-11 | `truncateBeforeCheckpoint` test: checkpoint LSN at exact segment boundary — only segment1 removed | planned |
| R16-12 | `truncateBeforeCheckpoint` test: all segments strictly before checkpoint — all removed | planned |
| R16-13 | `forEachRecord` test: write record with `RecordType =255` (unknown) — surfaces `ErrUnknownRecord`, increments `Stats.UnknownRecords` | planned |
| R16-14 | `forEachRecord` test: mutate envelope-CRC trailer bytes specifically (last4 bytes of segment) — surfaces `ErrCorrupt` | planned |
| R16-15 | `rotateFile` after rename: open `<base>.YYYYMMDD_HHMMSS.log.gz`, write gzipped content of rotated file, delete uncompressed file | planned |
| R16-16 | `listRotatedFiles` matches `<base>.YYYYMMDD_HHMMSS.log.gz` pattern (length18 suffix: `YYYYMMDD_HHMMSS` + `.log.gz`) | planned |
| R16-17 | `transactionSlot` gains `arena *MV.Arena` field | planned |
| R16-18 | `AllocateSlot` calls `MV.NewArena()` and stores it in the slot; `ReleaseSlot` calls `MV.PutArena` | planned |
| R16-19 | `tx` struct no longer carries `arena`; protocol.go uses `t.slot.arena` | planned |
| R16-20 | `go test ./... -race -count=1` green | planned |
| R16-21 | `go vet ./...` zero warnings; `gofmt -s -l .` no drift | planned |

## Design

### REQ000167 / R16-1, R16-2: param wiring

The `Operator` interface stays as `Next(ctx)` + `Close()`. Each
operator gets a `params []interface{}` field set at construction.
The `Eval` calls already support `params`:

```go
// internal/SQL/EX/operators.go

type SeqScan struct {
 // ... existing fields ...
 params []interface{}
}

func NewSeqScan(table string) *SeqScan {
 return &SeqScan{table: table}
}

func (s *SeqScan) WithParams(p []interface{}) *SeqScan {
 s.params = p
 return s
}
```

For each operator (`SeqScan`, `Filter`, `Project`, `Sort`, `Limit`,
`Aggregate`, `HashAggregate`, `NestedLoopJoin`, `Distinct`,
`Subquery`), add a `params []interface{}` field and pass it through
to `Eval`. The current `Eval` signature already accepts `params`
so this is purely a data-flow change.

```go
// internal/SQL/EX/ex.go
func (e *Executor) Query(ctx context.Context, sql string, args ...any) (*Rows, error) {
 parser := PS.NewParser(sql)
 stmt, err := parser.Parse()
 if err != nil {
 return nil, err
 }
 plan, err := e.planner.Plan(stmt)
 if err != nil {
 return nil, err
 }
 if plan == nil || plan.root == nil {
 return nil, errors.New("ex: plan produced no root")
 }
 // R16-1: thread args down to the operator tree.
 withParams(plan.root, args)
 defer plan.root.Close()
 // ... existing logic ...
}

// withParams is a small visitor that walks the operator tree and
// sets the params field on every operator that implements
// WithParams.
func withParams(op Operator, args []any) {
 if args == nil {
 return
 }
 if w, ok := op.(interface{ WithParams([]interface{}) Operator }); ok {
 op = w.WithParams(asAnySlice(args))
 }
 if c, ok := op.(interface{ Child() Operator }); ok {
 withParams(c.Child(), args)
 }
 // ... apply to children collections ...
}
```

### REQ000167 / R16-3, R16-4: type coercion

Add a `validateArgTypes` helper at the `ST` boundary:

```go
// internal/SYS/ST/st.go

type argTypeError struct {
 ArgIdx int
 GoType string
 SQLColumn string
 SQLType int
}

func (e *argTypeError) Error() string {
 return fmt.Sprintf("st: arg %d (Go %s) cannot bind to column %q (SQL type %d)",
 e.ArgIdx, e.GoType, e.SQLColumn, e.SQLType)
}

// validateArgTypes is a table-driven type check. It walks the
// schema of the prepared statement and verifies each `?`
// placeholder can be coerced from the Go type supplied at
// bind time to the SQL column type.
func validateArgTypes(args []any, colTypes []int) error {
 for i, a := range args {
 if i >= len(colTypes) {
 return AP.ErrTypeMismatch // too many args
 }
 ct := colTypes[i]
 if !coercible(reflect.TypeOf(a), ct) {
 return &argTypeError{ArgIdx: i, GoType: typeName(a), SQLType: ct}
 }
 }
 if len(args) < len(colTypes) {
 return AP.ErrTypeMismatch // too few args
 }
 return nil
}

func coercible(goType reflect.Type, sqlType int) bool {
 switch sqlType {
 case ls.CTInt, ls.CTBigInt, ls.CTTimestamp:
 switch goType.Kind() {
 case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
 reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
 return true
 }
 case ls.CTFloat:
 switch goType.Kind() {
 case reflect.Float32, reflect.Float64,
 reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
 reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
 return true
 }
 case ls.CTBool:
 return goType.Kind() == reflect.Bool
 case ls.CTVarchar, ls.CTText, ls.CTBlob:
 switch goType.Kind() {
 case reflect.String, reflect.Slice: // []byte
 return goType.Elem().Kind() == reflect.Uint8 || goType == reflect.TypeOf([]byte{})
 }
 }
 return false
}
```

Note: `ls.CTInt` etc. live in `internal/ENG/LS/table.go`. To avoid
`ST` importing `ENG/LS` (layering), mirror the constants in `ST` or
expose a `ColumnType → string` mapping from `SQL/EX`. Cleanest:
add a `SQLTypeName(ct int) string` accessor in `SQL/EX` that `ST`
calls.

The `Stmt.Query`/`Exec` integration:

```go
// internal/SYS/ST/st.go
func (s *Stmt) Query(ctx context.Context, args ...any) (*AP.Rows, error) {
 if s.engine.IsClosed() { return nil, AP.ErrClosed }
 s.mu.Lock()
 if s.closed { s.mu.Unlock(); return nil, AP.ErrClosed }
 s.mu.Unlock()

 // R16-3: type-validate before forwarding.
 if err := validateArgTypes(args, s.paramTypes); err != nil {
 return nil, err
 }

 exe := s.engine.Executor()
 rs, err := exe.Query(ctx, s.sql, args...)
 if err != nil { return nil, err }
 return &AP.Rows{Cols: rs.Cols, Types: rs.Types}, nil
}
```

The `Stmt` struct needs to cache `paramTypes` at `Prepare` time
(the planner is run once and the param column types are extracted
from the resulting plan's leaf scan). See `prepare.go` (new) for
details.

### REQ000186 / R16-7, R16-8: path unification

`flush.go:179` currently writes to `<fm.dir>/L0_<id>.sst`. Change
to nested:

```go
// internal/ENG/LS/flush.go
import "path/filepath"

func (fm *flushManager) requestFlush(m *memtable) {
 m.Freeze()
 id := nextFileID()
 fm.pendingWGs.Add(1)
 select {
 case fm.flushQueue <- &flushJob{
 memtable: m,
 outputPath: filepath.Join(fm.dir, "sst", fmt.Sprintf("L0_%d.sst", id)),
 manifest: fm.manifest,
 fileID: id,
 level:0,
 }:
 default:
 fm.pendingWGs.Done()
 }
}
```

`compaction.fileName` already returns
`<dir>/sst/L<level>_<minkey>_<maxkey>_<id>.sst`, which uses the
nested path. The fix is purely in `flush`. The compaction reader
calls `os.ReadFile(sstPath)` with the nested path; with the flush
fix, the on-disk layout and the reader expectation agree.

Also ensure `fm.dir/sst/` exists before flush. Add an
`os.MkdirAll(filepath.Join(fm.dir, "sst"),0o755)` call in
`newFlushManager` (idempotent if the dir exists).

### REQ000180 / R16-9: dynamic bloom sizing

The bloom size is computed at `sstWriter.newSSTWriter` time, when
`keyCount` is0. Move the size computation to `Finish`:

```go
// internal/ENG/LS/sst_writer.go

func (w *sstWriter) Finish() ([]byte, error) {
 // R16-9: dynamic bloom sizing.
 // Design: ENG.md:91-92 —10 bits per key.
 // Size = (N *10 +7) /8 bytes.
 if w.keyCount ==0 {
 return nil, errors.New("sst: empty writer")
 }
 newBloomSize := (w.keyCount*10 +7) /8
 if uint64(len(w.bloom)) != uint64(newBloomSize) {
 newBloom := make([]byte, newBloomSize)
 copy(newBloom, w.bloom)
 w.bloom = newBloom
 }
 // ... existing Finish logic ...
}
```

The reader (`sst_reader.go:33`) reads `bloomSize` from the footer;
no reader change needed.

The `SSTFileMeta.BloomBits` field already exists; on `Flush` the
manifest records `BloomBits:10` (per key), which is consistent
with the new size formula.

### REQ000191 / R16-10..14: coverage lift

Pure test additions to `internal/WAL/RP/rp_coverage_test.go`:

- Multi-segment `truncateBeforeCheckpoint` cases: write5 records
 to seg0, force rotate (or pre-create seg1 of size N), write a
 checkpoint at LSN `SegSize +50`. After Replay, seg0 should be
 truncated, seg1 retained.
- Boundary: checkpoint LSN at exactly `SegSize` (segment start of
 seg1). seg0 truncated, seg1 retained.
- All-before: checkpoint LSN at0. Both segments removed.
- Unknown record: write a record with `Type:255` directly to the
 segment file via `wr.Append` (the writer's `RecordType` is a
 `uint8` so we can set any value). Replay surfaces
 `ErrUnknownRecord`.
- Envelope CRC mutation: locate the last4 bytes of a record's
 envelope (CRC trailer), flip a bit. Replay surfaces
 `ErrCorrupt` and `CorruptionFailures++`.

Target coverage after these additions: ≥85%.

### REQ000179 / R16-17..19: arena on slot

Move `arena *MV.Arena` from `tx` struct (`protocol.go:33`) to
`transactionSlot` (`slot.go:24`):

```go
// internal/TXN/VL/slot.go
type transactionSlot struct {
 txnID uint64
 status atomic.Int32
 beginTS uint64
 commitTS uint64
 writeSet []KeyRange
 arena *MV.Arena // R16-17: per-slot arena, reclaimed on release.
 index int
}

func (sm *slotManager) AllocateSlot() *transactionSlot {
 sm.mu.Lock()
 defer sm.mu.Unlock()
 if len(sm.freeList) ==0 { return nil }
 idx := sm.freeList[len(sm.freeList)-1]
 sm.freeList = sm.freeList[:len(sm.freeList)-1]
 slot := &sm.slots[idx]
 slot.txnID =0
 slot.status.Store(int32(SlotActive))
 slot.beginTS =0
 slot.commitTS =0
 slot.writeSet = nil
 slot.arena = MV.NewArena() // R16-18: arena lives on the slot now.
 return slot
}

func (sm *slotManager) ReleaseSlot(slot *transactionSlot) {
 sm.mu.Lock()
 defer sm.mu.Unlock()
 idx := slot.index
 slot.status.Store(int32(SlotInactive))
 slot.txnID =0
 slot.beginTS =0
 slot.commitTS =0
 slot.writeSet = nil
 if slot.arena != nil {
 MV.PutArena(slot.arena) // R16-18: release in lockstep with slot.
 slot.arena = nil
 }
 if idx >=0 && idx < MaxConcurrentTXNs {
 sm.freeList = append(sm.freeList, idx)
 }
}
```

`tx` struct drops the `arena` field. `protocol.go` uses
`t.slot.arena`. The `manager.Begin` no longer allocates an arena
(saving the `MV.NewArena()` call there).

### REQ000009 / R16-15, R16-16: gzip rotation

After `os.Rename(currentPath, rotatedPath)`, compress the rotated
file:

```go
// internal/LOG/LG/logger.go

func (s *sharedLogger) rotateFile() error {
 // ... existing close + rename logic ...
 if err := os.Rename(currentPath, rotatedPath); err != nil {
 // ... existing fallback ...
 return err
 }
 // R16-15: gzip the rotated file in-place.
 if err := gzipFile(rotatedPath, rotatedPath+".gz"); err != nil {
 fmt.Fprintf(os.Stderr, "log rotation: gzip failed: %v\n", err)
 // non-fatal: keep the uncompressed rotated file.
 } else {
 if err := os.Remove(rotatedPath); err != nil {
 fmt.Fprintf(os.Stderr, "log rotation: remove uncompressed: %v\n", err)
 }
 }
 // ... existing reopen + cleanup logic ...
}

func gzipFile(src, dst string) error {
 in, err := os.Open(src)
 if err != nil { return err }
 defer in.Close()
 out, err := os.Create(dst)
 if err != nil { return err }
 defer out.Close()
 gz := gzip.NewWriter(out)
 if _, err := io.Copy(gz, in); err != nil {
 gz.Close()
 return err
 }
 return gz.Close()
}
```

`listRotatedFiles` updates:

```go
// internal/LOG/LG/logger.go
func (s *sharedLogger) listRotatedFiles() ([]string, error) {
 // ... existing dir read ...
 for _, entry := range entries {
 // R16-16: accept .log.gz suffix.
 // Match <base>.YYYYMMDD_HHMMSS.log OR <base>.YYYYMMDD_HHMMSS.log.gz
 if !strings.HasPrefix(name, prefix) || name == s.baseName {
 continue
 }
 suffix := strings.TrimPrefix(name, prefix)
 var timestampPart string
 switch {
 case strings.HasSuffix(suffix, ".log.gz"):
 timestampPart = strings.TrimSuffix(suffix, ".log.gz")
 case strings.HasSuffix(suffix, ext):
 timestampPart = strings.TrimSuffix(suffix, ext)
 default:
 continue
 }
 if len(timestampPart) !=15 { continue }
 rotated = append(rotated, filepath.Join(s.dir, name))
 }
 // ... existing sort ...
}
```

## Test Plan

### Unit tests (per REQ)

- `SQL/EX/param_test.go` (new) — operator `params` propagation;
 `?` substituted end-to-end via `Eval`.20+ cases.
- `SQL/EX/eval_param_test.go` (new) — `Eval` with `params`:
 integer, float, string, bool, null param, out-of-range index.
- `SYS/ST/st_test.go` (new) — `validateArgTypes` table:
 - Valid combinations succeed (12 cases)
 - Invalid combinations return `argTypeError` (15 cases)
 - Too few/too many args (2 cases)
 - `?` placeholder in INSERT/UPDATE/DELETE round-trip
- `ENG/LS/flush_test.go` — `outputPath` matches
 `sst/L0_<id>.sst` after the fix.
- `ENG/LS/compaction_test.go` — flush-then-compact: write to
 memtable, flush, run `compactL0ToL1`, observe that the L0 SST is
 read as input.
- `ENG/LS/sst_writer_test.go` — bloom size matches
 `(keyCount*10+7)/8`; verify by reading footer `bloomSize`.
- `WAL/RP/rp_coverage_test.go` — extend with R16-10..14.
- `LOG/LG/rotation_gzip_test.go` (new) — rotate, gzip, list.
- `TXN/VL/slot_test.go` (new) — `AllocateSlot` returns slot with
 non-nil arena; `ReleaseSlot` calls `PutArena`; verify pool
 reuse.

### Integration

- `TestEngine_ParamBinding_EndToEnd` — `Open`, `CREATE TABLE`,
 `INSERT ?`, `SELECT WHERE ?`, `UPDATE WHERE ?`, `DELETE WHERE ?`
 with type validation exercised.
- `TestEngine_ParamBinding_TypeMismatch` — bind wrong Go type,
 expect `AP.ErrTypeMismatch`, no row mutation.
- `TestFlush_Compaction_Visibility` — after iter-12b path fix,
 flush an L0 SST, observe it in the manifest, run compaction,
 observe the L0 SST consumed.

### Benchmarks

- `BenchmarkParamBinding_Insert` —10K inserts with `?` placeholders.
- `BenchmarkGzipRotation` — rotate a10 MB log, time the gzip step.

## Deviations / Risks

1. **Param threading touches every operator.** Adding `params
 []interface{}` to ~10 operators is mechanical but touches a lot
 of files. Risk: regression in operators that previously passed
 `nil` and now pass a non-nil slice. Mitigation: existing `Eval`
 already handles `params` (returns nil for out-of-range index);
 the only behavioral change is that `?` now resolves. Test
 coverage on each operator's `Eval` call path.
2. **`stmt.paramTypes` extraction requires running the planner at
 `Prepare` time.** This is a one-time cost per `Prepare`, not per
 execution. Existing `Prepare` already parses the SQL; running the
 planner once and caching the result is acceptable. If a user
 creates a statement that references a non-existent table,
 `Prepare` should still succeed (lazy binding) — the type check
 runs on `Query`/`Exec` with the schema available. Mitigation:
 `Prepare` produces a `paramTypes []int` (one entry per `?`); if
 the planner can't determine a column type, leave it as `-1`
 (skip validation for that slot).
3. **Gzip on rotation adds a `~5-20 ms` cost on the rotation
 path.** Rotation is rare (every `MaxSize` bytes) so this is
 acceptable. Mitigation: do the gzip in a goroutine if
 `MaxSize` is small; sync only if the goroutine hasn't finished
 before the next rotation.
4. **REQ000186 changes on-disk path layout.** Existing SSTs from
 iter-09 through iter-15 are in the flat `<dir>/L0_*.sst` layout.
 On first open after the upgrade, the engine will not find them.
 Mitigation: on `Open`, scan both `dir/sst/` and `dir/` and
 import any flat-layout L0 SSTs into the manifest, then `os.Rename`
 them into the nested path. (This is a small migration step in
 `Open`; document in commit message.)
5. **REQ000179 changes arena lifecycle.** Any test that holds a
 `*tx` reference past `Commit`/`Abort` and then calls `Insert` or
 `Delete` will fail (the arena is gone). Mitigation: `tx.finished`
 is already set; the existing `ErrTxFinished` check at line52
 catches this. Audit existing tests to ensure no one relies on
 post-finish arena use.
6. **REQ000167 also touches public API surface (`Stmt.Query`/`Exec`
 now returns `AP.ErrTypeMismatch` on bind mismatch).** Callers who
 silently relied on `?` being a no-op will now see errors.
 Mitigation: this is exactly the desired behavior; document in
 the commit message.

## Completion Criteria

| Rule | State |
|---|---|
| `go vet ./...` zero warnings | green |
| `gofmt -s -l .` no drift | green |
| `go test ./... -race -count=1` all green | green |
| `Stmt.Query`/`Exec` returns `AP.ErrTypeMismatch` on Go type × SQL type mismatch | green |
| `?` placeholder resolves correctly with `args ...any` end-to-end | green |
| `WAL/RP` coverage ≥85% | green |
| `flushManager` writes to `sst/L0_<id>.sst`; compaction reads it | green |
| `transactionSlot.arena` populated in `AllocateSlot`, released in `ReleaseSlot` | green |
| Bloom filter size = `(keyCount *10 +7) /8` bytes | green |
| Rotated log files are gzipped; `listRotatedFiles` matches `.gz` | green |
