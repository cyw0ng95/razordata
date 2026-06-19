package dual

// probeCasesV2 targets SQL features with higher bug density:
// subqueries, multi-table, NULL three-valued logic, aggregations,
// UPDATE/DELETE, views, CTEs, and edge-case expressions.
var probeCasesV2 = []dualCase{
	// --- Subqueries ---
	{
		Name: "scalar_subquery_in_select",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT (SELECT MAX(v) FROM t)",
		Want:  [][]any{{int64(30)}},
	},
	{
		Name: "scalar_subquery_in_where",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT v FROM t WHERE v > (SELECT AVG(v) FROM t) ORDER BY v",
		Want:  [][]any{{int64(20)}, {int64(30)}},
	},
	{
		Name: "correlated_subquery_exists",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2), (3, 1), (4, 2)",
			"CREATE TABLE s (id INTEGER PRIMARY KEY, g INTEGER)",
			"INSERT INTO s VALUES (1, 1)",
		},
		Query: "SELECT t.id FROM t WHERE EXISTS (SELECT 1 FROM s WHERE s.g = t.g) ORDER BY t.id",
		Want:  [][]any{{int64(1)}, {int64(3)}},
	},
	{
		Name: "correlated_subquery_not_exists",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2), (3, 1), (4, 2)",
			"CREATE TABLE s (id INTEGER PRIMARY KEY, g INTEGER)",
			"INSERT INTO s VALUES (1, 1)",
		},
		Query: "SELECT t.id FROM t WHERE NOT EXISTS (SELECT 1 FROM s WHERE s.g = t.g) ORDER BY t.id",
		Want:  [][]any{{int64(2)}, {int64(4)}},
	},
	{
		Name: "in_subquery_with_join",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT)",
			"INSERT INTO t VALUES (1, 'a'), (2, 'b'), (3, 'a')",
			"CREATE TABLE s (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO s VALUES (1, 10), (2, 20)",
		},
		Query: "SELECT v FROM s WHERE id IN (SELECT id FROM t WHERE g = 'a') ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(20)}},
	},

	// --- NULL three-valued logic ---
	{
		Name: "null_and_true",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, 10), (3, 20)",
		},
		Query: "SELECT v FROM t WHERE v > 5 AND v < 25 ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(20)}},
	},
	{
		Name: "null_or_false",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, 10), (3, 20)",
		},
		Query: "SELECT v FROM t WHERE v = 10 OR v = 20 ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(20)}},
	},
	{
		Name: "null_not_equal",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, 10)",
		},
		Query: "SELECT v FROM t WHERE v != 10 ORDER BY id",
		Want:  [][]any{{nil}},
	},
	{
		Name: "null_between",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, 5), (3, 15), (4, 25)",
		},
		Query: "SELECT v FROM t WHERE v BETWEEN 10 AND 20 ORDER BY v",
		Want:  [][]any{{int64(15)}},
	},
	{
		Name: "null_in_list",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, 10), (3, 20)",
		},
		Query: "SELECT v FROM t WHERE v IN (10, 20) ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(20)}},
	},
	{
		Name: "null_not_in_list",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, 10), (3, 20)",
		},
		Query: "SELECT v FROM t WHERE v NOT IN (10, 20) ORDER BY id",
		Want:  [][]any{{nil}},
	},
	{
		Name: "null_like",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, NULL), (2, 'hello')",
		},
		Query: "SELECT s FROM t WHERE s LIKE 'hel%' ORDER BY s",
		Want:  [][]any{{"hello"}},
	},
	{
		Name: "null_is_null_comparison",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, 10)",
		},
		Query: "SELECT v IS NULL FROM t ORDER BY id",
		Want:  [][]any{{int64(1)}, {int64(0)}},
	},

	// --- Aggregations ---
	{
		Name: "count_distinct",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 10), (3, 20), (4, 20), (5, 30)",
		},
		Query: "SELECT COUNT(DISTINCT v) FROM t",
		Want:  [][]any{{int64(3)}},
	},
	{
		Name: "sum_with_nulls",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, NULL), (3, 20)",
		},
		Query: "SELECT SUM(v) FROM t",
		Want:  [][]any{{int64(30)}},
	},
	{
		Name: "avg_with_nulls",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, NULL), (3, 20)",
		},
		Query: "SELECT AVG(v) FROM t",
		Want:  [][]any{{float64(15)}},
	},
	{
		Name: "min_max_with_nulls",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, NULL), (3, 20)",
		},
		Query: "SELECT MIN(v), MAX(v) FROM t",
		Want:  [][]any{{int64(10), int64(20)}},
	},
	{
		Name: "group_concat_separator",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT, v TEXT)",
			"INSERT INTO t VALUES (1, 'a', 'x'), (2, 'a', 'y'), (3, 'b', 'z')",
		},
		Query: "SELECT g, GROUP_CONCAT(v, ':') FROM t GROUP BY g ORDER BY g",
		Want:  [][]any{{"a", "x:y"}, {"b", "z"}},
	},
	{
		Name: "multiple_aggregates",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT COUNT(*), SUM(v), MIN(v), MAX(v), AVG(v) FROM t",
		Want:  [][]any{{int64(3), int64(60), int64(10), int64(30), float64(20)}},
	},
	{
		Name: "count_with_where",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT COUNT(*) FROM t WHERE v > 15",
		Want:  [][]any{{int64(2)}},
	},

	// --- GROUP BY edge cases ---
	{
		Name: "groupby_multiple_columns",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t VALUES (1, 1, 10), (2, 1, 20), (3, 2, 10), (4, 2, 20)",
		},
		Query: "SELECT a, b, COUNT(*) FROM t GROUP BY a, b ORDER BY a, b",
		Want: [][]any{
			{int64(1), int64(10), int64(1)},
			{int64(1), int64(20), int64(1)},
			{int64(2), int64(10), int64(1)},
			{int64(2), int64(20), int64(1)},
		},
	},
	{
		Name: "groupby_having_count",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT)",
			"INSERT INTO t VALUES (1, 'a'), (2, 'a'), (3, 'b'), (4, 'b'), (5, 'b')",
		},
		Query: "SELECT g, COUNT(*) AS cnt FROM t GROUP BY g HAVING cnt >= 2 ORDER BY cnt DESC",
		Want: [][]any{
			{"b", int64(3)},
			{"a", int64(2)},
		},
	},
	{
		Name: "groupby_having_sum",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT, v INTEGER)",
			"INSERT INTO t VALUES (1, 'a', 10), (2, 'a', 20), (3, 'b', 5)",
		},
		Query: "SELECT g, SUM(v) FROM t GROUP BY g HAVING SUM(v) > 15 ORDER BY g",
		Want:  [][]any{{"a", int64(30)}},
	},

	// --- ORDER BY edge cases ---
	{
		Name: "orderby_multiple_columns",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t VALUES (1, 2, 1), (2, 1, 2), (3, 1, 1)",
		},
		Query: "SELECT a, b FROM t ORDER BY a ASC, b DESC",
		Want: [][]any{
			{int64(1), int64(2)},
			{int64(1), int64(1)},
			{int64(2), int64(1)},
		},
	},
	{
		Name: "orderby_nulls_first",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, NULL), (3, 20)",
		},
		Query: "SELECT v FROM t ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(20)}, {nil}},
	},
	{
		Name: "orderby_expression",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t VALUES (1, 1, 10), (2, 2, 5), (3, 3, 1)",
		},
		Query: "SELECT a, b FROM t ORDER BY a + b DESC",
		Want: [][]any{
			{int64(1), int64(10)},
			{int64(2), int64(5)},
			{int64(3), int64(1)},
		},
	},

	// --- LIMIT/OFFSET ---
	{
		Name: "limit_offset",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30), (4, 40), (5, 50)",
		},
		Query: "SELECT v FROM t ORDER BY v LIMIT 2 OFFSET 2",
		Want:  [][]any{{int64(30)}, {int64(40)}},
	},
	{
		Name: "limit_zero",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10)",
		},
		Query: "SELECT v FROM t LIMIT 0",
		Want:  [][]any{},
	},
	{
		Name: "limit_larger_than_rows",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20)",
		},
		Query: "SELECT v FROM t LIMIT 100",
		Want:  [][]any{{int64(10)}, {int64(20)}},
	},

	// --- UPDATE/DELETE ---
	{
		Name: "update_basic",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20)",
			"UPDATE t SET v = 99 WHERE id = 1",
		},
		Query: "SELECT v FROM t WHERE id = 1",
		Want:  [][]any{{int64(99)}},
	},
	{
		Name: "update_all_rows",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20)",
			"UPDATE t SET v = v * 2",
		},
		Query: "SELECT v FROM t ORDER BY id",
		Want:  [][]any{{int64(20)}, {int64(40)}},
	},
	{
		Name: "update_with_expression",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t VALUES (1, 10, 5)",
			"UPDATE t SET b = a + b WHERE id = 1",
		},
		Query: "SELECT b FROM t WHERE id = 1",
		Want:  [][]any{{int64(15)}},
	},
	{
		Name: "delete_basic",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
			"DELETE FROM t WHERE id = 2",
		},
		Query: "SELECT v FROM t ORDER BY id",
		Want:  [][]any{{int64(10)}, {int64(30)}},
	},
	{
		Name: "delete_all_rows",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20)",
			"DELETE FROM t",
		},
		Query: "SELECT COUNT(*) FROM t",
		Want:  [][]any{{int64(0)}},
	},
	{
		Name: "delete_with_where_in",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30), (4, 40)",
			"DELETE FROM t WHERE v IN (20, 40)",
		},
		Query: "SELECT v FROM t ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(30)}},
	},

	// --- INSERT OR REPLACE / INSERT OR IGNORE ---
	{
		Name: "insert_or_replace",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10)",
			"INSERT OR REPLACE INTO t VALUES (1, 99)",
		},
		Query: "SELECT v FROM t WHERE id = 1",
		Want:  [][]any{{int64(99)}},
	},
	{
		Name: "insert_or_ignore",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10)",
			"INSERT OR IGNORE INTO t VALUES (1, 99)",
		},
		Query: "SELECT v FROM t WHERE id = 1",
		Want:  [][]any{{int64(10)}},
	},

	// --- Views ---
	{
		Name: "create_view_select",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20)",
			"CREATE VIEW v AS SELECT id, v * 2 AS doubled FROM t",
		},
		Query: "SELECT doubled FROM v ORDER BY id",
		Want:  [][]any{{int64(20)}, {int64(40)}},
	},
	{
		Name: "create_view_with_where",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
			"CREATE VIEW big AS SELECT * FROM t WHERE v > 15",
		},
		Query: "SELECT v FROM big ORDER BY v",
		Want:  [][]any{{int64(20)}, {int64(30)}},
	},
	{
		Name: "drop_view",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
			"CREATE VIEW v AS SELECT * FROM t",
			"DROP VIEW v",
		},
		Query: "SELECT id FROM t",
		Want:  [][]any{{int64(1)}},
	},

	// --- CTE (WITH clause) ---
	{
		Name: "cte_basic",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "WITH cte AS (SELECT v FROM t WHERE v > 15) SELECT v FROM cte ORDER BY v",
		Want:  [][]any{{int64(20)}, {int64(30)}},
	},
	{
		Name: "cte_with_aggregate",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT, v INTEGER)",
			"INSERT INTO t VALUES (1, 'a', 10), (2, 'a', 20), (3, 'b', 30)",
		},
		Query: "WITH sums AS (SELECT g, SUM(v) AS total FROM t GROUP BY g) SELECT g, total FROM sums ORDER BY g",
		Want:  [][]any{{"a", int64(30)}, {"b", int64(30)}},
	},

	// --- CAST ---
	{
		Name: "cast_text_to_integer",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, '42')",
		},
		Query: "SELECT CAST(s AS INTEGER) FROM t",
		Want:  [][]any{{int64(42)}},
	},
	{
		Name: "cast_integer_to_real",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 42)",
		},
		Query: "SELECT CAST(v AS REAL) FROM t",
		Want:  [][]any{{float64(42)}},
	},
	{
		Name: "cast_real_to_integer",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v REAL)",
			"INSERT INTO t VALUES (1, 3.7)",
		},
		Query: "SELECT CAST(v AS INTEGER) FROM t",
		Want:  [][]any{{int64(3)}},
	},

	// --- Complex WHERE clauses ---
	{
		Name: "where_or",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30), (4, 40)",
		},
		Query: "SELECT v FROM t WHERE v = 10 OR v = 30 ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(30)}},
	},
	{
		Name: "where_between_and_not",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 5), (3, 10), (4, 15), (5, 20)",
		},
		Query: "SELECT v FROM t WHERE v BETWEEN 5 AND 15 AND v != 10 ORDER BY v",
		Want:  [][]any{{int64(5)}, {int64(15)}},
	},
	{
		Name: "where_like_underscore",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, 'abc'), (2, 'axc'), (3, 'ayc'), (4, 'adc')",
		},
		Query: "SELECT s FROM t WHERE s LIKE 'a_c' ORDER BY s",
		Want:  [][]any{{"abc"}, {"adc"}, {"axc"}, {"ayc"}},
	},
	{
		Name: "where_not",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT v FROM t WHERE NOT (v = 20) ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(30)}},
	},
	{
		Name: "where_exists_correlated",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
			"CREATE TABLE s (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO s VALUES (1, 20)",
		},
		Query: "SELECT v FROM t WHERE EXISTS (SELECT 1 FROM s WHERE s.v = t.v)",
		Want:  [][]any{{int64(20)}},
	},

	// --- CASE WHEN edge cases ---
	{
		Name: "case_else_null",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2), (3, 3)",
		},
		Query: "SELECT CASE WHEN v > 2 THEN 'big' ELSE NULL END FROM t ORDER BY id",
		Want:  [][]any{{nil}, {nil}, {"big"}},
	},
	{
		Name: "case_no_else",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2), (3, 3)",
		},
		Query: "SELECT CASE WHEN v > 2 THEN 'big' END FROM t ORDER BY id",
		Want:  [][]any{{nil}, {nil}, {"big"}},
	},
	{
		Name: "case_with_between",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 5), (2, 15), (3, 25)",
		},
		Query: "SELECT CASE WHEN v BETWEEN 1 AND 10 THEN 'low' WHEN v BETWEEN 11 AND 20 THEN 'mid' ELSE 'high' END FROM t ORDER BY id",
		Want:  [][]any{{"low"}, {"mid"}, {"high"}},
	},

	// --- UNION variants ---
	{
		Name: "union_all_with_nulls",
		Setup: []string{
			"CREATE TABLE a (x INTEGER PRIMARY KEY)",
			"INSERT INTO a VALUES (1), (NULL)",
			"CREATE TABLE b (x INTEGER PRIMARY KEY)",
			"INSERT INTO b VALUES (NULL), (2)",
		},
		Query: "SELECT x FROM a UNION ALL SELECT x FROM b ORDER BY x",
		Want:  [][]any{{nil}, {nil}, {int64(1)}, {int64(2)}},
	},
	{
		Name: "union_distinct_with_nulls",
		Setup: []string{
			"CREATE TABLE a (x INTEGER PRIMARY KEY)",
			"INSERT INTO a VALUES (1), (NULL)",
			"CREATE TABLE b (x INTEGER PRIMARY KEY)",
			"INSERT INTO b VALUES (NULL), (2)",
		},
		Query: "SELECT x FROM a UNION SELECT x FROM b ORDER BY x",
		Want:  [][]any{{nil}, {int64(1)}, {int64(2)}},
	},
	{
		Name: "three_way_union",
		Setup: []string{
			"CREATE TABLE a (x INTEGER PRIMARY KEY)",
			"INSERT INTO a VALUES (1)",
			"CREATE TABLE b (x INTEGER PRIMARY KEY)",
			"INSERT INTO b VALUES (2)",
			"CREATE TABLE c (x INTEGER PRIMARY KEY)",
			"INSERT INTO c VALUES (3)",
		},
		Query: "SELECT x FROM a UNION SELECT x FROM b UNION SELECT x FROM c ORDER BY x",
		Want:  [][]any{{int64(1)}, {int64(2)}, {int64(3)}},
	},

	// --- Arithmetic edge cases ---
	{
		Name: "integer_division",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT 7 / 2",
		Want:  [][]any{{int64(3)}},
	},
	{
		Name: "negative_division",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT -7 / 2",
		Want:  [][]any{{int64(-3)}},
	},
	{
		Name: "modulo_negative",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT -7 % 3",
		Want:  [][]any{{int64(-1)}},
	},
	{
		Name: "exponentiation_no_op",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT 2 * 3 + 4",
		Want:  [][]any{{int64(10)}},
	},
	{
		Name: "parenthesized_expression",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT (2 + 3) * 4",
		Want:  [][]any{{int64(20)}},
	},

	// --- String edge cases ---
	{
		Name: "string_concat_with_null",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a TEXT, b TEXT)",
			"INSERT INTO t VALUES (1, 'hello', NULL)",
		},
		Query: "SELECT a || b FROM t",
		Want:  [][]any{{nil}},
	},
	{
		Name: "string_concat_all_null",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT NULL || NULL",
		Want:  [][]any{{nil}},
	},
	{
		Name: "length_function",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, 'hello')",
		},
		Query: "SELECT LENGTH(s) FROM t",
		Want:  [][]any{{int64(5)}},
	},
	{
		Name: "length_empty_string",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, '')",
		},
		Query: "SELECT LENGTH(s) FROM t",
		Want:  [][]any{{int64(0)}},
	},
	{
		Name: "substr_basic",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, 'hello')",
		},
		Query: "SELECT SUBSTR(s, 2, 3) FROM t",
		Want:  [][]any{{"ell"}},
	},
	{
		Name: "substr_from_end",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, 'hello')",
		},
		Query: "SELECT SUBSTR(s, -3) FROM t",
		Want:  [][]any{{"llo"}},
	},
	{
		Name: "trim_basic",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, '  hello  ')",
		},
		Query: "SELECT TRIM(s) FROM t",
		Want:  [][]any{{"hello"}},
	},
	{
		Name: "upper_lower",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, 'Hello')",
		},
		Query: "SELECT UPPER(s), LOWER(s) FROM t",
		Want:  [][]any{{"HELLO", "hello"}},
	},
	{
		Name: "replace_basic",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, 'aabbcc')",
		},
		Query: "SELECT REPLACE(s, 'b', 'x') FROM t",
		Want:  [][]any{{"aaxxcc"}},
	},

	// --- Numeric edge cases ---
	{
		Name: "abs_all_variants",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, -5), (2, 0), (3, 5)",
		},
		Query: "SELECT ABS(v) FROM t ORDER BY id",
		Want:  [][]any{{int64(5)}, {int64(0)}, {int64(5)}},
	},
	{
		Name: "round_negative",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT ROUND(-3.5)",
		Want:  [][]any{{float64(-4)}},
	},
	{
		Name: "round_zero_places",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT ROUND(2.4)",
		Want:  [][]any{{float64(2)}},
	},

	// --- Multi-table operations ---
	{
		Name: "cross_join_filter",
		Setup: []string{
			"CREATE TABLE a (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO a VALUES (1, 10), (2, 20)",
			"CREATE TABLE b (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO b VALUES (1, 10), (2, 30)",
		},
		Query: "SELECT a.v, b.v FROM a, b WHERE a.v = b.v",
		Want:  [][]any{{int64(10), int64(10)}},
	},
	{
		Name: "self_join",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 10)",
		},
		Query: "SELECT a.id, b.id FROM t a, t b WHERE a.v = b.v AND a.id < b.id ORDER BY a.id, b.id",
		Want: [][]any{
			{int64(1), int64(3)},
		},
	},

	// --- SELECT DISTINCT edge cases ---
	{
		Name: "distinct_all_nulls",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, NULL), (3, NULL)",
		},
		Query: "SELECT DISTINCT v FROM t",
		Want:  [][]any{{nil}},
	},
	{
		Name: "distinct_mixed_nulls",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, NULL), (3, 10), (4, NULL)",
		},
		Query: "SELECT DISTINCT v FROM t ORDER BY v",
		Want:  [][]any{{int64(10)}, {nil}},
	},
	{
		Name: "distinct_on_expression",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t VALUES (1, 1, 10), (2, 2, 10), (3, 1, 20)",
		},
		Query: "SELECT DISTINCT a FROM t ORDER BY a",
		Want:  [][]any{{int64(1)}, {int64(2)}},
	},

	// --- Boolean expressions ---
	{
		Name: "true_false_literals",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT TRUE, FALSE",
		Want:  [][]any{{int64(1), int64(0)}},
	},
	{
		Name: "boolean_in_where",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 0)",
		},
		Query: "SELECT v FROM t WHERE v ORDER BY v",
		Want:  [][]any{{int64(10)}},
	},

	// --- IN with subquery ---
	{
		Name: "in_empty_subquery",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20)",
			"CREATE TABLE s (id INTEGER PRIMARY KEY, v INTEGER)",
		},
		Query: "SELECT v FROM t WHERE v IN (SELECT v FROM s) ORDER BY v",
		Want:  [][]any{},
	},
	{
		Name: "not_in_empty_subquery",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20)",
			"CREATE TABLE s (id INTEGER PRIMARY KEY, v INTEGER)",
		},
		Query: "SELECT v FROM t WHERE v NOT IN (SELECT v FROM s) ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(20)}},
	},

	// --- Multiple statements in sequence ---
	{
		Name: "multi_statement_insert_update_select",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10)",
			"INSERT INTO t VALUES (2, 20)",
			"INSERT INTO t VALUES (3, 30)",
			"UPDATE t SET v = v + 1 WHERE id = 2",
			"DELETE FROM t WHERE id = 3",
		},
		Query: "SELECT id, v FROM t ORDER BY id",
		Want:  [][]any{{int64(1), int64(10)}, {int64(2), int64(21)}},
	},

	// --- Column aliasing ---
	{
		Name: "column_alias_in_select",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 42)",
		},
		Query: "SELECT v AS value FROM t",
		Want:  [][]any{{int64(42)}},
	},
	{
		Name: "column_alias_in_where",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20)",
		},
		Query: "SELECT v AS value FROM t WHERE value > 15",
		Want:  [][]any{{int64(20)}},
	},

	// --- Table aliases ---
	{
		Name: "table_alias_basic",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10)",
		},
		Query: "SELECT x.v FROM t AS x",
		Want:  [][]any{{int64(10)}},
	},
	{
		Name: "table_alias_join",
		Setup: []string{
			"CREATE TABLE a (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO a VALUES (1, 10)",
			"CREATE TABLE b (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO b VALUES (1, 20)",
		},
		Query: "SELECT a.v, b.v FROM a AS a JOIN b AS b ON a.id = b.id",
		Want:  [][]any{{int64(10), int64(20)}},
	},

	// --- CREATE TABLE constraints ---
	{
		Name: "create_table_not_null",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER NOT NULL)",
			"INSERT INTO t VALUES (1, 10)",
		},
		Query: "SELECT v FROM t",
		Want:  [][]any{{int64(10)}},
	},
	{
		Name: "create_table_default",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER DEFAULT 42)",
			"INSERT INTO t (id) VALUES (1)",
		},
		Query: "SELECT v FROM t",
		Want:  [][]any{{int64(42)}},
	},

	// --- Aggregate with GROUP BY and ORDER BY ---
	{
		Name: "aggregate_groupby_orderby_desc",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT, v INTEGER)",
			"INSERT INTO t VALUES (1, 'a', 10), (2, 'a', 20), (3, 'b', 5)",
		},
		Query: "SELECT g, SUM(v) FROM t GROUP BY g ORDER BY SUM(v) DESC",
		Want:  [][]any{{"a", int64(30)}, {"b", int64(5)}},
	},
	{
		Name: "aggregate_groupby_having_orderby",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT, v INTEGER)",
			"INSERT INTO t VALUES (1, 'a', 10), (2, 'a', 20), (3, 'b', 5), (4, 'b', 15)",
		},
		Query: "SELECT g, SUM(v) AS total FROM t GROUP BY g HAVING total > 20 ORDER BY total DESC",
		Want:  [][]any{{"a", int64(30)}},
	},

	// --- Window functions (basic) ---
	{
		Name: "row_number",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 30), (2, 10), (3, 20)",
		},
		Query: "SELECT v, ROW_NUMBER() OVER (ORDER BY v) FROM t",
		Want: [][]any{
			{int64(10), int64(1)},
			{int64(20), int64(2)},
			{int64(30), int64(3)},
		},
	},
	{
		Name: "rank_function",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 20), (4, 30)",
		},
		Query: "SELECT v, RANK() OVER (ORDER BY v) FROM t",
		Want: [][]any{
			{int64(10), int64(1)},
			{int64(20), int64(2)},
			{int64(20), int64(2)},
			{int64(30), int64(4)},
		},
	},
	{
		Name: "sum_window",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT, v INTEGER)",
			"INSERT INTO t VALUES (1, 'a', 10), (2, 'a', 20), (3, 'b', 5)",
		},
		Query: "SELECT v, SUM(v) OVER (PARTITION BY g) FROM t ORDER BY id",
		Want: [][]any{
			{int64(10), int64(30)},
			{int64(20), int64(30)},
			{int64(5), int64(5)},
		},
	},

	// --- Nested subqueries ---
	{
		Name: "nested_scalar_subquery",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT (SELECT MAX(v) FROM (SELECT v FROM t WHERE v < 30))",
		Want:  [][]any{{int64(20)}},
	},

	// --- UNION with different column types ---
	{
		Name: "union_different_types",
		Setup: []string{
			"CREATE TABLE a (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO a VALUES (1, 10)",
			"CREATE TABLE b (id INTEGER PRIMARY KEY, v TEXT)",
			"INSERT INTO b VALUES (1, '20')",
		},
		Query: "SELECT v FROM a UNION ALL SELECT v FROM b ORDER BY v",
		Want:  [][]any{{int64(10)}, {"20"}},
	},

	// --- LIMIT with ORDER BY and aggregation ---
	{
		Name: "limit_with_groupby",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT, v INTEGER)",
			"INSERT INTO t VALUES (1, 'a', 10), (2, 'a', 20), (3, 'b', 5), (4, 'c', 15)",
		},
		Query: "SELECT g, SUM(v) FROM t GROUP BY g ORDER BY SUM(v) DESC LIMIT 2",
		Want:  [][]any{{"a", int64(30)}, {"c", int64(15)}},
	},

	// --- EXISTS with multiple rows ---
	{
		Name: "exists_multiple_rows",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
			"CREATE TABLE s (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO s VALUES (1, 10), (2, 20)",
		},
		Query: "SELECT EXISTS (SELECT 1 FROM s WHERE v = 15)",
		Want:  [][]any{{int64(0)}},
	},

	// --- UPDATE with subquery ---
	{
		Name: "update_with_subquery",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
			"UPDATE t SET v = (SELECT MAX(v) FROM t) WHERE id = 1",
		},
		Query: "SELECT v FROM t ORDER BY id",
		Want:  [][]any{{int64(30)}, {int64(20)}, {int64(30)}},
	},

	// --- DELETE with subquery ---
	{
		Name: "delete_with_subquery",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
			"DELETE FROM t WHERE v > (SELECT AVG(v) FROM t)",
		},
		Query: "SELECT v FROM t ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(20)}},
	},

	// --- Aggregate with CASE ---
	{
		Name: "sum_case",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT SUM(CASE WHEN v > 15 THEN v ELSE 0 END) FROM t",
		Want:  [][]any{{int64(50)}},
	},
	{
		Name: "count_case",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30), (4, 5)",
		},
		Query: "SELECT COUNT(CASE WHEN v > 15 THEN 1 END) FROM t",
		Want:  [][]any{{int64(2)}},
	},

	// --- NULLIF edge cases ---
	{
		Name: "nullif_with_nulls",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t VALUES (1, NULL, 10)",
		},
		Query: "SELECT NULLIF(a, b) FROM t",
		Want:  [][]any{{nil}},
	},
	{
		Name: "nullif_different_values",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t VALUES (1, 10, 20)",
		},
		Query: "SELECT NULLIF(a, b) FROM t",
		Want:  [][]any{{int64(10)}},
	},

	// --- COALESCE edge cases ---
	{
		Name: "coalesce_all_args",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER)",
			"INSERT INTO t VALUES (1, NULL, NULL, 42)",
		},
		Query: "SELECT COALESCE(a, b, c) FROM t",
		Want:  [][]any{{int64(42)}},
	},
	{
		Name: "coalesce_first_nonnull",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t VALUES (1, 10, 20)",
		},
		Query: "SELECT COALESCE(a, b) FROM t",
		Want:  [][]any{{int64(10)}},
	},

	// --- DROP TABLE ---
	{
		Name: "drop_table",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t1 VALUES (1, 10)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t2 VALUES (1, 20)",
			"DROP TABLE t1",
		},
		Query: "SELECT v FROM t2",
		Want:  [][]any{{int64(20)}},
	},

	// --- Multiple aggregates with different types ---
	{
		Name: "agg_string_and_numeric",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT, v INTEGER)",
			"INSERT INTO t VALUES (1, 'a', 10), (2, 'b', 20), (3, 'c', 30)",
		},
		Query: "SELECT MIN(s), MAX(s), SUM(v) FROM t",
		Want:  [][]any{{"a", "c", int64(60)}},
	},

	// --- Expression in SELECT ---
	{
		Name: "expression_in_select",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 5)",
		},
		Query: "SELECT v * 2 + 1, v - 3, v * v FROM t",
		Want:  [][]any{{int64(11), int64(2), int64(25)}},
	},

	// --- Empty result sets ---
	{
		Name: "empty_result_no_rows",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
		},
		Query: "SELECT v FROM t WHERE 1 = 0",
		Want:  [][]any{},
	},
	{
		Name: "empty_table_distinct",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
		},
		Query: "SELECT DISTINCT v FROM t",
		Want:  [][]any{},
	},

	// --- NULL in aggregates ---
	{
		Name: "count_nulls",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, 10), (3, NULL)",
		},
		Query: "SELECT COUNT(v) FROM t",
		Want:  [][]any{{int64(1)}},
	},
	{
		Name: "count_star_vs_count_col",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, 10)",
		},
		Query: "SELECT COUNT(*), COUNT(v) FROM t",
		Want:  [][]any{{int64(2), int64(1)}},
	},
	{
		Name: "avg_all_nulls",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, NULL)",
		},
		Query: "SELECT AVG(v) FROM t",
		Want:  [][]any{{nil}},
	},
	{
		Name: "sum_all_nulls",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, NULL)",
		},
		Query: "SELECT SUM(v) FROM t",
		Want:  [][]any{{nil}},
	},

	// --- ORDER BY with NULLS ---
	{
		Name: "orderby_nulls_last",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, 30), (3, 10), (4, NULL)",
		},
		Query: "SELECT v FROM t ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(30)}, {nil}, {nil}},
	},
	{
		Name: "orderby_nulls_desc",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL), (2, 30), (3, 10)",
		},
		Query: "SELECT v FROM t ORDER BY v DESC",
		Want:  [][]any{{nil}, {int64(30)}, {int64(10)}},
	},

	// --- Multiple WHERE conditions ---
	{
		Name: "where_chain_of_ors",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2), (3, 3), (4, 4), (5, 5)",
		},
		Query: "SELECT v FROM t WHERE v = 1 OR v = 3 OR v = 5 ORDER BY v",
		Want:  [][]any{{int64(1)}, {int64(3)}, {int64(5)}},
	},
	{
		Name: "where_complex_and_or",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER)",
			"INSERT INTO t VALUES (1, 1, 10, 100), (2, 2, 20, 200), (3, 1, 30, 300)",
		},
		Query: "SELECT id FROM t WHERE (a = 1 AND b > 15) OR c = 200 ORDER BY id",
		Want:  [][]any{{int64(2)}, {int64(3)}},
	},

	// --- INSERT with multiple values ---
	{
		Name: "insert_multiple_values",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT COUNT(*) FROM t",
		Want:  [][]any{{int64(3)}},
	},
	{
		Name: "insert_select",
		Setup: []string{
			"CREATE TABLE src (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO src VALUES (1, 10), (2, 20)",
			"CREATE TABLE dst (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO dst SELECT * FROM src WHERE v > 15",
		},
		Query: "SELECT v FROM dst ORDER BY v",
		Want:  [][]any{{int64(20)}},
	},

	// --- Aggregate on empty table ---
	{
		Name: "count_empty",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
		},
		Query: "SELECT COUNT(*) FROM t",
		Want:  [][]any{{int64(0)}},
	},
	{
		Name: "sum_empty",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
		},
		Query: "SELECT SUM(v) FROM t",
		Want:  [][]any{{nil}},
	},
	{
		Name: "avg_empty",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
		},
		Query: "SELECT AVG(v) FROM t",
		Want:  [][]any{{nil}},
	},
	{
		Name: "min_empty",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
		},
		Query: "SELECT MIN(v) FROM t",
		Want:  [][]any{{nil}},
	},
	{
		Name: "max_empty",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
		},
		Query: "SELECT MAX(v) FROM t",
		Want:  [][]any{{nil}},
	},

	// --- GROUP BY with NULL group key ---
	{
		Name: "groupby_null_key",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g INTEGER, v INTEGER)",
			"INSERT INTO t VALUES (1, NULL, 10), (2, NULL, 20), (3, 1, 30)",
		},
		Query: "SELECT g, SUM(v) FROM t GROUP BY g ORDER BY g",
		Want:  [][]any{{int64(30)}, {nil, int64(30)}},
	},

	// --- Multiple CTEs ---
	{
		Name: "multiple_ctes",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "WITH a AS (SELECT v FROM t WHERE v < 20), b AS (SELECT v FROM t WHERE v > 15) SELECT v FROM a UNION ALL SELECT v FROM b ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(20)}, {int64(30)}},
	},
}
