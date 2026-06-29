package dual

import (
	"context"
	"testing"
)

// razorOnlyCases are executed on Razordata only, without sqlite
// comparison.  Used for Razordata-specific extensions or behavior
// that intentionally diverges from sqlite.
var razorOnlyCases = []dualCase{
	{
		Name:  "empty_in_list_null",
		Query: "SELECT NULL IN ()",
		Want:  [][]any{{int64(0)}}, // Razordata: false
	},
	{
		Name:  "empty_in_list_int",
		Query: "SELECT 1 IN ()",
		Want:  [][]any{{int64(0)}}, // Razordata: false
	},
	// Join elimination cases from EX/req000799_test.go.
	// Razordata eliminates unreferenced tables; sqlite does not.
	{
		Name: "join_elim_unqualified",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, a INTEGER)",
			"INSERT INTO t1 VALUES (1, 1), (2, 2), (3, 3)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, b INTEGER)",
			"INSERT INTO t2 VALUES (1, 10), (2, 20), (3, 30)",
			"CREATE TABLE t3 (id INTEGER PRIMARY KEY, c INTEGER)",
			"INSERT INTO t3 VALUES (1, 100), (2, 200), (3, 300)",
		},
		Query: "SELECT a FROM t1, t2, t3",
		// Unqualified `a` can't eliminate — all 3 tables kept.
		Want: [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}, {int64(3)},
			{int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}, {int64(3)},
			{int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}, {int64(3)}},
	},
	{
		Name: "join_elim_qualified",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, a INTEGER)",
			"INSERT INTO t1 VALUES (1, 1), (2, 2), (3, 3)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, b INTEGER)",
			"INSERT INTO t2 VALUES (1, 10), (2, 20), (3, 30)",
			"CREATE TABLE t3 (id INTEGER PRIMARY KEY, c INTEGER)",
			"INSERT INTO t3 VALUES (1, 100), (2, 200), (3, 300)",
		},
		Query: "SELECT t1.a FROM t1, t2, t3",
		// Qualified t1.a — t2, t3 eliminated. 3 rows.
		Want: [][]any{{int64(1)}, {int64(2)}, {int64(3)}},
	},
	{
		Name: "join_elim_where",
		Setup: []string{
			"CREATE TABLE t1 (id INTEGER PRIMARY KEY, a INTEGER)",
			"INSERT INTO t1 VALUES (1, 1), (2, 2), (3, 3)",
			"CREATE TABLE t2 (id INTEGER PRIMARY KEY, b INTEGER)",
			"INSERT INTO t2 VALUES (1, 10), (2, 20), (3, 30)",
			"CREATE TABLE t3 (id INTEGER PRIMARY KEY, c INTEGER)",
			"INSERT INTO t3 VALUES (1, 100), (2, 200), (3, 300)",
		},
		Query: "SELECT t1.a FROM t1, t2, t3 WHERE t2.b > 15",
		// t1 in SELECT, t2 in WHERE, t3 unreferenced → t3 eliminated.
		// 3 * 2 = 6 rows (t2.b > 15: 20, 30)
		Want: [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}},
	},
}

func TestRazorOnly_AllCases(t *testing.T) {
	ctx := context.Background()
	for _, c := range razorOnlyCases {
		t.Run(c.Name, func(t *testing.T) {
			v, diff := RunRazorOnly(ctx, c)
			if v != VerdictPassed {
				t.Logf("verdict=%s diff=%s", v, diff)
				t.Errorf("case failed")
			}
		})
	}
}
