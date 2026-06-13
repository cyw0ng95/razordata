# Iteration 26.6 — Core Scalar Functions Batch 2 (v0.26.8)

## Goal

Complete remaining 7 core scalar functions from REQ000384-433 bundle.

## Scope

### New Functions (7 REQs)

| REQ | Function | Effort | Notes |
|---|---|---|---|
| REQ000390 | `glob(X,Y)` | S | SQL glob pattern match (`*`, `?`, `[...]`) |
| REQ000395 | `likelihood(X,Y)` | S | No-op pass-through, planner hint |
| REQ000396 | `likely(X)` | S | No-op pass-through, planner hint |
| REQ000408 | `soundex(X)` | S | Soundex encoding (4-char code) |
| REQ000413 | `unhex(X[,Y])` | S | Hex string → BLOB |
| REQ000415 | `unistr(X)` | S | Backslash-escape decoder (`\uXXXX`) |
| REQ000416 | `unlikely(X)` | S | No-op pass-through, planner hint |

### Already Implemented (iter-26)

- REQ000391: `hex(X)` — done in iter-26
- REQ000405: `round(X,Y)` — done in iter-26
- REQ000406: `rtrim(X,Y)` — done in iter-26.7

**Total**: 7 new functions, ~300-400 LoC.

## Plan

1. `glob(X,Y)` — pattern matching with `*`, `?`, `[...]`
2. `soundex(X)` — 4-char phonetic encoding
3. `unhex(X,Y)` — hex string to BLOB
4. `unistr(X)` — escape sequence decoder
5. No-op hints: `likelihood`, `likely`, `unlikely`
6. Update probe_cases.go (+7 tests)
7. Unit tests in corefunc_quick_test.go
8. Docs & tag v0.26.8

## Acceptance

- 97/97 dual-runner tests pass
- go test ./... -race -count=1 passes
- REQ000390/395/396/408/413/415/416 marked DONE
- Core function bundle: 48/51 (94%)


---

## Outcome

7 REQs closed in ~6 commits, ~500 LoC. Core function bundle: 48/51 (94%).
Only 3 functions remain (round, rtrim, hex — already implemented in iter-26/26.7 but TBD table not cleaned).

**Final tag:** `v0.26.8`
**Dual-runner:** 100/100/0

