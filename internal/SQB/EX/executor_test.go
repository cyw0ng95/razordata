package EX

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestExecutorCRUD(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("users", []string{"id", "name", "age"})

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO users VALUES (1, 'alice', 30)"); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO users VALUES (2, 'bob', 25)"); err != nil {
		t.Fatalf("insert 2: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO users VALUES (3, 'carol', 40)"); err != nil {
		t.Fatalf("insert 3: %v", err)
	}

	rows, err := ex.QueryAll(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	res, err := ex.Exec(ctx, "UPDATE users SET age = 31 WHERE name = 'alice'")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
	}

	res, err = ex.Exec(ctx, "DELETE FROM users WHERE age > 35")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row deleted, got %d", res.RowsAffected)
	}

	rows, err = ex.QueryAll(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query after: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows remaining, got %d", len(rows))
	}
}

func TestExecutorQueryCols(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"a", "b"})
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 'x')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rs, err := ex.Query(ctx, "SELECT a, b FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !slices.Equal(rs.Cols, []string{"a", "b"}) {
		t.Errorf("expected cols [a b], got %v", rs.Cols)
	}
}

func TestExecutorInSubquery(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("users", []string{"id", "name", "age"})
	ex.RegisterTable("orders", []string{"user_id"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO users VALUES (1, 'alice', 30)",
		"INSERT INTO users VALUES (2, 'bob', 25)",
		"INSERT INTO users VALUES (3, 'carol', 40)",
		"INSERT INTO orders VALUES (1)",
		"INSERT INTO orders VALUES (3)",
	} {
		if _, err := ex.Exec(ctx, v); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT name FROM users WHERE id IN (SELECT user_id FROM orders)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Data[0].ToAny().(string)] = true
	}
	if !got["alice"] || !got["carol"] || got["bob"] {
		t.Errorf("unexpected results: %v", got)
	}
}

func TestExecutorDistinct(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"category", "value"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO t VALUES ('a', 1)",
		"INSERT INTO t VALUES ('a', 2)",
		"INSERT INTO t VALUES ('a', 1)",
		"INSERT INTO t VALUES ('b', 10)",
		"INSERT INTO t VALUES ('b', 10)",
		"INSERT INTO t VALUES ('c', 100)",
	} {
		if _, err := ex.Exec(ctx, v); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT DISTINCT category FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 distinct, got %d", len(rows))
	}
	rows, err = ex.QueryAll(ctx, "SELECT DISTINCT category, value FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 distinct (a,1), (a,2), (b,10), (c,100), got %d", len(rows))
	}
}

func TestExecutorDistinctWithWhere(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO t VALUES (1)",
		"INSERT INTO t VALUES (2)",
		"INSERT INTO t VALUES (2)",
		"INSERT INTO t VALUES (3)",
		"INSERT INTO t VALUES (3)",
		"INSERT INTO t VALUES (3)",
	} {
		ex.Exec(ctx, v)
	}
	rows, _ := ex.QueryAll(ctx, "SELECT DISTINCT x FROM t WHERE x > 1")
	if len(rows) != 2 {
		t.Errorf("expected 2 distinct (2, 3), got %d", len(rows))
	}
}

func TestExecutorExists(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"a"})
	ex.RegisterTable("has_orders", []string{"id"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	ex.Exec(ctx, "INSERT INTO has_orders VALUES (10)")
	rows, err := ex.QueryAll(ctx, "SELECT a FROM t WHERE EXISTS (SELECT 1 FROM has_orders)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 (EXISTS matches every row), got %d", len(rows))
	}
}

func TestExecutorExistsFalse(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("users", []string{"id", "name"})
	ex.RegisterTable("orders", []string{"user_id"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO users VALUES (1, 'alice')")
	ex.Exec(ctx, "INSERT INTO users VALUES (2, 'bob')")
	rows, _ := ex.QueryAll(ctx, "SELECT name FROM users WHERE EXISTS (SELECT 1 FROM orders)")
	if len(rows) != 0 {
		t.Errorf("expected 0 rows (no orders), got %d", len(rows))
	}
}

func TestExecutorExistsCorrelated(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("users", []string{"id", "name"})
	ex.RegisterTable("orders", []string{"user_id"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO users VALUES (1, 'alice')",
		"INSERT INTO users VALUES (2, 'bob')",
		"INSERT INTO users VALUES (3, 'carol')",
		"INSERT INTO orders VALUES (1)",
		"INSERT INTO orders VALUES (3)",
	} {
		ex.Exec(ctx, v)
	}
	rows, err := ex.QueryAll(ctx, "SELECT name FROM users WHERE EXISTS (SELECT 1 FROM orders WHERE user_id = id)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 users with orders, got %d", len(rows))
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Data[0].ToAny().(string)] = true
	}
	if !got["alice"] || !got["carol"] || got["bob"] {
		t.Errorf("unexpected: %v", got)
	}
}

func TestExecutorInSubqueryCorrelated(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("users", []string{"id", "name"})
	ex.RegisterTable("orders", []string{"user_id"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO users VALUES (1, 'alice')",
		"INSERT INTO users VALUES (2, 'bob')",
		"INSERT INTO users VALUES (3, 'carol')",
		"INSERT INTO orders VALUES (1)",
		"INSERT INTO orders VALUES (3)",
	} {
		ex.Exec(ctx, v)
	}
	rows, _ := ex.QueryAll(ctx, "SELECT name FROM users WHERE id IN (SELECT user_id FROM orders WHERE user_id = id)")
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestExecutorScalarSubquery(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})
	ex.RegisterTable("counters", []string{"n"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3)")
	ex.Exec(ctx, "INSERT INTO counters VALUES (10)")
	rows, err := ex.QueryAll(ctx, "SELECT x, (SELECT n FROM counters) AS c FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if !r.Data[1].Equal(NewIntValue(int64(10))) {
			t.Errorf("expected c=10, got %v", r.Data[1])
		}
	}
}

func TestExecutorScalarSubqueryEmpty(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})
	ex.RegisterTable("counters", []string{"n"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	rows, _ := ex.QueryAll(ctx, "SELECT x, (SELECT n FROM counters) AS c FROM t")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[1].IsNull() == false {
		t.Errorf("expected nil c, got %v", rows[0].Data[1])
	}
}

func TestExecutorExplain(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("users", []string{"id", "name", "age"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO users VALUES (1, 'alice', 30)")
	out, err := ex.Explain("SELECT name FROM users WHERE age > 25 ORDER BY name LIMIT 10")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	for _, want := range []string{"Scan", "Filter", "Sort", "Limit", "Project"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan missing %q in:\n%s", want, out)
		}
	}
}

func TestExecutorExplainDistinct(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})
	ex.Exec(context.Background(), "INSERT INTO t VALUES (1)")
	out, _ := ex.Explain("SELECT DISTINCT x FROM t")
	if !strings.Contains(out, "Distinct") {
		t.Errorf("expected Distinct in plan, got:\n%s", out)
	}
}

func TestExecutorInnerJoin(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("users", []string{"id", "name"})
	ex.RegisterTable("orders", []string{"id", "user_id"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO users VALUES (1, 'alice')",
		"INSERT INTO users VALUES (2, 'bob')",
		"INSERT INTO users VALUES (3, 'carol')",
		"INSERT INTO orders VALUES (10, 1)",
		"INSERT INTO orders VALUES (20, 3)",
		"INSERT INTO orders VALUES (30, 3)",
	} {
		ex.Exec(ctx, v)
	}
	rows, err := ex.QueryAll(ctx, "SELECT users.name, orders.id FROM users INNER JOIN orders ON orders.user_id = users.id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 joined rows, got %d", len(rows))
	}
	byName := map[string][]int64{}
	for _, r := range rows {
		byName[r.Data[0].ToAny().(string)] = append(byName[r.Data[0].ToAny().(string)], r.Data[1].ToAny().(int64))
	}
	if len(byName["alice"]) != 1 || byName["alice"][0] != 10 {
		t.Errorf("alice: expected [10], got %v", byName["alice"])
	}
	if len(byName["carol"]) != 2 {
		t.Errorf("carol: expected 2 orders, got %v", byName["carol"])
	}
	if _, ok := byName["bob"]; ok {
		t.Errorf("bob should not appear (no orders), got %v", byName["bob"])
	}
}

func TestExecutorCrossJoin(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("a", []string{"x"})
	ex.RegisterTable("b", []string{"y"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO a VALUES (1)")
	ex.Exec(ctx, "INSERT INTO a VALUES (2)")
	ex.Exec(ctx, "INSERT INTO b VALUES (10)")
	ex.Exec(ctx, "INSERT INTO b VALUES (20)")
	rows, err := ex.QueryAll(ctx, "SELECT * FROM a CROSS JOIN b")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 4 {
		t.Errorf("expected 4 (2x2) rows, got %d", len(rows))
	}
}

func TestExecutorGroupBy(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("orders", []string{"category", "amount"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO orders VALUES ('a', 10)",
		"INSERT INTO orders VALUES ('a', 20)",
		"INSERT INTO orders VALUES ('b', 5)",
		"INSERT INTO orders VALUES ('b', 15)",
		"INSERT INTO orders VALUES ('c', 100)",
	} {
		ex.Exec(ctx, v)
	}
	rows, err := ex.QueryAll(ctx, "SELECT category, SUM(amount) FROM orders GROUP BY category ORDER BY category")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(rows))
	}
	want := map[string]int64{"a": 30, "b": 20, "c": 100}
	for _, r := range rows {
		cat, ok := r.Data[0].ToAny().(string)
		if !ok {
			t.Fatalf("expected string type for Data[0], got %s", r.Data[0].Kind)
		}
		sum, ok := r.Data[1].ToAny().(int64)
		if !ok {
			t.Fatalf("expected int64 type for Data[1], got %s", r.Data[1].Kind)
		}
		if want[cat] != sum {
			t.Errorf("category %s: got %d, want %d", cat, sum, want[cat])
		}
	}
}

func TestExecutorHaving(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("orders", []string{"category", "amount"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO orders VALUES ('a', 10)",
		"INSERT INTO orders VALUES ('a', 20)",
		"INSERT INTO orders VALUES ('b', 5)",
		"INSERT INTO orders VALUES ('b', 15)",
		"INSERT INTO orders VALUES ('c', 100)",
	} {
		ex.Exec(ctx, v)
	}
	rows, err := ex.QueryAll(ctx, "SELECT category, SUM(amount) FROM orders GROUP BY category HAVING SUM(amount) > 25 ORDER BY category")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 groups (>25), got %d", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Data[0].ToAny().(string)] = true
	}
	if !seen["a"] || !seen["c"] || seen["b"] {
		t.Errorf("unexpected: %v", seen)
	}
}

func TestExecutorGroupByCount(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"k"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO t VALUES ('a')",
		"INSERT INTO t VALUES ('a')",
		"INSERT INTO t VALUES ('a')",
		"INSERT INTO t VALUES ('b')",
		"INSERT INTO t VALUES ('b')",
		"INSERT INTO t VALUES ('c')",
	} {
		ex.Exec(ctx, v)
	}
	rows, _ := ex.QueryAll(ctx, "SELECT k, COUNT(*) FROM t GROUP BY k ORDER BY k")
	if len(rows) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(rows))
	}
	want := map[string]int64{"a": 3, "b": 2, "c": 1}
	for _, r := range rows {
		if want[r.Data[0].ToAny().(string)] != r.Data[1].ToAny().(int64) {
			t.Errorf("%s: got %v, want %d", r.Data[0].ToAny().(string), r.Data[1], want[r.Data[0].ToAny().(string)])
		}
	}
}

func TestExecutorIndexScanSelection(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("users", []string{"id", "name"})
	ex.RegisterIndex("users", "idx_id", []string{"id"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO users VALUES (1, 'alice')")
	ex.Exec(ctx, "INSERT INTO users VALUES (2, 'bob')")
	plan, _ := ex.Explain("SELECT * FROM users WHERE id = 1")
	if !strings.Contains(plan, "Search") {
		t.Errorf("expected Search (IndexScan), plan was:\n%s", plan)
	}
	plan2, _ := ex.Explain("SELECT * FROM users WHERE name = 'alice'")
	if !strings.Contains(plan2, "Scan") {
		t.Errorf("expected Scan (SeqScan) (no index on name), plan was:\n%s", plan2)
	}
}
