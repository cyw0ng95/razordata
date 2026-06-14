# Iteration 27 — Maturity Push (v0.27.0)

**Status:** done (Phase 1-5); Phase 6 in progress
**Target:** v0.27.0 + .1 patch releases
**Budget:** ~36,500 LoC (Phases 1-5) + ~5,000 LoC (Phase 6) = ~41,500 LoC across 30 REQs

## Outcome

Phases 1-5: all 25 REQs shipped. 21 commits pushed. All 35 packages pass
`go test ./... -race -count=1`. SLT corpus: 42/42 pass (was 39/42).

Phase 6: 5 additional critical/high-priority REQs added after the v0.27.0
release. Tracked as v0.27.x patch series on the same iteration. (REQ000307
MV-OCC was originally planned for Phase 6 but deferred to a later
iteration — the protocol change risk is too high to ship in a patch.)

## Commits

| Commit | REQs | Description |
| 07b3a0a | REQ000443b | Fix negative_literal eval pipeline bug |
| a8e40b1 | REQ000435, REQ000436 | CREATE TRIGGER parser + WITH RECURSIVE flag |
| 1ff048e | REQ000248/249 | Generated columns (parse + materialize on INSERT) |
| 258d947 | REQ000318 | Write rate-limited compactor (token bucket) |
| b66ba79 | REQ000319 | Sub-compaction parallelism (key-range sub-jobs) |
| d3377f8 | REQ000320 | Configurable compaction style (leveled/tiered/hybrid) |
| e623c34 | REQ000299 | WAL columnar batch encoding |
| e312dbf | REQ000297 | SST block-level dictionary compression |
| 073cb23 | REQ000303 | W-TinyLFU admission policy |
| d4268c3 | REQ000304 | Off-heap large object pool |
| 90f49bb | REQ000306 | Stack-allocate version nodes (escape analysis hints) |
| 1cc6374 | REQ000308 | QSBR read path |
| 6dd04a2 | REQ000436, REQ000084 | Fix FROM-subquery parser + recursive CTE execution |
| a5ca4d3 | REQ000181 | Real goroutine ID tracking (runtime.Stack) |
| d641094 | REQ000164 | Epoch manager background goroutine + WaitForDrain |
| 95b7418 | REQ000175 | ReclaimPool — actual memory reclamation |
| 77fdae4 | REQ000317 | Parallel WAL replay (worker pool) |
| 4bc858d | REQ000183, REQ000310 | 8-wide EvalBatch SIMD + CPU feature detection |
| 20e4d2b | REQ000312 | Radix-partitioned hash join |
| 66062fe | REQ000314 | Columnar SST/PAX block layout |

## Goal

Major maturity release combining SLT compliance (42/42 pass target), storage engine upgrades, memory management improvements, and transaction optimizations. This is the largest single iteration in the project.

## Rules

- **Commit in-time:** one commit per REQ after tests pass; do not batch.
- **Test speed:** parallelize test packages, reduce slow paths (TXN/VL ~46s target <15s).
- **CI gate:** `go test ./... -race -count=1` must pass after every commit.
- **Lint gate:** `go vet ./...` zero warnings, `gofmt -s -l .` no drift.

## Phase 1 — SLT Compliance & SQL Foundations (~4,700 LoC)

Target: 42/42 SLT corpus pass. Fix the 3 remaining failures + add small parser features.

| # | REQ | Subsystem | Description | Effort | LoC | Touches |
|---|---|---|---|---|---|---|
| 1 | new | SQL/EX | Fix negative_literal eval pipeline bug (unary minus on column fails in full pipeline) | S | ~100 | `SQL/EX/eval.go`, `SQL/EX/planner.go`, `SQL/EX/select.go` |
| 2 | REQ000356 | SQL/LX+EX | Unary `NOT` as logical prefix operator (currently only infix; `~` is bitwise NOT) | S | ~100 | `SQL/LX/token.go`, `SQL/EX/eval.go` |
| 3 | REQ000358 | SQL/PS | XOR `^` parser gap — lexer emits `T_BITXOR` but parser doesn't recognize in precedence table | S | ~50 | `SQL/PS/ps.go` |
| 4 | REQ000435 | SQL/PS | `CREATE TRIGGER` parser (BEFORE/AFTER, FOR EACH ROW, BEGIN...END body) | M | ~800 | `SQL/PS/ps.go` — new `Trigger` AST, `parseTrigger` |
| 5 | REQ000443b | SQL/EX+TEST | negative_literal regression test + dual-runner probe | S | ~100 | `SQL/EX/eval_test.go`, `tests/sqlcmp/dual/probe_cases.go` |

**Phase 1 gate:** `TestSLT_GapSurvey` → 42/42 pass. All new code has unit tests.

## Phase 2 — Advanced SQL Features (~6,500 LoC)

Recursive CTE, generated columns, subquery planning, foreign keys.

| # | REQ | Subsystem | Description | Effort | LoC | Touches |
|---|---|---|---|---|---|---|
| 6 | REQ000436 | SQL/PS+PL+EX | Recursive CTE (`WITH RECURSIVE ... AS (anchor UNION ALL recursive)`) — parser flag, iterative executor, cycle detection | L | ~2,500 | `SQL/PS/ps.go` — `parseWith` RECURSIVE flag; `SQL/EX/cte.go` (new); `SQL/PL/planner.go` |
| 7 | REQ000248 | SQL/PS | Parse generated columns (`AS (expr) STORED/VIRTUAL`) in `CREATE TABLE` | M | ~400 | `SQL/PS/ps.go` — `ColDef.Generated` field |
| 8 | REQ000249 | SQL/EX | Generated column materialization on INSERT/UPDATE | M | ~600 | `SQL/EX/writers.go` — compute and store generated values |
| 9 | REQ000084 | SQL/PL+RE | Subquery planning (not just flatten) — decorrelate, lateral join, apply operator | M | ~1,000 | `SQL/RE/subq.go`, `SQL/PL/planner.go`, `SQL/EX/apply.go` (new) |
| 10 | REQ000126 | SQL/PS+EX | Foreign keys (`REFERENCES t(col)`, `ON DELETE CASCADE/SET NULL/RESTRICT`, `ON UPDATE CASCADE/SET NULL/RESTRICT`) | L | ~3,000 | `SQL/PS/ps.go` — FK AST; `SQL/EX/fk.go` (new); `SQL/EX/constraints.go`; `ENG/LS/catalog.go` |

**Phase 2 gate:** `go test ./SQL/... -race -count=1` green. Recursive CTE, generated columns, FK have table-driven tests. Subquery planning has regression tests.

## Phase 3 — Storage Engine Improvements (~5,000 LoC)

SST dictionary compression, WAL columnar encoding, compaction scheduling.

| # | REQ | Subsystem | Description | Effort | LoC | Touches |
|---|---|---|---|---|---|---|
| 11 | REQ000297 | ENG/LS | SST block-level dictionary compression (ZSTD with per-block trained dict, 4KB blocks, 1.5x→3x ratio) | M | ~1,500 | `ENG/LS/sst_writer.go` — `dictTrain`; `ENG/LS/sst_reader.go` — dict lookup |
| 12 | REQ000299 | WAL/WR | WAL columnar encoding (key delta-of-delta + bit-packing, `commitTS` varint; payload volume -60%+) | M | ~1,500 | `WAL/WR/encode.go` — columnar batch encode/decode |
| 13 | REQ000318 | ENG/LS | Write-rate-limited compactor (token bucket rate limiter, `Options.CompactionRateLimit`) | M | ~1,000 | `ENG/LS/compaction.go` — `RateLimiter` struct |
| 14 | REQ000319 | ENG/LS | Sub-compaction parallelism (split L4+ compaction into key-range sub-jobs, worker pool fan-out) | M | ~1,000 | `ENG/LS/subcompact.go` (new) |

**Phase 3 gate:** `go test ./ENG/... ./WAL/... -race -count=1` green. Benchmarks for SST read/write and WAL encode/decode show improvement.

## Phase 4 — Memory Management (~3,500 LoC)

W-TinyLFU cache admission, off-heap large object pool.

| # | REQ | Subsystem | Description | Effort | LoC | Touches |
|---|---|---|---|---|---|---|
| 15 | REQ000303 | MEM/BF | W-TinyLFU admission + SLRU (replaces clock-sweep; +30% hit rate vs LRU; configurable) | M | ~1,500 | `MEM/BF/wtinylfu.go` (new) — frequency sketch + admission; `Options.CachePolicy` |
| 16 | REQ000304 | MEM/OF | Off-heap large object pool (mimalloc-style size-class bins; bypasses GC for >64KB SST buffers) | L | ~2,000 | `MEM/OF/of.go` (new) — `mmap` + atomic free lists |

**Phase 4 gate:** `go test ./MEM/... -race -count=1` green. Benchmark: hit rate improvement for W-TinyLFU vs clock-sweep. Off-heap pool survives GC pressure test.

## Phase 5 — Transaction & Compaction Strategy (~4,500 LoC)

Version node stack allocation, QSBR read path, configurable compaction.

| # | REQ | Subsystem | Description | Effort | LoC | Touches |
|---|---|---|---|---|---|---|
| 17 | REQ000306 | TXN/MV | Stack-allocate version nodes via escape analysis hints (`//go:nosplit` + `noescape()`) | M | ~1,000 | `TXN/MV/node.go` — escape hint annotations |
| 18 | REQ000308 | TXN/LC | QSBR read path (Quiescent-State-Based Reclamation; readers set flag only, zero atomic load) | L | ~2,500 | `TXN/LC/qsbr.go` (new) — replaces `TXN/LC/epoch.go` |
| 19 | REQ000320 | ENG/LS | Configurable compaction style (`Options.CompactionStyle = leveled | tiered | hybrid`) | M | ~1,000 | `ENG/LS/compaction.go` — strategy interface; `Options.CompactionStyle` |

**Phase 5 gate:** `go test ./TXN/... ./ENG/... -race -count=1` green. QSBR replaces epoch reclamation without regression. Benchmark: version node allocation shows zero-alloc claim.

## Phase 6 — Linux I/O and Concurrency Maturity (~6,000 LoC)

Seven REQs added after the v0.27.0 release. Linux I/O
optimizations (io_uring, O_DIRECT, async fsync), NUMA placement,
IndexScan real seek, plus two SQL features: cost-based scan
selection and full SQL aggregate DISTINCT coverage.
(REQ000307 MV-OCC remains deferred to a later iteration — the
protocol-change risk is too high for a patch release.)

| # | REQ | Subsystem | Description | Effort | LoC | Touches |
|---|---|---|---|---|---|---|
| 20 | REQ000074 | SQL/EX | IndexScan real seek — replace prefix-scan fallback with `ENG/ID` B-tree seek (single-key + range) | M | ~800 | `SQL/EX/operators.go` — call into `ENG/ID/id.go` Seek/Range; `SQL/PL/planner.go` — index selection |
| 21 | REQ000301 | WAL/WR | Async fsync + io_uring linked submit (write→fsync chained via `IOSQE_IO_LINK`; lowers batch-commit latency) | S | ~400 | `WAL/WR/fl.go` — `BatchSyncWithUring`; stub when uring unavailable |
| 22 | REQ000309 | ENG/LS + MEM/BF | NUMA-aware placement: first-touch arena allocation in `TXN/MV`; buffer pool slot node id; worker CPU pin via `runtime.GOMAXPROCS` + affinity hint | M | ~1,200 | `TXN/MV/arena.go`, `MEM/BF/bf.go` — slot node id; `ENG/LS/compaction.go` — worker pool pin |
| 23 | REQ000295 | FIL | io_uring async I/O wrapper (SQ/CQ submission, SQPOLL mode, Linux-only; IOCP/kqueue fallback via existing `FIL/FS`) | L | ~2,000 | new `FIL/IO/uring.go`; `FIL/FS/fs.go` — cross-platform dispatch (`buildlinux` / non-linux) |
| 24 | REQ000296 | FIL | Direct I/O + io_uring fixed-file descriptor (bypass OS page cache, reduce fd table lookups) | M | ~600 | `FIL/FS/fs.go` — `IOSQE_FIXED_FILE` flags; integration with existing O_DIRECT |
| 25 | REQ000156 | SQL/EX+PL | Cost-based scan selection: `pickCheaperScan` compares `estimateCost` for SeqScan vs IndexScan candidates and swaps to the cheaper one when the WHERE column has a writer-registered index; extends the cost model with `indexMode` to differentiate real-seek (0.05) from prefix-scan (0.1) | M | ~400 | `SQL/EX/planner.go` — `pickCheaperScan` + `indexedColumnOrRange` helper; `SQL/EX/estimateCost` IndexScan mode |
| 26 | REQ000437 | SQL/PS+EX | Full SQL aggregate DISTINCT support: SUM/AVG/MIN/MAX/GROUP_CONCAT(DISTINCT col) dedup before aggregating; NULLs excluded from the distinct set per SQLite semantics; parser routes DISTINCT through the IDENT aggregate path for `GROUP_CONCAT` | M | ~500 | `SQL/PS/ps.go` — DISTINCT routing for IDENT aggregate names; `SQL/EX/aggregate.go` — `sumDistinct/avgDistinct/minDistinct/maxDistinct` helpers + GROUP_CONCAT DISTINCT |

**Phase 6 gate:** `go test ./... -race -count=1` green. New `Benchmark*` for
index seek latency, async fsync latency, and uring paths. NUMA path
verified by CPU-pin smoke test. `go vet ./...` and `gofmt -s -l .` clean.

**Dependency graph within Phase 6:**

```
REQ000074 (SQL IndexScan)         ── independent
REQ000301 (WAL async fsync)        ───┐
REQ000309 (NUMA)                    ───┤── Phase 6a (parallel)
REQ000295 (FIL io_uring)            ───┘
        │
        └── REQ000296 (Direct I/O + uring) — Phase 6b
```

**Cross-platform note (REQ000295/296/301):** the io_uring path is gated
on `linux/amd64` and `linux/arm64` via build tags. The default `darwin`
and `windows` builds use the existing pread/pwrite implementation.
`go test ./...` runs on the developer's host; a CI matrix runs the
Linux build with `-tags linux_uring` to exercise the new path.

## Dependency Graph

```
Phase 1 (no deps)
    ↓
Phase 2 (Phase 1 parser fixes prerequisite)
    ↓
Phase 3 (independent of Phase 2; can parallelize)
Phase 4 (independent; can parallelize)
Phase 5 (REQ000319 depends on REQ000318 from Phase 3)
    ↓
Phase 6 (independent; can parallelize)
    - REQ000295 → REQ000296 (within Phase 6)
    - REQ000295 → REQ000301 (within Phase 6)
```

Phases 3, 4, and 5 can run in parallel after Phase 2 completes. Phase 6
runs after Phase 5 ships v0.27.0; its 5 REQs land as v0.27.1 / v0.27.2
patches (or as a v0.28.0 minor if a Phase 6 REQ requires a catalog
migration). REQ000307 (MV-OCC) is intentionally NOT in Phase 6 — the
Silo-style rewrite of the commit protocol is risky enough to warrant
its own dedicated iteration.

## Test Speed Optimization

Current slow packages (target: all <15s):

| Package | Current | Target | Strategy |
|---|---|---|---|
| `TXN/VL` | ~46s | <15s | Parallel subtests, reduce epoch settle wait, batch slot allocation |
| `SQL/EX` | ~30s | <15s | Split monolithic test file, parallel table-driven tests |
| `ENG/LS` | ~20s | <15s | Reduce flush/compaction settle timeouts, shared test DB |

Actions:
- Add `t.Parallel()` to all table-driven subtests where safe.
- Reduce `time.Sleep` waits in integration tests (replace with poll/retry).
- Share `Engine` instances across related test groups (setup once, run N tests).
- Use `-short` flag to skip slow stress tests in regular CI.

## Release

- v0.27.0: tag cut when all Phase 1-5 REQs are done and `go test ./... -race -count=1` passes.
- v0.27.1 (or v0.28.0): tag cut when all Phase 6 REQs (5 REQs) are done; same CI gate.
  Decision criterion: if any Phase 6 REQ adds a new public API or `Options`
  field that changes the catalog on-disk format, bump to v0.28.0. Otherwise
  v0.27.1. The current Phase 6 REQs (074, 295, 296, 301, 309) only add new
  optional APIs (the io_uring shim is opt-in via build tag, the NUMA node
  id is an internal field); v0.27.1 is the right tag.

## Gap Analysis

- **ZSTD dependency:** REQ000297 requires ZSTD. Options: (a) use `github.com/klauspost/compress/zstd` (pure-Go, CGO-free), (b) fall back to flate if ZSTD unavailable. Choose (a) per project rules (no external C deps).
- **QSBR vs epoch:** REQ000308 replaces epoch-based reclamation. Must maintain backward compatibility: existing code using `epoch.go` API must compile during transition.
- **FK + catalog:** REQ000126 needs catalog V3 migration for FK metadata. Must not break existing catalog V1/V2 databases.
- **Recursive CTE cycle detection:** Must detect cycles to prevent infinite loops. Use visited-set with configurable max-depth (default 1000).
- **Generated columns VIRTUAL vs STORED:** Start with STORED only (materialized on write). VIRTUAL (computed on read) deferred to v0.28.

## Phase 6 Gap Analysis

- **io_uring kernel version floor:** REQ000295 needs Linux 5.6+ for `IOSQE_IO_LINK` (used by REQ000301) and 5.11+ for `IOSQE_FIXED_FILE` (used by REQ000296). On older kernels fall back to plain pread/pwrite; the new path is opt-in via `-tags linux_uring`. CI must run on a 5.11+ runner.
- **io_uring shim scope:** the Phase 6 implementation is a minimal viable shim. The full SQ/CQ memory management and SQE encoding for PREAD/PWRITE/FSYNC is a follow-up; the Phase 6 commits provide the interface and a constructor that returns `ErrShimUnsupported` from `SubmitWait`. The FIL subsystem continues to use pread/pwrite until the full implementation lands.
- **NUMA on non-NUMA hosts:** REQ000309's first-touch policy is a no-op on single-socket hosts. Detect via `/sys/devices/system/node/has_normal_memory` at engine open; if absent, skip node-id tracking.
- **IndexScan real seek (REQ000074) and secondary indexes (REQ000045):** real seek is over the primary key index only. Cross-column secondary indexes remain TBD. The hook should be `IndexScan -> ENG/ID.Seek(pk)` not `ENG/ID.Seek(col)`.

## Expected Outcome

- SLT corpus pass rate: 42/42 (100% of surveyed patterns)
- Storage: SST compression 1.5x-3x, WAL payload -60%
- Memory: +30% cache hit rate (W-TinyLFU), GC pressure reduced (off-heap pool)
- Transaction: zero-alloc version nodes, near-RCU read latency (QSBR)
- Compaction: rate-limited, parallel sub-compaction, configurable style

### Phase 6 add-on

- I/O: io_uring shim on Linux (interface in place; full SQ/CQ
  ring management is a follow-up); O_DIRECT + fixed-fd
  registration API exposed; async fsync decouples fsync
  latency from commit path
- SQL: real seek for `IndexScan` (no more prefix-scan fallback
  on PK lookups; range predicates use B-tree seek)
- NUMA: per-slot node id in buffer pool; worker OS-thread
  pinning in subcompaction
- Cross-platform: macOS and Windows builds use existing
  pread/pwrite; no behavior change

### Deferred from Phase 6

- **REQ000307 (MV-OCC timestamp ordering):** the Silo-style
  rewrite of the commit validation protocol was deemed too
  risky for a patch release. Will ship as part of a dedicated
  iter-28 (or later) that focuses on transaction layer
  improvements.


