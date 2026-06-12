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
