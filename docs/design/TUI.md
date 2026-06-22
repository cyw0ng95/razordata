# TUI — razordata Interactive Terminal

## Overview

The `rdtui` command launches an interactive terminal experience for human users. Rich REPL with syntax highlighting, auto-completion, multi-line editing, and beautiful output formatting.

**Design goals:**
- Beautiful, modern terminal experience
- SQL syntax highlighting
- Auto-completion (tables, columns, keywords)
- Multi-line editing with history
- Schema browsing
- Query timing and performance hints
- Separate binary from CLI (optional dependency)

## Design Principles

### 1. No Database Logic in Clients

The TUI is an **interactive orchestrator** — it never implements database behavior. All database operations live in internal subsystems accessed exclusively through `SYS/AP`. The TUI's responsibilities:

- Manage the bubbletea event loop and UI state
- Translate user input (keystrokes, dot commands) to SYS/AP calls
- Render query results with lipgloss styling
- Provide syntax highlighting and auto-completion (local, no DB needed for highlighting)
- Cache schema metadata for completion (lazy-loaded via SYS/AP)

**What the TUI does NOT do:**
- Execute SQL or parse queries (SYS/AP does this)
- Manage transactions or connection pooling (SYS/SY handles lifecycle)
- Implement query optimization or execution plans (SQL/EX, ENG do this)
- Store or retrieve data pages (MEM, WAL do this)
- Perform backup/restore (SYS/AP.Backup/Restore)

**Boundary enforcement:** `cmd/rdtui/` imports only `SYS/AP`, `SYS/SY`, `bubbletea`, `bubbles`, `lipgloss`, `chroma`. Any import of `ENG/*`, `TXN/*`, `WAL/*`, `MEM/*`, or `SQL/*` is a CI-catchable violation.

**Auto-completion caveat:** The completer queries the database for table/column names, but only through `SYS/AP`'s public `Query` method. It does not access internal catalog structures directly. Caching (30s TTL) is a TUI concern, not a database concern.

### 2. Testable by Pieces

The TUI is structured for layered testing:

| Layer | Test Strategy |
|-------|---------------|
| **Input parsing** | Unit tests: keystroke sequences → parsed command |
| **Dot command parsing** | Table-driven tests for `.tables`, `.schema`, `.mode`, etc. |
| **Output rendering** | Pure functions: `Rows` + `StyleConfig` → rendered string |
| **Auto-completion** | Unit tests with mock completion context; no DB needed |
| **Syntax highlighting** | Chroma lexer tests; deterministic given SQL input |
| **SYS/AP integration** | Mock `SYS/AP.Engine` interface; test TUI behavior with controlled responses |
| **End-to-end** | `bubbletea` test harness with simulated keystrokes; verify UI state transitions |

**Testable boundaries:**
- `input.go` — multi-line input handling, pure function with keystroke input
- `table.go` — rendering function, pure: `Rows → string`
- `completer.go` — accepts `CompleterConfig` with table/column lists; injectable mock
- `dotcmds.go` — pure command parser, no side effects
- `repl.go` — accepts `Engine` interface; mockable for unit tests

**Mocking strategy:** Same as CLI — define `Engine` interface in TUI package. Tests inject `mockEngine` that returns pre-constructed `Rows`/`Result`. The bubbletea `Update` function is pure and fully testable given simulated events.

**Key insight:** The TUI's complexity is in the *interaction model*, not the database logic. By isolating interaction code (input parsing, rendering, completion) from the SYS/AP interface, each piece is independently testable without spinning up a real database.

### 3. Ergonomic

The TUI is designed for **exploratory, interactive use**:

**Discoverability:**
- `F1` help shows all key bindings and dot commands
- Tab completion reveals available tables/columns
- `.help` dot command lists all dot commands
- Context-sensitive completion (after `FROM` → tables, after `.` → columns)

**Efficiency:**
- Multi-line input with smart newline detection (don't execute mid-typing)
- Ctrl+R reverse search for finding past queries
- Schema browser (`.browse`) for visual exploration
- `.eqp` auto EXPLAIN QUERY PLAN for performance debugging
- `.timer` toggle for per-query timing

**Feedback:**
- Query timing always visible (configurable via `.timer`)
- Row count and execution time shown after every query
- Performance hints for slow queries (>1s) with index suggestions
- Syntax errors shown inline with cursor position
- Connection status shown on startup and reconnection

**Comfort:**
- Configurable color themes (`~/.config/rdtui/theme.toml`)
- Adjustable font size and family
- Customizable key bindings (documented, not runtime-rebindable)
- History persists across sessions (10,000 entries, deduplicated)
- Read-only mode (`--readonly`) prevents accidental modifications

**Error handling:**
- Graceful degradation: if syntax highlighting fails, fall back to plain text
- If auto-completion query fails, disable completion silently (no error popup)
- If database is locked, show clear message: `"Database locked. Retry in 1s..."`
- Ctrl+C cancels current input (doesn't kill the TUI)
- Ctrl+D exits cleanly (confirms if unsaved work exists)

## Architecture

```
cmd/rdtui/                 # TUI package (separate binary, package main)
├── main.go                    # Entry point
├── repl.go                    # bubbletea REPL model
├── input.go                   # Multi-line input with syntax highlighting
├── table.go                   # Render query results as styled table
├── completer.go               # Auto-completion engine
├── history.go                 # Command history with search
├── dotcmds.go                 # .tables, .schema, .mode, .quit
├── theme.go                   # Color theme configuration
├── schema_browser.go          # Interactive schema browser
└── keybindings.go             # Key binding configuration
```

## Dependency Rule

```
cmd/rdtui/ ──imports──► SYS/AP (public API)
cmd/rdtui/ ──imports──► SYS/SY (engine lifecycle)
cmd/rdtui/ ──imports──► bubbletea (TUI framework)
cmd/rdtui/ ──imports──► bubbles (TUI components)
cmd/rdtui/ ──imports──► lipgloss (styling)
cmd/rdtui/ ──imports──► chroma (syntax highlighting)
cmd/rdtui/ ──DOES NOT──► SQL/EX, SQL/PS, ENG/*, TXN/*, WAL/*, MEM/*
```

## External Dependencies

| Package | Purpose | Why needed |
|---------|---------|-----------|
| `github.com/charmbracelet/bubbletea` | TUI framework | Event loop, model-update-view pattern |
| `github.com/charmbracelet/bubbles` | TUI components | Table, input, spinner, viewport |
| `github.com/charmbracelet/lipgloss` | Terminal styling | Colors, borders, layout |
| `github.com/alecthomas/chroma/v2` | Syntax highlighting | SQL keyword coloring |

## Invocation

```bash
# Launch TUI
$ rdtui mydb.razor

# Launch TUI with initial query
$ rdtui mydb.razor --query "SELECT * FROM users LIMIT 10"

# Launch TUI in read-only mode
$ rdtui mydb.razor --readonly
```

## REPL Interface

```
rdtui v0.28.0 — razordata interactive shell
Connected to: mydb.razor (3 tables, 1,234 rows)

rdtui> SELECT u.name, COUNT(o.id) AS order_count
     > FROM users u
     > LEFT JOIN orders o ON u.id = o.user_id
     > GROUP BY u.name
     > ORDER BY order_count DESC;
┌──────────┬─────────────┐
│ name     │ order_count │
├──────────┼─────────────┤
│ Alice    │          12 │
│ Bob      │           8 │
│ Charlie  │           3 │
│ Diana    │           0 │
└──────────┴─────────────┘
(4 rows, 5ms)

rdtui> _
```

## Features

### 1. Syntax Highlighting

SQL keywords, strings, numbers, operators are colored differently.

```
SELECT  → blue bold
FROM    → blue bold
WHERE   → blue bold
'text'  → green
123     → yellow
=       → red
```

Uses `chroma` SQL lexer. Theme configurable via `~/.config/rdtui/theme.toml`.

### 2. Auto-Completion

Triggered by Tab key. Context-aware:

| Context | Suggestions |
|---------|-------------|
| Start of input | SQL keywords (SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, ALTER, etc.) |
| After `FROM` | Table names (from `SELECT name FROM sqlite_master`) |
| After table name | Column names (from `PRAGMA table_info(table)`) |
| After `.` | Columns of the preceding table alias |
| After `.` dot | Schema-qualified names |

Completion engine queries the database lazily:
```go
func (c *Completer) getTables() []string {
    rows, _ := c.session.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table'")
    // cache for 30s
}

func (c *Completer) getColumns(table string) []string {
    rows, _ := c.session.Query(ctx, "PRAGMA table_info("+table+")")
    // cache for 30s
}
```

### 3. Multi-line Editing

- **Enter**: Execute query (if complete statement) or newline (if inside string/parenthesis)
- **Shift+Enter** or **Ctrl+J**: Always newline
- **Up/Down**: Navigate history
- **Ctrl+R**: Reverse search history
- **Ctrl+A/E**: Move to start/end of line
- **Ctrl+K/U**: Kill to end/start of line
- **Ctrl+L**: Clear screen

Smart newline detection:
```go
func isCompleteStatement(input string) bool {
    // Ends with ;
    // Not inside a string literal (odd number of single quotes)
    // Not inside parentheses (balanced parens)
}
```

### 4. Dot Commands

| Command | Description |
|---------|-------------|
| `.tables [pattern]` | List tables (with optional LIKE pattern) |
| `.schema [table]` | Show CREATE TABLE statement |
| `.indexes [table]` | List indexes |
| `.mode MODE` | Set output mode (table, csv, json, line, tabs) |
| `.headers on\|off` | Toggle column headers |
| `.separator SEP` | Set column separator |
| `.null TEXT` | Set NULL display string |
| `.width N1 N2 ...` | Set column widths |
| `.timer on\|off` | Toggle query timing |
| `.trace on\|off` | Toggle SQL tracing (show parsed SQL) |
| `.eqp on\|off` | Toggle automatic EXPLAIN QUERY PLAN |
| `.read FILE` | Execute SQL from file |
| `.output FILE` | Redirect output to file |
| `.once FILE` | Next query output to file |
| `.quit` / `.exit` / `\q` | Exit |

### 5. Schema Browser

`.browse` command opens an interactive schema browser:

```
┌─ Schema Browser ──────────────────────────┐
│                                            │
│  ▼ users (1,234 rows)                      │
│    ├─ id INTEGER PRIMARY KEY               │
│    ├─ name TEXT NOT NULL                    │
│    ├─ age INTEGER DEFAULT 0                │
│    └─ email TEXT UNIQUE                     │
│      └─ idx_users_email                    │
│                                            │
│  ▼ orders (567 rows)                       │
│    ├─ id INTEGER PRIMARY KEY               │
│    ├─ user_id INTEGER → users(id)          │
│    ├─ amount REAL                           │
│    └─ created_at TEXT                       │
│      └─ idx_orders_user_id                 │
│                                            │
│  ▶ products (89 rows)                      │
│                                            │
│  [Enter] Expand  [q] Close  [/] Search     │
└────────────────────────────────────────────┘
```

Uses `bubbles/treeview` or custom implementation.

### 6. Query History

- Stored in `~/.config/rdtui/history`
- Maximum 10,000 entries
- Search with Ctrl+R (fuzzy match)
- Persist across sessions
- Deduplicated (no consecutive duplicates)

### 7. Theme Configuration

`~/.config/rdtui/theme.toml`:

```toml
[colors]
keyword = "#569CD6"     # blue
string = "#6A9955"      # green
number = "#B5CEA8"      # yellow
operator = "#D4D4D4"    # white
comment = "#6A9999"     # cyan
table_header = "#FFFFFF" # white bold
table_border = "#808080" # gray

[font]
size = 14
family = "monospace"
```

### 8. Performance Hints

When a query takes > 1s, show hints:

```
rdtui> SELECT * FROM orders WHERE user_id = 1;
┌────┬─────────┬────────┬────────────────────┐
│ id │ user_id │ amount │ created_at         │
├────┼─────────┼────────┼────────────────────┤
│  1 │       1 │  99.99 │ 2026-01-15 10:30:00│
└────┴─────────┴────────┴────────────────────┘
(1 row, 1.2s)

Hint: Query took >1s. Consider adding an index:
  CREATE INDEX idx_orders_user_id ON orders (user_id);
```

## Key Bindings

| Key | Action |
|-----|--------|
| Enter | Execute query / Newline |
| Shift+Enter | Always newline |
| Tab | Auto-complete |
| Up/Down | History navigation |
| Ctrl+R | Reverse search history |
| Ctrl+A | Move to start of line |
| Ctrl+E | Move to end of line |
| Ctrl+K | Kill to end of line |
| Ctrl+U | Kill to start of line |
| Ctrl+L | Clear screen |
| Ctrl+C | Cancel current input / Exit |
| Ctrl+D | Exit (if empty input) |
| F1 | Help |
| F2 | Schema browser |
| F3 | Toggle timing |

## Output Formatting

Same formats as CLI, but with lipgloss styling:

**table** (default):
```
┌────┬──────────┬─────┐
│ id │ name     │ age │  ← white bold
├────┼──────────┼─────┤
│  1 │ Alice    │  30 │  ← default
│  2 │ Bob      │  25 │
└────┴──────────┴─────┘
(2 rows, 3ms)            ← dim
```

**json** (with syntax highlighting):
```json
[
  {"id": 1, "name": "Alice", "age": 30},
  {"id": 2, "name": "Bob", "age": 25}
]
```

## Integration with SYS/AP

Same as CLI. Uses only `SYS/AP` and `SYS/SY` public APIs.

```go
// Open engine
eng, _ := SY.Open(ctx, dbdir, AP.Options{})
defer eng.Close(ctx)

// Begin session
sess, _ := eng.Begin(ctx)
defer sess.Close()

// Execute queries
rows, _ := sess.Query(ctx, input)
result, _ := sess.Exec(ctx, input)

// Schema introspection
tables, _ := sess.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table'")
columns, _ := sess.Query(ctx, "PRAGMA table_info("+table+")")
```

### Mock Interface for Testing

Same pattern as CLI. The TUI defines local interfaces:

```go
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
```

**Testing the bubbletea loop:** The `Update` function is pure — given an `Event` and current `Model`, it returns a new `Model` and a `Cmd`. Tests call `Update` directly with synthetic events (keypress, query result, error) and verify state transitions without rendering.

**Testing rendering:** `table.go`'s `renderTable(rows *AP.Rows, style StyleConfig) string` is a pure function. Tests pass constructed `Rows` and assert exact output strings.

## Testing

### Unit Tests (no I/O)

| Package | Tests | Coverage |
|---------|-------|----------|
| `input_test.go` | Multi-line input parsing, complete-statement detection | All input logic |
| `table_test.go` | Styled table rendering with lipgloss | All render cases |
| `completer_test.go` | Auto-completion context matching | All completion contexts |
| `dotcmds_test.go` | Dot command parsing (`.tables`, `.schema`, etc.) | All dot commands |
| `history_test.go` | History persistence, search, deduplication | All history ops |
| `theme_test.go` | Theme parsing, color validation | All theme fields |

**Example: bubbletea Update test:**
```go
func TestRepl_Update_ExecuteQuery(t *testing.T) {
    mock_sess := &mockSession{
        queryResult: &AP.Rows{Cols: []string{"id"}, Data: [][]any{{1}}},
    }
    model := NewReplModel(mock_sess)
    
    // Simulate: user typed "SELECT 1" and pressed Enter
    model.input = "SELECT 1"
    model.cursor = len(model.input)
    
    // Send keypress event
    model, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
    
    assert.Equal(t, StateResults, model.state)
    assert.NotNil(t, cmd)  // Cmd executes the query
}
```

### Integration Tests (with I/O)

| Test | Method |
|------|--------|
| `repl_test.go` | End-to-end: real `SYS/AP` engine, simulated keystrokes via `tea.Simulate` |
| `schema_browser_test.go` | Real schema browsing, verify tree structure |
| `completion_e2e_test.go` | Full completion cycle: type → suggest → select → insert |

### Test Coverage Requirements

- Every TUI component (input, table, completer, dotcmds, history) has unit tests
- All bubbletea state transitions tested (idle → input → loading → results → error)
- Error paths: database unavailable, query timeout, syntax error, locked database
- Theme configuration tested: valid themes, invalid colors, missing fields
- `go test ./cmd/rdtui/... -race -cover` must pass with >75% coverage

## Build

```bash
# Build CLI (minimal deps)
go build -o rdcli ./cmd/rdcli/

# Build TUI (with charm deps)
go build -o rdtui ./cmd/rdtui/

# Build both
go build ./cmd/...
```

Separate binaries. TUI is optional — users who don't need the interactive experience don't pull in charm dependencies.

## Relationship to CLI

| Aspect | CLI (`rdcli`) | TUI (`rdtui`) |
|--------|--------------|-------------------|
| Binary | `rdcli` | `rdtui` |
| Dependencies | cobra only | bubbletea + bubbles + lipgloss + chroma |
| Startup | < 50ms | < 200ms |
| Memory | < 10 MB | < 50 MB |
| Interactive | No | Yes |
| Syntax highlight | No | Yes |
| Auto-complete | No | Yes |
| Scriptable | Yes | No |
| Pipe-friendly | Yes | No |
| Target user | Scripts, CI/CD, automation | Developers, DBAs, exploratory queries |

The TUI is a superset of the CLI's query functionality. The CLI's admin commands (backup, restore, integrity-check, vacuum, analyze) are also available in the TUI via dot commands or SQL.
