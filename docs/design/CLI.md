# CLI — razordata Command-Line Interface

## Overview

The `razor` CLI is a thin presentation layer over the `SYS/AP` public API. It contains zero database logic — all SQL parsing, planning, execution, and transaction management happen inside the engine. The CLI is responsible for:

1. Parsing command-line arguments and flags
2. Calling `SYS/AP` public API (`SY.Open`, `Session.Query`, `Session.Exec`)
3. Formatting output (table, json, csv, ndjson)
4. Handling I/O (stdin/stdout, files, config)

The CLI is **not** responsible for:
- SQL parsing or validation
- Query planning or optimization
- Transaction management
- Storage engine operations
- Any `internal/` package access beyond `SYS/AP` and `SYS/SY`

## Architecture

```
cmd/razor/                    # CLI package (package main)
├── main.go                   # Entry point, 10 lines
├── root.go                   # cobra root command, global flags
├── query.go                  # razor query <dbdir> <sql>
├── exec.go                   # razor exec <dbdir> <sql>
├── schema.go                 # razor schema <dbdir> [table]
├── dump.go                   # razor dump <dbdir>
├── import_cmd.go             # razor import <dbdir> <file> <table>
├── export.go                 # razor export <dbdir> <table>
├── admin.go                  # integrity-check / vacuum / analyze
├── backup.go                 # backup / restore
├── repl.go                   # Interactive REPL (bubbletea TUI)
├── output.go                 # Output formatting (table/json/csv/ndjson)
├── completer.go              # Auto-completion (table/col/key)
└── config.go                 # Config file (~/.config/razor/config.toml)
```

All files are `package main` in `cmd/razor/`. No sub-packages.

## Dependency Rule

```
cmd/razor/ ──imports──► SYS/AP (public API)
cmd/razor/ ──imports──► SYS/SY (engine lifecycle)
cmd/razor/ ──imports──► cobra, bubbletea, lipgloss (CLI framework)
cmd/razor/ ──DOES NOT──► SQL/EX, SQL/PS, ENG/*, TXN/*, WAL/*, MEM/*
```

If a feature requires access to internal packages, the feature must be exposed through `SYS/AP` first. The CLI never bypasses the public API.

## Command Structure

```
razor <command> [args] [flags]

# Core
razor open <dbdir>                           # Interactive REPL
razor query <dbdir> <sql> [--format json]    # One-shot query
razor exec <dbdir> <sql>                     # Execute DDL/DML

# Data
razor import <dbdir> <file> <table>          # Import CSV/JSON/TSV
razor export <dbdir> <table> [--format csv]  # Export table data
razor dump <dbdir> [--tables pattern]        # SQL export

# Schema
razor schema <dbdir> [table]                 # Browse schema
razor diff <dbdir1> <dbdir2>                 # Compare schemas

# Admin
razor integrity-check <dbdir>
razor vacuum <dbdir>
razor analyze <dbdir>
razor backup <src> <dst>
razor restore <backup> <dst>

# Info
razor info <dbdir>                           # Database statistics
razor version
```

## Command Details

### `razor open <dbdir>`

Launches interactive REPL. Uses `charmbracelet/bubbletea` for TUI.

**Flow:**
1. `SY.Open(ctx, dbdir, AP.Options{})` → `*Engine`
2. `eng.Begin(ctx)` → `Session`
3. Start bubbletea event loop
4. On each Enter: `session.Query(ctx, input)` or `session.Exec(ctx, input)`
5. Format output via `formatOutput(rows, format)`
6. On `\q` or Ctrl+C: `session.Close()` → `eng.Close(ctx)`

**REPL features:**
- SQL syntax highlighting (chroma)
- Multi-line input (Shift+Enter)
- History (Up/Down arrows, Ctrl+R search)
- Auto-completion (Tab): table names, column names, SQL keywords
- Dot commands: `.tables`, `.schema`, `.mode`, `.headers`, `.quit`
- Query timing display
- Progress spinner for long queries

### `razor query <dbdir> <sql>`

One-shot query execution. Pipe-friendly.

**Flow:**
1. `SY.Open(ctx, dbdir, AP.Options{ReadOnly: true})` → `*Engine`
2. `eng.Begin(ctx)` → `Session`
3. `session.Query(ctx, sql)` → `*AP.Rows`
4. `formatOutput(rows, format)` → stdout
5. Print `(N rows, Xms)` unless `--quiet`
6. Exit code: 0=success, 1=SQL error, 2=open error

**Flags:**
- `--format table|json|csv|ndjson|line` (default: auto-detect)
- `--header` / `--noheader` (default: on for terminal, off for pipe)
- `--null TEXT` (default: empty)
- `--separator SEP` (default: `|` for table, `,` for csv)
- `--timing` (default: on)
- `--quiet` (suppress row count message)
- `--raw` (no formatting, just values)
- `--max-rows N` (default: 1000, 0=unlimited)
- `--timeout DURATION` (default: 30s)

### `razor exec <dbdir> <sql>`

Execute DDL/DML without returning rows.

**Flow:**
1. `SY.Open(ctx, dbdir, AP.Options{})` → `*Engine`
2. `eng.Begin(ctx)` → `Session`
3. `session.Exec(ctx, sql)` → `AP.Result`
4. Print `(N rows affected)` unless `--quiet`
5. Exit code: 0=success, 1=SQL error

### `razor schema <dbdir> [table]`

Browse database schema.

**Without table arg:**
```
$ razor schema mydb.razor
Tables (3):
  users         1,234 rows   48 KB
  orders          567 rows   32 KB
  products         89 rows   12 KB
```

**With table arg:**
```
$ razor schema mydb.razor users
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    age INTEGER DEFAULT 0,
    email TEXT UNIQUE
);

Indexes:
  idx_users_email ON users (email)

Foreign Keys:
  (none)

Row count: 1,234
Size: 48 KB
```

**Flow:**
1. Open engine, begin session
2. `session.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table'")`
3. For each table: `session.Query(ctx, "PRAGMA table_info("+name+")")`
4. Format as tree/table

### `razor import <dbdir> <file> <table>`

Import data from file.

**Flow:**
1. Detect format from extension (`.csv`, `.json`, `.tsv`) or `--format` flag
2. Parse file, infer column types
3. `session.Exec(ctx, "CREATE TABLE IF NOT EXISTS ...")`
4. For each row: `session.Exec(ctx, "INSERT INTO ... VALUES (?, ?, ...)")`
5. Show progress bar for large files
6. Print `(N rows imported in Xs)`

### `razor export <dbdir> <table>`

Export table data.

**Flow:**
1. `session.Query(ctx, "SELECT * FROM "+table)` → rows
2. `formatOutput(rows, format)` → stdout or file

### `razor dump <dbdir>`

SQL export compatible with `razor import`.

**Flow:**
1. For each table: emit `CREATE TABLE ...`
2. For each row: emit `INSERT INTO ... VALUES (...)`
3. Output to stdout or `--output` file

### `razor integrity-check <dbdir>`

**Flow:**
1. `SY.Open(ctx, dbdir, AP.Options{ReadOnly: true})`
2. `session.Query(ctx, "PRAGMA integrity_check")`
3. If result is "ok": print "OK"
4. If result has errors: print each error, exit 1

### `razor vacuum <dbdir>`

**Flow:**
1. `SY.Open(ctx, dbdir, AP.Options{})`
2. `session.Exec(ctx, "VACUUM")`
3. Print "OK"

### `razor analyze <dbdir>`

**Flow:**
1. `SY.Open(ctx, dbdir, AP.Options{})`
2. For each table: `session.Exec(ctx, "ANALYZE "+table)`
3. Print summary

## Output Formatting

### Auto-detection

```go
func detectFormat() string {
    if !isTerminal(os.Stdout) {
        return "csv"  // pipe mode: csv, no headers
    }
    return "table"    // terminal: box-drawing table
}
```

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

**json**:
```json
[
  {"id": 1, "name": "Alice", "age": 30},
  {"id": 2, "name": "Bob", "age": 25}
]
```

**csv**:
```
id,name,age
1,Alice,30
2,Bob,25
```

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

## Config File

Location: `~/.config/razor/config.toml`

```toml
[output]
format = "table"        # table|json|csv|line|ndjson
headers = true
null = "NULL"
separator = "|"
timing = true
max_rows = 1000

[editor]
vi_mode = false
history_size = 10000
auto_complete = true
syntax_highlight = true

[connection]
timeout = "30s"
readonly = false
```

CLI flags override config values. `--config` flag overrides default path.

## Error Handling

| Exit Code | Meaning |
|-----------|---------|
| 0 | Success |
| 1 | SQL error (syntax, constraint, etc.) |
| 2 | Connection/open error (dir not found, corrupt, etc.) |
| 3 | Timeout |
| 4 | Config error |

Error output goes to stderr. In `--json` mode, errors are JSON:
```json
{"error": "syntax error near 'SELEC'", "code": 1}
```

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/spf13/cobra` | CLI framework, subcommands, flags |
| `github.com/charmbracelet/bubbletea` | TUI framework for REPL |
| `github.com/charmbracelet/bubbles` | TUI components (table, input, spinner) |
| `github.com/charmbracelet/lipgloss` | Terminal styling |
| `github.com/alecthomas/chroma` | SQL syntax highlighting |
| `github.com/pelletier/go-toml` | Config file parsing |

All are external Go libraries. No C dependencies.

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

// Types
AP.Rows { Cols []string, Types []int }
AP.Result { RowsAffected int64, LastInsertID uint64 }
AP.Options { Dir, InMemory, ReadOnly, ... }
```

No other internal types are used.
