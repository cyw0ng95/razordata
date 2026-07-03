package EX

import (
	"context"
	"testing"
)

// REQ001192: Reproduce the first failing query from select5.test.
func TestReq001192_4TableJoin(t *testing.T) {
	ResetForTest(t)
	ctx := context.Background()
	ex := NewExecutor()

	// Create tables matching select5.test schema
	for _, ddl := range []string{
		"CREATE TABLE t29(a29 INTEGER PRIMARY KEY, b29 INTEGER, x29 VARCHAR(40))",
		"CREATE TABLE t31(a31 INTEGER PRIMARY KEY, b31 INTEGER, x31 VARCHAR(40))",
		"CREATE TABLE t51(a51 INTEGER PRIMARY KEY, b51 INTEGER, x51 VARCHAR(40))",
		"CREATE TABLE t55(a55 INTEGER PRIMARY KEY, b55 INTEGER, x55 VARCHAR(40))",
	} {
		if _, err := ex.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}

	// Insert data from select5.test
	for _, ins := range []string{
		// t29
		"INSERT INTO t29 VALUES(1,4,'t29r1')", "INSERT INTO t29 VALUES(2,2,'t29r2')",
		"INSERT INTO t29 VALUES(3,9,'t29r3')", "INSERT INTO t29 VALUES(4,8,'t29r4')",
		"INSERT INTO t29 VALUES(5,10,'t29r5')", "INSERT INTO t29 VALUES(6,3,'t29r6')",
		"INSERT INTO t29 VALUES(7,7,'t29r7')", "INSERT INTO t29 VALUES(8,6,'t29r8')",
		"INSERT INTO t29 VALUES(9,1,'t29r9')", "INSERT INTO t29 VALUES(10,5,'t29r10')",
		// t31 (correct data from select5.test)
		"INSERT INTO t31 VALUES(1,1,'t31r1')", "INSERT INTO t31 VALUES(2,6,'t31r2')",
		"INSERT INTO t31 VALUES(3,4,'t31r3')", "INSERT INTO t31 VALUES(4,8,'t31r4')",
		"INSERT INTO t31 VALUES(5,2,'t31r5')", "INSERT INTO t31 VALUES(6,9,'t31r6')",
		"INSERT INTO t31 VALUES(7,7,'t31r7')", "INSERT INTO t31 VALUES(8,3,'t31r8')",
		"INSERT INTO t31 VALUES(9,5,'t31r9')", "INSERT INTO t31 VALUES(10,10,'t31r10')",
		// t51
		"INSERT INTO t51 VALUES(1,5,'t51r1')", "INSERT INTO t51 VALUES(2,3,'t51r2')",
		"INSERT INTO t51 VALUES(3,10,'t51r3')", "INSERT INTO t51 VALUES(4,7,'t51r4')",
		"INSERT INTO t51 VALUES(5,6,'t51r5')", "INSERT INTO t51 VALUES(6,2,'t51r6')",
		"INSERT INTO t51 VALUES(7,9,'t51r7')", "INSERT INTO t51 VALUES(8,4,'t51r8')",
		"INSERT INTO t51 VALUES(9,1,'t51r9')", "INSERT INTO t51 VALUES(10,8,'t51r10')",
		// t55 (correct data from select5.test)
		"INSERT INTO t55 VALUES(1,1,'t55r1')", "INSERT INTO t55 VALUES(2,3,'t55r2')",
		"INSERT INTO t55 VALUES(3,7,'t55r3')", "INSERT INTO t55 VALUES(4,9,'t55r4')",
		"INSERT INTO t55 VALUES(5,5,'t55r5')", "INSERT INTO t55 VALUES(6,4,'t55r6')",
		"INSERT INTO t55 VALUES(7,10,'t55r7')", "INSERT INTO t55 VALUES(8,8,'t55r8')",
		"INSERT INTO t55 VALUES(9,6,'t55r9')", "INSERT INTO t55 VALUES(10,2,'t55r10')",
	} {
		if _, err := ex.Exec(ctx, ins); err != nil {
			t.Fatal(err)
		}
	}

	// First failing query from REQ001192
	query := `SELECT x29,x31,x51,x55 FROM t51,t29,t31,t55 WHERE a51=b31 AND a29=6 AND a29=b51 AND b55=a31`
	rows, err := ex.QueryAll(ctx, query)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	t.Logf("got %d rows", len(rows))
	for i, r := range rows {
		t.Logf("row %d: %v", i, r.Data)
	}

	// Verify with correct data:
	// t29: a29=6 -> x29='table t29 row 6'
	// a29=b51 -> b51=6 -> t51: a51 where b51=6 -> a51=5 -> x51='table t51 row 5'
	// a51=b31 -> b31=5 -> t31: a31 where b31=5 -> a31=9 -> x31='table t31 row 9'
	// b55=a31 -> b55=9 -> t55: a55 where b55=9 -> a55=4 -> x55='table t55 row 4'
	// Expected: 1 row with ('table t29 row 6', 'table t31 row 9', 'table t51 row 5', 'table t55 row 4')
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
}
