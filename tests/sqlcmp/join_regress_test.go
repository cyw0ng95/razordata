// Package sqlcmp — multi-table join regression tests.
//
// This file provides a focused regression suite for multi-table join
// performance. It covers the patterns from select4.test (2-7 table
// cross joins with WHERE filters, equi-joins, IN-lists, OR conditions)
// but with a simplified setup for fast iteration.
//
// Run benchmarks:
//
//	go test -bench=BenchmarkJoin -benchmem ./tests/sqlcmp/
//
// Run regression check (timeout per query):
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
	name string
	sql  string
}

// joinQueries covers the key patterns from select4: 2-7 table joins
// with various filter types. Each query is designed to exercise a
// specific join optimization opportunity.
var joinQueries = []joinQuery{
	// --- 2-table: baseline ---
	{"j2_cross", "SELECT * FROM t1, t2"},
	{"j2_equi", "SELECT t1.a, t2.b FROM t1 INNER JOIN t2 ON t1.a = t2.b"},
	{"j2_where_in", "SELECT * FROM t1, t2 WHERE t2.b IN (110, 130, 150)"},
	{"j2_where_and", "SELECT * FROM t1, t2 WHERE t1.a > 150 AND t2.b < 200"},
	{"j2_where_or", "SELECT * FROM t1, t2 WHERE t1.a > 150 OR t2.b < 120"},

	// --- 3-table: HashJoin for 2nd join ---
	{"j3_cross", "SELECT * FROM t1, t2, t3"},
	{"j3_equi_chain", "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3 WHERE t1.a = t2.b AND t2.b = t3.c"},
	{"j3_mixed", "SELECT * FROM t1, t2, t3 WHERE t1.a > 150 AND t3.c IN (100, 200)"},
	{"j3_filter_only", "SELECT * FROM t1, t2, t3 WHERE t1.a IN (110, 130) AND t2.b > 100"},

	// --- 4-table: select4 core pattern ---
	{"j4_cross", "SELECT * FROM t1, t2, t3, t4"},
	{"j4_equi_2chain", "SELECT t1.a, t2.b FROM t1, t2, t3, t4 WHERE t1.a = t2.b AND t3.c = t4.d"},
	{"j4_where_2tables", "SELECT * FROM t1, t2, t3, t4 WHERE t1.a > 150 AND t4.d < 300"},
	{"j4_mixed", "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3, t4 WHERE t1.a = t2.b AND t3.c IN (100, 200, 300)"},
	{"j4_in_or", "SELECT * FROM t1, t2, t3, t4 WHERE t1.a IN (110, 130) AND (t2.b > 150 OR t4.d < 100)"},

	// --- 5-table: select4 join3 pattern ---
	{"j5_cross", "SELECT * FROM t1, t2, t3, t4, t5"},
	{"j5_equi", "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3, t4, t5 WHERE t1.a = t2.b AND t3.c = t4.d"},
	{"j5_where_in", "SELECT * FROM t1, t2, t3, t4, t5 WHERE t1.a IN (110, 130, 150) AND t3.c > 50 AND t5.e < 400"},
	{"j5_equi_filter", "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3, t4, t5 WHERE t1.a = t2.b AND t4.d IN (100, 200) AND t5.e > 50"},

	// --- 6-table: select4 join5 pattern ---
	{"j6_cross", "SELECT * FROM t1, t2, t3, t4, t5, t6"},
	{"j6_equi", "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3, t4, t5, t6 WHERE t1.a = t2.b AND t3.c = t4.d AND t5.e = t6.a"},
	{"j6_where", "SELECT * FROM t1, t2, t3, t4, t5, t6 WHERE t1.a > 50 AND t3.c IN (100, 200) AND t6.a < 400"},
	{"j6_mixed", "SELECT t1.a, t2.b FROM t1, t2, t3, t4, t5, t6 WHERE t1.a = t2.b AND t4.d IN (100, 200, 300) AND t6.a > 50"},

	// --- 7-table: select4 worst case ---
	{"j7_cross", "SELECT * FROM t1, t2, t3, t4, t5, t6, t7"},
	{"j7_equi", "SELECT t1.a, t2.b FROM t1, t2, t3, t4, t5, t6, t7 WHERE t1.a = t2.b AND t3.c = t4.d AND t5.e = t6.a"},
	{"j7_where", "SELECT * FROM t1, t2, t3, t4, t5, t6, t7 WHERE t1.a > 50 AND t4.d IN (100, 200) AND t7.a < 400"},
	{"j7_all_filters", "SELECT t1.a, t2.b FROM t1, t2, t3, t4, t5, t6, t7 WHERE t1.a = t2.b AND t3.c > 50 AND t4.d IN (100, 200) AND t5.e = t6.a AND t7.a < 400"},
}

// joinSetupSQL creates the 9 tables with 100 rows each.
// Values are designed so equi-joins between tables have matches.
func joinSetupSQL() []string {
	var stmts []string
	for i := 1; i <= 9; i++ {
		stmts = append(stmts, fmt.Sprintf(
			"CREATE TABLE t%d(a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			i))
	}
	for i := 1; i <= 9; i++ {
		for j := 0; j < 100; j++ {
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

// TestJoinRegress runs all join queries and verifies they complete
// without error and within the timeout.
func TestJoinRegress(t *testing.T) {
	db := setupJoinDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), joinRegressTimeout)
	defer cancel()

	for _, q := range joinQueries {
		t.Run(q.name, func(t *testing.T) {
			start := time.Now()
			rows, err := db.QueryContext(ctx, q.sql)
			if err != nil {
				t.Fatalf("query failed: %v", err)
			}
			count := 0
			for rows.Next() {
				count++
			}
			rows.Close()
			dur := time.Since(start)
			t.Logf("%s: %d rows in %v", q.name, count, dur)
			if dur > 2*time.Second {
				t.Errorf("%s: took %v (> 2s target)", q.name, dur)
			}
		})
	}
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
