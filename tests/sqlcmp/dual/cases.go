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
	return out
}
