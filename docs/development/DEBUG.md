# Razordata Debug Manual

> Comprehensive guide to the DBG subsystem for developers and troubleshooters.

## Building with Debug Support

All debug code is gated by `//go:build debug`. Without this tag, zero debug code is compiled — the binary is identical byte-for-byte to a production build.

```bash
# Production build (no debug)
go build ./cmd/razor

# Debug build
go build -tags debug ./cmd/razor

# Debug + profiling
go build -tags "debug,dbg_profiling" ./cmd/razor
```

**Build tags:**
| Tag | Effect |
|-----|--------|
| `debug` | Compiles all DBG code, registers real sinks, starts socket listener |
| `dbg_profiling` | Enables mutex/block profile sampling (~3% overhead) |
| `dbg_fgprof` | Adds fgprof off-CPU profile support |

## DBG Subsystem Architecture

```
internal/DBG/
├── TE/   Trace Events — structured ring-buffer events
├── CT/   Counters — atomic counters + latency histograms
├── IN/   Inspectors — page dumps, buffer pool, active transactions
├── PR/   Profiling — on-demand CPU/heap/goroutine/mutex/block/trace
├── DC/   Dynamic Control — per-subsystem log level, trace class toggle
├── SK/   Socket — UNIX domain socket command server
├── DI/   Debugger — top-level coordinator, wires all clusters
└── JD/   Join Debug — JOIN-specific debugging facilities
```

**Dependency direction:** No other subsystem imports `internal/DBG/`. All push paths go through `LOG/HK` interfaces. DBG registers itself as a hook implementation at init time.

## Runtime Debugging via PRAGMA

When built with `-tags debug`, the following PRAGMAs are available:

### Log Level Control

```sql
-- Set log level for a subsystem
PRAGMA set_log_level(ENG, DEBUG);

-- Get current log level
PRAGMA get_log_level(ENG);
```

### Trace Class Control

```sql
-- Enable trace class
PRAGMA enable_trace(sql, on);

-- Disable trace class
PRAGMA enable_trace(sql, off);
```

### JOIN Debugging

```sql
-- Enable join tracing (summary level)
PRAGMA debug_join_tracing = summary;

-- Enable join tracing (detailed level — predicate results)
PRAGMA debug_join_tracing = detailed;

-- Enable join tracing (full level — row values)
PRAGMA debug_join_tracing = full;

-- Check current status
PRAGMA debug_join_tracing;

-- Flush captured events
PRAGMA debug_join_flush;

-- Disable join tracing
PRAGMA debug_join_tracing = off;
```

### State Inspection

```sql
-- Dump buffer pool state
PRAGMA buffer_pool;

-- Show active transactions
PRAGMA active_txns;

-- Dump a specific page
PRAGMA dump_page('data', 42);

-- Show engine counters
PRAGMA engine_counters;

-- Show query statistics
PRAGMA query_stats;

-- Show WAL statistics
PRAGMA wal_stats;
```

## Runtime Debugging via Socket

When built with `-tags debug` and `EnableDebugSocket = true`, a UNIX domain socket is available at `<dbdir>/.debug/debug.sock`.

### Connecting

```bash
# Using razor CLI
razor debug <dbdir> stats

# Using netcat/socat
nc -U <dbdir>/.debug/debug.sock
```

### Available Commands

| Command | Description |
|---------|-------------|
| `heap` | Dump heap profile to file |
| `cpu N` | CPU profile for N seconds |
| `goroutine` | Dump goroutine profile |
| `stats` | Show engine counters |
| `gc` | Force garbage collection |
| `help` | Show all commands |

### JOIN Debug Commands

```
debug_join on           # Enable summary-level tracing
debug_join summary      # Same as above
debug_join detailed     # Enable detailed tracing (predicates)
debug_join full         # Enable full tracing (row values)
debug_join off          # Disable tracing
debug_join_flush        # Flush captured events to output
```

## JOIN Debugging Workflow

### Diagnosing Zero-Row Joins

When a multi-table comma-join returns zero rows instead of expected results:

```sql
-- 1. Enable detailed join tracing
PRAGMA debug_join_tracing = detailed;

-- 2. Run the problematic query
SELECT x8, e9+131 FROM t8, t9 WHERE e9=383 AND 561=e8;

-- 3. Flush and examine events
PRAGMA debug_join_flush;
```

**What to look for in the output:**

1. **RowFlow IN events** — Are rows entering the join operator? If no IN events, the scan is returning zero rows.
2. **Predicate PASS/FAIL** — Which predicates are filtering out all rows? A predicate that FAILS for every row pair indicates the bug.
3. **ColumnOffset** — Are column counts/offsets correct after multi-table joins? Mismatches indicate column shuffling.

### Diagnosing Column Shuffling

When join output has correct row count but wrong column values:

```sql
-- 1. Enable full tracing
PRAGMA debug_join_tracing = full;

-- 2. Run the query
SELECT t1.a, t2.b FROM t1 JOIN t2 ON t1.id = t2.id;

-- 3. Flush
PRAGMA debug_join_flush;
```

**What to look for:**

1. **RowFlow OUT events** — Check that output rows have the correct column order
2. **ColumnOffset events** — Look for any offset mismatches at operator boundaries

### Diagnosing Correlated EXISTS

When `EXISTS` returns wrong results:

```sql
-- 1. Enable detailed tracing
PRAGMA debug_join_tracing = detailed;

-- 2. Run the query
SELECT * FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.id = t1.id);

-- 3. Flush
PRAGMA debug_join_flush;
```

## Verbosity Levels

| Level | Name | What It Captures | Overhead |
|-------|------|------------------|----------|
| 0 | off | Nothing | Zero |
| 1 | summary | Row counts, strategy selection, correlation | Low |
| 2 | detailed | + Predicate results, column offsets | Moderate |
| 3 | full | + Full row values for all operators | High |

**Guidelines:**
- Use **level 1** for initial investigation (is the join producing any rows?)
- Use **level 2** when you need to see which predicates are failing
- Use **level 3** only when you need to inspect actual row values (expensive for large result sets)

## Performance Considerations

- **Zero overhead without debug tag:** All debug code is stripped by the compiler
- **Runtime toggle:** Enable only when debugging, disable immediately after
- **Ring buffer:** Fixed-capacity, oldest events are dropped when full
- **Flush clears buffer:** Call `PRAGMA debug_join_flush` after each query to avoid accumulation
- **Level 3 is expensive:** Full row tracing can be 3-10x slower for complex joins

## Common Debug Patterns

### Pattern 1: Quick Check

```sql
PRAGMA debug_join_tracing = summary;
-- run query
PRAGMA debug_join_flush;
PRAGMA debug_join_tracing = off;
```

### Pattern 2: Deep Dive

```sql
PRAGMA debug_join_tracing = detailed;
-- run query
PRAGMA debug_join_flush;
-- examine predicate results
-- adjust query or code
-- run again
PRAGMA debug_join_flush;
PRAGMA debug_join_tracing = off;
```

### Pattern 3: SLT select4 Debugging

For the 25 failing select4 cases (3-table+ comma-joins):

```sql
PRAGMA debug_join_tracing = detailed;
-- Run each failing query one at a time
PRAGMA debug_join_flush;
-- Analyze which predicates fail for each query
-- After fix, re-run all 25 to verify
PRAGMA debug_join_tracing = off;
```

## Troubleshooting

### Debug commands not available

- Ensure built with `-tags debug`
- Check that `EnableDebugSocket = true` in options for socket commands

### No events captured

- Verify `PRAGMA debug_join_tracing` shows a level > 0
- Check that the query actually uses a JOIN operator (not pushed down to scan)

### Socket connection refused

- Verify `EnableDebugSocket = true` in engine options
- Check that `<dbdir>/.debug/` directory exists and has correct permissions
- Verify the engine is running (socket is created on Open, removed on Close)
