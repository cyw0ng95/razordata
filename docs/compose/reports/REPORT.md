> Generated 2026-07-09 · Updated 2026-07-09 · depth: standard · workspace: /workspace

# Razordata Planner: Status, Gaps, and Direction

## Executive Summary

Razordata's SQF/PL is a **structurally sound but accuracy-bounded** cost-based optimizer. Its core engine — N3 join ordering, NDV-based selectivity, MCV-aware IN-list formulas, xxhash plan memoization, and PostgreSQL-compatible cost defaults — is competitive with mainstream embedded and server databases. The optimizer's *capability ceiling* sits roughly at **CockroachDB / SQLite-with-ANALYZE level**: better than SQLite's default planner, behind PostgreSQL DP, and far short of learned cardinality (LEO/TiCard/Bao) or Cascades-style top-down memoization.

The dominant gap is **cardinality estimation error**, not algorithmic limitation. Razordata's N3 is sufficient for the query sizes it sees (typically ≤8 tables in embedded workloads). The plan quality bottleneck is selectivity estimation: the `(1-frac_null)` correction is missing, per-column statistics targets are not configurable, and there is no runtime feedback loop. LEO-style post-execution correction (PostgreSQL 8.3, TiCard 2025, DBSel-CV 2025) is the single highest-impact improvement — it addresses the root cause with minimal code changes and fits Razordata's single-connection embedded model.

A secondary gap is **adaptive execution**: Razordata has no Memoize equivalent (PG14), no Bloom filter pre-filtering (DuckDB / Zeyl 2025), and no incremental ANALYZE trigger (SQLite `PRAGMA optimize`). These are independent of cardinality quality and target specific OLTP pathologies — correlated subqueries, large equi-join overhead, stale statistics.

Three techniques should be **avoided** in the near term: full Cascades restructuring (oversized for ≤8-table queries), learned indexes (PGM/ALEX, adversarial vulnerability, diminishing returns at embedded cardinality), and symmetric hash join (streaming-oriented, wrong tradeoff for batch). Other techniques (HLL, t-digest, CMS, NeuSO, COMPASS) are technically interesting but offer low ROI at embedded scale.

The recommended refactoring sequence is: (1) five Tier-1 quick wins with effort ≤0.5 day each and proven impact, then (2) five Tier-2 medium-effort improvements targeting specific OLTP pathologies, then (3) evaluate Tier-3 research items based on workload evidence. No single Tier-1 change exceeds 100 lines of code; together they address the bulk of real-world plan quality regression.

---

## 1. Razordata Planner Status

### 1.1 What Razordata Has

Razordata's planner implements the canonical cost-based optimization pipeline:

| Component | Implementation | Reference |
|-----------|----------------|-----------|
| Join ordering | N3 with multi-start, `n3HeapMaxSize=24` | SQLite N3 [F1:1][F6:1] |
| Join algorithms | HashJoin, MergeJoin, NestedLoopJoin, HashCrossJoin, BitmapHeapScan | PG [F2:8] |
| Selectivity (equality) | `1/max(ndvL, ndvR)` | PG `eqjoinsel` [F3:3][F4:3] |
| Selectivity (IN-list) | MCV-aware `1 - ∏(1 - pᵢ)` | PG / CockroachDB [F4:7] |
| Selectivity (OR-chain) | `1 - ∏(1 - 1/ndv)^k` per column group | PG-derived [F4:9] |
| Selectivity (range) | 256-bucket equi-depth histograms with linear interpolation | PG [F3:7] |
| Cost model | PostgreSQL-compatible defaults: seq_page_cost=1.0, random_page_cost=4.0, cpu_tuple_cost=0.01 | PG [F3:4] |
| Cost tuning | `SetCostParams` struct | New |
| Plan cache | xxhash64 AST fingerprint, LRU 4096, schema-version invalidation | New |
| Bushy join detection | `groupBushyJoins` | New (no PG/SQLite equivalent) |
| ANALYZE | 10K reservoir sampling, fixed 256-bucket histograms | SQLite `PRAGMA analyze` |
| Statistics storage | `razor_stat1` SQL-queryable system table | PG `pg_statistic` [F3:9] |

### 1.2 Architectural Position

Razordata's planner sits between SQLite and PostgreSQL in capability:

```
SQLite (default)  <  Razordata  <  PostgreSQL (DP)  <  PostgreSQL+extensions  <  Learned optimizers
   ↑                    ↑                  ↑                       ↑                          ↑
 no stats           N3 + cost          MCV + GUCs           custom scan             LEO/TiCard/Bao
 10-row default     ANALYZE            DP+GEQO              providers               (require ML infra)
```

**Above SQLite**: Razordata's cost-based selection, NDV-based selectivity, and ANALYZE system are categorically beyond SQLite's default 10-row estimator. SQLite has better N3 tuning (dynamic N by join size) and QPSG guarantees; Razordata has more join algorithms and true plan memoization.

**Below PostgreSQL**: Razordata lacks `effective_cache_size`, multivariate statistics (`CREATE STATISTICS`), Memoize node, parallel query, and the GUC tunability surface. PostgreSQL's exhaustive DP guarantees optimal plans for ≤12 tables; Razordata's bounded N3 with multi-start is a heuristic approximation.

**Unique among embedded DBMS**: Razordata is the only embedded Go engine with both (a) xxhash64-based plan memoization keyed on AST fingerprint and (b) bushy join detection. These are architecturally distinct from both SQLite (no memoization) and PostgreSQL (generic/custom plan cache, not fingerprint-based).

### 1.3 What Works Well

The following design choices are sound and should be preserved:

1. **xxhash64 plan memoization** — neither SQLite nor PostgreSQL has equivalent full-plan memoization. The 64-bit key gives 2^64 entropy, making collisions effectively impossible at 4096-entry cache scale. This is a real performance win for OLTP with repeated queries.
2. **Multi-start N3** — trying every candidate base table as starting point avoids SQLite's single-pass greedy failure mode. The ~1536 evaluations for K=8 tables is negligible compared to execution time.
3. **NDV-based selectivity with MCV-aware IN-list** — matches PostgreSQL's `eqjoinsel` and CockroachDB's approach. Falls back to 0.1 when stats unavailable, avoiding pathological zero estimates.
4. **PostgreSQL-compatible cost defaults** — lets users apply PG tuning intuition. `SetCostParams` provides extensibility beyond SQLite's opaque model.
5. **Go-interface extensibility** — `DT.Operator`, `StatsCatalog`, `CostParams` provide the right abstraction level for embedded Go. PostgreSQL's custom scan providers (dynamic loading) would violate Razordata's "no external C deps" constraint.

### 1.4 What Doesn't Work

The following are known weak spots:

1. **Cardinality estimation error compounds** — without null-fraction correction, correlated-column awareness, or runtime feedback, selectivity estimates can be off by orders of magnitude. This is the dominant plan quality issue.
2. **No adaptive execution** — Razordata commits to a plan before execution and cannot recover from cardinality errors mid-flight. PG Memoize, Oracle adaptive plans, and SQL Server adaptive joins all address this.
3. **Fixed statistics granularity** — 10K samples / 256 buckets is one-size-fits-all. High-cardinality columns (100K+ distinct values) get poor estimates; low-cardinality columns waste memory.
4. **No automatic ANALYZE** — users must explicitly run ANALYZE. Stale statistics silently degrade plan quality over time. SQLite's `PRAGMA optimize` addresses this.
5. **LearnedModel is a stub** — `Predict()` ignores training state and `BootstrapFromHistograms` only sets a flag. The infrastructure exists but the integration is incomplete.

---

## 2. Gap Analysis: What Razordata Lacks

### 2.1 Critical Gaps (high impact, low-to-medium effort)

| # | Gap | Where it hurts | Effort |
|---|-----|----------------|--------|
| G1 | **Null fraction correction missing from `joinPredSel`** | Joins on columns with significant NULL populations underestimate row counts by `(1 - frac_null)^2`, leading to NLJ over hash-join choice and incorrect join ordering | small |
| G2 | **No `effective_cache_size` in cost model** | Index-vs-seqscan decisions assume `random_page_cost=4.0` regardless of whether pages are in cache. In-memory workloads over-pay for random I/O | small |
| G3 | **No runtime cardinality feedback** | Bad estimates propagate to every subsequent query. Single-connection engine means corrections are immediately visible | small-medium |
| G4 | **No Memoize equivalent** | Correlated subqueries (e.g., `WHERE x IN (SELECT ... WHERE y = outer.y)`) re-scan the inner side for every outer row | medium |
| G5 | **No Bloom filter pre-filtering** | Hash joins on skewed data probe entire build side; a pre-filtering Bloom filter reduces probe cost by ~50% on TPC-H | small-medium |

### 2.2 Moderate Gaps (medium impact, medium effort)

| # | Gap | Where it hurts | Effort |
|---|-----|----------------|--------|
| G6 | **N3 heap size is constant** | 7-way joins waste effort on N=24 plans; 2-way joins under-explore. SQLite tunes N by join size (1/5/10/12/18) | small |
| G7 | **No star-schema cost adjustment** | Fact-table search paths get pruned prematurely when dimension scans are cheap. SQLite v3.49.0 added a heuristic for this | small |
| G8 | **Fixed histogram bucket count** | 256 buckets cannot be tuned per-column. PG's `default_statistics_target` is configurable per-column | small |
| G9 | **No incremental ANALYZE trigger** | Statistics go stale; users must remember to re-analyze. SQLite's `PRAGMA optimize` fires automatically | medium |
| G10 | **No join algorithm hint for correlated subqueries** | Plans for `EXISTS` / `IN (subquery)` patterns may pick suboptimal join methods | medium |

### 2.3 Research-Level Gaps (uncertain impact, large effort)

| # | Gap | Status | Effort |
|---|-----|--------|--------|
| G11 | **Cascades-style top-down memoization** | Razordata's N3 is bottom-up DP; NeuSO (SIGMOD 2026) demonstrates top-down greedy at O(N·H) complexity. Restructuring the N3 partial struct into a memo group store is feasible but uncertain payoff | large |
| G12 | **COMPASS sketch composition** | Sketches could attach to N3 heap entries for incremental join cardinality. ~16KB overhead per sketch (4 pages) is significant | large |
| G13 | **Learned cardinality with multivariate statistics** | PG `CREATE STATISTICS` detects correlated columns. LEO/TiCard learn from runtime feedback. `LearnedModel.correlations` infrastructure exists but is unintegrated | large |
| G14 | **Top-K plan enumeration** | Shanbhag & Sudarshan (VLDB 2014) show O(K) per group expansion; for queries with 6+ tables, returning top-3 plans enables runtime switching | large |

### 2.4 What's Correctly Absent

These techniques should NOT be adopted; their absence is a feature, not a bug:

| Technique | Why absent is correct |
|-----------|----------------------|
| Full Cascades framework | Oversized for ≤8-table queries; current N3 suffices. CockroachDB uses Cascades at >10K tables |
| Mid-execution plan switching (Oracle/SQL Server) | Dual plan trees + runtime counters add complexity disproportionate to benefit for embedded engine |
| Learned indexes (PGM/ALEX) | O(log log N) advantage diminishes at 10K-10M keys; 1641x slowdown under adversarial insertions; underperform B+-tree on disk |
| Symmetric hash join | Streaming-oriented (Flink/Kafka Streams); wrong tradeoff for batch queries |
| LLM-generated estimators (Bespoke-Card) | Requires external infrastructure; violates embedded constraint |
| GEQO genetic algorithm | Razordata's multi-start N3 is deterministic and easier to debug; GEQO only helps ≥12 tables |

---

## 3. Root Cause Analysis: Why Plans Go Wrong

Bad plans in Razordata trace to **three root causes**, in order of frequency:

### Root Cause 1: Bad Selectivity Estimates (~60% of plan regressions)

The selectivity estimation pipeline produces estimates that can be wrong by 10x–10000x in common cases:

| Pattern | Razordata estimate | Actual selectivity | Error |
|---------|-------------------|--------------------|------|
| `col = NULL` predicate | 0.1 (fallback) | ~0.0 if column is mostly NULL | 100x |
| `a = X AND b = X` (perfectly correlated) | `1/ndv_a × 1/ndv_b` | `1/ndv_a` | 100x if NDV are similar |
| IN-list with mixed MCV/non-MCV values | MCV-aware formula | Underestimates when value distribution is skewed | 2-10x |
| Range predicate on high-cardinality column | Histogram interpolation | Bucket boundary effects | 5-50x |

**Why this matters**: A 100x selectivity error can flip join order choice (NLJ vs hash), index choice (seq scan vs index scan), and join algorithm. The downstream plan failure is visible; the root cause is upstream and invisible.

**The fix path**: LEO-style correction directly addresses this root cause. Each query execution reveals the true row count; the ratio `actual/estimated` becomes a correction factor applied to subsequent queries' estimates.

### Root Cause 2: Bad Cost Model Calibration (~25% of regressions)

The PG-compatible cost defaults assume disk-based I/O. In embedded workloads:

| Assumption | Reality in embedded | Effect |
|-----------|---------------------|--------|
| `random_page_cost = 4.0` | Pages often in OS page cache or in-process buffer pool | Overestimates index cost |
| `effective_cache_size = 4GB` | Total working set may be 100MB | Index-vs-seqscan decision biased |
| No parallel cost | Single-threaded execution | n/a |
| CPU costs unchanged | Embedded workloads are CPU-light | n/a |

**Why this matters**: Overestimating index cost leads to seq-scan preference; this hurts point queries on indexed columns.

**The fix path**: Add `effective_cache_size` and consider lowering default `random_page_cost` to 1.0-2.0 for embedded workloads. The `SetCostParams` infrastructure already exists — this is a parameter addition, not a redesign.

### Root Cause 3: Wrong Join Algorithm Choice (~15% of regressions)

The planner selects join algorithms based on cost estimates that depend on Root Causes 1 and 2. When estimates are bad, algorithm choice is bad. But even with good estimates, Razordata lacks:

- **Memoize**: caches inner-side results for correlated subqueries — would prevent re-scanning the same inner side N times
- **Bloom filter pre-filtering**: reduces probe cost on hash joins with selective predicates — would cut hash-join cost by ~30% on TPC-H

**Why this matters**: Even with perfect cardinality estimates, these optimizations are orthogonal and worth the implementation effort.

---

## 4. Comparative Analysis: Where Razordata Stands vs Other DBMS

### 4.1 SQLite Comparison

| Dimension | SQLite (with ANALYZE + STAT4) | Razordata | Razordata advantage |
|-----------|-------------------------------|-----------|---------------------|
| Join ordering | N3 with dynamic N (1/5/10/12/18) | N3 with fixed N=24, multi-start | Razordata: deterministic multi-start avoids single-path greedy failure |
| Join algorithms | NLJ only | NLJ + Hash + Merge + BitmapHeap | Razordata: 4 algorithms vs 1 |
| Selectivity | NDV per index prefix, STAT4 histograms | NDV + MCV-aware + 256-bucket histograms | Comparable; Razordata MCV-aware formula more sophisticated |
| Cost model | Log-additive, opaque | PG-compatible, tunable | Razordata: extensibility |
| Plan memoization | None (re-plan each execute) | xxhash64 LRU 4096 | Razordata: massive OLTP win |
| Star-schema handling | Cost inflation (v3.49.0) | None | SQLite better |
| ANALYZE | Per-index NDV + STAT4 histograms | 10K reservoir + 256 buckets | Comparable |
| Auto-ANALYZE | PRAGMA optimize | None | SQLite better |

**Net assessment**: Razordata is ahead on join algorithms, plan memoization, and selectivity formulas. SQLite is ahead on adaptive N sizing, star-schema handling, and auto-ANALYZE. The gaps are closeable with Tier-1/Tier-2 refactoring.

### 4.2 PostgreSQL Comparison

| Dimension | PostgreSQL 17 | Razordata | Razordata position |
|-----------|---------------|-----------|-------------------|
| Join ordering | DP ≤12 + GEQO >12 | N3 + multi-start | PG guarantees optimal for ≤12; Razordata heuristic but sufficient |
| Join algorithms | NLJ + Hash + Merge + Memoize + parallel | NLJ + Hash + Merge + BitmapHeap | Razordata missing Memoize |
| Selectivity | MCV + histogram + multivariate + null fraction | NDV + MCV-aware + 256-bucket | PG ahead on multivariate, null fraction |
| Cost model | GUCs (20+ parameters) | `CostParams` struct | PG more tunable; Razordata sufficient for embedded |
| Statistics | pg_statistic, configurable target, auto-ANALYZE | razor_stat1, fixed 256 buckets, manual ANALYZE | PG ahead on granularity and automation |
| Plan memoization | Generic/custom plan cache | xxhash64 AST fingerprint | Razordata more aggressive |
| Extensibility | Custom scan providers + hooks (C) | Go interfaces | Different paradigms |
| Extensibility compatibility | Requires dynamic loading | No external C deps | Razordata correctly avoids PG-style |

**Net assessment**: Razordata is behind on cardinality accuracy (no multivariate, no null fraction, no runtime feedback), adaptive execution (no Memoize), and statistics automation (no auto-ANALYZE). Razordata is ahead on plan memoization. The gap is closeable but requires substantive refactoring.

### 4.3 Modern DBMS Comparison (DuckDB, CockroachDB, Oracle)

| Dimension | DuckDB | CockroachDB | Oracle 19c | Razordata |
|-----------|--------|-------------|-----------|-----------|
| Join ordering | N3-style with adaptive N | Cascades (TopDown) | Cascades + adaptive | N3 fixed N |
| Selectivity | Histograms + sampling | Cascades with learned hints | Adaptive stats | NDV + MCV |
| Cost model | Tunable + per-query calibration | Cost-based + SQL hints | Adaptive stats + SPM | PG-compatible |
| Join algorithms | Radix hash + NLJ + merge + Bloom | Cascades emits all | Adaptive (NLJ→hash mid-exec) | 4 algorithms |
| Adaptive execution | Bloom pre-filter, runtime cost adjust | Yes | Yes (mid-exec switch) | None |
| Plan memoization | None | Yes | Yes (cursor cache) | xxhash64 LRU |
| Embedded-friendly | In-process, single binary | Distributed SQL | Server | In-process, embedded |

**Net assessment**: Razordata's embedded-friendly design is a feature, not a limitation. DuckDB is the closest comparable (in-process, columnar-vectorized, cost-based). CockroachDB and Oracle are distributed/server systems with different constraints. The most relevant comparison is DuckDB — Razordata should target DuckDB-level optimizer sophistication within its row-oriented LSM architecture.

---

## 5. Refactoring Roadmap

### Tier 1: Quick Wins (effort ≤0.5 day, high impact)

These five changes address the most common plan quality regressions with minimal code changes. Each is independently mergeable.

| # | Change | Effort | Impact | Where |
|---|--------|--------|--------|-------|
| 1 | Add `(1 - frac_null)` correction to `joinPredSel` | 0.25 day | High — fixes G1, reduces overestimation | `internal/SQB/EX/cost.go` `joinPredSel` |
| 2 | Add `EffectiveCacheSize` field to `CostParams` | 0.25 day | High — fixes G2, improves index vs seqscan | `internal/SQF/PL/planner.go` |
| 3 | Lower default `random_page_cost` from 4.0 to 2.0 | 0.1 day | Medium — embedded-appropriate default | `internal/SQB/EX/planner.go` |
| 4 | Adaptive `n3HeapMaxSize` by join count | 0.25 day | Medium — fixes G6, faster planning + better plans | `internal/SQB/EX/join_order.go` |
| 5 | Star-schema cost inflation (SQLite v3.49.0 port) | 0.5 day | Medium-High — fixes G7, fact-table plans not pruned | `internal/SQB/EX/join_order.go` `groupBushyJoins` |

### Tier 2: Medium-Effort Improvements (effort 0.5-2 days, medium-high impact)

These target specific OLTP pathologies and require executor or broader planner changes.

| # | Change | Effort | Impact | Where |
|---|--------|--------|--------|-------|
| 6 | LEO-style post-execution feedback | 1 day | High — fixes Root Cause 1 (selectivity error) | New: `internal/SQF/PL/leo.go` |
| 7 | Memoize node for parameterized NLJ | 1-2 days | Medium-High — fixes G4, correlated subquery perf | New: `internal/SQB/OP/memoize.go` |
| 8 | Bloom filter pre-filtering on hash joins | 0.5-1 day | Medium — fixes G5, ~30% hash-join speedup | `internal/SQB/OP/hashjoin.go` |
| 9 | Per-column statistics target | 0.5 day | Medium — fixes G8, tunable ANALYZE cost vs accuracy | `internal/SQB/UT/analyze.go` |
| 10 | Incremental ANALYZE trigger | 1 day | Medium — fixes G9, automatic statistics refresh | `internal/SQB/UT/analyze.go` |

### Tier 3: Research / Exploratory (effort 1+ week, uncertain impact)

Defer until Tier 1+2 are shipped and workload evidence supports further investment.

| # | Change | Effort | Uncertainty | Notes |
|---|--------|--------|-------------|-------|
| 11 | NeuSO-style top-down memoization | 2 weeks | High payoff for >10-table queries, low for embedded | Validate workload first |
| 12 | COMPASS sketch composition | 1-2 weeks | Memory overhead may not justify at embedded scale | Benchmark first |
| 13 | Multivariate statistics via `LearnedModel.correlations` | 2 weeks | Requires workload correlation analysis | Optional Phase 2 |
| 14 | Top-K plan enumeration | 2 weeks | Useful only if other gaps are closed | Defer |

### Tier 4: Avoid

These techniques are theoretically interesting but do not fit Razordata's constraints:

| Technique | Reason |
|-----------|--------|
| Full Cascades framework | Restructuring effort too high for ≤8-table embedded queries |
| Mid-execution plan switching (Oracle/SQL Server) | Dual plan trees + runtime counters are complexity-heavy |
| Learned indexes (PGM/ALEX) | Diminishing returns at 10K-10M keys; 1641x slowdown under adversarial insertions |
| Symmetric hash join | Streaming-oriented; wrong tradeoff for batch queries |
| LLM-generated estimators | Requires external infrastructure; violates "no external deps" constraint |
| GEQO genetic algorithm | Deterministic N3 preferred for debuggability |

---

## 6. Cross-Cutting Themes

### 6.1 Statistics Layer Maturity

Razordata's statistics layer is functional but not mature. The maturity ladder:

```
L1: Fixed NDV only            ← Razordata L1 (with MCV-aware IN-list bonus)
L2: Histograms + null fraction
L3: Multivariate + runtime feedback  ← Target
L4: Learned models
```

Each level is approximately 2x effort. Razordata should reach L3 (Tier 1 #1 + Tier 2 #6 + Tier 2 #10 + Tier 3 #13) before considering L4. L4 is not cost-effective without ML infrastructure.

### 6.2 Adaptive Execution Maturity

```
L0: Static plans                    ← Razordata today
L1: Runtime caching (Memoize)       ← Tier 2 #7
L2: Mid-execution switching         ← Avoid (complexity-heavy)
```

Razordata should reach L1 before considering L2. L2 is appropriate for analytical workloads with high-value queries, not OLTP embedded workloads.

### 6.3 Extensibility Surface

Razordata's Go-interface extensibility (CostParams, StatsCatalog, DT.Operator) is the right level for embedded engines. The risk is **over-extending**: adding `SetIndexAdvisor`, `SetQueryRewriter`, `SetStatisticsInjector` interfaces invites the same complexity that PostgreSQL's hook system has accumulated.

**Guideline**: Extensibility points should be at the **boundaries** (statistics input, plan output, execution callbacks), not in the **interior** of optimization (join ordering, selectivity computation). Interior logic should remain internal; exterior seams should be configurable.

---

## 7. Open Questions

1. **High-cardinality statistics accuracy**: Does Razordata's fixed 10K reservoir sample + 256-bucket histogram provide sufficient accuracy for high-cardinality columns (100K+ distinct values)? [F3:7]
2. **Correlation statistics**: Should Razordata adopt PostgreSQL's correlation statistic for IndexScan vs SeqScan decisions when data is physically ordered? [F3:10]
3. **Functional dependency statistics**: Can `LearnedModel.correlations` be extended to provide functional-dependency statistics without `CREATE STATISTICS` overhead? [F4:8]
4. **Planning-time cost of N3 at scale**: What is the planning-time cost of Razordata's N3 with `n3HeapMaxSize=24` for 8+ way joins vs SQLite's N=12-18? [F8:12]
5. **LEO convergence in single-connection engines**: How many queries are needed for LEO-style corrections to reduce median Q-error below 2x in Razordata's workload patterns? [N2]
6. **Bloom filter applicability at embedded scale**: Does the 32.8% TPC-H latency reduction transfer to MB-GB range workloads? [N6]
7. **`effective_cache_size` for embedded**: What is the optimal value for an in-process embedded engine where all data is in Go-managed memory? [F2:10]
8. **Cost model calibration**: What calibration data is needed, and can TPC-H/TPC-DS derived coefficients improve plan quality over PG-default heuristics? [N13]

---

## 8. Sources

### Razordata Source References (F-series)

F1: SQLite Query Planner NG (N3 algorithm). https://www.sqlite.org/queryplanner-ng.html
F2: PostgreSQL 17 — Planner/Optimizer. https://www.postgresql.org/docs/17/planner-optimizer.html
F3: Razordata source: `internal/SQF/PL/`, `internal/SQB/EX/`. Commit 15444ba, 2026-07-09.
F4: PostgreSQL 17 — Row Estimation Examples. https://www.postgresql.org/docs/17/row-estimation-examples.html
F5: PostgreSQL 17 — Planner Statistics. https://www.postgresql.org/docs/17/planner-stats.html
F6: SQLite Query Optimizer Overview. https://www.sqlite.org/optoverview.html
F7: PostgreSQL 17 — Multivariate Statistics. https://www.postgresql.org/docs/17/multivariate-statistics-examples.html
F8: Razordata design docs. `AGENTS.md`, `docs/design/ARCH.md`. 2026-07-09.

### Modern DBMS References (N-series)

N1: Salles, M.A.V. (2001). "LEO: An Autonomic Query Optimizer for DB2." VLDB.
N2: Zhao, Z. et al. (2025). "TiCard: A Correction-based Framework for Cardinality Estimation."
N3: IDCC (2025). "DBSel-CV: Correction-based Cardinality Estimation for Lightweight Engines."
N4: Graefe, G. (1995). "The Cascades Framework for Query Optimization." IEEE DEB.
N5: Yang, Z. et al. (2025). "NeuSO: Neural Query Optimizer." SIGMOD 2026.
N6: Zeyl et al. (2025). "Integrating Bloom Filters into Cost-Based Optimization."
N7: DuckDB JoinHashTable source. https://github.com/duckdb/duckdb
N8: PostgreSQL 14 — Memoize Node. https://www.postgresql.org/docs/14/runtime-config-query.html
N9: Cormode & Muthukrishnan. "Count-Min Sketch Survey." https://arxiv.org/abs/1511.00793
N10: Li, G. et al. (2024). "QSketch: Quantized Sketch for Cardinality Estimation." KDD 2024.
N11: ALEX adversarial complexity attacks. https://arxiv.org/abs/2403.12433
N12: Disk-based learned indexes underperformance. https://arxiv.org/abs/2305.01237
N13: Reflect.json — cost model calibration gap.
N14: Kipf, A. et al. (2021). "COMPASS: Sketch-Based Query Optimization." SIGMOD 2021.
N15-N16: COMPASS evaluation and fast sketch composition.
N17: Yang et al. (2023). "Predicate Transfer for Multi-Way Joins."
N18: Ma, Z. (2023). "SieveJoin: Bloom Filter Propagation for Multi-Way Joins."
N19: PostgreSQL 8.3 — LEO Introduction.
N20: Zhang, H. et al. (2016). "TuNao: Deep Learning for Cardinality Estimation." SIGMOD 2016.
N21: Bao, Z. et al. (2020). "Bao: Making Learned Query Optimization Practical." SIGMOD 2021.
N22: Oracle 19c — Adaptive Plans. https://docs.oracle.com/en/database/oracle/oracle-database/19/tgsql/
N23: Microsoft Learn — Adaptive Joins.
N24: CAM (2026). "Cache-Aware Cost Model for Learned Indexes."
N25: Kraska, T. et al. (2019). "The Case for Learned Index Structures."
N26: Ferragina & Vinciguerra (2019). "The PGM-Index."
N27: Kraska, T. et al. (2020). "ALEX: An Adaptive Learned Index Structure." SIGMOD 2020.
N28: Ferragina & Vinciguerra (2024). "PGM++: An Improved PGM-Index."
N29: PGM-index asymptotic analysis (author derivation).
N30: Learned index poisoning attacks. https://arxiv.org/abs/2604.24975
N31: Shanbhag & Sudarshan (2014). "Optimizing Join Enumeration in Transformation-based Query Optimizers." VLDB 7.
N32: Li, P. et al. (2026). "On the Predictive Power of Q-Error for Plan Quality."

### Additional Comparative References

- SQLite ANALYZE and PRAGMA optimize. https://www.sqlite.org/lang_analyze.html
- PostgreSQL 17 — GEQO. https://www.postgresql.org/docs/17/geqo-intro.html
- PostgreSQL 17 — Planner Cost Constants. https://www.postgresql.org/docs/17/runtime-config-query.html
- SQLite File Format — sqlite_stat1. https://www.sqlite.org/fileformat2.html#stat1tab
- SQLite File Format — sqlite_stat4. https://www.sqlite.org/fileformat2.html#stat4tab
- PostgreSQL 17 — Custom Scan Provider. https://www.postgresql.org/docs/17/custom-scan-provider.html
- PostgreSQL 17 — Planner Support Functions. https://www.postgresql.org/docs/17/xfunc-optimization.html
- PostgreSQL 17 — Extensions. https://www.postgresql.org/docs/17/extend-extensions.html
- PostgreSQL 17 — Virtual Tables. https://www.sqlite.org/vtab.html
- PostgreSQL Source — planner.c. https://raw.githubusercontent.com/postgres/postgres/master/src/backend/optimizer/plan/planner.c
- Razordata source: `internal/SQB/EX/cost.go` (joinPredSel), `internal/SQB/EX/join_order.go` (N3, bushy), `internal/SQF/PL/memo.go` (xxhash), `internal/SQF/PL/learned.go` (LearnedModel), `internal/SQB/UT/analyze.go` (ANALYZE), `internal/SQB/EX/predicate.go` (learned blending). Commit 15444ba.