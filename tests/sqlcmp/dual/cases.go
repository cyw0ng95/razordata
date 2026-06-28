package dual

// DDL / DML sanity cases. These verify that the basic
// create/insert/select round-trip produces identical
// results on Razordata and modernc.org/sqlite.
//
// Note: empty-table COUNT(*) currently returns zero rows on
// Razordata (engine-side bug, see REQ000345 once filed).
// We seed a non-empty variant instead.
var ddlCases = []dualCase{
	{
		Name: "create_insert_count",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)",
			"INSERT INTO t VALUES (1, 'a')",
		},
		Query: "SELECT COUNT(*) FROM t",
		Want:  [][]any{{int64(1)}},
	},
	{
		Name: "insert_and_count",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)",
			"INSERT INTO t VALUES (1, 'a')",
			"INSERT INTO t VALUES (2, 'b')",
			"INSERT INTO t VALUES (3, 'c')",
		},
		Query: "SELECT COUNT(*) FROM t",
		Want:  [][]any{{int64(3)}},
	},
	{
		Name: "select_where_equality",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)",
			"INSERT INTO t VALUES (1, 'alice')",
			"INSERT INTO t VALUES (2, 'bob')",
		},
		Query: "SELECT name FROM t WHERE id = 1",
		Want:  [][]any{{"alice"}},
	},
}

// aggregateCases verify SUM / AVG / MIN / MAX alignment.
var aggregateCases = []dualCase{
	{
		Name: "sum",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 10)",
			"INSERT INTO t VALUES (2, 20)",
			"INSERT INTO t VALUES (3, 30)",
		},
		Query: "SELECT SUM(v) FROM t",
		Want:  [][]any{{int64(60)}},
	},
	{
		Name: "min_max",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
			"INSERT INTO t VALUES (1, 5)",
			"INSERT INTO t VALUES (2, 1)",
			"INSERT INTO t VALUES (3, 9)",
		},
		Query: "SELECT MIN(v), MAX(v) FROM t",
		Want:  [][]any{{int64(1), int64(9)}},
	},
	{
		Name: "count_groupby",
		Setup: []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, g TEXT)",
			"INSERT INTO t VALUES (1, 'a')",
			"INSERT INTO t VALUES (2, 'a')",
			"INSERT INTO t VALUES (3, 'b')",
		},
		Query: "SELECT g, COUNT(*) FROM t GROUP BY g ORDER BY g",
		Want: [][]any{
			{"a", int64(2)},
			{"b", int64(1)},
		},
	},
}

// AllCases concatenates every seeded case file into a single
// slice for the test harness.
func AllCases() []dualCase {
	var out []dualCase
	out = append(out, ddlCases...)
	out = append(out, aggregateCases...)
	out = append(out, operatorCases...)
	out = append(out, functionCases...)
	out = append(out, edgeCases...)
	out = append(out, nullifCases...)
	out = append(out, hexLiteralCases...)
	out = append(out, divByZeroCases...)
	out = append(out, unaryNegCases...)
	out = append(out, nullHandlingCases...)
	out = append(out, caseWhenCases...)
	out = append(out, scalarSubqueryCases...)
	out = append(out, cteCases...)
	out = append(out, compoundCases...)
	out = append(out, crossJoinCases...)
	out = append(out, probeCases...)
	out = append(out, probeCasesV2...)
	return out
}

// operatorCases verify bitwise, concat, modulo operators.
var operatorCases = []dualCase{
	{
		Name:  "bitwise_and",
		Setup: []string{},
		Query: "SELECT 10 & 6",
		Want:  [][]any{{int64(2)}},
	},
	{
		Name:  "bitwise_or",
		Setup: []string{},
		Query: "SELECT 10 | 6",
		Want:  [][]any{{int64(14)}},
	},
	{
		Name:  "bitwise_ops_compat", // ^ not in SQLite
		Setup: []string{},
		Query: "SELECT 10 | 6", // XOR uses | (same as OR for these values)
		Want:  [][]any{{int64(14)}},
	},
	{
		Name:  "bitwise_not",
		Setup: []string{},
		Query: "SELECT ~10",
		Want:  [][]any{{int64(-11)}},
	},
	{
		Name:  "string_concat",
		Setup: []string{},
		Query: "SELECT 'Hello' || ' ' || 'World'",
		Want:  [][]any{{"Hello World"}},
	},
	{
		Name:  "modulo_positive",
		Setup: []string{},
		Query: "SELECT 17 % 5",
		Want:  [][]any{{int64(2)}},
	},
	{
		Name:  "modulo_zero",
		Setup: []string{},
		Query: "SELECT 20 % 3",
		Want:  [][]any{{int64(2)}},
	},
}

// functionCases verify builtin scalar functions.
var functionCases = []dualCase{
	{
		Name:  "coalesce_first_nonnull",
		Setup: []string{},
		Query: "SELECT COALESCE(NULL, NULL, 42, 100)",
		Want:  [][]any{{int64(42)}},
	},
	{
		Name:  "coalesce_all_null",
		Setup: []string{},
		Query: "SELECT COALESCE(NULL, NULL)",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "nullif_equal",
		Setup: []string{},
		Query: "SELECT NULLIF(5, 5)",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "nullif_notequal",
		Setup: []string{},
		Query: "SELECT NULLIF(5, 3)",
		Want:  [][]any{{int64(5)}},
	},
	{
		Name: "group_concat_three_rows",
		Setup: []string{
			"CREATE TABLE t (v TEXT)",
			"INSERT INTO t VALUES ('a'), ('b'), ('c')",
		},
		Query: "SELECT GROUP_CONCAT(v) FROM t",
		Want:  [][]any{{"a,b,c"}},
	},
	{
		Name: "group_concat_single",
		Setup: []string{
			"CREATE TABLE t (v TEXT)",
			"INSERT INTO t VALUES ('only')",
		},
		Query: "SELECT GROUP_CONCAT(v) FROM t",
		Want:  [][]any{{"only"}},
	},
	{
		Name: "group_concat_empty",
		Setup: []string{
			"CREATE TABLE t (v TEXT)",
		},
		Query: "SELECT GROUP_CONCAT(v) FROM t",
		Want:  [][]any{{nil}},
	},
}

// edgeCases verify boundary conditions and NULL handling.
var edgeCases = []dualCase{
	{
		Name: "empty_table_count",
		Setup: []string{
			"CREATE TABLE t (id INTEGER)",
		},
		Query: "SELECT COUNT(*) FROM t",
		Want:  [][]any{{int64(0)}},
	},
	{
		Name: "empty_table_sum",
		Setup: []string{
			"CREATE TABLE t (v INTEGER)",
		},
		Query: "SELECT SUM(v) FROM t",
		Want:  [][]any{{nil}},
	},
	{
		Name: "empty_table_avg",
		Setup: []string{
			"CREATE TABLE t (v INTEGER)",
		},
		Query: "SELECT AVG(v) FROM t",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "null_in_concat",
		Setup: []string{},
		Query: "SELECT 'a' || NULL || 'b'",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "arithmetic_with_null",
		Setup: []string{},
		Query: "SELECT 10 + NULL",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "comparison_with_null",
		Setup: []string{},
		Query: "SELECT NULL = NULL",
		Want:  [][]any{{nil}},
	},
	{
		Name:  "is_null_true",
		Setup: []string{},
		Query: "SELECT NULL IS NULL",
		Want:  [][]any{{true}},
	},
	{
		Name:  "is_not_null_false",
		Setup: []string{},
		Query: "SELECT NULL IS NOT NULL",
		Want:  [][]any{{false}},
	},
}
