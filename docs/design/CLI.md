# CLI — razordata Command-Line Interface

## Overview

The `razor` CLI is a lightweight, scriptable interface over the `SYS/AP` public API. Designed for automation, CI/CD pipelines, scripting, and environments where resource usage matters. Zero external Go dependencies beyond `cobra`.

**Design goals:**
- Fast startup (< 50ms)
- Low memory footprint (< 10 MB)
- Pipe-friendly (structured output, proper exit codes)
- No interactive features (those belong in TUI)
- Minimal dependencies

## Architecture

```
cmd/razor/                    # CLI package (package main)
├── main.go                   # Entry point
├── root.go                   # cobra root command, global flags
├── query.go                  # razor query <dbdir> <sql>
├── exec.go                   # razor exec <dbdir> <sql>
├── schema.go                 # razor schema <dbdir> [table]
├── dump.go                   # razor dump <dbdir>
├── import_cmd.go             # razor import <dbdir> <file> <table>
├── export.go                 # razor export <dbdir> <table>
├── admin.go                  # integrity-check / vacuum / analyze
├── backup.go                 # backup / restore
├── info.go                   # razor info <dbdir>
├── output.go                 # Output formatting (table/json/csv/ndjson)
└── config.go                 # Config file (~/.config/razor/config.toml)
```

## Dependency Rule

```
cmd/razor/ ──imports──► SYS/AP (public API)
cmd/razor/ ──imports──► SYS/SY (engine lifecycle)
cmd/razor/ ──imports──► cobra (CLI framework)
cmd/razor/ ──imports──► encoding/json, encoding/csv (stdlib)
cmd/razor/ ──DOES NOT──► SQL/EX, SQL/PS, ENG/*, TXN/*, WAL/*, MEM/*
cmd/razor/ ──DOES NOT──► bubbletea, lipgloss, chroma (TUI deps)
```

## External Dependencies

| Package | Purpose | Why needed |
|---------|---------|-----------|
| `github.com/spf13/cobra` | CLI framework | Subcommands, flag parsing, help generation |

That's it. One external dependency. Everything else is stdlib.

## Command Structure

```
razor <command> [args] [flags]

# Query
razor query <dbdir> <sql> [--format json|csv|table|ndjson|line]

# Execute
razor exec <dbdir> <sql>

# Schema
razor schema <dbdir> [table]
razor diff <dbdir1> <dbdir2>

# Data
razor import <dbdir> <file> <table> [--format csv|json|tsv]
razor export <dbdir> <table> [--format csv|json] [--output file]
razor dump <dbdir> [--tables pattern] [--output file]

# Admin
razor integrity-check <dbdir>
razor vacuum <dbdir>
razor analyze <dbdir>
razor backup <src> <dst>
razor restore <backup> <dst>

# Info
razor info <dbdir>
razor version
```

No `razor open` — that's the TUI's job.

## Command Details

### `razor query <dbdir> <sql>`

One-shot query. Pipe-friendly.

```bash
# Table output (default in terminal)
$ razor query mydb.razor "SELECT * FROM users LIMIT 3"
┌────┬──────────┬─────┐
│ id │ name     │ age │
├────┼──────────┼─────┤
│  1 │ Alice    │  30 │
│  2 │ Bob      │  25 │
│  3 │ Charlie  │  35 │
└────┴──────────┴─────┘
(3 rows, 2ms)

# JSON output
$ razor query mydb.razor "SELECT * FROM users" --format json
[{"id":1,"name":"Alice","age":30},{"id":2,"name":"Bob","age":25}]

# CSV output (auto-detected when piped)
$ razor query mydb.razor "SELECT * FROM users" | head -2
id,name,age
1,Alice,30

# Raw output (no headers, no formatting)
$ razor query mydb.razor "SELECT count(*) FROM users" --raw --quiet
42

# Pipe into another command
$ razor query mydb.razor "SELECT name FROM users" --format csv --noheader | sort
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

### `razor exec <dbdir> <sql>`

Execute DDL/DML. No row output.

```bash
$ razor exec mydb.razor "CREATE TABLE users (id INT PRIMARY KEY, name TEXT)"
OK (0 rows affected, 1ms)

$ razor exec mydb.razor "INSERT INTO users VALUES (1, 'Alice')"
OK (1 row affected, 0ms)
```

### `razor schema <dbdir> [table]`

List tables or show table schema.

```bash
# List all tables
$ razor schema mydb.razor
Tables (3):
  users         1,234 rows
  orders          567 rows
  products         89 rows

# Show table schema
$ razor schema mydb.razor users
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

### `razor dump <dbdir>`

SQL export. Compatible with `razor import`.

```bash
# Dump to stdout
$ razor dump mydb.razor
CREATE TABLE users (...);
INSERT INTO users VALUES (1, 'Alice', 30);
INSERT INTO users VALUES (2, 'Bob', 25);

# Dump to file
$ razor dump mydb.razor --output backup.sql

# Dump specific tables
$ razor dump mydb.razor --tables "users,orders"
```

### `razor import <dbdir> <file> <table>`

Import data from file.

```bash
# Import CSV
$ razor import mydb.razor users.csv users
Imported 1,234 rows in 0.5s

# Import JSON
$ razor import mydb.razor users.json users --format json
```

### `razor info <dbdir>`

Database statistics.

```bash
$ razor info mydb.razor
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

Location: `~/.config/razor/config.toml`

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
