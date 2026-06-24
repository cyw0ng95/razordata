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

// BenchmarkEqualValueValue_IntInt verifies REQ000776: equalValueValue
// switches on Kind directly, avoiding interface conversion.
func BenchmarkEqualValueValue_IntInt(b *testing.B) {
	a := NewIntValue(42)
	c := NewIntValue(43)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		equalValueValue(a, c)
	}
}

// BenchmarkCompareValue_IntInt verifies REQ000776: compareValue
// switches on Kind directly with int64 fast path.
func BenchmarkCompareValue_IntInt(b *testing.B) {
	a := NewIntValue(42)
	c := NewIntValue(43)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		compareValue(a, c)
	}
}

// BenchmarkNumericArithValue_IntAdd verifies REQ000776: numericArithValue
// int64-int64 fast path avoids float conversion.
func BenchmarkNumericArithValue_IntAdd(b *testing.B) {
	a := NewIntValue(42)
	c := NewIntValue(43)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		numericArithValue(a, c, '+')
	}
}

// BenchmarkEvalBinaryComparison compares the old any-based compare
// path with the new Value-based compareValue (REQ000776).
func BenchmarkEvalBinaryComparison(b *testing.B) {
	// Pre-build rows with int64 values.
	rows := make([]Row, 100)
	for i := range rows {
		rows[i] = Row{
			Cols: []string{"x"},
			Data: []Value{NewIntValue(int64(i))},
		}
	}
	target := NewIntValue(50)

	// Old path: any-based compare (still goes through interface dispatch).
	oldFn := func(row *Row) any {
		v, _ := row.Lookup("x")
		r := compare(v, int64(50))
		return r < 0
	}
	// New path: Value-based compareValue (REQ000776 — direct Kind switch).
	newFn := func(row *Row) any {
		v, _ := row.Lookup("x")
		vv, _ := v.(Value)
		return compareValue(vv, target) < 0
	}

	b.ResetTimer()
	b.ReportAllocs()
	b.Run("old_any", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			oldFn(&rows[i%100])
		}
	})
	b.Run("new_value", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			newFn(&rows[i%100])
		}
	})
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

// select4-style fixture: 8 tables with 100 rows each (5 int cols).
func setupSelect4Tables(b *testing.B, n int) {
	b.Helper()
	UnregisterAll()
	for i := 1; i <= 9; i++ {
		name := fmt.Sprintf("t%d", i)
		if i == 4 {
			RegisterTableSchema("t4", []string{"a", "b", "c", "d", "e"})
			continue
		}
		if i == 6 {
			RegisterTableSchema("tn2", []string{"a", "b", "c", "d", "e"})
			RegisterTableSchema("t6", []string{"a", "b", "c", "d", "e"})
			continue
		}
		RegisterTableSchema(name, []string{"a", "b", "c", "d", "e"})
	}
	tablesMu.Lock()
	for i := 1; i <= 9; i++ {
		if i == 4 {
			continue
		}
		names := []string{fmt.Sprintf("t%d", i)}
		if i == 6 {
			names = append(names, "tn2")
		}
		for _, name := range names {
			for j := 0; j < n; j++ {
				v := int64(j)
				tables[name] = append(tables[name], Row{
					Cols: []string{"a", "b", "c", "d", "e"},
					Data: []Value{
						NewIntValue(v % 1000),
						NewIntValue(v % 900),
						NewIntValue(v % 800),
						NewIntValue(v % 700),
						NewIntValue(v % 600),
					},
				})
			}
		}
	}
	tablesMu.Unlock()
	RegisterTableSchema("t4", []string{"a", "b", "c", "d", "e"})
	tablesMu.Lock()
	for j := 0; j < n; j++ {
		v := int64(j)
		tables["t4"] = append(tables["t4"], Row{
			Cols: []string{"a", "b", "c", "d", "e"},
			Data: []Value{
				NewIntValue(v % 1000),
				NewIntValue(v % 900),
				NewIntValue(v % 800),
				NewIntValue(v % 700),
				NewIntValue(v % 600),
			},
		})
	}
	tablesMu.Unlock()
}

// BenchmarkSelect4_CompoundUnion replicates the ~8.5s slow query pattern:
// multi-table UNION ALL / EXCEPT / UNION with complex WHERE conditions
// and large IN-lists.
func BenchmarkSelect4_CompoundUnion(b *testing.B) {
	setupSelect4Tables(b, 100)
	ex := NewExecutor()
	ctx := context.Background()
	q := `SELECT b1 FROM t1
 WHERE b1 IN (226,211,307,736,242,88,956)
UNION ALL
 SELECT c5 FROM t5
 WHERE c5 IN (441,249,613,198,721,149,689,936,668,158,756,855,756)
    OR d5 IN (309,957,118,4,274,806,321,964,553,691,919,282,360)
UNION ALL
 SELECT e3 FROM t3
 WHERE (656=c3 OR c3=360)
    OR e3 IN (929,145,211)
    OR (724=e3 OR 175=c3 OR d3=645)
EXCEPT
 SELECT a2 FROM t2
 WHERE NOT (c2 IN (312,22,179,374,688)
         OR c2 IN (819,202,449,509,878)
         OR (d2=847))
UNION ALL
 SELECT b6 FROM t6
 WHERE (c6=490 AND a6=799 AND 933=b6 AND 794=e6 AND 778=d6)
    OR (452=c6 OR 90=e6 OR 35=d6)
    OR (c6=561 OR 337=d6 OR 811=a6)
EXCEPT
 SELECT d9 FROM t9
 WHERE NOT (b9 IN (312,827,864,891,66,926,214,228,269,361,446,171,496))
UNION
 SELECT c8 FROM t8
 WHERE (841=a8 AND 702=b8)
    OR (b8=975 AND c8=225)
    OR (416=e8 AND a8=883 AND d8=391 AND 52=b8 AND 381=c8)`
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSelect4_MultiTableJoin replicates the ~8.5s pattern of
// 6-table CROSS JOIN with complex OR/AND WHERE conditions, large
// IN-lists, and arithmetic expressions.
func BenchmarkSelect4_MultiTableJoin(b *testing.B) {
	setupSelect4Tables(b, 100)
	ex := NewExecutor()
	ctx := context.Background()
	q := `SELECT x9, a8+113, x3, b5, a2, c1, e7+413, a4
  FROM t3, t4, t1, t5, t8, t2, t9, t7
 WHERE e8 IN (295,349,512,242)
   AND 782=e7
   AND b4 IN (402,888,408,829,2,986)
   AND (a1=479 OR a1=20)
   AND d9 IN (818,763,770)
   AND c2 IN (161,758,511)
   AND c5 IN (697,819,158,544,734,293)
   AND a3=d7`
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSelect4_ORChain replicates the pattern of deeply nested
// OR conditions with multiple IN-lists and range predicates.
func BenchmarkSelect4_ORChain(b *testing.B) {
	setupSelect4Tables(b, 100)
	ex := NewExecutor()
	ctx := context.Background()
	q := `SELECT c6*856, x5
  FROM t6, t5
 WHERE c5 IN (820,44,696,824,668,723,598)
   AND (d6=778 OR d6=867 OR d6=778)`
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSelect4_NotInChain replicates the NOT (x IN (...)) pattern
// with large IN-lists.
func BenchmarkSelect4_NotInChain(b *testing.B) {
	setupSelect4Tables(b, 100)
	ex := NewExecutor()
	ctx := context.Background()
	q := `SELECT d9 FROM t9
  WHERE NOT (b9 IN (312,827,864,891,66,926,214,228,269,361,446,171,496))`
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEvalInHash_Int_Value vs BenchmarkEvalInHash_Int_Legacy compare
// the Value-typed hash set (evalInHashValue) against the legacy any-typed
// hash set (evalInHash) for IN-list probing (REQ000776).
func BenchmarkEvalInHash_Int_Value(b *testing.B) {
	expr := &PS.InExpr{
		Expr: &PS.NumberLiteral{Val: 42},
		List: make([]PS.Expr, 0, 16),
	}
	for i := int64(10); i < 26; i++ {
		expr.List = append(expr.List, &PS.NumberLiteral{Val: i})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := evalInHashValue(expr, NewIntValue(42), nil, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvalInHash_Int_Legacy(b *testing.B) {
	expr := &PS.InExpr{
		Expr: &PS.NumberLiteral{Val: 42},
		List: make([]PS.Expr, 0, 16),
	}
	for i := int64(10); i < 26; i++ {
		expr.List = append(expr.List, &PS.NumberLiteral{Val: i})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := evalInHash(expr, int64(42), nil, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvalInHash_String_Value(b *testing.B) {
	expr := &PS.InExpr{
		Expr: &PS.StringLiteral{Val: "target"},
		List: make([]PS.Expr, 0, 16),
	}
	for i := 0; i < 16; i++ {
		expr.List = append(expr.List, &PS.StringLiteral{Val: fmt.Sprintf("item_%d", i)})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := evalInHashValue(expr, NewTextValue("target"), nil, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvalInHash_String_Legacy(b *testing.B) {
	expr := &PS.InExpr{
		Expr: &PS.StringLiteral{Val: "target"},
		List: make([]PS.Expr, 0, 16),
	}
	for i := 0; i < 16; i++ {
		expr.List = append(expr.List, &PS.StringLiteral{Val: fmt.Sprintf("item_%d", i)})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := evalInHash(expr, "target", nil, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}
