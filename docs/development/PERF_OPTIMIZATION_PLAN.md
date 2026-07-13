# Performance Optimization Plan — Phase-based Implementation

> **Source:** Benchmark comparison (Razordata vs modernc.org/sqlite, 100 rows)
> **Baseline:** driver/bench_compare_test.go (REQ001419 companion)
> **Owner:** SYS/SE, ENG/LS, WAL/WR, SQB/EX, SQB/WT

---

## Performance Baseline

| Scenario | Razordata | SQLite | Gap |
|---|---|---|---|
| INSERT 100 rows | 4203 µs | 927 µs | **4.5x** |
| SELECT * | 128 µs | 133 µs | 1.0x |
| SELECT WHERE | 227 µs | 81 µs | 2.8x |
| SELECT ORDER+LIMIT | 252 µs | 141 µs | 1.8x |
| SELECT GROUP BY | 363 µs | 121 µs | 3.0x |
| SELECT COUNT(*) | 91 µs | 7 µs | **13x** |
| SELECT IN list | 149 µs | 71 µs | 2.1x |
| Self JOIN | 515 µs | 175 µs | 2.9x |
| UPDATE | 438 µs | 51 µs | **8.6x** |
| DELETE | 283 µs | timeout | — |

**Key insight:** Full scan (SELECT *) is parity — the storage engine and data structures are sound. The gap is in (a) session/Executor lifecycle overhead, (b) missing metadata, (c) no write batching, and (d) no block-level range pruning.

---

## Phase 0 — Pre-requisite: Fix broken test infrastructure

**REQ001415, REQ001416, REQ001417, REQ001418** (existing TBD entries)

Before implementing any performance work, the existing broken tests in `SYS/ST`, `SYS/TX`, `SYS/SY`, and `ENG/LS` must pass. These are not performance work — they are test-infrastructure blockers. Each of these fixes the `RZR-CFG-003 engine closed` failure and the catalog restart panic so that the benchmark suite has a green baseline to measure against.

**Estimate:** 2.5 days total across the 4 REQs.

---

## Phase 1 — Session & Executor Lifetime (Highest ROI)

**REQ001419: Session-level Executor reuse**
**REQ001422: Prepared statement persistence**

These are the two highest-ROI changes: they eliminate the per-query reconstruction of the full execution stack (Executor + stmtCache + planCache + operator tree + execCtx + rowArena). Session-level Executor is created once and reused across all queries in that session. PreparedPlan holds a compiled operator tree that can be reopened with new parameters without re-planning.

### REQ001419 — Session-level Executor reuse
**Subsystem:** SYS/SE + SYS/AP  
**Effort:** 1.5 days  
**Deps:** Phase 0 (REQ001415) must pass first  

**Fix:**
1. `Session` struct gains an `Executor` field (created once in `Engine.Begin()`).
2. `Session.Executor()` returns the cached Executor; `Session.Close()` shuts it down.
3. `Exec/Query/QueryAll` in SQB/EX change from always constructing inline to accepting the pre-built Executor as a parameter.
4. `database/sql.Driver` (driver/ds.go) — `openConn` calls `Engine.Begin()` and passes the Session's Executor to all subsequent calls.
5. Verify: 100-row INSERT benchmark drops from 4203 µs/op to ≤ 1500 µs/op (67% reduction).

**Acceptance:**
- `go test ./driver/ -bench='BenchmarkRazordata_Insert'` ≤ 1500 µs/op
- All existing driver tests pass
- SLT corpus (select1..select5) passes
- No memory leak: 1000 repeated sessions cleaned up cleanly

### REQ001422 — Prepared statement persistence
**Subsystem:** SYS/SE + SQB/EX + driver  
**Effort:** 1.5 days  
**Deps:** REQ001419 (Executor reuse must be in place first)  

**Fix:**
1. New `PreparedPlan` struct (SYS/SE/stmt.go) holding a compiled operator tree that is closed (resources released) but re-openable.
2. `Session.Prepare(sql string)` returns `*PreparedPlan` — compiles the query once, closes the tree.
3. `PreparedPlan.Exec(args)` / `PreparedPlan.Query(args)` inject parameters, re-open the tree, execute.
4. `database/sql.Stmt` caches a `*PreparedPlan` across calls — `driver.Stmt` no longer reconstructs the plan on every Exec/Query.
5. `PreparedPlan.Close()` releases the tree resources.

**Acceptance:**
- Repeated `SELECT * FROM t` via prepared stmt ≤ 1.5x per-query `QueryRow` cost
- `BenchmarkRazordata_SelectAll` with prepared stmt ≤ 80 µs/op
- No operator tree leak across 1000 prepared-exec cycles

---

## Phase 2 — Table Metadata & COUNT Optimization

**REQ001420: Table-level row count metadata**

This directly attacks the 13x COUNT(*) gap. The fix is a catalog table stat that stores approximate row count and a planner shortcut that returns it as a scalar operator instead of scanning.

### REQ001420 — Table-level row count metadata
**Subsystem:** ENG/LS + SQB/EX + ENG/CT  
**Effort:** 1 day  
**Deps:** Phase 0 (catalog path must work)  

**Fix:**
1. `TableStats` catalog record: `(table TEXT, row_count INTEGER, last_updated INTEGER)`.
2. On INSERT/UPDATE/DELETE, update `row_count` in WAL (append-only; exact for single-row ops, approximate for bulk).
3. On Engine.Open, reconstruct row_count from catalog + any WAL entries.
4. In SQB/EX planner: for `SELECT COUNT(*) FROM t` with no GROUP BY / DISTINCT / WHERE, emit a `Scalar` operator that returns the cached row_count (O(1), no scan).
5. Add `PRAGMA count = approximate` for callers who want exact scan (rare).

**Acceptance:**
- `BenchmarkRazordata_SelectCount` ≤ 1 µs/op (previously 91 µs)
- Exact count for single-row INSERT/DELETE; approximate ±1% within 1000 rows of the last update
- No regression for COUNT(*) with GROUP BY, DISTINCT, or WHERE (those still scan)

---

## Phase 3 — Write Batch Buffer

**REQ001421: Write batch buffer for autocommit INSERT/UPDATE**

This attacks the INSERT 4.5x and UPDATE 8.6x gaps. The fix is to buffer writes in a per-session write batch and flush once on commit, rather than flushing every row.

### REQ001421 — Write batch buffer
**Subsystem:** WAL/WR + SQB/WT + WAL/WR/frame.go  
**Effort:** 1.5 days  
**Deps:** Phase 0 + REQ001419 (session Executor + transaction context must be stable)  

**Fix:**
1. New `WriteBatch` struct in WAL/WR — an in-memory WAL frame accumulator.
2. `WAL.Writer` accepts a batch; `Writer.Flush()` writes the whole batch as a single WAL frame.
3. INSERT/UPDATE writers (SQB/WT) append to the session's WriteBatch instead of flushing per-row.
4. On autocommit, `Session.Commit()` flushes the batch once.
5. On `BEGIN`, batch is disabled (per-row flush for transaction isolation).
6. Buffer threshold: 16 KB (tunable via `PRAGMA batch_size`).

**Acceptance:**
- 100-row INSERT: ≤ 2000 µs/op (from 4203 µs, 53% reduction)
- UPDATE: ≤ 150 µs/op (from 438 µs, 65% reduction)
- Per-row `COMMIT` semantics unchanged: each transaction flushes independently
- No regression on WAL replay correctness

---

## Phase 4 — Block-Level Range Pruning

**REQ001423: B-tree/LSM block-level stats for range query planning**

This attacks the 2.8x SELECT WHERE gap. LSM blocks already have per-column min/max derivable from SST block stats. The fix is to use these for early block pruning in SeqScan.

### REQ001423 — Block-level min/max pruning
**Subsystem:** ENG/LS + SQB/OP  
**Effort:** 0.75 day  
**Deps:** Phase 0 + Phase 2 (catalog TableStats must exist)  

**Fix:**
1. In ENG/LS SST block construction, record per-column `min`/`max` in block stats.
2. In SeqScan operator, when a range predicate is present, compare the predicate's range against each block's min/max; skip blocks where the predicate's range is disjoint from the block's column min/max.
3. Expose `TableStats` column-level min/max in the catalog (REQ001420's TableStats extended).
4. Add `TestSeqScan_BlockPruning_RangePredicate` — verify 1000-row table with 10 ranges returns only matching blocks.

**Acceptance:**
- `BenchmarkRazordata_SelectWhere` drops from 227 µs/op to ≤ 100 µs/op
- Range predicates covering 10% of blocks skip 90% of blocks (verified via trace)
- No regression for non-range predicates (equality, IN, LIKE)

---

## Phase Ordering & Dependency Graph

```
Phase 0 (Test infra fix)
  ├── REQ001415 (SYS/ST)
  ├── REQ001416 (SYS/TX)
  ├── REQ001417 (SYS/SY)
  └── REQ001418 (ENG/LS)
       │
Phase 1 (Executor + PreparedPlan) ── highest ROI
  ├── REQ001419 (SYS/SE)  ── prerequisite for everything below
  └── REQ001422 (SYS/SE)  ── builds on REQ001419
       │
Phase 2 (Table metadata) ── independent of Phase 1, but uses catalog
  └── REQ001420 (ENG/LS)
       │
Phase 3 (Write batch) ── builds on Phase 1's session Executor
  └── REQ001421 (WAL/WR)
       │
Phase 4 (Block pruning) ── builds on Phase 2's TableStats
  └── REQ001423 (ENG/LS)
```

**Parallelizable:** Phase 2 (REQ001420) and Phase 1 (REQ001419, REQ001422) can run in parallel since they touch different subsystems. Phase 3 and Phase 4 are sequential on their respective prerequisites.

---

## Verification Plan

Each phase is gated by the existing `driver/bench_compare_test.go` benchmark suite:

| Phase | Gate Benchmark | Target |
|---|---|---|
| Phase 1 | `BenchmarkRazordata_Insert` | ≤ 1500 µs |
| Phase 1 | `BenchmarkRazordata_SelectAll` (prepared) | ≤ 80 µs |
| Phase 2 | `BenchmarkRazordata_SelectCount` | ≤ 1 µs |
| Phase 3 | `BenchmarkRazordata_Insert` | ≤ 2000 µs |
| Phase 3 | `BenchmarkRazordata_Update` | ≤ 150 µs |
| Phase 4 | `BenchmarkRazordata_SelectWhere` | ≤ 100 µs |

Every phase also requires:
- `go test ./internal/... -race -count=1` passes
- `go test ./driver/... -race -count=1` passes
- SLT corpus (select1..select5) passes
- No memory leak (1000 iteration cycles, goroutine count stable)

---

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| REQ001419 (session Executor) breaks transaction isolation | Medium | High | Keep original per-query Executor as fallback; switch via feature flag |
| REQ001421 (write batch) loses WAL durability in crash | High | Critical | Batch flushes on COMMIT only; `sync` after each batch write; verify WAL replay |
| REQ001420 (row count) becomes stale under high churn | Medium | Low | Mark row_count as approximate; callers requiring exactness use `PRAGMA count = exact` |
| REQ001423 (block pruning) mis-prunes on non-range predicates | Low | High | Only activate pruning when predicate is a range (BETWEEN/AND of < and >); fall back to full scan otherwise |
| Phase 1 breaks driver Conn pool reuse | Medium | High | driver Conn pool creates session per connection; session Executor is Conn-scoped |

---

## Total Effort

| Phase | Effort | Files touched |
|---|---|---|
| Phase 0 (pre-req) | 2.5 days | 6 files (REQ001415-1418) |
| Phase 1 | 3.0 days | 5 files (REQ001419, REQ001422) |
| Phase 2 | 1.0 day | 3 files (REQ001420) |
| Phase 3 | 1.5 days | 4 files (REQ001421) |
| Phase 4 | 0.75 day | 2 files (REQ001423) |
| **Total** | **8.75 days** | **~20 files** |
