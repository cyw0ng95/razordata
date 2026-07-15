package slt

import (
	"context"
	"fmt"
	"hash"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Runner drives a Driver through a sequence of parsed records and
// reports a per-record verdict plus aggregate Stats.
//
// The runner is sequential. Records are processed in source order.
// Conditional prefixes (skipif / onlyif) gate the next executable
// record. Halt short-circuits the rest of the script.
//
// The runner is intentionally tolerant: any single failure
// (parse error, exec error, diff mismatch) is logged and counted
// in Stats.Failed; subsequent records continue to be processed.
type Runner struct {
	driver     Driver
	classifier Classifier
	engineName string
	stats      Stats
	startTime  time.Time

	// pendingSkip is set by a preceding skipif/onlyif directive
	// to gate the next executable record. REQ000448: the runner
	// must evaluate the directive against the bound engine name
	// at the directive, not just flip a flag.
	pendingSkip bool
	// hashThreshold is the current max-result-set size before
	// values are stored as a hash. The corpus uses this to keep
	// full scripts small.
	hashThreshold int

	// labelMap accumulates the first-seen result hash for each
	// query label. Re-uses verify equivalence across queries
	// that share a label.
	labelMap map[string]string

	// REQ000840: per-record profiling, gated on RAZOR_SLT_PROFILE=1.
	// Collects every record's wall-clock time. At finalize(), sorts
	// and returns top-20 in Stats.Slowest.
	profileOn    bool
	recordTimers []slowTimer

	// haltOnTimeout is set when a query hits context deadline exceeded.
	// Subsequent records are skipped (fast-fail for timeout cascades).
	haltOnTimeout bool

	// FailFast controls whether the runner stops on the first failure.
	// When true, any statement error, query diff mismatch, or label
	// hash mismatch sets haltOnFailure and the Run loop skips all
	// remaining records. Default false (tolerant mode).
	FailFast bool

	// haltOnFailure is set when FailFast is true and a record fails.
	// Subsequent records are skipped (fast-fail on first failure).
	haltOnFailure bool

	// splitStart/splitEnd gate executable records to a 1-indexed
	// inclusive range [splitStart, splitEnd]. Parsed from
	// RAZOR_SLT_RANGE="start:end". Non-executable records
	// (skipif, onlyif, hash-threshold) are always processed to
	// maintain correct state. Halt before the split range is
	// skipped. When splitStart and splitEnd are both 0, splitting
	// is disabled and all records run normally.
	splitStart int
	splitEnd   int
	execCount  int // counts executed executable records within range
}

// NewRunner constructs a runner bound to a driver. The classifier
// is optional; if nil, all errors are treated as failures.
//
// The engineName is the identifier the runner matches against
// skipif/onlyif directives. An empty engineName disables
// conditional skipping: every record runs. REQ000448.
func NewRunner(driver Driver, classifier Classifier, engineName string) *Runner {
	if classifier == nil {
		classifier = defaultClassifier{}
	}
	profileOn := false
	if v := os.Getenv("RAZOR_SLT_PROFILE"); v == "1" || strings.EqualFold(v, "true") {
		profileOn = true
	}
	splitStart, splitEnd := 0, 0
	if v := os.Getenv("RAZOR_SLT_RANGE"); v != "" {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) == 2 {
			if a, err := strconv.Atoi(parts[0]); err == nil {
				splitStart = a
			}
			if b, err := strconv.Atoi(parts[1]); err == nil {
				splitEnd = b
			}
		}
	}
	return &Runner{
		driver:        driver,
		classifier:    classifier,
		engineName:    engineName,
		labelMap:      make(map[string]string),
		hashThreshold: 0,
		profileOn:     profileOn,
		FailFast:      os.Getenv("RAZOR_SLT_FAILFAST") == "1",
		splitStart:    splitStart,
		splitEnd:      splitEnd,
	}
}

// defaultClassifier returns VerdictFailed for any non-nil error.
// Used when the driver does not supply its own classifier.
type defaultClassifier struct{}

func (defaultClassifier) Classify(err error) Verdict {
	if err == nil {
		return VerdictPassed
	}
	return VerdictFailed
}

// Run executes every record and returns aggregate Stats. The
// records slice is consumed in order; records of kind
// RecordInvalid are counted as parse errors and skipped.
func (r *Runner) Run(ctx context.Context, records []Record) Stats {
	r.startTime = time.Now()

	// REQ001458: batch pre-compile all SQL plans before the execution loop.
	// Collects all executable SQLs and primes the executor's stmt+plan caches,
	// so each Query/Exec call skips parse+plan overhead.
	if pc, ok := r.driver.(PlanPrecompiler); ok {
		sqls := make([]string, 0, len(records))
		for i := range records {
			rec := &records[i]
			switch rec.Kind {
			case RecordStatementOK, RecordStatementError, RecordQuery:
				if rec.SQL != "" {
					sqls = append(sqls, rec.SQL)
				}
			}
		}
		pc.Precompile(ctx, sqls)
	}

	// REQ001467: fast path for straight-through scripts — no control-flow
	// directives (skipif/onlyif/halt/hash-threshold). Skips all state
	// tracking overhead: pendingSkip, splitRange, profiling, fast-fail.
	if r.splitStart == 0 && r.splitEnd == 0 && !r.FailFast && !r.profileOn {
		if !hasControlFlow(records) {
			return r.runStraightThrough(ctx, records)
		}
	}

	for i := range records {
		rec := &records[i]
		r.stats.Total++
		if rec.Kind == RecordInvalid {
			r.stats.ParseErrors++
			r.stats.Skipped++
			continue
		}
		// Conditional prefixes gate the next executable record.
		// REQ000448: evaluate skipif/onlyif against the bound
		// engine name. pendingSkip is the unified gate: set by
		// a matching skipif or a non-matching onlyif. A gated
		// Halt is a no-op (the canonical "onlyif X halt" pattern
		// means "halt the run if engine is X").
		if r.pendingSkip {
			r.stats.Skipped++
			r.pendingSkip = false
			// Any gated record is consumed: do not run it,
			// and do not let it cascade (a gated skipif must
			// not set the next pendingSkip).
			continue
		}
		// REQ001xxx: RAZOR_SLT_RANGE gating. Only executable records
		// within [splitStart, splitEnd] are executed. Non-executable
		// records (skipif, onlyif, hash-threshold) are always
		// processed to maintain correct state. Halt before the split
		// range is skipped.
		if r.splitStart > 0 || r.splitEnd > 0 {
			if isExecutable(rec.Kind) {
				r.execCount++
				if r.splitStart > 0 && r.execCount < r.splitStart {
					r.stats.Skipped++
					continue
				}
				if r.splitEnd > 0 && r.execCount > r.splitEnd {
					return r.finalize()
				}
			} else if rec.Kind == RecordHalt {
				if r.splitStart > 0 && r.execCount < r.splitStart {
					continue // skip halt before range
				}
			}
		}
		// REQ000840: record wall-clock time for each record when profiling.
		var recStart time.Time
		if r.profileOn {
			recStart = time.Now()
		}
		switch rec.Kind {
		case RecordStatementOK:
			r.runStatementOK(ctx, rec)
		case RecordStatementError:
			r.runStatementError(ctx, rec)
		case RecordQuery:
			r.runQuery(ctx, rec)
		case RecordHalt:
			if r.profileOn {
				r.recordTimers = append(r.recordTimers, slowTimer{line: rec.Line, kind: rec.Kind, label: rec.Label, sql: rec.SQL, dur: time.Since(recStart)})
			}
			return r.finalize()
		case RecordHashThreshold:
			r.hashThreshold = rec.HashThreshold
			r.stats.Total--
		case RecordSkipIf:
			r.pendingSkip = r.engineName != "" && r.engineName == rec.DBName
		case RecordOnlyIf:
			r.pendingSkip = r.engineName != "" && r.engineName != rec.DBName
		}
		// REQ000xxx: fast-fail on first failure. Useful during debugging
		// to see the first failing record immediately instead of waiting
		// for the full corpus to finish.
		if r.haltOnFailure {
			r.stats.FailFastTriggered = true
			r.stats.Skipped += len(records) - i - 1
			if r.profileOn {
				r.recordTimers = append(r.recordTimers, slowTimer{line: rec.Line, kind: rec.Kind, label: rec.Label, sql: rec.SQL, dur: time.Since(recStart)})
			}
			return r.finalize()
		}
		// REQ001056: fast-fail on context deadline exceeded — skip all
		// remaining records (they will also time out and waste time).
		if r.haltOnTimeout {
			r.stats.Skipped += len(records) - i - 1
			if r.profileOn {
				r.recordTimers = append(r.recordTimers, slowTimer{line: rec.Line, kind: rec.Kind, label: rec.Label, sql: rec.SQL, dur: time.Since(recStart)})
			}
			return r.finalize()
		}
		if r.profileOn {
			r.recordTimers = append(r.recordTimers, slowTimer{line: rec.Line, kind: rec.Kind, label: rec.Label, sql: rec.SQL, dur: time.Since(recStart)})
		}
	}
	return r.finalize()
}

// isExecutable reports whether a record kind is the body of a
// statement/query test (i.e. consumes the pending skip gate).
// Hash-threshold, skipif, onlyif, halt are not executable.
func isExecutable(k RecordKind) bool {
	switch k {
	case RecordStatementOK, RecordStatementError, RecordQuery:
		return true
	}
	return false
}

// runStatementOK dispatches a "statement ok" record.
func (r *Runner) runStatementOK(ctx context.Context, rec *Record) {
	err := r.driver.Exec(ctx, rec.SQL)
	if err == nil {
		r.stats.Passed++
		return
	}
	// REQ001056: fast-fail on timeout — subsequent records will also
	// time out in a cascade, wasting wall-clock time on diagnosis.
	if isContextDeadlineExceeded(err) {
		r.stats.Failed++
		r.haltOnTimeout = true
		return
	}
	switch r.classifier.Classify(err) {
	case VerdictSkipped:
		r.stats.Skipped++
	default:
		r.stats.Failed++
		if r.FailFast {
			r.haltOnFailure = true
		}
	}
}

// runStatementError dispatches a "statement error" record: the
// engine is expected to fail.
func (r *Runner) runStatementError(ctx context.Context, rec *Record) {
	err := r.driver.Exec(ctx, rec.SQL)
	if err == nil {
		// Engine accepted a statement it should have rejected.
		r.stats.Failed++
		if r.FailFast {
			r.haltOnFailure = true
		}
		return
	}
	// The error here is expected; the runner treats any error as
	// success for the "statement error" record. The classifier is
	// not consulted: an "expected error" is success regardless of
	// its category.
	r.stats.Passed++
}

// runQuery dispatches a "query" record, diffs the actual result
// against the expected rows, and consults the classifier on
// engine-side errors.
func (r *Runner) runQuery(ctx context.Context, rec *Record) {
	// REQ001457: fast-path through QueryRaw when the driver supports
	// it, bypassing database/sql's Prepare + Rows + reflection.
	var rs *ResultSet
	var err error
	if rq, ok := r.driver.(RawQuerier); ok {
		rs, err = rq.QueryRaw(ctx, rec.SQL)
	} else {
		rs, err = r.driver.Query(ctx, rec.SQL)
	}
	if err != nil {
		// REQ001056: fast-fail on timeout — subsequent records will
		// also time out in a cascade, wasting wall-clock time.
		if isContextDeadlineExceeded(err) {
			r.stats.Failed++
			r.stats.FailureContext = append(r.stats.FailureContext, FailureContext{
				Line: rec.Line, Kind: rec.Kind, SQL: rec.SQL,
				Diag: fmt.Sprintf("query error: %v", err),
			})
			r.haltOnTimeout = true
			return
		}
		switch r.classifier.Classify(err) {
		case VerdictSkipped:
			r.stats.Skipped++
		default:
			r.stats.Failed++
			r.stats.FailureContext = append(r.stats.FailureContext, FailureContext{
				Line: rec.Line, Kind: rec.Kind, SQL: rec.SQL,
				Diag: fmt.Sprintf("query error: %v", err),
			})
		}
		return
	}
	if diff := DiffResultSets(rs, rec); diff != "" {
		r.stats.Failed++
		r.stats.FailureContext = append(r.stats.FailureContext, FailureContext{
			Line: rec.Line,
			Kind: rec.Kind,
			SQL:  rec.SQL,
			Diag: fmt.Sprintf("result mismatch:\n%s", diff),
		})
		if r.FailFast {
			r.haltOnFailure = true
		}
		return
	}
	// Label bookkeeping: if this query has a label, the next
	// query with the same label must produce the same hash.
	if rec.Label != "" {
		h := resultHash(rs, rec.Sort)
		if prev, ok := r.labelMap[rec.Label]; ok {
			if prev != h {
				r.stats.Failed++
				if r.FailFast {
					r.haltOnFailure = true
				}
				return
			}
		} else {
			r.labelMap[rec.Label] = h
		}
	}
	r.stats.Passed++
}

// resultHash is a stable, sort-mode-aware digest of a result set.
// Used to compare queries that share a label.
func resultHash(rs *ResultSet, mode SortMode) string {
	rows := rs.Rows
	h := md5Pool.Get().(hash.Hash)
	defer func() {
		h.Reset()
		md5Pool.Put(h)
	}()
	if mode == RowSort || mode == ValueSort {
		idx := make([]int, len(rows))
		for i := range idx {
			idx[i] = i
		}
		if mode == ValueSort {
			sort.Slice(idx, func(i, j int) bool {
				return rowString(rows[idx[i]]) < rowString(rows[idx[j]])
			})
		} else {
			// RowSort: lexicographic order on tab-joined row string,
			// matching the canonical SQLLogicTest C code's strcmp-based
			// column-by-column comparison. Using valueLess here breaks
			// hash compatibility because valueLess compares integers
			// numerically while the canonical tool uses strcmp
			// (e.g. "1254" < "223" lexicographically but 223 < 1254
			// numerically).
			sort.Slice(idx, func(i, j int) bool {
				return rowString(rows[idx[i]]) < rowString(rows[idx[j]])
			})
		}
		for _, i := range idx {
			for _, cell := range rows[i] {
				io.WriteString(h, cell.String())
				io.WriteString(h, "\n")
			}
		}
		return bytehex(h.Sum(nil))
	}
	for _, row := range rows {
		for _, cell := range row {
			io.WriteString(h, cell.String())
			io.WriteString(h, "\n")
		}
	}
	return bytehex(h.Sum(nil))
}

// HashSorted is the public form of the hash used for
// "values hashing to" markers. The corpus computes an MD5 over
// the string forms of the values in sorted order, each followed by '\n'.
func HashSorted(vs []Value) string {
	sorted := make([]Value, len(vs))
	copy(sorted, vs)
	sort.Slice(sorted, func(i, j int) bool {
		return valueLess(sorted[i], sorted[j]) < 0
	})
	return hashValues(sorted)
}

// sortRows sorts a slice of rows lexicographically. The
// ValueSort variant ignores row boundaries and sorts individual
// cells.
func sortRows(rows [][]Value, valueOnly bool) {
	if valueOnly {
		var flat []Value
		for _, row := range rows {
			flat = append(flat, row...)
		}
		sort.Slice(flat, func(i, j int) bool {
			return valueLess(flat[i], flat[j]) < 0
		})
		// Re-emit as a single-row result so the rest of the
		// pipeline (label hashing) sees a consistent shape.
		if len(flat) == 0 {
			return
		}
		rows[0] = flat
		for i := 1; i < len(rows); i++ {
			rows[i] = nil
		}
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		for k := 0; k < len(a) && k < len(b); k++ {
			if c := valueLess(a[k], b[k]); c != 0 {
				return c < 0
			}
		}
		return len(a) < len(b)
	})
}

// valueLess is a SLT-aware comparison: NULL < Text < Integer
// < Real, with Text compared by SQLLogicTest's "lexicographic
// on the rendered form" rule.
func valueLess(a, b Value) int {
	if a.Kind != b.Kind {
		order := func(k ValueKind) int {
			switch k {
			case TypeNull:
				return 0
			case TypeText:
				return 1
			case TypeInteger:
				return 2
			case TypeReal:
				return 3
			}
			return 4
		}
		return order(a.Kind) - order(b.Kind)
	}
	switch a.Kind {
	case TypeNull:
		return 0
	case TypeText:
		return strings.Compare(a.Text, b.Text)
	case TypeInteger:
		switch {
		case a.Int < b.Int:
			return -1
		case a.Int > b.Int:
			return 1
		default:
			return 0
		}
	case TypeReal:
		switch {
		case a.Real < b.Real:
			return -1
		case a.Real > b.Real:
			return 1
		default:
			return 0
		}
	}
	return 0
}

func (r *Runner) finalize() Stats {
	r.stats.Duration = Duration(time.Since(r.startTime).Milliseconds())
	if r.profileOn && len(r.recordTimers) > 0 {
		sort.Slice(r.recordTimers, func(i, j int) bool {
			return r.recordTimers[i].dur > r.recordTimers[j].dur
		})
		limit := 20
		if len(r.recordTimers) < limit {
			limit = len(r.recordTimers)
		}
		r.stats.Slowest = make([]SlowRecord, limit)
		for i := 0; i < limit; i++ {
			r.stats.Slowest[i] = SlowRecord{
				Line:  r.recordTimers[i].line,
				Kind:  r.recordTimers[i].kind,
				Label: r.recordTimers[i].label,
				SQL:   truncateStr(r.recordTimers[i].sql, 300),
				Time:  r.recordTimers[i].dur,
			}
		}
	}
	return r.stats
}

// slowTimer is an internal timing record for per-record profiling.
type slowTimer struct {
	line  int
	kind  RecordKind
	label string
	sql   string
	dur   time.Duration
}

// hasControlFlow reports whether records contain any control-flow
// directives that prevent the straight-through fast path. REQ001467.
func hasControlFlow(records []Record) bool {
	for i := range records {
		switch records[i].Kind {
		case RecordSkipIf, RecordOnlyIf, RecordHalt, RecordHashThreshold:
			return true
		}
	}
	return false
}

// runStraightThrough is a tight loop for straight-through SLT scripts
// that skips all state tracking overhead: pendingSkip, splitRange,
// profiling, fast-fail. REQ001467.
func (r *Runner) runStraightThrough(ctx context.Context, records []Record) Stats {
	var passed, failed, skipped, total int
	for i := range records {
		rec := &records[i]
		total++
		switch rec.Kind {
		case RecordInvalid:
			skipped++
			continue
		case RecordStatementOK:
			if err := r.driver.Exec(ctx, rec.SQL); err == nil {
				passed++
			} else {
				switch r.classifier.Classify(err) {
				case VerdictSkipped:
					skipped++
				default:
					failed++
					r.stats.FailureContext = append(r.stats.FailureContext, FailureContext{
						Line: rec.Line, Kind: rec.Kind, SQL: rec.SQL,
						Diag: fmt.Sprintf("exec error: %v", err),
					})
				}
			}
		case RecordStatementError:
			if err := r.driver.Exec(ctx, rec.SQL); err != nil {
				passed++ // error expected
			} else {
				failed++ // no error when one was expected
				r.stats.FailureContext = append(r.stats.FailureContext, FailureContext{
					Line: rec.Line, Kind: rec.Kind, SQL: rec.SQL,
					Diag: "expected error but got none",
				})
			}
		case RecordQuery:
			rs, err := r.driver.Query(ctx, rec.SQL)
			if err != nil {
				switch r.classifier.Classify(err) {
				case VerdictSkipped:
					skipped++
				default:
					failed++
					r.stats.FailureContext = append(r.stats.FailureContext, FailureContext{
						Line: rec.Line, Kind: rec.Kind, SQL: rec.SQL,
						Diag: fmt.Sprintf("query error: %v", err),
					})
				}
				continue
			}
			if diff := DiffResultSets(rs, rec); diff != "" {
				failed++
				r.stats.FailureContext = append(r.stats.FailureContext, FailureContext{
					Line: rec.Line, Kind: rec.Kind, SQL: rec.SQL,
					Diag: diff,
				})
			} else {
				passed++
			}
		default:
			skipped++ // unexpected record kind
		}
		if r.haltOnTimeout {
			remaining := len(records) - i - 1
			if remaining > 0 {
				skipped += remaining
			}
			break
		}
	}
	r.stats.Total = total
	r.stats.Passed = passed
	r.stats.Failed = failed
	r.stats.Skipped = skipped
	return r.finalize()
}

// isContextDeadlineExceeded detects go context deadline errors.
func isContextDeadlineExceeded(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "context deadline exceeded")
}

// truncateStr truncates s to max characters with an ellipsis suffix.
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
