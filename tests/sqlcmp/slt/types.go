// Package slt implements the SQLLogicTest test format for Razordata.
//
// The format is documented at
// https://www.sqlite.org/sqllogictest/doc/trunk/about.wiki. We
// consume "full scripts" (statement + expected result rows) only;
// we do not consume prototype scripts or run the completion phase.
//
// This package is part of the test harness (tests/sqlcmp/slt) and
// never depends on internal Razordata packages other than the
// public AP surface re-exported via tests/sqlcmp/sqlcmp.go.
package slt

import (
	"strconv"
	"strings"
	"time"
)

// RecordKind enumerates the record types in a SQLLogicTest file.
type RecordKind int

const (
	RecordInvalid RecordKind = iota
	RecordStatementOK
	RecordStatementError
	RecordQuery
	RecordHalt
	RecordHashThreshold
	RecordSkipIf
	RecordOnlyIf
)

// String renders a RecordKind for diagnostics.
func (k RecordKind) String() string {
	switch k {
	case RecordStatementOK:
		return "statement ok"
	case RecordStatementError:
		return "statement error"
	case RecordQuery:
		return "query"
	case RecordHalt:
		return "halt"
	case RecordHashThreshold:
		return "hash-threshold"
	case RecordSkipIf:
		return "skipif"
	case RecordOnlyIf:
		return "onlyif"
	default:
		return "invalid"
	}
}

// SortMode controls how a query's result set is ordered before
// comparison. The default is NoSort.
type SortMode int

const (
	NoSort SortMode = iota
	RowSort
	ValueSort
)

// Record is one parsed record in a SQLLogicTest file. Records are
// delimited by blank lines; comments (lines starting with "#") are
// stripped before parsing.
type Record struct {
	Kind RecordKind
	// SQL is the statement text for StatementOK/StatementError/Query.
	SQL string
	// TypeString is the per-column type code ("T", "I", "R") for
	// Query. Each character maps to a column.
	TypeString string
	// Sort is the sort mode for Query.
	Sort SortMode
	// Label is the optional <label> tag for Query.
	Label string
	// HashThreshold is the value of a hash-threshold record.
	HashThreshold int
	// DBName is the target engine name for SkipIf/OnlyIf.
	DBName string
	// Expected are the rows the test asserts. For Query, these are
	// parsed from the lines after "----".
	Expected [][]Value
	// Line is the 1-indexed line of the first non-comment line of
	// the record in the source file. Used for diagnostics.
	Line int
}

// ValueKind is the typed value representation used by both expected
// and actual result cells.
type ValueKind int

const (
	TypeText ValueKind = iota
	TypeInteger
	TypeReal
	TypeNull
	TypeBlob // REQ000703: B type code for blob
)

// Value is a typed result cell. Only one of Text/Int/Real is
// meaningful at a time, governed by Kind.
type Value struct {
	Kind ValueKind
	Text string
	Int  int64
	Real float64
}

// String renders a Value in the same textual form the SQLLogicTest
// format uses, so expected and actual values can be compared with
// byte-exact equality after @-escape.
func (v Value) String() string {
	switch v.Kind {
	case TypeNull:
		return "NULL"
	case TypeInteger:
		return strconv.FormatInt(v.Int, 10)
	case TypeReal:
		return strconv.FormatFloat(v.Real, 'f', 3, 64)
	case TypeBlob: // REQ000703: blob rendered as hex string
		return v.Text
	default:
		return escapeText(v.Text)
	}
}

// escapeText replaces non-printable characters with '@', matching
// the SLT corpus rendering rule.
func escapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			b.WriteByte('@')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ParseValue decodes a single token from a result row according to
// the column's type code.
func ParseValue(tok, typeCode string) (Value, error) {
	tok = strings.TrimSpace(tok)
	if tok == "NULL" {
		return Value{Kind: TypeNull}, nil
	}
	if tok == "(empty)" {
		return Value{Kind: TypeText, Text: ""}, nil
	}
	switch typeCode {
	case "T":
		return Value{Kind: TypeText, Text: unescapeText(tok)}, nil
	case "I":
		// Some corpora emit integer-like reals (e.g. "1.000"). Be
		// lenient: if it parses as int64, use int; otherwise
		// round to nearest int.
		if i, err := strconv.ParseInt(tok, 10, 64); err == nil {
			return Value{Kind: TypeInteger, Int: i}, nil
		}
		if f, err := strconv.ParseFloat(tok, 64); err == nil {
			return Value{Kind: TypeInteger, Int: int64(f)}, nil
		}
		return Value{}, &ParseError{Msg: "expected integer: " + tok}
	case "R":
		// Reals render as "%.3f" in the corpus, so 3-decimal
		// parsing is exact.
		f, err := strconv.ParseFloat(tok, 64)
		if err != nil {
			return Value{}, &ParseError{Msg: "expected real: " + tok}
		}
		return Value{Kind: TypeReal, Real: f}, nil
	case "B": // REQ000703: blob type — stored as text (hex-encoded)
		return Value{Kind: TypeBlob, Text: tok}, nil
	default:
		return Value{}, &ParseError{Msg: "unknown type code: " + typeCode}
	}
}

// unescapeText reverses the corpus' @-escaping of control chars.
// The corpus uses '@' for both control chars and literal '@'
// (doubled). We undo both.
func unescapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '@' && i+1 < len(s) && s[i+1] == '@' {
			b.WriteByte('@')
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ParseError describes a record-level parse failure. The runner
// logs it as a parse error and continues.
type ParseError struct {
	Msg string
}

func (e *ParseError) Error() string { return "slt: parse: " + e.Msg }

// Stats is the per-run summary emitted by Runner.Run.
type Stats struct {
	Total       int
	Passed      int
	Failed      int
	Skipped     int
	ParseErrors int
	Duration    Duration
	Slowest     []SlowRecord // populated when RAZOR_SLT_PROFILE=1
}

// SlowRecord captures the wall-clock time of a single SLT record
// for profiling slow queries. Only populated when RAZOR_SLT_PROFILE=1.
type SlowRecord struct {
	Line  int
	Kind  RecordKind
	Label string
	SQL   string
	Time  time.Duration
}

// Duration is a time.Duration in milliseconds, kept as int64 so the
// JSON snapshot (coverage.json) is portable.
type Duration int64

// SortModeString is the textual form used in query records.
func (s SortMode) String() string {
	switch s {
	case RowSort:
		return "rowsort"
	case ValueSort:
		return "valuesort"
	default:
		return "nosort"
	}
}

// ParseSortMode decodes the optional sort mode token.
func ParseSortMode(s string) SortMode {
	switch s {
	case "rowsort":
		return RowSort
	case "valuesort":
		return ValueSort
	default:
		return NoSort
	}
}
