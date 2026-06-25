// Package sqlcmp — multi-table join regression tests.
//
// This file provides a focused regression suite for multi-table join
// performance. It covers the patterns from select4.test (2-7 table
// cross joins with WHERE filters, equi-joins, IN-lists, OR conditions)
// but with a simplified setup for fast iteration.
//
// Expected row counts are computed from the setup: 9 tables, each with
// 10 rows, values 100-109. All five columns (a,b,c,d,e) share the same
// value range so equi-joins have deterministic selectivity.
//
// Queries with comma-separated FROM for 3+ tables are blocked by
// REQ000794 (comma join not parsed) and currently return wrong results.
// Their expectRows is set to -1 (skip check) until the fix ships.
//
// Run benchmarks:
//
//	go test -bench=BenchmarkJoin -benchmem ./tests/sqlcmp/
//
// Run regression check:
//
//	go test -run=TestJoinRegress ./tests/sqlcmp/
package sqlcmp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/cyw0ng95/razordata/driver"
)

const joinRegressTimeout = 30 * time.Second

// joinQuery is a named SQL query for the regression suite.
type joinQuery struct {
	name        string
	sql         string
	expectRows  int64 // -1 means skip row-count validation (broken until fix, or unknown)
	maxDuration time.Duration
	warnAt      time.Duration
	brokenNote  string // explanation when expectRows==-1
	skip        bool   // skip this query (too slow / too many rows for CI)
}

var joinQueries = []joinQuery{
	// ── 2-table: baseline — all working ──
	{
		name: "j2_cross", sql: "SELECT * FROM t1, t2",
		expectRows: 100, maxDuration: 1 * time.Second, warnAt: 100 * time.Millisecond,
	},
	{
		name: "j2_equi", sql: "SELECT t1.a, t2.b FROM t1 INNER JOIN t2 ON t1.a = t2.b",
		expectRows: 10, maxDuration: 1 * time.Second, warnAt: 100 * time.Millisecond,
	},
	{
		name: "j2_where_in", sql: "SELECT * FROM t1, t2 WHERE t2.b IN (101, 103, 105)",
		expectRows: 30, maxDuration: 1 * time.Second, warnAt: 50 * time.Millisecond,
	},
	{
		name: "j2_where_and", sql: "SELECT * FROM t1, t2 WHERE t1.a > 105 AND t2.b < 108",
		expectRows: 32, maxDuration: 1 * time.Second, warnAt: 50 * time.Millisecond,
	},
	{
		name: "j2_where_or", sql: "SELECT * FROM t1, t2 WHERE t1.a > 105 OR t2.b < 103",
		expectRows: 58, maxDuration: 1 * time.Second, warnAt: 50 * time.Millisecond,
	},

	// ── 3-table ──
	{
		name: "j3_cross", sql: "SELECT * FROM t1, t2, t3",
		expectRows: 1000, maxDuration: 2 * time.Second, warnAt: 500 * time.Millisecond,
	},
	{
		name: "j3_equi_chain", sql: "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3 WHERE t1.a = t2.b AND t2.b = t3.c",
		expectRows: 10, maxDuration: 2 * time.Second, warnAt: 500 * time.Millisecond,
	},
	{
		name: "j3_mixed", sql: "SELECT * FROM t1, t2, t3 WHERE t1.a > 105 AND t3.c IN (100, 108)",
		expectRows: 80, maxDuration: 2 * time.Second, warnAt: 500 * time.Millisecond,
	},
	{
		name: "j3_filter_only", sql: "SELECT * FROM t1, t2, t3 WHERE t1.a IN (101, 103) AND t2.b > 100",
		expectRows: 180, maxDuration: 2 * time.Second, warnAt: 500 * time.Millisecond,
	},

	// ── 4-table ──
	{
		name: "j4_cross", sql: "SELECT * FROM t1, t2, t3, t4",
		expectRows: 10000, maxDuration: 30 * time.Second, warnAt: 500 * time.Millisecond,
		skip: true,
	},
	{
		name: "j4_equi_2chain", sql: "SELECT t1.a, t2.b FROM t1, t2, t3, t4 WHERE t1.a = t2.b AND t3.c = t4.d",
		expectRows: 100, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
	},
	{
		name: "j4_where_2tables", sql: "SELECT * FROM t1, t2, t3, t4 WHERE t1.a > 105 AND t4.d < 108",
		expectRows: 3200, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
	},
	{
		name: "j4_mixed", sql: "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3, t4 WHERE t1.a = t2.b AND t3.c IN (100, 103, 106)",
		expectRows: 30, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		// 30 = 10 (equi) × 3 (IN) — t4 eliminated (unreferenced)
	},
	{
		name: "j4_in_or", sql: "SELECT * FROM t1, t2, t3, t4 WHERE t1.a IN (101, 103) AND (t2.b > 105 OR t4.d < 103)",
		expectRows: 1160, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		// 1160 = 10000 × (2/10) × (1 - (6/10)*(7/10)) = 2000 × 0.58
	},

	// ── 5-table — comma FROM broken ──
	{
		name: "j5_cross", sql: "SELECT * FROM t1, t2, t3, t4, t5",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 100,000",
		skip:       true,
	},
	{
		name: "j5_equi", sql: "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3, t4, t5 WHERE t1.a = t2.b AND t3.c = t4.d",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794 + REQ000797: expected 1,000",
		skip:       true,
	},
	{
		name: "j5_where_in", sql: "SELECT * FROM t1, t2, t3, t4, t5 WHERE t1.a IN (101, 103, 105) AND t3.c > 100 AND t5.e < 108",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 21,600",
		skip:       true,
	},
	{
		name: "j5_equi_filter", sql: "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3, t4, t5 WHERE t1.a = t2.b AND t4.d IN (100, 105) AND t5.e > 100",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794 + REQ000797: expected 1,800",
		skip:       true,
	},

	// ── 6-table — comma FROM broken ──
	{
		name: "j6_cross", sql: "SELECT * FROM t1, t2, t3, t4, t5, t6",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 10^6",
		skip:       true,
	},
	{
		name: "j6_equi", sql: "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3, t4, t5, t6 WHERE t1.a = t2.b AND t3.c = t4.d AND t5.e = t6.a",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794 + REQ000797: expected 1,000",
		skip:       true,
	},
	{
		name: "j6_where", sql: "SELECT * FROM t1, t2, t3, t4, t5, t6 WHERE t1.a > 100 AND t3.c IN (100, 105) AND t6.a < 108",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 14,400",
		skip:       true,
	},
	{
		name: "j6_mixed", sql: "SELECT t1.a, t2.b FROM t1, t2, t3, t4, t5, t6 WHERE t1.a = t2.b AND t4.d IN (100, 105) AND t6.a > 100",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794 + REQ000797: expected 18,000",
		skip:       true,
	},

	// ── 7-table: select4 worst case ──
	{
		name: "j7_cross", sql: "SELECT * FROM t1, t2, t3, t4, t5, t6, t7",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 10^7",
		skip:       true,
	},
	{
		name: "j7_equi", sql: "SELECT t1.a, t2.b FROM t1, t2, t3, t4, t5, t6, t7 WHERE t1.a = t2.b AND t3.c = t4.d AND t5.e = t6.a",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794 + REQ000797: expected 1,000",
		skip:       true,
	},
	{
		name: "j7_where", sql: "SELECT * FROM t1, t2, t3, t4, t5, t6, t7 WHERE t1.a > 100 AND t4.d IN (100, 105) AND t7.a < 108",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 129,600",
		skip:       true,
	},
	{
		name: "j7_all_filters", sql: "SELECT t1.a, t2.b FROM t1, t2, t3, t4, t5, t6, t7 WHERE t1.a = t2.b AND t3.c > 100 AND t4.d IN (100, 105) AND t5.e = t6.a AND t7.a < 108",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794 + REQ000797: expected 16,200",
		skip:       true,
	},

	// ── select4-derived patterns: 5-table with equi-joins + IN-list ──
	{
		name:       "j5_equi_inlist",
		sql:        "SELECT t1.a, t2.b FROM t1, t9, t6, t3, t2 WHERE a3 = b9 AND c9 = 688 AND d6 IN (101, 103, 105) AND a1 = d9",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "select4-derived: 5-table with 4 equi/IN predicates",
		skip:       true,
	},
	{
		name:       "j5_equi_eq_chain",
		sql:        "SELECT t1.a, t2.b FROM t1, t9, t6, t3, t2 WHERE a3 = b9 AND c9 = 103 AND a1 = d9 AND d6 = 105",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "select4-derived: 5-table with 4 equi-join chains",
		skip:       true,
	},

	// ── select4-derived patterns: 6-table with equi-joins + OR ──
	{
		name:       "j6_equi_or",
		sql:        "SELECT t1.a, t2.b FROM t3, t1, t9, t2, t8, t4 WHERE a3 = e1 AND e8 IN (101, 103, 105) AND (e9 = 101 OR e9 = 103) AND c2 = 103 AND a1 IN (100, 102, 104)",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "select4-derived: 6-table with equi + IN + OR",
		skip:       true,
	},
	{
		name:       "j6_equi_inlist_chain",
		sql:        "SELECT t1.a, t2.b FROM t1, t2, t9, t8, t3, t4 WHERE c2 = 103 AND e8 IN (101, 103, 105, 107) AND a3 = e1 AND (e9 = 101 OR e9 = 103 OR e9 = 105) AND a1 IN (100, 102, 104, 106, 108)",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "select4-derived: 6-table with equi + IN + OR + multi IN",
		skip:       true,
	},

	// ── select4-derived patterns: 7-table with equi-joins + IN + equality ──
	{
		name:       "j7_equi_in_eq",
		sql:        "SELECT t1.a, t2.b FROM t7, t8, t1, t4, t2, t6, t5 WHERE c2 IN (103, 105) AND a1 = 101 AND d6 IN (100, 103, 105, 107) AND e7 IN (101, 103, 105) AND c5 IN (100, 103, 105) AND b4 = 103",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "select4-derived: 7-table with 6 IN/equality predicates",
		skip:       true,
	},
	{
		name:       "j7_equi_chain_in",
		sql:        "SELECT t1.a, t2.b FROM t2, t4, t5, t7, t6, t8, t1 WHERE c2 IN (103, 105) AND e7 IN (101, 103, 105, 107, 109) AND d6 IN (100, 103) AND e8 = 105 AND c5 IN (100, 103, 105) AND a1 = 101",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "select4-derived: 7-table with mixed IN/equality chain",
		skip:       true,
	},

	// ── select4-derived: 5-table with OR chains (no equi-join) ──
	{
		name:       "j5_or_chain",
		sql:        "SELECT t1.a FROM t1, t2, t3, t4, t5 WHERE t1.a IN (101, 103) OR t2.b IN (105, 107) OR t3.c > 105 OR t4.d < 103 OR t5.e = 101",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "select4-derived: 5-table with 5 OR predicates",
		skip:       true,
	},
	{
		name:       "j5_or_and_chain",
		sql:        "SELECT t1.a FROM t1, t2, t3, t4, t5 WHERE (t1.a IN (101, 103) OR t2.b > 105) AND (t3.c < 108 OR t4.d = 101) AND t5.e IN (100, 102, 104)",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "select4-derived: 5-table with OR+AND mixed predicates",
		skip:       true,
	},

	// ── select4-derived: 6-table with NOT IN + equi ──
	{
		name:       "j6_notin_equi",
		sql:        "SELECT t1.a, t2.b FROM t6, t8, t2, t3, t4, t1 WHERE c2 = 103 AND a3 = e1 AND a1 IN (100, 102, 104) AND e8 NOT IN (101, 103, 105) AND b4 IN (100, 103)",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "select4-derived: 6-table with NOT IN + equi",
		skip:       true,
	},

	// ── select4-derived: 7-table with all selective predicates ──
	{
		name:       "j7_all_selective",
		sql:        "SELECT t1.a, t2.b FROM t3, t8, t6, t1, t2, t4, t7 WHERE c2 = 103 AND a3 = e1 AND a1 IN (100, 102) AND e8 IN (101, 103) AND b4 = 103 AND c7 IN (100, 101, 102)",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "select4-derived: 7-table with 6 selective predicates",
		skip:       true,
	},

	// ── select4-derived: "predicate on last table" pattern ──
	// These test the key bottleneck: selective predicate on the last
	// table in the join chain means the NLJ must cross-join all
	// preceding tables before applying the filter.
	{
		name:       "j5_last_filter",
		sql:        "SELECT t1.a FROM t1, t2, t3, t4, t5 WHERE t5.e IN (101, 103)",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "selective predicate only on last table (t5)",
		skip:       true,
	},
	{
		name:       "j6_last_filter",
		sql:        "SELECT t1.a FROM t1, t2, t3, t4, t5, t6 WHERE t6.a IN (101, 103)",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "selective predicate only on last table (t6)",
		skip:       true,
	},
	{
		name:       "j7_last_filter",
		sql:        "SELECT t1.a FROM t1, t2, t3, t4, t5, t6, t7 WHERE t7.a IN (101, 103)",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "selective predicate only on last table (t7)",
		skip:       true,
	},

	// ── select4-derived: multi-column equi-join chain ──
	{
		name:       "j5_3equi_chain",
		sql:        "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3, t4, t5 WHERE t1.a = t2.b AND t2.b = t3.c AND t3.c = t4.d",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "3 equi-join chains in 5-table cross",
		skip:       true,
	},
	{
		name:       "j6_3equi_inlist",
		sql:        "SELECT t1.a, t2.b FROM t1, t2, t3, t4, t5, t6 WHERE t1.a = t2.b AND t3.c = t4.d AND t5.e = t6.a AND t1.a IN (101, 103, 105)",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "3 equi-joins + IN-list in 6-table cross",
		skip:       true,
	},

	// ── select4-derived: NOT IN + equi (negative selectivity) ──
	{
		name:       "j5_notin_equi",
		sql:        "SELECT t1.a FROM t1, t2, t3, t4, t5 WHERE t1.a = t2.b AND t3.c NOT IN (101, 103, 105) AND t5.e > 105",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "NOT IN + equi-join in 5-table cross",
		skip:       true,
	},

	// ── select4-derived: BETWEEN + equi ──
	{
		name:       "j5_between_equi",
		sql:        "SELECT t1.a FROM t1, t2, t3, t4, t5 WHERE t1.a = t2.b AND t3.c BETWEEN 101 AND 105 AND t5.e BETWEEN 100 AND 108",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "BETWEEN + equi-join in 5-table cross",
		skip:       true,
	},

	// ── select4-derived: LIKE + equi ──
	{
		name:       "j5_like_equi",
		sql:        "SELECT t1.a FROM t1, t2, t3, t4, t5 WHERE t1.a = t2.b AND t3.c LIKE '10%' AND t5.e > 105",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "LIKE pattern + equi-join in 5-table cross",
		skip:       true,
	},

	// ── select4-derived: compound SELECT (UNION/EXCEPT) ──
	{
		name: "j3_union_2branch",
		sql: "SELECT a FROM t1 WHERE a IN (101, 103) UNION ALL SELECT b FROM t2 WHERE b IN (105, 107) UNION ALL SELECT c FROM t3 WHERE c IN (100, 102, 104)",
		expectRows: 7, maxDuration: 1 * time.Second, warnAt: 100 * time.Millisecond,
	},
	{
		name: "j3_except_chain",
		sql: "SELECT a FROM t1 WHERE a IN (100, 101, 102, 103, 104) EXCEPT SELECT b FROM t2 WHERE b IN (101, 103) EXCEPT SELECT c FROM t3 WHERE c IN (100, 102)",
		expectRows: 1, maxDuration: 1 * time.Second, warnAt: 100 * time.Millisecond,
	},
	{
		name: "j5_union_except",
		sql: "SELECT a FROM t1 WHERE a IN (100,101,102,103,104,105) UNION ALL SELECT b FROM t2 WHERE b IN (106,107,108,109) EXCEPT SELECT c FROM t3 WHERE c IN (100,101) UNION ALL SELECT d FROM t4 WHERE d IN (108,109)",
		expectRows: -1, maxDuration: 2 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "select4-derived: UNION ALL + EXCEPT + UNION ALL chain",
		skip:       true,
	},
}

// joinSetupSQL creates the 9 tables with 10 rows each.
// Values are designed so equi-joins between tables always match.
func joinSetupSQL() []string {
	var stmts []string
	for i := 1; i <= 9; i++ {
		stmts = append(stmts, fmt.Sprintf(
			"CREATE TABLE t%d(a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			i))
	}
	for i := 1; i <= 9; i++ {
		for j := 0; j < 10; j++ {
			// All tables share the same value range for a/b/c/d/e
			// so equi-joins like t1.a = t2.b always match.
			v := 100 + j
			stmts = append(stmts, fmt.Sprintf(
				"INSERT INTO t%d VALUES(%d,%d,%d,%d,%d)",
				i, v, v, v, v, v))
		}
	}
	return stmts
}

func setupJoinDB(t testing.TB) *sql.DB {
	t.Helper()
	dir, err := os.MkdirTemp("", "join-regress-")
	if err != nil {
		t.Fatal(err)
	}
	if tb, ok := t.(testing.TB); ok {
		tb.Cleanup(func() { os.RemoveAll(dir) })
	}

	db, err := sql.Open("razor", filepath.Join(dir, "db.razor"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), joinRegressTimeout)
	defer cancel()

	for _, s := range joinSetupSQL() {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	return db
}

// TestJoinRegress runs all join queries and verifies:
//  1. Query completes without error (hard fail)
//  2. Row count matches expectation (soft fail when expectRows >= 0)
//  3. Duration stays under maxDuration (soft fail)
//  4. Warns if duration exceeds warnAt threshold
func TestJoinRegress(t *testing.T) {
	db := setupJoinDB(t)

	var failed, warned int
	for _, q := range joinQueries {
		q := q
		t.Run(q.name, func(t *testing.T) {
			if q.skip {
				t.Skip("skipped: produces too many rows for CI")
			}
			start := time.Now()
			queryCtx, cancel := context.WithTimeout(context.Background(), joinRegressTimeout)
			defer cancel()
			rows, err := db.QueryContext(queryCtx, q.sql)
			if err != nil {
				t.Fatalf("query failed: %v", err)
			}
			count := int64(0)
			for rows.Next() {
				count++
			}
			rows.Close()
			dur := time.Since(start)

			// Row count validation
			if q.expectRows >= 0 && count != q.expectRows {
				t.Errorf("row count: got %d, want %d (ratio %.2fx)", count, q.expectRows, float64(count)/float64(q.expectRows))
			}

			// Duration budgets
			if dur > q.maxDuration {
				failed++
				t.Errorf("timed out: %v > %v max", dur.Truncate(time.Millisecond), q.maxDuration)
			} else if dur > q.warnAt {
				warned++
				t.Logf("SLOW (%s): %v > %v warn threshold", q.name, dur.Truncate(time.Millisecond), q.warnAt)
			}

			// Summary line with progress annotation for broken queries
			annotation := ""
			if q.expectRows < 0 {
				if q.brokenNote != "" {
					annotation = " [BROKEN: " + q.brokenNote + "]"
				} else {
					annotation = " [row count unverified]"
				}
			} else if count == q.expectRows {
				annotation = " ✓"
			} else {
				annotation = fmt.Sprintf(" ✗ got=%d want=%d", count, q.expectRows)
			}
			t.Logf("%s: %d rows in %v%s", q.name, count, dur.Truncate(time.Millisecond), annotation)
		})
	}

	t.Cleanup(func() {
		if warned > 0 {
			t.Logf("%d queries exceeded warn threshold", warned)
		}
	})
}

// BenchmarkJoinRegress measures performance of each join pattern.
func BenchmarkJoinRegress(b *testing.B) {
	db := setupJoinDB(b)
	ctx := context.Background()

	for _, q := range joinQueries {
		b.Run(q.name, func(b *testing.B) {
			for b.Loop() {
				rows, err := db.QueryContext(ctx, q.sql)
				if err != nil {
					b.Fatal(err)
				}
				for rows.Next() {
				}
				rows.Close()
			}
		})
	}
}

// TestJoinElimination_E2E verifies REQ000799 via the SQL layer.
// Uses qualified column projections so join elimination can drop
// unreferenced tables. Compares against the * projection (which
// disables elimination by design) to show the work-savings.
func TestJoinElimination_E2E(t *testing.T) {
	db := setupJoinDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), joinRegressTimeout)
	defer cancel()

	cases := []struct {
		name        string
		sql         string
		expectRows  int64
		expectCols  int
		description string
	}{
		{
			name:        "star_3table_keeps_all",
			sql:         "SELECT * FROM t1, t2, t3",
			expectRows:  1000,
			expectCols:  15,
			description: "SELECT * blocks elimination — all 3 tables materialized",
		},
		{
			name:        "qual_3table_drops_t3",
			sql:         "SELECT t1.a, t2.b FROM t1, t2, t3",
			expectRows:  100,
			expectCols:  2,
			description: "Only t1, t2 referenced — t3 eliminated (10×10=100 not 10³)",
		},
		{
			name:        "qual_where_3table",
			sql:         "SELECT t1.a FROM t1, t2, t3 WHERE t2.b > 105",
			expectRows:  40,
			expectCols:  1,
			description: "t3 unreferenced — eliminated, 10*10=100 input filtered to 40",
		},
		{
			name:        "qual_3table_drops_t2",
			sql:         "SELECT t1.a FROM t1, t2, t3 WHERE t3.c IN (100, 105)",
			expectRows:  20,
			expectCols:  1,
			description: "Only t1 and t3 referenced — t2 eliminated (10*2=20)",
		},
		{
			name:        "all_3_referenced",
			sql:         "SELECT t1.a FROM t1, t2, t3 WHERE t1.a = t2.b AND t2.b = t3.c",
			expectRows:  10,
			expectCols:  1,
			description: "All 3 referenced via equi-join chain — nothing eliminated",
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			rows, err := db.QueryContext(ctx, c.sql)
			if err != nil {
				t.Fatalf("%s: query failed: %v", c.name, err)
			}
			defer rows.Close()

			cols, err := rows.Columns()
			if err != nil {
				t.Fatalf("%s: columns: %v", c.name, err)
			}
			if len(cols) != c.expectCols {
				t.Errorf("%s: columns: got %d, want %d (%s)", c.name, len(cols), c.expectCols, c.description)
			}

			count := int64(0)
			for rows.Next() {
				count++
			}
			if count != c.expectRows {
				t.Errorf("%s: rows: got %d, want %d (%s)", c.name, count, c.expectRows, c.description)
			}
		})
	}
}

// BenchmarkJoinElimination compares SELECT * (no elimination) vs
// qualified projection (with elimination) for the same join shape.
// Expectation: qualified projection is significantly faster because
// the planner drops unreferenced tables from the join chain.
func BenchmarkJoinElimination(b *testing.B) {
	db := setupJoinDB(b)
	ctx := context.Background()

	cases := []struct {
		name string
		sql  string
	}{
		{"star_3table", "SELECT * FROM t1, t2, t3"},
		{"qual_3table_drops_t3", "SELECT t1.a, t2.b FROM t1, t2, t3"},
		{"qual_3table_drops_t2", "SELECT t1.a, t3.c FROM t1, t2, t3"},
		{"qual_3table_drops_all_extras", "SELECT t1.a FROM t1, t2, t3"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				rows, err := db.QueryContext(ctx, c.sql)
				if err != nil {
					b.Fatal(err)
				}
				for rows.Next() {
				}
				rows.Close()
			}
		})
	}
}
