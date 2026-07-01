# ERROR System Refactor — Design Decisions and Implementation Roadmap

> Generated: 2026-07-01
> Scope: Cross-cutting ERROR model refactor (Code + SQLSTATE + Module + Layer + Classification).
> Status: Design frozen, iter-36 ready to start.
> Audience: Subsequent agents working on iter-36 through iter-40.

## Why this refactor exists

The current error system is fragmented across 12 clusters with 27 package-level sentinel
errors and only 1.5 structural error types (`AP.Error`, `PS.SyntaxError`). Key problems:

1. **Naming collisions**: `ErrNoRows` exists in both `SQF/PL` and `SYS/AP`; `ErrDivByZero` exists
   in both `SQB/EV` and `SQB/UT`.
2. **Inconsistent prefixes**: some clusters use `"cluster: msg"`, others use bare messages, some
   use long descriptive sentences.
3. **Multi-layer wrap duplicates messages**: `AP.Wrap` produces `Kind: msg: msg: msg` chains
   because `Error()` recurses through the wrapped error.
4. **No stable error code**: callers can only match by string prefix or sentinel equality.
   `errors.Is` works but `errors.Is(err, SomeOtherDBError)` cannot.
5. **Retry/Fatal classification is per-Kind only**: any unwrapped error becomes fatal by default,
   including IO errors that should retry.
6. **Design/implementation gap**: `docs/design/subsystems/SYS.md` already documents the
   `AP.Kind` + `AP.Wrap` contract, but only `SYS/SE` actually performs the wrapping. The other
   11 clusters return raw sentinels.

## Scope of this document

This report captures the design decisions for the ERROR refactor that downstream agents
must follow. It is the contract between the design discussion (this report) and the
implementation iterations (iter-36 through iter-40).

**Out of scope**:
- Changes to any file under `docs/design/` (human-only per AGENTS.md). A separate human action
  list is in `design-doc-update-list.md`.
- Changes to error generation in lower layers (those happen in iter-37+).

## Frozen decisions (15 items)

| # | Dimension | Decision |
|---|---|---|
| 0 | Overall approach | **Layered structural: B** — `AP.Error` extended; lower-layer sentinels retained as deprecation shims |
| 1 | Error code | **Code + SQLSTATE dual field** — internal stable `Code` + external standard 5-char `SQLSTATE` |
| 2 | Legacy sentinels | **Retain 1-2 iterations, then delete** — `// Deprecated:` comments; `errors.Is` compatibility throughout deprecation window |
| 3 | Struct fields (iter-36) | `Code` + `SQLSTATE` + `Module` + `Layer` + `Fields` + `Cause` |
| 4 | Struct fields (iter-38) | `Op` (operation name: SELECT / INSERT / LOCK / REPLAY / ...) |
| 5 | Field naming | `wrapped` retained as internal alias; new public field is **`Cause error`** pointing at the same underlying value |
| 6 | Retry/Fatal API | **New: `Classify(err) Classification` returning a struct** (Kind / Code / SQLSTATE / Retryable / Fatal); existing `IsRetryable(err)` and `IsFatal(err)` retained for compatibility |
| 7 | Cluster placement | **Inside `SYS/AP`** — new files `error_kind.go`, `error_code.go`, `error_classify.go`; do not create `internal/ERR/` |
| 8 | `Fields` type | **`map[string]string`** — no boxing; structured serialization stays simple |
| 9 | i18n | iter-36 does NOT implement; comment reserves `// TODO: Localized(locale string) string` |
| 10 | Logging/Metrics | Optional `Emit func(*Error)` hook in `AP` package; default nil; caller decides |
| 11 | `database/sql/driver.Error` | iter-36 implements `SQLState() string` method on `*Error` |
| 12 | Stability promise | v1.0 free to renumber; v1.0+ locks Code + SQLSTATE |
| 13 | Wrap output | `Error()` is single-line summary; `%+v` expands the cause chain |
| 14 | Deprecation style | `// Deprecated:` comment + `go vet` reporting |
| 15 | NotFound split | Keep single `KindNotFound`; differentiate via `Fields["entity"]` (`"table"` / `"column"` / `"key"` / `"savepoint"` / `"index"`) |
| 16 | New `Kind` constants | `KindInternal`, `KindNotImplemented`, `KindConflict`, `KindResourceExhausted`, `KindParse` (split from `KindSyntax` for lexer/parser layer) |

## Five `KindNotFound` entity values

```go
const (
    EntityTable      = "table"
    EntityColumn     = "column"
    EntityKey        = "key"
    EntitySavepoint  = "savepoint"
    EntityIndex      = "index"
    EntityView       = "view"
    EntityTrigger    = "trigger"
    EntityConstraint = "constraint"
)
```

Use these constants (defined in `error_kind.go`) when populating `Fields["entity"]`.

## `AP.Error` final shape (target after iter-36)

```go
// internal/SYS/AP/error.go (revised)

type Error struct {
    Kind     Kind
    Code     Code         // stable, RZR-{LAYER}-{NNN}
    SQLSTATE SQLSTATE     // 5-char standard SQL state
    Module   Module       // cluster identifier, e.g. "SQB/EV"
    Layer    Layer        // subsystem layer, e.g. "sql"
    Message  string       // human-readable summary
    Fields   map[string]string // structured context (line/col/table/column/key/...)
    Cause    error        // underlying cause (same as wrapped, public alias)
    wrapped  error        // existing internal field; Unwrap() reads this
}

// Methods:
//   Error() string                                  — single-line summary
//   Unwrap() error                                  — returns wrapped
//   SQLState() string                               — driver.Error compatibility
//   Format(s fmt.State, verb rune)                  — %v = summary, %+v = chain
//
// Constructors:
//   New(kind Kind, msg string) *Error               — basic; auto-fills Code/SQLSTATE/Module/Layer
//   Wrap(kind Kind, err error) *Error               — wraps an existing error
//   Newf(kind Kind, format string, args ...any) *Error
//   Wrapf(kind Kind, err error, format string, args ...any) *Error
//   WithField(key, value string) *Error             — fluent builder
//   WithModule(m Module) *Error
//   WithLayer(l Layer) *Error
//   WithOp(op string) *Error                        — populates Fields["op"]
//
// Helpers:
//   IsKind(err error, kind Kind) bool               — existing, retained
//   IsCode(err error, code Code) bool               — new
//   IsSQLState(err error, state SQLSTATE) bool      — new
//   Classify(err error) Classification              — new; returns full struct
//   IsRetryable(err error) bool                     — retained, uses Classify
//   IsFatal(err error) bool                         — retained, uses Classify
//   CodeOf(k Kind) Code
//   SQLStateOf(k Kind) SQLSTATE
//   ModuleOf(err error) Module                      — extracts module from chain
//   LayerOf(err error) Layer
//
// Hook:
//   Emit func(*Error)                               — optional; nil by default
//   SetEmit(fn func(*Error))                        — global setter (testable)
```

## Code table (snapshot — locked after iter-39)

Format: `RZR-{LAYER}-{NNN}` where NNN is a 3-digit fixed number per Kind.

| Kind | Code | SQLSTATE | Retryable | Fatal |
|---|---|---|---|---|
| `KindNotFound` | `RZR-SQL-001` | `"02000"` | no | yes |
| `KindDuplicateKey` | `RZR-SQL-002` | `"23000"` | no | yes |
| `KindTypeMismatch` | `RZR-SQL-003` | `"22005"` | no | yes |
| `KindSyntax` | `RZR-SQL-004` | `"42000"` | no | yes |
| `KindParse` | `RZR-SQL-005` | `"42000"` | no | yes |
| `KindConstraint` | `RZR-SQL-006` | `"23000"` | no | yes |
| `KindLocked` | `RZR-SQL-007` | `"40001"` | **yes** | no |
| `KindTxAborted` | `RZR-SQL-008` | `"40001"` | no | yes |
| `KindDeadlineExceeded` | `RZR-SQL-009` | `"57014"` | no | yes |
| `KindIO` | `RZR-IO-001` | `"08006"` | **yes** | no |
| `KindCorrupt` | `RZR-IO-002` | `"08001"` | no | yes |
| `KindReadOnly` | `RZR-CFG-001` | `"25006"` | no | yes |
| `KindUpgradeRequired` | `RZR-CFG-002` | `"08001"` | no | yes |
| `KindClosed` | `RZR-CFG-003` | `"08003"` | no | yes |
| `KindInvalidOptions` | `RZR-CFG-004` | `"08001"` | no | yes |
| `KindInternal` | `RZR-INT-001` | `"58000"` | no | yes |
| `KindNotImplemented` | `RZR-INT-002` | `"0A000"` | no | yes |
| `KindConflict` | `RZR-INT-003` | `"40001"` | no | yes |
| `KindResourceExhausted` | `RZR-INT-004` | `"54000"` | no | yes |

**Stability rule**: NNN numbers are append-only. Once a Kind gets a Code, that Code never
renumbers. New Kinds added in later versions get higher NNN values in the same Layer.

## Layer table

```go
const (
    LayerSQL    Layer = "sql"
    LayerTXN    Layer = "txn"
    LayerENG    Layer = "eng"
    LayerWAL    Layer = "wal"
    LayerFIL    Layer = "fil"
    LayerMEM    Layer = "mem"
    LayerLOG    Layer = "log"
    LayerIO     Layer = "io"
    LayerConfig Layer = "config"
)
```

Modules (free-form strings, cluster identifier) follow convention `"AAA/BB"` where AAA is
the subsystem and BB is the cluster (e.g., `"SQB/EV"`, `"WAL/RP"`, `"TXN/VL"`).

## Classification struct

```go
type Classification struct {
    Kind      Kind
    Code      Code
    SQLSTATE  SQLSTATE
    Retryable bool
    Fatal     bool
}

func Classify(err error) Classification {
    var e *Error
    if errors.As(err, &e) {
        return Classification{
            Kind:      e.Kind,
            Code:      e.Code,
            SQLSTATE:  e.SQLSTATE,
            Retryable: retryableKinds[e.Kind],
            Fatal:     fatalKinds[e.Kind],
        }
    }
    return Classification{Kind: KindInternal, Code: "RZR-INT-001", SQLSTATE: "58000", Fatal: true}
}
```

## Backward-compatibility shim

Existing sentinel variables (`ErrNotFound`, `ErrDuplicateKey`, ...) must keep working
with `errors.Is`. Implementation pattern:

```go
// Deprecated: Use AP.IsKind(err, AP.KindNotFound) instead.
// This sentinel will be removed in v1.0.
var ErrNotFound = New(KindNotFound, "key not found")
```

`New(...)` returns `*Error` with all fields auto-filled from `Kind`. `errors.Is(err, ErrNotFound)`
works because `ErrNotFound` is already an `*Error` and `errors.Is` compares pointer equality.

**Important**: do NOT change the value of any sentinel — any code comparing by pointer
(e.g., `err == ErrNoRows`) will break.

## Migration of conflicting names

The two `ErrNoRows` and two `ErrDivByZero` collisions must be resolved before iter-40:

| Old location | Old name | New location | New name |
|---|---|---|---|
| `SQF/PL/types.go` | `ErrNoRows` | (removed) | use `AP.New(KindNotFound, ...)` with `Fields["entity"]=EntityRow` |
| `SYS/AP/ap.go` | `ErrNoRows` | (removed) | use `AP.New(KindNotFound, ...)` |
| `SQB/EV/eval.go` | `ErrDivByZero` | `SQB/EV/eval.go` | `ErrEvalDivByZero` (rename + deprecation alias for old name) |
| `SQB/UT/decimal.go` | `ErrDivByZero` | `SQB/UT/decimal.go` | `ErrDecimalDivByZero` |

The rename in `SQB/EV` and `SQB/UT` happens in iter-37 with deprecation aliases so `errors.Is`
keeps working for one iteration before iter-40 deletes the old names.

## Iteration roadmap

### iter-36 — ERROR Foundation

**Scope** (additive only; no breaking changes):
- New `internal/SYS/AP/error_kind.go` with all `Kind` constants and `Kind.String()`.
- New `internal/SYS/AP/error_code.go` with `Code`, `SQLSTATE`, `CodeOf`, `SQLStateOf`, full mapping table.
- New `internal/SYS/AP/error_classify.go` with `Classification`, `Classify`, retry/fatal lookup tables.
- Modify `internal/SYS/AP/error.go`:
  - Add `Code`, `SQLSTATE`, `Module`, `Layer`, `Fields`, `Cause` fields to `Error` struct.
  - `Cause` is a public alias of the existing `wrapped` field (both share the same value).
  - Add `SQLState() string` method (driver.Error compatibility).
  - Replace `Error()` with new format `"{Module}/{Layer} {Code} [{SQLSTATE}]: {Message}"`.
  - Add `Format(s, verb)` for `%+v` chain expansion.
  - Add optional `Emit func(*Error)` and `SetEmit`.
- Modify `internal/SYS/AP/ap.go`:
  - All 14 existing sentinel `*Error` instances auto-fill `Code`/`SQLSTATE` via `New(...)`.
  - Add `// Deprecated:` comment above each sentinel.
- Extend `internal/SYS/AP/error_test.go`:
  - Snapshot test for `Error()` string format per Kind.
  - Snapshot test for `%+v` chain expansion.
  - `Classify` test for every Kind.
  - Compatibility test: `errors.Is(err, OldSentinel)` must work.
  - Compatibility test: `IsRetryable` / `IsFatal` still work.
- All existing tests in repo must still pass with zero modification.

**Acceptance**:
- `go vet ./...` zero warnings (deprecation comments do not produce warnings by default).
- `go test ./... -race -count=1` passes.
- `internal/SYS/AP/` test coverage >= 95% on error files.
- All existing 45 `errors.Is(err, ErrXxx)` test sites unchanged.

**Out of scope**:
- Cluster exit-point wrapping (iter-37).
- Sentinel removal (iter-40).
- NotFound entity splitting (already decided; just document constants).
- i18n / Logging / Op field (later iterations).

### iter-37 — Wrap Boundary

- Rename conflicting sentinels: `EV.ErrDivByZero` → `EV.ErrEvalDivByZero`; `UT.ErrDivByZero` → `UT.ErrDecimalDivByZero`. Both old names kept as deprecated aliases.
- New `internal/SYS/AP/error_wrap.go`:
  - `WrapAt(kind Kind, module Module, layer Layer, err error) *Error` — auto-fills module/layer.
  - `FromSentinel(err error) *Error` — recognizes known sentinel pointers and wraps with matching Kind.
- Install wrap layer at cluster exit points: `EX/Exec`, `EX/Query`, `SE/Exec`, `SE/Query`, `ST/Query`, `DS/Query`.
- All errors that cross subsystem boundary must come out as `*AP.Error`.

### iter-38 — Structural Error Types

- Add `Op` field to `AP.Error` struct.
- Add `Op` constants: `OpSelect`, `OpInsert`, `OpUpdate`, `OpDelete`, `OpCreate`, `OpDrop`, `OpAlter`, `OpBegin`, `OpCommit`, `OpRollback`, `OpSavepoint`, `OpReplay`, `OpFlush`, `OpCompact`, `OpCheckpoint`, `OpBackup`, `OpRestore`.
- Promote structural error types:
  - `PS.SyntaxError{Line, Col, Msg}` (already exists; align field names).
  - New `EX.ConstraintError{Table, Constraint, Columns, Values, Op}`.
  - New `WAL.RP.ReplayError{LSN, Cause, Op}`.
  - New `TX.VL.DeadlockError{TxID, Holders, Op}`.
- Each structural error implements a `ToAPError() *AP.Error` method.

### iter-39 — Code + SQLSTATE Snapshot Lock

- Lock `Code` and `SQLSTATE` constants with `// stable since v0.9.0` (current version 0.8.1; bumped at iter-39 start).
- Add wire format: `AP.Error.Error()` stable string format with byte-exact snapshot tests.
- Add JSON marshaling for `AP.Error`; snapshot tests for marshaled output.
- Document wire format compatibility promise in design doc (human edits).

### iter-40 — Sentinel Removal

- Remove deprecation aliases for old sentinels in `SYS/AP/ap.go`.
- Remove old names: `EV.ErrDivByZero`, `UT.ErrDivByZero`.
- Migrate remaining `errors.Is(err, ErrXxx)` to `AP.IsKind(err, KindXxx)` or `AP.IsCode(err, CodeXxx)`.
- Migrate all `==` comparisons (e.g., `err == ErrNoRows`) to `AP.IsKind(err, KindNotFound)`.
- `go vet` should report zero deprecated usages.

## Files to create or modify

### iter-36

- **NEW**: `internal/SYS/AP/error_kind.go`
- **NEW**: `internal/SYS/AP/error_code.go`
- **NEW**: `internal/SYS/AP/error_classify.go`
- **MODIFY**: `internal/SYS/AP/error.go`
- **MODIFY**: `internal/SYS/AP/ap.go` (only sentinel declarations; add `// Deprecated:`)
- **MODIFY**: `internal/SYS/AP/error_test.go` (extend existing tests)

### iter-37

- **NEW**: `internal/SYS/AP/error_wrap.go`
- **MODIFY**: `internal/SQB/EV/eval.go` (rename `ErrDivByZero`)
- **MODIFY**: `internal/SQB/UT/decimal.go` (rename `ErrDivByZero`)
- **MODIFY**: `internal/SQB/EX/ex.go` (add wrap at exit points)
- **MODIFY**: `internal/SYS/SE/se.go` (extend wrap calls)
- **MODIFY**: `internal/SYS/ST/st.go` (extend wrap calls)
- **MODIFY**: `internal/SYS/DS/` (driver wrap if needed)

### iter-38

- **MODIFY**: `internal/SYS/AP/error.go` (add `Op` field, `Op` constants)
- **NEW**: `internal/SQB/EX/constraint_error.go`
- **NEW**: `internal/WAL/RP/replay_error.go`
- **NEW**: `internal/TXN/VL/deadlock_error.go`
- **MODIFY**: `internal/SQF/PS/error.go` (align `SyntaxError` fields)

### iter-39

- **MODIFY**: `internal/SYS/AP/error_code.go` (lock constants with `// stable since`)
- **NEW**: `internal/SYS/AP/error_wire_test.go` (snapshot tests)
- **NEW**: `internal/SYS/AP/error_json_test.go`

### iter-40

- **MODIFY**: `internal/SYS/AP/ap.go` (remove 14 deprecated sentinels)
- **MODIFY**: `internal/SQB/EV/eval.go` (remove `ErrDivByZero` alias)
- **MODIFY**: `internal/SQB/UT/decimal.go` (remove `ErrDivByZero` alias)
- **MODIFY**: all test files using removed sentinels (migrate to `IsKind` / `IsCode`)

## Test contracts

### Existing tests that must NOT break in iter-36

```go
// These must keep working in iter-36:
errors.Is(err, AP.ErrNotFound)
errors.Is(err, AP.ErrTypeMismatch)
errors.Is(err, AP.ErrClosed)
err == AP.ErrNoRows                 // pointer comparison
AP.IsKind(err, AP.KindCorrupt)
AP.IsRetryable(err)
AP.IsFatal(err)
```

All 45 `errors.Is(err, ErrXxx)` sites and the 33 sentinel references in tests stay untouched
through iter-36.

### New tests for iter-36

```go
// Snapshot per Kind
func TestError_StringFormat(t *testing.T) { ... }

// Snapshot chain expansion
func TestError_FormatChain(t *testing.T) { ... }

// Classification per Kind
func TestClassify(t *testing.T) { ... }

// Code/SQLSTATE consistency
func TestCodeOf(t *testing.T) { ... }
func TestSQLStateOf(t *testing.T) { ... }

// driver.Error compatibility
func TestError_SQLStateMethod(t *testing.T) { ... }

// Sentinel shim compatibility
func TestLegacySentinel_StillUsable(t *testing.T) { ... }

// Emit hook
func TestEmit_Hook(t *testing.T) { ... }
```

## Risks and mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| `Error()` format change breaks log scrapers | Medium | Document format change in CHANGELOG; keep `%v` for compatibility |
| Sentinel rename breaks out-of-tree code | Low | Sentinels are internal; out-of-tree use not supported |
| `Classify` performance overhead | Low | Single `errors.As` call; no allocation if already `*Error` |
| `Fields` map allocation on every wrap | Medium | Lazy initialization; only allocate when first `WithField` called |
| Snapshot tests too brittle | Medium | Snapshot contains only Code/Kind/SQLSTATE/Message; Fields/Cause as separate snapshots |
| Driver wire format changes break Go users | Low | Wire format is `Error()` string; document; v1.0 freeze |

## Cross-references

- `docs/design/subsystems/SYS.md` line 130-213 — original design intent for `AP.Error` and `Kind` (human-owned; will be updated by human after iter-39 to reflect actual final shape).
- `docs/design/ARCH.md` — subsystem map and dependency rules.
- `AGENTS.md` lines starting with "REQUIREMENTS.md Maintenance" and "Iteration Lifecycle" — when iter-36 through iter-40 complete, REQ rows are deleted from `TBD` table.
- `docs/compose/reports/dependency-structure-principles.md` — sibling report on dependency structure.

## Pending human actions

The following changes to `docs/design/` are deferred to the human per Design Protection:

1. `docs/design/subsystems/SYS.md` §"Error Types" — update to match final `AP.Error` shape after iter-39.
2. `docs/design/subsystems/SYS.md` §"Cross-layer error contract" — update with new `Module` / `Layer` / `Code` / `SQLSTATE` fields.
3. `docs/design/ARCH.md` — no change required (error system is internal to SYS).
4. Add REQ rows for iter-36 through iter-40 to `docs/development/REQUIREMENTS.md` (per AGENTS.md Bug-To-Requirement Rule when bugs surface).

## Open issues (non-blocking)

These were considered but not frozen:

- **[OPEN-1]**: Should `Op` field be in `Fields["op"]` map or a top-level struct field? — Decision deferred to iter-38 implementation; recommend top-level for ergonomic access.
- **[OPEN-2]**: Should `Classification` be a method `(e *Error) Classify() Classification` in addition to package-level `Classify(err) Classification`? — Decision deferred; both can coexist.
- **[OPEN-3]**: JSON tag strategy on `AP.Error` — should `Module` be lowercase `"module"`? — Recommend snake_case for stable external contract; lowercase for internal-only fields.

These do not block iter-36.