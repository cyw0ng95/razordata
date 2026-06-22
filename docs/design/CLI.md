# CLI — razordata Command-Line Interface

## Overview

The `rdcli` CLI is a lightweight, scriptable interface over the `SYS/AP` public API. Designed for automation, CI/CD pipelines, scripting, and environments where resource usage matters. Zero external Go dependencies beyond `cobra`.

**Design goals:**
- Fast startup (< 50ms)
- Low memory footprint (< 10 MB)
- Pipe-friendly (structured output, proper exit codes)
- No interactive features (those belong in TUI)
- Minimal dependencies

## Design Principles

### 1. No Database Logic in Clients

The CLI is an **orchestrator only** — it never implements database behavior. All database operations (query execution, transaction management, WAL writes, page cache hits, etc.) live exclusively in internal subsystems (SYS/AP, ENG, TXN, WAL, MEM, SQL/EX). The CLI's sole responsibility is:

- Parse user input (flags, positional args, config)
- Translate to SYS/AP calls
- Format and stream output
- Handle exit codes and error messages

**What the CLI does NOT do:**
- Parse SQL (delegated to SQL/EX via SYS/AP)
- Manage connections or sessions directly (SYS/SY handles lifecycle)
- Buffer or cache query results (SYS/AP streams via `Rows` iterator)
- Implement backup/restore logic (SYS/AP.Backup/Restore)
- Validate or transform data (ENG/SQL do this)

**Boundary enforcement:** `cmd/rdcli/` imports only `SYS/AP`, `SYS/SY`, `cobra`, and stdlib. Any attempt to import `ENG/*`, `TXN/*`, `WAL/*`, `MEM/*`, or `SQL/*` is a design violation caught by CI.

### 2. Testable by Pieces

The CLI is structured so every layer can be tested independently:

| Layer | Test Strategy |
|-------|---------------|
| **Flag/config parsing** | Unit tests with synthetic args; no I/O |
| **Command routing** | Table-driven tests mapping (cmd, flags) → expected SYS/AP calls |
| **Output formatting** | Pure functions: input rows → formatted string; no database needed |
| **SYS/AP integration** | Mock `SYS/AP` interface; test CLI behavior with controlled responses |
| **End-to-end** | `go test` with `--tags slt_corpus` against test corpus; verifies real I/O |

**Testable boundaries:**
- `output.go` — pure formatting functions, fully unit-testable
- `config.go` — config parsing with no side effects
- `query.go`, `exec.go`, etc. — accept `SYS/AP.Engine` interface, injectable mock
- `main.go` — integration test with `t.Parallel()` subtests per command

**Mocking strategy:** Define `type Engine interface { Open(...) (*EngineImpl, error) }` in CLI package. Tests inject `mockEngine` that returns pre-constructed `Rows`/`Result` without touching disk.

### 3. Ergonomic

The CLI is designed for **human and machine** users:

**For humans (terminal):**
- Auto-detects terminal vs pipe, chooses optimal format
- Box-drawing table output is readable and compact
- Timing info shows by default (transparency)
- Helpful error messages with SQL context: `"syntax error at line 3: unexpected token 'FROM'"`
- `--help` is comprehensive with examples

**For machines (scripts/CI):**
- Structured output formats (JSON, CSV, NDJSON) for piping
- Consistent exit codes (0=success, 1=SQL error, 2=connection error, 3=timeout)
- `--quiet` and `--raw` flags for minimal output in pipelines
- `--max-rows` prevents runaway output in scripts
- `--timeout` prevents hanging in CI
- Exit code 0 even with 0 rows (query succeeded, just empty result)

**Config defaults that work:**
- No config file needed — all defaults are sensible
- Config file is additive, never required
- CLI flags always override config
- `~/.config/rdcli/config.toml` is optional

## Architecture

```
cmd/rdcli/                    # CLI package (package main)
├── main.go                   # Entry point
├── root.go                   # cobra root command, global flags
├── query.go                  # rdcli query <dbdir> <sql>
├── exec.go                   # rdcli exec <dbdir> <sql>
├── schema.go                 # rdcli schema <dbdir> [table]
├── dump.go                   # rdcli dump <dbdir>
├── import_cmd.go             # rdcli import <dbdir> <file> <table>
├── export.go                 # rdcli export <dbdir> <table>
├── admin.go                  # integrity-check / vacuum / analyze
├── backup.go                 # backup / restore
├── info.go                   # rdcli info <dbdir>
├── output.go                 # Output formatting (table/json/csv/ndjson)
└── config.go                 # Config file (~/.config/rdcli/config.toml)
```

## Dependency Rule

```
cmd/rdcli/ ──imports──► SYS/AP (public API)
cmd/rdcli/ ──imports──► SYS/SY (engine lifecycle)
cmd/rdcli/ ──imports──► cobra (CLI framework)
cmd/rdcli/ ──imports──► encoding/json, encoding/csv (stdlib)
cmd/rdcli/ ──DOES NOT──► SQL/EX, SQL/PS, ENG/*, TXN/*, WAL/*, MEM/*
cmd/rdcli/ ──DOES NOT──► bubbletea, lipgloss, chroma (TUI deps)
```

## External Dependencies

| Package | Purpose | Why needed |
|---------|---------|-----------|
| `github.com/spf13/cobra` | CLI framework | Subcommands, flag parsing, help generation |

That's it. One external dependency. Everything else is stdlib.

## Command Structure

```
rdcli <command> [args] [flags]

# Query
rdcli query <dbdir> <sql> [--format json|csv|table|ndjson|line]

# Execute
rdcli exec <dbdir> <sql>

# Schema
rdcli schema <dbdir> [table]
rdcli diff <dbdir1> <dbdir2>

# Data
rdcli import <dbdir> <file> <table> [--format csv|json|tsv]
rdcli export <dbdir> <table> [--format csv|json] [--output file]
rdcli dump <dbdir> [--tables pattern] [--output file]

# Admin
rdcli integrity-check <dbdir>
rdcli vacuum <dbdir>
rdcli analyze <dbdir>
rdcli backup <src> <dst>
rdcli restore <backup> <dst>

# Info
rdcli info <dbdir>
rdcli version
```

No `rdcli open` — that's the TUI's job.

## Command Details

### `rdcli query <dbdir> <sql>`

One-shot query. Pipe-friendly.

```bash
# Table output (default in terminal)
$ rdcli query mydb.razor "SELECT * FROM users LIMIT 3"
┌────┬──────────┬─────┐
│ id │ name     │ age │
├────┼──────────┼─────┤
│  1 │ Alice    │  30 │
│  2 │ Bob      │  25 │
│  3 │ Charlie  │  35 │
└────┴──────────┴─────┘
(3 rows, 2ms)

# JSON output
$ rdcli query mydb.razor "SELECT * FROM users" --format json
[{"id":1,"name":"Alice","age":30},{"id":2,"name":"Bob","age":25}]

# CSV output (auto-detected when piped)
$ rdcli query mydb.razor "SELECT * FROM users" | head -2
id,name,age
1,Alice,30

# Raw output (no headers, no formatting)
$ rdcli query mydb.razor "SELECT count(*) FROM users" --raw --quiet
42

# Pipe into another command
$ rdcli query mydb.razor "SELECT name FROM users" --format csv --noheader | sort
Alice
Bob
Charlie
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--format` | auto | Output format: `table`, `json`, `csv`, `ndjson`, `line` |
| `--header` | true (terminal) / false (pipe) | Show column headers |
| `--noheader` | — | Hide column headers |
| `--null` | empty | Display string for NULL values |
| `--separator` | `\|` (table), `,` (csv) | Column separator |
| `--timing` | true | Show query timing |
| `--quiet` | false | Suppress row count message |
| `--raw` | false | No formatting, just values |
| `--max-rows` | 1000 | Maximum rows to output (0=unlimited) |
| `--timeout` | 30s | Query timeout |

**Exit codes:**
| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | SQL error |
| 2 | Open/connection error |
| 3 | Timeout |

**Auto-detection:** When stdout is not a terminal (piped), default format is `csv` with no headers.

### `rdcli exec <dbdir> <sql>`

Execute DDL/DML. No row output.

```bash
$ rdcli exec mydb.razor "CREATE TABLE users (id INT PRIMARY KEY, name TEXT)"
OK (0 rows affected, 1ms)

$ rdcli exec mydb.razor "INSERT INTO users VALUES (1, 'Alice')"
OK (1 row affected, 0ms)
```

### `rdcli schema <dbdir> [table]`

List tables or show table schema.

```bash
# List all tables
$ rdcli schema mydb.razor
Tables (3):
  users         1,234 rows
  orders          567 rows
  products         89 rows

# Show table schema
$ rdcli schema mydb.razor users
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    age INTEGER DEFAULT 0,
    email TEXT UNIQUE
);

Indexes:
  idx_users_email ON users (email)

Rows: 1,234
```

### `rdcli dump <dbdir>`

SQL export. Compatible with `rdcli import`.

```bash
# Dump to stdout
$ rdcli dump mydb.razor
CREATE TABLE users (...);
INSERT INTO users VALUES (1, 'Alice', 30);
INSERT INTO users VALUES (2, 'Bob', 25);

# Dump to file
$ rdcli dump mydb.razor --output backup.sql

# Dump specific tables
$ rdcli dump mydb.razor --tables "users,orders"
```

### `rdcli import <dbdir> <file> <table>`

Import data from file.

```bash
# Import CSV
$ rdcli import mydb.razor users.csv users
Imported 1,234 rows in 0.5s

# Import JSON
$ rdcli import mydb.razor users.json users --format json
```

### `rdcli info <dbdir>`

Database statistics.

```bash
$ rdcli info mydb.razor
Database: mydb.razor
Tables: 3
Total rows: 1,890
Size: 92 KB
WAL segments: 2
Last modified: 2026-06-20 10:30:00
```

## Output Formatting

### Formats

**table** (default for terminal):
```
┌────┬──────────┬─────┐
│ id │ name     │ age │
├────┼──────────┼─────┤
│  1 │ Alice    │  30 │
│  2 │ Bob      │  25 │
└────┴──────────┴─────┘
(2 rows, 3ms)
```

Implemented with `fmt.Printf` and Unicode box-drawing characters. No external library.

**json**:
```json
[
  {"id": 1, "name": "Alice", "age": 30},
  {"id": 2, "name": "Bob", "age": 25}
]
```

Uses `encoding/json`.

**csv**:
```
id,name,age
1,Alice,30
2,Bob,25
```

Uses `encoding/csv`.

**ndjson** (newline-delimited JSON):
```
{"id":1,"name":"Alice","age":30}
{"id":2,"name":"Bob","age":25}
```

**line** (one line per column per row):
```
--- row 1 ---
id = 1
name = Alice
age = 30
--- row 2 ---
id = 2
name = Bob
age = 25
```

### Auto-detection Logic

```go
func detectFormat() string {
    if !isTerminal(os.Stdout) {
        return "csv"  // piped: csv, no headers
    }
    return "table"    // terminal: box-drawing table
}
```

## Config File

Location: `~/.config/rdcli/config.toml`

```toml
[output]
format = "table"
headers = true
null = "NULL"
separator = "|"
timing = true
max_rows = 1000

[connection]
timeout = "30s"
readonly = false
```

CLI flags override config values. `--config` flag overrides default path. Config file is optional — all defaults work without it.

## Integration with SYS/AP

The CLI uses only these `SYS/AP` types and functions:

```go
// Engine lifecycle
SY.Open(ctx, dir, AP.Options{}) → (*Engine, error)
eng.Close(ctx) → error
eng.Begin(ctx) → (Session, error)
eng.Stats() → AP.EngineStats

// Session operations
sess.Query(ctx, sql) → (*AP.Rows, error)
sess.Exec(ctx, sql) → (AP.Result, error)
sess.Close() → error

// Backup/Restore
AP.Backup(ctx, src, dst, opts) → (*AP.BackupStats, error)
AP.Restore(ctx, backup, dst) → (*AP.BackupStats, error)

// Types
AP.Rows { Cols []string, Types []int }
AP.Result { RowsAffected int64, LastInsertID uint64 }
AP.Options { Dir, InMemory, ReadOnly, ... }
```

No other internal types are used.

### Mock Interface for Testing

The CLI defines a local interface for testability:

```go
// Engine interface for mocking in tests
type Engine interface {
    Close(context.Context) error
    Begin(context.Context) (Session, error)
    Stats() AP.EngineStats
}

type Session interface {
    Query(context.Context, string) (*AP.Rows, error)
    Exec(context.Context, string) (AP.Result, error)
    Close() error
}

// Production implementation wraps real SYS/AP
type realEngine struct{ eng *SY.Engine }
type realSession struct{ sess SY.Session }
```

Tests inject `mockEngine` and `mockSession` that return pre-constructed responses without I/O.

## Testing

### Unit Tests (no I/O)

| Package | Tests | Coverage |
|---------|-------|----------|
| `output_test.go` | Format detection, table/JSON/CSV/NDJSON rendering | All format functions |
| `config_test.go` | Config parsing, flag overrides, defaults | All config fields |
| `query_test.go` | Flag parsing, arg validation, command routing | All query flags |
| `exec_test.go` | DDL/DML output formatting | All exec cases |
| `schema_test.go` | Schema output formatting | All schema cases |
| `admin_test.go` | Admin command routing | All admin commands |

**Example unit test:**
```go
func TestRenderTable(t *testing.T) {
    rows := &AP.Rows{
        Cols: []string{"id", "name"},
        Data: [][]any{{1, "Alice"}, {2, "Bob"}},
    }
    out := renderTable(rows, StyleConfig{Border: true})
    assert.Contains(t, out, "Alice")
    assert.Contains(t, out, "id")
}
```

### Integration Tests (with I/O)

| Test | Method |
|------|--------|
| `cmd_test.go` | End-to-end: real `SYS/AP` engine, in-memory DB, verify output |
| `backup_test.go` | Real backup/restore cycle, verify data integrity |
| `import_export_test.go` | Round-trip: import → query → export → compare |

**SLT corpus tests:** `go test -tags slt_corpus -run TestCLI_SLT` runs the SQLLogicTest corpus through the CLI, verifying query results match expected outputs.

### Test Coverage Requirements

- Every public CLI command has at least one test
- All output formats have unit tests
- Error paths tested: invalid SQL, missing DB, timeout, permission denied
- Exit codes verified: 0 (success), 1 (SQL error), 2 (connection error), 3 (timeout)
- `go test ./cmd/rdcli/... -race -cover` must pass with >80% coverage
