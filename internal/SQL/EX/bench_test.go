package EX

import (
	"context"
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func benchFixture(rows int) {
	UnregisterAll()
	RegisterTableSchema("t", []string{"id", "name", "age"})
	tablesMu.Lock()
	existing := tables["t"]
	for i := 0; i < rows; i++ {
		out := Row{
			Cols: []string{"id", "name", "age"},
			Data: []interface{}{int64(i), fmt.Sprintf("u%d", i), int64(20 + (i % 50))},
		}
		existing = append(existing, out)
	}
	tables["t"] = existing
	tablesMu.Unlock()
}

func BenchmarkExecutorQueryAll(b *testing.B) {
	benchFixture(1000)
	ex := NewExecutor()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := ex.QueryAll(ctx, "SELECT * FROM t")
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != 1000 {
			b.Fatalf("expected 1000 rows, got %d", len(rows))
		}
	}
}

func BenchmarkExecutorQueryWithFilter(b *testing.B) {
	benchFixture(1000)
	ex := NewExecutor()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := ex.QueryAll(ctx, "SELECT id, name FROM t WHERE age > 30 AND age < 50")
		if err != nil {
			b.Fatal(err)
		}
		_ = rows
	}
}

func BenchmarkExecutorCountStar(b *testing.B) {
	benchFixture(1000)
	ex := NewExecutor()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := ex.QueryAll(ctx, "SELECT COUNT(*) FROM t")
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != 1 {
			b.Fatalf("expected 1 row, got %d", len(rows))
		}
	}
}

func BenchmarkExecutorInsert(b *testing.B) {
	UnregisterAll()
	RegisterTableSchema("t", []string{"id", "name", "age"})
	ex := NewExecutor()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ex.Exec(ctx, fmt.Sprintf("INSERT INTO t VALUES (%d, 'u', 30)", i))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPlannerMemoization(b *testing.B) {
	UnregisterAll()
	RegisterTableSchema("t", []string{"id", "name", "age"})
	ex := NewExecutor()
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 'x', 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 'y', 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 'z', 30)")
	_, _ = ex.QueryAll(ctx, "SELECT * FROM t WHERE age > 5 ORDER BY age LIMIT 100")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, "SELECT * FROM t WHERE age > 5 ORDER BY age LIMIT 100")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPlannerMemoizeKey(b *testing.B) {
	stmt := memoKeyFixture()
	for i := 0; i < b.N; i++ {
		_ = serializeKey(stmt)
	}
}

func memoKeyFixture() PS.Stmt {
	pp := PS.NewParser("SELECT id, name FROM t WHERE age > 30 AND age < 50 ORDER BY age LIMIT 100")
	stmt, _ := pp.Parse()
	return stmt
}
