> Generated 2026-07-09 · Updated 2026-07-09 · depth: standard · workspace: /workspace

# Query Plan Optimizer Architecture: SQLite vs PostgreSQL vs Modern DBMS — Implications for Razordata SQF/PL

## Executive Summary

### Original Findings (SQLite vs PostgreSQL)

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

### Modern DBMS Findings (New)

- **LEO-style post-execution feedback is the highest-impact modern technique for Razordata** — it corrects cardinality estimates post-execution by adjusting histograms using actual-vs-predicted row count ratios. TiCard demonstrates ~157 queries for convergence in a training split, dropping P90 Q-error from 312.85 to 13.69 [N1][N2]. DBSel-CV achieves faster convergence with only thousands of parameters, explicitly noted as embeddable in lightweight engines like SQLite [N3].
- **The Cascades memo-based optimizer is architecturally feasible but oversized for Razordata** — NeuSO's top-down greedy enumerator (O(N·H) complexity, SIGMOD 2026) is more directly applicable, requiring restructuring of the N3 partial struct into a memo group store [N4][N5].
- **Bloom filter pre-filtering during join execution offers 32.8% latency reduction on TPC-H** when integrated into bottom-up cost-based optimization [N6]. DuckDB already implements this with linear probing and prefix range filters [N7].
- **PostgreSQL's Memoize node is a concrete, low-effort gap** — it requires no parallel coordination, uses a simple LRU hash table, and directly addresses correlated subquery performance in single-threaded execution [N8].
- **HyperLogLog, t-digest, and count-min sketch are theoretically applicable as statistics replacements** but their accuracy advantage over fixed 256-bucket histograms at embedded cardinalities (10K-10M keys) is unproven [N9][N10].
- **Learned indexes (PGM, ALEX) carry significant risk for Razordata** — their O(log log N) advantage diminishes at embedded-workload cardinalities, they are vulnerable to adversarial insertions (up to 1641x slowdown) [N11], and they underperform B+-tree on disk-resident workloads [N12].
- **No dedicated cost model innovation findings exist** — calibration-based cost models (TPC-H/TPC-DS) and machine-learned cost models remain unaddressed in the literature for embedded engines [N13].

---

## Background & Scope

### Motivation

Razordata's SQF/PL planner is an embedded Go cost-based optimizer inspired by SQLite's NGQP but incorporating PostgreSQL-compatible cost model parameters. The planner currently implements N3 join ordering with multi-start, NDV-based selectivity estimation, MCV-aware IN-list formulas, cost-based index selection, and xxhash plan memoization. This report analyzes how SQLite, PostgreSQL, and modern DBMS techniques differ architecturally and identifies specific, actionable improvements for Razordata's planner — prioritized by impact and effort, and scoped to what is feasible in an embedded Go engine with no external dependencies.

### Architectural Constraints

Razordata operates under strict embedded constraints [AGENTS.md]: no network server, no external C dependencies, Go 1.26+, single `go.mod`, Linux/macOS/Windows. The codebase uses `sync.Pool` for reusable page buffers, 4KB pages, and all public API methods must be goroutine-safe. `Engine.Write()` is the sole write path (serial); reads are lock-free via MVCC. Any technique requiring CGo, FFI, external services, or dynamic loading is flagged as infeasible.

### Scope

**In scope**: SQLite planner architecture (NGQP/N3, index selection, sqlite_stat1/stat4, selectivity estimation, pragmas), PostgreSQL planner architecture (DP, GEQO, cost model, pg_statistic, join algorithms, hooks), modern DBMS techniques (Cascades framework, learned cardinality estimation, adaptive query processing, Bloom filter joins, advanced statistics structures, learned indexes, top-K plans), key architectural differences, Razordata PL current state, and refactoring recommendations with effort estimates.

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

#### Cascades Framework and NeuSO (Modern)

The Cascades framework (Graefe, 1995) restructures optimization as a top-down search over equivalence classes of logical expressions stored in a Memo data structure [N4]. Each Memo group contains one logical expression and zero or more physical implementations with costs. Transformation rules (join commutativity, associativity, predicate pushdown) fire on groups to produce new groups [N4][N5].

NeuSO (SIGMOD 2026) introduces a top-down greedy plan enumerator that reduces enumeration cost from O(N!) DP to O(N·H) for subgraph queries [N5]. The enumerator greedily picks the cheapest next vertex at each step using a "minimum cost" metric, avoiding exponential state space. This is more directly applicable to Razordata's N3 approach than full Cascades — the current N3 heap-based DP already maintains a bounded set of partial plans and could be restructured as a top-down greedy with memoized minimum costs.

**Assessment**: Full Cascades restructuring is large effort with uncertain benefit for embedded workloads (typically <10 tables). NeuSO-style top-down greedy with memoized minimum costs is a more targeted improvement that fits within N3's existing structure. The xxhash key generation infrastructure is already in place; only the group store semantics need extension.

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

#### COMPASS Sketch-Based Estimation (Modern)

COMPASS uses Fast-AGMS sketches (count-min sketch variant) as the sole statistics type, intertwining optimization and execution by pushing sketch updates down during plan enumeration [N14]. It achieves 1.35x–11.28x speedup over competitors on the JOB benchmark [N15]. Sketches compose additively: `sketch(A∪B) = sketch(A) + sketch(B)` elementwise, enabling join cardinality estimation via cross-product on joined attribute sketches [N16].

**Integration with N3**: Sketches could be attached as metadata to each N3 heap entry for composition during expansion [N16]. However, COMPASS intertwines sketch construction with optimization in a top-down Cascades-style framework — adapting this to N3's bottom-up heap expansion requires careful design. A standard Count-Min sketch with width=1024, depth=4 occupies ~16KB (4 pages) — significant overhead for an embedded engine with 4KB page buffers [N16].

**Assessment**: Not recommended as a near-term technique due to high integration complexity. The histogram + NDV + MCV approach already covers the most important selectivity estimation scenarios.

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

PostgreSQL supports three join algorithms: nested loop (with index), merge (requires sort or index), and hash (builds hash table from right relation) [F2:8]. The planner generates Path structures during optimization and selects the cheapest path [F2:7]. The Memoize node (since PG14) caches inner-side results of parameterized NLJ joins to avoid redundant inner scans [N8].

#### Bloom Filter Pre-Filtering (Modern)

Integrating Bloom filters into bottom-up cost-based optimization yields 32.8% latency reduction on TPC-H 100GB versus post-optimization Bloom filter insertion [N6]. The key insight is that Bloom filter placement affects optimal join order — the cheapest plan without Bloom filters is not the cheapest plan with them [N6]. Predicate transfer generalizes Bloom join to multi-table joins, outperforming single Bloom join by 3.1x on TPC-H [N17]. SieveJoin propagates Bloom filters through multi-way join paths with negligible memory overhead [N18].

**Assessment**: The 32.8% reduction was evaluated on TPC-H at 100GB — applicability to embedded-scale workloads (MB-GB range) is unknown. However, the implementation is straightforward: build a Bloom filter from one join's output and apply it as a pre-filter on the next join's input. DuckDB already does this [N7].

**Feasibility**: High — Bloom filters are simple data structures implementable in Go.
**Impact**: Medium — 32.8% reduction is significant but may not transfer to smaller workloads.
**Effort**: Small-Medium.

#### Razordata's Position

Razordata already supports HashJoin, MergeJoin, NestedLoopJoin, HashCrossJoin, and BitmapHeapScan [F3:8]. The `groupBushyJoins` method detects independent equi-join pairs for bushy execution — a capability neither SQLite nor PostgreSQL has at the plan-generator level (PG relies on the executor for bushy plans) [F3:8].

**Key gap**: Razordata lacks PostgreSQL's Memoize node for parameterized NLJ inner-side caching [N8].

**Recommendation priority**:
1. **Memoize node** (effort: medium) — cache parameterized NLJ inner-side results to avoid redundant scans for correlated subqueries.
2. **Bloom filter pre-filtering** (effort: small-medium) — build Bloom filter from join output, apply as pre-filter on next join input.

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

PostgreSQL's Memoize node (since PG14) caches inner-side results of parameterized nested-loop joins [N8]. PostgreSQL's prepared statement plan cache uses generic vs custom plan selection, not AST fingerprinting [F3:6].

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

### Theme 9: Learned Cardinality Estimation (Modern)

#### LEO: Post-Execution Feedback Correction

LEO (Learning from the Optimizer) is a PostgreSQL-native feedback mechanism (implemented in DB2/PostgreSQL 8.3, 2007) that corrects selectivity estimates post-execution by adjusting histogram boundaries using actual-vs-predicted row count ratios [N1]. It computes a correction factor as `actual_count / estimated_count` and re-bins histograms accordingly. This is a lightweight, no-ML correction loop — the key advantage for embedded engines.

The feedback loop is synchronous: correction happens between queries, not during execution. In Razordata's single-connection model, corrections from query N are applied before query N+1 optimization, with no cross-connection amortization delay [N19].

**Single-connection convergence**: TiCard demonstrates that a training split of ~157 query executions yields correction-based cardinality learning with sub-millisecond inference, dropping P90 Q-error from 312.85 to 13.69 [N2]. Note: 157 is TiCard's training set size in one experimental configuration, not a proven convergence threshold. DBSel-CV achieves faster convergence than direct-prediction models with only thousands to tens of thousands of parameters, and is explicitly noted as embeddable in lightweight engines like SQLite [N3].

**Convergence behavior change**: In a single-connection engine, the correction schedule should be immediate (per-query) rather than batched, because there is no concurrent workload to amortize against [N19]. The convergence rate depends on workload diversity within the single connection, not on total system load — a narrow workload may never converge to good corrections [N19].

**Feasibility**: High — correction-based approaches augment rather than replace the native estimator, fit within Razordata's bounded struct approach (no `map[string]interface{}`), and require only a per-predicate correction factor.
**Impact**: High — addresses the root cause of plan quality degradation (cardinality estimation error).
**Effort**: Small-Medium — requires storing correction factors per predicate, applying them post-execution, and integrating with the existing histogram infrastructure.

#### TuNao and Bao: ML-Based Approaches

TuNao (SIGMOD 2016) was among the first to apply deep learning to cardinality estimation, mapping query features to result sizes via neural networks, but required heavy offline training and struggled with generalization [N20]. Bao (SIGMOD 2021) uses tree convolutional neural networks with Thompson sampling to provide per-query optimization hints, learning 10x faster than prior learned optimizers [N21].

**Assessment**: Both require ML infrastructure (training loops, feature extraction, model serving) that is disproportionate to the gain for an embedded engine. LEO-style correction provides the majority of the benefit with a fraction of the complexity.

### Theme 10: Adaptive Query Processing (Modern)

#### PostgreSQL Memoize Node

PostgreSQL introduced the Memoize node in version 14 (September 2021) to cache results from parameterized scans inside nested-loop joins [N8]. When the inner side of a parameterized NLJ produces the same result set for different outer-row parameter values, the cached result is reused, skipping the inner scan entirely. The node uses an LRU eviction policy [N8].

The Memoize node is inherently single-threaded within a given plan node — no parallel coordination is required. This makes it directly feasible for Razordata's execution model.

**Feasibility**: High — requires a hash table for caching join key lookups, which fits within Razordata's Go-native infrastructure.
**Impact**: Medium-High — directly addresses correlated subquery performance in parameterized join queries.
**Effort**: Medium — requires executor changes to add the Memoize operator and planner changes to emit it for parameterized NLJ joins.

#### Oracle Adaptive Plans and SQL Server Adaptive Joins

Oracle 12c (2013) introduced adaptive plans that switch join methods mid-execution based on actual row counts [N22]. SQL Server 2017 introduced adaptive joins that switch between NLJ and hash join at a configurable row-count threshold (default 100 rows) [N23].

**Assessment**: Mid-execution plan switching adds significant complexity (dual plan trees, runtime counters, fallback paths) and is not recommended for Razordata's embedded model. The Memoize node captures the practical benefit (adaptive caching) without the complexity of full plan switching.

### Theme 11: Advanced Statistics Structures (Modern)

#### HyperLogLog for NDV Estimation

HyperLogLog uses O(log log n) bits for NDV estimation with standard error ~1.04/√m [N9]. Razordata currently uses 256-bucket equi-depth histograms for range selectivity and NDV for equality selectivity. HyperLogLog replaces NDV counters with a single compact sketch.

**Assessment**: The accuracy advantage over fixed NDV counters at embedded cardinalities (10K–10M keys) is unproven. HLL's standard error of ~1% at 1KB memory is excellent, but Razordata already stores NDV as a single float64 — HLL would add complexity without clear benefit unless multi-column NDV estimation is needed.

**Feasibility**: High — HLL is a well-understood data structure implementable in Go.
**Impact**: Low — NDV estimation is already accurate; the bottleneck is correlation and multi-predicate selectivity, not NDV precision.
**Effort**: Small.

#### t-Digest for Quantile Estimation

t-digest provides mergeable quantile estimates with adaptive accuracy near distribution tails [N9]. Razordata's 256-bucket histograms approximate quantiles via linear interpolation within buckets. t-digest would provide more accurate quantile estimates, particularly for skewed distributions with long tails.

**Assessment**: The practical benefit depends on whether Razordata's queries frequently hit distribution tails. For uniform or moderately skewed data, 256 buckets are sufficient.

**Feasibility**: High.
**Impact**: Low-Medium — mainly benefits highly skewed distributions.
**Effort**: Small.

#### Count-Min Sketch for Frequency Estimation

Count-min sketch uses O(1/ε × log(1/δ)) space for frequency queries [N9]. QSketch (KDD 2024) achieves 8× memory reduction using 8-bit quantized counters with O(1) update [N10]. This is most relevant for MCV (Most Common Values) list replacement — instead of storing exact frequencies for top-K values, a sketch provides approximate frequencies with bounded error.

**Assessment**: Razordata's MCV lists are currently bounded by the statistics target. A CMS replacement would provide frequency estimates for ALL values, not just the top-K, potentially improving selectivity estimation for non-MCV predicates.

**Feasibility**: High — CMS is simple to implement in Go.
**Impact**: Low-Medium — mainly benefits selectivity estimation for values not in the MCV list.
**Effort**: Small-Medium.

### Theme 12: Cost Model Innovations (Gap)

#### Gap in Findings

The brief explicitly calls for research into: (a) calibration-based cost models using TPC-H/TPC-DS, (b) machine-learned cost models (regression on query features), and (c) hybrid approaches [N13]. No dedicated findings cover these topics. The only tangential reference is CAM (cache-aware cost model for learned indexes), which improves PGM throughput by 1.17x [N24] — relevant to index cost estimation but not to the broader cost model question.

#### Implications for Razordata

Razordata's cost model uses PostgreSQL-compatible defaults (seq_page_cost=1.0, random_page_cost=4.0, cpu_tuple_cost=0.01) with user-tunable `CostParams` [RPT:11]. The lack of `effective_cache_size` means index-vs-seqscan decisions may overestimate index benefit for in-memory workloads [RPT].

**Gap**: Without calibration data or learned cost adjustments, Razordata's cost model relies on heuristics that may not match actual execution costs on specific hardware.

**Recommendation**: The most impactful cost model improvement is adding `effective_cache_size` (effort: small) to adjust the random I/O penalty based on whether pages are likely in memory [RPT]. This is a direct port from PostgreSQL's approach.

### Theme 13: Automated Index Selection and Learned Indexes (Modern)

#### Learned Index Structures (PGM, ALEX)

PGM-index achieves O(log log N) lookup with O(N) space, provably outperforming B+-trees in asymptotic complexity [N25][N26]. ALEX achieves up to 4.1x speedup over B+-trees on read-write workloads with 2000x smaller index size [N27]. However, PGM++ shows that querying PGM-Indexes is "highly memory-bound, where the internal error-bounded search operations often become the bottleneck" [N28].

**Critical findings against learned indexes for Razordata**:

1. **Diminishing advantage at embedded cardinalities**: log log 10K ≈ 3.7 vs log₁₂₈(10K) ≈ 2.3 for B-tree with fanout 128; log log 10M ≈ 4.5 vs log₁₂₈(10M) ≈ 3.3 — the asymptotic advantage nearly vanishes at embedded scales [N29].
2. **Adversarial vulnerability**: ALEX suffers up to 1641x performance degradation from adversarial insertions [N11]; dynamic algorithmic complexity attacks degrade lookup throughput by 2–2.8x [N30].
3. **Disk-resident underperformance**: "directly applying the existing learned indexes on disk suffers from several drawbacks and cannot outperform a standard B+-tree in most cases" [N12].

**Assessment**: Learned indexes carry significant risk for Razordata's page-cache storage model. B+-tree remains more practical for engines with 4KB pages and single-goroutine write paths, given the diminishing asymptotic advantage at embedded scales and the adversarial vulnerability.

**Feasibility**: Medium (implementation) / Low (practical benefit).
**Impact**: Low — B+-tree is adequate for embedded cardinalities.
**Effort**: Large (implementation) with low payoff.

### Theme 14: Top-K Plan Enumeration (Modern)

PostgreSQL's GEQO switches from DP to genetic algorithms for ≥12 joins, demonstrating that no single enumeration strategy is optimal for all query sizes [N4]. Top-K enumeration within a Cascades framework requires maintaining K best plans per equivalence group. Shanbhag & Sudarshan show that each group expansion requires O(K) work [N5], yielding an estimated complexity of O(K·N²·H) for N tables (author derivation).

**Feasibility**: High.
**Impact**: Low-Medium — mainly relevant for queries with 6+ tables where multiple competitive plans exist.
**Effort**: Medium — builds on the Memo restructuring above.

---

## Integrated Refactoring Roadmap

### Tier 1: Quick Wins (effort: small, impact: high)

| # | Technique | Source | What to do |
|---|-----------|--------|------------|
| 1 | Null fraction in eqjoinsel | PG comparison | Add `(1-frac_null)` correction to `joinPredSel` |
| 2 | Effective cache size | PG comparison | Add `EffectiveCacheSize` to CostParams |
| 3 | Adaptive N sizing | PG comparison | Vary `n3HeapMaxSize` by join count (8→16→24) |
| 4 | LEO-style post-execution feedback | Modern | Store per-predicate correction factors, apply after each query |
| 5 | Star-query cost inflation | SQLite v3.49.0 | Inflate dimension-table costs in star patterns |

### Tier 2: Medium Impact (effort: medium, impact: medium-high)

| # | Technique | Source | What to do |
|---|-----------|--------|------------|
| 6 | Memoize node for parameterized NLJ | PG14 Memoize | Cache inner-side scan results keyed by parameter values |
| 7 | Per-column statistics target | PG comparison | Make histogram bucket count configurable (default 100) |
| 8 | Bloom filter pre-filtering | Zeyl et al. 2025 | Build Bloom filter from join output, apply as pre-filter |
| 9 | Incremental ANALYZE | SQLite PRAGMA optimize | Trigger ANALYZE based on modification counts |
| 10 | Configurable histogram buckets | PG comparison | Parameterize the 256-bucket count |

### Tier 3: Research/Exploratory (effort: large, impact: uncertain)

| # | Technique | Source | What to do |
|---|-----------|--------|------------|
| 11 | NeuSO-style top-down greedy | SIGMOD 2026 | Restructure N3 heap as memo group store |
| 12 | COMPASS sketch composition | SIGMOD 2021 | Evaluate at embedded scale (16KB overhead) |
| 13 | Extended statistics for correlated columns | PG comparison | Lightweight functional dependency detection |

### Tier 4: Avoid (risk > benefit for embedded)

| Technique | Why avoid |
|-----------|-----------|
| Full Cascades framework | Large restructuring, uncertain benefit for <10 table queries |
| Mid-execution plan switching | Too complex for embedded model |
| Learned indexes (PGM/ALEX) | Diminishing advantage at 10K-10M keys, adversarial vulnerability |
| Symmetric hash join | Streaming-oriented, wrong tradeoff for batch queries |
| LLM-generated estimators | Requires external infrastructure |

---

## Open Questions

1. **High-cardinality statistics accuracy**: Does Razordata's fixed 10K reservoir sample + 256-bucket histogram provide sufficient accuracy for high-cardinality columns (100K+ distinct values) compared to PostgreSQL's configurable `default_statistics_target`? [F3:7, F4:10]

2. **Correlation statistics for IndexScan vs SeqScan**: Should Razordata adopt PostgreSQL's correlation statistic to improve IndexScan vs SeqScan decisions when data is physically ordered? [F3:10]

3. **Functional dependency statistics**: Can Razordata's `LearnedModel.correlations` tracking be extended to provide functional-dependency statistics without PostgreSQL's `CREATE STATISTICS` overhead? [F4:8]

4. **Planning-time cost of N3 at scale**: What is the planning-time cost of Razordata's N3 with `n3HeapMaxSize=24` versus SQLite's N=12-18 for 8+ way joins? [F8:12]

5. **CROSS JOIN escape hatch**: Should Razordata add a CROSS JOIN escape hatch (like SQLite) for developer-controlled join ordering, or does multi-start N3 suffice? [F1:3]

6. **LEO convergence in single-connection engines**: How many queries are needed for LEO-style corrections to reduce median Q-error below 2x in Razordata's specific workload patterns? [N2, N19]

7. **Bloom filter applicability at embedded scale**: Does the 32.8% latency reduction from Bloom filter pre-filtering transfer to embedded-scale workloads (MB-GB range)? [N6]

8. **effective_cache_size for embedded**: What is the optimal `effective_cache_size` value for an in-process embedded engine where all data is in Go-managed memory? [RPT]

9. **COMPASS sketch composition at embedded scale**: Can attribute-level sketches be composed incrementally during N3's bottom-up heap expansion without materializing full join results, and what is the memory overhead for a 4KB-page embedded engine? [N14]

10. **Cost model calibration**: What calibration data is needed for Razordata's cost model, and can TPC-H/TPC-DS derived coefficients improve plan quality over PostgreSQL-default heuristics? [N13]

---

## Sources

### Original Sources (SQLite vs PostgreSQL)

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

### Modern DBMS Sources (New)

N1. Salles, M.A.V. (2001). "LEO: An Autonomic Query Optimizer for DB2." VLDB. https://www.csd.uoc.gr/~hy460/pdf/leo-db2.pdf
N2. Zhao, Z. et al. (2025). "TiCard: A Correction-based Framework for Cardinality Estimation." https://arxiv.org/abs/2512.14358
N3. IDCC (2025). "DBSel-CV: Correction-based Cardinality Estimation for Lightweight Engines." https://doi.org/10.1145/3783779.3783794
N4. Graefe, G. (1995). "The Cascades Framework for Query Optimization." IEEE Data Engineering Bulletin 18(3). https://www.microsoft.com/en-us/research/publication/the-cascades-framework-for-query-optimization/
N5. Yang, Z., Zou, Z., Zhao, J. (2025). "NeuSO: Neural Query Optimizer." SIGMOD 2026. https://arxiv.org/html/2509.23775v1
N6. Zeyl et al. (2025). "Integrating Bloom Filters into Cost-Based Optimization." https://arxiv.org/abs/2505.02994
N7. DuckDB Source — JoinHashTable. https://raw.githubusercontent.com/duckdb/duckdb/main/src/include/duckdb/execution/join_hashtable.hpp
N8. PostgreSQL 14 Documentation — Memoize Node. https://www.postgresql.org/docs/14/runtime-config-query.html
N9. Cormode, G. & Muthukrishnan, S. (2005/2015). "An Improved Data Stream Summary: The Count-Min Sketch and its Applications." Survey. https://arxiv.org/abs/1511.00793
N10. Li, G. et al. (2024). "QSketch: Quantized Sketch for Cardinality Estimation." KDD 2024. https://arxiv.org/abs/2406.19143
N11. ALEX adversarial complexity attacks. https://arxiv.org/abs/2403.12433
N12. Disk-based learned indexes underperformance. https://arxiv.org/abs/2305.01237
N13. Reflect.json — cost model calibration gap. 2026-07-09.
N14. Kipf, A. et al. (2021). "COMPASS: Sketch-Based Query Optimization." SIGMOD 2021. https://arxiv.org/abs/2102.02440
N15. Kipf et al. (2021). COMPASS JOB benchmark results (1.35x–11.28x speedup). SIGMOD 2021.
N16. Heddes, M. et al. (2024). "Fast Sketch Composition for Join Cardinality Estimation." SIGMOD 2024. https://arxiv.org/abs/2402.15953
N17. Yang, Z., Zhao, J., Yu, W., Koutris, A. (2023). "Predicate Transfer for Multi-Way Joins." https://arxiv.org/abs/2307.15255
N18. Ma, Z. (2023). "SieveJoin: Bloom Filter Propagation for Multi-Way Joins." https://arxiv.org/abs/2308.16370
N19. PostgreSQL 8.3 Documentation — LEO Introduction. https://www.postgresql.org/docs/8.3/static/geqo-pg-intro.html
N20. Zhang, H. et al. (2016). "TuNao: Deep Learning for Cardinality Estimation." SIGMOD 2016. https://doi.org/10.1145/2882903.2903720
N21. Bao, Z. et al. (2020). "Bao: Making Learned Query Optimization Practical." SIGMOD 2021. https://arxiv.org/abs/2004.03814
N22. Oracle 19c Documentation — Adaptive Plans. https://docs.oracle.com/en/database/oracle/oracle-database/19/tgsql/
N23. Microsoft Learn — Adaptive Joins. https://learn.microsoft.com/en-us/sql/relational-databases/performance/adaptive-joins
N24. CAM (2026). "Cache-Aware Cost Model for Learned Indexes." https://arxiv.org/abs/2606.21924
N25. Kraska, T. et al. (2019). "The Case for Learned Index Structures." https://arxiv.org/abs/1905.08898
N26. Ferragina, P. & Vinciguerra, G. (2019). "The PGM-Index: A Fully-Dynamic Optimal Space-Time Tradeoff." PVLDB 2020. https://arxiv.org/abs/1910.06169
N27. Kraska, T. et al. (2020). "ALEX: An Adaptive Learned Index Structure." SIGMOD 2020. https://arxiv.org/abs/1905.08898
N28. Ferragina, P. & Vinciguerra, G. (2024). "PGM++: An Improved PGM-Index." https://arxiv.org/abs/2410.00846
N29. PGM-index asymptotic analysis at embedded cardinalities (author derivation).
N30. Learned index poisoning attacks. https://arxiv.org/abs/2604.24975
N31. Shanbhag, A. & Sudarshan, S. (2014). "Optimizing Join Enumeration in Transformation-based Query Optimizers." VLDB 7. http://www.vldb.org/pvldb/vol7/p1243-shanbhag.pdf
N32. Li, P. et al. (2026). "On the Predictive Power of Q-Error for Plan Quality." https://arxiv.org/abs/2606.15600
