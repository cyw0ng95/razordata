package EX

import (
	"context"
	"testing"

	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type runCase struct {
	name string
	sql  string
	want [][]any
}

func TestExecutorEndToEnd(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("users", []Row{
		{Cols: []string{"id", "name", "age"}, Types: []LX.TokenType{LX.T_INT_KW, LX.T_TEXT, LX.T_INT_KW}, Data: []Value{NewIntValue(int64(1)), NewTextValue("alice"), NewIntValue(int64(30))}},
		{Cols: []string{"id", "name", "age"}, Types: []LX.TokenType{LX.T_INT_KW, LX.T_TEXT, LX.T_INT_KW}, Data: []Value{NewIntValue(int64(2)), NewTextValue("bob"), NewIntValue(int64(25))}},
		{Cols: []string{"id", "name", "age"}, Types: []LX.TokenType{LX.T_INT_KW, LX.T_TEXT, LX.T_INT_KW}, Data: []Value{NewIntValue(int64(3)), NewTextValue("carol"), NewIntValue(int64(40))}},
	})

	cases := []runCase{
		{
			name: "select_star",
			sql:  "SELECT * FROM users",
			want: [][]any{
				{int64(1), "alice", int64(30)},
				{int64(2), "bob", int64(25)},
				{int64(3), "carol", int64(40)},
			},
		},
		{
			name: "select_with_filter",
			sql:  "SELECT name FROM users WHERE age > 30",
			want: [][]any{
				{"carol"},
			},
		},
		{
			name: "select_with_and",
			sql:  "SELECT name FROM users WHERE age >= 25 AND age < 35",
			want: [][]any{
				{"alice"},
				{"bob"},
			},
		},
		{
			name: "select_with_in",
			sql:  "SELECT name FROM users WHERE id IN (1, 3)",
			want: [][]any{
				{"alice"},
				{"carol"},
			},
		},
		{
			name: "select_with_between",
			sql:  "SELECT name FROM users WHERE age BETWEEN 25 AND 35",
			want: [][]any{
				{"alice"},
				{"bob"},
			},
		},
		{
			name: "select_with_like",
			sql:  "SELECT name FROM users WHERE name LIKE 'a%'",
			want: [][]any{
				{"alice"},
			},
		},
		{
			name: "select_with_limit",
			sql:  "SELECT name FROM users LIMIT 2",
			want: [][]any{
				{"alice"},
				{"bob"},
			},
		},
		{
			name: "select_with_order_asc",
			sql:  "SELECT name FROM users ORDER BY age",
			want: [][]any{
				{"bob"},
				{"alice"},
				{"carol"},
			},
		},
		{
			name: "select_with_order_desc",
			sql:  "SELECT name FROM users ORDER BY age DESC",
			want: [][]any{
				{"carol"},
				{"alice"},
				{"bob"},
			},
		},
		{
			name: "select_with_alias",
			sql:  "SELECT name AS n FROM users WHERE age > 25",
			want: [][]any{
				{"alice"},
				{"carol"},
			},
		},
		{
			name: "select_with_expr_col",
			sql:  "SELECT age + 1 AS next FROM users WHERE id = 1",
			want: [][]any{
				{int64(31)},
			},
		},
		{
			name: "select_count_star",
			sql:  "SELECT COUNT(*) FROM users",
			want: [][]any{
				{int64(3)},
			},
		},
		{
			name: "select_sum_age",
			sql:  "SELECT SUM(age) FROM users",
			want: [][]any{
				{int64(95)},
			},
		},
		{
			name: "select_avg_age",
			sql:  "SELECT AVG(age) FROM users",
			want: [][]any{
				{float64(95) / float64(3)},
			},
		},
		{
			name: "select_min_age",
			sql:  "SELECT MIN(age) FROM users",
			want: [][]any{
				{int64(25)},
			},
		},
		{
			name: "select_max_age",
			sql:  "SELECT MAX(age) FROM users",
			want: [][]any{
				{int64(40)},
			},
		},
		{
			name: "select_count_with_filter",
			sql:  "SELECT COUNT(*) FROM users WHERE age > 25",
			want: [][]any{
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
	scan := OP.NewSeqScan(sel.From)
	var current Operator = scan
	if sel.Where != nil {
		current = OP.NewFilter(current, sel.Where)
	}
	hasAgg := hasAggregatePublic(sel.Cols)
	if hasAgg {
		current = AG.NewAggregate(current, nil, sel.Cols)
	}
	if len(sel.OrderBy) > 0 {
		current = OP.NewSort(current, sel.OrderBy)
	}
	if sel.Limit != nil {
		n, ok := limitInt64Public(sel.Limit)
		if !ok {
			t.Fatalf("non-literal LIMIT not supported in smoke test")
		}
		current = OP.NewLimit(current, n)
	}
	if !hasAgg && !isStarExprPublic(sel.Cols) {
		current = OP.NewProject(current, sel.Cols)
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

func drain(t *testing.T, op Operator) [][]any {
	t.Helper()
	ctx := context.Background()
	var out [][]any
	for {
		row, err := op.Next(ctx)
		if err == DT.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		cp := make([]any, len(row.Data))
		copy(cp, valueSliceToAny(row.Data))
		out = append(out, cp)
	}
	_ = op.Close()
	return out
}

// REQ000638: ExtractParamTypes on SQL without ? returns empty.
func TestExtractParamTypes_NoParams(t *testing.T) {
	e := NewExecutor()
	got := e.ExtractParamTypes("SELECT 1")
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

// REQ000638: ShallowCopy is independent (mutable state not shared).
func TestShallowCopy_Independent(t *testing.T) {
	e := NewExecutor()
	e.SetSnapshot(42)
	c := e.ShallowCopy()
	if c.Snapshot() != 0 {
		t.Errorf("ShallowCopy snapshot: got %d, want 0", c.Snapshot())
	}
	c.SetSnapshot(99)
	if e.Snapshot() != 42 {
		t.Errorf("original snapshot changed after copy: got %d, want 42", e.Snapshot())
	}
}

// REQ000638: OP.SeqScan.Close on uninitialized scan does not panic or error.
func TestSeqScan_CloseUninitialized(t *testing.T) {
	s := OP.NewSeqScan("test")
	if err := s.Close(); err != nil {
		t.Errorf("Close on uninitialized OP.SeqScan: %v", err)
	}
}

// REQ000638: OP.SeqScan double-close is idempotent.
func TestSeqScan_CloseDouble(t *testing.T) {
	s := OP.NewSeqScan("test")
	err1 := s.Close()
	err2 := s.Close()
	if err1 != nil || err2 != nil {
		t.Errorf("double Close errors: %v, %v", err1, err2)
	}
}

// REQ000638: helper — compare result rows (both [][]any).
func rowsEqual(got, want [][]any) bool {
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
