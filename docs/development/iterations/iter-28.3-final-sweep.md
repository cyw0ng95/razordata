# Iteration 28.3 — Final Bugfix Sweep + Remaining TBD (target v0.28.3)

Status: **in-progress** (v0.28.3).

## Scope

All remaining 25 genuinely-unshipped TBD REQs from `REQUIREMENTS.md`, plus paperwork cleanup for 19 iter-28.2-shipped REQs still sitting in TBD. The XL-effort items (REQ000321, 543, 549, 556, 316) are deferred to iter-29; all S/M/L items ship in 28.3.

| Category | Count | Effort |
|----------|------:|--------|
| Paperwork (move TBD→DONE for already-shipped) | 19 | 0 LoC |
| critical (wrong results) | 3 | 1×S, 1×M, 1×L |
| high (wrong rows / semantics) | 7 | 3×S, 4×M |
| medium (correctness / cleanup) | 5 | 2×S, 1×M, 2×L |
| L-effort features | 5 | 5×L |
| XL (deferred to iter-29) | 5 | 5×XL |
| **Total shipped in 28.3** | **20** | **6×S, 5×M, 6×L** |

## Phasing

Each phase ships as one or more commits. After each commit `go vet ./...`, `gofmt -s -l .`, and `go test ./internal/... -race -count=1` must pass (excluding the two pre-existing failures: `TestWorkflowSQLite`, `TestDual_AllSeededCases`).

### Phase 0 — Paperwork Cleanup

Move 19 iter-28.2-shipped REQs from TBD to DONE. No code changes.

**REQs**: 530, 553, 585, 589, 592, 595, 596, 597, 598, 608, 611, 614, 616, 617, 618, 630, 632, 633, 635

### Phase 1 — Critical Bugfixes (6 REQs)

| REQ | Subsystem | Priority | Effort | Fix |
|-----|-----------|----------|--------|-----|
| 640 | SQL/EX | critical | S | NOT IN with table scan — `evalIn` NOT IN path returns true for non-matching, not false |
| 642 | SQL/EX | critical | S | REPLACE INTO doesn't remove conflicting row — verify removeConflicting persistence |
| 646 | SQL/EX | critical | L | WHERE column-to-column comparison wrong — fix scalar + vectorized col-col compare |
| 641 | SQL/EX | high | M | DELETE FROM view — resolve view to base table delete path |
| 647 | SQL/EX | high | M | WHERE NOT BETWEEN returns 2× rows — fix evalBetween NOT case |
| 648 | SQL/EX | high | M | WHERE IS NULL returns wrong count — fix NULL-bitmap traversal for column refs |

### Phase 2 — Remaining High/Medium Bugfixes (6 REQs)

| REQ | Subsystem | Priority | Effort | Fix |
|-----|-----------|----------|--------|-----|
| 643 | SQL/PS | high | M | Trigger body semicolon rejected — accept semicolons as stmt separators in trigger body |
| 644 | SQL/EX | high | M | GROUP BY with qualified column reference — resolve through alias chain |
| 645 | SQL/EX | medium | S | DISTINCT on constant expression returns 0 rows — handle single-row constant input |
| 649 | SQL/EX | high | S | abs() with WHERE produces double rows — fix abs() in filter context |
| 650 | SQL/EX | medium | S | null IN () returns NULL instead of 0 — handle empty-list case |
| 547 | TXN/MV | medium | M | Per-NUMA arena pools — extend generational arena with per-NUMA pools |

### Phase 3 — L-Effort Features (6 REQs)

| REQ | Subsystem | Priority | Effort | Fix |
|-----|-----------|----------|--------|-----|
| 571 | ENG/LS | critical | L | SST page cache — block-level cache + sync.Pool; 256 MB, 4 KB blocks |
| 583 | SQL/PS | medium | L | Visitor pattern on AST — Accept() + BaseVisitor + HashVisitor |
| 586 | SQL/EX | medium | L | Thread ExecContext through operators — eliminate package-level globals |
| 537 | ENG/LS | high | L | Sharded memtable (per-prefix skiplist, N=16 default) |
| 538 | ENG/LS | high | L | Zero-copy iterator with borrowed buffers + epoch registration |
| 539 | MEM/BF | high | L | Sharded buffer pool with per-shard clock hands |
| 542 | WAL/FL | high | L | Group commit pipeline (50µs deadline) |

### Phase 4 — Deferred to iter-29 (5 XL REQs)

| REQ | Subsystem | Effort | Description |
|-----|-----------|--------|-------------|
| 316 | SQL/EX | L | Incremental materialized views — new subsystem |
| 321 | TXN | XL | Deterministic Simulation Testing framework — new test framework |
| 543 | SQL/EX | XL | Shape-specialized fast paths — new codegen shapes subsystem |
| 549 | MEM/BF | XL | Block-level MVCC version tagging — cross-txn cache sharing |
| 556 | SQL/PS+EX | XL | CREATE VIRTUAL TABLE parser + executor — new subsystem |

## Execution Order

```
Phase 0 (paperwork) → Phase 1 (critical bugs) → Phase 2 (high/medium bugs) → Phase 3 (L features) → Phase 4 (defer)
```

## Completion Criteria

- All phases land as separate commits (or groups of related commits).
- `go vet ./...` clean, `gofmt -s -l .` clean.
- `go test ./internal/... -race -count=1` green except for the two pre-existing failures.
- All shipped REQs moved from TBD to DONE in `REQUIREMENTS.md`.
- Deferred REQs remain in TBD with `Effort` XL and a note referencing iter-28.3.
- `docs/development/ROADMAP.md` updated with iter-28.3 row.
- Tag cut: `v0.28.3`.
