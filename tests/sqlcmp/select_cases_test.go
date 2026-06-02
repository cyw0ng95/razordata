package sqlcmp

import "testing"

var selectCases = []testCase{
	{"simple_select", "SELECT * FROM t", false},
	{"select_columns", "SELECT a, b, c FROM t", false},
	{"select_where", "SELECT * FROM t WHERE a = 1", false},
	{"select_where_ne", "SELECT * FROM t WHERE a != 1", false},
	{"select_where_lt", "SELECT * FROM t WHERE a < 1", false},
	{"select_where_le", "SELECT * FROM t WHERE a <= 1", false},
	{"select_where_gt", "SELECT * FROM t WHERE a > 1", false},
	{"select_where_ge", "SELECT * FROM t WHERE a >= 1", false},
	{"select_order_by", "SELECT * FROM t ORDER BY a", false},
	{"select_order_by_desc", "SELECT * FROM t ORDER BY a DESC", false},
	{"select_limit", "SELECT * FROM t LIMIT 10", false},
	{"select_offset", "SELECT * FROM t OFFSET 5", false},
	{"select_limit_offset", "SELECT * FROM t LIMIT 10 OFFSET 5", false},
	{"select_where_and", "SELECT * FROM t WHERE a = 1 AND b = 2", false},
	{"select_where_or", "SELECT * FROM t WHERE a = 1 OR b = 2", false},
	{"select_where_and_or", "SELECT * FROM t WHERE a = 1 AND b = 2 OR c = 3", false},
	{"select_where_not", "SELECT * FROM t WHERE NOT a = 1", false},
	{"select_where_in", "SELECT * FROM t WHERE a IN (1, 2, 3)", false},
	{"select_where_between", "SELECT * FROM t WHERE a BETWEEN 1 AND 10", false},
	{"select_where_like", "SELECT * FROM t WHERE a LIKE '%foo%'", false},
	{"select_where_is_null", "SELECT * FROM t WHERE a IS NULL", false},
	{"select_where_is_not_null", "SELECT * FROM t WHERE a IS NOT NULL", false},
	{"select_expr_plus", "SELECT a + b FROM t", false},
	{"select_expr_minus", "SELECT a - b FROM t", false},
	{"select_expr_star", "SELECT a * b FROM t", false},
	{"select_expr_slash", "SELECT a / b FROM t", false},
	{"select_expr_precedence", "SELECT a + b * c FROM t", false},
	{"select_expr_precedence2", "SELECT a * b + c FROM t", false},
	{"select_expr_parens", "SELECT (a + b) * c FROM t", false},
	{"select_expr_unary_minus", "SELECT -a FROM t", false},
	{"select_expr_unary_plus", "SELECT +a FROM t", false},
	{"select_int", "SELECT 42 FROM t", false},
	{"select_float", "SELECT 3.14 FROM t", false},
	{"select_string", "SELECT 'hello world' FROM t", false},
	{"select_param", "SELECT ? FROM t", false},
	{"select_star_where", "SELECT * FROM t WHERE a = 1", false},
	{"select_all_cols", "SELECT * FROM t WHERE a = 1 AND b = 2 ORDER BY c LIMIT 10 OFFSET 5", false},
	{"select_nested_parens", "SELECT * FROM t WHERE (((a = 1)))", false},
	{"select_multi_where_and", "SELECT * FROM t WHERE a = 1 AND b = 2 AND c = 3", false},
	{"select_multi_where_or", "SELECT * FROM t WHERE a = 1 OR b = 2 OR c = 3", false},
	{"select_complex_expr", "SELECT a + b * c - d / e FROM t", false},
	{"select_negated_expr", "SELECT * FROM t WHERE NOT (a = 1 AND b = 2)", false},
	{"select_order_by_multiple", "SELECT * FROM t ORDER BY a, b, c", false},
	{"select_order_by_asc_desc", "SELECT * FROM t ORDER BY a ASC, b DESC", false},
	{"select_limit_offset_both", "SELECT * FROM t WHERE a > 0 ORDER BY b LIMIT 100 OFFSET 10", false},
}

func TestSelectRewrite(t *testing.T) {
	for _, tc := range selectCases {
		t.Run(tc.name, tc.runRewrite)
	}
}

func TestSelectParse(t *testing.T) {
	for _, tc := range selectCases {
		t.Run(tc.name, tc.runParse)
	}
}
