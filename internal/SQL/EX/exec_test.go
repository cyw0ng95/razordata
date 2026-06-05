package EX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type runCase struct {
	name string
	sql  string
	want [][]interface{}
}

func TestExecutorEndToEnd(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	RegisterTable("users", []Row{
		{Cols: []string{"id", "name", "age"}, Types: []int{1, 2, 1}, Data: []interface{}{int64(1), "alice", int64(30)}},
		{Cols: []string{"id", "name", "age"}, Types: []int{1, 2, 1}, Data: []interface{}{int64(2), "bob", int64(25)}},
		{Cols: []string{"id", "name", "age"}, Types: []int{1, 2, 1}, Data: []interface{}{int64(3), "carol", int64(40)}},
	})

	cases := []runCase{
		{
			name: "select_star",
			sql:  "SELECT * FROM users",
			want: [][]interface{}{
				{int64(1), "alice", int64(30)},
				{int64(2), "bob", int64(25)},
				{int64(3), "carol", int64(40)},
			},
		},
		{
			name: "select_with_filter",
			sql:  "SELECT name FROM users WHERE age > 30",
			want: [][]interface{}{
				{"carol"},
			},
		},
		{
			name: "select_with_and",
			sql:  "SELECT name FROM users WHERE age >= 25 AND age < 35",
			want: [][]interface{}{
				{"alice"},
				{"bob"},
			},
		},
		{
			name: "select_with_in",
			sql:  "SELECT name FROM users WHERE id IN (1, 3)",
			want: [][]interface{}{
				{"alice"},
				{"carol"},
			},
		},
		{
			name: "select_with_between",
			sql:  "SELECT name FROM users WHERE age BETWEEN 25 AND 35",
			want: [][]interface{}{
				{"alice"},
				{"bob"},
			},
		},
		{
			name: "select_with_like",
			sql:  "SELECT name FROM users WHERE name LIKE 'a%'",
			want: [][]interface{}{
				{"alice"},
			},
		},
		{
			name: "select_with_limit",
			sql:  "SELECT name FROM users LIMIT 2",
			want: [][]interface{}{
				{"alice"},
				{"bob"},
			},
		},
		{
			name: "select_with_order_asc",
			sql:  "SELECT name FROM users ORDER BY age",
			want: [][]interface{}{
				{"bob"},
				{"alice"},
				{"carol"},
			},
		},
		{
			name: "select_with_order_desc",
			sql:  "SELECT name FROM users ORDER BY age DESC",
			want: [][]interface{}{
				{"carol"},
				{"alice"},
				{"bob"},
			},
		},
		{
			name: "select_with_alias",
			sql:  "SELECT name AS n FROM users WHERE age > 25",
			want: [][]interface{}{
				{"alice"},
				{"carol"},
			},
		},
		{
			name: "select_with_expr_col",
			sql:  "SELECT age + 1 AS next FROM users WHERE id = 1",
			want: [][]interface{}{
				{int64(31)},
			},
		},
		{
			name: "select_count_star",
			sql:  "SELECT COUNT(*) FROM users",
			want: [][]interface{}{
				{int64(3)},
			},
		},
		{
			name: "select_sum_age",
			sql:  "SELECT SUM(age) FROM users",
			want: [][]interface{}{
				{int64(95)},
			},
		},
		{
			name: "select_avg_age",
			sql:  "SELECT AVG(age) FROM users",
			want: [][]interface{}{
				{float64(95) / float64(3)},
			},
		},
		{
			name: "select_min_age",
			sql:  "SELECT MIN(age) FROM users",
			want: [][]interface{}{
				{int64(25)},
			},
		},
		{
			name: "select_max_age",
			sql:  "SELECT MAX(age) FROM users",
			want: [][]interface{}{
				{int64(40)},
			},
		},
		{
			name: "select_count_with_filter",
			sql:  "SELECT COUNT(*) FROM users WHERE age > 25",
			want: [][]interface{}{
				{int64(2)},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := PS.NewParser(c.sql)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			root := buildPlan(t, stmt)
			got := drain(t, root)
			if !rowsEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func buildPlan(t *testing.T, stmt PS.Stmt) Operator {
	t.Helper()
	sel, ok := stmt.(*PS.Select)
	if !ok {
		t.Fatalf("expected *Select, got %T", stmt)
	}
	scan := NewSeqScan(sel.From)
	var current Operator = scan
	if sel.Where != nil {
		current = NewFilter(current, sel.Where)
	}
	hasAgg := hasAggregatePublic(sel.Cols)
	if hasAgg {
		current = NewAggregate(current, nil, sel.Cols)
	}
	if len(sel.OrderBy) > 0 {
		current = NewSort(current, sel.OrderBy)
	}
	if sel.Limit != nil {
		n, ok := limitInt64Public(sel.Limit)
		if !ok {
			t.Fatalf("non-literal LIMIT not supported in smoke test")
		}
		current = NewLimit(current, n)
	}
	if !hasAgg && !isStarExprPublic(sel.Cols) {
		current = NewProject(current, sel.Cols)
	}
	return current
}

func hasAggregatePublic(cols []PS.Expr) bool {
	for _, c := range cols {
		if walkAgg(c) {
			return true
		}
	}
	return false
}

func walkAgg(e PS.Expr) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *PS.AggregateFunc:
		return true
	case *PS.BinaryExpr:
		return walkAgg(v.Left) || walkAgg(v.Right)
	case *PS.UnaryExpr:
		return walkAgg(v.Operand)
	case *PS.AliasedExpr:
		return walkAgg(v.Expr)
	}
	return false
}

func isStarExprPublic(cols []PS.Expr) bool {
	if len(cols) != 1 {
		return false
	}
	_, ok := cols[0].(*PS.StarExpr)
	return ok
}

func limitInt64Public(e PS.Expr) (int64, bool) {
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val, true
	}
	return 0, false
}

func drain(t *testing.T, op Operator) [][]interface{} {
	t.Helper()
	ctx := context.Background()
	var out [][]interface{}
	for {
		row, err := op.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		cp := make([]interface{}, len(row.Data))
		copy(cp, row.Data)
		out = append(out, cp)
	}
	_ = op.Close()
	return out
}

func rowsEqual(got [][]interface{}, want [][]interface{}) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if len(got[i]) != len(want[i]) {
			return false
		}
		for j := range got[i] {
			if got[i][j] != want[i][j] {
				return false
			}
		}
	}
	return true
}
