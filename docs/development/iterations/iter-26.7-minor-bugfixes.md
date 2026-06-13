# Iterations 26.7-26.15 — Minor Bugfixes & Small Features (v0.26.9 → v0.26.17)

## Overview

Nine minor iterations, each ~500 LoC, focused on bug fixes, small feature
additions, and infrastructure hardening. Each releases as a separate tag.

---

## v0.26.9 (iter-26.7) — Lexer/parser small additions

**Theme:** Add missing operators and special forms.

| REQ | Description | LoC |
|---|---|---|
| REQ000350 | Bitwise operators (`&`, `\|`, `^`, `~`) | ~200 |
| REQ000351 | String concat operator (`\|\|`) | ~100 |
| REQ000352 | Modulo operator (`%`) | ~80 |
| REQ000353 | COALESCE as special form | ~60 |
| REQ000354 | NULLIF as special form | ~60 |

**Total: 5 REQs, ~500 LoC**

---

## v0.26.10 (iter-26.8) — Bug sweep v4

**Theme:** Fix bugs surfaced in iter-25.

| REQ | Description | LoC |
|---|---|---|
| REQ000348 | `Session.Query` streaming (add `Next()` accessor) | ~300 |
| REQ000356 | Unary `NOT` as logical operator | ~80 |
| REQ000292 | numericArith int64 overflow check | ~50 |
| REQ000286 | Window materialize context propagation | ~50 |

**Total: 4 REQs, ~480 LoC**

---

## v0.26.11 (iter-26.9) — Logger infrastructure v2

**Theme:** Observability improvements.

| REQ | Description | LoC |
|---|---|---|
| REQ000322 | eBPF runtime tracing export | ~400 |
| REQ000161 | Clock-sweep integration audit | ~50 |
| REQ000101 | Prometheus metrics endpoint (stub) | ~50 |

**Total: 3 REQs, ~500 LoC**

---

## v0.26.12 (iter-26.10) — WAL batch sync

**Theme:** WAL write-path improvements.

| REQ | Description | LoC |
|---|---|---|
| REQ000160 | Batch commit with `sync.WaitGroup` | ~250 |
| REQ000301 | Async fsync + io_uring linked submit | ~200 |
| REQ000299 | WAL columnar encoding (key delta-of-delta) | ~100 |

**Total: 3 REQs, ~550 LoC** (slightly over)

---

## v0.26.13 (iter-26.11) — Compaction scheduling

**Theme:** Compaction subsystem improvements.

| REQ | Description | LoC |
|---|---|---|
| REQ000165 | Compaction job scheduling (level size budget) | ~200 |
| REQ000318 | Write-rate-limited compactor (token bucket) | ~150 |
| REQ000319 | Sub-compaction parallelism | ~150 |

**Total: 3 REQs, ~500 LoC**

---

## v0.26.14 (iter-26.12) — Buffer pool improvements

**Theme:** MEM subsystem hardening.

| REQ | Description | LoC |
|---|---|---|
| REQ000303 | W-TinyLFU admission + SLRU (replace clock-sweep) | ~300 |
| REQ000302 | PMem-aware buffer pool (DRAM hot + mmap'd cold) | ~150 |
| REQ000309 | NUMA-aware data placement (skeleton) | ~50 |

**Total: 3 REQs, ~500 LoC**

---

## v0.26.15 (iter-26.13) — TXN epoch & hazard

**Theme:** Lock-free TXN subsystem hardening.

| REQ | Description | LoC |
|---|---|---|
| REQ000164 | Epoch manager background goroutine (100ms tick) | ~200 |
| REQ000181 | Goroutine ID tracking (proper identity) | ~150 |
| REQ000175 | Hazard pointer Publish fix + Reclaim impl | ~150 |

**Total: 3 REQs, ~500 LoC**

---

## v0.26.16 (iter-26.14) — Index BTree fixes

**Theme:** ENG/ID subsystem hardening.

| REQ | Description | LoC |
|---|---|---|
| REQ000284 | BTree delete rebalancing (merge/redistribute) | ~300 |
| REQ000285 | uint32 page ID overflow protection | ~100 |
| REQ000287 | Window setOutput allocation optimization | ~50 |
| REQ000074 | `IndexScan` real seek (replace prefix-scan) | ~50 |

**Total: 4 REQs, ~500 LoC**

---

## v0.26.17 (iter-26.15) — Misc SQL features

**Theme:** Small SQL feature additions and bug fixes.

| REQ | Description | LoC |
|---|---|---|
| REQ000320 | Configurable compaction style (leveled/tiered) | ~150 |
| REQ000256 | Parse VACUUM / ANALYZE (already done in iter-23, but parser cleanup) | ~80 |
| REQ000350 (refactor) | Bitwise op precedence edge cases | ~100 |
| REQ000018 | File locking (flock) for multi-process access | ~80 |
| REQ000349 (cleanup) | Typed "unsupported" error for missing builtins | ~80 |

**Total: 5 REQs, ~490 LoC**

---

## Combined Outcomes

- **9 releases** (v0.26.9 → v0.26.17)
- **33 unique REQs** closed
- **~4500 LoC** total
- **9 tags** pushed
- **18 commits** average per release (2 per iter)

## Acceptance

Each iteration must:
- All 32 packages pass `go test ./... -race -count=1`
- Dual-runner: maintain 100% pass rate
- go vet + gofmt clean
- Single commit per major code change
- Update REQUIREMENTS.md TBD→DONE
- Update ROADMAP.md release tag

