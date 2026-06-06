# Razordata Requirements

> Feature catalog for future iteration planning. Each row describes one
> requirement with its current implementation status. Select rows to
> form new iterations.

## Status Legend

- **done** — Implemented and tested (shipped in v1)
- **partial** — Partially implemented; specific gap documented
- **planned** — Designed in `design/ARCH.md` and `design/subsystems/*.md`, not yet implemented
- **proposed** — Not in current design; candidate for future consideration

## Core Subsystem Requirements

| ID | Subsystem | Requirement | Status | Notes |
|---|---|---|---|---|
| R01 | LOG | `Logger` wraps `log/slog` with atomic level control | done | iter-00 |
| R02 | LOG | Structured key-value output (JSON/text) | done | iter-00 |
| R03 | LOG | Log file rotation on size threshold | done | iter-00 |
| R04 | LOG | Hook registry with async dispatch | done | iter-00 |
| R05 | LOG | `TraceHook` for SQL query tracing | done | iter-00 |
| R06 | LOG | `MetricHook` for throughput/latency counters | done | iter-00 |
| R07 | LOG | `ProfileHook` for CPU/heap dump on error | done | iter-00 |
| R08 | LOG | Bounded channel: drop on overflow, never block log path | done | iter-00 |
| R09 | LOG | Log compression after rotation | proposed | Open issue in LOG.md |
| R10 | FIL | Block I/O via `pread`/`pwrite` | done | iter-01 |
| R11 | FIL | `O_DIRECT` support with fallback | done | iter-01 |
| R12 | FIL | CRC32 checksum per block | done | iter-01 |
| R13 | FIL | `MetaPage` with magic/version/catalog root | done | iter-01 |
| R14 | FIL | Path validation (reject `..`, symlinks) | done | iter-01 |
| R15 | FIL | Cached directory FDs for `SyncDir` | done | iter-01 |
| R16 | FIL | WAL segment handle pool | done | iter-01 |
| R17 | FIL | `ftruncate` for replay segment shrinking | done | iter-01 |
| R18 | FIL | File locking (`flock`) for multi-process access | proposed | v2+; single-process is v1 |
| R19 | FIL | `MADV_DONTNEED` hints for buffer eviction | proposed | Open issue in FIL.md |
| R20 | MEM | Buffer pool: clock-sweep LRU eviction | done | iter-02 |
| R21 | MEM | Atomic `Pin`/`Unpin` with eviction gating | done | iter-02 |
| R22 | MEM | O(1) hash table lookup by `blockID` | done | iter-02 |
| R23 | MEM | Hint file for warm startup | done | iter-02 |
| R24 | MEM | Hint file compression (gzip) when > 1 MB | done | iter-02 |
| R25 | MEM | `sync.Pool` for page/iterator buffers | done | iter-02 |
| R26 | MEM | `mmap` instead of `read`/`write` | proposed | Open issue in MEM.md |
| R27 | WAL | Sequential append with LSN allocation | done | iter-03 |
| R28 | WAL | 64 MB segment rotation | done | iter-03 |
| R29 | WAL | `fsync` on commit | done | iter-03 |
| R30 | WAL | Batch flush / write barrier | done | iter-03 |
| R31 | WAL | WAL replay on startup | done | iter-03 |
| R32 | WAL | Checkpoint detection and segment truncation | done | iter-03 |
| R33 | WAL | `RTCheckpoint` record with catalog root | done | iter-03 |
| R34 | WAL | WAL compression (lz4) | proposed | Open issue in WAL.md |
| R35 | WAL | Corruption recovery: skip vs. fail | proposed | Open issue in WAL.md |
| R36 | ENG | Lock-free skiplist memtable | done | iter-04 |
| R37 | ENG | Memtable freeze + flush to L0 SST | done | iter-04 |
| R38 | ENG | SST writer: data blocks, index, bloom, footer | done | iter-04 |
| R39 | ENG | SST reader: block decode, bloom check, index binary search | done | iter-04 |
| R40 | ENG | Leveled compaction with multi-way merge sort | done | iter-04 |
| R41 | ENG | Atomic manifest versioning (temp + rename + `fsync`) | done | iter-04 |
| R42 | ENG | Bloom filter (10 bits/key, double-hashing) | done | iter-04 |
| R43 | ENG | Delta-encoded data blocks with restart points | done | iter-04 |
| R44 | ENG | `ENG/LS` benchmarks (skiplist, SST, flush) | proposed | AGENTS.md rule violation |
| R45 | ENG | Secondary indexes | proposed | v2 in `ENG/ID/` |
| R46 | ENG | IndexScan real seek (replace prefix-scan fallback) | partial | iter-08 R10; waits for `ENG/ID/` |
| R47 | ENG | Prefix bloom filters for range scans | proposed | Open issue in ENG.md |
| R48 | ENG | Table registry persistence (`ENG/TB/`) | proposed | Catalog lost on reopen |
| R49 | ENG | Schema cluster (`ENG/SC/`) split from LS | proposed | Currently folded into LS |
| R50 | ENG | Deparser cluster (`ENG/DP/`) split from LS | proposed | Currently folded into LS |
| R51 | TXN | MVCC version chain (lock-free, CAS insertion) | done | iter-05 |
| R52 | TXN | Per-thread arena allocation | done | iter-05 |
| R53 | TXN | Hazard pointer coordination | done | iter-05 |
| R54 | TXN | Epoch-based reclamation | done | iter-05 |
| R55 | TXN | Read view per transaction | done | iter-05 |
| R56 | TXN | Commit protocol: validate → assign `commitTS` → CAS `endTS` | done | iter-06 |
| R57 | TXN | Write-write conflict detection | done | iter-06 |
| R58 | TXN | Transaction slots (max 1024 concurrent) | done | iter-06 |
| R59 | TXN | Shadow writeSet for ROLLBACK | done | iter-06 |
| R60 | TXN | Read-uncommitted isolation (v1) | done | iter-09 R29 |
| R61 | TXN | Read-committed isolation | proposed | v2 |
| R62 | TXN | Full MVCC reads inside transactions (SELECT in tx) | partial | iter-09 shadow writeSet; v1.1 adds MVCC iterator |
| R63 | TXN | Savepoint support | done | iter-09 R15 |
| R64 | TXN | Generational arena | proposed | Open issue in TXN.md |
| R65 | SQL | Lexer with keyword map | done | iter-07 |
| R66 | SQL | Recursive-descent parser | done | iter-07 |
| R67 | SQL | AST node types for DDL/DML/SELECT | done | iter-07 |
| R68 | SQL | Constant folding | done | iter-07 |
| R69 | SQL | Predicate pushdown | done | iter-07 |
| R70 | SQL | Subquery flattening (IN/EXISTS) | done | iter-07 |
| R71 | SQL | Plan memoization (SHA256 of AST) | done | iter-08 |
| R72 | SQL | Cost estimation (uniform distribution) | done | iter-08 |
| R73 | SQL | `SeqScan` operator | done | iter-08 |
| R74 | SQL | `IndexScan` operator (prefix-scan fallback) | partial | iter-08 R10; waits for `ENG/ID/` |
| R75 | SQL | `Filter` / `Project` / `Sort` / `Limit` operators | done | iter-08 |
| R76 | SQL | `Insert` / `Update` / `Delete` operators | done | iter-08 |
| R77 | SQL | `Aggregate` (COUNT/SUM/AVG/MIN/MAX) | done | iter-08 |
| R78 | SQL | `HashAggregate` | done | iter-08 |
| R79 | SQL | `NestedLoopJoin` (INNER/CROSS) | done | iter-08 |
| R80 | SQL | `Distinct` operator | done | iter-08 |
| R81 | SQL | `EXPLAIN` rendering | done | iter-08 |
| R82 | SQL | Subquery operator (IN/EXISTS/scalar) | done | iter-08 |
| R83 | SQL | `SQL/RE` coverage: 49% → 80%+ | proposed | Open issue in ROADMAP |
| R84 | SQL | `RE` subquery planning (not just flatten) | proposed | Open issue in SQL.md |
| R85 | SQL | Histogram-based selectivity | proposed | Open issue in SQL.md |
| R86 | SQL | Parallel query execution | proposed | Open issue in SQL.md |
| R87 | SYS | `Engine.Open` with `Options` validation | done | iter-09 |
| R88 | SYS | `Engine.Close` with graceful shutdown | done | iter-09 |
| R89 | SYS | `Engine.Begin` → `Session` | done | iter-09 |
| R90 | SYS | `Engine.Stats` aggregation | done | iter-09 |
| R91 | SYS | Error type taxonomy (retryable vs fatal) | done | iter-09 |
| R92 | SYS | `Session.Query` / `Session.Exec` | done | iter-09 |
| R93 | SYS | `Session.SetDeadline` with `atomic.Value` | done | iter-09 |
| R94 | SYS | `Transaction.Commit` / `Rollback` | done | iter-09 |
| R95 | SYS | `Transaction.Savepoint` / `RollbackTo` | done | iter-09 |
| R96 | SYS | `Stmt.Prepare` / `Query` / `Exec` / `Close` | done | iter-09 |
| R97 | SYS | SIGTERM/SIGINT graceful shutdown handler | done | iter-09 |
| R98 | SYS | Session pooling (`sync.Pool`) | proposed | v2 |
| R99 | SYS | `ReadOnly` mode in `Options` | proposed | v2 |
| R100 | SYS | Network server (TCP/gRPC) | proposed | v2 |
| R101 | SYS | Prometheus metrics endpoint | proposed | v2 |
| R102 | SYS | Admin interface (schema dump, vacuum, manual compaction) | proposed | v2 |

## Cross-Cutting Requirements

| ID | Area | Requirement | Status | Notes |
|---|---|---|---|---|
| R103 | DDL | `CREATE TABLE` with column types and `PRIMARY KEY` | done | iter-08 |
| R104 | DDL | `DROP TABLE` | done | iter-08 |
| R105 | DDL | `NOT NULL` constraint enforcement | partial | Parser accepts; validator wiring pending |
| R106 | DDL | `DEFAULT` value substitution | partial | Parser accepts; evaluator wiring pending |
| R107 | DDL | `UNIQUE` constraint | proposed | Not in v1 design |
| R108 | DML | `INSERT` with column list | done | iter-08 |
| R109 | DML | `UPDATE` with `WHERE` | done | iter-08 |
| R110 | DML | `DELETE` with `WHERE` | done | iter-08 |
| R111 | DML | `SELECT` with `WHERE` / `ORDER BY` / `LIMIT` / `OFFSET` | done | iter-08 |
| R112 | DML | `COUNT(*)` / `SUM` / `AVG` / `MIN` / `MAX` aggregates | done | iter-08 |
| R113 | DML | `GROUP BY` | proposed | Not in v1 |
| R114 | DML | `DISTINCT` | done | iter-08 |
| R115 | DML | `IN` / `EXISTS` / scalar subqueries | done | iter-08 |
| R116 | DML | `JOIN` (INNER, CROSS) | done | iter-08 |
| R117 | DML | `OUTER JOIN` (LEFT/RIGHT/FULL) | proposed | Not in v1 |
| R118 | DML | `LIKE` pattern matching | done | iter-07 |
| R119 | DML | `BETWEEN` | done | iter-07 |
| R120 | DML | `IS NULL` / `IS NOT NULL` | done | iter-07 |
| R121 | TXN | `BEGIN` / `COMMIT` / `ROLLBACK` | done | iter-08 |
| R122 | TXN | Savepoints (`SAVEPOINT name` / `ROLLBACK TO name`) | done | iter-09 |
| R123 | TXN | `READ COMMITTED` / `REPEATABLE READ` / `SERIALIZABLE` | proposed | v2 |
| R124 | API | Parameter binding via `?` placeholders | done | iter-08 |
| R125 | API | `EXPLAIN <query>` | done | iter-08 |
| R126 | API | Foreign keys | proposed | Out of v1 scope |
| R127 | OPS | Catalog persistence across restarts | partial | In-memory catalog lost; v1.1 ships catalog LSM in `ENG/ID/` |
| R128 | OPS | Point-in-time backup / restore | proposed | Not designed |
| R129 | OPS | Online schema migration | proposed | Not designed |
| R130 | OBS | Query tracing via `TraceHook` | done | iter-00 |
| R131 | OBS | Latency histograms via `MetricHook` | done | iter-00 |
| R132 | OBS | CPU/heap profiling on error | done | iter-00 |
| R133 | OBS | Structured stats aggregation (`Engine.Stats`) | done | iter-09 |

## Performance & Quality Requirements

| ID | Area | Requirement | Status | Notes |
|---|---|---|---|---|
| R134 | Quality | `go vet ./...` zero warnings | done | All iterations |
| R135 | Quality | `gofmt -s -l .` no drift | done | All iterations |
| R136 | Quality | `go test ./... -race -count=1` all green | done | All iterations |
| R137 | Quality | Property-based tests for storage (crash/recovery) | done | All iterations |
| R138 | Quality | `Benchmark*` for every storage component | partial | `ENG/LS` has none (R44) |
| R139 | Quality | No allocations in hot paths | done | Pre-allocated buffers, `sync.Pool` |
| R140 | Quality | All public API methods goroutine-safe | done | iter-09 |
| R141 | Quality | `log/slog` only — no `fmt.Printf` in library | done | All iterations |
| R142 | Quality | Error messages: lowercase, no trailing punctuation | done | All iterations |
| R143 | Quality | Test coverage per subsystem ≥ 70% | partial | `SQL/RE` at 49% (R83) |
