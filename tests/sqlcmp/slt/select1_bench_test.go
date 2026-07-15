//go:build slt_corpus

package slt

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/cyw0ng95/razordata/driver"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	sy "github.com/cyw0ng95/razordata/internal/SYS/SY"
)

func setupSelect1(b *testing.B) *sql.DB {
	b.Helper()
	dir, err := os.MkdirTemp("", "bench-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { os.RemoveAll(dir) })

	db, err := sql.Open("razor", filepath.Join(dir, "db.razor"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })

	db.SetMaxOpenConns(1)

	stmts := []string{
		"CREATE TABLE t1(a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
		"INSERT INTO t1(e,c,b,d,a) VALUES(103,102,100,101,104)",
		"INSERT INTO t1(a,c,d,e,b) VALUES(107,106,108,109,105)",
		"INSERT INTO t1(e,d,b,a,c) VALUES(110,114,112,111,113)",
		"INSERT INTO t1(d,c,e,a,b) VALUES(116,119,117,115,118)",
		"INSERT INTO t1(c,d,b,e,a) VALUES(123,122,124,120,121)",
		"INSERT INTO t1(a,d,b,e,c) VALUES(127,128,129,126,125)",
		"INSERT INTO t1(e,c,a,d,b) VALUES(132,134,131,133,130)",
		"INSERT INTO t1(a,d,b,e,c) VALUES(138,136,139,135,137)",
		"INSERT INTO t1(e,c,d,a,b) VALUES(144,141,140,142,143)",
		"INSERT INTO t1(b,a,e,d,c) VALUES(145,149,146,148,147)",
		"INSERT INTO t1(b,c,a,d,e) VALUES(151,150,153,154,152)",
		"INSERT INTO t1(c,e,a,d,b) VALUES(155,157,159,156,158)",
		"INSERT INTO t1(c,b,a,d,e) VALUES(161,160,163,164,162)",
		"INSERT INTO t1(b,d,a,e,c) VALUES(167,169,168,165,166)",
		"INSERT INTO t1(d,b,c,e,a) VALUES(171,170,172,173,174)",
		"INSERT INTO t1(e,c,a,d,b) VALUES(177,176,179,178,175)",
		"INSERT INTO t1(b,e,a,d,c) VALUES(181,180,182,183,184)",
		"INSERT INTO t1(c,a,b,e,d) VALUES(187,188,186,189,185)",
		"INSERT INTO t1(d,b,c,e,a) VALUES(190,194,193,192,191)",
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			b.Fatalf("setup %q: %v", s, err)
		}
	}
	return db
}

// setupSelect1Engine creates a Razordata engine directly (bypassing
// database/sql) and returns the engine so benchmarks can use QueryAll
// to eliminate driver overhead. REQ001495.
func setupSelect1Engine(b *testing.B) (*sy.Engine, func()) {
	b.Helper()
	dir, err := os.MkdirTemp("", "bench-eng-")
	if err != nil {
		b.Fatal(err)
	}

	eng, err := sy.Open(context.Background(), filepath.Join(dir, "db"), AP.Options{
		Dir:              filepath.Join(dir, "db.engine"),
		MemTableSize:     1 << 20,
		BufferPoolMB:     64,
		MaxMemoryPerQuery: 512 << 20,
		JoinBufferSize:   256 << 20,
	})
	if err != nil {
		b.Fatal(err)
	}

	cleanup := func() {
		eng.Close(context.Background())
		os.RemoveAll(dir)
	}

	exe := eng.Executor()
	stmts := []string{
		"CREATE TABLE t1(a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
		"INSERT INTO t1(e,c,b,d,a) VALUES(103,102,100,101,104)",
		"INSERT INTO t1(a,c,d,e,b) VALUES(107,106,108,109,105)",
		"INSERT INTO t1(e,d,b,a,c) VALUES(110,114,112,111,113)",
		"INSERT INTO t1(d,c,e,a,b) VALUES(116,119,117,115,118)",
		"INSERT INTO t1(c,d,b,e,a) VALUES(123,122,124,120,121)",
		"INSERT INTO t1(a,d,b,e,c) VALUES(127,128,129,126,125)",
		"INSERT INTO t1(e,c,a,d,b) VALUES(132,134,131,133,130)",
		"INSERT INTO t1(a,d,b,e,c) VALUES(138,136,139,135,137)",
		"INSERT INTO t1(e,c,d,a,b) VALUES(144,141,140,142,143)",
		"INSERT INTO t1(b,a,e,d,c) VALUES(145,149,146,148,147)",
		"INSERT INTO t1(b,c,a,d,e) VALUES(151,150,153,154,152)",
		"INSERT INTO t1(c,e,a,d,b) VALUES(155,157,159,156,158)",
		"INSERT INTO t1(c,b,a,d,e) VALUES(161,160,163,164,162)",
		"INSERT INTO t1(b,d,a,e,c) VALUES(167,169,168,165,166)",
		"INSERT INTO t1(d,b,c,e,a) VALUES(171,170,172,173,174)",
		"INSERT INTO t1(e,c,a,d,b) VALUES(177,176,179,178,175)",
		"INSERT INTO t1(b,e,a,d,c) VALUES(181,180,182,183,184)",
		"INSERT INTO t1(c,a,b,e,d) VALUES(187,188,186,189,185)",
		"INSERT INTO t1(d,b,c,e,a) VALUES(190,194,193,192,191)",
	}
	for _, s := range stmts {
		if _, err := exe.Exec(context.Background(), s); err != nil {
			b.Fatalf("setup %q: %v", s, err)
		}
	}
	return eng, cleanup
}

// BenchmarkSelect1_Queries runs representative select1 queries through
// the full engine path (database/sql -> driver -> executor -> storage).
func BenchmarkSelect1_Queries(b *testing.B) {
	db := setupSelect1(b)

	queries := []struct {
		name string
		sql  string
	}{
		{"star", "SELECT * FROM t1"},
		{"one_col", "SELECT a FROM t1"},
		{"arith", "SELECT a+b*2+c*3+d*4+e*5 FROM t1"},
		{"case", "SELECT CASE WHEN a<b-3 THEN 111 WHEN a<=b THEN 222 WHEN a<b+3 THEN 333 ELSE 444 END FROM t1"},
		{"where", "SELECT a FROM t1 WHERE a>150"},
		{"order", "SELECT a FROM t1 ORDER BY a"},
		{"limit", "SELECT a FROM t1 LIMIT 5"},
		{"count", "SELECT count(*) FROM t1"},
		{"multi_col", "SELECT a+b*2+c*3, CASE WHEN a<b-3 THEN 111 WHEN a<=b THEN 222 WHEN a<b+3 THEN 333 ELSE 444 END, b, a+b*2+c*3+d*4+e*5 FROM t1"},
	}

	for _, q := range queries {
		b.Run(q.name, func(b *testing.B) {
			ctx := context.Background()
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

// BenchmarkSelect1_Prepared uses prepared statements to measure
// overhead without parse time.
func BenchmarkSelect1_Prepared(b *testing.B) {
	db := setupSelect1(b)
	ctx := context.Background()

	stmt, err := db.PrepareContext(ctx, "SELECT a+b*2+c*3+d*4+e*5 FROM t1")
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()

	b.ResetTimer()
	for b.Loop() {
		rows, err := stmt.QueryContext(ctx)
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
		}
		rows.Close()
	}
}

// BenchmarkSelect1_BlockCache measures query performance with the block cache
// warmed up (REQ001242). Compare with BenchmarkSelect1_Queries/star to see the
// improvement from cached SST blocks.
func BenchmarkSelect1_BlockCache(b *testing.B) {
	db := setupSelect1(b)
	ctx := context.Background()

	// Warm up: run query once to populate the block cache.
	rows, _ := db.QueryContext(ctx, "SELECT * FROM t1")
	for rows.Next() {
	}
	rows.Close()

	b.ResetTimer()
	for b.Loop() {
		rows, err := db.QueryContext(ctx, "SELECT * FROM t1")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
		}
		rows.Close()
	}
}

// BenchmarkSelect1_ThroughputDirect uses the engine's QueryAll directly
// (bypassing database/sql) to measure pure engine throughput. REQ001495.
func BenchmarkSelect1_ThroughputDirect(b *testing.B) {
	eng, cleanup := setupSelect1Engine(b)
	defer cleanup()
	ctx := context.Background()
	exe := eng.Executor()

	sqls := []string{
		"SELECT * FROM t1",
		"SELECT a FROM t1 WHERE a>150",
		"SELECT a+b*2+c*3+d*4+e*5 FROM t1",
		"SELECT CASE WHEN a<b-3 THEN 111 WHEN a<=b THEN 222 WHEN a<b+3 THEN 333 ELSE 444 END FROM t1",
		"SELECT count(*) FROM t1",
	}

	// Precompile all plans upfront (REQ001458).
	exe.Precompile(ctx, sqls)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		for _, q := range sqls {
			_, err := exe.QueryAll(ctx, q)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
