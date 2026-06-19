# Iteration 28.1 — Bugfix Sweep REQ000571–REQ000635 (target v0.28.1)

Status: **planned** — single release targeting v0.28.1. Builds on iter-28 (v0.28.0).

## Scope

65 REQs across ENG/LS, WAL, MEM, TXN, SQL/PS, SQL/EX, SQL/PL, SYS, FIL, LOG
subsystems. The vast majority are concrete bugs surfaced by code audit, SLT
corpus runs, and reasoning about concurrency semantics — not new features.
One is a code reorganization (ps.go split) and one is a performance
enhancement (SST page cache).

| Category | Count | Effort |
|----------|------:|--------|
| critical (data loss / panic) | 6 | 4×S, 2×M |
| high (wrong results / races) | 17 | 11×S, 6×M |
| medium (correctness / perf / cleanup) | 21 | 14×S, 7×M, 1×L |
| low (cosmetic / dead code) | 16 | 15×S, 1×M |
| code reorganization | 1 | 1×M |
| performance enhancement | 1 | 1×L |
| **Total** | **62** | **47×S, 18×M, 2×L** |

(The 65 vs 62 diff: three REQs (583/584/586) overlap with the medium/L
categories — counted once.)

## Phasing

Each phase ships as one or more commits. After each commit `go vet ./...`,
`gofmt -s -l .`, and `go test ./... -race -count=1` must pass (excluding the
two pre-existing failures: `TestWorkflowSQLite`, `TestDual_AllSeededCases`).

### Phase 1 — Critical Data-Loss / Panic Bugs (must-fix, 6 REQs)

Block the release on this phase.

- **REQ000572** — WAL replay double-counting segment offset (`rp.go:398-404`).
  Remove `offset += int64(consumed)` and fix LSN formula. New test with >64KB
  segment.
- **REQ000573** — Commit ordering: WAL before version chain
  (`TXN/VL/protocol.go:200-234`). Reorder to WAL RTCommit → sync → version
  chain commit. Failure-injection test.
- **REQ000574** — Missing RWMutex on `engine.memtables`/`activeMem`. Add
  `sync.RWMutex`; concurrent read/flush race test.
- **REQ000599** — Prefix bloom modulus mismatch (writer byte count, reader
  bit count). 7/8 of prefix queries return false negatives. Fix reader to
  match writer.
- **REQ000600** — Skiplist higher-level CAS linking. Levels 1+ never
  CAS-linked, degenerating from O(log n) to O(n). Walk every level.
- **REQ000601** — Compaction not removing overlap files. Unbounded disk
  growth on repeated compactions. Remove overlap files from `newLevels[level+1]`
  and filter overlap by key range.

### Phase 2 — High-Priority Correctness Bugs (17 REQs)

Mostly SQL semantics, executor wiring, and races.

| REQ | Subsystem | Fix |
|-----|-----------|-----|
| 575 | TXN/MV | Uncommitted writes visible to concurrent readers — `IsVisible` must filter uncommitted nodes |
| 576 | SQL/EX | Codegen batch functions are no-ops — drop registrations until real impls exist |
| 577 | SQL/EX | `VectorizedFilter.WithParams` returns nil — return `f` |
| 578 | SQL/EX | Nil params in UPDATE/DELETE WHERE — pass `u.params`/`d.params` |
| 579 | SQL/EX | AND/OR short-circuit evaluation in `evalBinary` |
| 581 | SQL/EX | GLOB regex compilation per row — byte-matcher or cached regex |
| 602 | ENG/LS | `readFromSST` does full scan — use `reader.Find(key)` |
| 603 | ENG/LS | `openSST` missing bounds checks — panic on corrupt footer |
| 604 | ENG/LS | `primaryIndex.Insert` no dedup — `Find` returns stale value |
| 605 | SQL/EX | `band()`/`bor()` NULL three-valued logic |
| 606 | SQL/EX | `evalSubstr` NULL handling (`fmt.Sprint(nil)` → `"<nil>"`) |
| 607 | SQL/EX | `CAST('false' AS BOOLEAN)` returns 1 |
| 608 | SQL/EX | Vectorized comparisons ignore NULL bitmap |
| 609 | SQL/EX | `batchToRow` sets column names to `""` |
| 610 | SYS/SE | `sessionStateMap` memory leak + atomic counter races |
| 611 | SYS/SY | Shared Executor race on `snapshotTS` |
| 630 | BK | Backup not acquiring engine lock — inconsistent snapshot |

### Phase 3 — SQL Eval Correctness (5 REQs)

Lightweight eval fixes that complement Phase 2.

| REQ | Fix |
|-----|-----|
| 580 | Pre-extract sort keys before `sort.SliceStable` — O(N log N × K) → O(N×K) |
| 619 | `SIGN(NULL)` must return NULL |
| 620 | `INSTR(NULL, ...)` must return NULL |
| 621 | `OCTET_LENGTH(blob)` uses `fmt.Sprint` → use `len(v)` |
| 631 | Propagate Eval errors in function evaluators (currently `_` swallows them) |

### Phase 4 — Infra Cleanups (18 REQs)

Lower-priority structural work. All are S or M effort.

| REQ | Subsystem | Fix |
|-----|-----------|-----|
| 583 | SQL/PS | Add visitor pattern to AST (compiles-time exhaustiveness) |
| 584 | SQL/PL | Memo LRU + schema-version key + xxhash |
| 585 | SQL/EX | Remove duplicate WHERE evaluation in DML Update/Delete |
| 586 | SQL/EX | Thread `ExecContext` through operators (start with `currentSubqueryPlanner`) |
| 587 | ENG/LS | Train SST dictionary once per SST (not per block) |
| 588 | ENG/LS | Package-level `crc32.MakeTable` + `binary.LittleEndian.AppendUint64` |
| 590 | ENG/LS | Compaction error swallowing — `slog.Error` + retry |
| 591 | SQL/EX | Package-level `soundexCodes` map |
| 593 | SQL/PS | Bare `ROLLBACK` returns `(nil, nil)` — parse as `RollbackTX` |
| 594 | WAL/FL | `Sync()` must return batch sync errors |
| 595 | MEM/BF | `Close` must flush dirty pages |
| 596 | MEM/BF | Pin/Unpin TOCTOU race |
| 612 | ENG/LS | Catalog `flushLocked` missing fsync before rename |
| 613 | ENG/LS | `GetByID` shallow copy — deep-copy slice fields |
| 614 | ENG/LS | `PutStats` no rollback on disk failure |
| 615 | ENG/LS | Bloom filter `int(uint32)` 32-bit panic |
| 616 | ENG/LS | Frozen memtable iteration order (MVCC) |
| 617 | TXN/VL | Rollback bypassing WAL |
| 618 | SYS/SE | `ReleaseSavepoint` no-op |
| 622 | SQL/EX | `DropIndex` data race on `tableIDs` |

### Phase 5 — Cosmetic / Dead Code (15 REQs)

Mechanical cleanups, no semantic impact.

| REQ | Fix |
|-----|-----|
| 589 | Remove dead code (`subFileName`, `IncRef/DecRef/RefCount`, dead `flushManager` methods) |
| 592 | Unify error sentinels (`ErrKeyNotFound` / `ErrNotFound`) — drop string-match `isNotFound` |
| 597 | Remove duplicate `flushManager` memtable tracking |
| 598 | `MergeIterator` accepts explicit dependencies (drops `*engine` coupling) |
| 623 | `bufPool` alignment waste |
| 624 | `WriteBlock` unnecessary zeroing |
| 625 | Goroutine-per-hook-per-event |
| 626 | Rotation check frequency (batch counter) |
| 627 | `lock()` deadline check after mutex |
| 628 | `Engine.Open` dead code |
| 629 | `closeBestEffort` skips executor |
| 632 | Flush orphans SST on manifest failure |
| 633 | `pkIterator` unsafe against concurrent deletes |
| 634 | `ManualCompact` sleep-based sync |
| 635 | `FIL/FS.Validate` misleading error |

### Phase 6 — Code Reorganization (1 REQ)

- **REQ000582** — Split `ps.go` (3,521 lines) into 11 per-statement files
  (`select.go`, `insert.go`, `update.go`, `delete.go`, `ddl.go`, `expr.go`,
  `window.go`, `trigger.go`, `transaction.go`, `explain.go`, `values.go`).
  Keep `ps.go` as the entry point with `Parse()` dispatch. Pure file
  reorganization, no logic changes. All existing tests must still pass.

### Phase 7 — Performance Enhancement (1 REQ)

- **REQ000571** — SST page cache (block-level + `sync.Pool`). Replace
  `os.ReadFile` in `readFromSST` and `mergeIterator.init`. Default 256 MB,
  4 KB blocks keyed by `(fileID, blockOffset)`, clock-sweep eviction.
  Compaction reads bypass the cache. Benchmarks required (allocs/op,
  ns/op for sequential and random reads before/after).

## Execution Order

```
Phase 1 (critical) → Phase 2 (high) → Phase 3 (eval) →
Phase 4 (cleanup) → Phase 5 (cosmetic) → Phase 6 (reorganize) → Phase 7 (perf)
```

Phase 1 is the gate — release blocked until all 6 pass. Phases 2-7 may run
in any order; the phase order above is by risk descending.

## Files to modify (estimated LoC)

| File | Approx LoC | Phases |
|------|------------|--------|
| `internal/ENG/LS/sst_writer.go` | +50 / −30 | 1, 2, 4, 5 |
| `internal/ENG/LS/sst_reader.go` | +60 / −20 | 1, 2, 4, 5 |
| `internal/ENG/LS/engine.go` | +40 / −20 | 1, 2, 4 |
| `internal/ENG/LS/skiplist.go` | +30 | 1 |
| `internal/ENG/LS/compaction.go` | +60 / −20 | 1, 4, 5 |
| `internal/ENG/LS/index.go` | +30 | 2, 5 |
| `internal/ENG/LS/catalog.go` | +40 / −10 | 4 |
| `internal/ENG/LS/page_cache.go` (new) | +200 | 7 |
| `internal/ENG/LS/sst_dict.go` | +20 | 4 |
| `internal/ENG/LS/flush.go` | +10 / −20 | 4, 5 |
| `internal/ENG/LS/subcompact.go` | −10 | 5 |
| `internal/ENG/LS/memtable.go` | −10 | 5 |
| `internal/WAL/RP/rp.go` | +5 / −5 | 1 |
| `internal/WAL/FL/fl.go` | +10 | 4 |
| `internal/MEM/BF/bf.go` | +30 | 4 |
| `internal/TXN/VL/protocol.go` | +20 | 1 |
| `internal/TXN/MV/version.go` | +20 | 2 |
| `internal/SYS/TX/tx.go` | +20 | 4 |
| `internal/SYS/SE/se.go` | +30 | 2, 4 |
| `internal/SYS/SY/sy.go` | +40 | 2, 5 |
| `internal/BK/bk.go` | +15 | 2 |
| `internal/FIL/DF/df.go` | +20 | 5 |
| `internal/FIL/FS/fs.go` | +5 | 5 |
| `internal/LOG/HK/hook.go` | +15 | 5 |
| `internal/LOG/LG/logger.go` | +15 | 5 |
| `internal/SQL/PS/ps.go` → 11 files | ~0 net (reorg) | 6 |
| `internal/SQL/PS/ast.go` | +40 | 4 |
| `internal/SQL/PS/visitor.go` (new) | +80 | 4 |
| `internal/SQL/PL/memo.go` | +50 / −20 | 4 |
| `internal/SQL/EX/eval.go` | +80 / −30 | 2, 3, 4 |
| `internal/SQL/EX/eval_vec.go` | +40 | 2 |
| `internal/SQL/EX/intermediate.go` | +30 | 2, 3 |
| `internal/SQL/EX/writers.go` | +20 / −30 | 2, 4 |
| `internal/SQL/EX/operators_vec.go` | +5 | 2 |
| `internal/SQL/EX/codegen_ops.go` | −50 | 2 |
| `internal/SQL/EX/adqc.go` | +20 | 2 |
| `tests/*` (new regression tests) | ~+1,500 | 1-7 |

**Estimated total LoC**: +2,500 / −500 (≈ +2,000 net)

## Deviations from iter-28

None planned. All REQs ship; no deferrals.

## Risks

1. **REQ000600 (skiplist rewrite)** — the higher-level CAS loop touches a
   concurrency primitive used by every write path. Must run with
   `go test -race` and `slt_lang_update.test` regression. Backup plan: ship
   under a feature flag and revert if the existing tests degrade.

2. **REQ000586 (ExecContext threading)** — touches the most code of any
   REQ in this iteration. Will be the last Phase 4 commit to land so the
   diff stays reviewable.

3. **REQ000582 (ps.go split)** — pure file reorg but 3,521 lines and 11
   new files. Must use `gofmt` and `goimports` to avoid import drift.
   Will land as one atomic commit.

4. **REQ000571 (page cache)** — new subsystem. Requires benchmarks before
   and after; release blocks on showing non-regression in `slt_lang_*`.

## Completion Criteria

- All 7 phases land as separate commits (or groups of related commits).
- `go vet ./...` clean, `gofmt -s -l .` clean.
- `go test ./... -race -count=1` green except for the two pre-existing
  failures (`TestWorkflowSQLite`, `TestDual_AllSeededCases`).
- All 65 REQs moved from TBD to DONE in `REQUIREMENTS.md`.
- `docs/development/ROADMAP.md` updated with iter-28.1 row.
- Tag cut: `v0.28.1`.

## See Also

- `docs/design/ARCH.md` — subsystem architecture
- `docs/development/REQUIREMENTS.md` — TBD / DONE backlog
- `docs/development/iterations/iter-28-parser-ddl-mvocc.md` — predecessor