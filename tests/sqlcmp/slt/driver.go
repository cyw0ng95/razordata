package slt

import (
	"context"
)

// Driver is the contract a database engine must implement to be
// driven by the SQLLogicTest runner.
//
// The lifecycle matches the SLT contract: every script starts with
// a fresh, empty database. Connect opens that database; Close
// tears it down. The runner will not call Exec or Query after
// Close, and never on a disconnected driver.
//
// Exec and Query return errors for any engine-side failure.
// Unsupported syntax should be reported as a non-nil error; the
// runner classifies errors as "skipped" vs "failed" via the
// driver-supplied Classifier.
type Driver interface {
	Connect(ctx context.Context) error
	Close(ctx context.Context) error
	Exec(ctx context.Context, sql string) error
	Query(ctx context.Context, sql string) (*ResultSet, error)
	// Reset drops all user tables/schemas and returns the engine to a
	// clean post-Connect state without tearing down the connection.
	// REQ001454. Optional — the Runner falls back to Close+Connect if
	// the driver does not implement Reset.
	Reset(ctx context.Context) error
}

// ResultSet is the materialized output of a SELECT-like query.
// Rows preserves engine-native ordering when Sort is NoSort.
// Columns names are taken from the engine's reported schema; the
// type codes ("T", "I", "R") are normalized to ValueKind.
type ResultSet struct {
	Columns []string
	Rows    [][]Value
}

// Classifier maps an engine-returned error to a runner-visible
// verdict. A driver may implement this to advertise specific
// errors as "unsupported" (skipped) rather than failures.
type Classifier interface {
	Classify(err error) Verdict
}

// Verdict is the per-record outcome the runner reports.
type Verdict int

const (
	VerdictPassed Verdict = iota
	VerdictFailed
	VerdictSkipped
)

// String renders a Verdict for stats output.
func (v Verdict) String() string {
	switch v {
	case VerdictPassed:
		return "PASS"
	case VerdictFailed:
		return "FAIL"
	default:
		return "SKIP"
	}
}

// RazorEngineName is the identifier the SLT runner matches
// against skipif/onlyif directives. REQ000448: any directive
// that does not equal this string is treated as "not our engine"
// and is honored (skipif X for X != razor is a no-op; onlyif X
// for X != razor skips the next record).
const RazorEngineName = "razor"
