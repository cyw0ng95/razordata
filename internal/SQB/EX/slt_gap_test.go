package EX

import (
	"context"
	"strings"
	"testing"
)

// TestSLT_GapSurvey runs a small sample of common SQLLogicTest
// patterns and reports which features RazorDB does not support.
// REQ000335+: gap survey.
func TestSLT_GapSurvey(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"a", "b", "c", "d", "e"})

	ctx := context.Background()
	// Setup data
	ex.Exec(ctx, "INSERT INTO t1 VALUES (104, 100, 102, 101, 103)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (107, 105, 106, 108, 109)")

	// Each probe: try as Exec, fall back to Query
	probes := []struct {
		name  string
		sql   string
		isSel bool
		notes string
	}{
		{"correlated_subquery",
			"SELECT (SELECT count(*) FROM t1 AS x WHERE x.b<t1.b) FROM t1",
			true, "correlated subqueries in SELECT list"},
		{"exists_subquery",
			"SELECT * FROM t1 WHERE EXISTS(SELECT 1 FROM t1 AS x WHERE x.b<t1.b)",
			true, "EXISTS subquery in WHERE"},
		{"scalar_subquery_in_where",
			"SELECT * FROM t1 WHERE a > (SELECT avg(a) FROM t1)",
			true, "scalar subquery in WHERE"},
		{"case_when",
			"SELECT CASE WHEN c>100 THEN a*2 ELSE b*10 END FROM t1",
			true, "CASE WHEN expression"},
		{"case_simple",
			"SELECT CASE a+1 WHEN b THEN 111 WHEN c THEN 222 ELSE 333 END FROM t1",
			true, "simple CASE"},
		{"between_and",
			"SELECT * FROM t1 WHERE c BETWEEN b-2 AND d+2",
			true, "BETWEEN x AND y expression"},
		{"not_between",
			"SELECT * FROM t1 WHERE d NOT BETWEEN 110 AND 150",
			true, "NOT BETWEEN"},
		{"expression_in_select",
			"SELECT a+b*2+c*3+d*4+e*5 FROM t1",
			true, "arithmetic in select"},
		{"count_star",
			"SELECT count(*) FROM t1",
			true, "count(*) aggregate"},
		{"min_max",
			"SELECT min(a), max(b) FROM t1",
			true, "min/max aggregates"},
		{"having",
			"SELECT a, count(*) FROM t1 GROUP BY a HAVING count(*) > 0",
			true, "HAVING clause"},
		{"view_create",
			"CREATE VIEW v1 AS SELECT a FROM t1",
			false, "CREATE VIEW"},
		{"trigger_create",
			"CREATE TRIGGER t AFTER INSERT ON t1 BEGIN SELECT 1; END",
			false, "CREATE TRIGGER"},
		{"cast_int",
			"SELECT CAST(a AS TEXT) FROM t1",
			true, "CAST(x AS type)"},
		{"is_null",
			"SELECT * FROM t1 WHERE a IS NULL",
			true, "IS NULL"},
		{"is_not_null",
			"SELECT * FROM t1 WHERE a IS NOT NULL",
			true, "IS NOT NULL"},
		{"in_subquery",
			"SELECT * FROM t1 WHERE a IN (SELECT b FROM t1)",
			true, "IN (subquery)"},
		{"order_by_desc",
			"SELECT a FROM t1 ORDER BY a DESC",
			true, "ORDER BY DESC"},
		{"limit_offset",
			"SELECT a FROM t1 LIMIT 5 OFFSET 2",
			true, "LIMIT OFFSET"},
		{"group_concat",
			"SELECT group_concat(a) FROM t1",
			true, "group_concat aggregate"},
		{"union_all",
			"SELECT a FROM t1 UNION ALL SELECT b FROM t1",
			true, "UNION ALL"},
		{"negative_literal",
			"SELECT -a FROM t1",
			true, "unary minus on column"},
		{"coalesce",
			"SELECT COALESCE(a, b) FROM t1",
			true, "COALESCE function"},
		{"nullif",
			"SELECT NULLIF(a, b) FROM t1",
			true, "NULLIF function"},
		{"recursive_cte",
			"WITH RECURSIVE cnt(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x<5) SELECT x FROM cnt",
			true, "recursive CTE"},
		{"window_function",
			"SELECT a, rank() OVER (ORDER BY a) FROM t1",
			true, "window function RANK"},
		{"like_pattern",
			"SELECT * FROM t1 WHERE CAST(a AS TEXT) LIKE '1%'",
			true, "LIKE pattern"},
		{"in_list",
			"SELECT * FROM t1 WHERE a IN (1, 2, 3)",
			true, "IN (value list)"},
		{"not_in_list",
			"SELECT * FROM t1 WHERE a NOT IN (1, 2, 3)",
			true, "NOT IN (value list)"},
		{"aggregate_distinct",
			"SELECT count(DISTINCT a) FROM t1",
			true, "count(DISTINCT) aggregate"},
		{"sum_aggregate",
			"SELECT sum(a) FROM t1",
			true, "sum aggregate"},
		{"avg_aggregate",
			"SELECT avg(a) FROM t1",
			true, "avg aggregate"},
		{"substr_function",
			"SELECT substr('hello', 1, 3)",
			true, "substr function"},
		{"trim_function",
			"SELECT trim('  hello  ')",
			true, "trim function"},
		{"abs_function",
			"SELECT abs(-5)",
			true, "abs function"},
		{"typeof_function",
			"SELECT typeof(42)",
			true, "typeof function"},
		{"create_index",
			"CREATE INDEX i1 ON t1(a)",
			false, "CREATE INDEX"},
		{"drop_index",
			"DROP INDEX i1",
			false, "DROP INDEX"},
		{"explain",
			"EXPLAIN SELECT * FROM t1",
			true, "EXPLAIN statement"},
		{"vacuum",
			"VACUUM",
			false, "VACUUM statement"},
		{"analyze",
			"ANALYZE t1",
			false, "ANALYZE statement"},
		{"alter_table_add",
			"ALTER TABLE t1 ADD COLUMN f INTEGER",
			false, "ALTER TABLE ADD COLUMN"},
	}

	pass := 0
	fail := 0
	var fails []string
	for _, p := range probes {
		var err error
		if p.isSel {
			_, err = ex.Query(ctx, p.sql)
		} else {
			_, err = ex.Exec(ctx, p.sql)
		}
		if err != nil {
			fail++
			errStr := err.Error()
			if len(errStr) > 100 {
				errStr = errStr[:100] + "..."
			}
			fails = append(fails, p.name+": "+p.notes+" — "+errStr)
		} else {
			pass++
		}
	}
	t.Logf("Gap survey: pass=%d fail=%d total=%d", pass, fail, len(probes))
	for _, f := range fails {
		t.Logf("  FAIL %s", f)
	}
}

// TestSLT_CommonPatterns spot-checks the most common SQL patterns
// to ensure they don't regress.
func TestSLT_CommonPatterns(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"a", "b", "c"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 100, 'x')")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2, 200, 'y')")

	// Each should not panic with syntax error
	tests := []string{
		"SELECT * FROM t1",
		"SELECT a, b FROM t1",
		"SELECT a + b FROM t1",
		"SELECT * FROM t1 WHERE a = 1",
		"SELECT * FROM t1 ORDER BY a",
		"SELECT count(*) FROM t1",
	}
	for _, sql := range tests {
		_, err := ex.Query(ctx, sql)
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "syntax error") {
				t.Errorf("Unexpected syntax error for %q: %v", sql, err)
			}
		}
	}
}
