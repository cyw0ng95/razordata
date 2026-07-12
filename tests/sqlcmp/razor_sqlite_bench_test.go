//go:build !slt_corpus

// Benchmark comparing Razordata vs modernc sqlite on standard select patterns.
//
// Setup: 100-row table with 5 integer columns.
// Run: go test -bench=BenchmarkRazorVsSqlite -benchtime=10x -benchmem ./tests/sqlcmp/
package sqlcmp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/cyw0ng95/razordata/driver"
	_ "modernc.org/sqlite"
)

var benchQueries100 = []struct {
	name string
	sql  string
}{
	{"star", "SELECT * FROM t1"},
	{"one_col", "SELECT a FROM t1"},
	{"arith", "SELECT a+b*2+c*3+d*4+e*5 FROM t1"},
	{"case", "SELECT CASE WHEN a<b-3 THEN 111 WHEN a<=b THEN 222 WHEN a<b+3 THEN 333 ELSE 444 END FROM t1"},
	{"where", "SELECT a FROM t1 WHERE a>50"},
	{"order", "SELECT a FROM t1 ORDER BY a"},
	{"order_desc", "SELECT a FROM t1 ORDER BY a DESC"},
	{"limit", "SELECT a FROM t1 LIMIT 10"},
	{"count_star", "SELECT count(*) FROM t1"},
	{"count_col", "SELECT count(b) FROM t1"},
	{"multi_col", "SELECT a+b*2+c*3, CASE WHEN a<b-3 THEN 111 WHEN a<=b THEN 222 WHEN a<b+3 THEN 333 ELSE 444 END, b, a+b*2+c*3+d*4+e*5 FROM t1"},
	{"group_by", "SELECT b, count(*) FROM t1 GROUP BY b"},
	{"subquery", "SELECT (SELECT avg(a) FROM t1) FROM t1"},
	{"join_equi", "SELECT t1.a, t2.b FROM t1, t1 AS t2 WHERE t1.a = t2.a"},
	{"having", "SELECT b, count(*) FROM t1 GROUP BY b HAVING count(*) > 5"},
	{"where_like", "SELECT a FROM t1 WHERE b LIKE '%5%'"},
	{"where_between", "SELECT a FROM t1 WHERE b BETWEEN 3 AND 7"},
	{"order_multi", "SELECT a, b, c FROM t1 ORDER BY b DESC, c ASC"},
	{"count_distinct", "SELECT count(DISTINCT b) FROM t1"},
	{"aggregates_multi", "SELECT b, count(*), avg(a), sum(c), min(d), max(e) FROM t1 GROUP BY b"},
}

func benchSetupSQL(n int) []string {
	stmts := []string{
		"CREATE TABLE t1(a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
	}
	for i := 1; i <= n; i++ {
		v := i
		stmts = append(stmts, fmt.Sprintf(
			"INSERT INTO t1 VALUES(%d,%d,%d,%d,%d)",
			v, v%10, v%20, v%30, v%40))
	}
	return stmts
}

func openBenchDB(b *testing.B, engine, dir string) *sql.DB {
	b.Helper()
	var db *sql.DB
	var err error
	switch engine {
	case "razor":
		db, err = sql.Open("razor", filepath.Join(dir, "db.razor"))
	case "sqlite":
		db, err = sql.Open("sqlite", ":memory:")
	}
	if err != nil {
		b.Fatalf("%s open: %v", engine, err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func initBenchDB(b *testing.B, db *sql.DB) {
	b.Helper()
	ctx := context.Background()
	for _, s := range benchSetupSQL(100) {
		if _, err := db.ExecContext(ctx, s); err != nil {
			b.Fatalf("setup %q: %v", s, err)
		}
	}
}

// BenchmarkRazorVsSqlite compares Razordata vs modernc sqlite on
// representative query patterns with 100 rows. Run with:
//
//	go test -bench=BenchmarkRazorVsSqlite -benchtime=10x -benchmem ./tests/sqlcmp/
func BenchmarkRazorVsSqlite(b *testing.B) {
	razorDir, err := os.MkdirTemp("", "bench-razor-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { os.RemoveAll(razorDir) })

	razorDB := openBenchDB(b, "razor", razorDir)
	b.Cleanup(func() { razorDB.Close() })
	initBenchDB(b, razorDB)

	sqliteDB := openBenchDB(b, "sqlite", "")
	b.Cleanup(func() { sqliteDB.Close() })
	initBenchDB(b, sqliteDB)

	ctx := context.Background()
	for _, q := range benchQueries100 {
		q := q
		b.Run(q.name+"/razor", func(b *testing.B) {
			for b.Loop() {
				rows, err := razorDB.QueryContext(ctx, q.sql)
				if err != nil {
					b.Skipf("%s: %v", q.name, err)
				}
				for rows.Next() {
				}
				rows.Close()
			}
		})
		b.Run(q.name+"/sqlite", func(b *testing.B) {
			for b.Loop() {
				rows, err := sqliteDB.QueryContext(ctx, q.sql)
				if err != nil {
					b.Skipf("%s: %v", q.name, err)
				}
				for rows.Next() {
				}
				rows.Close()
			}
		})
	}
}
