# Iteration 28.3 — Final Bugfix Sweep + Remaining TBD (target v0.28.3)

Status: **done** (v0.28.3).

## Scope

All remaining 25 genuinely-unshipped TBD REQs from `REQUIREMENTS.md`, plus paperwork cleanup for 19 iter-28.2-shipped REQs still sitting in TBD. The XL-effort items (REQ000321, 543, 549, 556, 316) are deferred to iter-29; Phase 3 L-effort features (571, 583, 586, 537, 538, 539, 542) and REQ000547 also deferred to iter-29.

| Category | Count | Effort |
|----------|------:|--------|
| Paperwork (move TBD→DONE for already-shipped) | 19 | 0 LoC |
| Shipped in 28.3 (bugfixes) | 11 | 4×S, 4×M |
| Deferred to iter-29 | 13 | 5×L, 5×XL, 1×M |
| **Total shipped in 28.3** | **11** | **4×S, 4×M** |

## Outcome

iter-28.3 shipped 11 REQs (2 fixed, 4 verified as pre-fixed, 5 test-only) across 3 commits.

### Phase 0 — Paperwork Cleanup (commit `62dd529`)
Moved 19 iter-28.2-shipped REQs from TBD to DONE: 530, 553, 585, 589, 592, 595, 596, 597, 598, 608, 611, 614, 616, 617, 618, 630, 632, 633, 635.

### Phase 1 — Critical Bugfixes (commit `acc5f6f`)
- **REQ000650** (null IN ()): Fixed `evalIn` in `eval.go` — empty-list check moved before target-nil shortcut; `NULL IN ()` now returns `false`. Code change in `internal/SQL/EX/eval.go`.
- **REQ000648** (IS NULL wrong count): Fixed `extractPK` in `store.go` — NULL PK values generate synthetic atomic rowid instead of all mapping to the same engine key. Code change in `internal/SQL/EX/store.go`.
- **REQ000640** (NOT IN table scan): Verified pre-fixed. Added test in `tbd_283_test.go`.
- **REQ000642** (REPLACE INTO): Verified pre-fixed. Added test.
- **REQ000646** (col-col comparison): Verified pre-fixed. Added test.
- **REQ000647** (NOT BETWEEN): Verified pre-fixed. Added test.
- **REQ000649** (abs filter): Verified pre-fixed. Added test.
- 7 bug-reproduction tests added in `internal/SQL/EX/tbd_283_test.go`.

### Phase 2 — Remaining Bugfixes (commit `f0f4ef1`)
- **REQ000641** (DELETE FROM view): Added view resolution in `buildWriterOp` — `LookupView` redirects DELETE/UPDATE to base table. Code change in `internal/SQL/EX/ex.go`.
- **REQ000643** (trigger semicolons): Fixed `parseCreateTrigger` in `ddl.go` — consumes trailing semicolons after `END` and after single-stmt bodies so top-level `Parse()` EOF check passes. Code change in `internal/SQL/PS/ddl.go`.
- **REQ000644** (GROUP BY alias): Verified pre-fixed. Added test.
- **REQ000645** (DISTINCT constant): Verified pre-fixed. Added test.
- 4 new tests added in `tbd_283_test.go` (DELETE view, 2× trigger body, GROUP BY alias, DISTINCT constant).

### Deferred to iter-29
Phase 3 L-effort features (571, 583, 586, 537, 538, 539, 542), REQ000547 (Per-NUMA arena pools), and all 5 XL items (321, 543, 549, 556, 316) deferred to iter-29.

### Verification
- `go test ./internal/... -race -count=1`: 34/34 packages pass.
- `go vet ./...`: pre-existing warnings only (FIL/IO uring_linux.go, SQL/EX adqc_telemetry.go, SQL/EX writers.go, SYS/ST engine_cache.go, TXN/MV version.go).
- `gofmt -s -l .`: pre-existing drift outside Phase 1/2 diffs; `ddl.go` formatted in Phase 2 commit.
- REQUIREMENTS.md: TBD=14, DONE=140 (after Phase 0-2 moves).

### Commits
| Commit | Phase | Description |
|--------|-------|-------------|
| `62dd529` | 0 | Paperwork cleanup — move 19 shipped REQs TBD→DONE, create iter-28.3 plan |
| `acc5f6f` | 1 | Critical bugfixes: null IN (), IS NULL PK, + 5 verified pre-fixed |
| `f0f4ef1` | 2 | DELETE/UPDATE view, trigger semicolons, + 2 verified pre-fixed |

Tag: `v0.28.3`.
