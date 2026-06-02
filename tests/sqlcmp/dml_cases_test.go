package sqlcmp

import "testing"

var dmlCases = []testCase{
	{"insert_values", "INSERT INTO t VALUES (1, 'a')", false},
	{"insert_multirow", "INSERT INTO t VALUES (1, 'a'), (2, 'b')", false},
	{"insert_with_cols", "INSERT INTO t (a, b) VALUES (1, 'a')", false},
	{"insert_with_cols_multirow", "INSERT INTO t (a, b) VALUES (1, 'a'), (2, 'b')", false},
	{"insert_with_expr", "INSERT INTO t (a, b) VALUES (1 + 1, 'x')", false},
	{"insert_null", "INSERT INTO t VALUES (NULL, NULL)", false},
	{"insert_string_special", "INSERT INTO t VALUES (1, 'a''b\"c')", false},
	{"insert_string_newline", "INSERT INTO t VALUES (1, 'line1\nline2')", false},
	{"insert_multiple_cols", "INSERT INTO t (a, b, c, d) VALUES (1, 2, 3, 4)", false},
	{"insert_all_types", "INSERT INTO t VALUES (1, 'text', 3.14, NULL)", false},
	{"update_set", "UPDATE t SET a = 1", false},
	{"update_set_multi", "UPDATE t SET a = 1, b = 2", false},
	{"update_where", "UPDATE t SET a = 1 WHERE b = 2", false},
	{"update_where_and", "UPDATE t SET a = 1 WHERE b = 2 AND c = 3", false},
	{"update_expr", "UPDATE t SET a = b + 1", false},
	{"update_where_expr", "UPDATE t SET a = 1 WHERE b = c + 2", false},
	{"update_where_or", "UPDATE t SET a = 1 WHERE b = 2 OR c = 3", false},
	{"update_where_in", "UPDATE t SET a = 1 WHERE b IN (1, 2, 3)", false},
	{"update_null", "UPDATE t SET a = NULL WHERE b = 1", false},
	{"update_string", "UPDATE t SET a = 'new_value' WHERE b = 1", false},
	{"update_expr_calc", "UPDATE t SET a = b * 2 + c / 3 - 1", false},
	{"update_where_between", "UPDATE t SET a = 1 WHERE b BETWEEN 10 AND 20", false},
	{"update_where_like", "UPDATE t SET a = 1 WHERE b LIKE '%test%'", false},
	{"delete_all", "DELETE FROM t", false},
	{"delete_where", "DELETE FROM t WHERE a = 1", false},
	{"delete_where_and", "DELETE FROM t WHERE a = 1 AND b = 2", false},
	{"delete_where_or", "DELETE FROM t WHERE a = 1 OR b = 2", false},
	{"delete_where_in", "DELETE FROM t WHERE a IN (1, 2, 3)", false},
	{"delete_where_between", "DELETE FROM t WHERE a BETWEEN 1 AND 10", false},
	{"delete_where_null", "DELETE FROM t WHERE a IS NULL", false},
	{"delete_where_like", "DELETE FROM t WHERE a LIKE '%pattern%'", false},
}

func TestDMLRewrite(t *testing.T) {
	for _, tc := range dmlCases {
		t.Run(tc.name, tc.runRewrite)
	}
}

func TestDMLParse(t *testing.T) {
	for _, tc := range dmlCases {
		t.Run(tc.name, tc.runParse)
	}
}
