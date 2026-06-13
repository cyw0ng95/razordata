# Iteration 26.4 — v0.26.6 Compound SELECT (UNION/INTERSECT/EXCEPT)

**Subsystem:** `SQL/LX`, `SQL/PS`, `SQL/EX`, `SQL/RE`, `tests/sqlcmp/dual`

**Status:** done

**Est. LOC:** ~300 (1 REQ: REQ000383)

**Target release:** v0.26.6

**Tags:** `feature`, `parser`, `executor`, `sql-compliance`

---

## Overview

This iteration implements compound SELECT statements with
UNION, INTERSECT, and EXCEPT operators (REQ000383). The work
covers lexer tokens, parser grammar (with correct INTERSECT >
UNION/EXCEPT precedence), executor semantics (dedup for
UNION/INTERSECT/EXCEPT, no-dedup for UNION ALL), and RE layer
support for rewrite/format.

**Key design decision:** Trailing ORDER BY/LIMIT/OFFSET clauses
apply to the entire compound result, not individual leaf SELECTs.
This matches SQLite semantics and required a three-way split of
the parser:

- `parseOneSelect` — parses a single SELECT (no trailing clauses)
- `parseIntersectChain` — chains INTERSECT operators (higher precedence)
- `parseSelect` — chains UNION/EXCEPT (lower precedence) and
  always parses trailing clauses

---

## Goals

1. Close REQ000383 — implement UNION, INTERSECT, EXCEPT with
   correct precedence (INTERSECT binds tighter).
2. Support ALL suffix on UNION (UNION ALL preserves duplicates).
3. Reject EXCEPT ALL syntax (not in SQLite standard).
4. Trailing ORDER BY/LIMIT/OFFSET apply to compound result.
5. Add dual-runner probe coverage for all compound operators.
6. Implement RE.Rewrite() and RE.Format() for CompoundStmt.

## Non-Goals

- Core function matrix REQ000384-417 (separate iteration).
- Performance optimization (no profiling gates needed for MVP).
- REW layer persistence (RE is sufficient for v1).

---

## REQ-by-REQ Notes

### REQ000383 — Compound SELECT: UNION, INTERSECT, EXCEPT

**Symptom:** `SELECT x FROM a UNION SELECT x FROM b` returned
`ps: syntax error at line 1 col X: expected expression, got UNION`.

**Root cause:** Parser grammar only supported single SELECT
statements; no CompoundStmt AST node, no set operator tokens.

**Fix — Lexer (`SQL/LX`):**

Added four new token types in `token.go`:
- `T_UNION`, `T_INTERSECT`, `T_EXCEPT`, `T_ALL`

And keyword entries in `lx.go`:
```go
"T_UNION":    T_UNION,    "T_INTERSECT": T_INTERSECT,
"T_EXCEPT":   T_EXCEPT,   "T_ALL":       T_ALL,
```

**Fix — AST (`SQL/PS/ast.go`):**

```go
type CompoundOp uint8
const (
    CompoundUnion CompoundOp = iota
    CompoundUnionAll
    CompoundIntersect
    CompoundExcept
)

type CompoundStmt struct {
    Left   Stmt
    Op     CompoundOp
    Right  Stmt
    OrderBy []OrderItem
    Limit  Expr
    Offset Expr
}
```

**Fix — Parser (`SQL/PS/ps.go`):**

Three-way split for correct precedence:

1. `parseSelect()` — chains UNION/EXCEPT (left-associative),
   calls `parseIntersectChain()` for leaf operands, always
   parses trailing ORDER BY/LIMIT/OFFSET.

2. `parseIntersectChain()` — chains INTERSECT (left-associative),
   calls `parseOneSelect()` for operands, never parses trailing
   clauses (they belong to the outer caller).

3. `parseOneSelect()` — parses a single SELECT without trailing
   clauses (stops at HAVING), returns `*Select`.

Trailing clause parsing is consolidated:
```go
ob, lim, off, err := p.parseTrailingClauses()
// Attach to either *CompoundStmt or *Select
```

EXCEPT ALL rejection:
```go
if p.current.Type == LX.T_ALL {
    if op == CompoundExcept {
        return nil, &SyntaxError{Expected: "EXCEPT (no ALL suffix)"}
    }
    op = CompoundUnionAll
}
```

**Fix — Executor (`SQL/EX/compound.go` — new file):**

```go
func NewCompoundOp(ctx context.Context, stmt *PS.CompoundStmt) (*CompoundOpExecutor, error)
```

Three set operator implementations:

1. **UNION / UNION ALL:**
   - `drainAll()` left and right to `[][]value.Value`
   - UNION: `dedupRows()` using `distinctKey(row)` (same as Distinct op)
   - UNION ALL: concatenate without dedup

2. **INTERSECT:**
   - Dedup both sides
   - Build map of left rows, probe with right
   - Return intersection

3. **EXCEPT:**
   - Dedup both sides
   - Build map of right rows, filter left against it
   - Return left minus right

All operators attach ORDER BY/LIMIT/OFFSET from the CompoundStmt
after applying the set operation.

**Fix — Planner (`SQL/EX/planner.go`):**

New `planCompound()` dispatcher:
```go
case *PS.CompoundStmt:
    return planCompound(ctx, s, db)
```

`planSubStmt()` routes both `*Select` and `*CompoundStmt`
(either can appear in subquery or parenthesized context).

**Fix — RE (`SQL/RE/rewrite.go`, `SQL/RE/format.go`):**

- `rewriteCompound(stmt *PS.CompoundStmt, ...)` — recursively
  rewrites left/right children, preserves OrderBy/Limit/Offset
- `formatCompound(w io.Writer, stmt *PS.CompoundStmt, ...)` —
  formats with correct operator tokens and parentheses

**Probes:** 6 new dual-runner cases in `probe_cases.go`:
- `union_dedup` — verifies UNION removes duplicates
- `union_all_no_dedup` — verifies UNION ALL keeps duplicates
- `intersect` — set intersection
- `except` — set difference
- `union_empty_left` — empty left operand
- `union_with_limit` — LIMIT applies to compound result

**Touches:**
- `internal/SQL/LX/token.go` — 4 new tokens
- `internal/SQL/LX/lx.go` — keyword entries
- `internal/SQL/PS/ast.go` — CompoundOp, CompoundStmt
- `internal/SQL/PS/ps.go` — parseSelect, parseIntersectChain,
  parseOneSelect, parseTrailingClauses, parseIntersect fix
- `internal/SQL/EX/compound.go` — new file (~200 LoC)
- `internal/SQL/EX/planner.go` — planCompound, planSubStmt
- `internal/SQL/RE/rewrite.go` — rewriteCompound
- `internal/SQL/RE/format.go` — formatCompound
- `tests/sqlcmp/dual/probe_cases.go` — 6 new probes

---

## Files Changed

- `internal/SQL/LX/token.go` — T_UNION, T_INTERSECT, T_EXCEPT, T_ALL
- `internal/SQL/LX/lx.go` — keyword mapping
- `internal/SQL/PS/ast.go` — CompoundOp enum, CompoundStmt struct
- `internal/SQL/PS/ps.go` — three-way parse split + trailing clause handling
- `internal/SQL/EX/compound.go` — new executor (~200 LoC)
- `internal/SQL/EX/planner.go` — planCompound dispatcher
- `internal/SQL/RE/rewrite.go` — rewriteCompound
- `internal/SQL/RE/format.go` — formatCompound
- `tests/sqlcmp/dual/probe_cases.go` — 6 compound SELECT probes
- `docs/development/REQUIREMENTS.md` — REQ000383 TBD → DONE
- `docs/development/ROADMAP.md` — v0.26.6 row
- `docs/development/iterations/iter-26.4-compound-select.md` — this file

## Test Results

- `go test ./... -race -count=1` green (3× full suite).
- Dual-runner: 67 passed, 0 failed, 0 skipped (was 65/2/0 before
  UNION+LIMIT fix).
- `go vet ./...` zero warnings.
- `gofmt -s -l .` clean.

**Bug fixed during iteration:** Initial implementation had
`parseOneSelect` consume trailing ORDER BY/LIMIT/OFFSET, causing
the right-side SELECT in a UNION to incorrectly attach these
clauses. Fix: `parseOneSelect` never parses trailing clauses;
`parseSelect` always does.

---

## Commit Sequence

1. `42a9b7c` — `feat(SQL/LX/PS/EX/RE): REQ000383 compound SELECT
   UNION/INTERSECT/EXCEPT parser + executor`
2. `8f3d7e1` — `fix(SQL/PS): trailing ORDER BY/LIMIT/OFFSET apply
   to compound chain, not leaf SELECTs`
3. (this commit) — `docs(REQ): close REQ000383, add v0.26.6 to
   ROADMAP, iteration doc`

---

## Gap Analysis

No deferred bugs. The UNION+LIMIT issue was discovered and fixed
within the same iteration (before tag).

---

## Outcome

1 REQ closed (REQ000383) in 3 commits, ~300 LoC added.
Compound SELECT MVP complete with correct precedence semantics
and full dual-runner probe coverage. The next iteration will
tackle the core function matrix (REQ000384-417, currently 19
remaining TODO functions).

**Final tag:** `v0.26.6`
**Final commit:** (this iteration's docs commit)
**Final LoC delta:** 10 files changed, 412 insertions(+), 34 deletions(-)
