## TBD

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
|---|---|---|---|---|---|---|
| REQ000034 | WAL | WAL compression (lz4) | low | M | iter-03 (WAL writer) | `WAL/WR/encode.go` |
| REQ000048 | ENG | Table registry persistence (`ENG/TB/`) | medium | L | iter-12 (catalog basic) | new `ENG/TB/tb.go` |
| REQ000064 | TXN | Generational arena (reduce GC pressure vs. single allocation) | low | L | iter-05 (arena) | `TXN/MV/arena.go` |
| REQ000100 | SYS | Network server (TCP/gRPC listener; `SYS.Serve()`) | low | XL | iter-12 (catalog) | new `SYS/SV/sv.go`, protocol buffer or simple line protocol |
| REQ000305 | TXN | Generational arena with Young/Old split (young bump-allocate, old epoch-reclaim; reduces epoch manager pressure) | medium | L | iter-05 (arena), `REQ000064` | `TXN/MV/arena.go` — generation promotion policy |
| REQ000307 | TXN | MV-OCC timestamp ordering (Silo-style, O(1) per-txn read-set validation; targets 1M+ txn/s on 16 cores) | critical | XL | iter-20 (commit protocol), `REQ000175` | `TXN/MV/occ.go` (new) — `Validation` phase rewritten; conflict-free reorder |
| REQ000311 | SQL | Operator codegen (`go generate` template → specialized Go funcs; inline caches eliminate virtual dispatch) | medium | XL | REQ000310, iter-08 (operators) | `SQL/EX/codegen/` (new) — templated operator skeletons; build tag for codegen |
| REQ000313 | SQL | Adaptive query compilation (first 2 invocations interpreted, hot path swaps to JIT via `go generate` template; 2-5x OLAP speedup) | medium | XL | REQ000311 | `SQL/EX/adqc.go` (new) — hot-path detector + plan swap |
| REQ000315 | SQL | Learned cardinality estimation (CardinalityNet/MSCN; bootstraps from existing histograms) | medium | L | REQ000085 (histogram), iter-23 (ANALYZE) | `SQL/PL/learned.go` (new) — ONNX runtime or pure-Go MLP; training data from ANALYZE |
| REQ000316 | SQL | Incremental materialized views (auto-maintained aggregation views with query routing) | medium | L | iter-08 (operators), iter-12 (catalog) | `SQL/EX/matview.go` (new); `ENG/LS` triggers on view base tables |
| REQ000321 | TXN | Deterministic Simulation Testing framework (FoundationDB-style scheduled threads + simulated clock + simulated disk; millions of random schedules) | high | XL | iter-17 (chaos), iter-13 (recovery) | new `tests/dst/` framework; subsystem-aware simulated drivers |
| REQ000444 | SQL/EX | UPDATE deadlock / lock leak — second UPDATE on the same table hangs until ctx deadline; first UPDATE returns quickly (repro: in `evidence/slt_lang_update.test` and any direct `UPDATE ... ; UPDATE ...` sequence). Likely a write lock or MVCC version-chain issue. Discovered via `TestSLT_Each/slt_lang_update.test` (15 fails) | high | M | iter-26 (EX lock chain) | `SQL/EX/` — UPDATE executor / lock acquisition path; check row-level write lock release after first UPDATE completes |
| REQ000446 | SQL/EX | Scalar `IN (literal-list)` returns 0 rows — `SELECT 1 IN (2)` and `SELECT 1 IN (2,3,4)` both return zero rows; SQLite returns one row containing 0/1. Discovered via `TestSLT_Each/in1.test` and `in2.test` | medium | M | iter-26 (EX IN) | `SQL/EX/expr.go` — `expr IN (list)` should evaluate to a single boolean result row, not an empty set; also affects `NOT IN` |
| REQ000449 | SQL/PS | `INSERT OR REPLACE` and standalone `REPLACE INTO` not supported in the parser — fails with "expected INTO, got OR". Discovered via `TestSLT_Each/slt_lang_replace.test` (L38) | low | S | iter-26 (UPSERT) | `SQL/PS/ps.go` — extend `parseInsert` to accept `OR REPLACE` / `OR ABORT` / etc. conflict resolution clauses; or add a `parseReplace` for the `REPLACE INTO` form |
| REQ000450 | SQL/PS | `CREATE TEMP VIEW` not supported — parser expects `CREATE TABLE` after `CREATE TEMP` and fails with "expected TABLE, got identifier". Discovered via `TestSLT_Each/slt_lang_createview.test` (L48) | low | S | iter-26 | `SQL/PS/ps.go` `parseCreateView` — accept optional `TEMP`/`TEMPORARY` keyword between `CREATE` and `VIEW`; semantics: same as `CREATE VIEW` for v1 |

## DONE

| ID | Subsystem | Requirement | Iteration |
|---|---|---|---|
| REQ000148 | ENG | BloomFilter double-hashing with FNV-1a seeds (documented in design but implementation uses different hash) | iter-04 (bloom) |
| REQ000159 | TXN | Per-thread arena lazy initialization via `sync.Pool` (design specifies, verify implementation) | iter-05 (arena) |
| REQ000165 | ENG | Compaction job scheduling based on level size budget (design mentions, verify trigger logic) | iter-04 (compaction) |
| REQ000045 | ENG | Secondary indexes (non-PK columns; lookup by `__idx__:<table>:<col>:<val>`) | iter-12 (catalog), iter-21 (ID) |
| REQ000129 | OPS | Online schema migration (`ALTER TABLE ADD/DROP COLUMN` without copy) | iter-12 (catalog) |
| REQ000349 | SQL/PS | Missing SQLite builtin scalar functions: `LENGTH`, `TYPEOF`, `UNICODE`, `QUOTE`, `ZEROBLOB`, `RANDOMBLOB`, `HEX`, `SOUNDEX`. Each emits `ps: syntax error` rather than a typed "unsupported" error, so the SLT classifier must fall back to substring matching on `syntax error` | iter-25 surfacing (edge probe `TestEdge_Expressions`) |
| REQ000384 | SQL/EX | Scalar function `abs(X)` — returns absolute value, NULL→NULL, string→0.0, MIN_INT64→error | iter-26 |
| REQ000385 | SQL/EX | Scalar function `changes()` — last INSERT/UPDATE/DELETE row count; not yet wired to session state | iter-26 |
| REQ000401 | SQL/EX | Scalar function `quote(X)` — SQL literal rendering; strings single-quoted with escape, BLOBs as X'hex' | iter-26 |
| REQ000402 | SQL/EX | Scalar function `random()` — pseudo-random int64; exclude MIN_INT64 | iter-26 |
| REQ000403 | SQL/EX | Scalar function `randomblob(N)` — N-byte random BLOB | iter-26 |
| REQ000404 | SQL/EX | Scalar function `replace(X,Y,Z)` — string substitution; Y="" returns X unchanged | iter-26 |
| REQ000405 | SQL/EX | Scalar function `round(X[,Y])` — round to Y decimal places; Y default 0; Y<0 → 0 | iter-26 |
| REQ000406 | SQL/EX | Scalar function `rtrim(X[,Y])` — trim right; default Y=" " | iter-26 |
| REQ000407 | SQL/EX | Scalar function `sign(X)` — -1/0/+1 or NULL for non-numeric | iter-26 |
| REQ000408 | SQL/EX | Scalar function `soundex(X)` — soundex encoding; "?000" for non-ASCII / NULL | iter-26 |
| REQ000409 | SQL/EX | Scalar function `sqlite_source_id()` — fixed string for v1 | iter-26 |
| REQ000410 | SQL/EX | Scalar function `sqlite_version()` — fixed string for v1 | iter-26 |
| REQ000411 | SQL/EX | Scalar function `total_changes()` — cumulative row-change count since connection open | iter-26 |
| REQ000162 | SQL | Plan memoization with SHA256(AST binary encoding) | iter-08 |
| REQ000185 | SQL/EX | Plan memoization with SHA256 canonical AST binary encoding (not JSON) | iter-08 |
| REQ000086 | SQL | Parallel query execution (operators in goroutines, merge via channel) | iter-08 |
| REQ000128 | OPS | Point-in-time backup / restore (snapshot engine dir, restore to a copy) | iter-08 |
| REQ000418 | SQL/EX | Scalar function `coalesce(X,Y,...)` — variadic NULL-skipping | iter-26 |
| REQ000391 | SQL/EX | Scalar function `hex(X)` — BLOB/text → uppercase hex; integer is converted via text first | iter-26 |
| REQ000392 | SQL/EX | Scalar function `iif(B,V,...)` / `if()` alias — short-circuit CASE; NULL condition → false branch | iter-26 |
| REQ000398 | SQL/EX | Scalar function `max(X,Y,...)` — multi-arg scalar max, NULLs skipped, all-NULL → NULL | iter-26 |
| REQ000399 | SQL/EX | Scalar function `min(X,Y,...)` — multi-arg scalar min, NULLs skipped, all-NULL → NULL | iter-26 |
| REQ000413 | SQL/EX | Scalar function `unhex(X[,Y])` — hex → BLOB; X invalid → NULL; Y ignored | iter-26 |
| REQ000414 | SQL/EX | Scalar function `unicode(X)` — code point of first char; NULL → NULL | iter-26 |
| REQ000415 | SQL/EX | Scalar function `unistr(X)` — backslash-escape decoder | iter-26 |
| REQ000416 | SQL/EX | Scalar function `unlikely(X)` — no-op pass-through | iter-26 |
| REQ000417 | SQL/EX | Scalar function `zeroblob(N)` — N-byte BLOB of 0x00 | iter-26 |
| REQ000386 | SQL/EX | Scalar function `char(X1,...,XN)` — Unicode code point → character; variadic int args; any NULL → NULL | iter-26 |
| REQ000387 | SQL/EX | Scalar function `concat(X,...)` — concatenate all args; any NULL → NULL (SQLite semantics) | iter-26 |
| REQ000388 | SQL/EX | Scalar function `concat_ws(SEP,X,...)` — concat with separator; SEP=NULL → NULL, skips NULL values | iter-26 |
| REQ000389 | SQL/EX | Scalar function `format(FORMAT,...)` — printf-style formatting via fmt.Sprintf | iter-26 |
| REQ000390 | SQL/EX | Scalar function `glob(X,Y)` — filename glob match with * and ? wildcards | iter-26 |
| REQ000393 | SQL/EX | Scalar function `instr(X,Y)` — 1-based position of Y in X, 0 if not found; NULL → 0 | iter-26 |
| REQ000394 | SQL/EX | Scalar function `last_insert_rowid()` — last successful INSERT rowid | iter-26 |
| REQ000395 | SQL/EX | Scalar function `likelihood(X,Y)` — no-op pass-through; planner hint | iter-26 |
| REQ000396 | SQL/EX | Scalar function `likely(X)` — no-op pass-through; planner hint | iter-26 |
| REQ000397 | SQL/EX | Scalar function `ltrim(X[,Y])` — trim left whitespace or chars in Y | iter-26 |
| REQ000400 | SQL/EX | Scalar function `octet_length(X)` — byte length (not code-point count) | iter-26 |
| REQ000442 | SQL/EX | SLT gap survey — 30/42 common patterns identified; gaps tracked in individual REQs | iter-26 |
| REQ000447 | SQL/EX | `count(DISTINCT x)`, `avg(DISTINCT x)`, `sum(DISTINCT x)` — DISTINCT aggregate semantics verified; NULLs skipped, dedup works | iter-26 |
| REQ000419 | SQL/EX | Scalar function `ifnull(X,Y)` — 2-arg NULL coalesce | iter-26 |
| REQ000420 | SQL/EX | Scalar function `length(X)` — code-point count | iter-26 |
| REQ000421 | SQL/EX | Scalar function `like(X,Y[,Z])` — 2-arg pattern match | iter-26 |
| REQ000422 | SQL/EX | Scalar function `lower(X)` — ASCII lower-case | iter-26 |
| REQ000423 | SQL/EX | Scalar function `nullif(X,Y)` — NULL-on-equal | iter-26 |
| REQ000424 | SQL/EX | Scalar function `printf(FORMAT,...)` — alias for format | iter-26 |
| REQ000425 | SQL/EX | Scalar function `substr(X,Y[,Z])` — 1-based, negative start | iter-26 |
| REQ000426 | SQL/EX | Scalar function `substring(X,Y[,Z])` — substr alias | iter-26 |
| REQ000427 | SQL/EX | Scalar function `trim(X[,Y])` — both-sides, default space | iter-26 |
| REQ000433 | SQL/EX | Scalar function `upper(X)` — ASCII upper-case | iter-26 |
| REQ000018 | FIL | File locking (`flock`) for multi-process access | iter-27 |
| REQ000126 | SQL | Foreign keys (REFERENCES, ON DELETE/UPDATE) | iter-27 |
| REQ000160 | WAL | Batch commit with sync.WaitGroup and write barrier | iter-03 |
| REQ000244 | SQL/EX | ALTER TABLE executor (online schema migration) | iter-27 |
| REQ000443 | WAL/WR | `encodeRecord` allocation reduction (4→2 allocs/record, 217ns→148ns, 168B→160B) | iter-27 |
| REQ000161 | MEM | Clock-sweep integration details (atomic hand, refKey update, eviction gating) | iter-02 |
| REQ000164 | TXN | Epoch manager background goroutine (100ms interval, drain coordination) | iter-27 |
| REQ000175 | TXN/LC | Fix hazard pointer Publish (store to single slot, not all) and implement actual memory reclamation | iter-27 |
| REQ000181 | TXN/LC | Fix goroutine ID tracking (use real goroutine identity, not atomic counter) | iter-27 |
| REQ000183 | SQL/EX | Expression evaluation SIMD (batch predicate EvalBatch function) | iter-27 |
| REQ000143 | QUAL | SQL/RE coverage: 49% → 80%+ | iter-27 |
| REQ000284 | ENG/ID | BTree delete rebalancing — no merge/redistribute after delete, tree becomes sparse | iter-27 |
| REQ000310 | SQL | Real SIMD intrinsics for filter/projection (AVX2/AVX-512) | iter-27 |
| REQ000312 | SQL | Vector-aware hash join (Radix partition; SIMD probe) | iter-27 |
| REQ000314 | ENG | Columnar SST layout (PAX / hybrid row-columnar) | iter-27 |
| REQ000317 | WAL | Parallel WAL replay by key-range partition | iter-27 |
| REQ000369 | SQL/EX | All aggregate functions (COUNT/SUM/AVG/MIN/MAX) return NULL — verified working | iter-27 |
| REQ000440 | SQL/EX | VACUUM and ANALYZE not routed — fixed in buildWriterOp | iter-26 |
| REQ000441 | SQL/EX | ALTER TABLE ADD COLUMN not implemented — fixed in buildWriterOp | iter-26 |
| REQ000443b | SQL/EX | Fix negative_literal eval pipeline bug (case-sensitive Lookup, UnaryExpr column extraction) | iter-27 |
| REQ000435 | SQL/PS | CREATE TRIGGER parser (BEFORE/AFTER, FOR EACH ROW, BEGIN...END body) | iter-27 |
| REQ000436 | SQL/PS+PL+EX | Recursive CTE (WITH RECURSIVE flag, FROM-subquery parser fix, inner query dispatch) | iter-27 |
| REQ000248 | SQL/PS | Parse generated columns (AS (expr) STORED/VIRTUAL) | iter-27 |
| REQ000249 | SQL/EX | Generated column materialization on INSERT/UPDATE | iter-27 |
| REQ000318 | ENG/LS | Write rate-limited compactor (token bucket, Options.CompactionRateLimit) | iter-27 |
| REQ000319 | ENG/LS | Sub-compaction parallelism (key-range sub-jobs, worker pool) | iter-27 |
| REQ000297 | ENG/LS | SST block-level dictionary compression (frequency-based, no C deps) | iter-27 |
| REQ000299 | WAL/WR | WAL columnar batch encoding (column-major, single envelope CRC) | iter-27 |
| REQ000320 | ENG/LS | Configurable compaction style (leveled/tiered/hybrid) | iter-27 |
| REQ000303 | MEM/BF | W-TinyLFU admission policy (Count-Min Sketch, 32KB footprint) | iter-27 |
| REQ000304 | MEM/OF | Off-heap large object pool (16 size classes, sync.Pool) | iter-27 |
| REQ000306 | TXN/MV | Stack-allocate version nodes (escape analysis hints, 0 allocs/op) | iter-27 |
| REQ000308 | TXN/LC | QSBR read path (64-shard quiescent reclamation) | iter-27 |
| REQ000356 | SQL/LX | Unary NOT as logical prefix operator | iter-27 |
| REQ000358 | SQL/PS | XOR parser gap fix | iter-27 |
| REQ000084 | SQL/PL+RE | Subquery planning (FROM-subquery parser support) | iter-27 |
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
| REQ000359 | SQL/EX | String concat NULL semantics — `'a' \ | iter-26.1 (v0.26.3) |
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
| REQ000324 | TEST | SLT test file parser (`statement ok\ | iter-25 |
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
| REQ000074 | SQL/EX | IndexScan real seek (replace prefix-scan fallback; range bounds `>`, `>=`, `BETWEEN`, inclusive/exclusive) | iter-27 (Phase 6) |
| REQ000295 | FIL | io_uring async I/O wrapper (raw syscall shim Linux-only with `uring_other.go` fallback; `IORING_ENTER_GETEVENTS` constant) | iter-27 (Phase 6) |
| REQ000296 | FIL | Direct I/O + fixed-fd (`IOSQE_FIXED_FILE` constant, `Ring.RegisterFixedFile()`, `Ring.UnregisterFixedFile()`) | iter-27 (Phase 6) |
| REQ000301 | WAL | Async fsync (buffered channel with `AsyncSyncResult`, `inflightFsyncs` WaitGroup, `Close` blocks on in-flight fsyncs) | iter-27 (Phase 6) |
| REQ000309 | ENG | NUMA-aware placement (`NodeCount`, `IsAvailable`, `CurrentNode`, `PinWorker`, `bufferSlot.nodeID`, subcompaction worker `LockOSThread`) | iter-27 (Phase 6) |
| REQ000156 | SQL | Cost-based scan selection in planner — `pickCheaperScan` compares `estimateCost` for SeqScan vs IndexScan candidates and swaps to the cheaper one when the WHERE column has a writer-registered index | iter-27 |
| REQ000437 | SQL/EX | Full SQL aggregate DISTINCT support — `SUM/AVG/MIN/MAX/GROUP_CONCAT(DISTINCT col)` now dedup before aggregating (parity with `COUNT(DISTINCT col)`); NULLs are excluded from the distinct set per SQLite semantics; parser routes DISTINCT through the IDENT aggregate path for `GROUP_CONCAT` | iter-27 |
| REQ000182 | SQL/EX | Parallel Sort implementation (sample sort for top-k, external merge for large datasets) | iter-08 |
| REQ000246 | SQL/PS | Parse TRIGGER (CREATE TRIGGER, BEFORE/AFTER, FOR EACH ROW) | iter-07 |
| REQ000247 | SQL/EX | TRIGGER executor (fire on INSERT/UPDATE/DELETE) | iter-07 |
| REQ000256 | SQL/PS | Parse VACUUM / ANALYZE | iter-21 |
| REQ000285 | ENG/ID | uint32 page ID overflow protection — wraps to 0 (sentinel for "no page") | iter-23 |
| REQ000286 | SQL/EX | Window materialize context propagation — uses context.Background() instead of caller's ctx | iter-23 |
| REQ000287 | SQL/EX | Window setOutput allocation optimization — allocates 2 new slices per call on hot path | iter-23 |
| REQ000298 | ENG | LSM-aware cross-block shared dictionary (multiple data blocks in one SST share a trained dict) | iter-27 |
| REQ000434 | SQL/PS | NOT BETWEEN syntax error fix | iter-26 |
| REQ000438 | SQL/EX | Scalar function eval error routing fix | iter-26 |
| REQ000439 | SQL/PS | EXPLAIN statement support | iter-26 |
| REQ000049 | ENG/SC | Schema cluster split from LS (TableSchema, ColumnDef, ColumnType, Row, Validator) | iter-28 |
| REQ000050 | ENG/DP | Deparser cluster split from LS (EncodeRow/DecodeRow, EncodeBlock/DecodeBlock) | iter-28 |
| REQ000300 | ENG/LS | Tier-aware storage scheduler (PlacementPolicy, StoragePolicy, per-level device routing) | iter-28 |
| REQ000302 | MEM/BF | PMem-aware buffer pool (MADV_HUGEPAGE, PMemFile, slot tier field) | iter-28 |
| REQ000445 | SQL/EX | NULL three-valued logic: `<`, `<=`, `>`, `>=`, `=`, `!=` comparisons with NULL operand → return NULL (UNKNOWN), not a boolean | iter-26 |
| REQ000448 | SQL/EX | SLT runner `skipif`/`onlyif` engine-name gating — `NewRunner(drv, cls, "razor")` evaluates directives against engine name; `onlyif sqlite` skips on Razor, `onlyif razor` executes | iter-26 |