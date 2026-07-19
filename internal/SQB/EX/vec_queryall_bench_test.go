//go:build !slt_corpus_full

package EX

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkQueryAll_VecVsRow_1KRows(b *testing.B) {
	const rowCount = 1024
	ResetForTest(b)
	ex, eng := newEngineExecutor(b)
	defer eng.Close()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE bench (a INTEGER PRIMARY KEY, b INTEGER, c TEXT)"); err != nil {
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			b.Fatalf("CREATE: %v", err)
		}
	}
	for i := 0; i < rowCount; i++ {
		ins := fmt.Sprintf("INSERT INTO bench VALUES (%d, %d, 'row%d')", i, i*10, i)
		if _, err := ex.Exec(ctx, ins); err != nil {
			b.Fatalf("INSERT %d: %v", i, err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := ex.QueryAll(ctx, "SELECT b FROM bench")
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != rowCount {
			b.Fatalf("rows: %d want %d", len(rows), rowCount)
		}
	}
}

func BenchmarkVectorized_SelectFilter_10K(b *testing.B) {
	const rowCount = 10000
	ResetForTest(b)
	ex, eng := newEngineExecutor(b)
	defer eng.Close()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE bench (a INTEGER PRIMARY KEY, b INTEGER, c TEXT, d INTEGER)"); err != nil {
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			b.Fatalf("CREATE: %v", err)
		}
	}
	for i := 0; i < rowCount; i++ {
		ins := fmt.Sprintf("INSERT INTO bench VALUES (%d, %d, 'row%d', %d)", i, i*10, i, i%100)
		if _, err := ex.Exec(ctx, ins); err != nil {
			b.Fatalf("INSERT %d: %v", i, err)
		}
	}
	b.ResetTimer()

	b.Run("vec", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			rows, err := ex.QueryAll(ctx, "SELECT a, b, c FROM bench WHERE d > 50")
			if err != nil {
				b.Fatal(err)
			}
			if len(rows) == 0 {
				b.Fatal("no rows")
			}
		}
	})
}

func BenchmarkVectorized_Aggregate_10K(b *testing.B) {
	const rowCount = 10000
	ResetForTest(b)
	ex, eng := newEngineExecutor(b)
	defer eng.Close()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE bench (a INTEGER PRIMARY KEY, b INTEGER, c TEXT, d INTEGER)"); err != nil {
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			b.Fatalf("CREATE: %v", err)
		}
	}
	for i := 0; i < rowCount; i++ {
		ins := fmt.Sprintf("INSERT INTO bench VALUES (%d, %d, 'row%d', %d)", i, i*10, i, i%100)
		if _, err := ex.Exec(ctx, ins); err != nil {
			b.Fatalf("INSERT %d: %v", i, err)
		}
	}
	b.ResetTimer()

	b.Run("vec", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			rows, err := ex.QueryAll(ctx, "SELECT count(*), sum(b), avg(b), max(b), min(b) FROM bench WHERE d > 50")
			if err != nil {
				b.Fatal(err)
			}
			if len(rows) == 0 {
				b.Fatal("no rows")
			}
		}
	})
}

func BenchmarkVectorized_Join_10K(b *testing.B) {
	const rowCount = 10000
	ResetForTest(b)
	ex, eng := newEngineExecutor(b)
	defer eng.Close()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE t1 (a INTEGER PRIMARY KEY, b INTEGER, c TEXT)"); err != nil {
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			b.Fatalf("CREATE t1: %v", err)
		}
	}
	if _, err := ex.Exec(ctx, "CREATE TABLE t2 (a INTEGER, d INTEGER, e TEXT)"); err != nil {
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			b.Fatalf("CREATE t2: %v", err)
		}
	}
	for i := 0; i < rowCount; i++ {
		ins1 := fmt.Sprintf("INSERT INTO t1 VALUES (%d, %d, 'row%d')", i, i*10, i)
		if _, err := ex.Exec(ctx, ins1); err != nil {
			b.Fatalf("INSERT t1 %d: %v", i, err)
		}
		ins2 := fmt.Sprintf("INSERT INTO t2 VALUES (%d, %d, 'val%d')", i, i%100, i)
		if _, err := ex.Exec(ctx, ins2); err != nil {
			b.Fatalf("INSERT t2 %d: %v", i, err)
		}
	}
	b.ResetTimer()

	b.Run("vec", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			rows, err := ex.QueryAll(ctx, "SELECT t1.a, t1.b, t2.d, t2.e FROM t1, t2 WHERE t1.a = t2.a AND t2.d > 50")
			if err != nil {
				b.Fatal(err)
			}
			if len(rows) == 0 {
				b.Fatal("no rows")
			}
		}
	})
}

func BenchmarkVectorized_Update_10K(b *testing.B) {
	const rowCount = 10000
	ResetForTest(b)
	ex, eng := newEngineExecutor(b)
	defer eng.Close()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE bench (a INTEGER PRIMARY KEY, b INTEGER, c TEXT, d INTEGER)"); err != nil {
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			b.Fatalf("CREATE: %v", err)
		}
	}
	for i := 0; i < rowCount; i++ {
		ins := fmt.Sprintf("INSERT INTO bench VALUES (%d, %d, 'row%d', %d)", i, i*10, i, i%100)
		if _, err := ex.Exec(ctx, ins); err != nil {
			b.Fatalf("INSERT %d: %v", i, err)
		}
	}
	b.ResetTimer()

	b.Run("vec", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			res, err := ex.Exec(ctx, "UPDATE bench SET b = b + 1, d = d * 2 WHERE d > 50")
			if err != nil {
				b.Fatal(err)
			}
			if res.RowsAffected == 0 && rowCount > 0 {
				b.Fatal("no rows affected")
			}
		}
	})
}

func BenchmarkVectorized_Delete_10K(b *testing.B) {
	const rowCount = 10000
	ResetForTest(b)
	ex, eng := newEngineExecutor(b)
	defer eng.Close()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE bench (a INTEGER PRIMARY KEY, b INTEGER, c TEXT, d INTEGER)"); err != nil {
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			b.Fatalf("CREATE: %v", err)
		}
	}
	b.Run("vec", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for j := 0; j < rowCount; j++ {
				ins := fmt.Sprintf("INSERT INTO bench VALUES (%d, %d, 'row%d', %d)", j, j*10, j, j%100)
				if _, err := ex.Exec(ctx, ins); err != nil {
					b.Fatal(err)
				}
			}
			res, err := ex.Exec(ctx, "DELETE FROM bench WHERE d > 50")
			if err != nil {
				b.Fatal(err)
			}
			if res.RowsAffected == 0 {
				b.Fatal("no rows deleted")
			}
		}
	})
}

func BenchmarkVectorized_Subquery_1K(b *testing.B) {
	const rowCount = 1000
	ResetForTest(b)
	ex, eng := newEngineExecutor(b)
	defer eng.Close()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE t1 (a INTEGER, b INTEGER, c TEXT)"); err != nil {
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			b.Fatalf("CREATE: %v", err)
		}
	}
	for i := 0; i < rowCount; i++ {
		ins := fmt.Sprintf("INSERT INTO t1 VALUES (%d, %d, 'row%d')", i, i*10, i)
		if _, err := ex.Exec(ctx, ins); err != nil {
			b.Fatalf("INSERT %d: %v", i, err)
		}
	}
	b.ResetTimer()
	b.Run("vec", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			rows, err := ex.QueryAll(ctx, "SELECT a, b FROM t1 WHERE b > (SELECT avg(b) FROM t1 AS x WHERE x.a < t1.a)")
			if err != nil {
				b.Fatal(err)
			}
			_ = rows
		}
	})
}