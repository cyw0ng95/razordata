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
| REQ000323 | TEST | SQLLogicTest corpus mirror as git submodule (`tests/sqlcmp/corpus` from `MarvBeer/sqlite-test-suite`); build-tag-gated fetch | high | S | iter-25 plan | `.gitmodules` entry; build tag `slt_corpus` |
| REQ000324 | TEST | SLT test file parser (`statement ok\|error`, `query <types> <sort> <label>`, `halt`, `hash-threshold`, `skipif`, `onlyif`; tolerant, line-oriented) | high | M | REQ000323 | new `tests/sqlcmp/slt/parser.go` + `types.go`; fixtures in `slt/testdata/*.test` |
| REQ000325 | TEST | Driver interface (`Connect`/`Close`/`Exec`/`Query`; returns `ResultSet` with typed `Value` cells) | high | S | REQ000324 | new `tests/sqlcmp/slt/driver.go` |
| REQ000326 | TEST | Razordata driver implementation wrapping public `internal/SYS.Engine` API; SQL errors classified as `skipped` not `failed` | high | M | REQ000325 | new `tests/sqlcmp/slt/razor_driver.go` |
| REQ000327 | TEST | SLT runner + type-aware result diff (`T`/`I`/`R`/`NULL`; `nosort`/`rowsort`/`valuesort`; `label` grouping) | high | M | REQ000324, REQ000326 | new `tests/sqlcmp/slt/runner.go` + `diff.go` |
| REQ000328 | TEST | Corpus subset default target (~200 `.test` files from `select1-4`, `index/`, `evidence/`, `minmax/`, `cast/`, `null/`, `decimal/`, `datetime/`); calibrated pass-rate threshold; full corpus gated by `slt_corpus_full` tag | high | S | REQ000327 | new `tests/sqlcmp/slt/sanygo_test.go` |
| REQ000329 | TEST | Pure-Go reference oracle via `modernc.org/sqlite` dependency (test-only); fallback to hand-rolled `tests/sqlcmp/oracle/` if modernc fails to build in sandbox | high | M | iter-25 plan | `go.mod` entry; new `tests/sqlcmp/dual/` package |
| REQ000330 | TEST | Dual runner: same SQL on Razordata + oracle; result-set diff after normalization | high | M | REQ000329 | new `tests/sqlcmp/dual/dual.go` + `dual_test.go` |
| REQ000331 | TEST | Result-set normalization (int→int64, float round, strip whitespace, column-name sort, `''`/`NULL` config flag) | medium | M | REQ000330 | new `tests/sqlcmp/dual/normalize.go` |
| REQ000332 | TEST | Dual case authoring convention (`dualCase` struct, table-driven; seed ~50 cases across DDL/DML/aggregates/joins) | medium | M | REQ000330 | new `tests/sqlcmp/dual/cases/*.go` (5 files) |
| REQ000333 | TEST | JUnit XML output for CI consumption (pass/fail/skip per testcase) | medium | S | REQ000327 | new `tests/sqlcmp/slt/junit.go` |
| REQ000334 | TEST | Coverage snapshot (`tests/sqlcmp/slt/coverage.json` per run; `coverage.baseline.json` committed; regression check) | medium | S | REQ000328 | new `tests/sqlcmp/slt/coverage.go` |
| REQ000335 | TEST | CI workflow: PR + nightly; run subset; upload JUnit; post pass-rate PR comment vs `main` | medium | S | REQ000333, REQ000334 | new `.github/workflows/slt.yml` |
| REQ000336 | TEST | Developer guide: how to run, add cases, re-baseline coverage | low | S | iter-25 plan | new `tests/sqlcmp/README.md` + `tests/sqlcmp/slt/README.md` |
| REQ000337 | TEST | Architecture note lives in iteration doc (not `design/`); test harness is operational, not architectural | low | S | iter-25 plan | `development/iterations/iter-25-sqlite-testsuite.md` (this file) |

## Unfixed Bugs (surfaces as requirements)

These rows are bugs that were discovered during a prior iteration but
not fixed in that iteration's scope. The "Touches" column points to
the discovery context. See `AGENTS.md` Bug-To-Requirement Rule.

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
|---|---|---|---|---|---|---|

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
