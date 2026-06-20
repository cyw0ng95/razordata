# Iteration 29 — Full Sweep: Style, Modernization, Performance, PRAGMA, CLI/TUI (target v0.29.0)

Status: **planned**.

## Scope

All 43 remaining REQs from `REQUIREMENTS.md` TBD table. No postponing — every REQ must be implemented and committed individually.

| Category | Count | Effort |
|----------|------:|--------|
| Critical | 4 | 4×M |
| High | 15 | 9×M, 3×S, 2×L, 1×XL |
| Medium | 18 | 10×M, 8×S |
| Low | 16 | 16×S |
| **Total** | **43** | |

## Execution Order

REQs are ordered by dependency and priority. Each REQ gets its own commit. No batching.

### Phase 1 — Foundation (no deps, unlock downstream)

| # | REQ | Subsystem | Effort | Description |
|---|-----|-----------|--------|-------------|
| 1 | REQ000644 | SQL/PS | S | Doc comments for AST types |
| 2 | REQ000645 | TXN/MV | S | Doc comments for MV types |
| 3 | REQ000646 | ENG/LS | S | Doc comments for LS types |
| 4 | REQ000649 | ENG/LS | S | Table-driven tests for skiplist |
| 5 | REQ000665 | SYS/AP+ENG/LS | S | Centralize default constants |
| 6 | REQ000671 | ENG/DP | S | Pre-allocate buffers in EncodeBlock/DecodeBlock |
| 7 | REQ000672 | WAL/WR | S | Pool LZ4 hash table |
| 8 | REQ000673 | FIL/DF | S | Remove unnecessary buffer zeroing |
| 9 | REQ000674 | FIL/MF | S | Eliminate double-encoding in writeMeta |
| 10 | REQ000675 | FIL/LF | S | Remove TOCTOU os.Stat before unix.Open |
| 11 | REQ000676 | WAL/RP | S | Eliminate double file open in replay |
| 12 | REQ000677 | ENG/SC | S | Remove stateless Validator type |
| 13 | REQ000678 | ENG/DP | S | Unify Pair and KV types |
| 14 | REQ000679 | ENG/SC | S | Combine redundant CTInt/CTBigInt branches |
| 15 | REQ000680 | ENG/LS | S | Pre-allocate memtables slice |
| 16 | REQ000681 | ENG/SC | S | Zero-copy EncodeVarchar/EncodeText |
| 17 | REQ000696 | ENG/LS | S | Pool sstWriter instances |
| 18 | REQ000697 | ENG/LS | S | Reorder CatalogEntry struct fields |
| 19 | REQ000698 | ENG/LS | S | Pre-allocate bytes.Buffer in compression |

### Phase 2 — Modernization (stdlib upgrades)

| # | REQ | Subsystem | Effort | Description |
|---|-----|-----------|--------|-------------|
| 20 | REQ000689 | ALL | M | Adopt slices/maps/cmp packages |
| 21 | REQ000690 | ENG/LS | S | Replace binary.Write in manifest |
| 22 | REQ000691 | ALL | M | Migrate math/rand to math/rand/v2 |
| 23 | REQ000692 | ENG/LS+SQL/EX | S | Use errgroup for parallel operations |
| 24 | REQ000693 | ALL | M | for range N syntax migration |
| 25 | REQ000694 | ENG/LS+SQL/EX+FIL/LF | M | Replace fmt.Sprintf in hot paths |
| 26 | REQ000695 | ALL | M | Typed atomics migration |

### Phase 3 — Performance (hot path optimizations)

| # | REQ | Subsystem | Effort | Description |
|---|-----|-----------|--------|-------------|
| 27 | REQ000706 | ENG/LS | S | Fix primary index key comparison allocations |
| 28 | REQ000707 | SQL/EX | S | Replace invertSelection map with bitmap |
| 29 | REQ000708 | ENG/LS | M | Process FNV-1a in 8-byte chunks |
| 30 | REQ000709 | ENG/LS | M | Make bloom size power-of-2 |
| 31 | REQ000710 | ENG/LS | S | Merge bloom construction into single pass |
| 32 | REQ000711 | WAL/WR | M | Zero-copy columnar decode |
| 33 | REQ000712 | ENG/LS | S | Skip list node field reordering |
| 34 | REQ000705 | SQL/EX | M | Unroll vectorized comparison kernels |

### Phase 4 — Catalog & API

| # | REQ | Subsystem | Effort | Description |
|---|-----|-----------|--------|-------------|
| 35 | REQ000660 | ENG/LS+TB | L | Extract shared catalog to ENG/catalog |
| 36 | REQ000664 | SYS/AP | M | Embed subsystem stats types |
| 37 | REQ000683 | FIL/DF+MF+LF | M | Disk full graceful handling |

### Phase 5 — SQL Features

| # | REQ | Subsystem | Effort | Description |
|---|-----|-----------|--------|-------------|
| 38 | REQ000684 | SQL/EX | M | Multi-column hash join keys |
| 39 | REQ000685 | SQL/EX+PL | L | LEFT OUTER JOIN |
| 40 | REQ000686 | SQL/EX | L | RANGE window frame spec |

### Phase 6 — PRAGMA Support

| # | REQ | Subsystem | Effort | Description |
|---|-----|-----------|--------|-------------|
| 41 | REQ000729 | SQL/EX | M | PRAGMA table_info |
| 42 | REQ000730 | SQL/EX | M | PRAGMA foreign_keys |
| 43 | REQ000731 | SQL/EX | M | PRAGMA index_list + index_info |
| 44 | REQ000732 | SQL/EX | S | PRAGMA table_list |
| 45 | REQ000733 | SQL/EX | M | PRAGMA foreign_key_check + foreign_key_list |
| 46 | REQ000734 | SQL/EX | M | Wire PRAGMA values to subsystems |
| 47 | REQ000735 | SQL/EX | M | PRAGMA wal_checkpoint + wal_autocheckpoint |

### Phase 7 — SLT & Testing

| # | REQ | Subsystem | Effort | Description |
|---|-----|-----------|--------|-------------|
| 48 | REQ000700 | tests/slt | S | Fix primary key classifier pattern |
| 49 | REQ000701 | tests/slt | M | SLT framework unit tests |
| 50 | REQ000702 | tests/slt | M | Expand corpus subset |
| 51 | REQ000703 | tests/slt | S | Blob type code support |

### Phase 8 — CLI/TUI Foundation

| # | REQ | Subsystem | Effort | Description |
|---|-----|-----------|--------|-------------|
| 52 | REQ000727 | SQL/EX | M | sqlite_master virtual table |
| 53 | REQ000687 | SYS/SY+AP | S | Configurable shutdown timeout |
| 54 | REQ000688 | SYS/SY | S | Emergency shutdown mode |

### Phase 9 — XL Features

| # | REQ | Subsystem | Effort | Description |
|---|-----|-----------|--------|-------------|
| 55 | REQ000543 | SQL/EX | XL | Shape-specialized fast paths |
| 56 | REQ000546 | SQL/EX | L | Vectorized string columns |
| 57 | REQ000549 | MEM/BF | XL | Block-level MVCC version tagging |

## Commit Convention

Each REQ gets its own commit with the format:
```
<type>(<subsystem>): REQ<id> <short description>

<optional body with details>
```

Types: `feat`, `refactor`, `perf`, `test`, `docs`, `fix`

## Verification

After each commit:
```bash
go vet ./...
go test ./... -race -count=1
```

## Outcome (to be filled)

- REQs shipped: 0/43
- Actual LoC:
- Deviations:
