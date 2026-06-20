# CLI — razordata Command-Line Interface

## Overview

The `rdcli` CLI is a lightweight, scriptable interface over the `SYS/AP` public API. Designed for automation, CI/CD pipelines, scripting, and environments where resource usage matters. Zero external Go dependencies beyond `cobra`.

**Design goals:**
- Fast startup (< 50ms)
- Low memory footprint (< 10 MB)
- Pipe-friendly (structured output, proper exit codes)
- No interactive features (those belong in TUI)
- Minimal dependencies

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
