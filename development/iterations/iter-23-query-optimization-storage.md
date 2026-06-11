# Iteration 23 — Query Optimization & Storage Enhancement (v0.20.0 → v0.22.0)

**Subsystem:** `SQL/EX`, `SQL/PL`, `SQL/PS`, `ENG/LS`, `ENG/ID`, `SYS`, `WAL/WR`, `WAL/RP`

**Status:** pending

**Est. LOC:** ~12,000

**Requirements:** REQ000258, REQ000085, REQ000261, REQ000272, REQ000257, REQ000259, REQ000260, REQ000236, REQ000237, REQ000262, REQ000263, REQ000264, REQ000265, REQ000250, REQ000252, REQ000047, REQ000271

**Target releases:** v0.20.0 (stats + integrity), v0.21.0 (window + datetime + JSON), v0.22.0 (B-tree index)

---

## Overview

This iteration delivers three major capability clusters:

1. **Query Optimization & Data Integrity (v0.20.0)** — ANALYZE executor, histogram-based selectivity, integrity check, VACUUM, backup/restore, WAL checksum verification
2. **SQL Expression Extensions (v0.21.0)** — Window functions, DATE/TIME/TIMESTAMP types, JSON type with operators
3. **Storage Performance Upgrade (v0.22.0)** — B-tree secondary index package (full implementation), SST prefix bloom filters, block compression

**Key difference from prior iterations:** No LoC limit. Each requirement must be fully implemented and tested before marking complete. No partial shipments.

---

## Cluster A: Statistics & Integrity (v0.20.0) — ~4,000 LOC

### REQ000258: ANALYZE Executor (~800 LOC)

**Files:** `SQL/EX/analyze.go` (new), `SQL/PS/ps.go` (parser extension), `ENG/LS/stats.go` (persistence)

**Implementation:**
```go
// ANALYZE <table_name>
type AnalyzeOperator struct {
    tableID   uint64
    columns   []uint64
    sampleSize int // default 10000
}

func (op *AnalyzeOperator) Next(ctx context.Context) (Row, error) {
    // 1. Full table scan with reservoir sampling
    // 2. Build histogram buckets per column
    // 3. Persist to catalog (ENG/LS/catalog.go)
    // 4. Return single row: "Analyzed X rows in Y columns"
}
```

**Histogram structure:**
```go
type Histogram struct {
    Buckets []Bucket
    NumDistinct int64
    NumNulls    int64
}

type Bucket struct {
    UpperBound []byte  // encoded value
    Count      int64   // rows <= upper_bound
    EqCount    int64   // rows == upper_bound
}
```

**Tests:**
- `TestAnalyze_SingleColumn` — integer column, uniform distribution
- `TestAnalyze_MultiColumn` — analyze all columns
- `TestAnalyze_Persistence` — stats survive Close/Open
- `TestAnalyze_LargeTable` — 1M rows, verify sampling accuracy

---

### REQ000085: Histogram-Based Selectivity (~600 LOC)

**Files:** `SQL/PL/estimateCost.go` (rewrite), `SQL/PL/selectivity.go` (new)

**Before:** Uniform distribution assumption (selectivity = 1/NDV)

**After:**
```go
func estimateSelectivity(stats *ColumnStats, op Operator, val []byte) float64 {
    switch op.(type) {
    case EQ:
        return histogramEQ(stats.Histogram, val)
    case LT, GT, LTE, GTE:
        return histogramRange(stats.Histogram, op, val)
    case LIKE:
        // fallback to uniform
        return 0.1
    }
}
```

**Impact:** `EXPLAIN` shows accurate `rows=` estimates after `ANALYZE`.

**Tests:**
- `TestSelectivity_EQ` — point query accuracy
- `TestSelectivity_Range` — range query integration
- `TestPlanner_UsesHistogram` — verify planner picks correct index after ANALYZE

---

### REQ000261: PRAGMA integrity_check (~1,200 LOC)

**Files:** `SQL/EX/integrity.go` (new), `SQL/PS/ps.go` (parser), `SYS/SY/sy.go` (pragma dispatch)

**Syntax:** `PRAGMA integrity_check` or `PRAGMA integrity_check(<table_name>)`

**Checks:**
1. SST file checksums (block-level CRC32)
2. LSM manifest consistency (no gaps in L0, sorted levels)
3. Index-table consistency (every index entry points to valid row)
4. Catalog checksums
5. WAL segment header validity

**Return:** Table with columns `(table, page, error)`. Empty = OK.

**Tests:**
- `TestIntegrity_CorruptSST` — inject bad checksum, verify detection
- `TestIntegrity_MissingIndexEntry` — delete index entry, detect orphan
- `TestIntegrity_FullScan` — all checks on clean DB returns empty

---

### REQ000272: WAL Checksum Verification (~400 LOC)

**Files:** `WAL/RP/rp.go` (replayer), `WAL/WR/wr.go` (writer enhancement)

**Current:** CRC32 computed but not verified on replay

**Change:**
```go
func (r *Replayer) Replay(dir string, apply func(Record) error) error {
    for each segment {
        for each record {
            if !crc32Verify(record) {
                return ErrCorrupt{Segment: seg, Offset: off}
            }
            apply(record)
        }
    }
}
```

**Tests:**
- `TestReplay_VerifyChecksum` — corrupt bit, detect on replay
- `TestReplay_TornWrite` — partial record at segment end, skip gracefully

---

### REQ000257: VACUUM Executor (~1,000 LOC)

**Files:** `SQL/EX/vacuum.go` (new), `SQL/PS/ps.go` (parser: `VACUUM [table]`)

**Algorithm:**
1. Acquire schema lock (block writes)
2. For each table:
   - Full scan with `Iterator`
   - Write to new SST (compact tombstones)
   - Replace old SST atomically
3. For each index:
   - Rebuild from table scan
4. Release schema lock

**Option:** `VACUUM FULL` — rewrite all SSTs even if no tombstones

**Tests:**
- `TestVacuum_ReclaimSpace` — INSERT + DELETE, verify SST size reduction
- `TestVacuum_IndexRebuild` — verify indexes after vacuum
- `TestVacuum_Concurrent` — concurrent SELECT during vacuum (should not block)

---

### REQ000259: Backup/Restore API (~800 LOC)

**Files:** `SYS/BK/bk.go` (new), `SYS/AP/ap.go` (public API exposure)

**Backup:**
```go
func Backup(ctx context.Context, srcDir, dstDir string, options BackupOptions) error
// 1. Acquire read lock (block writes)
// 2. Copy all files (engine directory)
// 3. Copy WAL with LSN marker
// 4. Release lock
```

**Restore:**
```go
func Restore(ctx context.Context, backupDir, restoreDir string) error
// 1. Verify backup integrity
// 2. Copy to restore directory
// 3. Reset WAL checkpoint
```

**Tests:**
- `TestBackup_Consistent` — backup during reads, restore to new dir, verify data
- `TestBackup_Large` — 10GB DB, verify completion within timeout
- `TestRestore_Overwrite` — restore to existing dir (error)

---

### REQ000260: Admin CLI razor (~600 LOC)

**Files:** `cmd/razor/main.go` (new)

**Commands:**
```bash
razor integrity-check /path/to/db
razor vacuum /path/to/db
razor analyze /path/to/db
razor backup /path/to/db /backup/path
razor restore /backup/path /restore/path
razor schema-dump /path/to/db
```

**Tests:**
- CLI integration tests (spawn process, verify stdout)

---

## Cluster B: SQL Expression Extensions (v0.21.0) — ~5,000 LOC

### REQ000236: Window Function Parsing (~800 LOC)

**Files:** `SQL/PS/ps.go` (extension), `SQL/PS/ast.go` (new AST nodes)

**Syntax:**
```sql
SELECT ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC)
SELECT SUM(sales) OVER (ROWS BETWEEN 3 PRECEDING AND CURRENT ROW)
```

**AST:**
```go
type WindowSpec struct {
    PartitionBy []Expr
    OrderBy     []OrderByExpr
    Frame       *WindowFrame  // ROWS or RANGE, bounds
}

type WindowFunc struct {
    Name string  // ROW_NUMBER, RANK, SUM, AVG, etc.
    Args []Expr
    Over *WindowSpec
}
```

**Tokens:** `T_OVER`, `T_PARTITION`, `T_BY`, `T_ROWS`, `T_RANGE`, `T_PRECEDING`, `T_FOLLOWING`, `T_CURRENT`

**Tests:**
- `TestParse_WindowFunc` — all window function types
- `TestParse_FrameSpec` — ROWS/RANGE bounds

---

### REQ000237: Window Function Executor (~2,000 LOC)

**Files:** `SQL/EX/window.go` (new), `SQL/EX/ex.go` (integration)

**Operators:**
- `ROW_NUMBER()` — sequential numbering per partition
- `RANK()` — ranking with gaps for ties
- `DENSE_RANK()` — ranking without gaps
- `SUM() OVER`, `AVG() OVER`, `COUNT() OVER` — aggregate window functions
- `LAG()`, `LEAD()` — access previous/next row

**Execution strategy:**
1. Materialize partition (buffer all rows for current partition key)
2. Sort by ORDER BY within partition
3. Compute window function per row
4. Spillover to disk if partition > memory limit

**Frame handling:**
```go
type WindowFrame struct {
    Type      FrameType  // ROWS or RANGE
    Start     FrameBound // UNBOUNDED PRECEDING, n PRECEDING, CURRENT ROW
    End       FrameBound // CURRENT ROW, n FOLLOWING, UNBOUNDED FOLLOWING
}
```

**Tests:**
- `TestWindow_RowNumber` — basic numbering
- `TestWindow_Rank` — ties handling
- `TestWindow_Aggregate` — SUM/AVG over window
- `TestWindow_Frame` — ROWS BETWEEN correctness
- `TestWindow_LargePartition` — spillover to disk

---

### REQ000262 + REQ000263: DATE/TIME/TIMESTAMP Types (~1,000 LOC)

**Files:** `SQL/LX/token.go` (tokens), `SQL/PS/ps.go` (literals), `SQL/EX/datetime.go` (new), `SQL/EX/eval.go` (arithmetic)

**Type tokens:** `T_DATE`, `T_TIME`, `T_TIMESTAMP`

**Storage:** `time.Time` → Unix epoch (int64) + timezone offset

**Operators:**
- Comparison: `date_col > '2024-01-01'`
- Arithmetic: `date_col + INTERVAL '7' DAY`
- Extraction: `EXTRACT(YEAR FROM date_col)`

**Functions:** `strftime()`, `julianday()`, `date()`, `time()`, `datetime()`

**Tests:**
- `TestDateTime_Parse` — literal parsing
- `TestDateTime_Compare` — comparison operators
- `TestDateTime_Arithmetic` — INTERVAL operations

---

### REQ000264 + REQ000265: JSON Type & Operators (~1,200 LOC)

**Files:** `SQL/LX/token.go` (tokens), `SQL/PS/ps.go` (operators), `SQL/EX/json.go` (new)

**Type token:** `T_JSON`

**Operators:**
- `->`  — extract JSON object field as JSON
- `->>` — extract JSON object field as text

**Functions:**
- `json_extract(json, path)`
- `json_type(json)`
- `json_valid(json)`
- `json_array(...)`, `json_object(...)`

**Storage:** UTF-8 encoded `[]byte`, validated on INSERT

**Tests:**
- `TestJSON_OperatorArrow` — `->` returns JSON
- `TestJSON_OperatorDoubleArrow` — `->>` returns text
- `TestJSON_Extract` — path expressions

---

## Cluster C: Storage Performance Upgrade (v0.22.0) — ~6,000 LOC

### REQ000250: B-tree Secondary Index Package (~3,500 LOC)

**Files:** `ENG/ID/id.go` (new), `ENG/ID/page.go` (page management), `ENG/ID/cursor.go` (seek/next)

**B-tree structure:**
- Page size: 4 KB (power of 2)
- Node types: Internal (keys + child pointers), Leaf (key + value)
- Order: ~250 keys per internal node (4KB / 16-byte entry)
- Height: log₂₅₀(100M) ≈ 3-4 levels for 100M entries

**API:**
```go
type BTree struct {
    root    PageID
    order   int
    cache   *PageCache  // LRU cache of pages
}

func (bt *BTree) Seek(key []byte) (value []byte, found bool, err error)
func (bt *BTree) Insert(key, value []byte) error
func (bt *BTree) Delete(key []byte) error
func (bt *BTree) Cursor() *Cursor
```

**Cursor:**
```go
type Cursor struct {
    path    []PageID  // root to leaf
    index   int       // position in leaf
}

func (c *Cursor) Seek(key []byte) bool
func (c *Cursor) Next() bool
func (c *Cursor) Key() []byte
func (c *Cursor) Value() []byte
```

**Concurrency:**
- Tree-level RWMutex for structural modifications
- Page-level latches for concurrent reads
- WAL-integrated: all mutations logged before apply

**Tests:**
- `TestBTree_InsertSeek` — basic operations
- `TestBTree_Delete` — deletion + rebalance
- `TestBTree_ConcurrentRead` — M readers, no writers
- `TestBTree_ConcurrentWrite` — serialized writers, WAL integration
- `TestBTree_Cursor` — seek + scan
- `TestBTree_CrashRecovery` — WAL replay after crash
- `TestBTree_Large` — 1M entries, verify height ≤ 5

---

### REQ000252: IndexScan B-tree Integration (~800 LOC)

**Files:** `SQL/EX/operators.go` (IndexScan rewrite), `ENG/LS/index_store.go` (migration path)

**Before:** LSM-backed index (`__idx__:` keyspace)

**After:**
```go
type IndexScan struct {
    indexID   uint64
    btree     *ENG.ID.BTree  // B-tree handle
    cursor    *ENG.ID.Cursor
    condition Expr
}

func (op *IndexScan) Next(ctx context.Context) (Row, error) {
    // B-tree Seek(key) → O(log N)
    // Fetch PK from leaf value
    // Store.Get(PK) → row data
}
```

**Fallback:** If B-tree not available, use LSM prefix scan (legacy path, deprecated)

**Tests:**
- `TestIndexScan_BTreeSeek` — point lookup via B-tree
- `TestIndexScan_Range` — range scan with cursor
- `TestIndexScan_JoinBTreeIndex` — index nested loop join

---

### REQ000047: SST Prefix Bloom Filters (~600 LOC)

**Files:** `ENG/LS/sst_writer.go` (bloom on flush), `ENG/LS/sst_reader.go` (bloom check)

**Current:** Bloom filter per SST for full key set

**Enhancement:** Additional bloom filter for key prefixes (first 8 bytes)

**Use case:** `WHERE prefix_col LIKE 'abc%'` — check prefix bloom before full scan

**Tests:**
- `TestPrefixBloom_Filter` — verify false positive rate
- `TestPrefixBloom_RangeScan` — LIKE prefix query skips non-matching SSTs

---

### REQ000271: SST Block Compression (~1,000 LOC)

**Files:** `ENG/LS/sst_writer.go` (compress on write), `ENG/LS/sst_reader.go` (decompress on read)

**Codec:** snappy or lz4 (configurable)

**Block size:** 4 KB (aligned with page size)

**Format:**
```
[Block Header: 1B codec + 3B uncompressed size]
[Compressed Data]
[CRC32: 4B]
```

**Tests:**
- `TestCompression_RoundTrip` — compress → decompress → verify
- `TestCompression_Ratio` — typical data compresses > 50%
- `TestCompression_Corrupt` — detect corruption via CRC

---

## Implementation Order (Linear Sequence)

### Phase 1: Statistics & Integrity (v0.20.0) — Weeks 1-3

1. REQ000272 — WAL checksum verification (foundational, low risk)
2. REQ000258 — ANALYZE executor
3. REQ000085 — Histogram selectivity
4. REQ000261 — PRAGMA integrity_check
5. REQ000257 — VACUUM executor
6. REQ000259 — Backup/Restore API
7. REQ000260 — Admin CLI

**Milestone:** Tag v0.20.0 after all 7 REQs pass tests

---

### Phase 2: SQL Expression Extensions (v0.21.0) — Weeks 4-6

1. REQ000262/263 — DATE/TIME/TIMESTAMP (simpler, independent)
2. REQ000264/265 — JSON type & operators
3. REQ000236 — Window function parsing
4. REQ000237 — Window function executor

**Milestone:** Tag v0.21.0 after all 4 REQs pass tests

---

### Phase 3: Storage Performance (v0.22.0) — Weeks 7-10

1. REQ000250 — B-tree core implementation (largest single task)
2. REQ000250 — B-tree WAL integration
3. REQ000250 — B-tree concurrency (RWMutex + page latches)
4. REQ000252 — IndexScan B-tree integration
5. REQ000047 — SST prefix bloom filters
6. REQ000271 — SST block compression

**Milestone:** Tag v0.22.0 after all 6 REQs pass tests

---

## Gap Analysis & Pre-existing Bugs

**iter-22 Deferred Items:**
- UNIQUE index enforcement: Still not wired. Add to Phase 3 as `REQ000252` extension (enforce uniqueness on B-tree insert).
- Histogram stats types defined but unused: Resolved by REQ000258 + REQ000085 in Phase 1.

**Cross-REQ Dependencies:**
- REQ000250 (B-tree) must complete before REQ000252 (IndexScan integration)
- REQ000258 (ANALYZE) must complete before REQ000085 (selectivity uses histograms)

---

## Testing Requirements

**Each REQ must have:**
1. Unit tests (table-driven, happy path + edge cases)
2. Integration tests (end-to-end with SQL API)
3. Property-based tests (crash recovery, concurrent access)
4. Benchmark (for performance-critical paths: B-tree seek, window function, compression)

**Quality gates:**
- `go test ./... -race -count=1` — all green (5 consecutive runs)
- Coverage: >80% for new files
- No allocs in hot paths (B-tree seek, bloom check)

---

## Outcome (Post-Completion Update)

**Status:** [done]

**Actual LOC:** [TBD post-completion]

**Commits:** [TBD]

**Tests added:** [TBD]

**Tags:**
- v0.20.0: [commit hash] — Statistics & Integrity cluster
- v0.21.0: [commit hash] — Window functions + DateTime + JSON
- v0.22.0: [commit hash] — B-tree index + SST optimizations

**Deviations:** [TBD — document any scope changes post-completion]

**Metrics:**
- ANALYZE accuracy: <5% selectivity estimation error on uniform data
- Window function memory: <100MB for 1M-row partition (spillover beyond)
- B-tree seek latency: p50 < 100μs, p99 < 1ms (1M entries, SSD)
- SST compression ratio: >50% on typical text data
