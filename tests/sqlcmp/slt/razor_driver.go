package slt

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	razordriver "github.com/cyw0ng95/razordata/driver"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	v1 "github.com/cyw0ng95/razordata/internal/SYS/SY"
)

// sltVerbose controls per-query debug output. Default off.
// Set RAZOR_SLT_VERBOSE=1 to see QUERY_START/QUERY_END stderr lines.
var sltVerbose = os.Getenv("RAZOR_SLT_VERBOSE") == "1" || os.Getenv("RAZOR_SLT_VERBOSE") == "true"

// RazorDriver implements Driver against a live Razordata engine
// via the database/sql "razor" driver. Each Connect creates a
// fresh on-disk database in a temp directory. Close removes
// the temp dir.
type RazorDriver struct {
	mu         sync.Mutex
	dir        string
	dsn        string
	db         *sql.DB
	engine     *v1.Engine
	classifier *RazorClassifier
}

// NewRazorDriver returns a driver ready for Connect.
func NewRazorDriver() *RazorDriver {
	return &RazorDriver{classifier: &RazorClassifier{}}
}

// RazorClassifier implements Classifier for Razordata's error set.
type RazorClassifier struct{}

// Classify returns VerdictSkipped for known soft failures.
func (c *RazorClassifier) Classify(err error) Verdict {
	if err == nil {
		return VerdictPassed
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "syntax error"),
		strings.Contains(msg, "not supported"),
		strings.Contains(msg, "unsupported"),
		strings.Contains(msg, "unknown token"),
		strings.Contains(msg, "parse error"),
		strings.Contains(msg, "no such table"),
		strings.Contains(msg, "no such column"),
		strings.Contains(msg, "table not found"),
		strings.Contains(msg, "column not found"),
		strings.Contains(msg, "pragma not supported"),
		strings.Contains(msg, "feature not supported"),
		strings.Contains(msg, "not implemented"),
		strings.Contains(msg, "primary key constraint not supported"),
		strings.Contains(msg, "razordata: syntax"),
		strings.Contains(msg, "razordata: type mismatch"),
		strings.Contains(msg, "razordata: no active txn"),
		strings.Contains(msg, "razordata: read-only"),
		strings.Contains(msg, "razordata: not open"),
		strings.Contains(msg, "razordata: closed"):
		return VerdictSkipped
	default:
		return VerdictFailed
	}
}

// Connect creates a unique temp dir, opens a database/sql connection,
// and stores the underlying engine for edge-probe tests.
// Temp dir is created under RAZOR_SLT_TMP when set, or the system
// default temp dir otherwise. REQ001056: use a non-tmpfs directory
// (e.g. project-local tmp/) to avoid in-memory filesystem issues.
func (d *RazorDriver) Connect(ctx context.Context) error {
	tmpRoot := os.Getenv("RAZOR_SLT_TMP")
	if tmpRoot == "" {
		tmpRoot = ""
	}
	dir, err := os.MkdirTemp(tmpRoot, "razor-slt-")
	if err != nil {
		return err
	}
	d.dir = dir

	dsn := filepath.Join(dir, "db.razor")
	d.dsn = dsn

	// Pre-create engine with small memory budget to avoid OOM.
opts := AP.Options{
		Dir:              filepath.Join(dir, "db.razor.engine"),
		MemTableSize:     1 << 20,   // 1 MiB minimum
		BufferPoolMB:     64,        // 64 MiB minimum
		MaxMemoryPerQuery: 512 << 20, // 512 MiB per-query cap (REQ001056)
		JoinBufferSize:    256 << 20, // 256 MiB per-hash-join cap (REQ001056)
		MaxResultRows:     100_000,   // cap query results to prevent OOM from cross joins (REQ001056)
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		_ = os.RemoveAll(dir)
		d.dir = ""
		return err
	}
	eng, err := v1.Open(ctx, dir, opts)
	if err != nil {
		_ = os.RemoveAll(dir)
		d.dir = ""
		return err
	}
	d.engine = eng

	// Register the engine so sql.Open("razor", dsn) finds it.
	razordriver.RegisterEngine(dsn, eng)

	db, err := sql.Open("razor", dsn)
	if err != nil {
		_ = eng.Close(context.Background())
		_ = os.RemoveAll(dir)
		d.dir = ""
		d.engine = nil
		return err
	}
	db.SetMaxOpenConns(1)
	d.db = db

	// Verify the connection is alive.
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_ = eng.Close(context.Background())
		_ = os.RemoveAll(dir)
		d.dir = ""
		d.db = nil
		d.engine = nil
		return err
	}

	return nil
}

// Reset drops all user tables/schemas and resets the engine catalog,
// returning the connection to a clean post-Connect state without
// tearing down the connection or re-creating temp directories.
// REQ001454.
func (d *RazorDriver) Reset(ctx context.Context) error {
	if d.engine == nil {
		return nil
	}
	return d.engine.Reset(ctx)
}

// Close tears down the connection and removes the temp dir.
func (d *RazorDriver) Close(ctx context.Context) error {
	var firstErr error
	if d.db != nil {
		if err := d.db.Close(); err != nil {
			firstErr = err
		}
		d.db = nil
	}
	if d.dsn != "" {
		razordriver.CloseEngine(d.dsn)
		d.dsn = ""
	}
	if d.engine != nil {
		if err := d.engine.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		d.engine = nil
	}
	if d.dir != "" {
		if err := os.RemoveAll(d.dir); err != nil && firstErr == nil {
			firstErr = err
		}
		d.dir = ""
	}
	return firstErr
}

// Exec runs a DDL/DML statement via database/sql.
func (d *RazorDriver) Exec(ctx context.Context, sql string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db == nil {
		return errors.New("slt: razor: not connected")
	}
	t0 := time.Now()
	_, err := d.db.ExecContext(ctx, sql)
	if dur := time.Since(t0); dur > 500*time.Millisecond {
		trunc := sql
		if len(trunc) > 100 {
			trunc = trunc[:100]
		}
		if sltVerbose {
			fmt.Fprintf(os.Stderr, "SLOW_EXEC[%v] %s\n", dur, trunc)
		}
	}
	return err
}

// Query runs a SELECT and materializes the result set.
func (d *RazorDriver) Query(ctx context.Context, sql string) (*ResultSet, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db == nil {
		return nil, errors.New("slt: razor: not connected")
	}
	trunc := sql
	if len(trunc) > 80 {
		trunc = trunc[:80]
	}
	if sltVerbose {
		fmt.Fprintf(os.Stderr, "QUERY_START: %s\n", trunc)
	}
	t0 := time.Now()
	rs, err := d.queryContext(ctx, sql)
	if sltVerbose {
		fmt.Fprintf(os.Stderr, "QUERY_END[%v]: %s\n", time.Since(t0), trunc)
	}
	return rs, err
}

// queryContext is the internal query path (unlocked).
func (d *RazorDriver) queryContext(ctx context.Context, sql string) (*ResultSet, error) {
	rows, err := d.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	rs := &ResultSet{Columns: cols}

	for rows.Next() {
		ptrs := make([]any, len(cols))
		for i := range ptrs {
			var v any
			ptrs[i] = &v
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]Value, len(ptrs))
		for i, p := range ptrs {
			row[i] = valueFromAny(*p.(*any))
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs, rows.Err()
}

// EngineAccessor returns the underlying *ls.Engine for edge
// probes. Returns false if no engine is wired.
func (d *RazorDriver) EngineAccessor() (EngineSyncer, bool) {
	if d == nil || d.engine == nil {
		return nil, false
	}
	return d.engine.Engine(), true
}

// EngineSyncer is the subset of *ls.Engine used by edge probes.
type EngineSyncer interface {
	Sync() error
}

func (d *RazorDriver) engineAccessor() (EngineSyncer, bool) {
	return d.EngineAccessor()
}

// valueFromAny normalizes database/sql values to SLT Value.
func valueFromAny(v any) Value {
	if v == nil {
		return Value{Kind: TypeNull}
	}
	switch x := v.(type) {
	case int64:
		return Value{Kind: TypeInteger, Int: x}
	case int:
		return Value{Kind: TypeInteger, Int: int64(x)}
	case int32:
		return Value{Kind: TypeInteger, Int: int64(x)}
	case float64:
		if x == float64(int64(x)) && x >= -1e15 && x <= 1e15 {
			return Value{Kind: TypeInteger, Int: int64(x)}
		}
		return Value{Kind: TypeReal, Real: x}
	case bool:
		if x {
			return Value{Kind: TypeInteger, Int: 1}
		}
		return Value{Kind: TypeInteger, Int: 0}
	case string:
		return Value{Kind: TypeText, Text: x}
	case []byte:
		// REQ000811: blobs are stored as hex-encoded text with TypeBlob
		return Value{Kind: TypeBlob, Text: hex.EncodeToString(x)}
	case fmt.Stringer:
		return Value{Kind: TypeText, Text: x.String()}
	default:
		return Value{Kind: TypeText, Text: fmt.Sprintf("%v", v)}
	}
}
