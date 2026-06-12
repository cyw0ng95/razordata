package slt

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"sort"
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
	stats      Stats
	startTime  time.Time

	// skipDB is the engine name currently filtered out by a
	// skipif record. Empty means "do not skip".
	skipDB string
	// onlyDB is the engine name required by an onlyif record.
	// Empty means "no restriction".
	onlyDB string
	// hashThreshold is the current max-result-set size before
	// values are stored as a hash. The corpus uses this to keep
	// full scripts small.
	hashThreshold int

	// labelMap accumulates the first-seen result hash for each
	// query label. Re-uses verify equivalence across queries
	// that share a label.
	labelMap map[string]string
}

// NewRunner constructs a runner bound to a driver. The classifier
// is optional; if nil, all errors are treated as failures.
func NewRunner(driver Driver, classifier Classifier) *Runner {
	if classifier == nil {
		classifier = defaultClassifier{}
	}
	return &Runner{
		driver:        driver,
		classifier:    classifier,
		labelMap:      make(map[string]string),
		hashThreshold: 0,
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
	for i := range records {
		rec := &records[i]
		r.stats.Total++
		if rec.Kind == RecordInvalid {
			r.stats.ParseErrors++
			r.stats.Skipped++
			continue
		}
		// Conditional prefixes gate the next executable record.
		// We apply them in source order: a skipif cancels the
		// current executable; an onlyif restricts it. The
		// prefix records themselves are not counted in stats
		// (they are configuration).
		if r.skipDB != "" || r.onlyDB != "" {
			// The prefix still needs to be classified: a
			// skipif that filters us out is a skip, not a
			// failure.
			r.stats.Skipped++
			// Clear the filter for the next record.
			r.skipDB = ""
			r.onlyDB = ""
			continue
		}
		switch rec.Kind {
		case RecordStatementOK:
			r.runStatementOK(ctx, rec)
		case RecordStatementError:
			r.runStatementError(ctx, rec)
		case RecordQuery:
			r.runQuery(ctx, rec)
		case RecordHalt:
			return r.finalize()
		case RecordHashThreshold:
			r.hashThreshold = rec.HashThreshold
			// The hash-threshold record itself is a config
			// directive, not an executable; do not count it
			// twice. The Total increment above is the only
			// occurrence. We treat it as a pass without
			// affecting Passed/Failed counts: subtract from
			// Total so the pass rate is not skewed.
			r.stats.Total--
		case RecordSkipIf:
			r.skipDB = rec.DBName
		case RecordOnlyIf:
			r.onlyDB = rec.DBName
		}
	}
	return r.finalize()
}

// runStatementOK dispatches a "statement ok" record.
func (r *Runner) runStatementOK(ctx context.Context, rec *Record) {
	err := r.driver.Exec(ctx, rec.SQL)
	if err == nil {
		r.stats.Passed++
		return
	}
	switch r.classifier.Classify(err) {
	case VerdictSkipped:
		r.stats.Skipped++
	default:
		r.stats.Failed++
	}
}

// runStatementError dispatches a "statement error" record: the
// engine is expected to fail.
func (r *Runner) runStatementError(ctx context.Context, rec *Record) {
	err := r.driver.Exec(ctx, rec.SQL)
	if err == nil {
		// Engine accepted a statement it should have rejected.
		r.stats.Failed++
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
	rs, err := r.driver.Query(ctx, rec.SQL)
	if err != nil {
		switch r.classifier.Classify(err) {
		case VerdictSkipped:
			r.stats.Skipped++
		default:
			r.stats.Failed++
		}
		return
	}
	if diff := DiffResultSets(rs, rec); diff != "" {
		r.stats.Failed++
		// diff is a diagnostic; it is not currently retained
		// beyond the stat. A future iteration may attach diffs
		// to a Coverage record for the failing file.
		return
	}
	// Label bookkeeping: if this query has a label, the next
	// query with the same label must produce the same hash.
	if rec.Label != "" {
		h := resultHash(rs, rec.Sort)
		if prev, ok := r.labelMap[rec.Label]; ok {
			if prev != h {
				r.stats.Failed++
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
	// Operate on a copy so the original ordering is preserved
	// for diagnostics.
	rows := make([][]Value, len(rs.Rows))
	for i, row := range rs.Rows {
		rows[i] = append([]Value(nil), row...)
	}
	if mode == RowSort || mode == ValueSort {
		sortRows(rows, mode == ValueSort)
	}
	h := md5.New()
	for _, row := range rows {
		for i, cell := range row {
			if i > 0 {
				io.WriteString(h, "\t")
			}
			io.WriteString(h, cell.String())
		}
		io.WriteString(h, "\n")
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// HashSorted is the public form of the hash used for
// "values hashing to" markers. The corpus computes an MD5 over
// the tab-joined string forms of the values in sorted order.
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
	return r.stats
}
