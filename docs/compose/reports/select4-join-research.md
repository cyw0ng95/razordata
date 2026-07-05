# Select4 Multi-Table JOIN — Research Report

**Date:** 2026-07-05
**Author:** Research Agent
**Scope:** select4 corpus multi-table JOIN plans, DBG infrastructure, known issues

---

## 1. Executive Summary

This report analyzes the select4 multi-table JOIN test corpus (tests/sqlcmp/corpus/test/select4.test), the planning and execution infrastructure for 3-table+ joins, the DEBUG subsystem for tracing JOIN behavior, and all known defects with their current status. Key finding: **the join infrastructure works for select4** — all 7 failures are in compound set operations (UNION/EXCEPT), not joins. The highest-priority defect is REQ001113, which affects 3-table+ comma-joins with constant-equality predicates.

---

## 2. select4 Corpus — Structure & Characteristics

**File:** tests/sqlcmp/corpus/test/select4.test
- **Lines:** 48,300
- **Queries:** 2,832 (all expected to pass, zero error/abort directives)
- **Tables:** t1–t9, each with 100 rows
- **Schema:** (aX INTEGER, bX INTEGER, cX INTEGER, dX INTEGER, eX INTEGER, xX VARCHAR(30))

### Join Distribution

| Join Size | Approximate Count |
|-----------|-------------------|
| 2-table comma joins | ~370 |
| 3–8 table comma joins | ~1,444+ |
| 7-table joins | 192 |
| 8-table joins | 84 |

### Predicate Patterns

| Pattern | Example | Count |
|---------|---------|-------|
| Constant equality | `c5=733`, `488=d2` | 1,200+ |
| Cross-table equality (join keys) | `a1=d9`, `c5=c9` | 800+ |
| IN predicates | `a2 IN (336,268,304,574,980)` | 400+ |
| OR predicates | `(e9=245 OR 35=e9 OR 799=e9)` | 200+ |

**Key observation:** Every multi-table query uses **implicit comma syntax** (no `INNER JOIN` keyword). Each query is tested in **all table-order permutations** to validate planner commutativity.

---

## 3. Multi-Table JOIN Planning

### Entry Point: `planSelectJoins` (planner.go:6351)

The planner builds multi-table joins as a **left-deep linear chain** for connected graphs, and uses **bushy trees** only for disconnected table sets.

### Join Order Selection

| Table Count | Algorithm | Location |
|-------------|-----------|----------|
| ≤4 | `exhaustiveJoinOrder` (Heap's permutation, max 24) | planner.go:5316–5359 |
| 5–8 | `n3JoinOrderingMultiStart` (N3 from each base table) | planner.go:5172–5291 |
| >8 | `n3JoinOrdering` (N3, fixed base, N=24 heap) | planner.go:4888–5150 |

**N3 Algorithm Details:**
- Min-heap of N=24 best partial plans
- Each step extends each partial plan by adding one remaining table
- Pruning: candidates exceeding `bestCost × n3PruneMultiplier` (2.0) are dropped
- Per-table selectivity pre-computed from single-table predicates (REQ000909/000883)
- Predicate lookup caching by (sorted joined-set, candidate) key (REQ001096)

### Bushy Grouping: `groupBushyJoins` (planner.go:5544–5670)

**Algorithm:**
1. **k ≤ 3:** Returns single group `[][]string{joinOrder}` — no bushy benefit
2. **k > 3 and connected:** Returns single left-deep group (REQ001192 fix)
3. **k > 3 and disconnected:** Partitions into independent groups

**Transitive Dependency Check (REQ001113):**
- Tracks `equiJoinTables` map: which tables each table equi-joins to
- If a candidate equi-joins to a table that is also equi-joined by a table in an existing group, they share a common equi-join partner and must be in the same group
- Also tracks `consumedPreds` so cross-group predicates aren't re-extracted

### Predicate Handling

`splitPredicatesByTable` (planner.go:1308–1337) separates:
- **Per-table predicates** → pushed down to individual scans (lines 2278–2283)
- **Cross-table predicates** → used for join ordering and join operator creation

**Equi-join key extraction:** `extractEquiJoinKeys` (lines 1644–1655) calls `equiJoinKey` (lines 1588–1610), which checks if a binary `=` expression connects a column from the already-joined set to a column from the right table. Returns **fully qualified names** (`table.col`) so downstream lookup works across multi-table rows.

**Transitive equality inference:** `inferTransitiveEqualities` (lines 6026–6038+) uses union-find to derive implied equalities (e.g., `a=b AND b=c` ⇒ `a=c`), expanding the set of usable join keys.

---

## 4. JOIN Execution

### Plan Structure: Left-Deep Linear Chain

For connected graphs, the plan tree is always left-deep:

```
          NLJ(t2, t3)
         /         \
     t1          t2-t3
```

Each join operator's output becomes the **left child** of the next join. Row flow is concatenation: `[left.Data..., right.Data...]`.

### Operator Implementations

| Operator | Type | Purpose | Key Methods |
|----------|------|---------|-------------|
| `NestedLoopJoin` | join.go:388–420 | General join — hash mode fallback, block mode, simple NLJ | `Next()`, `nextBlock()`, `tryHashCrossJoin()` |
| `HashJoin` | hashjoin.go | Radix-partitioned equi-join, multi-column keys | `buildAndProbe()`, `emitUnmatchedRight()` |
| `HashCrossJoin` | hashcrossjoin.go | Small-table single-column equi-join (≤1024 rows) | `lookupColumn()` |
| `MergeJoin` | mergejoin.go | Sort-merge on pre-sorted inputs | — |

### NLJ's Three-Mode Selection (join.go:388–420)

1. **Hash mode** (`tryHashCrossJoin`): Only for pure INNER/CROSS with no ON clause and small tables. Skipped if either side is already a join op (`isJoinOp`).
2. **Block mode** (`nextBlock`): Default — batches 64 left rows, materializes right, emits matches.
3. **Simple NLJ** (lines 421–491): Single-row at a time (no batch). Used as fallback.

### Predicate Evaluation

| Operator | Method | Details |
|----------|--------|---------|
| HashJoin | Two-level | Hash match + `ValuesEqualMulti` value comparison |
| NestedLoopJoin | `EvalValue` | ON closure with `Outer` pointer chain for qualified lookups |
| Compiled predicates | Cached indices | `findColIndex` resolved once per closure |

**Critical safeguard (REQ000846):** Bare-name col=col comparisons are NOT compiled because the compiled function reads from the wrong row's Data slice for columns that resolve via the outer chain. Falls back to `Eval`.

### Column Prefixing (Critical for Multi-Table)

When a table is first introduced in a join, its columns are prefixed with the table name (`prefixCols()`). The `hasAnyPrefix()` guard (REQ000725/REQ000861) prevents **double-prefixing** when the left side is already join output. Without it, columns become `t4.t3.a` and lookups fail.

**Shared schema optimization:** Join operators pre-build shared metadata (Cols/Types/colIndex) from the first output row and reuse it across all emitted rows to avoid per-row allocation.

---

## 5. DEBUG Infrastructure — Current State

### Package Layout

```
internal/DBG/
├── DI/     di.go          — Debugger coordinator (NewDebugger, Close)
├── JD/     tracer.go      — JoinTracer interface, BufferedTracer, verbosity levels
│          event.go         — 5 event types: RowFlow, Predicate, ColumnOffset, Strategy, Correlation
│          buffer.go        — Lock-free ring buffer (power-of-2 capacity, atomic counters)
├── CT/     stats.go       — DebugStats with 15 atomic counters
│          metric.go        — MetricHook + expvar export + 5 latency histograms
│          hist.go          — LatencyHist (20-bucket log2 histogram)
├── DC/     control.go     — Runtime toggling: per-subsystem log level, trace class on/off, slow query threshold
├── TE/     ring.go        — Lock-free ring buffer (general trace events)
│          sink.go          — EventSink impl for LOG/HK
├── PR/     profile.go     — On-demand CPU/heap/goroutine/mutex/block/trace profiles
│          signal.go        — SIGUSR1/SIGUSR2/SIGHUP handlers
├── SK/     socket.go      — UNIX domain socket server at <dbdir>/.debug/debug.sock
│          dispatch.go      — Command dispatch: heap, cpu, goroutine, stats, gc, debug_join*
├── IN/     inspect.go     — Page dumps, buffer pool state, active transactions
└── DC/     control.go     — Already listed above
```

### JoinTracer Interface (JD/tracer.go)

```go
type JoinTracer interface {
    RowFlow(operator string, table string, rowID uint64, entering bool)
    Predicate(operator string, expr string, leftRowID, rightRowID uint64, passed bool)
    ColumnOffset(operator string, expected, actual int, colName string)
    Strategy(operator string, chosen string, reason string, estimatedCost float64)
    Correlation(stage int, tables []string, rowCount int64)
}
```

### Event Types

| Event Type | Fields | Verbosity Level |
|------------|--------|-----------------|
| `EventRowFlow` | table, rowID, entering | Summary (1) |
| `EventPredicate` | expr, leftRowID, rightRowID, passed | Detailed (2) |
| `EventColumnOffset` | expected, actual, colName | Detailed (2) |
| `EventStrategy` | chosen, reason, estimatedCost | Summary (1) |
| `EventCorrelation` | stage, tables, rowCount | Summary (1) |

### BufferedTracer

- **Capacity:** 4096 events (power-of-2 for fast masking)
- **Append:** Lock-free via `atomic.Uint64` written counter
- **Overflow:** Tracks dropped events
- **Flush:** Atomically resets buffer head and returns all events

### Debug Control (DC)

| Function | Purpose |
|----------|---------|
| `SetLogLevel(subsystem, level)` | Per-subsystem log level override |
| `EnableTrace(class, enabled)` | Trace class toggle |
| `SetSlowThreshold(d)` | Slow query threshold in nanoseconds |

### Counters (CT/stats.go)

15 atomic counters: queries (total/slow), rows (returned/written), pages (read/written), WAL (bytes/fsyncs), compactions (total/bytes), cache (hits/misses), txns (commits/aborts), lock contention (ns). Exposed via `expvar.Publish("razordata")`.

### Operator Integration

Each operator emits debug events at key points:

| Operator | Debug Functions |
|----------|-----------------|
| NestedLoopJoin | `nljDebugStrategy()`, `nljDebugRowFlow()`, `nljDebugPredicate()`, `nljDebugCorrelation()` |
| HashJoin | `hashJoinDebugStrategy()`, `hashJoinDebugRowFlow()`, `hashJoinDebugPredicate()` |
| Filter | `filterDebugPredicate()` |
| Project | `projectDebugOffset()` (column offset mismatch detection) |

**Build tag pattern:** Each debug function has `*_debug.go` (real impl, `//go:build debug`) and `*_nodebug.go` (noop stub, `//go:build !debug`). Zero overhead in production builds.

### How to Enable Debugging

**Method 1: Socket Commands (WORKING)**

```bash
# Build
go build -tags debug ./cmd/razor

# Start with EnableDebugSocket: true, then:
nc -U <dbdir>/.debug/debug.sock
debug_join detailed    # Enable detailed tracing
# ... run queries ...
debug_join_flush        # Flush captured events
debug_join_filter <op>  # Filter by operator name
debug_join_summary      # Aggregated stats
debug_join off          # Disable
```

**Method 2: SQL PRAGMAs (NOT WORKING)**

The code for PRAGMAs exists in `pragma_debug.go` but **is never wired up** to the SQL execution path. The documented PRAGMAs in `docs/development/DEBUG.md` do not work through SQL.

---

## 6. What's Missing in Debug Infrastructure

### Critical Gaps

| Issue | Location | Impact |
|-------|----------|--------|
| SQL PRAGMAs are orphaned | `HandleDebugPragma` defined but never called in `WT.Pragma.Next()` | Cannot trace joins via SQL; socket commands only work |
| No per-query debug scope | Global tracer | Enabling tracing affects all queries until explicitly disabled |
| No row value inspection | "Full" verbosity (level 3) | No code path emits actual row values at level 3 |
| No hash table state inspection | HashJoin | No visibility into bucket distribution, probe counts, memory usage |
| No query/plan ID | Events | If multiple queries run concurrently, events are mixed with no way to separate |
| No JOIN-specific counters | CT/stats.go | General counters only; no nested loop iterations, hash builds, hash probes |
| Fixed buffer capacity | Hardcoded at 4096 | No runtime way to adjust buffer size |
| CTE debug not built | REQ001196/REQ001197/REQ001198 | CTE tracing PRAGMAs don't exist |
| No debug init for JOIN tracer | `di.NewDebugger()` | JOIN tracer only created by PRAGMA/socket commands; no automatic registration |
| PRAGMA/socket interface mismatch | Slight API differences | Socket: `debug_join on|off|summary|detailed|full`; PRAGMA: `debug_join_tracing = off|summary|detailed|full` |

### No `pragma_nodebug.go` Stub

There is no `//go:build !debug` stub for `HandleDebugPragma`. Without it, callers that expect this function would fail to compile without the debug tag.

---

## 7. Known Issues in Multi-Table JOINs

### 🟥 REQ001113 — HIGH PRIORITY (25 select4 records still fail)

**Subsystem:** EX/join
**Status:** 2-table cases fixed (3 regression tests pass). 3-table+ cases still failing.

**Symptom:** 3-table+ implicit comma-joins with constant-equality predicates produce zero rows when ≥1 are expected.

**Example from select4:**
```sql
SELECT x8, e9+131 FROM t8, t9 WHERE e9=383 AND 561=e8
-- Expected: 1 row
-- Observed: 0 rows
```

**Progress:**
- REQ001156 (REQ001113_2table_test.go): 2-table sub-items verified fixed
- 3-table+ multi-table cross-joins produce zero surviving rows when they should produce ≥1

**Suspected Locations:**
- `HashJoin.buildAndProbe()` — drops the cross-table equality predicate when both sides are already per-table-filtered
- `NestedLoopJoin.Next()` — iterates row pairs but never evaluates the join predicate
- `Project` output column offsets misaligned after multi-table joins

**Root Cause Analysis:** The issue likely stems from one or more of:
1. **Cross-table predicate not propagated to join operator:** When both tables have per-table predicates that are pushed down, the cross-table equality predicate may not be correctly extracted and passed to the join operator
2. **Column offset mismatch:** After multiple join operators, the output column indices may not align with what downstream operators (Project, Filter) expect
3. **Outer chain resolution failure:** For compiled predicates, column indices may be stale if row layouts differ between iterations

**Tests:**
- `internal/SQB/EX/req001113_test.go` (2 tests): Bushy transitive dependency, cross-group equi-join
- `internal/SQB/EX/req001113_2table_test.go` (3 tests): Predicate distribution, table-order permutation, split distribution

### 🟧 Issue A: Column Offset Mismatch in HashJoin

**Location:** hashjoin.go:319–330 (`emitUnmatchedRight()`)

```go
leftLen := j.dataPerRow - len(right.Data)  // DERIVED
```

**Risk:** If `right.Data` length differs from `firstRightData` (variable column count), the offset is wrong. Affects left-outer joins and full outer joins.

**Potential Fix:** Compute `leftLen` from the shared schema rather than deriving from runtime data length.

### 🟧 Issue B: Compiled Predicate Index Staleness

**Location:** intermediate.go:1784–1824 (`makeCompiledColColCmp`)

```go
var leftIdx, rightIdx int = -1, -1  // Captured in closure
```

**Risk:** Column indices are cached in closure variables and resolved only once. If rows have different column layouts (possible in cross-join output), the index becomes stale.

**Potential Fix:** Revalidate indices on each row, or use a per-row lookup mechanism.

### 🟧 Issue C: NLJ Block Mode Data Buffer Aliasing

**Location:** join.go:857–901 (`blkDataBuf`)

**Risk:** Shared data buffer where each row's Data is a sub-slice with bounded capacity. Column layout is assumed constant across batches — could break with dynamic schemas (unlikely but possible).

**Mitigation:** The third colon capacity is set to `off+blkDataPerRow`, bounding each row's slice to itself. Generally safe, but could fail if right side's column layout changes between batches.

---

## 8. Current Test Status

### select4 SLT Results

```
pass=3849 fail=7 skip=1 parse-err=0 total=3857 (99.8% pass rate)
```

### The 7 Failing Queries

| Line | Type | Error |
|------|------|-------|
| L3766 | EXCEPT/UNION/EXCEPT (6 arms) | row count: got 5, want 6 |
| L3924 | UNION/EXCEPT (multi-arm) | hashed: got 39 cells, want 41 |
| L4724 | UNION ALL/UNION | hashed: got 38 cells, want 39 |
| L4784 | UNION/EXCEPT (multi-arm) | hashed: got 41 cells, want 42 |
| L4869 | UNION/EXCEPT (3 arms) | row count: got 0, want 1 |
| L5013 | UNION ALL/EXCEPT | hashed: got 16 cells, want 19 |
| L5065 | UNION ALL/EXCEPT (multi-arm) | row count: got 7, want 8 |

**Key finding:** All 7 failures are **compound set-operation queries** (UNION/EXCEPT/UNION ALL with NOT predicates). **Zero join failures.** Every failure is "got < want" (rows dropped or incorrectly deduplicated). The join infrastructure works for these queries; the failures are in set-operation execution.

### Failure Pattern

| Pattern | Count |
|---------|-------|
| Multi-arm compound queries | 7/7 |
| NOT predicates in WHERE | 7/7 |
| Under-counts (got < want) | 7/7 |
| Join-related failures | 0/7 |

---

## 9. Testing Infrastructure

### Test Files

| Test File | Purpose | Lines | Status |
|-----------|---------|-------|--------|
| `join_regress_test.go` | Dedicated multi-table join regression suite | 613 | 29 queries skipped (CI row volume), many marked `expectRows: -1` with `brokenNote` |
| `req001113_test.go` | Bushy transitive dependency + cross-group equi-join | 123 | Tests exist, 2 tests |
| `req001113_2table_test.go` | 2-table permutation + predicate distribution | 133 | 3 tests pass |
| `compound_probe_test.go` | Compound query with all indexes (select4-derived) | 125 | Probes index selection impact |
| `dual/probes_v2.go` | General SQL feature probes (cross-join, self-join) | 1325 | Contains join test cases |

### Notable Gap

`join_regress_test.go` has select4-derived 5–7 table comma-join queries matching the REQ001113 failure pattern, but they are only marked as `REQ000794` (comma join not parsed) — never updated to reference REQ001113. The `brokenNote` field is stale.

### Test Setup

`join_regress_test.go` setup (lines 367–413):
- 9 tables (t1–t9), each with 10 rows
- Values 100–109 for all 5 columns
- Equi-joins always match 1-to-1
- Test flow: runs each query, counts rows, verifies count if `expectRows >= 0`, checks `maxDuration` (hard fail) and `warnAt` (soft warn)

---

## 10. Immediate Action Items

| Priority | Action | Owner | Notes |
|----------|--------|-------|-------|
| P1 | Enable SQL PRAGMAs for join tracing | TBD | Wire `HandleDebugPragma` into pragma dispatch path; socket commands already work |
| P1 | Investigate REQ001113 failures | TBD | Use DBG socket commands to trace 3-table+ comma-joins with constant-equality predicates; focus on column offset alignment |
| P2 | Verify HashJoin column offset correctness | TBD | Test `emitUnmatchedRight()` with variable-width right rows |
| P2 | Audit compiled predicate index handling | TBD | Ensure cached indices are revalidated across rows with different layouts |
| P3 | Update `join_regress_test.go` brokenNote references | TBD | Change `REQ000794` to `REQ001113` for select4-derived queries |
| P3 | Add per-query debug scope | TBD | Allow scoping join tracing to a single query execution |
| P3 | Add JOIN-specific counters | TBD | Nested loop iterations, hash builds, hash probes, outer join null rows |
| P3 | Implement CTE debug infrastructure | REQ001196/REQ001197/REQ001198 | `debug_cte_tracing`, `debug_cte_flush`, `debug_cte_summary` PRAGMAs |

---

## Appendix A: Key File Locations

| File | Purpose |
|------|---------|
| `internal/SQB/EX/planner.go` | Join planning: `planSelectJoins`, `groupBushyJoins`, `exhaustiveJoinOrder`, `n3JoinOrdering`, `extractEquiJoinKeys` |
| `internal/SQB/OP/join.go` | `NestedLoopJoin`, three-mode selection, block mode, column prefixing |
| `internal/SQB/OP/hashjoin.go` | `HashJoin`, radix partitioning, `buildAndProbe`, `emitUnmatchedRight` |
| `internal/SQB/OP/hashcrossjoin.go` | `HashCrossJoin`, small-table equi-join |
| `internal/SQB/OP/mergejoin.go` | `MergeJoin`, sort-merge join |
| `internal/SQB/OP/intermediate.go` | `Filter`, `Project`, compiled predicates, `makeCompiledColColCmp` |
| `internal/DBG/JD/tracer.go` | `JoinTracer` interface, `BufferedTracer`, verbosity levels |
| `internal/DBG/JD/event.go` | Event types: `RowFlow`, `Predicate`, `ColumnOffset`, `Strategy`, `Correlation` |
| `internal/DBG/JD/buffer.go` | Lock-free ring buffer implementation |
| `internal/DBG/DC/control.go` | Debug control: `SetLogLevel`, `EnableTrace`, `SetSlowThreshold` |
| `internal/DBG/CT/stats.go` | `DebugStats`, atomic counters |
| `internal/DBG/SK/socket.go` | UNIX domain socket server |
| `internal/DBG/SK/dispatch.go` | Command dispatch for socket commands |
| `tests/sqlcmp/join_regress_test.go` | Multi-table join regression test suite |
| `tests/sqlcmp/slt/compound_probe_test.go` | Compound query probe test |
| `tests/sqlcmp/corpus/test/select4.test` | select4 test corpus (48,300 lines, 2,832 queries) |

---

## Appendix B: REQ References

| REQ | Status | Description |
|-----|--------|-------------|
| REQ001113 | 🔴 HIGH (partial) | 3-table+ comma-joins with constant-equality predicates produce zero rows. 2-table cases fixed; 25 select4 records still fail |
| REQ001155 | ✅ Fixed | ON-only join elimination bug |
| REQ001156 | ✅ Fixed | Chain-equi-joins incorrectly split into multiple bushy groups (fixed by `isConnectedGraph()`) |
| REQ001192 | ✅ Fixed | Connected equi-join graphs now return single left-deep group |
| REQ001196 | 🟡 TBD | No CTE trace events — `planRecursiveCTE` is a black box |
| REQ001197 | 🟡 TBD | No CTE debug counters — `DebugStats` lacks CTE iteration tracking |
| REQ001198 | 🟡 TBD | No CTE debug PRAGMAs |
| REQ001113_2table | ✅ Fixed | 2-table sub-items verified (3 regression tests pass) |
| REQ000725 | ✅ Fixed | Column prefixing guard prevents double-prefixing |
| REQ000794 | 🟡 Stale | Comma join parsing issues (referenced in `join_regress_test.go` but should be REQ001113) |
| REQ000846 | ✅ Fixed | Bare-name col=col comparisons not compiled (falls back to Eval) |
