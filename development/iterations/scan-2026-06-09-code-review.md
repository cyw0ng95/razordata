# Code Review Audit — 2026-06-09

Deep audit of source code to find reasonable requirements
for features, performance, and quality. Output of a
post-iter-19 (v0.16.0) review pass.

## 1. Stub Implementations (Real Bugs, not TODOs)

| Location | Issue | Severity |
|----------|-------|----------|
| `LOG/HK/trace.go:16-19` | `traceHook.OnLog` is empty stub — no SQL query tracing | High |
| `LOG/HK/metric.go:24-27` | `metricHook.OnLog` is empty stub — counters never increment | High |
| `LOG/HK/profile.go:16-20` | `profileHook.OnLog` is empty stub — no pprof dump | Medium |
| `SQL/EX/operators_vec.go:179-180` | `errVectorizedNotImplemented` declared but never used | Low |
| `WAL/FL/fl.go:120` | `Sync()` comment says "stub" — still returns nil | Medium |

**Recommendation:** Implement the 3 HK hooks. They're the
backbone of observability (REQ000005/006/007 from iter-00
are still stubs in code, even if their interfaces are done).

## 2. Dead Code (Defined But Never Used)

| Symbol | Location | Status |
|--------|----------|--------|
| `NewHashAggregate` | `SQL/EX/hashagg.go:26` | Defined, never called by planner |
| `EncodeCommitRecord` | `TXN/VL/wal_record.go:15` | Defined, never called by Commit |
| `DecodeCommitRecord` | `TXN/VL/wal_record.go:52` | Defined, never called by Replay |
| `LX.T_BLOB`/`T_VARCHAR` etc | (general) | Multiple type token branches in eval |

**Implications:**
- `NewHashAggregate` is the better algorithm for GROUP BY
  but the planner always picks `NewAggregate` (line 271).
  Should use HashAggregate for >1000 row datasets.
- `EncodeCommitRecord` is the WAL format for commit
  records but the commit protocol doesn't write to WAL.
  This is REQ000171.
- WAL/RP doesn't decode commit records on recovery.

## 3. Hot Path Performance Opportunities

### 3.1 Skiplist Insert (ENG/LS/skiplist.go:51-52)

```go
predecessors := make([]*node, lvl)  // alloc per insert
successors := make([]*node, lvl)    // alloc per insert
```

2 allocations per insert. Use `sync.Pool` for reusable
slices, or per-thread scratch arrays.

**Estimated gain:** 30-50% on insert throughput.
**Effort:** S (1-2 days), ~100 LOC.

### 3.2 WAL Append Serialization (WAL/WR/wr.go:171-172)

```go
w.mu.Lock()    // global write lock
defer w.mu.Unlock()
```

Single mutex serializes ALL WAL appends. Real-world databases
use group commit (already partially implemented in WAL/FL)
or per-segment sharding.

**Estimated gain:** 2-4x concurrent write throughput.
**Effort:** L (3-5 days), ~300 LOC.
**Note:** This depends on REQ000176/184 (group commit)
which is shipped; need to wire into Append path.

### 3.3 Buffer Pool Single Mutex (MEM/BF/bf.go:160)

```go
b.ht.mu.RLock()  // single hash table mutex
```

All hash table operations contend on a single mutex.
Sharded mutexes (per-bucket or per-segment) would reduce
contention.

**Estimated gain:** 2-3x under high concurrency.
**Effort:** M (2-3 days), ~200 LOC.

### 3.4 Interface{} in Hot Paths

`SQL/EX/eval.go` uses `interface{}` for return values and
parameters. Each call boxes/unboxes values.

- `Eval()` returns `interface{}` — 28 occurrences
- `interface{}` allocation in eval path: ~10/eval call

**Mitigation:** Use generics (Go 1.18+) or specialized
typed evaluators.

**Effort:** M, requires Go 1.18+.

## 4. SQL Feature Gaps (Design vs Implementation)

| Feature | Design | Implementation | Gap |
|---------|--------|----------------|-----|
| GROUP BY | SQL.md:362 | PS parser ✅, Aggregate used (not HashAggregate) | Use HashAggregate |
| OUTER JOIN (LEFT/RIGHT/FULL) | SQL.md:390 | PS parser ✅, planner skips non-INNER (planner.go:234) | Implement LEFT JOIN |
| CASE expression | SQL.md:267 | EX eval ✅, PS parser `parseCaseExpr` 0% tested | Add parser tests |
| EXISTS subquery | SQL.md | EX eval ✅, PS parser `parseExists` 0% tested | Add parser tests |
| CREATE INDEX | ENG.md:104 | None | New REQ |
| ALTER TABLE | SQL.md | None | New REQ |
| FOREIGN KEY | SQL.md | None | New REQ |
| EXPLAIN | SQL.md | `explain.go` only for cost, not SQL syntax | New REQ |

**Effort estimate:**
- CASE/EXISTS parser tests: 1-2 days
- OUTER JOIN: M (3-5 days)
- GROUP BY HashAggregate: S (1-2 days)
- CREATE INDEX: XL (5-7 days)
- ALTER TABLE: XL
- FOREIGN KEY: L (3-5 days)

## 5. Test Coverage Gaps (Current State)

| Package | Coverage | Target | Gap |
|---------|----------|--------|-----|
| **SQL/PL** | 30.6% | 80% | **+49% gap (highest priority)** |
| MEM/BF | 66.9% | 80% | +13% gap |
| LOG/LG | 70.8% | 80% | +9% gap |
| FIL/LF | 73.7% | 80% | +6% gap |
| WAL/RP | 75.2% | 80% | +5% gap |
| SYS/SY | 76.4% | 80% | +4% gap |
| ENG/LS | 81.9% | 80% | ✅ met |
| SQL/PS | 67.9% | 80% | +12% gap |
| TXN/SN | 82.4% | 80% | ✅ met |
| TXN/MV | 88.9% | 80% | ✅ met |
| TXN/LC | 98.1% | 80% | ✅ met |
| TXN/VL | 93.9% | 80% | ✅ met |
| SQL/EX | 68.8% | 80% | +11% gap |
| WAL/FL | 92.2% | 80% | ✅ met |
| WAL/WR | 80.8% | 80% | ✅ met |
| SQL/LX | 89.5% | 80% | ✅ met |
| MEM/SP | 96.4% | 80% | ✅ met |
| FIL/FS | 74.7% | 80% | +5% gap |
| FIL/MF | 80.6% | 80% | ✅ met |

**Priority for coverage uplift:**
1. SQL/PL (30.6% → 80%) — M effort, ~500 LOC tests
2. SQL/PS (67.9% → 80%) — M effort, ~200 LOC tests
3. SQL/EX (68.8% → 80%) — L effort, ~600 LOC tests
4. MEM/BF (66.9% → 80%) — M effort
5. LOG/LG (70.8% → 80%) — S effort

## 6. Missing Benchmarks

| Package | Benchmarks | Issue |
|---------|------------|-------|
| FIL/LF | 0 | No benchmarks for segment manager |
| LOG/HK | 0 | No hook dispatch benchmarks |
| SQL/PS | 0 | No parser throughput benchmarks |
| SQL/PL | 0 | No planner cost benchmarks |
| SQL/EX | many | Some missing: Aggregate, Join, Sort |

**Recommendation:** Add ~10-15 benchmarks for hot paths
per AGENTS.md "at least one Benchmark per storage component"
rule.

## 7. Missing Observability (HK Hooks)

The HK package has stubs for trace/metric/profile hooks.
The `metricHook` struct even has the counter fields
(`queryCount`, `rowsReturned`, `bytesRead`, `bytesWritten`)
but `OnLog` doesn't increment them.

**REQ items (already in TBD, partially):**
- REQ000005 (TraceHook for SQL) — stub needs implementation
- REQ000006 (MetricHook for throughput/latency) — stub needs impl
- REQ000007 (ProfileHook for pprof) — stub needs impl
- REQ000101 (Prometheus metrics endpoint) — needs impl

## 8. Concurrency / Hot Path Lock Patterns

| Location | Pattern | Recommendation |
|----------|---------|----------------|
| `MEM/BF/bf.go:160` | Single RWMutex | Shard by blockID hash |
| `WAL/WR/wr.go:171` | Single Mutex | Per-segment locks |
| `SQL/EX/parallel.go` | OK (uses pool) | No change |
| `SQL/EX/pipeline.go` | OK (bounded chans) | No change |
| `TXN/LC/hazard.go` | OK (lock-free CAS) | No change |
| `TXN/LC/epoch.go` | OK (sync.Map) | No change |
| `TXN/MV/arena.go` | sync.Pool | No change |
| `ENG/LS/skiplist.go` | Lock-free | No change |

## 9. Error Handling Patterns

- All packages use `errors.New`/`fmt.Errorf` consistently
- Wrap chain: I/O → structural → API (per AGENTS.md)
- Some errors return `nil, nil` for partial-success paths
  (e.g., `WAL/FL/Sync()`) — these are documented as stubs

## 10. Documented Deviations

Some REQs are documented as not yet implemented in comments:

- `WAL/FL/fl.go:120` — "Sync is a stub"
- `LOG/HK/trace.go:18` — "TODO: emit structured trace"
- `LOG/HK/metric.go:25` — "TODO: parse log events"
- `LOG/HK/profile.go:17` — "TODO: trigger profile dump"
- `SQL/EX/operators_vec.go:125` — "TODO: return concrete type"
- `SQL/EX/operators_vec.go:179` — "vectorized not yet implemented"

## Summary of New REQs Discovered

### REQ000193: Implement MetricHook counters (High, M, ~150 LOC)
Wire `OnLog` to increment `queryCount`, `rowsReturned`, etc.
Already a stub; the fields are defined.

### REQ000194: Implement TraceHook (High, M, ~200 LOC)
Trace SQL queries (start/end with timing). Currently a no-op.

### REQ000195: Implement ProfileHook (Medium, M, ~150 LOC)
Trigger pprof dump on Error level events.

### REQ000196: HashAggregate in planner (High, S, ~80 LOC)
Use `NewHashAggregate` instead of `NewAggregate` for
large datasets (>1000 rows). Currently never called.

### REQ000197: OUTER JOIN executor (Critical, M, ~400 LOC)
Planner skips non-INNER joins. Implement LEFT JOIN executor.

### REQ000198: Skiplist sync.Pool for scratch arrays (High, S, ~100 LOC)
Eliminate 2 allocations per insert via reusable slices.

### REQ000199: Sharded BF mutex (Medium, M, ~200 LOC)
Reduce hash table contention via per-shard locks.

### REQ000200: WAL per-segment locks (Medium, L, ~300 LOC)
Replace global write mutex with per-segment locks.
(Depends on group commit being wired up.)

### REQ000201: SQL/PL coverage uplift to 80% (High, M, ~500 LOC)
Add tests for cost model, index selection, plan caching.

### REQ000202: CASE/EXISTS parser tests (Medium, S, ~150 LOC)
Both are 0% covered.

### REQ000203: Missing benchmarks (Medium, M, ~200 LOC)
Add benchmarks for FIL/LF, LOG/HK, SQL/PS, SQL/PL.

### REQ000204: CREATE INDEX (Critical, XL, ~1500 LOC)
New SQL feature; required for non-PK lookups.

### REQ000205: EXPLAIN SQL syntax (Medium, M, ~300 LOC)
EXPLAIN currently only computes cost internally.

## Recommended v0.17.0 Scope (Updated)

Based on this audit, v0.17.0 should focus on:

### Block A: Hot Path Performance (S effort, ~600 LOC)
- **REQ000198** — Skiplist sync.Pool (~100 LOC)
- **REQ000199** — Sharded BF mutex (~200 LOC)
- **REQ000196** — HashAggregate in planner (~80 LOC)
- **REQ000193** — MetricHook counters (~150 LOC)
- Coverage: SQL/PL 30% → 60% (~70 LOC)

### Block B: SQL Feature Completeness (M effort, ~700 LOC)
- **REQ000197** — OUTER JOIN executor (~400 LOC)
- **REQ000202** — CASE/EXISTS parser tests (~150 LOC)
- **REQ000205** — EXPLAIN SQL syntax (~150 LOC)
- Coverage: SQL/PS 67.9% → 80% (~100 LOC tests)

### Block C: Observability Hooks (M effort, ~500 LOC)
- **REQ000194** — TraceHook SQL tracing (~200 LOC)
- **REQ000195** — ProfileHook pprof (~150 LOC)
- **REQ000203** — Missing benchmarks (~200 LOC)

### Block D: TXN Correctness (L effort, ~700 LOC, deferred)
- REQ000147, REQ000171, REQ000164, REQ000175 (existing TBD)
- EncodeCommitRecord wiring

**Total v0.17.0:** ~1,800 LOC + 700 (TXN) = 2,500 LOC, 12-15 days.

## Recommended v0.18.0 Scope

- **REQ000204** — CREATE INDEX (XL, ~1500 LOC)
- **REQ000200** — WAL per-segment locks (L, ~300 LOC)
- **REQ000201** — SQL/PL coverage 60% → 80% (M, ~400 LOC)
- Network server scaffolding

## Out-of-Scope (Defer to v0.19.0+)

- ALTER TABLE (XL)
- FOREIGN KEY (L)
- Prometheus /metrics endpoint (M)
- Backup/restore (M)
- Generational arena (L)
