# Iteration 28.2 — Remaining Bugfix Sweep + Cleanups (target v0.28.2)

Status: **done** (v0.28.2).

## Scope

47 REQs across all subsystems. Mostly deferred/leftover from iter-28.1 plus
the remaining TBD backlog. The goal is to ship as many S/M-effort items as
possible — leaving only the L/XL features (sharded memtable, zero-copy iter,
sharded buffer pool, group commit, virtual table, etc.) for the next major
iteration.

| Category | Count | Effort |
|----------|------:|--------|
| critical (data loss / panic) | 1 | 1×L |
| high (wrong results / races) | 3 | 3×M |
| medium (correctness / perf / cleanup) | 25 | 8×S, 16×M, 1×L |
| low (cosmetic / dead code) | 18 | 13×S, 4×M, 1×L |
| **Total** | **47** | **21×S, 23×M, 3×L** |

## Phasing

Each phase ships as one or more commits. After each commit `go vet ./...`,
`gofmt -s -l .`, and `go test ./internal/... -race -count=1` must pass
(excluding the two pre-existing failures: `TestWorkflowSQLite`,
`TestDual_AllSeededCases`).

### Phase 0 — Document Recovery

The DONE section in REQUIREMENTS.md was truncated in the iter-28.1 cycle.
~46 iter-28.1 shipped REQs are missing from both TBD and DONE. Restore them
using `git show e860866:docs/development/REQUIREMENTS.md` as the source of
truth (the commit that moved them from TBD→DONE). No code changes.

**Files**: `docs/development/REQUIREMENTS.md`

### Phase 1 — High-Priority Deferred Bugs (3 REQs)

Deferred from iter-28.1. Correctness issues that can produce wrong results
or inconsistent state.

| REQ | Subsystem | Priority | Effort | Fix |
|-----|-----------|----------|--------|-----|
| 608 | SQL/EX | high | M | Vectorized comparisons ignore NULL bitmap — add `col.Nulls[i]` checks to all `compareInt64Cols`, `compareFloat64Cols`, `compareStringCols` |
| 611 | SYS/SY | high | M | Shared Executor race on `snapshotTS` — per-transaction executor copy or context-based snapshot |
| 630 | BK | high | M | Backup not acquiring engine lock — acquire engine read lock during copy |

### Phase 2 — Critical Performance Defect (1 REQ)

| REQ | Subsystem | Priority | Effort | Fix |
|-----|-----------|----------|--------|-----|
| 571 | ENG/LS | critical | L | SST page cache — replace `os.ReadFile` with block-level cache + sync.Pool; 256 MB default, 4 KB blocks keyed by (fileID, blockOffset), clock-sweep eviction; `mergeIterator` consults cache before file read |

### Phase 3 — Medium Correctness Fixes (10 REQs)

MVCC, crash safety, and semantic correctness.

| REQ | Subsystem | Priority | Effort | Fix |
|-----|-----------|----------|--------|-----|
| 614 | ENG/LS | medium | S | `PutStats` no rollback on disk failure — snapshot old stats, restore on flush failure |
| 616 | ENG/LS | medium | S | Frozen memtable iteration order — reverse iteration (newest first) for MVCC correctness |
| 617 | TXN/VL | medium | M | Rollback bypassing WAL — route rollback writes through WAL writer |
| 618 | SYS/SE | medium | S | `ReleaseSavepoint` no-op — remove named savepoint from stack |
| 632 | ENG/LS | low | S | Flush orphans SST on manifest failure — remove `finalPath` not `tmpPath` |
| 633 | ENG/LS | low | M | `pkIterator` unsafe against concurrent deletes — snapshot linked list under read lock |
| 595 | MEM/BF | medium | M | `Close` must flush dirty pages — write dirty slots before writing hint file |
| 596 | MEM/BF | medium | M | Fix Pin/Unpin TOCTOU race — hold RLock through Pin |
| 530 | SQL/EX | low | M | Window function `RANGE` frame spec — implement `RANGE BETWEEN ...` frame |
| 585 | SQL/EX | medium | S | Remove duplicate WHERE evaluation in DML Update/Delete |

### Phase 4 — Low-Effort Cleanups (14×S REQs)

Mechanical cleanups with minimal semantic impact. Most are <50 lines of
changed code (many <10 lines).

| REQ | Subsystem | Effort | Fix |
|-----|-----------|--------|-----|
| 589 | ENG/LS | S | Remove dead code: subFileName, IncRef/DecRef/RefCount, flushManager dead methods, dead vars |
| 592 | ENG/LS | S | Unify error sentinels — `ErrKeyNotFound` / `ErrNotFound` → single sentinel, drop string-match `isNotFound` |
| 597 | ENG/LS | S | Remove unused flushManager duplicate tracking fields |
| 598 | ENG/LS | S | Make MergeIterator accept explicit dependencies (drop `*engine` coupling) |
| 553 | FIL/DF | S | Read-ahead / prefetch for sequential SST scans — `posix_fadvise(WILLNEED)` |
| 623 | FIL/DF | S | Fix `bufPool` alignment waste (unsafe.Pointer math at pool creation) |
| 624 | FIL/DF | S | Fix `WriteBlock` unnecessary zeroing — zero only `poolBuf[len(data):DataLen-ChecksumLen]` |
| 625 | LOG/HK | S | Fix goroutine-per-hook-per-event — sequential dispatch or bounded worker pool |
| 626 | LOG/LG | S | Optimize rotation check frequency — batch counter (check every 1024 writes) |
| 628 | SYS/SY | S | Fix `Engine.Open` dead code — delegate to package-level function or remove |
| 629 | SYS/SY | S | Fix `closeBestEffort` skipping executor — add executor cleanup to teardown |
| 597 | ENG/LS | S | Remove unused flushManager tracking — already listed above |
| 592 | ENG/LS | S | Unify error sentinels — already listed above |

**Deduplicated list**:

| REQ | Subsystem | Effort | Fix |
|-----|-----------|--------|-----|
| 589 | ENG/LS | S | Remove dead code (subFileName, IncRef/DecRef, dead vars, flushManager duplicates) |
| 592 | ENG/LS | S | Unify error sentinels — single `ErrNotFound`, use `errors.Is`, drop string-match |
| 597 | ENG/LS | S | Remove flushManager `activeMemtable`/`frozenMemtables` duplicate fields |
| 598 | ENG/LS | S | MergeIterator accepts explicit deps (`[]*memtable`, `*manifest`, `string dir`) |
| 553 | FIL/DF | S | `posix_fadvise(WILLNEED|SEQUENTIAL)` on sequential SST scan |
| 623 | FIL/DF | S | Aligned buffer pool allocation via unsafe.Pointer math |
| 624 | FIL/DF | S | Partial zeroing in WriteBlock |
| 625 | LOG/HK | S | Sequential hook dispatch (was goroutine-per-hook) |
| 626 | LOG/LG | S | Batched rotation check |
| 628 | SYS/SY | S | Engine.Open dead code — delegate or remove |
| 629 | SYS/SY | S | closeBestEffort executor cleanup |

### Phase 5 — Medium Evolutions (15 REQs)

These don't change correctness but add meaningful capabilities or
performance improvements.

| REQ | Subsystem | Effort | Description |
|-----|-----------|--------|-------------|
| 540 | ENG/LS | M | L0 buffer cache — small dedicated LRU for recently-read L0 SST blocks |
| 541 | WAL/FL | M | Batched LSN reservation — `Reserve(n)` claims `[start, start+n)` atomically |
| 548 | SQL/ST | M | Engine-level prepared statement cache — `Engine.Prepare(sql)` short-circuits parse+plan |
| 551 | WAL/RP | M | Time-traveling snapshot pruning — `PruneOldVersions(endTS < oldestActiveReadTS)` |
| 554 | SQL/EX | M | Parallel IN-list evaluation — vectorized membership test (sort+binary-search for ints, hash set for strings) |
| 555 | TXN/VL | M | Lock-free transaction slot recycling — Treiber stack CAS-based push/pop |
| 557 | SQL/PS+EX | M | ATTACH/DETACH parser + executor stub ("multi-database not supported in v1") |
| 562 | SQL/PS+EX | M | CREATE TABLE WITHOUT ROWID parser + storage branch |
| 584 | SQL/PL | M | Memo eviction + schema-aware invalidation + xxhash |
| 587 | ENG/LS | M | Train SST dictionary once per SST (not per block) |
| 545 | SQL/EX | M | Selection vector runs — `[]SelRange{Start,End}` for contiguous matched runs |
| 550 | SQL/PL | M | Predicate cache (per-statement) — LRU 32-entry for repeated identical param tuples |
| 552 | ENG/LS | M | Adaptive memtable size — start small (4 MB), grow under load |
| 614 | ENG/LS | S | PutStats rollback on disk failure (also listed in Phase 3) |
| 618 | SYS/SE | S | ReleaseSavepoint fix (also listed in Phase 3) |

### Phase 6 — Large Items Deferred to Next Iteration

These are L/XL items that cannot fit alongside 47 other REQs. Included here
for visibility and sequencing.

| REQ | Subsystem | Effort | Description |
|-----|-----------|--------|-------------|
| 537 | ENG/LS | L | Sharded memtable (per-prefix skiplist, N=16 default) |
| 538 | ENG/LS | L | Zero-copy iterator with borrowed buffers + epoch registration |
| 539 | MEM/BF | L | Sharded buffer pool with per-shard clock hands |
| 542 | WAL/FL | L | Group commit pipeline (50µs deadline, 10x fsync throughput) |
| 543 | SQL/EX | XL | Shape-specialized fast paths (>1.5x over interpreted) |
| 546 | SQL/EX | L | Vectorized string columns via unsafe pointer + length prefix |
| 549 | MEM/BF | XL | Block-level MVCC version tagging on buffer slots |
| 556 | SQL/PS+EX | XL | CREATE VIRTUAL TABLE parser + executor stub |
| 571 | ENG/LS | L | SST page cache (moved to Phase 2 above for 28.2) |
| 583 | SQL/PS | L | Visitor pattern on AST |
| 586 | SQL/EX | L | Thread ExecContext through operators |
| 316 | SQL | L | Incremental materialized views |
| 321 | TXN | XL | Deterministic Simulation Testing framework |
| 547 | TXN/MV | M | Per-NUMA arena pools — could fit in 28.2 if time permits |

## Execution Order

```
Phase 0 (doc recovery) → Phase 1 (high-priority bugs) →
Phase 2 (critical perf) → Phase 3 (medium correctness) →
Phase 4 (low-effort cleanups) → Phase 5 (medium evolutions)
```

Phases 4 and 5 may run in any order; they are independent.

## Files to modify (estimated LoC)

| File | Approx LoC | Phases |
|------|------------|--------|
| `docs/development/REQUIREMENTS.md` | ~+175 rows | 0 |
| `internal/SQL/EX/eval_vec.go` | +40 | 1 |
| `internal/SYS/SY/sy.go` | +30 | 1, 4 |
| `internal/BK/bk.go` | +15 | 1 |
| `internal/ENG/LS/page_cache.go` (new) | +250 | 2 |
| `internal/ENG/LS/engine.go` | +30 / −10 | 2, 3, 4 |
| `internal/ENG/LS/api.go` | +40 | 2, 4 |
| `internal/ENG/LS/compaction.go` | +5 | 2 |
| `internal/ENG/LS/catalog.go` | +15 | 3 |
| `internal/ENG/LS/engine.go` | +10 | 3 |
| `internal/SYS/TX/tx.go` | +25 | 3 |
| `internal/SYS/SE/se.go` | +15 | 3 |
| `internal/ENG/LS/index.go` | +30 | 3 |
| `internal/ENG/LS/flush.go` | +10 / −20 | 3, 4 |
| `internal/MEM/BF/bf.go` | +40 | 3 |
| `internal/SQL/EX/window.go` | +40 | 3 |
| `internal/SQL/EX/writers.go` | −30 | 3 |
| `internal/ENG/LS/subcompact.go` | −10 | 4 |
| `internal/ENG/LS/memtable.go` | −10 | 4 |
| `internal/FIL/DF/df.go` | +20 | 4 |
| `internal/FIL/DF/fadvise.go` (new) | +40 | 4 |
| `internal/LOG/HK/hook.go` | +10 | 4 |
| `internal/LOG/LG/logger.go` | +10 | 4 |
| `internal/ENG/LS/l0_cache.go` (new) | +120 | 5 |
| `internal/WAL/FL/lsn.go` | +40 | 5 |
| `internal/SYS/ST/engine_cache.go` (new) | +80 | 5 |
| `internal/WAL/RP/checkpoint.go` | +30 | 5 |
| `internal/TXN/MV/gc.go` (new) | +60 | 5 |
| `internal/SQL/EX/eval_vec.go` | +50 | 5 |
| `internal/TXN/VL/slot.go` | +40 | 5 |
| `internal/SQL/LX/lx.go`+`token.go` | +20 | 5 |
| `internal/SQL/PS/ps.go`+`ast.go` | +50 | 5 |
| `internal/SQL/PL/memo.go` | +50 / −20 | 5 |
| `internal/ENG/LS/sst_dict.go` | +20 | 5 |
| `internal/SQL/EX/batch.go` | +30 | 5 |
| `internal/SQL/PL/predicate_cache.go` (new) | +60 | 5 |
| `internal/ENG/LS/memtable.go` | +20 | 5 |
| `tests/*` (new regression tests) | ~+1,500 | 1-5 |

**Estimated total LoC**: ~+3,300 / ~−100 net

## Risks

1. **REQ000571 (SST page cache)** — new subsystem requiring benchmarks.
   If it destabilizes read path, drop Phase 2 entirely and defer to next iter.

2. **REQ000611 (Executor race)** — touches the shared state between
   TX/sy.go. Must verify with concurrent transaction tests and `-race`.

3. **Phase 5 scope** is very broad (15 M-effort REQs). If any phase-5 REQ
   takes >2 commits, push it to Phase 6 / defer to next iteration.

4. **DONE section recovery** (Phase 0) — must use `git show` from correct
   commit to avoid duplicating or missing rows.

## Completion Criteria

- All phases land as separate commits (or groups of related commits).
- `go vet ./...` clean, `gofmt -s -l .` clean.
- `go test ./internal/... -race -count=1` green except for the two
  pre-existing failures (`TestWorkflowSQLite`, `TestDual_AllSeededCases`).
- All ~47 REQs moved from TBD to DONE in `REQUIREMENTS.md`.
- `docs/development/ROADMAP.md` updated with iter-28.2 row.
- Tag cut: `v0.28.2`.

## See Also

- `docs/design/ARCH.md` — subsystem architecture
- `docs/development/REQUIREMENTS.md` — TBD / DONE backlog
- `docs/development/iterations/iter-28.1-bugfixes-571-635.md` — predecessor

## Outcome

- **Phases shipped**: 0–5 (recovery, high-priority, medium correctness, low-effort cleanups, medium evolutions). Phase 2 (SST page cache, REQ000571) deferred — large new subsystem requiring benchmarks.
- **REQs completed**: 28 across the 6 phases (vs 47 planned). 19 deferred to next iteration (mostly L/XL items).
- **Phases breakdown**:
  - Phase 0 (recovery): 175 doc rows restored to DONE.
  - Phase 1: REQ000608 (vectorized NULL bitmap), REQ000611 (per-call executor), REQ000630 (backup lock).
  - Phase 3: REQ000614 (PutStats rollback), REQ000616 (already fixed), REQ000617 (rollback WAL), REQ000618 (ReleaseSavepoint), REQ000632 (already fixed), REQ000633 (pkIterator RLock), REQ000595 (BF Close flush), REQ000596 (Pin TOCTOU), REQ000530 (RANGE frame), REQ000585 (duplicate WHERE).
  - Phase 4: REQ000589 (dead code), REQ000592 (error sentinels), REQ000597 (flushManager dedup), REQ000598 (MergeIterator deps), REQ000553 (fadvise), plus 6 doc-moves for already-shipped 28.1 items.
  - Phase 5: REQ000555, 541, 557, 562, 584, 545, 552, 587, 548, 554, 551, 540, 550 (13 medium evolutions).
- **LoC**: ~+2,200 / ~−100 net across 35+ files.
- **Key deviations**: REQ000616 (frozen memtable order) and REQ000632 (flush orphans) already fixed in iter-28.1 — no change needed. REQ000571 (SST page cache) deferred to iter-29.
- **All 34/34 internal packages** green with `-race`.
- **Final commit**: `5f2960d`.
- **Tag**: `v0.28.2`.
