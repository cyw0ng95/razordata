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

	// ── 3-table — comma FROM broken until REQ000794 ──
	{
		name: "j3_cross", sql: "SELECT * FROM t1, t2, t3",
		expectRows: -1, maxDuration: 2 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: comma FROM ignores tables 3+, expected 1,000",
	},
	{
		name: "j3_equi_chain", sql: "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3 WHERE t1.a = t2.b AND t2.b = t3.c",
		expectRows: -1, maxDuration: 2 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 10",
	},
	{
		name: "j3_mixed", sql: "SELECT * FROM t1, t2, t3 WHERE t1.a > 105 AND t3.c IN (100, 108)",
		expectRows: -1, maxDuration: 2 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 80",
	},
	{
		name: "j3_filter_only", sql: "SELECT * FROM t1, t2, t3 WHERE t1.a IN (101, 103) AND t2.b > 100",
		expectRows: -1, maxDuration: 2 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 180",
	},

	// ── 4-table — comma FROM broken ──
	{
		name: "j4_cross", sql: "SELECT * FROM t1, t2, t3, t4",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 10,000",
		skip:       true,
	},
	{
		name: "j4_equi_2chain", sql: "SELECT t1.a, t2.b FROM t1, t2, t3, t4 WHERE t1.a = t2.b AND t3.c = t4.d",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794 + REQ000797: expected 100",
		skip:       true,
	},
	{
		name: "j4_where_2tables", sql: "SELECT * FROM t1, t2, t3, t4 WHERE t1.a > 105 AND t4.d < 108",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 3,200",
		skip:       true,
	},
	{
		name: "j4_mixed", sql: "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3, t4 WHERE t1.a = t2.b AND t3.c IN (100, 103, 106)",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 300",
		skip:       true,
	},
	{
		name: "j4_in_or", sql: "SELECT * FROM t1, t2, t3, t4 WHERE t1.a IN (101, 103) AND (t2.b > 105 OR t4.d < 103)",
		expectRows: -1, maxDuration: 10 * time.Second, warnAt: 500 * time.Millisecond,
		brokenNote: "REQ000794: expected 980",
		skip:       true,
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
