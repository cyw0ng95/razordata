package EX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func TestEval(t *testing.T) {
	cases := []struct {
		name   string
		expr   PS.Expr
		params []interface{}
		want   interface{}
		err    bool
	}{
		{"number", &PS.NumberLiteral{Val: 42}, nil, int64(42), false},
		{"float", &PS.FloatLiteral{Val: 3.14}, nil, float64(3.14), false},
		{"string", &PS.StringLiteral{Val: "hello"}, nil, "hello", false},
		{"bool_true", &PS.BoolLiteral{Val: true}, nil, true, false},
		{"bool_false", &PS.BoolLiteral{Val: false}, nil, false, false},
		{"null", &PS.NullLiteral{}, nil, nil, false},
		{"ident", &PS.Ident{Name: "x"}, nil, "x", false},
		{"param", &PS.Param{Index: 0}, []interface{}{10}, 10, false},
		{"star", &PS.StarExpr{}, nil, "*", false},
		{"unary_minus", &PS.UnaryExpr{Op: int(LX.T_MINUS), Operand: &PS.NumberLiteral{Val: 5}}, nil, int64(-5), false},
		{"unary_plus", &PS.UnaryExpr{Op: int(LX.T_PLUS), Operand: &PS.NumberLiteral{Val: 5}}, nil, int64(5), false},
		{"binary_eq", &PS.BinaryExpr{Op: int(LX.T_EQ), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_ne", &PS.BinaryExpr{Op: int(LX.T_NE), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 2}}, nil, true, false},
		{"binary_lt", &PS.BinaryExpr{Op: int(LX.T_LT), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 2}}, nil, true, false},
		{"binary_le", &PS.BinaryExpr{Op: int(LX.T_LE), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_gt", &PS.BinaryExpr{Op: int(LX.T_GT), Left: &PS.NumberLiteral{Val: 2}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_ge", &PS.BinaryExpr{Op: int(LX.T_GE), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_plus", &PS.BinaryExpr{Op: int(LX.T_PLUS), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 2}}, nil, int64(3), false},
		{"binary_minus", &PS.BinaryExpr{Op: int(LX.T_MINUS), Left: &PS.NumberLiteral{Val: 5}, Right: &PS.NumberLiteral{Val: 3}}, nil, int64(2), false},
		{"binary_mul", &PS.BinaryExpr{Op: int(LX.T_STAR), Left: &PS.NumberLiteral{Val: 3}, Right: &PS.NumberLiteral{Val: 4}}, nil, int64(12), false},
		{"binary_div", &PS.BinaryExpr{Op: int(LX.T_SLASH), Left: &PS.NumberLiteral{Val: 10}, Right: &PS.NumberLiteral{Val: 2}}, nil, int64(5), false},
		{"binary_and", &PS.BinaryExpr{Op: int(LX.T_AND), Left: &PS.BoolLiteral{Val: true}, Right: &PS.BoolLiteral{Val: true}}, nil, true, false},
		{"binary_or", &PS.BinaryExpr{Op: int(LX.T_OR), Left: &PS.BoolLiteral{Val: false}, Right: &PS.BoolLiteral{Val: true}}, nil, true, false},
		{"case_simple", &PS.CaseExpr{
			WhenList: []PS.WhenClause{{Cond: &PS.BoolLiteral{Val: true}, Then: &PS.StringLiteral{Val: "yes"}}},
			Else:     &PS.StringLiteral{Val: "no"},
		}, nil, "yes", false},
		{"case_searched_int_truthy", &PS.CaseExpr{
			WhenList: []PS.WhenClause{{Cond: &PS.NumberLiteral{Val: 1}, Then: &PS.StringLiteral{Val: "yes"}}},
		}, nil, "yes", false},
		{"case_searched_int_falsy", &PS.CaseExpr{
			WhenList: []PS.WhenClause{{Cond: &PS.NumberLiteral{Val: 0}, Then: &PS.StringLiteral{Val: "yes"}}},
			Else:     &PS.StringLiteral{Val: "no"},
		}, nil, "no", false},
		{"case_simple_form", &PS.CaseExpr{
			Expr:     &PS.Ident{Name: "x"},
			WhenList: []PS.WhenClause{{Cond: &PS.NumberLiteral{Val: 1}, Then: &PS.StringLiteral{Val: "one"}}},
			Else:     &PS.StringLiteral{Val: "other"},
		}, nil, "other", false},
		{"unary_not_truthy_int", &PS.UnaryExpr{Op: int(LX.T_NOT), Operand: &PS.NumberLiteral{Val: 5}}, nil, false, false},
		{"unary_not_falsy_int", &PS.UnaryExpr{Op: int(LX.T_NOT), Operand: &PS.NumberLiteral{Val: 0}}, nil, true, false},
		{"unary_not_empty_string", &PS.UnaryExpr{Op: int(LX.T_NOT), Operand: &PS.StringLiteral{Val: ""}}, nil, true, false},
		{"between_in_range", &PS.BetweenExpr{
			Expr: &PS.NumberLiteral{Val: 5},
			Low:  &PS.NumberLiteral{Val: 1},
			High: &PS.NumberLiteral{Val: 10},
		}, nil, true, false},
		{"between_below", &PS.BetweenExpr{
			Expr: &PS.NumberLiteral{Val: 0},
			Low:  &PS.NumberLiteral{Val: 1},
			High: &PS.NumberLiteral{Val: 10},
		}, nil, false, false},
		{"between_above", &PS.BetweenExpr{
			Expr: &PS.NumberLiteral{Val: 11},
			Low:  &PS.NumberLiteral{Val: 1},
			High: &PS.NumberLiteral{Val: 10},
		}, nil, false, false},
		{"in_list_hit", &PS.InExpr{
			Expr: &PS.NumberLiteral{Val: 2},
			List: []PS.Expr{&PS.NumberLiteral{Val: 1}, &PS.NumberLiteral{Val: 2}, &PS.NumberLiteral{Val: 3}},
		}, nil, true, false},
		{"in_list_miss", &PS.InExpr{
			Expr: &PS.NumberLiteral{Val: 5},
			List: []PS.Expr{&PS.NumberLiteral{Val: 1}, &PS.NumberLiteral{Val: 2}, &PS.NumberLiteral{Val: 3}},
		}, nil, false, false},
		{"in_list_null", &PS.InExpr{
			Expr: &PS.NullLiteral{},
			List: []PS.Expr{&PS.NumberLiteral{Val: 1}, &PS.NumberLiteral{Val: 2}},
		}, nil, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Eval(tc.expr, nil, tc.params)
			if tc.err {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvalLike(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		s       string
		want    bool
	}{
		{"prefix_pct", "abc%", "abcdef", true},
		{"prefix_pct_miss", "abc%", "xabcdef", false},
		{"suffix_pct", "%abc", "xyzabc", true},
		{"contains_pct", "%abc%", "xabcy", true},
		{"underscore_single", "a_c", "abc", true},
		{"underscore_miss", "a_c", "ac", false},
		{"only_pct", "%", "anything", true},
		{"empty_pattern", "", "abc", false},
		{"pct_then_literal", "a%b", "axxxyb", true},
		{"pct_then_literal_miss", "a%b", "axxxyc", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matchLike(c.pattern, c.s)
			if got != c.want {
				t.Errorf("matchLike(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
			}
		})
	}
}

func TestEvalCrossTypeEq(t *testing.T) {
	cases := []struct {
		name string
		a, b interface{}
		want bool
	}{
		{"int_eq_int", int64(1), int64(1), true},
		{"int_eq_float", int64(1), float64(1), true},
		{"float_eq_int", float64(2.5), int64(2), false},
		{"string_eq_string", "abc", "abc", true},
		{"nil_eq_nil", nil, nil, true},
		{"nil_eq_int", nil, int64(0), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := equalValue(c.a, c.b)
			if got != c.want {
				t.Errorf("equalValue(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestOperators(t *testing.T) {
	scan := NewSeqScan("t")
	if scan == nil {
		t.Fatal("NewSeqScan returned nil")
	}

	filter := NewFilter(scan, nil)
	if filter == nil {
		t.Fatal("NewFilter returned nil")
	}

	project := NewProject(scan, nil)
	if project == nil {
		t.Fatal("NewProject returned nil")
	}

	sort := NewSort(scan, nil)
	if sort == nil {
		t.Fatal("NewSort returned nil")
	}

	limit := NewLimit(scan, 0)
	if limit == nil {
		t.Fatal("NewLimit returned nil")
	}
}

func TestSeqScanEmpty(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	scan := NewSeqScan("missing")
	_, err := scan.Next(context.Background())
	if err != ErrNoRows {
		t.Errorf("expected ErrNoRows for missing table, got %v", err)
	}
}

func TestFilterPassesThrough(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	RegisterTable("t", []Row{
		{Cols: []string{"x"}, Data: []interface{}{int64(1)}},
		{Cols: []string{"x"}, Data: []interface{}{int64(2)}},
	})
	scan := NewSeqScan("t")
	filter := NewFilter(scan, &PS.NumberLiteral{Val: 1})
	row, err := filter.Next(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.Data[0] != int64(1) {
		t.Errorf("expected 1, got %v", row.Data[0])
	}
}

func TestProjectStarPassesThrough(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	RegisterTable("t", []Row{
		{Cols: []string{"x"}, Data: []interface{}{int64(7)}},
	})
	scan := NewSeqScan("t")
	project := NewProject(scan, []PS.Expr{&PS.StarExpr{}})
	row, err := project.Next(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.Data[0] != int64(7) {
		t.Errorf("expected 7, got %v", row.Data[0])
	}
}

func TestSortThenIterate(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	RegisterTable("t", []Row{
		{Cols: []string{"x"}, Data: []interface{}{int64(3)}},
		{Cols: []string{"x"}, Data: []interface{}{int64(1)}},
		{Cols: []string{"x"}, Data: []interface{}{int64(2)}},
	})
	scan := NewSeqScan("t")
	s := NewSort(scan, []PS.OrderItem{{Expr: &PS.Ident{Name: "x"}, Desc: false}})
	want := []int64{1, 2, 3}
	for _, w := range want {
		row, err := s.Next(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if row.Data[0] != w {
			t.Errorf("expected %d, got %v", w, row.Data[0])
		}
	}
	if _, err := s.Next(context.Background()); err != ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestLimitStops(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	RegisterTable("t", []Row{
		{Cols: []string{"x"}, Data: []interface{}{int64(1)}},
		{Cols: []string{"x"}, Data: []interface{}{int64(2)}},
		{Cols: []string{"x"}, Data: []interface{}{int64(3)}},
	})
	scan := NewSeqScan("t")
	l := NewLimit(scan, 2)
	count := 0
	for {
		_, err := l.Next(context.Background())
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		count++
	}
	if count != 2 {
		t.Errorf("expected 2 rows, got %d", count)
	}
}

func TestInsertNotImplemented(t *testing.T) {
	insert := NewInsert("t", nil, nil)
	_, err := insert.Next(context.Background())
	if err != ErrNotImplemented {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
}

func TestUpdateNotImplemented(t *testing.T) {
	scan := NewSeqScan("t")
	update := NewUpdate("t", nil, nil, scan)
	_, err := update.Next(context.Background())
	if err != ErrNotImplemented {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
}

func TestDeleteNotImplemented(t *testing.T) {
	scan := NewSeqScan("t")
	delete := NewDelete("t", nil, scan)
	_, err := delete.Next(context.Background())
	if err != ErrNotImplemented {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
}
