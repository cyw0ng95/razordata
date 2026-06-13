package dual

// probeCases surfaces bugs discovered through edge-case probing.
// All tables include PRIMARY KEY (RazorData requirement).
var probeCases = []dualCase{
	// JOIN probes
	{
		Name: "cross_join_basic",
		Setup: []string{
			"CREATE TABLE a (x INTEGER PRIMARY KEY)",
			"INSERT INTO a VALUES (1), (2)",
			"CREATE TABLE b (y INTEGER PRIMARY KEY)",
			"INSERT INTO b VALUES (10), (20)",
		},
		Query: "SELECT a.x, b.y FROM a, b ORDER BY a.x, b.y",
		Want: [][]any{
			{int64(1), int64(10)},
			{int64(1), int64(20)},
			{int64(2), int64(10)},
			{int64(2), int64(20)},
		},
	},
	// NULL in arithmetic inside column
	{
		Name: "null_arithmetic_in_column",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10), (2, NULL), (3, 20)",
		},
		Query: "SELECT v + 5 FROM t ORDER BY id",
		Want: [][]any{
			{int64(15)},
			{nil},
			{int64(25)},
		},
	},
	// String comparison
	{
		Name: "string_lt",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, 'apple'), (2, 'banana'), (3, 'cherry')",
		},
		Query: "SELECT s FROM t WHERE s < 'c' ORDER BY s",
		Want: [][]any{
			{"apple"},
			{"banana"},
		},
	},
	// BETWEEN
	{
		Name: "between_inclusive",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2), (3, 3), (4, 4), (5, 5)",
		},
		Query: "SELECT v FROM t WHERE v BETWEEN 2 AND 4 ORDER BY v",
		Want: [][]any{
			{int64(2)},
			{int64(3)},
			{int64(4)},
		},
	},
	// LIKE pattern
	{
		Name: "like_prefix",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, 'hello'), (2, 'help'), (3, 'world')",
		},
		Query: "SELECT s FROM t WHERE s LIKE 'hel%' ORDER BY s",
		Want: [][]any{
			{"hello"},
			{"help"},
		},
	},
	// ORDER BY DESC
	{
		Name: "order_by_desc",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 3), (3, 2)",
		},
		Query: "SELECT v FROM t ORDER BY v DESC",
		Want: [][]any{
			{int64(3)},
			{int64(2)},
			{int64(1)},
		},
	},
	// Subquery in WHERE
	{
		Name: "subquery_in_where",
		Setup: []string{
			"CREATE TABLE t (v INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1), (2), (3)",
			"CREATE TABLE s (v INTEGER PRIMARY KEY)",
			"INSERT INTO s VALUES (2), (3)",
		},
		Query: "SELECT v FROM t WHERE v IN (SELECT v FROM s) ORDER BY v",
		Want: [][]any{
			{int64(2)},
			{int64(3)},
		},
	},
	// CASE WHEN
	{
		Name: "case_when_basic",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2), (3, 3), (4, NULL)",
		},
		Query: "SELECT CASE WHEN v IS NULL THEN 'null' WHEN v < 2 THEN 'small' ELSE 'big' END FROM t ORDER BY id",
		Want: [][]any{
			{"small"},
			{"big"},
			{"big"},
			{"null"},
		},
	},
	// DISTINCT
	{
		Name: "distinct_basic",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 1), (3, 2), (4, 2), (5, 3)",
		},
		Query: "SELECT DISTINCT v FROM t ORDER BY v",
		Want: [][]any{
			{int64(1)},
			{int64(2)},
			{int64(3)},
		},
	},
	// LIMIT
	{
		Name: "limit_basic",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2), (3, 3), (4, 4), (5, 5)",
		},
		Query: "SELECT v FROM t ORDER BY v LIMIT 2",
		Want: [][]any{
			{int64(1)},
			{int64(2)},
		},
	},
	// CAST
	{
		Name: "cast_int_to_text",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 42)",
		},
		Query: "SELECT CAST(v AS TEXT) FROM t",
		Want: [][]any{
			{"42"},
		},
	},
	// EXISTS
	{
		Name: "exists_subquery",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2)",
			"CREATE TABLE s (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO s VALUES (1, 2)",
		},
		Query: "SELECT EXISTS(SELECT 1 FROM s WHERE v = 2)",
		Want: [][]any{
			{int64(1)},
		},
	},
	// Negative numbers
	{
		Name: "negative_arithmetic",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, -5), (2, 0), (3, 5)",
		},
		Query: "SELECT v * 2 FROM t ORDER BY id",
		Want: [][]any{
			{int64(-10)},
			{int64(0)},
			{int64(10)},
		},
	},
	// Division by zero
	{
		Name: "division_by_zero",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10)",
		},
		Query: "SELECT v / 0 FROM t",
		Want: [][]any{
			{nil},
		},
	},
	// Multiple columns in WHERE
	{
		Name: "where_and",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)",
			"INSERT INTO t VALUES (1, 1, 10), (2, 2, 20), (3, 1, 30)",
		},
		Query: "SELECT b FROM t WHERE a = 1 AND b > 15 ORDER BY b",
		Want: [][]any{
			{int64(20)},
			{int64(30)},
		},
	},
	// GROUP BY with HAVING
	{
		Name: "groupby_having",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT, v INTEGER)",
			"INSERT INTO t VALUES (1, 'a', 1), (2, 'a', 2), (3, 'b', 3)",
		},
		Query: "SELECT g, SUM(v) FROM t GROUP BY g HAVING SUM(v) > 2 ORDER BY g",
		Want: [][]any{
			{"a", int64(3)},
			{"b", int64(3)},
		},
	},
	// IN with literal list
	{
		Name: "in_list",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2), (3, 3), (4, 4)",
		},
		Query: "SELECT v FROM t WHERE v IN (1, 3) ORDER BY v",
		Want: [][]any{
			{int64(1)},
			{int64(3)},
		},
	},
	// String functions
	{
		Name: "upper_function",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, 'hello')",
		},
		Query: "SELECT UPPER(s) FROM t",
		Want: [][]any{
			{"HELLO"},
		},
	},
	// REQ000379: chained unary minus. SQLite treats `--` as a
	// line comment unconditionally (even after a value token), so
	// `SELECT 5--5` returns 5. Chained unary minus requires an
	// explicit space: `SELECT 5- -5` = 10. The parser must
	// accept the explicit-space form; the no-space form is
	// always a comment.
	{
		Name: "chained_unary_minus_with_space",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT 5- -5",
		Want: [][]any{
			{int64(10)},
		},
	},
	{
		Name: "chained_unary_minus_three_with_space",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT 5- - -5",
		Want: [][]any{
			{int64(10)},
		},
	},
	{
		Name: "minus_minus_value_is_comment",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT 5-- this is a comment",
		Want: [][]any{
			{int64(5)},
		},
	},
	// REQ000378: HAVING with COUNT(*). The aggregate column
	// emitted by the Aggregate operator is named `COUNT(*)` (with
	// the star literal), not `COUNT(col)`. evalAggregate must
	// look up the StarExpr form, not just the Ident form.
	{
		Name: "having_count_star",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT)",
			"INSERT INTO t VALUES (1, 'a'), (2, 'a'), (3, 'b')",
		},
		Query: "SELECT g, COUNT(*) FROM t GROUP BY g HAVING COUNT(*) > 1 ORDER BY g",
		Want: [][]any{
			{"a", int64(2)},
		},
	},
	// REQ000380: `NOT LIKE` — parse as NOT (x LIKE y); eval
	// negates the LIKE result.
	{
		Name: "not_like_true",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, 'abc'), (2, 'xyz')",
		},
		Query: "SELECT s FROM t WHERE s NOT LIKE 'a%' ORDER BY s",
		Want: [][]any{
			{"xyz"},
		},
	},
	{
		Name: "not_like_false",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)",
			"INSERT INTO t VALUES (1, 'abc'), (2, 'xyz')",
		},
		Query: "SELECT s FROM t WHERE s NOT LIKE 'x%' ORDER BY s",
		Want: [][]any{
			{"abc"},
		},
	},
	// REQ000381: `NOT IN` — list and subquery forms.
	{
		Name: "not_in_list",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2), (3, 3), (4, 4)",
		},
		Query: "SELECT v FROM t WHERE v NOT IN (1, 3) ORDER BY v",
		Want: [][]any{
			{int64(2)},
			{int64(4)},
		},
	},
	{
		Name: "not_in_subquery",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 1), (2, 2), (3, 3)",
			"CREATE TABLE s (v INTEGER PRIMARY KEY)",
			"INSERT INTO s VALUES (2), (3)",
		},
		Query: "SELECT v FROM t WHERE v NOT IN (SELECT v FROM s) ORDER BY v",
		Want: [][]any{
			{int64(1)},
		},
	},
	// REQ000382: ABS, HEX, ROUND scalar functions.
	{
		Name: "abs_negative",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT ABS(-7)",
		Want: [][]any{
			{int64(7)},
		},
	},
	{
		Name: "abs_positive",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT ABS(7)",
		Want: [][]any{
			{int64(7)},
		},
	},
	{
		Name: "abs_null",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT ABS(NULL)",
		Want: [][]any{
			{nil},
		},
	},
	{
		Name: "hex_string",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT HEX('abc')",
		Want: [][]any{
			{"616263"},
		},
	},
	{
		Name: "hex_integer",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT HEX(255)",
		Want: [][]any{
			{"323535"},
		},
	},
	{
		Name: "round_no_places",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT ROUND(3.5)",
		Want: [][]any{
			{float64(4)},
		},
	},
	{
		Name: "round_with_places",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY)",
			"INSERT INTO t VALUES (1)",
		},
		Query: "SELECT ROUND(3.14159, 2)",
		Want: [][]any{
			{float64(3.14)},
		},
	},
	// REQ000383: compound SELECT (UNION, UNION ALL, INTERSECT,
	// EXCEPT). All four operators with dedup semantics matching
	// SQLite standard.
	{
		Name: "union_dedup",
		Setup: []string{
			"CREATE TABLE a (x INTEGER PRIMARY KEY)",
			"INSERT INTO a VALUES (1), (2)",
			"CREATE TABLE b (x INTEGER PRIMARY KEY)",
			"INSERT INTO b VALUES (2), (3)",
		},
		Query: "SELECT x FROM a UNION SELECT x FROM b ORDER BY x",
		Want: [][]any{
			{int64(1)},
			{int64(2)},
			{int64(3)},
		},
	},
	{
		Name: "union_all_no_dedup",
		Setup: []string{
			"CREATE TABLE a (x INTEGER PRIMARY KEY)",
			"INSERT INTO a VALUES (1), (2)",
			"CREATE TABLE b (x INTEGER PRIMARY KEY)",
			"INSERT INTO b VALUES (2), (3)",
		},
		Query: "SELECT x FROM a UNION ALL SELECT x FROM b ORDER BY x",
		Want: [][]any{
			{int64(1)},
			{int64(2)},
			{int64(2)},
			{int64(3)},
		},
	},
	{
		Name: "intersect",
		Setup: []string{
			"CREATE TABLE a (x INTEGER PRIMARY KEY)",
			"INSERT INTO a VALUES (1), (2), (3)",
			"CREATE TABLE b (x INTEGER PRIMARY KEY)",
			"INSERT INTO b VALUES (2), (3), (4)",
		},
		Query: "SELECT x FROM a INTERSECT SELECT x FROM b ORDER BY x",
		Want: [][]any{
			{int64(2)},
			{int64(3)},
		},
	},
	{
		Name: "except",
		Setup: []string{
			"CREATE TABLE a (x INTEGER PRIMARY KEY)",
			"INSERT INTO a VALUES (1), (2), (3)",
			"CREATE TABLE b (x INTEGER PRIMARY KEY)",
			"INSERT INTO b VALUES (2), (3), (4)",
		},
		Query: "SELECT x FROM a EXCEPT SELECT x FROM b ORDER BY x",
		Want: [][]any{
			{int64(1)},
		},
	},
	{
		Name: "union_empty_left",
		Setup: []string{
			"CREATE TABLE a (x INTEGER PRIMARY KEY)",
			"CREATE TABLE b (x INTEGER PRIMARY KEY)",
			"INSERT INTO b VALUES (1), (2)",
		},
		Query: "SELECT x FROM a UNION SELECT x FROM b ORDER BY x",
		Want: [][]any{
			{int64(1)},
			{int64(2)},
		},
	},
	{
		Name: "union_with_limit",
		Setup: []string{
			"CREATE TABLE a (x INTEGER PRIMARY KEY)",
			"INSERT INTO a VALUES (1), (2)",
			"CREATE TABLE b (x INTEGER PRIMARY KEY)",
			"INSERT INTO b VALUES (3), (4)",
		},
		Query: "SELECT x FROM a UNION ALL SELECT x FROM b ORDER BY x LIMIT 2",
		Want: [][]any{
			{int64(1)},
			{int64(2)},
		},
	},
	// Core scalar functions: session counters (REQ000385/394/411)
	{
		Name: "changes_function",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT)",
			"INSERT INTO t VALUES (1, 'a')",
		},
		Query: "SELECT CHANGES()",
		Want:  [][]any{{int64(1)}},
	},
	{
		Name: "total_changes_function",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT)",
			"INSERT INTO t VALUES (1, 'a')",
			"UPDATE t SET val = 'b' WHERE id = 1",
			"DELETE FROM t WHERE id = 1",
		},
		Query: "SELECT TOTAL_CHANGES()",
		Want:  [][]any{{int64(3)}},
	},
	// Core scalar functions: string (REQ000386-392)
	{
		Name:  "char_function",
		Query: "SELECT CHAR(65, 66, 67)",
		Want:  [][]any{{"ABC"}},
	},
	{
		Name:  "concat_function",
		Query: "SELECT CONCAT('Hello', ' ', 'World')",
		Want:  [][]any{{"Hello World"}},
	},
	{
		Name:  "concat_ws_function",
		Query: "SELECT CONCAT_WS(',', 'a', 'b', 'c')",
		Want:  [][]any{{"a,b,c"}},
	},
	{
		Name:  "format_function",
		Query: "SELECT FORMAT('%s %s', 'Hello', 'World')",
		Want:  [][]any{{"Hello World"}},
	},
	{
		Name:  "ltrim_function",
		Query: "SELECT LTRIM('  hello  ')",
		Want:  [][]any{{"hello  "}},
	},
	{
		Name:  "rtrim_function",
		Query: "SELECT RTRIM('  hello  ')",
		Want:  [][]any{{"  hello"}},
	},
	{
		Name:  "replace_function",
		Query: "SELECT REPLACE('aaa', 'a', 'b')",
		Want:  [][]any{{"bbb"}},
	},
	{
		Name:  "quote_function",
		Query: "SELECT QUOTE('it''s')",
		Want:  [][]any{{"'it''s'"}},
	},
	// Core scalar functions: type/info (REQ000395-400)
	{
		Name:  "typeof_integer",
		Query: "SELECT TYPEOF(42)",
		Want:  [][]any{{"integer"}},
	},
	{
		Name:  "typeof_text",
		Query: "SELECT TYPEOF('text')",
		Want:  [][]any{{"text"}},
	},
	{
		Name:  "typeof_null",
		Query: "SELECT TYPEOF(NULL)",
		Want:  [][]any{{"null"}},
	},
	{
		Name:  "octet_length_utf8",
		Query: "SELECT OCTET_LENGTH('café')",
		Want:  [][]any{{int64(5)}},
	},
	{
		Name:  "unicode_function",
		Query: "SELECT UNICODE('A')",
		Want:  [][]any{{int64(65)}},
	},
	// Core scalar functions: conditional (REQ000401)
	{
		Name:  "iif_true",
		Query: "SELECT IIF(1 > 0, 'yes', 'no')",
		Want:  [][]any{{"yes"}},
	},
	{
		Name:  "iif_false",
		Query: "SELECT IIF(0, 'yes', 'no')",
		Want:  [][]any{{"no"}},
	},
	// Core scalar functions: search (REQ000412)
	{
		Name:  "instr_found",
		Query: "SELECT INSTR('hello world', 'world')",
		Want:  [][]any{{int64(7)}},
	},
	{
		Name:  "instr_not_found",
		Query: "SELECT INSTR('hello', 'x')",
		Want:  [][]any{{int64(0)}},
	},
	// Core scalar functions: numeric (REQ000402-404,407-408)
	{
		Name:  "sign_negative",
		Query: "SELECT SIGN(-5)",
		Want:  [][]any{{int64(-1)}},
	},
	{
		Name:  "sign_zero",
		Query: "SELECT SIGN(0)",
		Want:  [][]any{{int64(0)}},
	},
	{
		Name:  "sign_positive",
		Query: "SELECT SIGN(42)",
		Want:  [][]any{{int64(1)}},
	},
	{
		Name:  "zeroblob_content",
		Query: "SELECT ZEROBLOB(8)",
		Want:  [][]any{{[]byte{0, 0, 0, 0, 0, 0, 0, 0}}},
	},
}
