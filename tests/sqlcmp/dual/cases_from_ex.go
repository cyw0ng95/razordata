package dual

// Cases migrated from internal/SQB/EX/ to reduce file count
// and concentrate SQL evaluation tests in the dual harness.

// nullifCases from EX/req000807_test.go: NULLIF semantics.
var nullifCases = []dualCase{
	{
		Name:  "nullif_equal",
		Query: "SELECT NULLIF(5, 5)",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "nullif_not_equal",
		Query: "SELECT NULLIF(5, 3)",
		Want:  [][]any{{int64(5)}},
	},
	{
		Name:  "nullif_negative",
		Query: "SELECT NULLIF(-3, 1000)",
		Want:  [][]any{{int64(-3)}},
	},
	{
		Name:  "nullif_first_null",
		Query: "SELECT NULLIF(NULL, 5)",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "nullif_second_null",
		Query: "SELECT NULLIF(5, NULL)",
		Want:  [][]any{{int64(5)}},
	},
}

// hexLiteralCases from EX/req000808_test.go: X'...' hex literal.
var hexLiteralCases = []dualCase{
	{
		Name:  "hex_AB",
		Query: "SELECT X'4142'",
		Want:  [][]any{{"AB"}},
	},
	{
		Name:  "hex_012",
		Query: "SELECT x'303132'",
		Want:  [][]any{{"012"}},
	},
	{
		Name:  "hex_empty",
		Query: "SELECT X''",
		Want:  [][]any{{""}},
	},
	{
		Name:  "hex_hello",
		Query: "SELECT X'48656C6C6F'",
		Want:  [][]any{{"Hello"}},
	},
}

// divByZeroCases from EX/req000809_test.go: division by zero returns NULL.
var divByZeroCases = []dualCase{
	{
		Name:  "null_div_minus_zero",
		Query: "SELECT CAST(NULL AS INTEGER) / -0",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "int_div_zero",
		Query: "SELECT 1 / 0",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "float_div_zero",
		Query: "SELECT 1.0 / 0.0",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "mod_zero",
		Query: "SELECT 1 % 0",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "div_by_zero_keyword",
		Query: "SELECT 1 DIV 0",
		Want:  [][]any{{nil}},
	},
}

// unaryNegCases from EX/neg_literal_test.go: unary minus on column values.
//
//nolint:dupword
var unaryNegCases = []dualCase{
	{
		Name: "negate_column",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t1 VALUES (1, 10, 20)",
		},
		Query: "SELECT -a FROM t1",
		Want:  [][]any{{int64(-10)}},
	},
	{
		Name: "negate_negate_column",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t1 VALUES (1, 10, 20)",
		},
		Query: "SELECT -(-a) FROM t1",
		Want:  [][]any{{int64(10)}},
	},
	{
		Name: "negate_column_case_insensitive",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t1 VALUES (1, 10, 20)",
		},
		Query: "SELECT -A FROM t1",
		Want:  [][]any{{int64(-10)}},
	},
}

// nullHandlingCases from EX/null_req_test.go: NULL three-valued logic
// and CAST(NULL) semantics.
var nullHandlingCases = []dualCase{
	{
		Name: "not_between_null",
		Setup: []string{
			"CREATE TABLE t929 (id INTEGER PRIMARY KEY, col1 INTEGER)",
			"INSERT INTO t929 VALUES (1, 1), (2, 2), (3, 3), (4, 4), (5, 5), (6, 6), (7, 7), (8, 8), (9, 9)",
		},
		Query: "SELECT * FROM t929 WHERE col1 NOT BETWEEN NULL AND -col1",
		Want:  [][]any{},
	},
	{
		Name: "cast_null_decimal",
		Setup: []string{
			"CREATE TABLE t931a (id INTEGER PRIMARY KEY)",
			"INSERT INTO t931a VALUES (1), (2), (3)",
		},
		Query: "SELECT CAST(NULL AS DECIMAL) FROM t931a",
		Want:  [][]any{{nil}, {nil}, {nil}},
	},
	{
		Name: "cast_null_signed",
		Setup: []string{
			"CREATE TABLE t931b (id INTEGER PRIMARY KEY)",
			"INSERT INTO t931b VALUES (1), (2)",
		},
		Query: "SELECT CAST(NULL AS SIGNED) FROM t931b",
		Want:  [][]any{{nil}, {nil}},
	},
	{
		Name: "sum_all_null",
		Setup: []string{
			"CREATE TABLE t942 (id INTEGER PRIMARY KEY)",
			"INSERT INTO t942 VALUES (1), (2), (3)",
		},
		Query: "SELECT SUM(ALL CAST(NULL AS SIGNED)) FROM t942",
		Want:  [][]any{{nil}},
	},
	{
		Name: "nullif_chained_unary",
		Setup: []string{
			"CREATE TABLE t915 (id INTEGER PRIMARY KEY)",
			"INSERT INTO t915 VALUES (1), (2)",
		},
		Query: "SELECT NULLIF(-COUNT(*), +67 * - - (+25) + 89 + -39 * 63) + +54 FROM t915",
		Want:  [][]any{{int64(52)}},
	},
	{
		Name: "is_not_null_filter",
		Setup: []string{
			"CREATE TABLE t941 (id INTEGER PRIMARY KEY, col0 INTEGER)",
			"INSERT INTO t941 VALUES (1, 1), (2, 2), (3, 3)",
		},
		Query: "SELECT col0 FROM t941 WHERE +col0 IS NOT NULL",
		Want:  [][]any{{int64(1)}, {int64(2)}, {int64(3)}},
	},
	{
		Name: "compare_with_null",
		Setup: []string{
			"CREATE TABLE t912 (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t912 VALUES (1, 10), (2, NULL), (3, 30)",
		},
		Query: "SELECT id FROM t912 WHERE v > 20",
		Want:  [][]any{{int64(3)}},
	},
	{
		Name: "or_in_isnull",
		Setup: []string{
			"CREATE TABLE t913 (id INTEGER PRIMARY KEY, col0 INTEGER, col1 INTEGER, col3 INTEGER)",
			"INSERT INTO t913 VALUES (1, 1, 50, 30), (2, 2, 80, NULL), (3, 3, 60, 50)",
		},
		Query: "SELECT col0 FROM t913 WHERE ((col1 < 71.25)) OR (col3 IN (53,42,27,44) OR (col3 IS NULL)) AND (col0 >= 42)",
		Want:  [][]any{{int64(1)}, {int64(3)}},
	},
	{
		Name: "sum_distinct_text_negation",
		Setup: []string{
			"CREATE TABLE t918 (id INTEGER PRIMARY KEY, col2 TEXT)",
			"INSERT INTO t918 VALUES (1, 'a'), (2, 'b'), (3, 'a')",
		},
		Query: "SELECT SUM(DISTINCT -col2) FROM t918",
		Want:  [][]any{{nil}},
	},
}

// caseWhenCases from EX/req000832_test.go: CASE/COALESCE with NULL and aggregates.
var caseWhenCases = []dualCase{
	{
		Name: "not_not_ge_null",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t VALUES (1, 1)",
		},
		Query: "SELECT NOT(NOT -79 >= NULL) FROM t",
		Want:  [][]any{{nil}},
	},
	{
		Name: "case_when_null_else",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t VALUES (1, 1)",
		},
		Query: "SELECT CASE WHEN NULL THEN 'a' ELSE 'b' END FROM t",
		Want:  [][]any{{"b"}},
	},
	{
		Name: "coalesce_null_case",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t VALUES (1, 1)",
		},
		Query: "SELECT COALESCE((CASE WHEN NULL THEN 1 END), 99) FROM t",
		Want:  [][]any{{int64(99)}},
	},
	{
		Name: "coalesce_nonnull_case",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t VALUES (1, 1)",
		},
		Query: "SELECT COALESCE((CASE WHEN 1=1 THEN 42 END), 99) FROM t",
		Want:  [][]any{{int64(42)}},
	},
	{
		Name: "coalesce_aggregate_min",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t VALUES (1, -30)",
		},
		Query: "SELECT COALESCE(NULL, MIN(x) * 10) FROM t",
		Want:  [][]any{{int64(-300)}},
	},
	{
		Name: "coalesce_aggregate_sum",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT COALESCE(NULL, SUM(x)) FROM t",
		Want:  [][]any{{int64(60)}},
	},
	{
		Name: "coalesce_aggregate_first_nonnull",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t VALUES (1, 5)",
		},
		Query: "SELECT COALESCE(MAX(x), 99) FROM t",
		Want:  [][]any{{int64(5)}},
	},
}

// scalarSubqueryCases from EX/req000859_test.go: subquery in SELECT list.
var scalarSubqueryCases = []dualCase{
	{
		Name: "nested_scalar_subquery",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT (SELECT MAX(v) FROM (SELECT v FROM t WHERE v < 30))",
		Want:  [][]any{{int64(20)}},
	},
	{
		Name: "outer_scalar_subquery_constant",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT (SELECT 1)",
		Want:  [][]any{{int64(1)}},
	},
	{
		Name: "outer_scalar_subquery_max",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "SELECT (SELECT MAX(v) FROM t)",
		Want:  [][]any{{int64(30)}},
	},
}

// cteCases from EX/req000813_test.go: CTE (WITH clause).
var cteCases = []dualCase{
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
		Name: "cte_aggregate",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT, v INTEGER)",
			"INSERT INTO t VALUES (1, 'a', 10), (2, 'a', 20), (3, 'b', 30)",
		},
		Query: "WITH sums AS (SELECT g, SUM(v) AS total FROM t GROUP BY g) SELECT g, total FROM sums ORDER BY g",
		Want:  [][]any{{"a", int64(30)}, {"b", int64(30)}},
	},
	{
		Name: "cte_multiple",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
		},
		Query: "WITH a AS (SELECT v FROM t WHERE v < 20), b AS (SELECT v FROM t WHERE v > 15) SELECT v FROM a UNION ALL SELECT v FROM b ORDER BY v",
		Want:  [][]any{{int64(10)}, {int64(20)}, {int64(30)}},
	},
	{
		Name:  "cte_values",
		Query: "WITH cte AS (SELECT 10 AS x UNION ALL SELECT 20 UNION ALL SELECT 30) SELECT * FROM cte ORDER BY x",
		Want:  [][]any{{int64(10)}, {int64(20)}, {int64(30)}},
	},
}

// compoundCases from EX/req000838_test.go: UNION ALL / EXCEPT / INTERSECT.
var compoundCases = []dualCase{
	{
		Name: "compound_union_all",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t1 VALUES (1, 1), (2, 2)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t2 VALUES (1, 3)",
		},
		Query: "SELECT x FROM t1 UNION ALL SELECT x FROM t2 ORDER BY x",
		Want:  [][]any{{int64(1)}, {int64(2)}, {int64(3)}},
	},
	{
		Name: "compound_except",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t1 VALUES (1, 1), (2, 2), (3, 3)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t2 VALUES (1, 2)",
		},
		Query: "SELECT x FROM t1 EXCEPT SELECT x FROM t2 ORDER BY x",
		Want:  [][]any{{int64(1)}, {int64(3)}},
	},
	{
		Name: "compound_intersect",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t1 VALUES (1, 1), (2, 2)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t2 VALUES (1, 2), (2, 3)",
		},
		Query: "SELECT x FROM t1 INTERSECT SELECT x FROM t2 ORDER BY x",
		Want:  [][]any{{int64(2)}},
	},
	{
		Name: "compound_except_chain",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t1 VALUES (1, 1), (2, 2), (3, 3)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t2 VALUES (1, 2)",
			"CREATE TABLE t3 (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t3 VALUES (1, 3)",
		},
		Query: "SELECT x FROM t1 EXCEPT SELECT x FROM t2 EXCEPT SELECT x FROM t3 ORDER BY x",
		Want:  [][]any{{int64(1)}},
	},
	{
		Name: "compound_union_dedup",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t1 VALUES (1, 1), (2, 2)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, x INTEGER)",
			"INSERT INTO t2 VALUES (1, 2), (2, 3)",
		},
		Query: "SELECT x FROM t1 UNION SELECT x FROM t2 ORDER BY x",
		Want:  [][]any{{int64(1)}, {int64(2)}, {int64(3)}},
	},
}

// crossJoinCases from EX/req000861_test.go: multi-table cross join with
// IN filter and cross-table OR.
var crossJoinCases = []dualCase{
	{
		Name: "cross_join_no_filter",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t1 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t2 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t3 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t3 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t4 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t4 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
		},
		Query: "SELECT count(*) FROM t1, t2, t3, t4",
		Want:  [][]any{{int64(10000)}},
	},
	{
		Name: "cross_join_in_filter",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t1 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t2 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t3 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t3 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t4 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t4 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
		},
		Query: "SELECT count(*) FROM t1, t2, t3, t4 WHERE t1.a IN (101,103)",
		Want:  [][]any{{int64(2000)}},
	},
	{
		Name: "cross_join_or_filter",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t1 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t2 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t3 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t3 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t4 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t4 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
		},
		Query: "SELECT count(*) FROM t1, t2, t3, t4 WHERE t2.b > 105 OR t4.d < 103",
		Want:  [][]any{{int64(5800)}},
	},
	{
		Name: "cross_join_in_and_or",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t1 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t2 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t3 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t3 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
			"CREATE TABLE t4 (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)",
			"INSERT INTO t4 VALUES (1, 101, 101, 101, 101, 101), (2, 102, 102, 102, 102, 102), (3, 103, 103, 103, 103, 103), (4, 104, 104, 104, 104, 104), (5, 105, 105, 105, 105, 105), (6, 106, 106, 106, 106, 106), (7, 107, 107, 107, 107, 107), (8, 108, 108, 108, 108, 108), (9, 109, 109, 109, 109, 109), (10, 110, 110, 110, 110, 110)",
		},
		Query: "SELECT count(*) FROM t1, t2, t3, t4 WHERE t1.a IN (101,103) AND (t2.b > 105 OR t4.d < 103)",
		Want:  [][]any{{int64(1160)}},
	},
}
