> Generated 2026-07-09 · depth: standard · workspace: /workspace

# Query Plan Optimizer Architecture: SQLite vs PostgreSQL — Implications for Razordata SQF/PL

## Executive Summary

- **SQLite's N3 (N-Nearest-Neighbors) algorithm provides O(K×N) join ordering** with N=12-18 for 3+ table joins, prioritizing deterministic plan stability (QPSG) over exhaustive optimality; it guarantees identical plans for identical schemas and statistics across runs [F1:1][F5:4].
- **PostgreSQL uses dual strategies: exhaustive dynamic programming for ≤12 tables, GEQO (genetic algorithm) for larger queries**, accepting non-determinism in exchange for better plans on complex joins with configurable pool size (100-1000) and selection bias [F2:1][F2:2].
- **Razordata's N3 with multi-start (K≤8) and n3HeapMaxSize=24 matches or complements SQLite's structural sophistication** but lacks SQLite's star-query cost-inflation heuristic (v3.49.0) that prevents greedy N3 from prematurely discarding fact-table search paths [F3:1][F6:3][F1:9].
- **Selectivity estimation across systems follows a gradient**: SQLite defaults to 10 duplicates per leftmost index column without ANALYZE; Razordata uses NDV-based equality (1/max(ndvL, ndvR)), MCV-aware IN-list formulas, and histogram range queries; PostgreSQL adds multivariate statistics for correlated columns — the independence assumption causes catastrophic underestimation when columns are correlated [F4:3][F4:4].
- **Razordata's cost model defaults exactly match PostgreSQL's conventional values** (seq_page_cost=1.0, random_page_cost=4.0, cpu_tuple_cost=0.01, cpu_index_tuple_cost=0.005, cpu_operator_cost=0.0025) but lacks effective_cache_size for index-vs-seqscan tradeoffs [F3:4].
- **Razordata already supports HashJoin, MergeJoin, and NestedLoopJoin** — exceeding SQLite's nested-loop-only approach — but lacks PostgreSQL's Memoize node for caching parameterized NLJ inner-side results and parallel join variants [F3:8][F6:7].
- **Plan memoization is architecturally distinct in Razordata**: xxhash64 AST fingerprints with schema-version invalidation in an LRU cache (4096 entries), a capability absent in both SQLite (re-plans per statement) and PostgreSQL (statement-level generic/custom plan cache, not full-plan fingerprinting) [F3:6].
- **Statistics collection differs materially**: Razordata uses fixed 10K reservoir sampling with 256-bucket histograms; SQLite's ANALYZE collects per-index NDV cascading statistics; PostgreSQL's default_statistics_target=100 is configurable per-column, enabling accuracy-vs-time tradeoffs for high-cardinality columns that Razordata cannot currently make [F3:7][F6:6].
- **The learned selectivity model (LearnedModel) is architecturally forward-looking but currently a stub** — Predict() returns the histogram selectivity regardless of training state, and BootstrapFromHistograms only sets the trained flag without fitting any model [F4:8].
- **Extensibility tradeoff is well-defined**: PostgreSQL's custom scan providers and planner hooks require dynamic loading, incompatible with Razordata's "no external C deps" embedded philosophy; Razordata correctly uses Go interfaces (DT.Operator, StatsCatalog) for extensibility instead [F5:9].

---

## Background & Scope

### Motivation

Razordata's SQF/PL planner is an embedded Go cost-based optimizer inspired by SQLite's NGQP but incorporating PostgreSQL-compatible cost model parameters. The planner currently implements N3 join ordering with multi-start, NDV-based selectivity estimation, MCV-aware IN-list formulas, cost-based index selection, and xxhash plan memoization. This report analyzes how SQLite and PostgreSQL differ architecturally and identifies specific, actionable improvements for Razordata's planner — prioritized by impact and effort, and scoped to what is feasible in an embedded Go engine with no external dependencies.

### Scope

**In scope**: SQLite planner architecture (NGQP/N3, index selection, sqlite_stat1/stat4, selectivity estimation, pragmas), PostgreSQL planner architecture (DP, GEQO, cost model, pg_statistic, join algorithms, hooks), key architectural differences, Razordata PL current state, and refactoring recommendations with effort estimates.

**Out of scope**: SQL parser internals, execution engine details beyond what the planner emits, storage internals beyond statistics interfaces, MySQL/Oracle planners, full implementation of recommendations, and query benchmarking.

---

## Body

### Theme 1: Join Ordering Algorithms

#### SQLite's N3 Approach

SQLite introduced N3 (N-Nearest-Neighbors) in version 3.8.0 (2013) to replace the legacy Nearest-Neighbor heuristic [F1:1]. N3 keeps N best partial plans at each step instead of greedily choosing a single best neighbor. The algorithm's complexity is O(K×N) where K is the number of tables, and the storage requirement is O(N) [F1:1][F6:1]. SQLite tunes N by join size: N=1 for simple queries, N=5 for 2-way joins, N=10 (later raised to 12, then 18 for star schemas) for 3+ tables [F1:1][F6:2][F7:2].

For TPC-H Q8 (an 8-way join), the NN heuristic yields a plan 750× slower than optimal (log-cost 36.92 vs 27.38) because NN only considers a single best path at each step and misses globally better combinations [F1:2]. N3 at N≥10 finds the optimal join order.

SQLite implements joins exclusively as nested loops [F1:3]. Inner joins are freely reorderable; outer joins are not (due to commutativity/associativity constraints). CROSS JOIN is treated specially — tables are never reordered, giving developers a mechanism to force specific nesting order [F1:3].

#### PostgreSQL's Dual Strategy

PostgreSQL uses exhaustive dynamic programming for queries involving ≤12 tables (the `geqo_threshold` default) and GEQO (Genetic Query Optimizer) for larger queries [F2:1][F2:2]. Dynamic programming exhaustively considers all join orders up to the threshold, guaranteeing optimal plans for moderate joins. For 13+ tables, GEQO uses a genetic algorithm with configurable pool size (default 100-1000 individuals), generations, and selection bias (1.50-2.00) [F2:2][F2:12].

GEQO was developed at the University of Mining and Technology in Freiberg, Germany, specifically for decision-support queries with many joins that made exhaustive search infeasible [F2:12]. The tradeoff is acknowledged: GEQO "reduces planning time for complex queries... at the cost of producing plans that are sometimes inferior to those found by the normal exhaustive-search algorithm" [F2:2].

#### Razordata's Position

Razordata's N3 implementation with multi-start (trying every candidate base table as starting point) and n3HeapMaxSize=24 matches or complements SQLite's structural sophistication [F3:1][F7:7]. For a query with K FROM-tables, the cost is K × K² × H evaluations where H=24; with K=8 this is ~1536 evaluations — negligible compared to execution time [F8:12]. However, Razordata's N=24 is a flat constant, whereas SQLite dynamically adjusts N based on join size and schema structure [F6:2].

**Key gap**: Razordata lacks SQLite's star-query cost-inflation heuristic (v3.49.0) that artificially raises dimension-table full-scan costs to prevent N3 from greedily discarding fact-table search paths [F3:1][F6:3][F1:9]. Razordata detects bushy joins (`groupBushyJoins`) but has no equivalent cost adjustment for star schemas [F7:10].

**Key gap**: Razordata lacks PostgreSQL's Memoize node for caching parameterized NLJ inner-side results [F6:7]. PostgreSQL's `enable_memoize` (since PG14) caches inner-side scan results by parameter values, avoiding redundant inner scans for correlated subqueries.

**Recommendation priority**:
1. **Star-query cost inflation** (effort: small) — port SQLite v3.49.0's dimension-table cost-raising heuristic to `n3JoinOrderingMultiStart` when `groupBushyJoins` detects a star pattern.
2. **Adaptive N sizing** (effort: small) — vary `n3HeapMaxSize` by join count (e.g., N=8 for 2-way, N=16 for 4-6 way, N=24 for 7+ way) instead of flat 24.
3. **Memoize node for parameterized NLJ** (effort: medium) — cache inner-side scan results keyed by parameter values for reuse across outer-loop iterations.

### Theme 2: Statistics and Selectivity Estimation

#### Three-Tier Selectivity Model (PostgreSQL)

PostgreSQL uses a three-tier selectivity model per predicate: MCV (Most Common Values) lists for equality (exact frequency when value is in the list), histograms for range (equal-frequency buckets with linear interpolation), and the two are combined — MCV selectivity is computed directly, then the histogram estimates the non-MCV population, and the two are merged weighted by their respective population fractions [F4:1].

For equality selectivity of values NOT in the MCV list, PostgreSQL uses the formula: `(1 - sum(mcv_freqs)) / (num_distinct - num_mcv)`, distributing remaining probability mass uniformly across non-MCV distinct values. For a column with 676 distinct values and 10 MCVs, a non-MCV equality predicate gets selectivity ≈ 0.00146 [F4:2].

For equi-join selectivity, PostgreSQL's `eqjoinsel` for unique columns uses: `(1 - null_frac1) * (1 - null_frac2) / max(num_rows1, num_rows2)` [F4:3].

#### SQLite's Simpler Model

SQLite's selectivity estimation is the simplest of the three systems. Without ANALYZE, SQLite defaults to 10 duplicates per leftmost index column [F1:7][F4:5]. With ANALYZE, it collects NDV per index prefix column (stored in sqlite_stat1) and optionally histogram bounds (sqlite_stat3 for leftmost column, sqlite_stat4 for all columns, compile-time gated by SQLITE_ENABLE_STAT3/STAT4) [F1:5][F1:6]. However, histogram usage (when compiled with STAT4) only aids range queries on the leftmost index column, and only when the right-hand side is a compile-time constant or parameter — it cannot help with range queries on non-leftmost index columns or expression-based predicates [F4:6].

#### Razordata's Approach

Razordata's `joinPredSel` method implements `1/max(ndvL, ndvR)` for equality predicates, directly matching PostgreSQL's `eqjoinsel` standard function [F3:3]. Both fall back to 0.1 when NDV is unavailable. The IN-list selectivity uses the MCV-aware formula `1 - ∏(1 - pᵢ)` over matched MCV values, which is a CockroachDB/PostgreSQL-derived approach [F4:7]. OR-chain selectivity uses `1 - ∏(1 - 1/ndv)^k` per column group for same-column OR equalities, which is more sophisticated than SQLite (which either converts OR to IN or does full index union) and structurally similar to PostgreSQL's approach of summing MCV frequencies for OR-matched values [F4:9].

**Key gap — multivariate statistics**: PostgreSQL's `CREATE STATISTICS` addresses the independence assumption that causes catastrophic underestimation for correlated predicates. Without statistics objects, AND-combined predicates are multiplied independently (1% × 1% = 0.01% for `a=1 AND b=1` where `a=b`), but with functional dependencies enabled, the planner estimates 1% — two orders of magnitude more accurate [F4:4][F6:5][F7:6]. Razordata's `LearnedModel.correlations` tracking exists but is not yet integrated into multi-predicate selectivity estimation in a production-ready way [F4:4].

**Key gap — null fraction correction**: PostgreSQL's `eqjoinsel` includes null fraction correction `(1 - null_frac1) * (1 - null_frac2)` while Razordata's implementation omits this [F4:3]. This matters when columns have significant NULL populations.

**Key gap — fixed statistics granularity**: Razordata uses a fixed 10K reservoir sample with 256-bucket histograms; PostgreSQL's `default_statistics_target=100` is configurable per-column via `ALTER TABLE SET STATISTICS` [F3:7][F6:6]. Razordata cannot trade off analysis time for accuracy on high-cardinality columns (100K+ distinct values).

**Recommendation priority**:
1. **Null fraction in eqjoinsel** (effort: small) — incorporate `(1 - null_frac)` correction into `joinPredSel` when NullCount is available.
2. **Per-column statistics target** (effort: medium) — add a statistics target parameter to `analyzeTable` (e.g., default 100, configurable per-table) to trade accuracy for ANALYZE time on high-cardinality columns.
3. **Extended statistics for correlated columns** (effort: large) — implement a lightweight functional dependency detection (no CREATE STATISTICS overhead) using `LearnedModel.correlations` to correct AND-predicate selectivity for known-correlated column pairs.

### Theme 3: Cost Model Architecture

#### SQLite's Log-Additive Cost Model

SQLite's cost model is log-additive for nested loops — costs multiply in linear space but are represented logarithmically [F1:8]. It estimates setup cost (automatic index construction), per-step iteration cost, row count, and sorting cost. The outermost loop uses a standalone cost with no dependency on other tables. SQLite deliberately does not expose its cost constants publicly, making the model opaque but internally consistent.

SQLite adds automatic query-time indexes when no existing index aids a query and the expected lookup count exceeds log(N); these are transient, statement-scoped, and cost O(N*logN) to build [F1:12].

#### PostgreSQL's Parameterized Cost Model

PostgreSQL's cost model uses relative units with `seq_page_cost=1.0` as the baseline; `random_page_cost=4.0`, `cpu_tuple_cost=0.01`, `cpu_index_tuple_cost=0.005`, `cpu_operator_cost=0.0025` [F2:3]. All cost variables are user-tunable via GUCs (Grand Unified Configuration). The `effective_cache_size` parameter (default 4GB) influences index-vs-seqscan decisions by estimating available disk cache [F2:10]. Parallel query support adds `parallel_setup_cost=1000` and `parallel_tuple_cost=0.1` with minimum scan sizes of 8MB for tables and 512KB for indexes [F2:9].

#### Razordata's Position

Razordata's `DefaultCostParams()` returns values exactly matching PostgreSQL's defaults [F3:4]. However, Razordata lacks `effective_cache_size` (PG default 4GB, used to adjust index vs seqscan tradeoff) and parallel cost parameters. The code comments confirm: "Defaults match PostgreSQL's conventional values (REQ001104)" [F3:4].

Razordata's cost model is tunable via `SetCostParams`, allowing callers to adjust for specific workloads — e.g., all-in-memory tables where random I/O is cheap [F4:11]. This is more extensible than SQLite's opaque model but less than PostgreSQL's full GUC system.

**Key gap — effective_cache_size**: Without this parameter, Razordata's index-vs-seqscan decisions are based solely on random_page_cost=4.0, which may overestimate index benefit for in-memory workloads where random I/O cost approaches sequential I/O cost.

**Recommendation priority**:
1. **Add effective_cache_size** (effort: small) — incorporate a cache-size hint into `estimateIndexCost` to adjust the random I/O penalty based on whether pages are likely in memory.
2. **Tune default random_page_cost for embedded** (effort: small) — consider defaulting to 1.0-2.0 for an embedded engine where all data is in-process, rather than PostgreSQL's 4.0 which assumes disk-based I/O.

### Theme 4: Join Algorithm Selection

#### SQLite's Nested-Loop-Only Approach

SQLite implements joins exclusively as nested loops [F1:3]. Inner joins are freely reorderable; outer joins are not. This is the simplest possible join implementation but means SQLite cannot exploit hash or merge opportunities for equi-joins on large tables.

#### PostgreSQL's Cost-Based Algorithm Selection

PostgreSQL supports three join algorithms: nested loop (with index), merge (requires sort or index), and hash (builds hash table from right relation) [F2:8]. The planner generates Path structures during optimization and selects the cheapest path [F2:7]. The Memoize node (since PG14) caches inner-side results of parameterized NLJ joins to avoid redundant inner scans [F6:7].

#### Razordata's Position

Razordata already supports HashJoin, MergeJoin, NestedLoopJoin, HashCrossJoin, and BitmapHeapScan [F3:8]. The `groupBushyJoins` method detects independent equi-join pairs for bushy execution — a capability neither SQLite nor PostgreSQL has at the plan-generator level (PG relies on the executor for bushy plans) [F3:8].

**Key gap**: Razordata lacks PostgreSQL's Memoize node for parameterized NLJ inner-side caching [F6:7].

**Recommendation priority**:
1. **Memoize node** (effort: medium) — cache parameterized NLJ inner-side results to avoid redundant scans for correlated subqueries.

### Theme 5: ANALYZE and Statistics Collection

#### SQLite's ANALYZE System

SQLite's ANALYZE collects sqlite_stat1 (per-index row counts) and optionally sqlite_stat3/stat4 histograms [F1:5][F1:6]. Without ANALYZE, the default is 10 duplicates per leftmost index column [F1:7]. `PRAGMA optimize` (introduced 3.18.0, significantly enhanced 3.46.0) selectively runs ANALYZE based on accumulated query-planner usage records, auto-limiting scope for large databases [F1:10]. `PRAGMA analysis_limit` (3.32.0) enables approximate ANALYZE by capping rows scanned per index (100-1000 recommended), but sqlite_stat4 histograms require full scans [F1:10].

#### PostgreSQL's ANALYZE System

PostgreSQL's ANALYZE collects MCV lists, histograms, and per-column distinct counts in `pg_statistic` [F2:4][F7:5]. The `default_statistics_target=100` controls histogram/MCV granularity per column, configurable via `ALTER TABLE SET STATISTICS` [F2:11][F6:6]. Larger values improve accuracy but increase ANALYZE time.

#### Razordata's Position

Razordata's `analyzeTable` uses a fixed `sampleSize = 10000` for reservoir sampling and builds 256-bucket equi-depth histograms [F3:7]. The `razor_stat1` system table makes statistics queryable via SQL [F3:9]. The zero-row problem (empty table selectivity) is explicitly guarded against — `estimateRowCount` skips entries where `DT.Tables` has an empty slice, and equality/range selectivity return fallback values (0.1/0.3) when RowCount is 0 [F4:10].

**Key gap — no automatic statistics refresh**: SQLite's `PRAGMA optimize` automatically triggers ANALYZE based on query usage [F1:10]; Razordata requires explicit ANALYZE calls with no equivalent automatic trigger, though SQLite's `PRAGMA optimize` is also opt-in per-connection rather than a true background process [F6:11].

**Key gap — fixed statistics granularity**: The 10K reservoir sample and 256-bucket histogram cannot be tuned per-column. For high-cardinality columns (100K+ distinct values), 256 buckets provide ~400 distinct values per bucket — potentially insufficient accuracy for skewed distributions [F3:7].

**Recommendation priority**:
1. **Incremental ANALYZE** (effort: medium) — implement `PRAGMA optimize`-style automatic ANALYZE that triggers based on table modification counts since last ANALYZE.
2. **Configurable histogram bucket count** (effort: small) — make the 256-bucket count a parameter, defaulting to 100 (matching PostgreSQL's default) to reduce memory per column.

### Theme 6: Star-Schema Handling

Star schemas (one large fact table joined to many small dimension tables) present a specific challenge for N3-style join ordering. The greedy nature of N3 can prematurely discard fact-table search paths when cheap dimension-table full scans dominate the early N-best heap [F1:9][F6:3][F7:10].

SQLite's solution (v3.49.0, 2025-02-06) is to artificially raise the estimated full-table scan cost for dimension tables so that the cost is slightly greater than the cost of finding the relevant rows of the fact table [F1:9][F5:10]. This replaced an earlier approach (v3.47.0-3.48.0) that lowered fact-table costs, which caused other regressions [F1:9].

Razordata detects bushy joins via `groupBushyJoins` but has no equivalent cost adjustment for star schemas [F3:1][F7:10]. The N3 pruning multiplier (`n3PruneMultiplier=2.0`) may be too aggressive for star-schema queries with 6+ dimension tables, potentially pruning fact-table search paths before they can compete with cheap dimension scans.

**Recommendation priority**:
1. **Star-query cost inflation** (effort: small) — when `groupBushyJoins` detects a star pattern, inflate dimension-table full-scan costs by a small factor (e.g., 1.1× the estimated fact-table search cost) to prevent N3 from discarding fact-table paths.

### Theme 7: Plan Memoization and Caching

#### SQLite

SQLite has no plan memoization at all — prepared statements are re-planned on each execution [F3:6]. The Query Planner Stability Guarantee (QPSG, opt-in via `SQLITE_ENABLE_QPSG`) ensures deterministic plan selection given the same schema and statistics [F5:4][F7:12].

#### PostgreSQL

PostgreSQL's Memoize node (since PG14) caches inner-side results of parameterized nested-loop joins [F6:7]. PostgreSQL's prepared statement plan cache uses generic vs custom plan selection, not AST fingerprinting [F3:6].

#### Razordata

Razordata uses xxhash64 (not SHA256) to fingerprint AST → plan, stored in an LRU cache (4096 entries, REQ000584) [F3:6][F5:7]. Schema version bumps (DDL) invalidate all cached plans via key mismatch. This is architecturally distinct — neither SQLite nor PostgreSQL has equivalent full-plan memoization.

**Recommendation priority**:
1. **Investigate xxhash64 collision risk** (effort: small) — the 16-hex-character key has 64 bits of entropy; assess whether collisions are possible at scale (4096 entries is low risk, but larger caches warrant analysis).
2. **Per-parameter-value cache for NLJ** (effort: medium) — complement the statement-level memoization with a per-parameter-value cache for parameterized NLJ inner-side results.

### Theme 8: Extensibility Patterns

#### PostgreSQL

PostgreSQL provides deep planner extensibility via custom scan providers (since 9.6) and planner hooks (planner_hook, planner_setup_hook, planner_shutdown_hook, create_upper_paths_hook) [F2:6][F5:1]. Planner support functions allow user-defined functions to provide custom selectivity estimates, cost models, and index condition conversions at plan time [F5:2]. The extension packaging system (since 9.1) bundles C code, SQL objects, and control files into installable units [F5:3].

#### SQLite

SQLite's extensibility is at the data access layer (virtual tables with `xBestIndex`), not the planner layer [F5:6]. Virtual tables can report which WHERE constraints they handle and their estimated cost — a simpler extensibility model than PostgreSQL's custom scan providers [F5:6]. SQLite prioritizes predictability over adaptability via QPSG [F5:4].

#### Razordata

Razordata's extensibility comes via Go interfaces (DT.Operator, StatsCatalog) rather than loadable modules — appropriate for the "no external C deps" embedded design constraint [F5:9][F5:11]. The `CostParams` struct allows callers to tune cost model coefficients without modifying planner internals [F5:11]. The `StatsCatalog` interface enables custom statistics sources [F3:4].

**Assessment**: PostgreSQL-style extensibility (dynamic loading, plugin systems) is incompatible with Razordata's embedded Go philosophy. Razordata correctly uses Go interfaces for extensibility. The current extensibility surface (CostParams, StatsCatalog, DT.Operator) is appropriate for an embedded engine.

---

## Open Questions

1. **High-cardinality statistics accuracy**: Does Razordata's fixed 10K reservoir sample + 256-bucket histogram provide sufficient accuracy for high-cardinality columns (100K+ distinct values) compared to PostgreSQL's configurable `default_statistics_target`? [F3:7, F4:10]

2. **Correlation statistics for IndexScan vs SeqScan**: Should Razordata adopt PostgreSQL's correlation statistic to improve IndexScan vs SeqScan decisions when data is physically ordered? PostgreSQL uses this to predict heap fetch sequentiality — Razordata lacks it. [F3:10]

3. **Functional dependency statistics**: Can Razordata's `LearnedModel.correlations` tracking be extended to provide functional-dependency statistics without PostgreSQL's `CREATE STATISTICS` overhead? [F4:8]

4. **Planning-time cost of N3 at scale**: What is the planning-time cost of Razordata's N3 with `n3HeapMaxSize=24` versus SQLite's N=12-18 for 8+ way joins? Razordata's multi-start at K=8 requires ~1536 evaluations — is this acceptable for embedded OLTP? [F8:12]

5. **CROSS JOIN escape hatch**: Should Razordata add a CROSS JOIN escape hatch (like SQLite) for developer-controlled join ordering, or does multi-start N3 suffice? [F1:3]

---

## Sources

1. SQLite Query Planner NG (N3 algorithm documentation). https://www.sqlite.org/queryplanner-ng.html. Accessed 2026-07-09.
2. SQLite Query Optimizer Overview (join types, index selection, skip-scan, auto-indexes). https://www.sqlite.org/optoverview.html. Accessed 2026-07-09.
3. SQLite File Format — sqlite_stat1 table. https://www.sqlite.org/fileformat2.html#stat1tab. Accessed 2026-07-09.
4. SQLite File Format — sqlite_stat4 table. https://www.sqlite.org/fileformat2.html#stat4tab. Accessed 2026-07-09.
5. SQLite ANALYZE and PRAGMA optimize. https://www.sqlite.org/lang_analyze.html. Accessed 2026-07-09.
6. PostgreSQL 17 Documentation — Planner/Optimizer. https://www.postgresql.org/docs/17/planner-optimizer.html. Accessed 2026-07-09.
7. PostgreSQL 17 Documentation — Genetic Query Optimizer (GEQO). https://www.postgresql.org/docs/17/geqo-intro.html. Accessed 2026-07-09.
8. PostgreSQL 17 Documentation — Planner Cost Constants. https://www.postgresql.org/docs/17/runtime-config-query.html. Accessed 2026-07-09.
9. PostgreSQL 17 Documentation — Row Estimation Examples. https://www.postgresql.org/docs/17/row-estimation-examples.html. Accessed 2026-07-09.
10. PostgreSQL 17 Documentation — Planner Statistics. https://www.postgresql.org/docs/17/planner-stats.html. Accessed 2026-07-09.
11. PostgreSQL 17 Documentation — Multivariate Statistics Examples. https://www.postgresql.org/docs/17/multivariate-statistics-examples.html. Accessed 2026-07-09.
12. PostgreSQL 17 Documentation — Custom Scan Provider. https://www.postgresql.org/docs/17/custom-scan-provider.html. Accessed 2026-07-09.
13. PostgreSQL 17 Documentation — Planner Support Functions. https://www.postgresql.org/docs/17/xfunc-optimization.html. Accessed 2026-07-09.
14. PostgreSQL 17 Documentation — Extensions. https://www.postgresql.org/docs/17/extend-extensions.html. Accessed 2026-07-09.
15. PostgreSQL 17 Documentation — Virtual Tables. https://www.sqlite.org/vtab.html. Accessed 2026-07-09.
16. PostgreSQL Source Code — planner.c (hooks). https://raw.githubusercontent.com/postgres/postgres/master/src/backend/optimizer/plan/planner.c. Accessed 2026-07-09.
17. Razordata source: internal/SQF/PL/planner.go (CostParams, N3 constants). Commit 15444ba, 2026-07-09.
18. Razordata source: internal/SQB/EX/cost.go (joinPredSel, selectivity formulas). Commit 15444ba, 2026-07-09.
19. Razordata source: internal/SQB/EX/planner.go (CostParams defaults). Commit 15444ba, 2026-07-09.
20. Razordata source: internal/SQB/EX/join_order.go (n3JoinOrderingMultiStart, groupBushyJoins). Commit 15444ba, 2026-07-09.
21. Razordata source: internal/ENG/LS/stats.go (ColumnStats struct). Commit 15444ba, 2026-07-09.
22. Razordata source: internal/SQF/PL/memo.go (xxhash memoization). Commit 15444ba, 2026-07-09.
23. Razordata source: internal/SQF/PL/learned.go (LearnedModel stub). Commit 15444ba, 2026-07-09.
24. Razordata source: internal/SQB/EX/predicate.go (learned selectivity blending). Commit 15444ba, 2026-07-09.
25. Razordata source: internal/SQB/UT/analyze.go (ANALYZE implementation, razor_stat1 schema). Commit 15444ba, 2026-07-09.
26. Razordata source: internal/SQB/EX/planner_stats_propagation.go (applyStatsPropagation). Commit 15444ba, 2026-07-09.
27. Razordata source: internal/SQB/EX/req001218_or_to_in_test.go (OR-chain selectivity tests). Commit 15444ba, 2026-07-09.
28. Razordata design: AGENTS.md (project rules, embedded constraints). 2026-07-09.
29. Razordata design: docs/design/ARCH.md (directory layout, architecture). 2026-07-09.
