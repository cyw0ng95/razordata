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
| REQ000144 | SQL | SIMD vectorized execution (batch processing, manual unrolling, selection vectors) | high | XL | iter-08 (operators) | new `SQL/EX/operators_vec.go`, `SQL/EX/batch.go`, columnar batch memory management |
| REQ000145 | SQL | Parallel query execution (worker pool, fan-out/fan-in, channel merge) | high | XL | iter-08 (operators) | new `SQL/EX/operators_parallel.go`, `SQL/EX/sort_parallel.go` |
| REQ000147 | TXN | Complete commit protocol implementation (6 phases: Begin/Read/Write/Pre-commit/Commit/Post-commit, Abort flow) | critical | L | iter-06 (VL) | `TXN/VL/protocol.go` — add detailed CAS loops, validation scan, error handling |
| REQ000148 | ENG | BloomFilter double-hashing with FNV-1a seeds (documented in design but implementation uses different hash) | medium | M | iter-04 (bloom) | `ENG/LS/sst_writer.go`, `ENG/LS/sst_reader.go` — align with design spec |
| REQ000149 | SQL | Columnar batch memory management (`sync.Pool` for 1024-row batches) | medium | M | REQ000144 (vectorization) | new `SQL/EX/batch.go` — `Batch` struct, selection vectors |
| REQ000150 | SQL | Parallel Sort implementation (sample sort for top-k, external merge sort for large datasets) | medium | L | REQ000145 (parallel exec) | new `SQL/EX/sort_parallel.go` |
| REQ000151 | SQL | Parallel HashJoin (sharded hash tables, parallel build and probe) | low | XL | REQ000145 (parallel exec) | `SQL/EX/join.go` — extend with parallel variants |
| REQ000156 | SQL | Executor cost model integration (design mentions cost estimation, no operator selection based on cost) | medium | M | iter-08 (planner) | `SQL/EX/planner.go` — use cost for operator selection |
| REQ000157 | SQL | Expression evaluation SIMD acceleration (batch predicate evaluation) | medium | M | REQ000144 (vectorization) | `SQL/EX/eval.go` — vectorized `EvalBatch` function |
| REQ000158 | TXN | Hazard pointer publication/clear protocol in Read flow (design specifies, verify implementation) | high | S | iter-05 (hazard) | audit `TXN/LC/hazard.go` + `TXN/SN/snapshot.go` |
| REQ000159 | TXN | Per-thread arena lazy initialization via `sync.Pool` (design specifies, verify implementation) | medium | M | iter-05 (arena) | `TXN/MV/arena.go` — add lazy init, exhaustion handling |
| REQ000160 | WAL | Batch commit with `sync.WaitGroup` and write barrier (design in FL cluster) | medium | M | iter-03 (WAL) | `WAL/FL/fl.go` — `BatchSync` implementation |
| REQ000161 | MEM | Clock-sweep integration details (atomic hand, refKey update, eviction gating) | medium | S | iter-02 (buffer pool) | audit `MEM/BF/bf.go` — verify matches design |
| REQ000162 | SQL | Plan memoization with SHA256(AST binary encoding) | low | M | iter-08 (planner) | `SQL/PL/memo.go` — canonical AST serialization |
| REQ000164 | TXN | Epoch manager background goroutine (100ms interval, drain coordination) | high | M | iter-05 (epoch) | `TXN/LC/epoch.go` — add background goroutine if missing |
| REQ000165 | ENG | Compaction job scheduling based on level size budget (design mentions, verify trigger logic) | medium | M | iter-04 (compaction) | `ENG/LS/compaction.go` — size budget monitoring |
| REQ000171 | TXN/VL | WAL integration in commit protocol (write RTCommit/RTData records, call WAL.Sync) | critical | L | iter-06 (VL) | `TXN/VL/protocol.go` — Commit/Insert/Delete must write WAL records per design TXN.md:203-226 |
| REQ000172 | SYS/SY | Implement6-phase graceful shutdown (stop accept → wait tx → flush → stop goroutines → close subsystems → cleanup) | critical | XL | iter-09 (SY) | Replace `SYS/SY/shutdown.go` (36 lines) with full implementation per SYS.md:196-283 |
| REQ000173 | SQL/EX | SIMD vectorized operators (columnar batch layout, selection vectors, manual unrolling) | critical | XL | iter-08 (operators) | Create `SQL/EX/operators_vec.go`, `SQL/EX/batch.go` per SQL.md:377-380 |
| REQ000174 | ENG/LS | BloomFilter double-hash with FNV-1a (seeds0x811C9DC5,0x01000193) replace CRC32 | critical | M | iter-04 (bloom) | `ENG/LS/sst_writer.go`, `ENG/LS/sst_reader.go` — align with ENG.md:93-98 |
| REQ000175 | TXN/LC | Fix hazard pointer Publish (store to single slot, not all) and implement actual memory reclamation | high | L | iter-05 (hazard/epoch) | `TXN/LC/hazard.go` fix Publish, `TXN/LC/epoch.go` implement Reclaim wait+free per TXN.md:96-121 |
| REQ000176 | WAL/FL | Implement batch commit with sync.WaitGroup and write barrier (group multiple fsync into one) | high | M | iter-03 (WAL) | Replace `WAL/FL/fl.go` Sync/BatchSync stubs with actual implementation per WAL.md:103-117 |
| REQ000181 | TXN/LC | Fix goroutine ID tracking (use real goroutine identity, not atomic counter) | medium | M | iter-05 (epoch) | `TXN/LC/epoch.go` — proper goroutine tracking per TXN.md:113-121 |
| REQ000182 | SQL/EX | Parallel Sort implementation (sample sort for top-k, external merge for large datasets) | medium | L | iter-08 (Sort) | Create `SQL/EX/sort_parallel.go` per SQL.md:367-372 |
| REQ000183 | SQL/EX | Expression evaluation SIMD (batch predicate EvalBatch function) | medium | M | iter-08 (eval) | `SQL/EX/eval.go` — add vectorized EvalBatch per SQL.md:303-306 |
| REQ000185 | SQL/EX | Plan memoization with SHA256 canonical AST binary encoding (not JSON) | low | M | iter-08 (planner) | `SQL/PL/memo.go` — implement binary serialization per SQL.md:215 |
| REQ000035 | WAL | Corruption recovery policy: detect torn write, skip vs. fail | critical | S | iter-13 (recovery) | **MOVED TO DONE** — segment file header + envelope CRC; tail tolerated, mid-segment fails loud, Stats counters |
| REQ000061 | TXN | Read-committed isolation (default); upgrade from v1 read-uncommitted | critical | L | iter-05/06 (MVCC + VL) | `TXN/VL/protocol.go`, `TXN/SN/snapshot.go` — re-snapshot per statement |
| REQ000062 | TXN | MVCC reads inside transactions (SELECT in tx sees own writes through Tx iterator) | critical | L | iter-09 (shadow writeSet) | `TXN/SN`, `SQL/EX` — switch session to Tx-aware iterator |
| REQ000113 | SQL | `GROUP BY` (single + multi col; with/without aggregates) | critical | M | iter-08 (Aggregate) | `SQL/PS`, `SQL/EX/aggregate.go` (extend HashAggregate), `SQL/RE` (pushdown) |
| REQ000117 | SQL | `OUTER JOIN` (LEFT/RIGHT/FULL) | critical | M | iter-08 (NestedLoopJoin) | `SQL/EX/join.go` — add outer variants; `SQL/PS` |
| REQ000126 | SQL | Foreign keys (REFERENCES, ON DELETE/UPDATE) | high | L | iter-11 (UNIQUE), iter-12 (catalog), iter-21 (FKEY index?) | `SQL/PS`, `SQL/EX/constraints.go`, new FK validation in writers |
| REQ000102 | SYS | Admin CLI `razor-admin` (schema dump, vacuum, manual compact, integrity check) | high | M | iter-12 (catalog), iter-17 (bench coverage) | new `cmd/razor-admin/main.go`, reuse `SQL/EX` for SQL ops |
| REQ000143 | QUAL | `SQL/RE` coverage: 49% → 80%+ | high | M | iter-07 (RE implementation) | `SQL/RE/*_test.go` — fill error-path branches, subquery flatten cases |
| REQ000074 | SQL | `IndexScan` real seek (replace prefix-scan fallback) | high | M | iter-08 (IndexScan op) | `SQL/EX/operators.go` — call into real `ENG/ID/` once iter-21 ships, or stub |
| REQ000034 | WAL | WAL compression (lz4) | low | M | iter-03 (WAL writer) | `WAL/WR/encode.go` |
| REQ000045 | ENG | Secondary indexes (non-PK columns; lookup by `__idx__:<table>:<col>:<val>`) | low | XL | iter-12 (catalog), iter-21 (ID) | new `ENG/ID/` package, `SQL/PL` index selection |
| REQ000047 | ENG | Prefix bloom filters for range scans | low | M | iter-04 (bloom) | `ENG/LS/sst_writer.go` |
| REQ000048 | ENG | Table registry persistence (`ENG/TB/`) | medium | L | iter-12 (catalog basic) | new `ENG/TB/tb.go` |
| REQ000049 | ENG | Schema cluster (`ENG/SC/`) split from LS | low | M | iter-04 | new `ENG/SC/sc.go`; move `TableSchema` from LS |
| REQ000050 | ENG | Deparser cluster (`ENG/DP/`) split from LS | low | M | iter-04 | new `ENG/DP/dp.go`; move row/block encoding |
| REQ000064 | TXN | Generational arena (reduce GC pressure vs. single allocation) | low | L | iter-05 (arena) | `TXN/MV/arena.go` |
| REQ000084 | SQL | `RE` subquery planning (not just flatten) | medium | M | iter-07 (RE), iter-08 (Subq op) | `SQL/RE/subq.go`, `SQL/PL/planner.go` |
| REQ000085 | SQL | Histogram-based selectivity (replace uniform distribution) | medium | M | iter-12 (catalog stats) | new stats storage, `SQL/PL/estimateCost` |
| REQ000086 | SQL | Parallel query execution (operators in goroutines, merge via channel) | low | XL | iter-08 (operators) | `SQL/EX/ex.go` — channel-based Next; cancellation hygiene |
| REQ000100 | SYS | Network server (TCP/gRPC listener; `SYS.Serve()`) | low | XL | iter-12 (catalog) | new `SYS/SV/sv.go`, protocol buffer or simple line protocol |
| REQ000101 | SYS | Prometheus metrics endpoint (`/metrics` HTTP) | medium | S | iter-00 (MetricHook), iter-100 (server) | `LOG/HK/metric.go` export, `SYS/SV/sv.go` |
| REQ000123 | TXN-API | Configurable isolation levels (`READ COMMITTED` / `REPEATABLE READ` / `SERIALIZABLE` via `SET TRANSACTION`) | medium | M | iter-61 (RC implementation) | `SQL/PS`, `SQL/EX`, `TXN/SN/snapshot.go`, `AP.Options` |
| REQ000128 | OPS | Point-in-time backup / restore (snapshot engine dir, restore to a copy) | medium | M | iter-03 (WAL), iter-04 (manifest) | new `SYS/BK/bk.go`; document procedure |
| REQ000129 | OPS | Online schema migration (`ALTER TABLE ADD/DROP COLUMN` without copy) | low | XL | iter-12 (catalog) | new `SQL/EX/alter.go`, `ENG/LS` schema-aware readers |
| REQ000018 | FIL | File locking (`flock`) for multi-process access | low | S | iter-01 (FIL) | `FIL/FS/fs.go` — optional via `Options`; out of v1 scope (single-process) |

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
| REQ000153 | SYS | Active-tx wait (30s timeout, force-abort on timeout) | iter-14 |
| REQ000154 | SYS | Background-goroutine coordination (compaction, flush, epoch, hook dispatcher) | iter-14 |
| REQ000166 | SYS | Per-subsystem `Close()` ordering in Phase 5 of shutdown | iter-14 |
| REQ000178 | SYS/SY | `validateOptions` (duplicate of REQ000152, same code) | iter-14 |
| REQ000184 | WAL/FL | Implement WriteBuffer struct with 256 KB pre-allocated buffer | iter-15 |
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
| REQ000191 | WAL/RP | Coverage lift: WAL/RP is at75.2% (multi-segment truncate + ErrUnknownRecord added; shortfall now in resync-window edges) | iter-16 |
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
