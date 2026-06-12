# Iteration 25 — SQLite Compatibility Test Suite

**Subsystem:** `tests/sqlcmp` (new clusters: `tests/sqlcmp/slt/`, `tests/sqlcmp/dual/`)

**Status:** done

**Est. LOC:** ~3,500 (no cap)

**Target release:** v0.25.0

**Tags:** `compliance`, `tests`, `sqlcmp`

---

## Overview

This iteration builds a two-track SQLite compatibility test suite for
Razordata:

- **Track A — SQLLogicTest corpus driver.** A pure-Go parser and runner
  for the SQLLogicTest `.test` file format (the format used by SQLite's
  official test corpus at `sqlite.org/sqllogictest`). The corpus itself
  is mirrored into the repository as a git submodule from
  `github.com/MarvBeer/sqlite-test-suite` (community fossil mirror).
  The driver runs the corpus against Razordata and reports pass/fail.
- **Track C — Dual-engine result equivalence.** A pure-Go reference
  SQLite engine (`modernc.org/sqlite`, the CGO-free translation of
  SQLite's C source to Go) is embedded as a comparison oracle inside
  `tests/sqlcmp/dual/`. The same SQL is run against Razordata and the
  pure-Go reference; result sets are diffed.

**Why pure-Go, not the `sqlite3` CLI binary.** AGENTS.md forbids
external C dependencies and the build environment does not always
have `sqlite3` on PATH (confirmed: this iteration's smoke run finds
no `sqlite3` binary). `modernc.org/sqlite` is a faithful translation
of the SQLite C source, exposes the same SQL semantics, and bundles
as a single Go module. Both Razordata and the oracle are pure Go; no
subprocess, no platform-specific binaries, no PATH dependency.

**Goals.**

1. Quantify Razordata's SQL-level compatibility with SQLite as a single
   percentage that can be tracked over releases.
2. Catch result-set regressions in PRs with a fast, deterministic,
   in-process test suite.
3. Cover the gap between "Razordata parses this" (current
   `tests/sqlcmp`) and "Razordata returns the correct answer" (this
   iteration).

**Non-goals.**

- Performance, concurrency, transactional behavior — sqllogictest
  explicitly does not measure these (per sqllogictest about.wiki).
- Storage topology, MVCC, WAL — already covered by
  `tests/sqlcmp` and property-based crash-recovery tests.
- Schema features Razordata does not yet support (FOREIGN KEY,
  TRIGGER, ATTACH, GENERATED) — the driver will skip them and
  report the skip count separately, not as failures.

---

## Build Order

```
S1. Corpus submodule + SLT parser  →  S2. SLT driver  →
S3. modernc/sqlite oracle  →  S4. dual-runner  →  S5. CI hook
```

Each step must integrate with already-shipped code: SLT driver
reuses `internal/SQL/PS`, `internal/SQL/EX`, and `internal/SYS` via
the public Engine API only; it does not import private packages.

---

## Cluster A — SLT Parser & Driver (~1,400 LOC)

### Architecture

```
tests/sqlcmp/slt/
├── parser.go        # .test file → []Record
├── types.go         # Record, StatementRecord, QueryRecord, Halt, etc.
├── driver.go        # Driver interface (Connect, Exec, Query, Close)
├── razor_driver.go  # Driver implementation wrapping internal/SYS
├── runner.go        # Read records, dispatch to driver, diff results
├── diff.go          # Result-set diff (type-aware: T/I/R)
└── sanygo_test.go   # Real corpus run gated by build tag
```

`Record` shape mirrors the SQLLogicTest spec at
`https://www.sqlite.org/sqllogictest/doc/trunk/about.wiki`:

| Record | Format |
|---|---|
| `statement ok` | `<ok/error> <SQL>` |
| `statement error` | |
| `query <types> <sort> <label>` | `<SQL>\n----\n<expected rows>` |
| `halt` | |
| `hash-threshold <n>` | |
| `skipif <db>` | |
| `onlyif <db>` | |

We do **not** need to support the full TCL/Perl prototype generator —
Razordata consumes *full scripts* only, not prototype scripts. This
cuts the implementation surface by half.

### REQ000323 — Corpus Submodule Mirror

**Files:** `.gitmodules`, `tests/sqlcmp/corpus/`

Add `tests/sqlcmp/corpus/` as a git submodule pointing at
`https://github.com/MarvBeer/sqlite-test-suite` pinned to a known
good SHA. The submodule contains ~7M lines of `.test` files split
into directories (`index/`, `select1-5/`, `evidence/`, etc.).

`.gitmodules` entry:

```ini
[submodule "tests/sqlcmp/corpus"]
    path = tests/sqlcmp/corpus
    url = https://github.com/MarvBeer/sqlite-test-suite.git
    branch = main
    shallow = true
```

**Constraint:** AGENTS.md and Razordata's CI both run inside
sandboxed environments. Initializing a 700 MB submodule in CI is
costly. We add a build tag (`//go:build slt_corpus`) so the corpus
is only fetched when developers explicitly opt in. Default
`go test ./tests/sqlcmp/...` does not require the submodule.

**Pin policy:** the commit SHA is recorded in `.gitmodules` after
the first successful full run; subsequent updates require a manual
review of `CHANGELOG` between the pinned SHA and the new HEAD.

### REQ000324 — SLT Test File Parser

**Files:** `tests/sqlcmp/slt/parser.go`, `tests/sqlcmp/slt/types.go`

A streaming, line-oriented parser. ~300 LOC. Handles:

- Comment stripping (`#` to end of line)
- Blank-line record separator
- `statement ok | error` followed by multi-line SQL
- `query <types> <sort-mode> <label>` followed by SQL and
  optional `----` + result rows
- `halt`, `hash-threshold N`, `skipif <db>`, `onlyif <db>` prefixes
- Result rows as `T` (text), `I` (integer), `R` (real), `NULL`,
  `(empty)` (empty string), with `@` escape for control chars

The parser is **tolerant**: an unparseable record is reported as a
parse error and the runner continues with the next record. We do
not abort on first error — the goal is coverage, not strict
sequencing.

### REQ000325 — Driver Interface

**Files:** `tests/sqlcmp/slt/driver.go`

```go
type Driver interface {
    Connect() error
    Close() error
    Exec(ctx context.Context, sql string) error
    Query(ctx context.Context, sql string) (*ResultSet, error)
}

type ResultSet struct {
    Columns []string
    Rows    [][]Value
}

type Value struct {
    Kind int   // TypeText, TypeInteger, TypeReal, TypeNull
    Text string
    Int  int64
    Real float64
}
```

`Connect` opens a fresh in-memory engine; `Close` tears it down.
This matches SQLLogicTest's "begin with empty database" contract.

### REQ000326 — Razordata Driver Implementation

**Files:** `tests/sqlcmp/slt/razor_driver.go`

Wraps `internal/SYS.Engine` with `Options{InMemory: true}` (or the
directory-based equivalent if `InMemory` is not yet a flag — fall
back to `os.MkdirTemp` + `os.RemoveAll` on close). Exposes the
public `Engine`, `Session`, `Transaction` API. ~200 LOC.

**SQL normalization:** the SLT format uses SQLite-specific syntax
in some files. We do not attempt to translate. Instead, the runner
intercepts `syntax error: ...` and `unsupported: ...` style errors
and classifies them as `skipped` rather than `failed`.

### REQ000327 — Runner & Result Diff

**Files:** `tests/sqlcmp/slt/runner.go`, `tests/sqlcmp/slt/diff.go`

Sequential record processing:

1. Apply `skipif razor` and `onlyif <other>` filters — if the
   record's target is not `razor`, skip it.
2. `statement ok`: driver.Exec; if err, **failed**.
3. `statement error`: driver.Exec; if nil, **failed**.
4. `query <types> <sort> <label>`: driver.Query; diff result
   against expected rows, accounting for `nosort` / `rowsort` /
   `valuesort` and `label` grouping (queries with the same label
   must produce the same result set).

`diff.go` does type-aware comparison:

- `T` columns: byte-exact after the `@` control-char escape is
  applied to the actual value.
- `I` columns: integer-equality after rounding real → int if
  Razordata returns float.
- `R` columns: `%.3f` formatting match (per sqllogictest spec).
- `NULL` cells: must match `NULL` literally.

`runner.Stats()` returns:

```go
type Stats struct {
    Total       int
    Passed      int
    Failed      int
    Skipped     int
    ParseErrors int
    Duration    time.Duration
}
```

### REQ000328 — Corpus Run, Subset Default

**Files:** `tests/sqlcmp/slt/sanygo_test.go`

The default test target is a **hand-picked subset** of ~200 `.test`
files covering: `select1`, `select2`, `select4`, `index/`, `evidence/`,
`minmax/*`, `cast/*`, `null/*`, `decimal/*`, `datetime/*` (paths
relative to `corpus/test/`). This subset runs in under 60 seconds
on a developer laptop and is what CI uses.

A second target, `//go:build slt_corpus_full`, runs the entire
`corpus/test/` tree. This is nightly-only and excluded from PR
gating.

Test name: `TestSQLLogicTest_CorpusSubset`. It calls into the
runner, asserts `passed / total > threshold` where threshold is
calibrated against the v0.25.0 baseline. The threshold is
**stored as a `float64` in code**, not a build tag, so it can be
re-baselined per release without forking the test file.

---

## Cluster B — Pure-Go Reference Oracle (~1,200 LOC)

### REQ000329 — modernc.org/sqlite Dependency

**Files:** `go.mod`, `go.sum`

Add `modernc.org/sqlite` as a test-only dependency:

```
require modernc.org/sqlite v1.34.5 // indirect
```

**Verification before adding:** this library is a pure-Go
translation of the SQLite C source, ~120 MB compiled, slow on
first compile. Verify it builds in the sandbox and does not
require CGO. If it fails, fall back to a hand-rolled minimal
SQL interpreter in `tests/sqlcmp/oracle/` that supports the 30
SQL statements we need for equivalence testing (SELECT, INSERT,
UPDATE, DELETE, simple aggregates, basic WHERE, no CTEs, no
triggers, no window functions). The hand-rolled oracle is
~1,000 LOC and covers ~80% of the cases the dual-runner cares
about. **Decision deferred to first attempt** — try modernc
first, fall back if it does not build.

### REQ000330 — Dual Runner

**Files:** `tests/sqlcmp/dual/dual.go`, `tests/sqlcmp/dual/dual_test.go`

A test-only runner that, for a given input SQL:

1. Opens a Razordata engine in a temp dir.
2. Opens a `modernc.org/sqlite` in-memory connection.
3. Runs the statement on both.
4. Queries `SELECT * FROM <last_table>` on both, after DML.
5. Diffs the two result sets.

This is **not** an SLT file format. It is a Go-table-driven
harness like the existing `tests/sqlcmp/dml_cases_test.go`, but
with a real oracle on the other side. The existing 5 case files
(DDL/DML/Lexer/Select/Workflow) become the seed corpus; new
cases can be added by appending struct literals.

**Where it lives:** `tests/sqlcmp/dual/`, a sibling of
`tests/sqlcmp/slt/`. Does not pollute the existing
`tests/sqlcmp` package; imports it for `NewParser` and
`Rewrite` helpers.

### REQ000331 — Result-Set Normalization

**Files:** `tests/sqlcmp/dual/normalize.go`

modernc and Razordata may differ in:

- Integer widths (`int64` vs `int`).
- Float precision (Razordata's `DECIMAL` vs SQLite's `REAL`).
- String trailing whitespace.
- NULL vs empty string.
- Column order in `SELECT *`.

The normalizer applies a fixed set of rules before diff:

- Cast all integers to `int64`.
- Round all floats to 6 significant digits.
- Strip trailing whitespace from strings.
- Coerce `''` and `NULL` per a config flag (default: treat
  `''` as `''`, not `NULL`).
- Sort column maps by column name before comparison.

These rules are conservative; mismatches that survive
normalization are real bugs in one of the engines.

### REQ000332 — Case Authoring Convention

**Files:** `tests/sqlcmp/dual/cases/*.go`

A new case file looks like:

```go
package dual

var aggregateCases = []dualCase{
    {
        name: "count_star",
        setup: []string{
            "CREATE TABLE t (a INT)",
            "INSERT INTO t VALUES (1),(2),(3)",
        },
        query: "SELECT COUNT(*) FROM t",
        expect: [][]any{{int64(3)}},
    },
}
```

Each case is a `dualCase` struct. The test runner walks all
case files in the `cases/` directory and reports:

- `pass`: result matches oracle.
- `diff`: result differs.
- `panic`: either engine panicked.
- `unsupported`: Razordata returned a clear "unsupported" error;
  not a failure.

Initial scope: ~50 cases covering DDL, DML, aggregates, GROUP BY,
ORDER BY, LIMIT, simple joins. The case count grows each
iteration as features land.

---

## Cluster C — Reporting & CI Integration (~400 LOC)

### REQ000333 — JUnit XML Output

**Files:** `tests/sqlcmp/slt/junit.go`

For CI consumption, the SLT runner writes a JUnit-format XML
report: `<testsuite name="slt" tests="N" failures="M" ...>`.
This is the lingua franca for GitHub Actions, GitLab CI,
Jenkins. The threshold check (`passed / total > X`) lives in
the test file; CI only needs the XML.

### REQ000334 — Coverage Snapshot

**Files:** `tests/sqlcmp/slt/coverage.go`

Tracks, per `*.test` file, the pass / fail / skip count over
time. Writes `tests/sqlcmp/slt/coverage.json` after each run.
The file is git-ignored; the baseline (v0.25.0 first run) is
captured as `coverage.baseline.json` and committed. Subsequent
runs are diffed against the baseline; a regression in any
single file is a test failure.

This is the metric the team tracks across releases. A higher
pass rate is the leading indicator of SQLite compatibility.

### REQ000335 — CI Wiring (`.github/workflows/slt.yml`)

A new GitHub Actions workflow:

- Trigger: PR, nightly.
- Steps: checkout (with `submodules: recursive`), `go test
  ./tests/sqlcmp/slt/... -tags slt_corpus -run
  TestSQLLogicTest_CorpusSubset -v -timeout 30m`.
- Upload `junit.xml` as a workflow artifact.
- Post a PR comment with the pass-rate delta vs `main` branch.

**Note:** this file lives in `.github/`, not `design/`. It is
operational, not architectural.

---

## Cluster D — Documentation (~300 LOC, mostly .md)

### REQ000336 — Developer Guide

**Files:** `tests/sqlcmp/README.md`

Explains:

- How to run the suite locally:
  `go test ./tests/sqlcmp/slt/... -tags slt_corpus -v`
- How to add a new case to `tests/sqlcmp/dual/cases/`.
- How to re-baseline `coverage.baseline.json` after a release.
- The two-tier test gates: subset (PR) vs full (nightly).

### REQ000337 — Architecture Note (not in design/)

**Files:** `development/iterations/iter-25-sqlite-testsuite.md`
(this document)

Captures the architectural intent: why pure-Go oracle, why
submodule, why dual-runner. Lives in the iteration doc so it
ships with the iteration that implements it. Not promoted to
`design/` because it is a test harness, not a database
subsystem.

---

## File-by-File Line Budget

| Path | LOC |
|---|---|
| `tests/sqlcmp/slt/parser.go` | 350 |
| `tests/sqlcmp/slt/types.go` | 80 |
| `tests/sqlcmp/slt/driver.go` | 60 |
| `tests/sqlcmp/slt/razor_driver.go` | 220 |
| `tests/sqlcmp/slt/runner.go` | 280 |
| `tests/sqlcmp/slt/diff.go` | 180 |
| `tests/sqlcmp/slt/junit.go` | 110 |
| `tests/sqlcmp/slt/coverage.go` | 100 |
| `tests/sqlcmp/slt/sanygo_test.go` | 60 |
| `tests/sqlcmp/dual/dual.go` | 180 |
| `tests/sqlcmp/dual/normalize.go` | 140 |
| `tests/sqlcmp/dual/dual_test.go` | 100 |
| `tests/sqlcmp/dual/cases/*.go` (5 files × 80) | 400 |
| `tests/sqlcmp/README.md` | 120 |
| `tests/sqlcmp/slt/README.md` | 80 |
| `.gitmodules` (1 entry) | 6 |
| `.github/workflows/slt.yml` | 50 |
| **Total** | **~2,516** |

Plus corpus submodule (no LOC; data only).

---

## Dependencies on Existing Code

| Existing | How this iteration uses it |
|---|---|
| `internal/SYS` `Engine`, `Options`, `Session`, `Transaction` | Public API for `razor_driver.go` |
| `internal/SQL/PS` `Parser`, `Stmt` | Indirect (via `Engine.Query`) |
| `internal/SQL/EX` `EXPLAIN` | Used by debug build of runner to annotate failures |
| `tests/sqlcmp` existing 5 case files | `dual/cases/` reuses the table-driven pattern |
| `go test -race -count=1` | Quality gate; new tests must comply |

**Gap analysis** of pre-existing bugs that this iteration surfaces
but does not fix:

- `REQ000284` (BTree delete rebalancing) — if SLT corpus
  exercises `DELETE` heavily, mismatches will appear. Logged in
  unfixed bugs, not fixed here.
- `REQ000285` (uint32 page ID overflow) — corpus with >4B
  pages will trigger. Not in scope.
- `REQ000286/287` (window materialization) — corpus window
  tests will fail. Out of scope.

These are encoded as unfixed-bug REQs in the same commit, per
AGENTS.md's Bug-To-Requirement Rule.

---

## Implementation Order

1. **REQ000323** — submodule + .gitmodules entry. Trivial, but
   unblocks everything.
2. **REQ000324** — parser. Test against 5 hand-written `.test`
   fixtures first.
3. **REQ000325 + REQ000326** — Driver + Razordata impl. Run a
   single test file end-to-end.
4. **REQ000327** — runner + diff. Run the `select1.test` subset.
5. **REQ000328** — gate the subset on a calibrated threshold.
6. **REQ000329** — add modernc dependency. **Decision point**: if
   it fails to build, implement `tests/sqlcmp/oracle/` fallback.
7. **REQ000330 + REQ000331** — dual runner + normalizer.
8. **REQ000332** — seed 50 dual cases.
9. **REQ000333 + REQ000334** — JUnit output + coverage snapshot.
10. **REQ000335** — CI workflow.
11. **REQ000336 + REQ000337** — documentation.

---

## Testing Requirements

**Each REQ must have:**

1. Unit tests (table-driven, fixture-based, golden file).
2. Integration: parser test against fixtures in
   `tests/sqlcmp/slt/testdata/*.test`.
3. Property test: random valid SLT input round-trips through
   parser → formatter → parser.
4. Dual runner: every case file has an `oracle-vs-razor` diff
   test.

**Quality gates:**

- `go test ./... -race -count=1` — all green.
- `go test ./tests/sqlcmp/slt/... -tags slt_corpus -run
  TestSQLLogicTest_CorpusSubset` — pass rate ≥ calibrated
  threshold.
- `go test ./tests/sqlcmp/dual/...` — all seeded cases pass.
- `go vet ./...` — zero warnings.
- `gofmt -s -l .` — no drift.

**Performance:**

- SLT parser: 50 MB / second on a single core.
- Dual runner: 100 cases / second.
- Corpus subset: completes within 60 seconds.

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| `modernc.org/sqlite` does not build in sandbox | medium | medium | Fallback to hand-rolled oracle in `tests/sqlcmp/oracle/` |
| Corpus submodule is too large for CI | medium | low | Build tag gates full corpus; default PR uses 200-file subset |
| Razordata result sets diverge from oracle in edge cases | high | low | These are real bugs; surface as test failures, log via REQ |
| Submodule pin drifts from upstream | low | low | Pin to SHA, re-baseline on intentional bumps |
| modernc/sqlite is too slow for CI | medium | medium | Use subset only in PR; full corpus is nightly |

---

## Out of Scope (Future Iterations)

- **Performance parity benchmarks** — would need a separate
  harness; not in this iteration.
- **Transaction isolation tests** — sqllogictest explicitly
  ignores these; would need FoundationDB-style schedule
  explorer (covered by REQ000321 DST).
- **Fuzzing the parser** — go-fuzz on the SLT parser itself;
  orthogonal to this iteration.
- **Property-based result-set checks** — `rapid.Check` on dual
  runner; defer until dual runner stabilizes.

---

## Outcome

**Status:** done. Shipped in v0.25.0.

### What Shipped

| Cluster | Files | LOC | Status |
|---|---|---|---|
| A — SLT parser / driver | `slt/types.go`, `slt/parser.go`, `slt/driver.go`, `slt/razor_driver.go`, `slt/filestat.go`, `slt/hash.go` | ~1,200 | done |
| A — Runner + diff | `slt/runner.go`, `slt/diff.go` | ~470 | done |
| A — Corpus subset gate | `slt/subset.go`, `slt/corpus_test.go` | ~200 | done (skeleton; corpus not yet cloned) |
| A — Coverage / JUnit | `slt/coverage.go`, `slt/junit.go` | ~190 | done |
| A — Tests + sample | `slt/parser_test.go`, `slt/runner_test.go`, `slt/razor_driver_test.go`, `slt/junit_test.go`, `slt/coverage_test.go`, `slt/sample_test.go` | ~770 | done |
| B — Dual runner | `dual/dual.go`, `dual/shim.go`, `dual/cases.go`, `dual/dual_test.go` | ~570 | done (modernc.org/sqlite oracle) |
| D — Documentation | `tests/sqlcmp/README.md`, `corpus/README.md` | ~150 | done |
| A — Submodule | `.gitmodules`, `corpus/.gitkeep` | n/a | done (submodule URL recorded; clone is human-gated) |
| **Total** | 18 source + 6 test files | **~3,560** | |

### Deviations from Plan

- **REQ000329 (modernc.org/sqlite):** first-choice path worked
  on the first try. The hand-rolled oracle fallback in
  `tests/sqlcmp/oracle/` was not implemented. ~1,000 LOC of
  spec-estimated work avoided.
- **REQ000335 (CI workflow):** not implemented this iteration.
  The spec assumed a `.github/workflows/slt.yml` file; the
  repository has no `.github/` directory and CI integration is
  a human-gated decision. The CI hook logic (env-driven JUnit
  output, env-driven baseline diff) is in place and ready to
  be wired up when the workflow file lands.
- **Coverage baseline:** the v0.25.0 baseline
  (`tests/sqlcmp/slt/testdata/coverage.baseline.json`) is
  intentionally not committed — the corpus submodule is not
  cloned in this development environment, so no real corpus
  data is available. The first release to run the corpus
  against a populated submodule will commit the baseline.

### Bugs Surfaced

- **REQ000345** — `SELECT COUNT(*) FROM t` on an empty `t`
  returns zero rows instead of one row with `0`. Filed in
  `REQUIREMENTS.md` Unfixed Bugs. Discovered by the dual
  runner: the modernc oracle returns `[[0]]` and the
  Razordata side returns `[]`. The bug is in the SQL/EX
  aggregate operator's empty-input path, not in the
  test driver.

### Verification

- `go test -race -count=1 ./tests/sqlcmp/...` — all green
  (slt: 28 unit + 4 integration + 5 coverage; dual: 7; existing
  5 case files unchanged).
- `go test -race -count=1 -tags slt_corpus ./tests/sqlcmp/slt/...`
  — all green; the corpus subset test correctly skips when
  the corpus submodule is not initialised.
- `go build ./...` — clean.
- `go vet ./tests/...` — clean.
- `gofmt -s -l tests/sqlcmp/` — no drift.

### Open Items for Future Iterations

- Clone the corpus submodule and commit the first real
  coverage baseline.
- Add `.github/workflows/slt.yml` (REQ000335) when CI is
  configured.
- Address REQ000345 (empty-table aggregate) to bring the
  pass rate up.
- Property-based fuzzing of the SLT parser
  (deferred per spec, Out of Scope).
