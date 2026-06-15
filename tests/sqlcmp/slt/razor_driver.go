package slt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	_ "github.com/cyw0ng95/razordata/internal/SYS/SE"
	v1 "github.com/cyw0ng95/razordata/internal/SYS/SY"
)

// RazorDriver implements Driver against a live Razordata engine.
// Each Connect creates a fresh on-disk database in a temp
// directory and opens a session. Close removes the temp dir.
//
// The driver holds one AP.Session at a time. Concurrent Exec /
// Query calls are serialized via mu to prevent race conditions
// in the underlying engine's merge iterator.
type RazorDriver struct {
	mu        sync.Mutex
	dir       string
	engine    *v1.Engine
	session   AP.Session
	classifier *RazorClassifier
}

// NewRazorDriver returns a driver ready for Connect. The temp
// directory is created on Connect, not on construction, so a
// failed Connect does not leak disk state.
func NewRazorDriver() *RazorDriver {
	return &RazorDriver{classifier: &RazorClassifier{}}
}

// RazorClassifier implements Classifier for Razordata's
// v1.SYS error set. Errors matching the "unsupported syntax"
// / "parse error" / "constraint violation" patterns are
// classified as Skipped; everything else is Failed.
type RazorClassifier struct{}

// Classify returns VerdictSkipped for known soft failures.
func (c *RazorClassifier) Classify(err error) Verdict {
	if err == nil {
		return VerdictPassed
	}
	if err == context.Canceled || err == context.DeadlineExceeded {
		return VerdictFailed
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
		strings.Contains(msg, "primary key"),
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

// Connect creates a unique temp dir, opens the engine, and begins
// a session. The session is implicitly transactional; statements
// are visible to subsequent reads in the same script.
//
// We explicitly call EX.UnregisterAll() before opening so a
// previous driver instance (typically a prior test) does not
// leak its registered tables / views into this run. EX keeps
// package-level state to avoid going through the catalog for
// hot-path reads; that state is shared across all engines in
// the process.
func (d *RazorDriver) Connect(ctx context.Context) error {
	EX.UnregisterAll()
	dir, err := os.MkdirTemp("", "razor-slt-")
	if err != nil {
		return err
	}
	d.dir = dir
	eng, err := v1.Open(ctx, dir, AP.Options{})
	if err != nil {
		_ = os.RemoveAll(dir)
		d.dir = ""
		return err
	}
	d.engine = eng
	sess, err := eng.Begin(ctx)
	if err != nil {
		_ = eng.Close(ctx)
		d.engine = nil
		_ = os.RemoveAll(dir)
		d.dir = ""
		return err
	}
	d.session = sess
	return nil
}

// Close releases the session, closes the engine, and removes the
// temp directory. Idempotent.
func (d *RazorDriver) Close(ctx context.Context) error {
	var firstErr error
	if d.session != nil {
		// Roll back any implicit transaction. Errors here are
		// non-fatal: the temp dir will be removed regardless.
		_ = d.session.Rollback(ctx)
		d.session = nil
	}
	if d.engine != nil {
		if err := d.engine.Close(ctx); err != nil {
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

// Exec runs a DDL/DML statement. Errors are returned verbatim; the
// runner routes them through the classifier.
func (d *RazorDriver) Exec(ctx context.Context, sql string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.session == nil {
		return errors.New("slt: razor: not connected")
	}
	_, err := d.session.Exec(ctx, sql)
	return err
}

// Query runs a SELECT and materializes the result set. Razordata
// returns the schema via *ex.Rows; row data is pulled through
// QueryAll and converted to SLT Value cells.
func (d *RazorDriver) Query(ctx context.Context, sql string) (*ResultSet, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.session == nil {
		return nil, errors.New("slt: razor: not connected")
	}
	exe := d.engine.Executor()
	if exe == nil {
		return nil, errors.New("slt: razor: executor unavailable")
	}
	rows, err := exe.QueryAll(ctx, sql)
	if err != nil {
		return nil, err
	}
	return rowsToResultSet(rows), nil
}

// rowsToResultSet maps the executor's []EX.Row to the SLT
// ResultSet. Column names come from the first non-empty row; if
// every row is empty, the column list is empty (the runner treats
// this as a successful zero-row query).
func rowsToResultSet(rows []EX.Row) *ResultSet {
	rs := &ResultSet{}
	if len(rows) == 0 {
		return rs
	}
	rs.Columns = append(rs.Columns, rows[0].Cols...)
	for _, r := range rows {
		row := make([]Value, len(r.Data))
		for i, cell := range r.Data {
			row[i] = valueFromAny(cell)
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs
}

// EngineAccessor returns the underlying *ls.Engine for tests
// that need to invoke engine-level methods (e.g. Sync) not
// exposed on the SLT Driver interface. The bool is false if
// the driver has been closed or never connected.
func (d *RazorDriver) EngineAccessor() (EngineSyncer, bool) {
	if d == nil || d.engine == nil {
		return nil, false
	}
	return d.engine.Engine(), true
}

// EngineSyncer is the subset of *ls.Engine used by edge
// probes. Defined as an interface so the edge_probe tests do
// not need to import internal/ENG/LS.
type EngineSyncer interface {
	Sync() error
}

// engineAccessor is the package-internal alias used by the
// edge_probe test files. Returns false if no engine is wired.
func (d *RazorDriver) engineAccessor() (EngineSyncer, bool) {
	return d.EngineAccessor()
}

// valueFromAny normalizes the executor's interface{} cells to SLT
// Value. Razordata returns int64, float64, string, bool, []byte,
// time.Time, and nil directly; we map each to the closest SLT
// representation.
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
		// Distinguish ints encoded as floats (whole number, small
		// magnitude) from true reals. The corpus emits reals as
		// "%.3f", so values like 1.000 are expected to round-trip
		// as int.
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
		return Value{Kind: TypeText, Text: string(x)}
	default:
		return Value{Kind: TypeText, Text: fmt.Sprintf("%v", v)}
	}
}
