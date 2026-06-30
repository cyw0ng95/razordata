package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestEval(t *testing.T) {
	cases := []struct {
		name   string
		expr   PS.Expr
		params []any
		want   any
		err    bool
	}{
		{"number", &PS.NumberLiteral{Val: 42}, nil, int64(42), false},
		{"float", &PS.FloatLiteral{Val: 3.14}, nil, float64(3.14), false},
		{"string", &PS.StringLiteral{Val: "hello"}, nil, "hello", false},
		{"bool_true", &PS.BoolLiteral{Val: true}, nil, true, false},
		{"bool_false", &PS.BoolLiteral{Val: false}, nil, false, false},
		{"null", &PS.NullLiteral{}, nil, nil, false},
		{"ident", &PS.Ident{Name: "x"}, nil, "x", false},
		{"param", &PS.Param{Index: 0}, []any{10}, int64(10), false},
		{"star", &PS.StarExpr{}, nil, "*", false},
		{"unary_minus", &PS.UnaryExpr{Op: LX.T_MINUS, Operand: &PS.NumberLiteral{Val: 5}}, nil, int64(-5), false},
		{"unary_plus", &PS.UnaryExpr{Op: LX.T_PLUS, Operand: &PS.NumberLiteral{Val: 5}}, nil, int64(5), false},
		{"binary_eq", &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_ne", &PS.BinaryExpr{Op: LX.T_NE, Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 2}}, nil, true, false},
		// REQ000815: NULL != NULL should return UNKNOWN (nil), not TRUE.
		{"null_ne_null", &PS.BinaryExpr{Op: LX.T_NE, Left: &PS.NullLiteral{}, Right: &PS.NullLiteral{}}, nil, nil, false},
		{"null_ne_literal", &PS.BinaryExpr{Op: LX.T_NE, Left: &PS.NullLiteral{}, Right: &PS.NumberLiteral{Val: 10}}, nil, nil, false},
		{"literal_ne_null", &PS.BinaryExpr{Op: LX.T_NE, Left: &PS.NumberLiteral{Val: 10}, Right: &PS.NullLiteral{}}, nil, nil, false},
		{"binary_lt", &PS.BinaryExpr{Op: LX.T_LT, Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 2}}, nil, true, false},
		{"binary_le", &PS.BinaryExpr{Op: LX.T_LE, Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_gt", &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.NumberLiteral{Val: 2}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_ge", &PS.BinaryExpr{Op: LX.T_GE, Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_plus", &PS.BinaryExpr{Op: LX.T_PLUS, Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 2}}, nil, int64(3), false},
		{"binary_minus", &PS.BinaryExpr{Op: LX.T_MINUS, Left: &PS.NumberLiteral{Val: 5}, Right: &PS.NumberLiteral{Val: 3}}, nil, int64(2), false},
		{"binary_mul", &PS.BinaryExpr{Op: LX.T_STAR, Left: &PS.NumberLiteral{Val: 3}, Right: &PS.NumberLiteral{Val: 4}}, nil, int64(12), false},
		{"binary_div", &PS.BinaryExpr{Op: LX.T_SLASH, Left: &PS.NumberLiteral{Val: 10}, Right: &PS.NumberLiteral{Val: 2}}, nil, int64(5), false},
		{"binary_and", &PS.BinaryExpr{Op: LX.T_AND, Left: &PS.BoolLiteral{Val: true}, Right: &PS.BoolLiteral{Val: true}}, nil, true, false},
		{"binary_or", &PS.BinaryExpr{Op: LX.T_OR, Left: &PS.BoolLiteral{Val: false}, Right: &PS.BoolLiteral{Val: true}}, nil, true, false},
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
		{"unary_not_truthy_int", &PS.UnaryExpr{Op: LX.T_NOT, Operand: &PS.NumberLiteral{Val: 5}}, nil, false, false},
		{"unary_not_falsy_int", &PS.UnaryExpr{Op: LX.T_NOT, Operand: &PS.NumberLiteral{Val: 0}}, nil, true, false},
		{"unary_not_empty_string", &PS.UnaryExpr{Op: LX.T_NOT, Operand: &PS.StringLiteral{Val: ""}}, nil, true, false},
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
		}, nil, nil, false},
		{"is_null_true", &PS.BinaryExpr{Op: LX.T_IS, Left: &PS.NullLiteral{}, Right: &PS.NullLiteral{}}, nil, true, false},
		{"is_not_null_false", &PS.BinaryExpr{Op: LX.T_IS, Left: &PS.NullLiteral{}, Right: &PS.UnaryExpr{Op: LX.T_NOT, Operand: &PS.NullLiteral{}}}, nil, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EV.EvalValue(tc.expr, nil, tc.params)
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
			if got.ToAny() != tc.want {
				t.Errorf("got %v, want %v", got.ToAny(), tc.want)
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
			got := EV.MatchLike(c.pattern, c.s, "")
			if got != c.want {
				t.Errorf("MatchLike(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
			}
		})
	}
}

func TestEvalLikeEscape(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		s       string
		escape  string
		want    bool
	}{
		{"escape_pct", "100%", "100%", `\`, true},
		{"escape_underscore", "100_", "100_", `\`, true},
		{"escape_literal_pct", "100%%", "100% complete", `\`, true},
		{"pct_after_escape", "100%x", "100%x", `\`, true},
		{"no_escape_empty", "100%", "100%", "", true},
		{"escape_char_in_escape", `100\\%`, `100\`, `\`, true},
		{"escaped_pct_not_wildcard", `100\%`, "100%", `\`, true},
		{"escaped_underscore_not_wildcard", `100\_`, "100_", `\`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EV.MatchLike(c.pattern, c.s, c.escape)
			if got != c.want {
				t.Errorf("MatchLike(%q, %q, %q) = %v, want %v", c.pattern, c.s, c.escape, got, c.want)
			}
		})
	}
}

func TestEvalNullArithmetic(t *testing.T) {
	cases := []struct {
		name string
		expr PS.Expr
		want any
	}{
		{"null_plus_int", &PS.BinaryExpr{Op: LX.T_PLUS, Left: &PS.NullLiteral{}, Right: &PS.NumberLiteral{Val: 5}}, nil},
		{"int_plus_null", &PS.BinaryExpr{Op: LX.T_PLUS, Left: &PS.NumberLiteral{Val: 5}, Right: &PS.NullLiteral{}}, nil},
		{"null_times_int", &PS.BinaryExpr{Op: LX.T_STAR, Left: &PS.NullLiteral{}, Right: &PS.NumberLiteral{Val: 5}}, nil},
		{"null_div_int", &PS.BinaryExpr{Op: LX.T_SLASH, Left: &PS.NullLiteral{}, Right: &PS.NumberLiteral{Val: 5}}, nil},
		{"int_div_by_zero", &PS.BinaryExpr{Op: LX.T_SLASH, Left: &PS.NumberLiteral{Val: 10}, Right: &PS.NumberLiteral{Val: 0}}, nil},
		{"float_div_by_zero", &PS.BinaryExpr{Op: LX.T_SLASH, Left: &PS.FloatLiteral{Val: 10}, Right: &PS.FloatLiteral{Val: 0}}, nil},
		{"int_plus_float_promotes", &PS.BinaryExpr{Op: LX.T_PLUS, Left: &PS.NumberLiteral{Val: 1}, Right: &PS.FloatLiteral{Val: 2.5}}, float64(3.5)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := EV.EvalValue(c.expr, nil, nil)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got.ToAny() != c.want {
				t.Errorf("got %v, want %v", got.ToAny(), c.want)
			}
		})
	}
}

func TestEvalCast(t *testing.T) {
	cases := []struct {
		name string
		expr PS.Expr
		want any
		err  bool
	}{
		{"int_from_int", &PS.CastExpr{Expr: &PS.NumberLiteral{Val: 42}, Type: &PS.TypeInfo{Type: LX.T_INT_KW}}, int64(42), false},
		{"int_from_float", &PS.CastExpr{Expr: &PS.FloatLiteral{Val: 3.7}, Type: &PS.TypeInfo{Type: LX.T_INT_KW}}, int64(3), false},
		{"int_from_string", &PS.CastExpr{Expr: &PS.StringLiteral{Val: "123"}, Type: &PS.TypeInfo{Type: LX.T_INT_KW}}, int64(123), false},
		{"int_from_string_bad", &PS.CastExpr{Expr: &PS.StringLiteral{Val: "abc"}, Type: &PS.TypeInfo{Type: LX.T_INT_KW}}, nil, true},
		{"float_from_int", &PS.CastExpr{Expr: &PS.NumberLiteral{Val: 5}, Type: &PS.TypeInfo{Type: LX.T_FLOAT_KW}}, float64(5), false},
		{"text_from_int", &PS.CastExpr{Expr: &PS.NumberLiteral{Val: 5}, Type: &PS.TypeInfo{Type: LX.T_TEXT}}, "5", false},
		{"text_from_float", &PS.CastExpr{Expr: &PS.FloatLiteral{Val: 1.5}, Type: &PS.TypeInfo{Type: LX.T_TEXT}}, "1.5", false},
		{"bool_from_int", &PS.CastExpr{Expr: &PS.NumberLiteral{Val: 1}, Type: &PS.TypeInfo{Type: LX.T_BOOL}}, true, false},
		{"bool_from_zero", &PS.CastExpr{Expr: &PS.NumberLiteral{Val: 0}, Type: &PS.TypeInfo{Type: LX.T_BOOL}}, false, false},
		{"null_to_int", &PS.CastExpr{Expr: &PS.NullLiteral{}, Type: &PS.TypeInfo{Type: LX.T_INT_KW}}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := EV.EvalValue(c.expr, nil, nil)
			if c.err {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got.ToAny() != c.want {
				t.Errorf("got %v, want %v", got.ToAny(), c.want)
			}
		})
	}
}

func TestEvalCrossTypeEq(t *testing.T) {
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"int_eq_int", int64(1), int64(1), true},
		{"int_eq_float", int64(1), float64(1), true},
		{"float_eq_int", float64(2.5), int64(2), false},
		{"string_eq_string", "abc", "abc", true},
		{"nil_eq_nil", nil, nil, false},
		{"nil_eq_int", nil, int64(0), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DT.EqualValueAny(c.a, c.b)
			if got != c.want {
				t.Errorf("DT.EqualValueAny(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestOperators(t *testing.T) {
	scan := OP.NewSeqScan("t")
	if scan == nil {
		t.Fatal("NewSeqScan returned nil")
	}

	filter := OP.NewFilter(scan, nil)
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
	scan := OP.NewSeqScan("missing")
	_, err := scan.Next(context.Background())
	if err != DT.ErrNoRows {
		t.Errorf("expected ErrNoRows for missing table, got %v", err)
	}
}

func TestFilterPassesThrough(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	DT.RegisterTable("t", []Row{
		{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(1))}},
		{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(2))}},
	})
	scan := OP.NewSeqScan("t")
	filter := OP.NewFilter(scan, &PS.NumberLiteral{Val: 1})
	row, err := filter.Next(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !row.Data[0].Equal(NewIntValue(int64(1))) {
		t.Errorf("expected 1, got %v", row.Data[0])
	}
}

func TestProjectStarPassesThrough(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	DT.RegisterTable("t", []Row{
		{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(7))}},
	})
	scan := OP.NewSeqScan("t")
	project := NewProject(scan, []PS.Expr{&PS.StarExpr{}})
	row, err := project.Next(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !row.Data[0].Equal(NewIntValue(int64(7))) {
		t.Errorf("expected 7, got %v", row.Data[0])
	}
}

func TestSortThenIterate(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	DT.RegisterTable("t", []Row{
		{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(3))}},
		{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(1))}},
		{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(2))}},
	})
	scan := OP.NewSeqScan("t")
	s := NewSort(scan, []PS.OrderItem{{Expr: &PS.Ident{Name: "x"}, Desc: false}})
	want := []int64{1, 2, 3}
	for _, w := range want {
		row, err := s.Next(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !row.Data[0].Equal(NewIntValue(int64(w))) {
			t.Errorf("expected %d, got %v", w, row.Data[0])
		}
	}
	if _, err := s.Next(context.Background()); err != DT.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestLimitStops(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	DT.RegisterTable("t", []Row{
		{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(1))}},
		{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(2))}},
		{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(3))}},
	})
	scan := OP.NewSeqScan("t")
	l := NewLimit(scan, 2)
	count := 0
	for {
		_, err := l.Next(context.Background())
		if err == DT.ErrNoRows {
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

func TestInsertAppendsRows(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	DT.RegisterTable("t", []Row{{Cols: []string{"a"}, Data: []Value{NewIntValue(int64(0))}}})
	insert := NewInsert("t", nil, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}},
		{&PS.NumberLiteral{Val: 2}},
	}, nil, nil)
	_, err := insert.Next(context.Background())
	if err != DT.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
	if insert.RowsAffected() != 2 {
		t.Errorf("expected 2 rows affected, got %d", insert.RowsAffected())
	}
	DT.TablesMu.RLock()
	defer DT.TablesMu.RUnlock()
	if len(DT.Tables["t"]) != 3 {
		t.Errorf("expected 3 rows in table (1 seed + 2 inserts), got %d", len(DT.Tables["t"]))
	}
}

func TestUpdateModifiesRows(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	DT.RegisterTable("t", []Row{
		{Cols: []string{"a", "b"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("x")}},
		{Cols: []string{"a", "b"}, Data: []Value{NewIntValue(int64(2)), NewTextValue("y")}},
	})
	scan := OP.NewSeqScan("t")
	update := NewUpdate("t", []PS.Pair{{Col: "b", Val: &PS.StringLiteral{Val: "z"}}}, nil, scan, nil)
	_, err := update.Next(context.Background())
	if err != DT.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
	if update.RowsAffected() != 2 {
		t.Errorf("expected 2 rows affected, got %d", update.RowsAffected())
	}
	DT.TablesMu.RLock()
	defer DT.TablesMu.RUnlock()
	for _, r := range DT.Tables["t"] {
		if !r.Data[1].Equal(NewTextValue("z")) {
			t.Errorf("expected b='z', got %v", r.Data[1])
		}
	}
}

func TestDeleteRemovesMatching(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	DT.RegisterTable("t", []Row{
		{Cols: []string{"a"}, Data: []Value{NewIntValue(int64(1))}},
		{Cols: []string{"a"}, Data: []Value{NewIntValue(int64(2))}},
		{Cols: []string{"a"}, Data: []Value{NewIntValue(int64(3))}},
	})
	scan := OP.NewSeqScan("t")
	filter := OP.NewFilter(scan, &PS.BinaryExpr{
		Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1},
	})
	del := NewDelete("t", nil, filter, nil)
	_, err := del.Next(context.Background())
	if err != DT.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
	if del.RowsAffected() != 2 {
		t.Errorf("expected 2 rows affected, got %d", del.RowsAffected())
	}
	DT.TablesMu.RLock()
	defer DT.TablesMu.RUnlock()
	if len(DT.Tables["t"]) != 1 {
		t.Fatalf("expected 1 row, got %d", len(DT.Tables["t"]))
	}
	if !DT.Tables["t"][0].Data[0].Equal(NewIntValue(int64(1))) {
		t.Errorf("expected row 1 to remain, got %v", DT.Tables["t"][0].Data[0])
	}
}

func TestUpdate_BackToBackSameTable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		_, err := ex.Exec(ctx, "INSERT INTO t VALUES (?)", i)
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	res, err := ex.Exec(ctx, "UPDATE t SET x = x + 1")
	if err != nil {
		t.Fatalf("first update: %v", err)
	}
	if res.RowsAffected != 5 {
		t.Fatalf("first update: expected 5 rows, got %d", res.RowsAffected)
	}

	res, err = ex.Exec(ctx, "UPDATE t SET x = x + 1")
	if err != nil {
		t.Fatalf("second update: %v", err)
	}
	if res.RowsAffected != 5 {
		t.Fatalf("second update: expected 5 rows, got %d", res.RowsAffected)
	}

	rows, err := ex.QueryAll(ctx, "SELECT x FROM t ORDER BY x")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}
	for i, r := range rows {
		want := int64(i + 3)
		got, ok := r.Data[0].ToAny().(int64)
		if !ok {
			t.Fatalf("row %d: expected int64, got %T", i, r.Data[0])
		}
		if got != want {
			t.Errorf("row %d: got %d, want %d", i, got, want)
		}
	}
}

func TestUpdate_BackToBackMatchingRows(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x", "y"})
	ctx := context.Background()

	for _, v := range []string{
		"INSERT INTO t VALUES (1, 1)",
		"INSERT INTO t VALUES (1, 2)",
		"INSERT INTO t VALUES (2, 1)",
		"INSERT INTO t VALUES (2, 2)",
	} {
		if _, err := ex.Exec(ctx, v); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	res, err := ex.Exec(ctx, "UPDATE t SET x = 10 WHERE y = 1")
	if err != nil {
		t.Fatalf("first update: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("first update: expected 2 rows, got %d", res.RowsAffected)
	}

	res, err = ex.Exec(ctx, "UPDATE t SET y = 10 WHERE y = 2")
	if err != nil {
		t.Fatalf("second update: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("second update: expected 2 rows, got %d", res.RowsAffected)
	}
}

func TestCreateAndDropTable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{Name: "new", Cols: []PS.ColDef{{Name: "a", Type: LX.T_INT_KW}}})
	_, err := ct.Next(context.Background())
	if err != DT.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
	DT.TablesMu.RLock()
	if _, ok := DT.Tables["new"]; !ok {
		t.Error("expected table to be created")
	}
	DT.TablesMu.RUnlock()
	dt := NewDropTable(&PS.DropTable{Name: "new"})
	_, err = dt.Next(context.Background())
	if err != DT.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
	DT.TablesMu.RLock()
	if _, ok := DT.Tables["new"]; ok {
		t.Error("expected table to be dropped")
	}
	DT.TablesMu.RUnlock()
}

func TestGlob_BinaryOp(t *testing.T) {
	cases := []struct {
		pattern string
		s       string
		want    bool
	}{
		{"h*", "hello", true},
		{"h*", "world", false},
		{"*.txt", "file.txt", true},
		{"*.txt", "file.go", false},
		{"h?llo", "hello", true},
		{"h?llo", "hllo", false},
	}
	for _, tc := range cases {
		got, err := EV.GlobValue(NewTextValue(tc.pattern), NewTextValue(tc.s))
		if err != nil {
			t.Fatalf("glob(%q, %q): %v", tc.pattern, tc.s, err)
		}
		if got.Kind != KindBool {
			t.Fatalf("glob(%q, %q): got Kind %v", tc.pattern, tc.s, got.Kind)
		}
		if got.Bo != tc.want {
			t.Errorf("glob(%q, %q) = %v, want %v", tc.pattern, tc.s, got.Bo, tc.want)
		}
	}
}

// TestFilter_NullNotEqual verifies REQ000815: WHERE NULL <> NULL
// returns 0 rows (UNKNOWN filters out the row).
func TestFilter_NullNotEqual(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v"})

	ctx := context.Background()
	// Insert a row with NULL value.
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, NULL)"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Query with NULL != NULL predicate — should return 0 rows.
	rows, err := ex.QueryAll(ctx, "SELECT v FROM t WHERE v != v")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for WHERE v != v (NULL <> NULL), got %d", len(rows))
	}

	// Also test NULL != literal — should also return 0 rows.
	rows2, err := ex.QueryAll(ctx, "SELECT v FROM t WHERE v != 10")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(rows2) != 0 {
		t.Errorf("expected 0 rows for WHERE v != 10 (NULL <> 10), got %d", len(rows2))
	}
}
