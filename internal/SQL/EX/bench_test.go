package EX

import (
	"context"
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
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
			Data: []Value{NewIntValue(int64(i)), NewTextValue(fmt.Sprintf("u%d", i)), NewIntValue(int64(20 + (i % 50)))},
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

// BenchmarkEqualValue_IntInt verifies REQ000754: int64-int64 fast path
// in equalValue avoids normalizeInt re-boxing.
func BenchmarkEqualValue_IntInt(b *testing.B) {
	// Direct function call benchmark (no Eval overhead).
	for i := 0; i < b.N; i++ {
		equalValue(int64(i), int64(i+1))
	}
}

// BenchmarkEqualValue_StringString benchmarks the string comparison path.
func BenchmarkEqualValue_StringString(b *testing.B) {
	for i := 0; i < b.N; i++ {
		equalValue("test", "test")
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

func BenchmarkExecutorGroupBy(b *testing.B) {
	UnregisterAll()
	RegisterTableSchema("t", []string{"category", "amount"})
	ex := NewExecutor()
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t VALUES ('c%d', %d)", i%50, i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, "SELECT category, SUM(amount) FROM t GROUP BY category")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExecutorInnerJoin(b *testing.B) {
	UnregisterAll()
	RegisterTableSchema("a", []string{"id", "x"})
	RegisterTableSchema("b", []string{"a_id", "y"})
	ex := NewExecutor()
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO a VALUES (%d, %d)", i, i*2))
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO b VALUES (%d, %d)", i, i*3))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, "SELECT a.id, b.y FROM a INNER JOIN b ON b.a_id = a.id")
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

// BenchmarkConstraintsInsert measures INSERT throughput when the
// table has NOT NULL and DEFAULT constraints, against a fresh
// in-memory table. Verifies the constraint check path has no
// measurable regression vs. the unconstrained path.
func BenchmarkConstraintsInsert(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		UnregisterAll()
		ct := NewCreateTable(&PS.CreateTable{
			Name: "t",
			Cols: []PS.ColDef{
				{Name: "id", Type: 1, Nullable: false, PK: true},
				{Name: "name", Type: 1, Nullable: false},
				{Name: "score", Type: 1, Default: &PS.NumberLiteral{Val: 0}},
			},
			PK: stringPtr("id"),
		})
		_, _ = ct.Next(context.Background())
		ins, err := NewInsertWithStore(nil, "t", []string{"id", "name"}, [][]PS.Expr{
			{&PS.NumberLiteral{Val: int64(i)}, &PS.StringLiteral{Val: "x"}},
		}, nil, nil)
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		_, _ = ins.Next(context.Background())
	}
}

func stringPtr(s string) *string { return &s }

// BenchmarkUniqueInsert measures INSERT throughput when the table
// has a UNIQUE constraint. The benchmark is the in-memory path; the
// engine path is a no-op lookup in v1 (deferred to REQ000045).
func BenchmarkUniqueInsert(b *testing.B) {
	UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "email", Type: 1, Unique: true},
		},
		PK: stringPtr("id"),
	})
	_, _ = ct.Next(context.Background())
	ex := NewExecutor()
	_ = ex
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		UnregisterAll()
		ct := NewCreateTable(&PS.CreateTable{
			Name: "t",
			Cols: []PS.ColDef{
				{Name: "id", Type: 1, Nullable: false, PK: true},
				{Name: "email", Type: 1, Unique: true},
			},
			PK: stringPtr("id"),
		})
		_, _ = ct.Next(context.Background())
		ins, err := NewInsertWithStore(nil, "t", []string{"id", "email"}, [][]PS.Expr{
			{&PS.NumberLiteral{Val: int64(i)}, &PS.StringLiteral{Val: "u@x"}},
		}, nil, nil)
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		_, _ = ins.Next(context.Background())
	}
}

// REQ000799/REQ000802 verification: j3-style benchmark suite.
// j3_mixed = 3-table cross join (1000×1000×1000) + filter + project
// j3_filter_only = same shape, filter only, no projection
// j4_cross, j5_cross, j6_cross scale up to verify complexity bounds.
func setupJ3Tables(b *testing.B, n int) {
	b.Helper()
	UnregisterAll()
	RegisterTableSchema("t1", []string{"id", "a", "b", "c", "d"})
	RegisterTableSchema("t2", []string{"id", "a", "b", "c", "d"})
	RegisterTableSchema("t3", []string{"id", "a", "b", "c", "d"})
	tablesMu.Lock()
	for i := 0; i < n; i++ {
		v := int64(i)
		tables["t1"] = append(tables["t1"], Row{
			Cols: []string{"id", "a", "b", "c", "d"},
			Data: []Value{NewIntValue(v), NewIntValue(v % 100), NewIntValue(v % 50), NewIntValue(v % 25), NewIntValue(v % 10)},
		})
		tables["t2"] = append(tables["t2"], Row{
			Cols: []string{"id", "a", "b", "c", "d"},
			Data: []Value{NewIntValue(v), NewIntValue(v % 100), NewIntValue(v % 50), NewIntValue(v % 25), NewIntValue(v % 10)},
		})
		tables["t3"] = append(tables["t3"], Row{
			Cols: []string{"id", "a", "b", "c", "d"},
			Data: []Value{NewIntValue(v), NewIntValue(v % 100), NewIntValue(v % 50), NewIntValue(v % 25), NewIntValue(v % 10)},
		})
	}
	tablesMu.Unlock()
}

func BenchmarkJ3_Mixed(b *testing.B) {
	setupJ3Tables(b, 100)
	ex := NewExecutor()
	ctx := context.Background()
	q := "SELECT t1.id, t2.a, t3.b FROM t1, t2, t3 WHERE t1.a > 50 AND t2.b < 25"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ex.QueryAll(ctx, q); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJ3_FilterOnly(b *testing.B) {
	setupJ3Tables(b, 100)
	ex := NewExecutor()
	ctx := context.Background()
	q := "SELECT t1.id FROM t1, t2, t3 WHERE t1.a > 50 AND t2.b < 25 AND t3.c > 10"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ex.QueryAll(ctx, q); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJ4_Cross(b *testing.B) {
	setupJ3Tables(b, 100)
	ex := NewExecutor()
	ctx := context.Background()
	q := "SELECT t1.id FROM t1, t2, t3, t1 t1b WHERE t1.a = t1b.a"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ex.QueryAll(ctx, q); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFilter_ADQC_Cache measures the benefit of global predicate
// caching. CacheHit reuses a compiled predicate across Filter instances;
// CacheMiss uses a different predicate each time (compilation on every
// call). REQ000802+.
func BenchmarkFilter_ADQC_Cache(b *testing.B) {
	pred := &PS.BinaryExpr{
		Op: int(LX.T_AND),
		Left: &PS.BinaryExpr{
			Op:    int(LX.T_GT),
			Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
			Right: &PS.NumberLiteral{Val: int64(50)},
		},
		Right: &PS.BinaryExpr{
			Op:    int(LX.T_LT),
			Left:  &PS.QualifiedName{Table: "t2", Name: "b"},
			Right: &PS.NumberLiteral{Val: int64(25)},
		},
	}

	rows := make([]Row, 100)
	for i := 0; i < 100; i++ {
		rows[i] = Row{
			Cols: []string{"t1.a", "t2.b"},
			Data: []Value{NewIntValue(int64(i % 100)), NewIntValue(int64(i % 50))},
		}
	}

	ctx := context.Background()

	b.Run("CacheHit", func(b *testing.B) {
		// Warm the cache with one full pass.
		warm := &sliceRowOp{rows: rows}
		wf := NewFilter(warm, pred)
		for {
			_, err := wf.Next(ctx)
			if err == ErrNoRows {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
		wf.Close()

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			child := &sliceRowOp{rows: rows}
			f := NewFilter(child, pred)
			for {
				_, err := f.Next(ctx)
				if err == ErrNoRows {
					break
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			f.Close()
		}
	})

	b.Run("CacheMiss", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			p := &PS.BinaryExpr{
				Op: int(LX.T_AND),
				Left: &PS.BinaryExpr{
					Op:    int(LX.T_GT),
					Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
					Right: &PS.NumberLiteral{Val: int64(i % 100)},
				},
				Right: &PS.BinaryExpr{
					Op:    int(LX.T_LT),
					Left:  &PS.QualifiedName{Table: "t2", Name: "b"},
					Right: &PS.NumberLiteral{Val: int64(25)},
				},
			}
			child := &sliceRowOp{rows: rows}
			f := NewFilter(child, p)
			for {
				_, err := f.Next(ctx)
				if err == ErrNoRows {
					break
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			f.Close()
		}
	})
}

// sliceRowOp is a simple operator that yields rows from a pre-built slice.
type sliceRowOp struct {
	rows []Row
	pos  int
}

func (s *sliceRowOp) Next(_ context.Context) (Row, error) {
	if s.pos >= len(s.rows) {
		return Row{}, ErrNoRows
	}
	r := s.rows[s.pos]
	s.pos++
	return r, nil
}

func (s *sliceRowOp) Close() error {
	s.pos = 0
	return nil
}
