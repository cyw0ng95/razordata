# Razordata Requirements

> Feature catalog for future iteration planning. Select rows from the
> TBD section to form new iterations.
>
> **Sections**: TBD (pending) → DONE (shipped).

## TBD

Columns for selection:
- **Priority**: critical / high / medium / low
- **Effort**: rough size in person-days (S = ≤1, M = 1-3, L = 3-7, XL = 7+)
- **Deps**: prerequisite REQs that must ship first
- **Touches**: packages / files that need to change

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
|---|---|---|---|---|---|---|
| REQ000148 | ENG | BloomFilter double-hashing with FNV-1a seeds (documented in design but implementation uses different hash) | medium | M | iter-04 (bloom) | `ENG/LS/sst_writer.go`, `ENG/LS/sst_reader.go` — align with design spec |
| REQ000156 | SQL | Executor cost model integration (design mentions cost estimation, no operator selection based on cost) | medium | M | iter-08 (planner) | `SQL/EX/planner.go` — use cost for operator selection |
| REQ000159 | TXN | Per-thread arena lazy initialization via `sync.Pool` (design specifies, verify implementation) | medium | M | iter-05 (arena) | `TXN/MV/arena.go` — add lazy init, exhaustion handling |
| REQ000160 | WAL | Batch commit with `sync.WaitGroup` and write barrier (design in FL cluster) | medium | M | iter-03 (WAL) | `WAL/FL/fl.go` — `BatchSync` implementation |
| REQ000161 | MEM | Clock-sweep integration details (atomic hand, refKey update, eviction gating) | medium | S | iter-02 (buffer pool) | audit `MEM/BF/bf.go` — verify matches design |
| REQ000162 | SQL | Plan memoization with SHA256(AST binary encoding) | low | M | iter-08 (planner) | `SQL/PL/memo.go` — canonical AST serialization |
| REQ000164 | TXN | Epoch manager background goroutine (100ms interval, drain coordination) | high | M | iter-05 (epoch) | `TXN/LC/epoch.go` — add background goroutine if missing |
| REQ000165 | ENG | Compaction job scheduling based on level size budget (design mentions, verify trigger logic) | medium | M | iter-04 (compaction) | `ENG/LS/compaction.go` — size budget monitoring |
| REQ000175 | TXN/LC | Fix hazard pointer Publish (store to single slot, not all) and implement actual memory reclamation | high | L | iter-05 (hazard/epoch) | `TXN/LC/hazard.go` fix Publish, `TXN/LC/epoch.go` implement Reclaim wait+free per TXN.md:96-121 |
| REQ000181 | TXN/LC | Fix goroutine ID tracking (use real goroutine identity, not atomic counter) | medium | M | iter-05 (epoch) | `TXN/LC/epoch.go` — proper goroutine tracking per TXN.md:113-121 |
| REQ000182 | SQL/EX | Parallel Sort implementation (sample sort for top-k, external merge for large datasets) | medium | L | iter-08 (Sort) | Create `SQL/EX/sort_parallel.go` per SQL.md:367-372 |
| REQ000183 | SQL/EX | Expression evaluation SIMD (batch predicate EvalBatch function) | medium | M | iter-08 (eval) | `SQL/EX/eval.go` — add vectorized EvalBatch per SQL.md:303-306 |
| REQ000185 | SQL/EX | Plan memoization with SHA256 canonical AST binary encoding (not JSON) | low | M | iter-08 (planner) | `SQL/PL/memo.go` — implement binary serialization per SQL.md:215 |
| REQ000126 | SQL | Foreign keys (REFERENCES, ON DELETE/UPDATE) | high | L | iter-11 (UNIQUE), iter-12 (catalog), iter-21 (FKEY index?) | `SQL/PS`, `SQL/EX/constraints.go`, new FK validation in writers |
| REQ000143 | QUAL | `SQL/RE` coverage: 49% → 80%+ | high | M | iter-07 (RE implementation) | `SQL/RE/*_test.go` — fill error-path branches, subquery flatten cases |
| REQ000074 | SQL | `IndexScan` real seek (replace prefix-scan fallback) | high | M | iter-08 (IndexScan op) | `SQL/EX/operators.go` — call into real `ENG/ID/` once iter-21 ships, or stub |
| REQ000034 | WAL | WAL compression (lz4) | low | M | iter-03 (WAL writer) | `WAL/WR/encode.go` |
| REQ000045 | ENG | Secondary indexes (non-PK columns; lookup by `__idx__:<table>:<col>:<val>`) | low | XL | iter-12 (catalog), iter-21 (ID) | new `ENG/ID/` package, `SQL/PL` index selection |
| REQ000048 | ENG | Table registry persistence (`ENG/TB/`) | medium | L | iter-12 (catalog basic) | new `ENG/TB/tb.go` |
| REQ000049 | ENG | Schema cluster (`ENG/SC/`) split from LS | low | M | iter-04 | new `ENG/SC/sc.go`; move `TableSchema` from LS |
| REQ000050 | ENG | Deparser cluster (`ENG/DP/`) split from LS | low | M | iter-04 | new `ENG/DP/dp.go`; move row/block encoding |
| REQ000064 | TXN | Generational arena (reduce GC pressure vs. single allocation) | low | L | iter-05 (arena) | `TXN/MV/arena.go` |
| REQ000084 | SQL | `RE` subquery planning (not just flatten) | medium | M | iter-07 (RE), iter-08 (Subq op) | `SQL/RE/subq.go`, `SQL/PL/planner.go` |
| REQ000086 | SQL | Parallel query execution (operators in goroutines, merge via channel) | low | XL | iter-08 (operators) | `SQL/EX/ex.go` — channel-based Next; cancellation hygiene |
| REQ000100 | SYS | Network server (TCP/gRPC listener; `SYS.Serve()`) | low | XL | iter-12 (catalog) | new `SYS/SV/sv.go`, protocol buffer or simple line protocol |
| REQ000101 | SYS | Prometheus metrics endpoint (`/metrics` HTTP) | medium | S | iter-00 (MetricHook), iter-100 (server) | `LOG/HK/metric.go` export, `SYS/SV/sv.go` |
| REQ000128 | OPS | Point-in-time backup / restore (snapshot engine dir, restore to a copy) | medium | M | iter-03 (WAL), iter-04 (manifest) | new `SYS/BK/bk.go`; document procedure |
| REQ000129 | OPS | Online schema migration (`ALTER TABLE ADD/DROP COLUMN` without copy) | low | XL | iter-12 (catalog) | new `SQL/EX/alter.go`, `ENG/LS` schema-aware readers |
| REQ000018 | FIL | File locking (`flock`) for multi-process access | low | S | iter-01 (FIL) | `FIL/FS/fs.go` — optional via `Options`; out of v1 scope (single-process) |
| REQ000244 | SQL/EX | ALTER TABLE executor (online schema migration) | medium | L | REQ000243 | `SQL/EX/alter.go` (new) — `ENG/LS` schema-aware readers |
| REQ000246 | SQL/PS | Parse TRIGGER (`CREATE TRIGGER`, `BEFORE/AFTER`, `FOR EACH ROW`) | low | L | iter-07 | `SQL/PS/ps.go` — `Trigger` AST, `parseTrigger` |
| REQ000247 | SQL/EX | TRIGGER executor (fire on INSERT/UPDATE/DELETE) | low | L | REQ000246 | `SQL/EX/trigger.go` (new) — hook into writers |
| REQ000248 | SQL/PS | Parse generated columns (`AS (expr) STORED/VIRTUAL`) | low | M | iter-12 | `SQL/PS/ps.go` — `ColDef.Generated` field |
| REQ000249 | SQL/EX | Generated column materialization on INSERT/UPDATE | low | M | REQ000248 | `SQL/EX/writers.go` — compute and store generated values |
| REQ000256 | SQL/PS | Parse VACUUM / ANALYZE | medium | S | iter-21 | `SQL/PS/ps.go` — `Vacuum`, `Analyze` AST |
| REQ000284 | ENG/ID | BTree delete rebalancing — no merge/redistribute after delete, tree becomes sparse | high | M | iter-23 | `ENG/ID/id.go:414-437` |
| REQ000285 | ENG/ID | uint32 page ID overflow protection — wraps to 0 (sentinel for "no page") | medium | S | iter-23 | `ENG/ID/id.go:109-113` |
| REQ000286 | SQL/EX | Window materialize context propagation — uses context.Background() instead of caller's ctx | medium | S | iter-23 | `SQL/EX/window.go:55-77` |
| REQ000287 | SQL/EX | Window setOutput allocation optimization — allocates 2 new slices per call on hot path | medium | S | iter-23 | `SQL/EX/window.go:209-218` |
| REQ000292 | SQL/EX | numericArith int64 overflow check — multiplication wraps without check (pre-existing) | medium | S | iter-19 | `SQL/EX/eval.go:646-658` |
| REQ000295 | FIL | io_uring async I/O wrapper (SQ/CQ submission, SQPOLL mode, Linux-only with IOCP/kqueue fallback) | critical | L | iter-01 (FIL), `golang.org/x/sys/unix` available | new `FIL/IO/uring.go`; cross-platform dispatch in `FIL/FS/fs.go` |
| REQ000296 | FIL | Direct I/O + io_uring fixed-file descriptor (bypass OS page cache, reduce fd table lookups) | high | M | REQ000295, iter-01 (O_DIRECT) | `FIL/FS/fs.go` — `IOSQE_FIXED_FILE` flags; integration with `O_DIRECT` fallback |
| REQ000297 | ENG | SST block-level dictionary compression (ZSTD with per-block trained dict, 4KB blocks: 1.5x→3x ratio) | high | M | iter-23 (flate baseline) | `ENG/LS/sst_writer.go` — `dictTrain` per block; `ENG/LS/sst_reader.go` — dict lookup |
| REQ000298 | ENG | LSM-aware cross-block shared dictionary (multiple data blocks in one SST share a trained dict) | medium | M | REQ000297 | `ENG/LS/sst_writer.go` — write dict in SST meta block; reader caches per-SST dict |
| REQ000299 | WAL | WAL columnar encoding (key delta-of-delta + bit-packing, `commitTS` varint; payload volume -60%+) | high | M | iter-03 (WAL) | `WAL/WR/encode.go` — columnar batch encode/decode |
| REQ000300 | ENG | Tier-aware storage scheduler (`Options.StoragePolicy`: hot=NVMe, cold=HDD/S3, hybrid; per-level device hint) | medium | L | iter-04 (LSM), iter-12 (catalog) | `ENG/LS/compaction.go` — `PlacementPolicy` per level; `Options.StoragePolicy` field |
| REQ000301 | WAL | Async fsync + io_uring linked submit (write→fsync chained via `IOSQE_IO_LINK`, lower batch-commit latency) | high | S | REQ000295, iter-17 (BatchSync) | `WAL/WR/fl.go` — `BatchSyncWithUring` |
| REQ000302 | MEM | PMem-aware buffer pool (DRAM hot slots + mmap'd PMem cold slots; `MADV_HUGEPAGE` for 2MB pages) | medium | L | iter-02 (buffer pool) | `MEM/BF/bf.go` — tier selection on `Pin`; `MEM/BF/pmem.go` (new) |
| REQ000303 | MEM | W-TinyLFU admission + SLRU (replaces clock-sweep; +30% hit rate vs LRU; Redis 8 default) | high | M | iter-02 (clock-sweep) | `MEM/BF/wtinylfu.go` (new) — frequency sketch + admission; configurable |
| REQ000304 | MEM | Off-heap large object pool (mimalloc-style size-class bins; bypasses GC for >64KB SST buffers) | medium | L | iter-02, iter-11 (mmap) | `MEM/OF/of.go` (new) — `mmap` + atomic free lists |
| REQ000305 | TXN | Generational arena with Young/Old split (young bump-allocate, old epoch-reclaim; reduces epoch manager pressure) | medium | L | iter-05 (arena), `REQ000064` | `TXN/MV/arena.go` — generation promotion policy |
| REQ000306 | TXN | Stack-allocate version nodes via escape analysis hints (`//go:nosplit` + `noescape()`; zero-GC hot path) | medium | M | iter-05 (arena) | `TXN/MV/node.go` — escape hint annotations; benchmark zero-allocation claim |
| REQ000307 | TXN | MV-OCC timestamp ordering (Silo-style, O(1) per-txn read-set validation; targets 1M+ txn/s on 16 cores) | critical | XL | iter-20 (commit protocol), `REQ000175` | `TXN/MV/occ.go` (new) — `Validation` phase rewritten; conflict-free reorder |
| REQ000308 | TXN | QSBR read path (Quiescent-State-Based Reclamation; readers set flag only, zero atomic load; near-RCU latency) | high | L | iter-05 (epoch) | `TXN/LC/qsbr.go` (new) — replaces `TXN/LC/epoch.go`; quiescent state callbacks |
| REQ000309 | ENG | NUMA-aware data placement (buffer pool slot node id, worker CPU pin, first-touch arena allocation) | high | M | iter-04 (LSM), iter-02 (buffer pool) | `ENG/LS/memtable.go` — `numactl` API integration; `MEM/BF/bf.go` NUMA hint field |
| REQ000310 | SQL | Real SIMD intrinsics for filter/projection (AVX2/AVX-512 `_mm256_cmpgt_epi64`, `_mm256_maskload_epi64`; `golang.org/x/sys/cpu` dispatch) | high | L | REQ000144 (vectorization 1.0) | `SQL/EX/eval.go` — SIMD path with CPUID dispatch; fallback to scalar |
| REQ000311 | SQL | Operator codegen (`go generate` template → specialized Go funcs; inline caches eliminate virtual dispatch) | medium | XL | REQ000310, iter-08 (operators) | `SQL/EX/codegen/` (new) — templated operator skeletons; build tag for codegen |
| REQ000312 | SQL | Vector-aware hash join (Radix partition; SIMD probe of multiple hash buckets in parallel) | high | L | REQ000310, iter-08 (NestedLoopJoin) | `SQL/EX/hashjoin.go` (new) — radix partition + SIMD probe |
| REQ000313 | SQL | Adaptive query compilation (first 2 invocations interpreted, hot path swaps to JIT via `go generate` template; 2-5x OLAP speedup) | medium | XL | REQ000311 | `SQL/EX/adqc.go` (new) — hot-path detector + plan swap |
| REQ000314 | ENG | Columnar SST layout (PAX / hybrid row-columnar; only queried columns decompressed; 75% I/O saving for narrow queries) | medium | XL | REQ000310, iter-04 (SST) | `ENG/LS/sst_writer.go` — PAX block layout; `ENG/LS/sst_reader.go` — column-selective decode |
| REQ000315 | SQL | Learned cardinality estimation (CardinalityNet/MSCN; bootstraps from existing histograms) | medium | L | REQ000085 (histogram), iter-23 (ANALYZE) | `SQL/PL/learned.go` (new) — ONNX runtime or pure-Go MLP; training data from ANALYZE |
| REQ000316 | SQL | Incremental materialized views (auto-maintained aggregation views with query routing) | medium | L | iter-08 (operators), iter-12 (catalog) | `SQL/EX/matview.go` (new); `ENG/LS` triggers on view base tables |
| REQ000317 | WAL | Parallel WAL replay by key-range partition (worker pool; manifest serial; 100GB replay 30s→8s) | high | M | iter-13 (replay) | `WAL/RP/parallel.go` (new) — partition dispatch; ordered manifest apply |
| REQ000318 | ENG | Write-rate-limited compactor (RocksDB-style `rate_limiter`; prevents compaction starvation under write bursts) | high | M | iter-04 (compaction) | `ENG/LS/compaction.go` — `RateLimiter` (token bucket); exposed via `Options` |
| REQ000319 | ENG | Sub-compaction parallelism (split L4+ compaction into key-range sub-jobs; worker pool fan-out) | high | M | iter-04, REQ000318 | `ENG/LS/subcompact.go` (new) — partition + merge result |
| REQ000320 | ENG | Configurable compaction style (`Options.CompactionStyle = leveled \| tiered \| hybrid`; tiered for time-series) | medium | M | iter-04 | `ENG/LS/compaction.go` — strategy interface; `Options.CompactionStyle` field |
| REQ000321 | TXN | Deterministic Simulation Testing framework (FoundationDB-style scheduled threads + simulated clock + simulated disk; millions of random schedules) | high | XL | iter-17 (chaos), iter-13 (recovery) | new `tests/dst/` framework; subsystem-aware simulated drivers |
| REQ000322 | LOG | eBPF runtime tracing export (`ProfileHook` data consumed by eBPF programs for lock contention / I/O queue / GC pause maps) | medium | M | iter-00 (ProfileHook) | `LOG/HK/profile.go` — BPF map publishing; optional `cmd/razor-ebpf` tool |
DONE REQ000350 | SQL/LX | Bitwise operators (`&`, `|`, `^`, `~`) — not in lexer/parser | medium | M | iter-07 (lexer) | `SQL/LX/token.go`, `SQL/LX/lx.go`, `SQL/PS/ps.go` — add tokens + precedence |
DONE REQ000351 | SQL/LX | String concatenation operator (`\|\|`) — not in lexer/parser | low | S | iter-07 (lexer) | `SQL/LX/token.go`, `SQL/PS/ps.go` — add token + binary op |
DONE REQ000352 | SQL/LX | Modulo operator (`%`) — not in lexer/parser | low | S | iter-07 (lexer) | `SQL/LX/token.go`, `SQL/EX/eval.go` — add token + eval |
DONE REQ000353 | SQL/EX | COALESCE as special form (currently only works as function call) | low | S | iter-08 (eval) | `SQL/EX/eval.go` — add COALESCE case in evalFunction |
DONE REQ000354 | SQL/EX | NULLIF as special form (currently only works as function call) | low | S | iter-08 (eval) | `SQL/EX/eval.go` — add NULLIF case in evalFunction |

## Unfixed Bugs (surfaces as requirements)

These rows are bugs that were discovered during a prior iteration but
not fixed in that iteration's scope. The "Touches" column points to
the discovery context. See `AGENTS.md` Bug-To-Requirement Rule.

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
|---|---|---|---|---|---|---|
| REQ000348 | SQL/EX | `Session.Query` returns schema-only `*AP.Rows` (no row data streaming; callers must use unexported `QueryAll`) | medium | M | iter-25 surfacing | `internal/SYS/AP/ap.go` `Session` interface needs `Next()` accessor; `internal/SYS/SE/se.go` returns `&AP.Rows{Cols,Types}` with no streaming |
| REQ000349 | SQL/PS | Missing SQLite builtin scalar functions: `LENGTH`, `TYPEOF`, `UNICODE`, `QUOTE`, `ZEROBLOB`, `RANDOMBLOB`, `HEX`, `SOUNDEX`. Each emits `ps: syntax error` rather than a typed "unsupported" error, so the SLT classifier must fall back to substring matching on `syntax error` | low | XL | iter-25 surfacing (edge probe `TestEdge_Expressions`) | `SQL/EX/eval.go` function dispatch table |
| REQ000356 | SQL/LX | Unary `NOT` as logical operator (currently only works as infix in some contexts; `~` bitwise NOT) | low | S | iter-07 (lexer) | `SQL/LX/token.go`, `SQL/EX/eval.go` |

## DONE

| ID | Subsystem | Requirement | Iteration |
|---|---|---|---|
| REQ000035 | WAL | Corruption recovery policy: detect torn write, skip vs. fail | iter-13 |
| REQ000098 | SYS | Session pooling (`sync.Pool`) | iter-15 |
| REQ000099 | SYS | `ReadOnly` mode in `Options` (skip WAL writes, O_RDONLY opens) | iter-15 |
| REQ000127 | SQL | Catalog persistence across restarts (`CREATE TABLE` / `DROP TABLE` survive `Close`/`Open`) | iter-12 |
| REQ000146 | SYS | 6-phase graceful shutdown sequence per SYS.md:215-282 | iter-14 |
| REQ000152 | SYS | `validateOptions` with field-by-field checks per SYS.md:198-214 | iter-14 |
| REQ000258 | SQL/EX | ANALYZE executor (collect column statistics) | iter-23 |
| REQ000085 | SQL | Histogram-based selectivity (replace uniform distribution) | iter-23 |
| REQ000261 | SYS | Integrity check (`PRAGMA integrity_check`) | iter-23 |
| REQ000272 | WAL | Checksum verification on WAL replay (detect corruption) | iter-23 |
| REQ000257 | SQL/EX | VACUUM executor (reclaim tombstone space, rebuild SST) | iter-23 |
| REQ000259 | SYS | Backup/restore API (snapshot engine dir to copy) | iter-23 |
| REQ000260 | SYS | Admin CLI `razor` (schema dump, vacuum, integrity check) | iter-23 |
| REQ000153 | SYS | Active-tx wait (30s timeout, force-abort on timeout) | iter-14 |
| REQ000147 | TXN | Complete commit protocol (6 phases) | iter-20 |
| REQ000171 | TXN/VL | WAL integration in commit protocol | iter-20 |
| REQ000174 | ENG/LS | BloomFilter FNV-1a double-hash | iter-20 |
| REQ000193 | LOG/HK | MetricHook counters wiring | iter-20 |
| REQ000196 | SQL/EX | HashAggregate in planner (1000-row threshold) | iter-20 |
| REQ000345 | SQL/EX | Empty-table aggregate returns 1 row (not 0) | iter-26 |
| REQ000346 | SQL/EX | Test isolation: EX package-level maps reset | iter-26 |
| REQ000347 | ENG | Silent data loss in LSM flush path | iter-26 |
| REQ000357 | SQL/EX | SELECT without FROM returns 0 rows — wrap in Values op when `s.From==""` | iter-26.1 (v0.26.3) |
| REQ000359 | SQL/EX | String concat NULL semantics — `'a' \|\| NULL` returns NULL | iter-26.1 (v0.26.3) |
| REQ000360 | SQL/EX | Arithmetic NULL semantics — `10 + NULL` returns NULL | iter-26.1 (v0.26.3) |
| REQ000361 | SQL/EX | IS NULL / IS NOT NULL semantics — `NULL IS NULL` true, `NULL IS NOT NULL` false | iter-26.1 (v0.26.3) |
| REQ000362 | SQL/EX | Comparison with NULL — `5 = NULL` returns NULL | iter-26.1 (v0.26.3) |
| REQ000364 | ENG/LS | Negative WaitGroup counter panic in flushManager (REQ000347 followup) — Add(1) before send, enqueueMu serializes with Stop | iter-26.1 (v0.26.3) |
| REQ000365 | tests/sqlcmp/dual | `allProbeCases` undeclared — register `probeCases` in `AllCases()` | iter-26.1 (v0.26.3) |
| REQ000355 | SQL/PS | Aggregate function `GROUP_CONCAT(expr)` — route through AggregateFunc when name is a known aggregate | iter-26.2 (v0.26.4) |
| REQ000363 | SQL/EX | GROUP_CONCAT empty result — empty table returns NULL (after REQ000367 hidden-PK) | iter-26.2 (v0.26.4) |
| REQ000366 | SQL/EX | Subquery planner store threading — `Row.planner` + `currentSubqueryPlanner`; `outerInjector` updates `outer` in place for memoized plans | iter-26.2 (v0.26.4) |
| REQ000367 | SQL/EX | Hidden rowid for tables without PRIMARY KEY — `hiddenPK` flag, atomic `nextRowID` | iter-26.2 (v0.26.4) |
| REQ000368 | SQL/PS | Parser comma-join `FROM a, b` — synthesize CROSS joins for trailing comma-separated tables | iter-26.2 (v0.26.4) |
| REQ000378 | SQL/EX | HAVING with `COUNT(*)` returns 0 rows — evalAggregate resolves `COUNT(*)` (StarExpr arg) to the precomputed column | iter-26.3 (v0.26.5) |
| REQ000379 | SQL/PS | Chained unary minus: `SELECT 5- -5` must equal 10 — regression test for SQLite-compatible `--` comment behavior | iter-26.3 (v0.26.5) |
| REQ000380 | SQL/PS | `NOT LIKE` parser error — parsePostfix now peeks `T_NOT T_LIKE` and dispatches to parseNotLike | iter-26.3 (v0.26.5) |
| REQ000381 | SQL/PS | `NOT IN (subquery)` parser error — same pattern, dispatches to parseNotIn; parseIn refactored to use parseInBody helper | iter-26.3 (v0.26.5) |
| REQ000382 | SQL/EX | Add `ABS`, `HEX`, `ROUND` scalar functions to evalFunction | iter-26.3 (v0.26.5) |
| REQ000383 | SQL/PS/EX | Compound SELECT: UNION, INTERSECT, EXCEPT with correct precedence (INTERSECT binds tighter), trailing ORDER BY/LIMIT/OFFSET apply to compound result; RE rewrite/format support | iter-26.4 (v0.26.6) |
| REQ000197 | SQL/EX | OUTER JOIN executor (LEFT/RIGHT/FULL) | iter-20 |
| REQ000198 | ENG/LS | Skiplist sync.Pool for scratch arrays | iter-20 |
| REQ000201 | QUAL | SQL/PL coverage 30.6% to 98.8% | iter-20 |
| REQ000202 | SQL/PS | Parser CASE/EXISTS tests | iter-20 |
| REQ000206 | SQL/PS | Type tokens (NUMERIC/DATE/TIME/JSON/DECIMAL) | iter-20 |
| REQ000207 | SQL/PS | Parameterized types VARCHAR(N)/DECIMAL(P,S) | iter-20 |
| REQ000208 | SQL/EX | Type affinity system (SQLite-like 5 affinities) | iter-20 |
| REQ000209 | SQL/PS | DEFAULT clause parsing tests | iter-20 |
| REQ000210 | SQL/PS | CHECK constraint parsing | iter-20 |
| REQ000211 | SQL/EX | CHECK constraint enforcement | iter-20 |
| REQ000218 | SQL/EX | HAVING filter (already implemented) | iter-20 |
| REQ000251 | SQL/PS | Parse CREATE INDEX (UNIQUE, multi-column) | iter-22 |
| REQ000252 | SQL/EX | IndexScan operator (real seek via LSM index keyspace) | iter-22 |
| REQ000253 | SQL/PL | Index selection in planner (col=lit equality → real seek) | iter-22 |
| REQ000254 | ENG | Histogram-based selectivity stats (types defined, ANALYZE pending) | iter-22 |
| REQ000229 | SQL/EX | DECIMAL type storage (big.Float) | iter-20 |
| REQ000154 | SYS | Background-goroutine coordination (compaction, flush, epoch, hook dispatcher) | iter-14 |
| REQ000166 | SYS | Per-subsystem `Close()` ordering in Phase 5 of shutdown | iter-14 |
| REQ000178 | SYS/SY | `validateOptions` (duplicate of REQ000152, same code) | iter-14 |
| REQ000155 | ENG | Catalog persistence across restarts | iter-12 (consolidated with REQ000127) |
| REQ000009 | LOG | Log compression after rotation (gzip) | iter-16 |
| REQ000167 | SQL | Parameter binding type coercion (Go int -> BIGINT, string -> INT error) | iter-16 |
| REQ000179 | TXN/VL | Add arena field to transactionSlot struct for per-transaction tracking | iter-16 |
| REQ000180 | ENG/LS | Dynamic BloomFilter sizing ((N *10 +7) /8 bytes) replace fixed4096 bytes | iter-16 |
| REQ000186 | ENG/LS | Fix SST file path mismatch: compaction.fileName produces sst/L<N>_<minkey-hex>_<maxkey-hex>_<id>.sst while flush now emits the same shape | iter-16 |
| REQ000044 | ENG | `ENG/LS` benchmarks (skiplist insert/find, SST write/read, flush, compaction) | iter-17 |
| REQ000138 | QUAL | `Benchmark*` for every storage component (catch any missing) | iter-17 |
| REQ000163 | SQL | Rewriter AST normalization (design mentions, verify completeness) | iter-17 (partial: 65.0%%) |
| REQ000169 | LOG | Debug-level allocation trade-off documentation (level check before allocation) | iter-17 |
| REQ000170 | WAL | RTMerge record encoding implementation | iter-17 |
| REQ000191 | WAL/RP | Coverage lift: WAL/RP is at 75.2% (multi-segment truncate + ErrUnknownRecord added; shortfall now in resync-window edges) | iter-16 |
| REQ000192 | SQL/EX | Adaptive vectorization threshold (auto-fallback to row-at-a-time for tables <100K rows) | iter-19 |
| REQ000194 | LOG/HK | Implement TraceHook for SQL query tracing (start/end with timing) | iter-00 |
| REQ000195 | LOG/HK | Implement ProfileHook (pprof dump on Error events) | iter-00 |
| REQ000199 | MEM/BF | Sharded buffer pool mutex (reduce hash table contention) | iter-02 |
| REQ000200 | WAL/WR | Per-segment locks (replace global write mutex) | iter-03 |
| REQ000203 | QUAL | Missing benchmarks (FIL/LF, LOG/HK, SQL/PS, SQL/PL have 0) | iter-17 |
| REQ000204 | SQL | CREATE INDEX (no implementation, no parser support) | iter-21 |
| REQ000205 | SQL | EXPLAIN SQL syntax (currently only cost calc, not SQL statement) | iter-21 |
| REQ000176 | WAL | Batch commit with sync.WaitGroup and write barrier | iter-17 |
| REQ000184 | WAL | 256 KB pre-allocated writeBuffer for batched WAL writes | iter-17 |
| REQ000144 | SQL | SIMD vectorized execution (batch + 4-wide unrolling + selection vectors) | iter-19 (Phase 1) |
| REQ000149 | SQL | Columnar batch memory management (sync.Pool for 1024-row batches) | iter-19 (Phase 1) |
| REQ000157 | SQL | Expression evaluation SIMD acceleration (batch predicate) | iter-19 (Phase 1) |
| REQ000173 | SQL/EX | SIMD vectorized operators (consolidated into REQ000144) | iter-19 (Phase 1) |
| REQ000145 | SQL | Parallel query execution (worker pool, fan-out/fan-in, channel merge) | iter-19 (Phase 2) |
| REQ000158 | TXN | Hazard pointer publication/clear protocol (split into PublishCurrent/PublishNext) | iter-15 |
| REQ000172 | SYS/SY | 6-phase graceful shutdown implementation per SYS.md:196-283 | iter-14 |
| REQ000150 | SQL | Parallel Sort implementation (sample sort for top-k) | iter-19 (Phase 3) |
| REQ000001 | LOG | `Logger` wraps `log/slog` with atomic level control | iter-00 |
| REQ000002 | LOG | Structured key-value output (JSON/text) | iter-00 |
| REQ000003 | LOG | Log file rotation on size threshold | iter-00 |
| REQ000004 | LOG | Hook registry with async dispatch | iter-00 |
| REQ000005 | LOG | `TraceHook` for SQL query tracing | iter-00 |
| REQ000006 | LOG | `MetricHook` for throughput/latency counters | iter-00 |
| REQ000007 | LOG | `ProfileHook` for CPU/heap dump on error | iter-00 |
| REQ000008 | LOG | Bounded channel: drop on overflow, never block log path | iter-00 |
| REQ000010 | FIL | Block I/O via `pread`/`pwrite` | iter-01 |
| REQ000011 | FIL | `O_DIRECT` support with fallback | iter-01 |
| REQ000012 | FIL | CRC32 checksum per block | iter-01 |
| REQ000013 | FIL | `MetaPage` with magic/version/catalog root | iter-01 |
| REQ000014 | FIL | Path validation (reject `..`, symlinks) | iter-01 |
| REQ000015 | FIL | Cached directory FDs for `SyncDir` | iter-01 |
| REQ000016 | FIL | WAL segment handle pool | iter-01 |
| REQ000017 | FIL | `ftruncate` for replay segment shrinking | iter-01 |
| REQ000020 | MEM | Buffer pool: clock-sweep LRU eviction | iter-02 |
| REQ000021 | MEM | Atomic `Pin`/`Unpin` with eviction gating | iter-02 |
| REQ000022 | MEM | O(1) hash table lookup by `blockID` | iter-02 |
| REQ000023 | MEM | Hint file for warm startup | iter-02 |
| REQ000024 | MEM | Hint file compression (gzip) when > 1 MB | iter-02 |
| REQ000025 | MEM | `sync.Pool` for page/iterator buffers | iter-02 |
| REQ000027 | WAL | Sequential append with LSN allocation | iter-03 |
| REQ000028 | WAL | 64 MB segment rotation | iter-03 |
| REQ000029 | WAL | `fsync` on commit | iter-03 |
| REQ000030 | WAL | Batch flush / write barrier | iter-03 |
| REQ000031 | WAL | WAL replay on startup | iter-03 |
| REQ000032 | WAL | Checkpoint detection and segment truncation | iter-03 |
| REQ000033 | WAL | `RTCheckpoint` record with catalog root | iter-03 |
| REQ000036 | ENG | Lock-free skiplist memtable | iter-04 |
| REQ000037 | ENG | Memtable freeze + flush to L0 SST | iter-04 |
| REQ000038 | ENG | SST writer: data blocks, index, bloom, footer | iter-04 |
| REQ000039 | ENG | SST reader: block decode, bloom check, index binary search | iter-04 |
| REQ000040 | ENG | Leveled compaction with multi-way merge sort | iter-04 |
| REQ000041 | ENG | Atomic manifest versioning (temp + rename + `fsync`) | iter-04 |
| REQ000042 | ENG | Bloom filter (10 bits/key, double-hashing) | iter-04 |
| REQ000043 | ENG | Delta-encoded data blocks with restart points | iter-04 |
| REQ000046 | ENG | IndexScan operator exists (prefix-scan fallback) | iter-08 |
| REQ000051 | TXN | MVCC version chain (lock-free, CAS insertion) | iter-05 |
| REQ000052 | TXN | Per-thread arena allocation | iter-05 |
| REQ000053 | TXN | Hazard pointer coordination | iter-05 |
| REQ000054 | TXN | Epoch-based reclamation | iter-05 |
| REQ000055 | TXN | Read view per transaction | iter-05 |
| REQ000056 | TXN | Commit protocol: validate → assign `commitTS` → CAS `endTS` | iter-06 |
| REQ000057 | TXN | Write-write conflict detection | iter-06 |
| REQ000058 | TXN | Transaction slots (max 1024 concurrent) | iter-06 |
| REQ000059 | TXN | Shadow writeSet for ROLLBACK | iter-06 |
| REQ000060 | TXN | Read-uncommitted isolation (v1) | iter-09 |
| REQ000063 | TXN | Savepoint support | iter-09 |
| REQ000065 | SQL | Lexer with keyword map | iter-07 |
| REQ000066 | SQL | Recursive-descent parser | iter-07 |
| REQ000067 | SQL | AST node types for DDL/DML/SELECT | iter-07 |
| REQ000068 | SQL | Constant folding | iter-07 |
| REQ000069 | SQL | Predicate pushdown | iter-07 |
| REQ000070 | SQL | Subquery flattening (IN/EXISTS) | iter-07 |
| REQ000071 | SQL | Plan memoization (SHA256 of AST) | iter-08 |
| REQ000072 | SQL | Cost estimation (uniform distribution) | iter-08 |
| REQ000073 | SQL | `SeqScan` operator | iter-08 |
| REQ000075 | SQL | `Filter` / `Project` / `Sort` / `Limit` operators | iter-08 |
| REQ000076 | SQL | `Insert` / `Update` / `Delete` operators | iter-08 |
| REQ000077 | SQL | `Aggregate` (COUNT/SUM/AVG/MIN/MAX) | iter-08 |
| REQ000078 | SQL | `HashAggregate` | iter-08 |
| REQ000079 | SQL | `NestedLoopJoin` (INNER/CROSS) | iter-08 |
| REQ000080 | SQL | `Distinct` operator | iter-08 |
| REQ000081 | SQL | `EXPLAIN` rendering | iter-08 |
| REQ000082 | SQL | Subquery operator (IN/EXISTS/scalar) | iter-08 |
| REQ000087 | SYS | `Engine.Open` with `Options` validation | iter-09 |
| REQ000088 | SYS | `Engine.Close` with graceful shutdown | iter-09 |
| REQ000089 | SYS | `Engine.Begin` → `Session` | iter-09 |
| REQ000090 | SYS | `Engine.Stats` aggregation | iter-09 |
| REQ000091 | SYS | Error type taxonomy (retryable vs fatal) | iter-09 |
| REQ000092 | SYS | `Session.Query` / `Session.Exec` | iter-09 |
| REQ000093 | SYS | `Session.SetDeadline` with `atomic.Value` | iter-09 |
| REQ000094 | SYS | `Transaction.Commit` / `Rollback` | iter-09 |
| REQ000095 | SYS | `Transaction.Savepoint` / `RollbackTo` | iter-09 |
| REQ000096 | SYS | `Stmt.Prepare` / `Query` / `Exec` / `Close` | iter-09 |
| REQ000097 | SYS | SIGTERM/SIGINT graceful shutdown handler | iter-09 |
| REQ000103 | DDL | `CREATE TABLE` with column types and `PRIMARY KEY` | iter-08 |
| REQ000104 | DDL | `DROP TABLE` | iter-08 |
| REQ000105 | DDL | `NOT NULL` constraint enforcement | iter-10 |
| REQ000106 | DDL | `DEFAULT` value substitution | iter-10 |
| REQ000107 | SQL | `UNIQUE` constraint (in-memory path) | iter-11 |
| REQ000019 | MEM | `MADV_DONTNEED` hints for buffer eviction | iter-11 |
| REQ000026 | FIL | `mmap` BlockDevice for SST reads | iter-11 |
| REQ000108 | DML | `INSERT` with column list | iter-08 |
| REQ000109 | DML | `UPDATE` with `WHERE` | iter-08 |
| REQ000110 | DML | `DELETE` with `WHERE` | iter-08 |
| REQ000111 | DML | `SELECT` with `WHERE` / `ORDER BY` / `LIMIT` / `OFFSET` | iter-08 |
| REQ000112 | DML | `COUNT(*)` / `SUM` / `AVG` / `MIN` / `MAX` aggregates | iter-08 |
| REQ000114 | DML | `DISTINCT` | iter-08 |
| REQ000115 | DML | `IN` / `EXISTS` / scalar subqueries | iter-08 |
| REQ000116 | DML | `JOIN` (INNER, CROSS) | iter-08 |
| REQ000118 | DML | `LIKE` pattern matching | iter-07 |
| REQ000119 | DML | `BETWEEN` | iter-07 |
| REQ000120 | DML | `IS NULL` / `IS NOT NULL` | iter-07 |
| REQ000121 | TXN-API | `BEGIN` / `COMMIT` / `ROLLBACK` | iter-08 |
| REQ000124 | API | Parameter binding via `?` placeholders | iter-08 |
| REQ000125 | API | `EXPLAIN <query>` | iter-08 |
| REQ000130 | OBS | Query tracing via `TraceHook` | iter-00 |
| REQ000131 | OBS | Latency histograms via `MetricHook` | iter-00 |
| REQ000132 | OBS | CPU/heap profiling on error | iter-00 |
| REQ000133 | OBS | Structured stats aggregation (`Engine.Stats`) | iter-09 |
| REQ000134 | QUAL | `go vet ./...` zero warnings | all |
| REQ000135 | QUAL | `gofmt -s -l .` no drift | all |
| REQ000136 | QUAL | `go test ./... -race -count=1` all green | all |
| REQ000137 | QUAL | Property-based tests for storage (crash/recovery) | all |
| REQ000139 | QUAL | No allocations in hot paths | all |
| REQ000140 | QUAL | All public API methods goroutine-safe | iter-09 |
| REQ000141 | QUAL | `log/slog` only — no `fmt.Printf` in library | all |
| REQ000142 | QUAL | Error messages: lowercase, no trailing punctuation | all |
| REQ000230 | SQL/PS | Parse WITH clause (CTE: `WITH x AS (...) SELECT...`) | iter-21 |
| REQ000231 | SQL/PL | CTE planner (materialization vs inline expansion) | iter-21 |
| REQ000232 | SQL/PS | Parse ON CONFLICT clause (`INSERT ... ON CONFLICT DO NOTHING/UPDATE`) | iter-21 |
| REQ000233 | SQL/EX | UPSERT executor (`INSERT...ON CONFLICT`) | iter-21 |
| REQ000234 | SQL/PS | Parse RETURNING clause (`INSERT/UPDATE/DELETE ... RETURNING col`) | iter-21 |
| REQ000235 | SQL/EX | RETURNING executor (return rows from DML) | iter-21 |
| REQ000238 | SQL/PS | Parse SAVEPOINT / RELEASE / ROLLBACK TO | iter-21 |
| REQ000239 | TXN/VL | Savepoint implementation (nested transaction markers) | iter-21 |
| REQ000273 | SQL/PS | Add `EXPLAIN` and `EXPLAIN QUERY PLAN` keyword tokens | iter-21 |
| REQ000274 | SQL/PS | Parse `EXPLAIN [QUERY PLAN] <stmt>` prefix syntax | iter-21 |
| REQ000275 | SQL/PS | `ExplainStmt` AST (`Mode` enum, `Inner` statement) | iter-21 |
| REQ000276 | SQL/PL | `PlanNode` tree wrapper (type, cost, rows, children) | iter-21 |
| REQ000277 | SQL/PL | Visitor pattern: emit PlanNodes during planner tree construction | iter-21 |
| REQ000278 | SQL/PL | Per-operator cost annotation (`cost=N rows=N width=N`) | iter-21 |
| REQ000279 | SQL/EX | `EXPLAIN` execution path: skip row execution, return plan as result-set | iter-21 |
| REQ000280 | SQL/EX | EXPLAIN on DML (INSERT/UPDATE/DELETE) returns execution plan | iter-21 |
| REQ000281 | SQL/EX | EXPLAIN QUERY PLAN formatter (tree-style, human-readable) | iter-21 |
| REQ000236 | SQL/PS | Parse window functions (`OVER`, `PARTITION BY`, `ROW_NUMBER`, `RANK`) | iter-23 |
| REQ000237 | SQL/EX | Window function executor (`ROW_NUMBER`, `RANK`, `SUM OVER`, `LAG`, `LEAD`) | iter-23 |
| REQ000262 | SQL/PS | Add DATE / TIME / TIMESTAMP type tokens | iter-23 |
| REQ000263 | SQL/EX | DATE / TIME / TIMESTAMP value storage and arithmetic | iter-23 |
| REQ000264 | SQL/PS | Add JSON type and parse `->`, `->>`, `json_extract` | iter-23 |
| REQ000265 | SQL/EX | JSON value storage and `json_extract` executor | iter-23 |
| REQ000250 | ENG/ID | B-tree secondary index package (foundation) | iter-23 |
| REQ000047 | ENG | Prefix bloom filters for range scans | iter-23 |
| REQ000271 | ENG/LS | Compression for SST blocks (flate) | iter-23 |
| REQ000282 | SQL/EX | Fix computeRank RANK for tied rows | iter-23 |
| REQ000283 | ENG/ID | Fix Cursor.Next() leaf boundary traversal | iter-23 |
| REQ000293 | QUAL | Window operator test coverage (window_test.go) | iter-23 |
| REQ000294 | QUAL | Cross-leaf cursor test coverage | iter-23 |
| REQ000123 | TXN-API | Configurable isolation levels (SET TRANSACTION) | iter-24 |
| REQ000255 | TXN | Read-committed per-statement snapshot | iter-24 |
| REQ000062 | TXN | MVCC own-writes visibility in transactions | iter-24 |
| REQ000061 | TXN | Read-committed as default isolation level | iter-24 |
| REQ000291 | SQL/PS | EXCLUDED.col reference in ON CONFLICT DO UPDATE | iter-24 |
| REQ000240 | SQL/PS | Parse CREATE VIEW | iter-24 |
| REQ000241 | SQL/PL | View resolution (inline expansion) | iter-24 |
| REQ000243 | SQL/PS | Parse ALTER TABLE ADD/DROP COLUMN/RENAME | iter-24 |
| REQ000288 | SQL/PS | parseInterval unit validation | iter-24 |
| REQ000290 | SQL/EX | LAG/LEAD arbitrary offset support | iter-24 |
| REQ000270 | SQL/PS | FETCH FIRST n ROWS ONLY | iter-24 |
| REQ000242 | SYS | Pragmas (cache_size, journal_mode, synchronous) | iter-24 |
| REQ000323 | TEST | SQLLogicTest corpus mirror as git submodule (`tests/sqlcmp/corpus` from `MarvBeer/sqlite-test-suite`); build-tag-gated fetch | iter-25 |
| REQ000324 | TEST | SLT test file parser (`statement ok\|error`, `query <types> <sort> <label>`, `halt`, `hash-threshold`, `skipif`, `onlyif`; tolerant, line-oriented) | iter-25 |
| REQ000325 | TEST | Driver interface (`Connect`/`Close`/`Exec`/`Query`; returns `ResultSet` with typed `Value` cells) | iter-25 |
| REQ000326 | TEST | Razordata driver implementation wrapping public `internal/SYS.Engine` API; SQL errors classified as `skipped` not `failed` | iter-25 |
| REQ000327 | TEST | SLT runner + type-aware result diff (`T`/`I`/`R`/`NULL`; `nosort`/`rowsort`/`valuesort`; `label` grouping) | iter-25 |
| REQ000328 | TEST | Corpus subset default target (~200 `.test` files from `select1-4`, `index/`, `evidence/`, `minmax/`, `cast/`, `null/`, `decimal/`, `datetime/`); calibrated pass-rate threshold; full corpus gated by `slt_corpus_full` tag | iter-25 |
| REQ000329 | TEST | Pure-Go reference oracle via `modernc.org/sqlite` dependency (test-only); fallback to hand-rolled `tests/sqlcmp/oracle/` if modernc fails to build in sandbox | iter-25 |
| REQ000330 | TEST | Dual runner: same SQL on Razordata + oracle; result-set diff after normalization | iter-25 |
| REQ000331 | TEST | Result-set normalization (int→int64, float round, strip whitespace, column-name sort, `''`/`NULL` config flag) | iter-25 |
| REQ000332 | TEST | Dual case authoring convention (`dualCase` struct, table-driven; seed ~50 cases across DDL/DML/aggregates/joins) | iter-25 |
| REQ000333 | TEST | JUnit XML output for CI consumption (pass/fail/skip per testcase) | iter-25 |
| REQ000334 | TEST | Coverage snapshot (`tests/sqlcmp/slt/coverage.json` per run; `coverage.baseline.json` committed; regression check) | iter-25 |
| REQ000335 | TEST | CI workflow: PR + nightly; run subset; upload JUnit; post pass-rate PR comment vs `main` | iter-25 (deferred — no `.github/` in repo) |
| REQ000336 | TEST | Developer guide: how to run, add cases, re-baseline coverage | iter-25 |
| REQ000337 | TEST | Architecture note lives in iteration doc (not `design/`); test harness is operational, not architectural | iter-25 |

---

## Newly Discovered Bugs (2026-06-12, dual-runner expansion)

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
|---|---|---|---|---|---|---|
| REQ000358 | SQL/PS | XOR operator (`^`) parser gap — lexer emits `T_BITXOR` but parser doesn't recognize in precedence table | high | S | iter-07 (lexer) | `SQL/PS/ps.go` — add `T_BITXOR` to binary operator switch |

> The following bugs from the 2026-06-12 dual-runner pass were
> resolved in iter-26.1 (v0.26.3): REQ000357 (SELECT no-FROM),
> REQ000359 (concat NULL), REQ000360 (arith NULL), REQ000361
> (IS NULL semantics), REQ000362 (= NULL), REQ000364 (flush
> WaitGroup), REQ000365 (allProbeCases undeclared). They are
> now in the DONE table.
>
> The following bugs from the SLT corpus / dual-runner
> expansion were resolved in iter-26.2 (v0.26.4):
> REQ000355 (GROUP_CONCAT dispatch), REQ000363 (GROUP_CONCAT
> empty → NULL), REQ000366 (subquery store threading),
> REQ000367 (hidden PK for no-PK tables), REQ000368 (comma-
> join). They are now in the DONE table.

## Newly Discovered Bugs (2026-06-13, v0.26.5 candidate pool)

Probing a wider SLT-style test set surfaced the following bugs.
Severity and effort are estimated; final scope for v0.26.5 is
decided per release. See iter-26.3 planning doc (when written)
for the chosen subset.

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
|---|---|---|---|---|---|---|

## Newly Discovered Bugs (2026-06-12, SLT corpus run)

The SLT corpus from `jzombie/sqlite-sqllogictest-corpus` was copied to `tests/sqlcmp/corpus/test/`.
Running `select1.test` (12K lines, ~3K query records) against the engine shows:

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
|---|---|---|---|---|---|---|

---

## SQLite Core Function Coverage (https://sqlite.org/lang_corefunc.html)

Goal: full coverage of the 60 functions on SQLite's core scalar
function page, each with a positive test case in
`internal/SQL/EX/corefunc_test.go` (table-driven, named cases so
the dual-runner and SLT harness can pinpoint the missing one).
Status column tracks the implementation state.

| Function | Status | Notes |
|---|---|---|
| `abs(X)` DONE REQ000384 | returns absolute value, NULL→NULL, string→0.0, MIN_INT64→error |
| `changes()` DONE REQ000385 | last INSERT/UPDATE/DELETE row count; not yet wired to session state |
| `char(X1,...,XN)` DONE REQ000386 | Unicode code point → character; accepts variadic int args |
| `coalesce(X,Y,...)` | REQ000418 (DONE) | iter-26 already implemented |
| `concat(X,...)` DONE REQ000387 | concatenate non-NULL args; all-NULL → "" (note: current `\|\|` returns NULL on NULL) |
| `concat_ws(SEP,X,...)` DONE REQ000388 | concat with separator; SEP=NULL → NULL |
| `format(FORMAT,...)` DONE REQ000389 | printf-style formatting (subset of fmt verbs) |
| `glob(X,Y)` DONE REQ000390 | filename glob match (X=pattern, Y=string) |
| `hex(X)` DONE REQ000391 | BLOB/text → uppercase hex; integer is converted via text first |
| `ifnull(X,Y)` | REQ000419 (DONE) | iter-26 |
| `iif(B1,V1,...)` DONE REQ000392 | short-circuit CASE; `if()` alias |
| `instr(X,Y)` DONE REQ000393 | position of Y in X (1-based), 0 if not found |
| `last_insert_rowid()` DONE REQ000394 | engine-level rowid; engine must expose per-session counter |
| `length(X)` | REQ000420 (DONE) | iter-26 — returns code-point count (not bytes); close to SQLite's semantics |
| `like(X,Y[,Z])` | REQ000421 (DONE) | iter-26 — two-arg form; ESCAPE clause not yet supported |
| `likelihood(X,Y)` DONE REQ000395 | no-op pass-through; hint to planner |
| `likely(X)` DONE REQ000396 | no-op pass-through |
| `load_extension(X[,Y])` | REQ000428 (SKIP) | not in v1 scope; would require CGO bridge |
| `lower(X)` | REQ000422 (DONE) | iter-26 |
| `ltrim(X[,Y])` DONE REQ000397 | trim left; default Y=" " |
| `max(X,Y,...)` DONE REQ000398 | multi-arg scalar max; uses first collating function |
| `min(X,Y,...)` DONE REQ000399 | multi-arg scalar min |
| `nullif(X,Y)` | REQ000423 (DONE) | iter-26 |
| `octet_length(X)` DONE REQ000400 | byte length; differs from `length` for UTF-8 |
| `printf(FORMAT,...)` | REQ000424 (DONE) | alias for `format`; merge with REQ000389 |
| `quote(X)` DONE REQ000401 | SQL literal rendering; strings single-quoted with escape, BLOBs as X'hex' |
| `random()` DONE REQ000402 | pseudo-random int64; exclude MIN_INT64 |
| `randomblob(N)` DONE REQ000403 | N-byte random BLOB |
| `replace(X,Y,Z)` DONE REQ000404 | string substitution; Y="" returns X unchanged |
| `round(X[,Y])` DONE REQ000405 | round to Y decimal places; Y default 0; Y<0 → 0 |
| `rtrim(X[,Y])` DONE REQ000406 | trim right; default Y=" " |
| `sign(X)` DONE REQ000407 | -1/0/+1 or NULL for non-numeric |
| `soundex(X)` DONE REQ000408 | soundex encoding; "?000" for non-ASCII / NULL |
| `sqlite_compileoption_get(N)` | REQ000429 (SKIP) | engine-internal, returns NULL for v1 |
| `sqlite_compileoption_used(X)` | REQ000430 (SKIP) | engine-internal, returns 0 for v1 |
| `sqlite_offset(X)` | REQ000431 (SKIP) | requires SQLITE_ENABLE_OFFSET_SQL_FUNC compile flag |
| `sqlite_source_id()` DONE REQ000409 | fixed string for v1 ("razordata-v0.26.x") |
| `sqlite_version()` DONE REQ000410 | fixed string for v1 ("0.26.x") |
| `substr(X,Y[,Z])` | REQ000425 (DONE) | iter-26 — 1-based, negative start counts from right |
| `substring(X,Y[,Z])` | REQ000426 (DONE) | alias for `substr` |
| `total_changes()` DONE REQ000411 | cumulative row-change count since connection open |
| `trim(X[,Y])` | REQ000427 (DONE) | iter-26 — both sides; default Y=" " |
| `typeof(X)` DONE REQ000412 | returns "null" / "integer" / "real" / "text" / "blob" |
| `unhex(X[,Y])` DONE REQ000413 | hex → BLOB; X invalid → NULL; Y is ignored-char set |
| `unicode(X)` DONE REQ000414 | code point of first char; NULL → NULL |
| `unistr(X)` DONE REQ000415 | backslash-escape decoder (\uXXXX, \+XXXXXX, \UXXXXXXXX) |
| `unistr_quote(X)` | REQ000432 (SKIP) | low-value, complex |
| `unlikely(X)` DONE REQ000416 | no-op pass-through |
| `upper(X)` | REQ000433 (DONE) | iter-26 |
| `zeroblob(N)` DONE REQ000417 | N-byte BLOB of 0x00 |

### Sub-bundle REQs (to-implement functions only)

| ID | Function | Effort |
|---|---|---|
### Already implemented (iter-26.9)

This iteration adds 5 small lexer/parser features:

| REQ ID | Function | Notes |
|---|---|---|
| REQ000350 | bitwise operators | &, \|, ^, ~ (already in lexer/parser/eval) |
| REQ000351 | concat operator | \|\| (already in lexer/parser/eval) |
| REQ000352 | modulo operator | % (already in lexer/parser/eval) |
| REQ000353 | COALESCE special form | now works without parens |
| REQ000354 | NULLIF special form | now works without parens |

### Already implemented (iter-26.8)

This iteration completes 7 additional core scalar functions:

| REQ ID | Function | Notes |
|---|---|---|
| REQ000390 | glob | pattern matching with *, ?, [...] |
| REQ000391 | hex | string to uppercase hex (iter-26) |
| REQ000395 | likelihood | no-op planner hint |
| REQ000396 | likely | no-op planner hint |
| REQ000405 | round | round to decimal places (iter-26) |
| REQ000406 | rtrim | right trim (iter-26/26.7) |
| REQ000408 | soundex | 4-char phonetic encoding |
| REQ000413 | unhex | hex string to BLOB |
| REQ000415 | unistr | backslash-escape decoder |
| REQ000416 | unlikely | no-op planner hint |

### Already implemented (iter-26.7)

This iteration implements 21 core scalar functions plus session state infrastructure:

| REQ ID | Function | Notes |
|---|---|---|
| REQ000384 | abs | absolute value (existing, confirmed working) |
| REQ000385 | changes | session counter for last DML row count |
| REQ000386 | char | Unicode code points to UTF-8 string |
| REQ000387 | concat | string concatenation |
| REQ000388 | concat_ws | concat with separator |
| REQ000389 | format | printf-style formatting |
| REQ000392 | iif | short-circuit conditional |
| REQ000393 | instr | substring position |
| REQ000394 | last_insert_rowid | session counter for last INSERT rowid |
| REQ000397 | ltrim | left trim |
| REQ000398 | max | scalar multi-arg max |
| REQ000399 | min | scalar multi-arg min |
| REQ000400 | octet_length | byte length |
| REQ000401 | quote | SQL literal quoting |
| REQ000402 | random | pseudo-random int64 |
| REQ000403 | randomblob | random bytes |
| REQ000404 | replace | string substitution |
| REQ000407 | sign | signum function |
| REQ000409 | sqlite_source_id | build identifier |
| REQ000410 | sqlite_version | version string |
| REQ000411 | total_changes | cumulative DML count |
| REQ000412 | typeof | type name string |
| REQ000414 | unicode | first code point |
| REQ000417 | zeroblob | zero-filled BLOB |

### Already implemented (iter-26)

These 10 functions were implemented in iter-26 before the REQ matrix was created:
| REQ000128 | OPS | Point-in-time backup / restore (snapshot engine dir, restore to a copy) | medium | M | iter-03 (WAL), iter-04 (manifest) | new `SYS/BK/bk.go`; document procedure |
| REQ000129 | OPS | Online schema migration (`ALTER TABLE ADD/DROP COLUMN` without copy) | low | XL | iter-12 (catalog) | new `SQL/EX/alter.go`, `ENG/LS` schema-aware readers |
| REQ000018 | FIL | File locking (`flock`) for multi-process access | low | S | iter-01 (FIL) | `FIL/FS/fs.go` — optional via `Options`; out of v1 scope (single-process) |
| REQ000244 | SQL/EX | ALTER TABLE executor (online schema migration) | medium | L | REQ000243 | `SQL/EX/alter.go` (new) — `ENG/LS` schema-aware readers |
| REQ000246 | SQL/PS | Parse TRIGGER (`CREATE TRIGGER`, `BEFORE/AFTER`, `FOR EACH ROW`) | low | L | iter-07 | `SQL/PS/ps.go` — `Trigger` AST, `parseTrigger` |
| REQ000247 | SQL/EX | TRIGGER executor (fire on INSERT/UPDATE/DELETE) | low | L | REQ000246 | `SQL/EX/trigger.go` (new) — hook into writers |
| REQ000248 | SQL/PS | Parse generated columns (`AS (expr) STORED/VIRTUAL`) | low | M | iter-12 | `SQL/PS/ps.go` — `ColDef.Generated` field |
| REQ000249 | SQL/EX | Generated column materialization on INSERT/UPDATE | low | M | REQ000248 | `SQL/EX/writers.go` — compute and store generated values |
| REQ000256 | SQL/PS | Parse VACUUM / ANALYZE | medium | S | iter-21 | `SQL/PS/ps.go` — `Vacuum`, `Analyze` AST |
| REQ000284 | ENG/ID | BTree delete rebalancing — no merge/redistribute after delete, tree becomes sparse | high | M | iter-23 | `ENG/ID/id.go:414-437` |
| REQ000285 | ENG/ID | uint32 page ID overflow protection — wraps to 0 (sentinel for "no page") | medium | S | iter-23 | `ENG/ID/id.go:109-113` |
| REQ000286 | SQL/EX | Window materialize context propagation — uses context.Background() instead of caller's ctx | medium | S | iter-23 | `SQL/EX/window.go:55-77` |
| REQ000287 | SQL/EX | Window setOutput allocation optimization — allocates 2 new slices per call on hot path | medium | S | iter-23 | `SQL/EX/window.go:209-218` |
| REQ000292 | SQL/EX | numericArith int64 overflow check — multiplication wraps without check (pre-existing) | medium | S | iter-19 | `SQL/EX/eval.go:646-658` |
| REQ000295 | FIL | io_uring async I/O wrapper (SQ/CQ submission, SQPOLL mode, Linux-only with IOCP/kqueue fallback) | critical | L | iter-01 (FIL), `golang.org/x/sys/unix` available | new `FIL/IO/uring.go`; cross-platform dispatch in `FIL/FS/fs.go` |
| REQ000296 | FIL | Direct I/O + io_uring fixed-file descriptor (bypass OS page cache, reduce fd table lookups) | high | M | REQ000295, iter-01 (O_DIRECT) | `FIL/FS/fs.go` — `IOSQE_FIXED_FILE` flags; integration with `O_DIRECT` fallback |
| REQ000297 | ENG | SST block-level dictionary compression (ZSTD with per-block trained dict, 4KB blocks: 1.5x→3x ratio) | high | M | iter-23 (flate baseline) | `ENG/LS/sst_writer.go` — `dictTrain` per block; `ENG/LS/sst_reader.go` — dict lookup |
| REQ000298 | ENG | LSM-aware cross-block shared dictionary (multiple data blocks in one SST share a trained dict) | medium | M | REQ000297 | `ENG/LS/sst_writer.go` — write dict in SST meta block; reader caches per-SST dict |
| REQ000299 | WAL | WAL columnar encoding (key delta-of-delta + bit-packing, `commitTS` varint; payload volume -60%+) | high | M | iter-03 (WAL) | `WAL/WR/encode.go` — columnar batch encode/decode |
| REQ000300 | ENG | Tier-aware storage scheduler (`Options.StoragePolicy`: hot=NVMe, cold=HDD/S3, hybrid; per-level device hint) | medium | L | iter-04 (LSM), iter-12 (catalog) | `ENG/LS/compaction.go` — `PlacementPolicy` per level; `Options.StoragePolicy` field |
| REQ000301 | WAL | Async fsync + io_uring linked submit (write→fsync chained via `IOSQE_IO_LINK`, lower batch-commit latency) | high | S | REQ000295, iter-17 (BatchSync) | `WAL/WR/fl.go` — `BatchSyncWithUring` |
| REQ000302 | MEM | PMem-aware buffer pool (DRAM hot slots + mmap'd PMem cold slots; `MADV_HUGEPAGE` for 2MB pages) | medium | L | iter-02 (buffer pool) | `MEM/BF/bf.go` — tier selection on `Pin`; `MEM/BF/pmem.go` (new) |
| REQ000303 | MEM | W-TinyLFU admission + SLRU (replaces clock-sweep; +30% hit rate vs LRU; Redis 8 default) | high | M | iter-02 (clock-sweep) | `MEM/BF/wtinylfu.go` (new) — frequency sketch + admission; configurable |
| REQ000304 | MEM | Off-heap large object pool (mimalloc-style size-class bins; bypasses GC for >64KB SST buffers) | medium | L | iter-02, iter-11 (mmap) | `MEM/OF/of.go` (new) — `mmap` + atomic free lists |
| REQ000305 | TXN | Generational arena with Young/Old split (young bump-allocate, old epoch-reclaim; reduces epoch manager pressure) | medium | L | iter-05 (arena), `REQ000064` | `TXN/MV/arena.go` — generation promotion policy |
| REQ000306 | TXN | Stack-allocate version nodes via escape analysis hints (`//go:nosplit` + `noescape()`; zero-GC hot path) | medium | M | iter-05 (arena) | `TXN/MV/node.go` — escape hint annotations; benchmark zero-allocation claim |
| REQ000307 | TXN | MV-OCC timestamp ordering (Silo-style, O(1) per-txn read-set validation; targets 1M+ txn/s on 16 cores) | critical | XL | iter-20 (commit protocol), `REQ000175` | `TXN/MV/occ.go` (new) — `Validation` phase rewritten; conflict-free reorder |
| REQ000308 | TXN | QSBR read path (Quiescent-State-Based Reclamation; readers set flag only, zero atomic load; near-RCU latency) | high | L | iter-05 (epoch) | `TXN/LC/qsbr.go` (new) — replaces `TXN/LC/epoch.go`; quiescent state callbacks |
| REQ000309 | ENG | NUMA-aware data placement (buffer pool slot node id, worker CPU pin, first-touch arena allocation) | high | M | iter-04 (LSM), iter-02 (buffer pool) | `ENG/LS/memtable.go` — `numactl` API integration; `MEM/BF/bf.go` NUMA hint field |
| REQ000310 | SQL | Real SIMD intrinsics for filter/projection (AVX2/AVX-512 `_mm256_cmpgt_epi64`, `_mm256_maskload_epi64`; `golang.org/x/sys/cpu` dispatch) | high | L | REQ000144 (vectorization 1.0) | `SQL/EX/eval.go` — SIMD path with CPUID dispatch; fallback to scalar |
| REQ000311 | SQL | Operator codegen (`go generate` template → specialized Go funcs; inline caches eliminate virtual dispatch) | medium | XL | REQ000310, iter-08 (operators) | `SQL/EX/codegen/` (new) — templated operator skeletons; build tag for codegen |
| REQ000312 | SQL | Vector-aware hash join (Radix partition; SIMD probe of multiple hash buckets in parallel) | high | L | REQ000310, iter-08 (NestedLoopJoin) | `SQL/EX/hashjoin.go` (new) — radix partition + SIMD probe |
| REQ000313 | SQL | Adaptive query compilation (first 2 invocations interpreted, hot path swaps to JIT via `go generate` template; 2-5x OLAP speedup) | medium | XL | REQ000311 | `SQL/EX/adqc.go` (new) — hot-path detector + plan swap |
| REQ000314 | ENG | Columnar SST layout (PAX / hybrid row-columnar; only queried columns decompressed; 75% I/O saving for narrow queries) | medium | XL | REQ000310, iter-04 (SST) | `ENG/LS/sst_writer.go` — PAX block layout; `ENG/LS/sst_reader.go` — column-selective decode |
| REQ000315 | SQL | Learned cardinality estimation (CardinalityNet/MSCN; bootstraps from existing histograms) | medium | L | REQ000085 (histogram), iter-23 (ANALYZE) | `SQL/PL/learned.go` (new) — ONNX runtime or pure-Go MLP; training data from ANALYZE |
| REQ000316 | SQL | Incremental materialized views (auto-maintained aggregation views with query routing) | medium | L | iter-08 (operators), iter-12 (catalog) | `SQL/EX/matview.go` (new); `ENG/LS` triggers on view base tables |
| REQ000317 | WAL | Parallel WAL replay by key-range partition (worker pool; manifest serial; 100GB replay 30s→8s) | high | M | iter-13 (replay) | `WAL/RP/parallel.go` (new) — partition dispatch; ordered manifest apply |
| REQ000318 | ENG | Write-rate-limited compactor (RocksDB-style `rate_limiter`; prevents compaction starvation under write bursts) | high | M | iter-04 (compaction) | `ENG/LS/compaction.go` — `RateLimiter` (token bucket); exposed via `Options` |
| REQ000319 | ENG | Sub-compaction parallelism (split L4+ compaction into key-range sub-jobs; worker pool fan-out) | high | M | iter-04, REQ000318 | `ENG/LS/subcompact.go` (new) — partition + merge result |
| REQ000320 | ENG | Configurable compaction style (`Options.CompactionStyle = leveled \| tiered \| hybrid`; tiered for time-series) | medium | M | iter-04 | `ENG/LS/compaction.go` — strategy interface; `Options.CompactionStyle` field |
| REQ000321 | TXN | Deterministic Simulation Testing framework (FoundationDB-style scheduled threads + simulated clock + simulated disk; millions of random schedules) | high | XL | iter-17 (chaos), iter-13 (recovery) | new `tests/dst/` framework; subsystem-aware simulated drivers |
| REQ000322 | LOG | eBPF runtime tracing export (`ProfileHook` data consumed by eBPF programs for lock contention / I/O queue / GC pause maps) | medium | M | iter-00 (ProfileHook) | `LOG/HK/profile.go` — BPF map publishing; optional `cmd/razor-ebpf` tool |
DONE REQ000350 | SQL/LX | Bitwise operators (`&`, `|`, `^`, `~`) — not in lexer/parser | medium | M | iter-07 (lexer) | `SQL/LX/token.go`, `SQL/LX/lx.go`, `SQL/PS/ps.go` — add tokens + precedence |
DONE REQ000351 | SQL/LX | String concatenation operator (`\|\|`) — not in lexer/parser | low | S | iter-07 (lexer) | `SQL/LX/token.go`, `SQL/PS/ps.go` — add token + binary op |
DONE REQ000352 | SQL/LX | Modulo operator (`%`) — not in lexer/parser | low | S | iter-07 (lexer) | `SQL/LX/token.go`, `SQL/EX/eval.go` — add token + eval |
DONE REQ000353 | SQL/EX | COALESCE as special form (currently only works as function call) | low | S | iter-08 (eval) | `SQL/EX/eval.go` — add COALESCE case in evalFunction |
DONE REQ000354 | SQL/EX | NULLIF as special form (currently only works as function call) | low | S | iter-08 (eval) | `SQL/EX/eval.go` — add NULLIF case in evalFunction |

## Unfixed Bugs (surfaces as requirements)

These rows are bugs that were discovered during a prior iteration but
not fixed in that iteration's scope. The "Touches" column points to
the discovery context. See `AGENTS.md` Bug-To-Requirement Rule.

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
|---|---|---|---|---|---|---|
| REQ000348 | SQL/EX | `Session.Query` returns schema-only `*AP.Rows` (no row data streaming; callers must use unexported `QueryAll`) | medium | M | iter-25 surfacing | `internal/SYS/AP/ap.go` `Session` interface needs `Next()` accessor; `internal/SYS/SE/se.go` returns `&AP.Rows{Cols,Types}` with no streaming |
| REQ000349 | SQL/PS | Missing SQLite builtin scalar functions: `LENGTH`, `TYPEOF`, `UNICODE`, `QUOTE`, `ZEROBLOB`, `RANDOMBLOB`, `HEX`, `SOUNDEX`. Each emits `ps: syntax error` rather than a typed "unsupported" error, so the SLT classifier must fall back to substring matching on `syntax error` | low | XL | iter-25 surfacing (edge probe `TestEdge_Expressions`) | `SQL/EX/eval.go` function dispatch table |
| REQ000356 | SQL/LX | Unary `NOT` as logical operator (currently only works as infix in some contexts; `~` bitwise NOT) | low | S | iter-07 (lexer) | `SQL/LX/token.go`, `SQL/EX/eval.go` |

## DONE

| ID | Subsystem | Requirement | Iteration |
|---|---|---|---|
| REQ000035 | WAL | Corruption recovery policy: detect torn write, skip vs. fail | iter-13 |
| REQ000098 | SYS | Session pooling (`sync.Pool`) | iter-15 |
| REQ000099 | SYS | `ReadOnly` mode in `Options` (skip WAL writes, O_RDONLY opens) | iter-15 |
| REQ000127 | SQL | Catalog persistence across restarts (`CREATE TABLE` / `DROP TABLE` survive `Close`/`Open`) | iter-12 |
| REQ000146 | SYS | 6-phase graceful shutdown sequence per SYS.md:215-282 | iter-14 |
| REQ000152 | SYS | `validateOptions` with field-by-field checks per SYS.md:198-214 | iter-14 |
| REQ000258 | SQL/EX | ANALYZE executor (collect column statistics) | iter-23 |
| REQ000085 | SQL | Histogram-based selectivity (replace uniform distribution) | iter-23 |
| REQ000261 | SYS | Integrity check (`PRAGMA integrity_check`) | iter-23 |
| REQ000272 | WAL | Checksum verification on WAL replay (detect corruption) | iter-23 |
| REQ000257 | SQL/EX | VACUUM executor (reclaim tombstone space, rebuild SST) | iter-23 |
| REQ000259 | SYS | Backup/restore API (snapshot engine dir to copy) | iter-23 |
| REQ000260 | SYS | Admin CLI `razor` (schema dump, vacuum, integrity check) | iter-23 |
| REQ000153 | SYS | Active-tx wait (30s timeout, force-abort on timeout) | iter-14 |
| REQ000147 | TXN | Complete commit protocol (6 phases) | iter-20 |
| REQ000171 | TXN/VL | WAL integration in commit protocol | iter-20 |
| REQ000174 | ENG/LS | BloomFilter FNV-1a double-hash | iter-20 |
| REQ000193 | LOG/HK | MetricHook counters wiring | iter-20 |
| REQ000196 | SQL/EX | HashAggregate in planner (1000-row threshold) | iter-20 |
| REQ000345 | SQL/EX | Empty-table aggregate returns 1 row (not 0) | iter-26 |
| REQ000346 | SQL/EX | Test isolation: EX package-level maps reset | iter-26 |
| REQ000347 | ENG | Silent data loss in LSM flush path | iter-26 |
| REQ000357 | SQL/EX | SELECT without FROM returns 0 rows — wrap in Values op when `s.From==""` | iter-26.1 (v0.26.3) |
| REQ000359 | SQL/EX | String concat NULL semantics — `'a' \|\| NULL` returns NULL | iter-26.1 (v0.26.3) |
| REQ000360 | SQL/EX | Arithmetic NULL semantics — `10 + NULL` returns NULL | iter-26.1 (v0.26.3) |
| REQ000361 | SQL/EX | IS NULL / IS NOT NULL semantics — `NULL IS NULL` true, `NULL IS NOT NULL` false | iter-26.1 (v0.26.3) |
| REQ000362 | SQL/EX | Comparison with NULL — `5 = NULL` returns NULL | iter-26.1 (v0.26.3) |
| REQ000364 | ENG/LS | Negative WaitGroup counter panic in flushManager (REQ000347 followup) — Add(1) before send, enqueueMu serializes with Stop | iter-26.1 (v0.26.3) |
| REQ000365 | tests/sqlcmp/dual | `allProbeCases` undeclared — register `probeCases` in `AllCases()` | iter-26.1 (v0.26.3) |
| REQ000355 | SQL/PS | Aggregate function `GROUP_CONCAT(expr)` — route through AggregateFunc when name is a known aggregate | iter-26.2 (v0.26.4) |
| REQ000363 | SQL/EX | GROUP_CONCAT empty result — empty table returns NULL (after REQ000367 hidden-PK) | iter-26.2 (v0.26.4) |
| REQ000366 | SQL/EX | Subquery planner store threading — `Row.planner` + `currentSubqueryPlanner`; `outerInjector` updates `outer` in place for memoized plans | iter-26.2 (v0.26.4) |
| REQ000367 | SQL/EX | Hidden rowid for tables without PRIMARY KEY — `hiddenPK` flag, atomic `nextRowID` | iter-26.2 (v0.26.4) |
| REQ000368 | SQL/PS | Parser comma-join `FROM a, b` — synthesize CROSS joins for trailing comma-separated tables | iter-26.2 (v0.26.4) |
| REQ000378 | SQL/EX | HAVING with `COUNT(*)` returns 0 rows — evalAggregate resolves `COUNT(*)` (StarExpr arg) to the precomputed column | iter-26.3 (v0.26.5) |
| REQ000379 | SQL/PS | Chained unary minus: `SELECT 5- -5` must equal 10 — regression test for SQLite-compatible `--` comment behavior | iter-26.3 (v0.26.5) |
| REQ000380 | SQL/PS | `NOT LIKE` parser error — parsePostfix now peeks `T_NOT T_LIKE` and dispatches to parseNotLike | iter-26.3 (v0.26.5) |
| REQ000381 | SQL/PS | `NOT IN (subquery)` parser error — same pattern, dispatches to parseNotIn; parseIn refactored to use parseInBody helper | iter-26.3 (v0.26.5) |
| REQ000382 | SQL/EX | Add `ABS`, `HEX`, `ROUND` scalar functions to evalFunction | iter-26.3 (v0.26.5) |
| REQ000383 | SQL/PS/EX | Compound SELECT: UNION, INTERSECT, EXCEPT with correct precedence (INTERSECT binds tighter), trailing ORDER BY/LIMIT/OFFSET apply to compound result; RE rewrite/format support | iter-26.4 (v0.26.6) |
| REQ000197 | SQL/EX | OUTER JOIN executor (LEFT/RIGHT/FULL) | iter-20 |
| REQ000198 | ENG/LS | Skiplist sync.Pool for scratch arrays | iter-20 |
| REQ000201 | QUAL | SQL/PL coverage 30.6% to 98.8% | iter-20 |
| REQ000202 | SQL/PS | Parser CASE/EXISTS tests | iter-20 |
| REQ000206 | SQL/PS | Type tokens (NUMERIC/DATE/TIME/JSON/DECIMAL) | iter-20 |
| REQ000207 | SQL/PS | Parameterized types VARCHAR(N)/DECIMAL(P,S) | iter-20 |
| REQ000208 | SQL/EX | Type affinity system (SQLite-like 5 affinities) | iter-20 |
| REQ000209 | SQL/PS | DEFAULT clause parsing tests | iter-20 |
| REQ000210 | SQL/PS | CHECK constraint parsing | iter-20 |
| REQ000211 | SQL/EX | CHECK constraint enforcement | iter-20 |
| REQ000218 | SQL/EX | HAVING filter (already implemented) | iter-20 |
| REQ000251 | SQL/PS | Parse CREATE INDEX (UNIQUE, multi-column) | iter-22 |
| REQ000252 | SQL/EX | IndexScan operator (real seek via LSM index keyspace) | iter-22 |
| REQ000253 | SQL/PL | Index selection in planner (col=lit equality → real seek) | iter-22 |
| REQ000254 | ENG | Histogram-based selectivity stats (types defined, ANALYZE pending) | iter-22 |
| REQ000229 | SQL/EX | DECIMAL type storage (big.Float) | iter-20 |
| REQ000154 | SYS | Background-goroutine coordination (compaction, flush, epoch, hook dispatcher) | iter-14 |
| REQ000166 | SYS | Per-subsystem `Close()` ordering in Phase 5 of shutdown | iter-14 |
| REQ000178 | SYS/SY | `validateOptions` (duplicate of REQ000152, same code) | iter-14 |
| REQ000155 | ENG | Catalog persistence across restarts | iter-12 (consolidated with REQ000127) |
| REQ000009 | LOG | Log compression after rotation (gzip) | iter-16 |
| REQ000167 | SQL | Parameter binding type coercion (Go int -> BIGINT, string -> INT error) | iter-16 |
| REQ000179 | TXN/VL | Add arena field to transactionSlot struct for per-transaction tracking | iter-16 |
| REQ000180 | ENG/LS | Dynamic BloomFilter sizing ((N *10 +7) /8 bytes) replace fixed4096 bytes | iter-16 |
| REQ000186 | ENG/LS | Fix SST file path mismatch: compaction.fileName produces sst/L<N>_<minkey-hex>_<maxkey-hex>_<id>.sst while flush now emits the same shape | iter-16 |
| REQ000044 | ENG | `ENG/LS` benchmarks (skiplist insert/find, SST write/read, flush, compaction) | iter-17 |
| REQ000138 | QUAL | `Benchmark*` for every storage component (catch any missing) | iter-17 |
| REQ000163 | SQL | Rewriter AST normalization (design mentions, verify completeness) | iter-17 (partial: 65.0%%) |
| REQ000169 | LOG | Debug-level allocation trade-off documentation (level check before allocation) | iter-17 |
| REQ000170 | WAL | RTMerge record encoding implementation | iter-17 |
| REQ000191 | WAL/RP | Coverage lift: WAL/RP is at 75.2% (multi-segment truncate + ErrUnknownRecord added; shortfall now in resync-window edges) | iter-16 |
| REQ000192 | SQL/EX | Adaptive vectorization threshold (auto-fallback to row-at-a-time for tables <100K rows) | iter-19 |
| REQ000194 | LOG/HK | Implement TraceHook for SQL query tracing (start/end with timing) | iter-00 |
| REQ000195 | LOG/HK | Implement ProfileHook (pprof dump on Error events) | iter-00 |
| REQ000199 | MEM/BF | Sharded buffer pool mutex (reduce hash table contention) | iter-02 |
| REQ000200 | WAL/WR | Per-segment locks (replace global write mutex) | iter-03 |
| REQ000203 | QUAL | Missing benchmarks (FIL/LF, LOG/HK, SQL/PS, SQL/PL have 0) | iter-17 |
| REQ000204 | SQL | CREATE INDEX (no implementation, no parser support) | iter-21 |
| REQ000205 | SQL | EXPLAIN SQL syntax (currently only cost calc, not SQL statement) | iter-21 |
| REQ000176 | WAL | Batch commit with sync.WaitGroup and write barrier | iter-17 |
| REQ000184 | WAL | 256 KB pre-allocated writeBuffer for batched WAL writes | iter-17 |
| REQ000144 | SQL | SIMD vectorized execution (batch + 4-wide unrolling + selection vectors) | iter-19 (Phase 1) |
| REQ000149 | SQL | Columnar batch memory management (sync.Pool for 1024-row batches) | iter-19 (Phase 1) |
| REQ000157 | SQL | Expression evaluation SIMD acceleration (batch predicate) | iter-19 (Phase 1) |
| REQ000173 | SQL/EX | SIMD vectorized operators (consolidated into REQ000144) | iter-19 (Phase 1) |
| REQ000145 | SQL | Parallel query execution (worker pool, fan-out/fan-in, channel merge) | iter-19 (Phase 2) |
| REQ000158 | TXN | Hazard pointer publication/clear protocol (split into PublishCurrent/PublishNext) | iter-15 |
| REQ000172 | SYS/SY | 6-phase graceful shutdown implementation per SYS.md:196-283 | iter-14 |
| REQ000150 | SQL | Parallel Sort implementation (sample sort for top-k) | iter-19 (Phase 3) |
| REQ000001 | LOG | `Logger` wraps `log/slog` with atomic level control | iter-00 |
| REQ000002 | LOG | Structured key-value output (JSON/text) | iter-00 |
| REQ000003 | LOG | Log file rotation on size threshold | iter-00 |
| REQ000004 | LOG | Hook registry with async dispatch | iter-00 |
| REQ000005 | LOG | `TraceHook` for SQL query tracing | iter-00 |
| REQ000006 | LOG | `MetricHook` for throughput/latency counters | iter-00 |
| REQ000007 | LOG | `ProfileHook` for CPU/heap dump on error | iter-00 |
| REQ000008 | LOG | Bounded channel: drop on overflow, never block log path | iter-00 |
| REQ000010 | FIL | Block I/O via `pread`/`pwrite` | iter-01 |
| REQ000011 | FIL | `O_DIRECT` support with fallback | iter-01 |
| REQ000012 | FIL | CRC32 checksum per block | iter-01 |
| REQ000013 | FIL | `MetaPage` with magic/version/catalog root | iter-01 |
| REQ000014 | FIL | Path validation (reject `..`, symlinks) | iter-01 |
| REQ000015 | FIL | Cached directory FDs for `SyncDir` | iter-01 |
| REQ000016 | FIL | WAL segment handle pool | iter-01 |
| REQ000017 | FIL | `ftruncate` for replay segment shrinking | iter-01 |
| REQ000020 | MEM | Buffer pool: clock-sweep LRU eviction | iter-02 |
| REQ000021 | MEM | Atomic `Pin`/`Unpin` with eviction gating | iter-02 |
| REQ000022 | MEM | O(1) hash table lookup by `blockID` | iter-02 |
| REQ000023 | MEM | Hint file for warm startup | iter-02 |
| REQ000024 | MEM | Hint file compression (gzip) when > 1 MB | iter-02 |
| REQ000025 | MEM | `sync.Pool` for page/iterator buffers | iter-02 |
| REQ000027 | WAL | Sequential append with LSN allocation | iter-03 |
| REQ000028 | WAL | 64 MB segment rotation | iter-03 |
| REQ000029 | WAL | `fsync` on commit | iter-03 |
| REQ000030 | WAL | Batch flush / write barrier | iter-03 |
| REQ000031 | WAL | WAL replay on startup | iter-03 |
| REQ000032 | WAL | Checkpoint detection and segment truncation | iter-03 |
| REQ000033 | WAL | `RTCheckpoint` record with catalog root | iter-03 |
| REQ000036 | ENG | Lock-free skiplist memtable | iter-04 |
| REQ000037 | ENG | Memtable freeze + flush to L0 SST | iter-04 |
| REQ000038 | ENG | SST writer: data blocks, index, bloom, footer | iter-04 |
| REQ000039 | ENG | SST reader: block decode, bloom check, index binary search | iter-04 |
| REQ000040 | ENG | Leveled compaction with multi-way merge sort | iter-04 |
| REQ000041 | ENG | Atomic manifest versioning (temp + rename + `fsync`) | iter-04 |
| REQ000042 | ENG | Bloom filter (10 bits/key, double-hashing) | iter-04 |
| REQ000043 | ENG | Delta-encoded data blocks with restart points | iter-04 |
| REQ000046 | ENG | IndexScan operator exists (prefix-scan fallback) | iter-08 |
| REQ000051 | TXN | MVCC version chain (lock-free, CAS insertion) | iter-05 |
| REQ000052 | TXN | Per-thread arena allocation | iter-05 |
| REQ000053 | TXN | Hazard pointer coordination | iter-05 |
| REQ000054 | TXN | Epoch-based reclamation | iter-05 |
| REQ000055 | TXN | Read view per transaction | iter-05 |
| REQ000056 | TXN | Commit protocol: validate → assign `commitTS` → CAS `endTS` | iter-06 |
| REQ000057 | TXN | Write-write conflict detection | iter-06 |
| REQ000058 | TXN | Transaction slots (max 1024 concurrent) | iter-06 |
| REQ000059 | TXN | Shadow writeSet for ROLLBACK | iter-06 |
| REQ000060 | TXN | Read-uncommitted isolation (v1) | iter-09 |
| REQ000063 | TXN | Savepoint support | iter-09 |
| REQ000065 | SQL | Lexer with keyword map | iter-07 |
| REQ000066 | SQL | Recursive-descent parser | iter-07 |
| REQ000067 | SQL | AST node types for DDL/DML/SELECT | iter-07 |
| REQ000068 | SQL | Constant folding | iter-07 |
| REQ000069 | SQL | Predicate pushdown | iter-07 |
| REQ000070 | SQL | Subquery flattening (IN/EXISTS) | iter-07 |
| REQ000071 | SQL | Plan memoization (SHA256 of AST) | iter-08 |
| REQ000072 | SQL | Cost estimation (uniform distribution) | iter-08 |
| REQ000073 | SQL | `SeqScan` operator | iter-08 |
| REQ000075 | SQL | `Filter` / `Project` / `Sort` / `Limit` operators | iter-08 |
| REQ000076 | SQL | `Insert` / `Update` / `Delete` operators | iter-08 |
| REQ000077 | SQL | `Aggregate` (COUNT/SUM/AVG/MIN/MAX) | iter-08 |
| REQ000078 | SQL | `HashAggregate` | iter-08 |
| REQ000079 | SQL | `NestedLoopJoin` (INNER/CROSS) | iter-08 |
| REQ000080 | SQL | `Distinct` operator | iter-08 |
| REQ000081 | SQL | `EXPLAIN` rendering | iter-08 |
| REQ000082 | SQL | Subquery operator (IN/EXISTS/scalar) | iter-08 |
| REQ000087 | SYS | `Engine.Open` with `Options` validation | iter-09 |
| REQ000088 | SYS | `Engine.Close` with graceful shutdown | iter-09 |
| REQ000089 | SYS | `Engine.Begin` → `Session` | iter-09 |
| REQ000090 | SYS | `Engine.Stats` aggregation | iter-09 |
| REQ000091 | SYS | Error type taxonomy (retryable vs fatal) | iter-09 |
| REQ000092 | SYS | `Session.Query` / `Session.Exec` | iter-09 |
| REQ000093 | SYS | `Session.SetDeadline` with `atomic.Value` | iter-09 |
| REQ000094 | SYS | `Transaction.Commit` / `Rollback` | iter-09 |
| REQ000095 | SYS | `Transaction.Savepoint` / `RollbackTo` | iter-09 |
| REQ000096 | SYS | `Stmt.Prepare` / `Query` / `Exec` / `Close` | iter-09 |
| REQ000097 | SYS | SIGTERM/SIGINT graceful shutdown handler | iter-09 |
| REQ000103 | DDL | `CREATE TABLE` with column types and `PRIMARY KEY` | iter-08 |
| REQ000104 | DDL | `DROP TABLE` | iter-08 |
| REQ000105 | DDL | `NOT NULL` constraint enforcement | iter-10 |
| REQ000106 | DDL | `DEFAULT` value substitution | iter-10 |
| REQ000107 | SQL | `UNIQUE` constraint (in-memory path) | iter-11 |
| REQ000019 | MEM | `MADV_DONTNEED` hints for buffer eviction | iter-11 |
| REQ000026 | FIL | `mmap` BlockDevice for SST reads | iter-11 |
| REQ000108 | DML | `INSERT` with column list | iter-08 |
| REQ000109 | DML | `UPDATE` with `WHERE` | iter-08 |
| REQ000110 | DML | `DELETE` with `WHERE` | iter-08 |
| REQ000111 | DML | `SELECT` with `WHERE` / `ORDER BY` / `LIMIT` / `OFFSET` | iter-08 |
| REQ000112 | DML | `COUNT(*)` / `SUM` / `AVG` / `MIN` / `MAX` aggregates | iter-08 |
| REQ000114 | DML | `DISTINCT` | iter-08 |
| REQ000115 | DML | `IN` / `EXISTS` / scalar subqueries | iter-08 |
| REQ000116 | DML | `JOIN` (INNER, CROSS) | iter-08 |
| REQ000118 | DML | `LIKE` pattern matching | iter-07 |
| REQ000119 | DML | `BETWEEN` | iter-07 |
| REQ000120 | DML | `IS NULL` / `IS NOT NULL` | iter-07 |
| REQ000121 | TXN-API | `BEGIN` / `COMMIT` / `ROLLBACK` | iter-08 |
| REQ000124 | API | Parameter binding via `?` placeholders | iter-08 |
| REQ000125 | API | `EXPLAIN <query>` | iter-08 |
| REQ000130 | OBS | Query tracing via `TraceHook` | iter-00 |
| REQ000131 | OBS | Latency histograms via `MetricHook` | iter-00 |
| REQ000132 | OBS | CPU/heap profiling on error | iter-00 |
| REQ000133 | OBS | Structured stats aggregation (`Engine.Stats`) | iter-09 |
| REQ000134 | QUAL | `go vet ./...` zero warnings | all |
| REQ000135 | QUAL | `gofmt -s -l .` no drift | all |
| REQ000136 | QUAL | `go test ./... -race -count=1` all green | all |
| REQ000137 | QUAL | Property-based tests for storage (crash/recovery) | all |
| REQ000139 | QUAL | No allocations in hot paths | all |
| REQ000140 | QUAL | All public API methods goroutine-safe | iter-09 |
| REQ000141 | QUAL | `log/slog` only — no `fmt.Printf` in library | all |
| REQ000142 | QUAL | Error messages: lowercase, no trailing punctuation | all |
| REQ000230 | SQL/PS | Parse WITH clause (CTE: `WITH x AS (...) SELECT...`) | iter-21 |
| REQ000231 | SQL/PL | CTE planner (materialization vs inline expansion) | iter-21 |
| REQ000232 | SQL/PS | Parse ON CONFLICT clause (`INSERT ... ON CONFLICT DO NOTHING/UPDATE`) | iter-21 |
| REQ000233 | SQL/EX | UPSERT executor (`INSERT...ON CONFLICT`) | iter-21 |
| REQ000234 | SQL/PS | Parse RETURNING clause (`INSERT/UPDATE/DELETE ... RETURNING col`) | iter-21 |
| REQ000235 | SQL/EX | RETURNING executor (return rows from DML) | iter-21 |
| REQ000238 | SQL/PS | Parse SAVEPOINT / RELEASE / ROLLBACK TO | iter-21 |
| REQ000239 | TXN/VL | Savepoint implementation (nested transaction markers) | iter-21 |
| REQ000273 | SQL/PS | Add `EXPLAIN` and `EXPLAIN QUERY PLAN` keyword tokens | iter-21 |
| REQ000274 | SQL/PS | Parse `EXPLAIN [QUERY PLAN] <stmt>` prefix syntax | iter-21 |
| REQ000275 | SQL/PS | `ExplainStmt` AST (`Mode` enum, `Inner` statement) | iter-21 |
| REQ000276 | SQL/PL | `PlanNode` tree wrapper (type, cost, rows, children) | iter-21 |
| REQ000277 | SQL/PL | Visitor pattern: emit PlanNodes during planner tree construction | iter-21 |
| REQ000278 | SQL/PL | Per-operator cost annotation (`cost=N rows=N width=N`) | iter-21 |
| REQ000279 | SQL/EX | `EXPLAIN` execution path: skip row execution, return plan as result-set | iter-21 |
| REQ000280 | SQL/EX | EXPLAIN on DML (INSERT/UPDATE/DELETE) returns execution plan | iter-21 |
| REQ000281 | SQL/EX | EXPLAIN QUERY PLAN formatter (tree-style, human-readable) | iter-21 |
| REQ000236 | SQL/PS | Parse window functions (`OVER`, `PARTITION BY`, `ROW_NUMBER`, `RANK`) | iter-23 |
| REQ000237 | SQL/EX | Window function executor (`ROW_NUMBER`, `RANK`, `SUM OVER`, `LAG`, `LEAD`) | iter-23 |
| REQ000262 | SQL/PS | Add DATE / TIME / TIMESTAMP type tokens | iter-23 |
| REQ000263 | SQL/EX | DATE / TIME / TIMESTAMP value storage and arithmetic | iter-23 |
| REQ000264 | SQL/PS | Add JSON type and parse `->`, `->>`, `json_extract` | iter-23 |
| REQ000265 | SQL/EX | JSON value storage and `json_extract` executor | iter-23 |
| REQ000250 | ENG/ID | B-tree secondary index package (foundation) | iter-23 |
| REQ000047 | ENG | Prefix bloom filters for range scans | iter-23 |
| REQ000271 | ENG/LS | Compression for SST blocks (flate) | iter-23 |
| REQ000282 | SQL/EX | Fix computeRank RANK for tied rows | iter-23 |
| REQ000283 | ENG/ID | Fix Cursor.Next() leaf boundary traversal | iter-23 |
| REQ000293 | QUAL | Window operator test coverage (window_test.go) | iter-23 |
| REQ000294 | QUAL | Cross-leaf cursor test coverage | iter-23 |
| REQ000123 | TXN-API | Configurable isolation levels (SET TRANSACTION) | iter-24 |
| REQ000255 | TXN | Read-committed per-statement snapshot | iter-24 |
| REQ000062 | TXN | MVCC own-writes visibility in transactions | iter-24 |
| REQ000061 | TXN | Read-committed as default isolation level | iter-24 |
| REQ000291 | SQL/PS | EXCLUDED.col reference in ON CONFLICT DO UPDATE | iter-24 |
| REQ000240 | SQL/PS | Parse CREATE VIEW | iter-24 |
| REQ000241 | SQL/PL | View resolution (inline expansion) | iter-24 |
| REQ000243 | SQL/PS | Parse ALTER TABLE ADD/DROP COLUMN/RENAME | iter-24 |
| REQ000288 | SQL/PS | parseInterval unit validation | iter-24 |
| REQ000290 | SQL/EX | LAG/LEAD arbitrary offset support | iter-24 |
| REQ000270 | SQL/PS | FETCH FIRST n ROWS ONLY | iter-24 |
| REQ000242 | SYS | Pragmas (cache_size, journal_mode, synchronous) | iter-24 |
| REQ000323 | TEST | SQLLogicTest corpus mirror as git submodule (`tests/sqlcmp/corpus` from `MarvBeer/sqlite-test-suite`); build-tag-gated fetch | iter-25 |
| REQ000324 | TEST | SLT test file parser (`statement ok\|error`, `query <types> <sort> <label>`, `halt`, `hash-threshold`, `skipif`, `onlyif`; tolerant, line-oriented) | iter-25 |
| REQ000325 | TEST | Driver interface (`Connect`/`Close`/`Exec`/`Query`; returns `ResultSet` with typed `Value` cells) | iter-25 |
| REQ000326 | TEST | Razordata driver implementation wrapping public `internal/SYS.Engine` API; SQL errors classified as `skipped` not `failed` | iter-25 |
| REQ000327 | TEST | SLT runner + type-aware result diff (`T`/`I`/`R`/`NULL`; `nosort`/`rowsort`/`valuesort`; `label` grouping) | iter-25 |
| REQ000328 | TEST | Corpus subset default target (~200 `.test` files from `select1-4`, `index/`, `evidence/`, `minmax/`, `cast/`, `null/`, `decimal/`, `datetime/`); calibrated pass-rate threshold; full corpus gated by `slt_corpus_full` tag | iter-25 |
| REQ000329 | TEST | Pure-Go reference oracle via `modernc.org/sqlite` dependency (test-only); fallback to hand-rolled `tests/sqlcmp/oracle/` if modernc fails to build in sandbox | iter-25 |
| REQ000330 | TEST | Dual runner: same SQL on Razordata + oracle; result-set diff after normalization | iter-25 |
| REQ000331 | TEST | Result-set normalization (int→int64, float round, strip whitespace, column-name sort, `''`/`NULL` config flag) | iter-25 |
| REQ000332 | TEST | Dual case authoring convention (`dualCase` struct, table-driven; seed ~50 cases across DDL/DML/aggregates/joins) | iter-25 |
| REQ000333 | TEST | JUnit XML output for CI consumption (pass/fail/skip per testcase) | iter-25 |
| REQ000334 | TEST | Coverage snapshot (`tests/sqlcmp/slt/coverage.json` per run; `coverage.baseline.json` committed; regression check) | iter-25 |
| REQ000335 | TEST | CI workflow: PR + nightly; run subset; upload JUnit; post pass-rate PR comment vs `main` | iter-25 (deferred — no `.github/` in repo) |
| REQ000336 | TEST | Developer guide: how to run, add cases, re-baseline coverage | iter-25 |
| REQ000337 | TEST | Architecture note lives in iteration doc (not `design/`); test harness is operational, not architectural | iter-25 |

---

## Newly Discovered Bugs (2026-06-12, dual-runner expansion)

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
|---|---|---|---|---|---|---|
| REQ000358 | SQL/PS | XOR operator (`^`) parser gap — lexer emits `T_BITXOR` but parser doesn't recognize in precedence table | high | S | iter-07 (lexer) | `SQL/PS/ps.go` — add `T_BITXOR` to binary operator switch |

> The following bugs from the 2026-06-12 dual-runner pass were
> resolved in iter-26.1 (v0.26.3): REQ000357 (SELECT no-FROM),
> REQ000359 (concat NULL), REQ000360 (arith NULL), REQ000361
> (IS NULL semantics), REQ000362 (= NULL), REQ000364 (flush
> WaitGroup), REQ000365 (allProbeCases undeclared). They are
> now in the DONE table.
>
> The following bugs from the SLT corpus / dual-runner
> expansion were resolved in iter-26.2 (v0.26.4):
> REQ000355 (GROUP_CONCAT dispatch), REQ000363 (GROUP_CONCAT
> empty → NULL), REQ000366 (subquery store threading),
> REQ000367 (hidden PK for no-PK tables), REQ000368 (comma-
> join). They are now in the DONE table.

## Newly Discovered Bugs (2026-06-13, v0.26.5 candidate pool)

Probing a wider SLT-style test set surfaced the following bugs.
Severity and effort are estimated; final scope for v0.26.5 is
decided per release. See iter-26.3 planning doc (when written)
for the chosen subset.

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
|---|---|---|---|---|---|---|

## Newly Discovered Bugs (2026-06-12, SLT corpus run)

The SLT corpus from `jzombie/sqlite-sqllogictest-corpus` was copied to `tests/sqlcmp/corpus/test/`.
Running `select1.test` (12K lines, ~3K query records) against the engine shows:

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
|---|---|---|---|---|---|---|

---

## SQLite Core Function Coverage (https://sqlite.org/lang_corefunc.html)

Goal: full coverage of the 60 functions on SQLite's core scalar
function page, each with a positive test case in
`internal/SQL/EX/corefunc_test.go` (table-driven, named cases so
the dual-runner and SLT harness can pinpoint the missing one).
Status column tracks the implementation state.

| Function | Status | Notes |
|---|---|---|
| `abs(X)` DONE REQ000384 | returns absolute value, NULL→NULL, string→0.0, MIN_INT64→error |
| `changes()` DONE REQ000385 | last INSERT/UPDATE/DELETE row count; not yet wired to session state |
| `char(X1,...,XN)` DONE REQ000386 | Unicode code point → character; accepts variadic int args |
| `coalesce(X,Y,...)` | REQ000418 (DONE) | iter-26 already implemented |
| `concat(X,...)` DONE REQ000387 | concatenate non-NULL args; all-NULL → "" (note: current `\|\|` returns NULL on NULL) |
| `concat_ws(SEP,X,...)` DONE REQ000388 | concat with separator; SEP=NULL → NULL |
| `format(FORMAT,...)` DONE REQ000389 | printf-style formatting (subset of fmt verbs) |
| `glob(X,Y)` DONE REQ000390 | filename glob match (X=pattern, Y=string) |
| `hex(X)` DONE REQ000391 | BLOB/text → uppercase hex; integer is converted via text first |
| `ifnull(X,Y)` | REQ000419 (DONE) | iter-26 |
| `iif(B1,V1,...)` DONE REQ000392 | short-circuit CASE; `if()` alias |
| `instr(X,Y)` DONE REQ000393 | position of Y in X (1-based), 0 if not found |
| `last_insert_rowid()` DONE REQ000394 | engine-level rowid; engine must expose per-session counter |
| `length(X)` | REQ000420 (DONE) | iter-26 — returns code-point count (not bytes); close to SQLite's semantics |
| `like(X,Y[,Z])` | REQ000421 (DONE) | iter-26 — two-arg form; ESCAPE clause not yet supported |
| `likelihood(X,Y)` DONE REQ000395 | no-op pass-through; hint to planner |
| `likely(X)` DONE REQ000396 | no-op pass-through |
| `load_extension(X[,Y])` | REQ000428 (SKIP) | not in v1 scope; would require CGO bridge |
| `lower(X)` | REQ000422 (DONE) | iter-26 |
| `ltrim(X[,Y])` DONE REQ000397 | trim left; default Y=" " |
| `max(X,Y,...)` DONE REQ000398 | multi-arg scalar max; uses first collating function |
| `min(X,Y,...)` DONE REQ000399 | multi-arg scalar min |
| `nullif(X,Y)` | REQ000423 (DONE) | iter-26 |
| `octet_length(X)` DONE REQ000400 | byte length; differs from `length` for UTF-8 |
| `printf(FORMAT,...)` | REQ000424 (DONE) | alias for `format`; merge with REQ000389 |
| `quote(X)` DONE REQ000401 | SQL literal rendering; strings single-quoted with escape, BLOBs as X'hex' |
| `random()` DONE REQ000402 | pseudo-random int64; exclude MIN_INT64 |
| `randomblob(N)` DONE REQ000403 | N-byte random BLOB |
| `replace(X,Y,Z)` DONE REQ000404 | string substitution; Y="" returns X unchanged |
| `round(X[,Y])` DONE REQ000405 | round to Y decimal places; Y default 0; Y<0 → 0 |
| `rtrim(X[,Y])` DONE REQ000406 | trim right; default Y=" " |
| `sign(X)` DONE REQ000407 | -1/0/+1 or NULL for non-numeric |
| `soundex(X)` DONE REQ000408 | soundex encoding; "?000" for non-ASCII / NULL |
| `sqlite_compileoption_get(N)` | REQ000429 (SKIP) | engine-internal, returns NULL for v1 |
| `sqlite_compileoption_used(X)` | REQ000430 (SKIP) | engine-internal, returns 0 for v1 |
| `sqlite_offset(X)` | REQ000431 (SKIP) | requires SQLITE_ENABLE_OFFSET_SQL_FUNC compile flag |
| `sqlite_source_id()` DONE REQ000409 | fixed string for v1 ("razordata-v0.26.x") |
| `sqlite_version()` DONE REQ000410 | fixed string for v1 ("0.26.x") |
| `substr(X,Y[,Z])` | REQ000425 (DONE) | iter-26 — 1-based, negative start counts from right |
| `substring(X,Y[,Z])` | REQ000426 (DONE) | alias for `substr` |
| `total_changes()` DONE REQ000411 | cumulative row-change count since connection open |
| `trim(X[,Y])` | REQ000427 (DONE) | iter-26 — both sides; default Y=" " |
| `typeof(X)` DONE REQ000412 | returns "null" / "integer" / "real" / "text" / "blob" |
| `unhex(X[,Y])` DONE REQ000413 | hex → BLOB; X invalid → NULL; Y is ignored-char set |
| `unicode(X)` DONE REQ000414 | code point of first char; NULL → NULL |
| `unistr(X)` DONE REQ000415 | backslash-escape decoder (\uXXXX, \+XXXXXX, \UXXXXXXXX) |
| `unistr_quote(X)` | REQ000432 (SKIP) | low-value, complex |
| `unlikely(X)` DONE REQ000416 | no-op pass-through |
| `upper(X)` | REQ000433 (DONE) | iter-26 |
| `zeroblob(N)` DONE REQ000417 | N-byte BLOB of 0x00 |

### Sub-bundle REQs (to-implement functions only)

| ID | Function | Effort |
|---|---|---|

### Already implemented (iter-26)

These 10 functions were implemented in iter-26 before the REQ matrix was created:

| REQ ID | Function | Notes |
|---|---|---|
| REQ000418 | coalesce | variadic NULL-skipping |
| REQ000419 | ifnull | 2-arg NULL coalesce |
| REQ000420 | length | code-point count |
| REQ000421 | like | 2-arg pattern match |
| REQ000422 | lower | ASCII lower-case |
| REQ000423 | nullif | NULL-on-equal |
| REQ000424 | printf | alias for format |
| REQ000425 | substr | 1-based, negative start |
| REQ000426 | substring | substr alias |
| REQ000427 | trim | both-sides, default space |
| REQ000433 | upper | ASCII upper-case |

### Out of scope (SKIP)

| REQ ID | Function | Reason |
|---|---|---|
| REQ000428 | load_extension | CGO bridge, out of v1 scope |
| REQ000429 | sqlite_compileoption_get | engine-internal debug helper |
| REQ000430 | sqlite_compileoption_used | engine-internal debug helper |
| REQ000431 | sqlite_offset | requires compile-time flag |
| REQ000432 | unistr_quote | low value, complex escape rules |

### Acceptance

- `internal/SQL/EX/corefunc_test.go` is a single table-driven
  test with one named subtest per function above. Each subtest
  asserts the SQLite-canonical return for a representative
  input. Functions marked DONE already have an existing test;
  the new file should still cover them under a uniform shape.
- Coverage of the new functions must hit 100% via the
  test table (positive and edge cases: NULL in/out, empty
  string, default args, integer overflow for `abs`).
- The dual-runner probe set in `tests/sqlcmp/dual/probe_cases.go`
  should add a representative `corefunc_*` case for each
  newly added function, mirroring the modernc oracle output.

