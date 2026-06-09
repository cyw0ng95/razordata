# TBD Requirements Scan — 2026-06-09

Comprehensive scan of all TBD requirements (42 items) with
priority grouping, effort estimation, and implementation status
audit. Output of an audit pass after iter-19 (v0.16.0).

## TBD Distribution

| Priority | Count | Sum Effort |
|----------|-------|------------|
| Critical | 8 | 3L + 4M + 1XL |
| High | 7 | 2L + 5M + 0XL |
| Medium | 15 | 0L + 13M + 0XL (note REQ000182=L) |
| Low | 12 | 1L + 8M + 0XL (note REQ000045=XL) |
| **Total** | **42** | **~75 person-days** |

## Status Audit (Already Done But Still in TBD)

The following REQs have shipped work but the TBD row was
not removed. They should be moved to DONE.

| REQ | Description | Shipped In | Verification |
|-----|-------------|------------|--------------|
| REQ000158 | Hazard pointer PublishCurrent/PublishNext | iter-15 | `TXN/LC/hazard.go:27-37` shows split methods |
| REQ000172 | 6-phase graceful shutdown | iter-14 | `SYS/SY/shutdown.go` is 235 lines (vs 36-line stub) |
| REQ000176 | WAL BatchSync | iter-17 (v0.13.1) | `WAL/FL/fl.go:122-127` |
| REQ000184 | 256 KB writeBuffer | iter-17 (v0.13.1) | `WAL/FL/fl.go:58-69` |

## Critical REQs (8 items, ~25-30 days)

| REQ | Description | Effort | Dependencies | Recommendation |
|-----|-------------|--------|--------------|----------------|
| **REQ000174** | ENG/LS BloomFilter FNV-1a double-hash (replace CRC32) | M | iter-04 | **HIGH** — alignment with design, fix correctness gap |
| **REQ000147** | TXN/VL Complete commit protocol (6 phases) | L | iter-06 | **HIGH** — already partial, fill in Abort flow |
| **REQ000171** | TXN/VL WAL integration (write RTCommit/RTData) | L | iter-06 | **HIGH** — critical for durability |
| **REQ000172** | SYS/SY 6-phase shutdown | XL | iter-09 | **DONE** — should be moved to DONE |
| **REQ000113** | SQL GROUP BY | M | iter-08 | **HIGH** — essential SQL feature |
| **REQ000117** | SQL OUTER JOIN | M | iter-08 | **HIGH** — essential SQL feature |
| **REQ000061** | TXN Read-committed isolation | L | iter-05/06 | **MEDIUM** — production isolation level |
| **REQ000062** | TXN MVCC reads in tx | L | iter-09 | **MEDIUM** — needed for proper SQL semantics |

## High REQs (7 items, ~20-25 days)

| REQ | Description | Effort | Dependencies | Recommendation |
|-----|-------------|--------|--------------|----------------|
| **REQ000158** | TXN Hazard pointer protocol | S | iter-05 | **DONE** — move to DONE |
| **REQ000164** | TXN Epoch background goroutine | M | iter-05 | **HIGH** — needed for REQ000175 |
| **REQ000175** | TXN/LC Memory reclamation fix | L | iter-05 | **HIGH** — depends on REQ000164 |
| **REQ000126** | SQL Foreign keys | L | iter-11/12/21 | **LOW** — many dependencies |
| **REQ000102** | SYS Admin CLI `razor-admin` | M | iter-12/17 | **MEDIUM** — operational tool |
| **REQ000143** | QUAL SQL/RE coverage 49% → 80%+ | M | iter-07 | **MEDIUM** — coverage gap |
| **REQ000074** | SQL IndexScan real seek | M | iter-08/21 | **LOW** — depends on iter-21 |

## Recommended v0.17.0 Scope

**v0.17.0 — TXN Correctness + SQL Completeness (~2,000 LOC, 10-12 days)**

Rationale: iter-17/19 focused on performance (SIMD, parallel).
The next iteration should address correctness gaps in TXN/VL
(many critical REQs) and SQL completeness (GROUP BY, OUTER JOIN).

### Block A: TXN Commit Protocol Completion (Critical, ~700 LOC)

1. **REQ000147** — Complete commit protocol (6 phases, Abort flow)
2. **REQ000171** — WAL integration in commit (RTCommit/RTData records)
3. **REQ000164** — Epoch background goroutine (100ms interval)
4. **REQ000175** — Memory reclamation fix (depends on REQ000164)

### Block B: SQL Feature Completeness (Critical, ~800 LOC)

1. **REQ000113** — GROUP BY (single + multi col, with/without aggregates)
2. **REQ000117** — OUTER JOIN (LEFT/RIGHT/FULL)
3. **REQ000143** — SQL/RE coverage 49% → 80% (partial; revisit)

### Block C: Cleanup (Low, ~200 LOC)

1. **Move already-done REQs to DONE**: REQ000158, REQ000172,
   REQ000176, REQ000184
2. **REQ000174** — BloomFilter FNV-1a (M effort, ~150 LOC)

## Recommended v0.18.0 Scope (Future)

**v0.18.0 — Production Readiness (~2,500 LOC, 12-15 days)**

1. **REQ000102** — Admin CLI `razor-admin`
2. **REQ000061** — Read-committed isolation
3. **REQ000062** — MVCC reads in transactions
4. **REQ000084** — RE subquery planning
5. **REQ000148** — BloomFilter double-hashing (alternative to FNV-1a)
6. **REQ000161** — Clock-sweep integration audit
7. **REQ000165** — Compaction scheduling based on level size

## Recommended v0.19.0 Scope (Long-term)

**v0.19.0 — Operational Features (~3,000 LOC, 15-20 days)**

1. **REQ000100** — Network server (TCP/gRPC)
2. **REQ000101** — Prometheus metrics endpoint
3. **REQ000128** — Point-in-time backup/restore
4. **REQ000129** — Online schema migration
5. **REQ000045** — Secondary indexes (XL)
6. **REQ000123** — Configurable isolation levels

## Low-Priority / Defer

- REQ000034 (WAL compression) — defer to v0.20+
- REQ000047 (Prefix bloom) — defer (optional optimization)
- REQ000049/050 (Schema/Deparser split) — defer (refactor)
- REQ000064 (Generational arena) — defer (optimization)
- REQ000086 (Parallel query in EX) — already done in iter-19
- REQ000129 (Online schema migration) — XL, defer
- REQ000018 (File locking) — defer (single-process v1)
- REQ000126 (Foreign keys) — defer (many dependencies)

## Cross-Cutting Themes

1. **TXN Correctness (Block A)** — many critical REQs in TXN/VL
   have been pending since iter-06. Without these, the database
   lacks proper commit semantics, isolation, and durability.

2. **SQL Completeness (Block B)** — GROUP BY and OUTER JOIN are
   essential SQL features. Without them, the database cannot
   run a wide class of analytical queries.

3. **Documentation Hygiene** — 4 REQs (REQ000158, REQ000172,
   REQ000176, REQ000184) are duplicates of work already shipped.
   The TBD list has drifted from reality; needs cleanup.

4. **Performance Plateau** — iter-19 hit 10-30x on TPC-H for
   large data, but the calibration showed 5.7x SLOWER on 10K
   rows. The next opportunity is adaptive thresholds
   (REQ000192, from iter-19 Phase 3).

## Suggested Action

1. **Clean up TBD list** — move REQ000158, REQ000172, REQ000176,
   REQ000184 to DONE (one commit, low effort).
2. **Plan v0.17.0** — TXN correctness + SQL completeness.
   Target 10-12 days, 2,000 LOC.
3. **Defer v0.18.0/v0.19.0** — production readiness + operational
   features, 30+ days cumulative.

## v0.17.0 Implementation Plan (Preview)

### Block A: TXN Commit Protocol Completion

**Files:**
- `internal/TXN/VL/protocol.go` — 6 phases + Abort flow
- `internal/TXN/VL/wal.go` (new) — WAL record emission
- `internal/TXN/LC/epoch.go` — background goroutine
- `internal/TXN/LC/reclaim.go` — actual memory free

**Tests:**
- Commit/Abort symmetry
- WAL record types
- Epoch transitions
- Memory leak detection

### Block B: SQL Feature Completeness

**Files:**
- `internal/SQL/PS/ps.go` — GROUP BY syntax
- `internal/SQL/RE/rewrite.go` — GROUP BY pushdown
- `internal/SQL/EX/aggregate.go` — extend HashAggregate
- `internal/SQL/EX/join.go` — outer join variants
- `internal/SQL/PS/join.go` (new) — outer join syntax

**Tests:**
- GROUP BY single + multi col
- Outer join (LEFT, RIGHT, FULL)
- Coverage uplift to 80%+

### Block C: Cleanup

- Move 4 REQs to DONE
- BloomFilter FNV-1a migration
- Documentation update

Total: ~2,000 LOC, 10-12 days.
