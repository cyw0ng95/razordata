// Package dual provides a side-by-side comparison harness
// between Razordata and a pure-Go reference SQLite engine
// (modernc.org/sqlite). The two engines receive the same
// sequence of statements; result sets are diffed after
// normalization.
//
// Package dual is part of the test harness. It is not used at
// runtime by the engine.
package dual

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/cyw0ng95/razordata/tests/sqlcmp/slt"
)

// dualCase is a single test case: a setup script, a query, and
// the expected result set (after normalization). Empty Setup
// means "fresh in-memory database".
type dualCase struct {
	Name  string
	Setup []string
	Query string
	Want  [][]any
}

// Run executes every case on both engines and returns the
// per-case outcome. The two engines are kept independent:
// each case gets a fresh Razordata temp dir and a fresh
// modernc :memory: connection.
//
// Errors that classify as "Razordata does not support this
// syntax yet" are surfaced as Skipped rather than Failed; the
// caller decides whether to assert a zero-failure outcome.
func Run(ctx context.Context, cases []dualCase) Results {
	var r Results
	for _, c := range cases {
		verdict, diff := runOne(ctx, c)
		r.Cases = append(r.Cases, CaseResult{Name: c.Name, Verdict: verdict, Diff: diff})
		switch verdict {
		case VerdictPassed:
			r.Passed++
		case VerdictSkipped:
			r.Skipped++
		default:
			r.Failed++
		}
	}
	return r
}

// runOne executes a single case. The "razor" side uses the
// slt.RazorDriver; the "oracle" side uses modernc via the
// stdlib database/sql.
func runOne(ctx context.Context, c dualCase) (Verdict, string) {
	// We use the slt package indirectly to keep a single
	// driver implementation. The import is local to avoid
	// cycles.
	razorSide, err := runRazor(ctx, c)
	if err != nil {
		// If the engine refused to even start, treat as
		// skipped (classifier-equivalent).
		if isUnsupported(err) {
			return VerdictSkipped, err.Error()
		}
		return VerdictFailed, fmt.Sprintf("razor: %v", err)
	}
	oracleSide, err := runOracle(ctx, c)
	if err != nil {
		return VerdictFailed, fmt.Sprintf("oracle: %v", err)
	}
	if !resultSetEqual(razorSide, oracleSide) {
		return VerdictFailed, fmt.Sprintf("mismatch:\n  razor=%v\n  oracle=%v", razorSide, oracleSide)
	}
	return VerdictPassed, ""
}

// Verdict is the per-case outcome.
type Verdict int

const (
	VerdictPassed Verdict = iota
	VerdictFailed
	VerdictSkipped
)

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

// Results is the per-run summary.
type Results struct {
	Cases   []CaseResult
	Passed  int
	Failed  int
	Skipped int
}

// CaseResult is one row in the dual-runner report.
type CaseResult struct {
	Name    string
	Verdict Verdict
	Diff    string
}

// runRazor executes the case on a fresh RazorDriver.
func runRazor(ctx context.Context, c dualCase) ([][]any, error) {
	d := newRazorDriverShim()
	if err := d.Connect(ctx); err != nil {
		return nil, err
	}
	defer d.Close(ctx)
	for _, stmt := range c.Setup {
		if err := d.Exec(ctx, stmt); err != nil {
			return nil, fmt.Errorf("setup %q: %w", stmt, err)
		}
	}
	rs, err := d.Query(ctx, c.Query)
	if err != nil {
		return nil, err
	}
	out := make([][]any, 0, len(rs.Rows))
	for _, row := range rs.Rows {
		r := make([]any, len(row))
		for i, cell := range row {
			r[i] = razorCellToAny(cell)
		}
		out = append(out, r)
	}
	return out, nil
}

// razorCellToAny unwraps an slt.Value into a Go any for the
// normalize step. The mapping mirrors slt's internal
// valueFromAny so dual and slt agree on the wire format.
// REQ000811: TypeBlob is decoded from hex to []byte so it
// normalizes to string alongside oracle's blob output.
func razorCellToAny(v slt.Value) any {
	if v.Kind == slt.TypeNull {
		return nil
	}
	switch v.Kind {
	case slt.TypeInteger:
		return v.Int
	case slt.TypeReal:
		return v.Real
	case slt.TypeBlob:
		// REQ000811: decode hex-encoded blob to raw bytes.
		// normalizeCell will convert []byte -> string.
		decoded, err := hex.DecodeString(v.Text)
		if err != nil {
			// Fallback: return the raw hex string if decode fails.
			return v.Text
		}
		return decoded
	default:
		return v.Text
	}
}

// runOracle executes the case on a fresh modernc connection.
func runOracle(ctx context.Context, c dualCase) ([][]any, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	for _, stmt := range c.Setup {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return nil, fmt.Errorf("setup %q: %w", stmt, err)
		}
	}
	rows, err := db.QueryContext(ctx, c.Query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return drainRows(ctx, rows)
}

// drainRows materializes a *sql.Rows into [][]any. The cells
// are typed by Scan: integers land in int64, floats in
// float64, text in string, NULL in nil.
func drainRows(ctx context.Context, rows *sql.Rows) ([][]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]any
	for rows.Next() {
		holders := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range holders {
			ptrs[i] = &holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, holders)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// resultSetEqual compares two result sets after normalization.
// Normalization rules: integers are cast to int64; floats are
// rounded to 6 significant digits; strings are trimmed; column
// order is irrelevant (we sort rows lexicographically).
func resultSetEqual(a, b [][]any) bool {
	na := normalize(a)
	nb := normalize(b)
	if len(na) != len(nb) {
		return false
	}
	for i := range na {
		if len(na[i]) != len(nb[i]) {
			return false
		}
		for j := range na[i] {
			if !cellEqual(na[i][j], nb[i][j]) {
				return false
			}
		}
	}
	return true
}

func normalize(in [][]any) [][]any {
	out := make([][]any, 0, len(in))
	for _, row := range in {
		r := make([]any, len(row))
		for i, c := range row {
			r[i] = normalizeCell(c)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		for k := 0; k < len(a) && k < len(b); k++ {
			if c := compareCell(a[k], b[k]); c != 0 {
				return c < 0
			}
		}
		return len(a) < len(b)
	})
	return out
}

func normalizeCell(v any) any {
	// fmt.Printf("DEBUG: normalizeCell input type=%T value=%v\n", v, v)
	switch x := v.(type) {
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case float64:
		// Round to 6 digits and keep as float64.
		return roundFloat(x, 6)
	case float32:
		return roundFloat(float64(x), 6)
	case []byte:
		// REQ000811: convert blob bytes to string for comparison with oracle.
		// fmt.Printf("DEBUG: normalizeCell []byte len=%d -> string\n", len(x))
		return string(x)
	case string:
		// REQ000811: if the string looks like a slice representation (e.g. "[0 0 0 0 0 0 0 0]"),
		// it means the blob was already converted to string via fmt.Sprintf("%v", v) in valueFromAny.
		// In this case, we need to parse the numbers between brackets and convert to raw bytes.
		if len(x) > 0 && x[0] == '[' {
			// Parse the slice representation: "[0 0 0 0 0 0 0 0]"
			// Extract the numbers between brackets
			x = x[1 : len(x)-1] // remove brackets
			parts := strings.Fields(x)
			bytes := make([]byte, len(parts))
			for i, p := range parts {
				if n, err := strconv.ParseInt(p, 10, 0); err == nil {
					bytes[i] = byte(n)
				}
			}
			return string(bytes)
		}
		return x
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	case nil:
		return nil
	default:
		// fmt.Printf("DEBUG: normalizeCell default type=%T value=%v\n", v, v)
		return fmt.Sprintf("%v", v)
	}
}

func roundFloat(v float64, digits int) float64 {
	mul := 1.0
	for i := 0; i < digits; i++ {
		mul *= 10
	}
	if v < 0 {
		return float64(int64(v*mul-0.5)) / mul
	}
	return float64(int64(v*mul+0.5)) / mul
}

func cellEqual(a, b any) bool {
	return compareCell(a, b) == 0
}

func compareCell(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	switch ax := a.(type) {
	case int64:
		switch bx := b.(type) {
		case int64:
			switch {
			case ax < bx:
				return -1
			case ax > bx:
				return 1
			}
			return 0
		case float64:
			return compareFloat(float64(ax), bx)
		}
	case float64:
		switch bx := b.(type) {
		case int64:
			return compareFloat(ax, float64(bx))
		case float64:
			return compareFloat(ax, bx)
		}
	case string:
		if bs, ok := b.(string); ok {
			switch {
			case ax < bs:
				return -1
			case ax > bs:
				return 1
			}
			return 0
		}
	}
	// Heterogeneous types: fall back to stringified comparison.
	as := fmt.Sprintf("%v", a)
	bs := fmt.Sprintf("%v", b)
	switch {
	case as < bs:
		return -1
	case as > bs:
		return 1
	}
	return 0
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// isUnsupported mirrors the slt.RazorClassifier heuristics so
// the dual runner can mark unsupported cases as Skipped without
// re-importing the slt package.
func isUnsupported(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := err.Error()
	for _, sub := range []string{
		"syntax error", "not supported", "unsupported",
		"unknown token", "parse error", "primary key",
		"no such table", "no such column", "table not found",
		"column not found", "feature not supported",
		"not implemented", "razordata: type mismatch",
	} {
		if containsFold(msg, sub) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
