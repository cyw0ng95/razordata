# Iteration20 — TXN Correctness + Quick Wins + SQL Completeness + Type System (v0.17.0)

**Subsystem:** `TXN/VL`, `TXN/LC`, `LOG/HK`, `ENG/LS`, `SQL/EX`, `SQL/PS`, `SQL/PL`
**Status:** planned
**Est. LOC:** ~2,500
**Requirements:** REQ000171, REQ000147, REQ000193, REQ000198, REQ000196, REQ000202, REQ000174, REQ000197, REQ000201, REQ000206, REQ000207, REQ000208, REQ000209, REQ000218, REQ000229
**Target release:** v0.17.0
**Commit:** `<filled at completion>`
**Tag:** v0.17.0

## Overview

Comprehensive iteration addressing four categories of work:

**Block A: TXN Correctness (Critical).** The current
commit protocol is incomplete: `tx.Commit()` does not write
to WAL (REQ000171), and the protocol lacks the 6-phase structure
(REQ000147) called for in `TXN.md:203-226`. Without these, the
database has a data loss risk — commits can be lost on crash
because the WAL never sees them. This block fixes the highest-
risk gap in the codebase.

**Block B: Quick Wins (High ROI).** Four small, high-value
optimizations: wire MetricHook counters (REQ000193) that have
fields but never increment, eliminate skiplist insert allocations
(REQ000198), use the dead-code HashAggregate in the planner
(REQ000196), add parser tests for CASE/EXISTS (REQ000202).

**Block C: SQL Completeness (High Visibility).** Closes a
critical SQL feature gap: OUTER JOINs (LEFT/RIGHT/FULL) are
parsed but the planner explicitly skips them (`planner.go:234`).
Also lifts SQL/PL test coverage from 30.6% to 80%+ (REQ000201),
the lowest in the codebase. Plus trivial fix for HAVING
(REQ000218).

**Block D: BloomFilter Alignment.** Replaces CRC32 with
FNV-1a double-hashing (REQ000174) per the design spec
(`ENG.md:93-98`). The implementation has drifted from design
(CRC32 Koopman + Castagnoli) to the documented
FNV-1a seeds (0x811C9DC5, 0x01000193).

**Block E: Type System Foundation (Critical).** No matter
LoC: type system completeness is foundational for SQL
correctness. Adds NUMERIC/DATE/TIME/JSON/DECIMAL type tokens
(REQ000206), parameterized types like VARCHAR(N) and DECIMAL(P,S)
(REQ000207), SQLite-like 5-affinity type coercion (REQ000208),
DEFAULT clause values (REQ000209), DECIMAL precision/scale
arithmetic (REQ000229). Closes the gap between Razordata's
8 type tokens and SQLite's full type system.

## Outcome

(empty — to be filled at end of iteration)

## Dependencies

- Required: iter-06 (`TXN/VL` commit protocol exists)
- Required: iter-00 (`LOG/HK` hook framework)
- Required: iter-04 (`ENG/LS` skiplist + SST infra)
- Required: iter-07 (`SQL/PS` parser)
- Required: iter-08 (`SQL/EX` operators exist)
- Required: iter-19 (Phase 1+2+3 vectorized execution foundation)
- Touches:
  - `internal/TXN/VL/protocol.go` — 6-phase commit
  - `internal/TXN/VL/wal.go` (new) — WAL record emission
  - `internal/TXN/LC/epoch.go` — background goroutine support
  - `internal/TXN/LC/reclaim.go` (new) — memory reclamation
  - `internal/LOG/HK/metric.go` — wire OnLog to atomic counters
  - `internal/ENG/LS/skiplist.go` — sync.Pool for scratch arrays
  - `internal/ENG/LS/sst_writer.go` — FNV-1a hash
  - `internal/ENG/LS/sst_reader.go` — FNV-1a hash verify
  - `internal/SQL/EX/planner.go` — HashAggregate, OUTER JOIN, HAVING
  - `internal/SQL/EX/join.go` — OUTER JOIN executor
  - `internal/SQL/EX/coerce.go` (new) — type affinity matrix
  - `internal/SQL/PS/ps_test.go` — CASE/EXISTS parser tests
  - `internal/SQL/PL/*_test.go` — cost model, index selection tests
  - `internal/SQL/LX/token.go` — NUMERIC, DATE, TIME, JSON, DECIMAL tokens
  - `internal/SQL/PS/ps.go` — VARCHAR(N), DECIMAL(P,S), DEFAULT parsing

## Current State (audit, 2026-06-09)

**TXN commit protocol gap.** `tx.Commit()` (`protocol.go:110`)
calls `t.sm.Validate()` and `node.Commit()` but does **not**
emit a WAL record. The `EncodeCommitRecord` function in
`wal_record.go:15` is defined but never called. The
`WAL/FL.BatchSync` is implemented (iter-17) and ready to use,
but the commit path doesn't wire to it. Result: commits are
lost on crash.

**Dead code.** `NewHashAggregate` (`hashagg.go:26`) is defined
but never called by the planner (`planner.go:271` always picks
`NewAggregate`).

**Stubs.** `MetricHook.OnLog` (`LOG/HK/metric.go:24`) is empty
even though `queryCount`/`rowsReturned`/`bytesRead`/`bytesWritten`
fields exist.

**Hot path allocation.** Skiplist `Insert` allocates
`predecessors := make([]*node, lvl)` and
`successors := make([]*node, lvl)` per call — 2 allocations
per insert.

**SQL feature gap.** `planner.go:234` reads
`if j.Kind != "INNER" && j.Kind != "CROSS" { continue }` —
LEFT/RIGHT/FULL joins are silently dropped.

**Test coverage gap.** `SQL/PL` is at 30.6% (lowest in the
codebase). The planner's cost model, index selection logic,
and plan cache are untested.

**Design drift.** `ENG/LS/sst_writer.go:93-94` uses
`crc32.Checksum` (Koopman + Castagnoli), but `ENG.md:93-98`
specifies FNV-1a seeds (0x811C9DC5, 0x01000193). Implementation
has drifted from design.

---

## Implementation Plan

### Block A: TXN Correctness (Critical, ~550 LOC, 3-4 days)

#### REQ000171: WAL Integration in Commit (~150 LOC)

Wire `tx.Commit()` to write a WAL record via `WAL.Flush.Sync()`.

**Steps:**
1. Add `WAL` field to `tx` struct (or accept via constructor)
2. In `Commit()`, after `t.finalize(SlotCommitted)`, call
   `t.wal.Append(EncodeCommitRecord(txnID, commitTS, keys))`
3. Call `t.wal.Sync()` for durability
4. On error, return wrapped error

**Tests:**
- `TestCommit_WritesWALRecord` — verify WAL has 1 record after commit
- `TestCommit_DurabilityAfterCrash` — crash simulation,
  verify record survives
- `TestCommit_NoWALConfigured` — backward compat for tests
  that don't wire WAL

#### REQ000147: Complete Commit Protocol (~400 LOC)

Implement the 6-phase structure per `TXN.md:203-226`:
Begin, Read, Write, Pre-commit, Commit, Post-commit + Abort flow.

**Steps:**
1. Add explicit phase tracking to `tx` struct
2. Add `PhaseBegin()`, `PhaseRead()`, `PhaseWrite()`,
   `PhasePreCommit()`, `PhaseCommit()`, `PhasePostCommit()`
3. Add `PhaseAbort()` for the Abort flow
4. Each phase validates preconditions before transitioning
5. Add CAS-based retry logic for write-write conflicts

**Tests:**
- `TestCommit_Phases` — verify phase progression
- `TestCommit_AbortFlow` — Abort transitions to SlotAborted
- `TestCommit_Retry` — concurrent commits with conflicts

---

### Block B: Quick Wins (High ROI, ~480 LOC, 2-3 days)

#### REQ000193: MetricHook Counters (~150 LOC)

Wire `OnLog` to increment existing atomic counters.

**Steps:**
1. Parse `msg` field for known patterns:
   - `"sql.query.start"` → `queryCount++`
   - `"sql.rows"` → `rowsReturned += count`
   - `"wal.read"` → `bytesRead += count`
   - `"wal.write"` → `bytesWritten += count`
2. Add `Stats()` method to expose all counters

**Tests:**
- `TestMetricHook_QueryCount` — verify counter increments
- `TestMetricHook_BytesRead` — verify byte counter
- `TestMetricHook_Reset` — verify counter can be reset

#### REQ000198: Skiplist sync.Pool (~100 LOC)

Eliminate 2 allocations per insert.

**Steps:**
1. Add `predecessorPool sync.Pool` and `successorPool sync.Pool`
2. In `Insert()`, `Get()` from pool instead of `make()`
3. `Put()` back in defer
4. Slices sized to max level (16) to avoid re-allocation

**Tests:**
- `TestSkiplist_Allocations` — `testing.AllocsPerRun` = 0
- `TestSkiplist_ConcurrentPoolSafety` — no race conditions
- Benchmark improvement: `BenchmarkSkiplistInsert` should drop 30%+

#### REQ000196: HashAggregate in Planner (~80 LOC)

Use `NewHashAggregate` instead of `NewAggregate` for
datasets > 1000 rows.

**Steps:**
1. In `planner.go:262`, check `len(s.Cols) > 1000`
2. If true, use `NewHashAggregate`, else `NewAggregate`
3. Verify HashAggregate produces same results

**Tests:**
- `TestPlanner_HashAggregateForLarge` — 10K rows use HashAggregate
- `TestPlanner_AggregateForSmall` — 100 rows use Aggregate
- `TestAggregate_Equivalence` — both produce same output

#### REQ000202: CASE/EXISTS Parser Tests (~150 LOC)

Add table-driven tests for `parseCaseExpr` and `parseExists`.

**Steps:**
1. Add `TestParseCaseExpr_SimpleForm`
2. Add `TestParseCaseExpr_SearchedForm`
3. Add `TestParseExists_TrueSubquery`
4. Add `TestParseExists_FalseSubquery`
5. Add `TestParseExists_WithColumns`

**Coverage target:** `parseCaseExpr` 0% → 80%, `parseExists` 0% → 80%

---

### Block D: BloomFilter Alignment (~150 LOC, 1 day)

#### REQ000174: FNV-1a Double-Hash (~150 LOC)

Replace CRC32 with FNV-1a per design spec.

**Steps:**
1. Add `fnvHash1` and `fnvHash2` functions in `sst_writer.go`
   using seeds 0x811C9DC5 and 0x01000193
2. Replace `crc32.Checksum` calls with `fnvHash1` and `fnvHash2`
3. In `sst_reader.go`, verify bloom filter using same hashes
4. Backward compat: detect old CRC32 SSTs via magic number,
   read both formats

**Tests:**
- `TestBloomFilter_FNV1a_Conformance` — verify hash matches spec
- `TestBloomFilter_RoundTrip` — write + read produce same result
- `TestBloomFilter_FalsePositiveRate` — <1% at expected density
- `TestSST_BackwardCompat` — old CRC32 SSTs still readable

---

### Block C: SQL Completeness (High Visibility, ~900 LOC, 4-5 days)

#### REQ000197: OUTER JOIN Executor (~400 LOC)

Implement LEFT/RIGHT/FULL JOIN, currently skipped at
`planner.go:234`.

**Steps:**
1. In `planner.go`, remove the `if j.Kind != "INNER" && j.Kind != "CROSS"`
   skip
2. Add `NestedLoopLeftJoin` operator in `join.go`
3. For LEFT JOIN: emit left row + NULL-padded right when no match
4. For RIGHT JOIN: symmetric
5. For FULL JOIN: union of LEFT and RIGHT

**Tests:**
- `TestLeftJoin_NoMatch` — left row emitted with NULL right
- `TestLeftJoin_WithMatch` — standard join output
- `TestRightJoin_EmptyLeft` — right rows with NULL left
- `TestFullJoin_BothEmpty` — both sides padded

#### REQ000201: SQL/PL Coverage 30% → 80% (~500 LOC)

Add tests for cost model, index selection, plan caching.

**Steps:**
1. `TestCostModel_TableScan` — verify cost computation
2. `TestCostModel_IndexScan` — verify lower cost vs SeqScan
3. `TestIndexSelection_PKLookup` — verify PK index chosen
4. `TestIndexSelection_NoIndexFallback` — falls back to SeqScan
5. `TestPlanCaching_DeterministicKey` — same query → same plan ID
6. `TestPlanCache_Eviction` — LRU eviction works

**Coverage target:** `SQL/PL` 30.6% → 80%+

---

### Block E: Type System Foundation (Critical, ~420 LOC, 2-3 days)

Closes the gap between Razordata's type tokens and the
SQLite-like type system. Adds parameterized types, type
affinity, and DECIMAL arithmetic. No matter LoC: this is
a foundational capability.

#### REQ000206: New Type Tokens (S, ~80 LOC)

Add NUMERIC, DATE, TIME, JSON, DECIMAL type tokens to
`SQL/LX/token.go`.

**Steps:**
1. Add `T_NUMERIC`, `T_DATE`, `T_TIME`, `T_JSON`, `T_DECIMAL`
   constants in `LX.token.go`
2. Add tokenName mapping (e.g., `T_NUMERIC: "NUMERIC"`)
3. Add keyword matching in `PS` keyword map

**Tests:**
- `TestLex_NUMERIC_Token`
- `TestLex_DATE_Token`
- `TestLex_JSON_Token`

#### REQ000207: Parameterized Types (M, ~120 LOC)

Parse `VARCHAR(N)`, `CHAR(N)`, `DECIMAL(P,S)`, `NUMERIC(P,S)`.

**Steps:**
1. In `parseColumnDef`, after type token, check for `T_LPAREN`
2. Consume integer literal for size (N) or precision (P)
3. If `T_COMMA`, consume scale (S)
4. Expect `T_RPAREN`
5. Store in `ColDef.Size` and add `ColDef.Precision`/`Scale`
   fields

**Tests:**
- `TestParse_VARCHAR_N`
- `TestParse_DECIMAL_PS`
- `TestParse_CHAR_N`

#### REQ000208: Type Affinity (M, ~100 LOC)

SQLite-like 5 affinities: TEXT, NUMERIC, INTEGER, REAL, NONE.

**Steps:**
1. New file `SQL/EX/coerce.go`
2. Affinity matrix: column type → affinity
3. Modify `Eval` to apply implicit coercion based on affinity
4. When comparing values of different affinities, coerce
   per SQLite rules (e.g., NUMERIC vs TEXT → try numeric,
   fallback to text compare)
5. Add `Options.StrictTypes` (default true) to disable
   affinity for safety

**Tests:**
- `TestAffinity_IntVsText` — `'5' = 5` semantics
- `TestAffinity_NumericPromotion` — `5 = 5.0` always true
- `TestAffinity_StrictMode` — opt-in only

#### REQ000209: DEFAULT Clause (S, ~60 LOC)

Wire `ColDef.Default` (already in AST) into CREATE TABLE.

**Steps:**
1. In `parseColumnDef`, after optional NOT NULL, check for
   `T_DEFAULT`
2. Consume `T_DEFAULT` and parse expression via `parseExpr`
3. Store in `ColDef.Default`
4. EX executor: on INSERT, fill missing column with
   `ColDef.Default` evaluated

**Tests:**
- `TestParse_DEFAULT_Literal`
- `TestParse_DEFAULT_Null`
- `TestInsert_UsesDefault`

#### REQ000229: DECIMAL Storage (M, ~120 LOC)

Precision/scale arithmetic for DECIMAL type.

**Steps:**
1. Choose `math/big.Float` (stdlib, no dependency) for v1
2. Add `Decimal` type wrapping `*big.Float` with precision/scale
3. In `Eval`, recognize DECIMAL columns, return Decimal value
4. In `evalBinary`, dispatch DECIMAL arithmetic
5. Comparison operators handle DECIMAL vs DECIMAL and
   DECIMAL vs numeric

**Tests:**
- `TestDecimal_Addition_Precision`
- `TestDecimal_Comparison`
- `TestDecimal_OverflowBehavior`

#### REQ000218: HAVING Filter (S, ~40 LOC, included for cohesion)

Wire HAVING filter (currently broken at `planner.go:275`).

**Steps:**
1. In planner, after Aggregate, add Filter with `s.Having`
2. Verify Filter can apply to grouped rows
3. Test that HAVING works on aggregate results

**Tests:**
- `TestPlanner_HAVING_Filter`
- `TestHAVING_NoMatchingGroups`

---

## Deviations / Risks

1. **WAL commit breaks existing tests.** Some tests use
   `VL` directly without a WAL. Mitigation: WAL field is
   optional; `nil` WAL skips the write (test mode).

2. **OUTER JOIN semantics differ from spec.** LEFT/RIGHT/FULL
   join have subtle semantic differences (NULL handling,
   duplicate elimination). Mitigation: table-driven tests
   against SQL standard reference outputs.

3. **FNV-1a backward compat.** Old SSTs use CRC32.
   Mitigation: detect via magic number; reader supports both.

4. **SQL/PL coverage hard to reach 80%.** Plan cache and
   memoization have complex interaction. Mitigation: prioritize
   cost model + index selection; defer cache edge cases to
   v0.18.0 if needed.

5. **HashAggregate may have different performance
   characteristics than Aggregate.** Mitigation: keep both,
   choose based on row count (REQ000196 threshold: 1000 rows).

6. **Type affinity may break implicit casts.** SQLite-like
   affinity changes how comparisons coerce types (e.g.,
   `'5' = 5` may become TRUE). Mitigation: opt-in via Option;
   default to strict (no implicit coercion) for safety.

7. **DECIMAL precision/scale requires decimal library.**
   `big.Float` is stdlib but slower; `shopspring/decimal`
   is faster but adds dependency. Mitigation: use `big.Float`
   for v1; add `shopspring/decimal` later if performance
   matters.

## Completion Criteria

| Rule | State |
|---|---|
| `go vet ./...` zero warnings | TBD |
| `gofmt -s -l .` no drift | TBD |
| `go test ./... -race -count=1` all green | TBD |
| TXN Commit writes WAL record | TBD (REQ000171) |
| TXN 6-phase commit protocol complete | TBD (REQ000147) |
| MetricHook counters increment | TBD (REQ000193) |
| Skiplist zero allocations per insert | TBD (REQ000198) |
| HashAggregate used in planner for large data | TBD (REQ000196) |
| SQL/PS parseCaseExpr, parseExists 0% → 80% | TBD (REQ000202) |
| SST writer uses FNV-1a per design | TBD (REQ000174) |
| OUTER JOIN (LEFT/RIGHT/FULL) works | TBD (REQ000197) |
| SQL/PL coverage 30% → 80% | TBD (REQ000201) |
| HAVING filter wires (currently broken) | TBD (REQ000218) |
| New type tokens: NUMERIC, DATE, TIME, JSON, DECIMAL | TBD (REQ000206) |
| VARCHAR(N), DECIMAL(P,S) parameterized parsing | TBD (REQ000207) |
| Type affinity system (5 affinities) | TBD (REQ000208) |
| DEFAULT clause parsing and INSERT fill | TBD (REQ000209) |
| DECIMAL precision/scale arithmetic | TBD (REQ000229) |

## Benchmarks

Target improvements:

| Benchmark | Baseline | Target |
|-----------|----------|--------|
| `BenchmarkSkiplistInsert` | TBD allocs | 0 allocs, 30%+ faster |
| `BenchmarkGroupBy_10K` | row | HashAggregate path |
| `BenchmarkOUTER_JOIN_1K` | not implemented | <100ms |
| `BenchmarkCommit_Durability` | no WAL | <1ms WAL append |

## Estimated Cost

| Block | LOC | Tests | Bench | Total | Days |
|-------|-----|-------|-------|-------|------|
| A: TXN Correctness | 550 | 250 | 50 | 850 | 3-4 |
| B: Quick Wins | 480 | 200 | 80 | 760 | 2-3 |
| C: SQL Completeness | 900 | 300 | 100 | 1,300 | 4-5 |
| D: BloomFilter | 150 | 100 | 50 | 300 | 1 |
| E: Type System | 420 | 180 | 50 | 650 | 2-3 |
| **Total** | **2,500** | **1,030** | **330** | **3,860** | **12-16** |
