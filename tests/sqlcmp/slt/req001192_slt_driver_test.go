//go:build slt_corpus

package slt

import (
    "context"
    "testing"
)

func TestREQ001192_Select5WithSltDriver(t *testing.T) {
    driver := NewRazorDriver()
    ctx := context.Background()
    if err := driver.Connect(ctx); err != nil {
        t.Fatalf("Connect: %v", err)
    }
    defer driver.Close(ctx)

    stmt := func(sql string) {
        if err := driver.Exec(ctx, sql); err != nil {
            t.Fatalf("exec[%s]: %v", sql, err)
        }
    }

    // Setup tables from select5.test
    stmt("CREATE TABLE t41 (a41 INTEGER PRIMARY KEY, b41 INTEGER, x41 VARCHAR(40))")
    stmt("INSERT INTO t41 VALUES(1,6,'table t41 row 1')")
    stmt("INSERT INTO t41 VALUES(2,1,'table t41 row 2')")
    stmt("INSERT INTO t41 VALUES(3,3,'table t41 row 3')")
    stmt("INSERT INTO t41 VALUES(4,2,'table t41 row 4')")
    stmt("INSERT INTO t41 VALUES(5,9,'table t41 row 5')")
    stmt("INSERT INTO t41 VALUES(6,5,'table t41 row 6')")
    stmt("INSERT INTO t41 VALUES(7,8,'table t41 row 7')")
    stmt("INSERT INTO t41 VALUES(8,7,'table t41 row 8')")
    stmt("INSERT INTO t41 VALUES(9,10,'table t41 row 9')")
    stmt("INSERT INTO t41 VALUES(10,4,'table t41 row 10')")
    stmt("CREATE TABLE t54 (a54 INTEGER PRIMARY KEY, b54 INTEGER, x54 VARCHAR(40))")
    stmt("INSERT INTO t54 VALUES(1,6,'table t54 row 1')")
    stmt("INSERT INTO t54 VALUES(2,7,'table t54 row 2')")
    stmt("INSERT INTO t54 VALUES(3,2,'table t54 row 3')")
    stmt("INSERT INTO t54 VALUES(4,9,'table t54 row 4')")
    stmt("INSERT INTO t54 VALUES(5,8,'table t54 row 5')")
    stmt("INSERT INTO t54 VALUES(6,5,'table t54 row 6')")
    stmt("INSERT INTO t54 VALUES(7,4,'table t54 row 7')")
    stmt("INSERT INTO t54 VALUES(8,1,'table t54 row 8')")
    stmt("INSERT INTO t54 VALUES(9,10,'table t54 row 9')")
    stmt("INSERT INTO t54 VALUES(10,3,'table t54 row 10')")
    stmt("CREATE TABLE t27 (a27 INTEGER PRIMARY KEY, b27 INTEGER, x27 VARCHAR(40))")
    stmt("INSERT INTO t27 VALUES(1,8,'table t27 row 1')")
    stmt("INSERT INTO t27 VALUES(2,3,'table t27 row 2')")
    stmt("INSERT INTO t27 VALUES(3,6,'table t27 row 3')")
    stmt("INSERT INTO t27 VALUES(4,7,'table t27 row 4')")
    stmt("INSERT INTO t27 VALUES(5,4,'table t27 row 5')")
    stmt("INSERT INTO t27 VALUES(6,2,'table t27 row 6')")
    stmt("INSERT INTO t27 VALUES(7,10,'table t27 row 7')")
    stmt("INSERT INTO t27 VALUES(8,9,'table t27 row 8')")
    stmt("INSERT INTO t27 VALUES(9,5,'table t27 row 9')")
    stmt("INSERT INTO t27 VALUES(10,1,'table t27 row 10')")
    stmt("CREATE TABLE t32 (a32 INTEGER PRIMARY KEY, b32 INTEGER, x32 VARCHAR(40))")
    stmt("INSERT INTO t32 VALUES(1,10,'table t32 row 1')")
    stmt("INSERT INTO t32 VALUES(2,2,'table t32 row 2')")
    stmt("INSERT INTO t32 VALUES(3,6,'table t32 row 3')")
    stmt("INSERT INTO t32 VALUES(4,3,'table t32 row 4')")
    stmt("INSERT INTO t32 VALUES(5,4,'table t32 row 5')")
    stmt("INSERT INTO t32 VALUES(6,1,'table t32 row 6')")
    stmt("INSERT INTO t32 VALUES(7,7,'table t32 row 7')")
    stmt("INSERT INTO t32 VALUES(8,5,'table t32 row 8')")
    stmt("INSERT INTO t32 VALUES(9,9,'table t32 row 9')")
    stmt("INSERT INTO t32 VALUES(10,8,'table t32 row 10')")

    query := "SELECT x27,x41,x54,x32 FROM t41,t54,t27,t32 WHERE a54=b41 AND b27=a32 AND b32=a41 AND a54=2"

    rs, err := driver.Query(ctx, query)
    if err != nil {
        t.Fatalf("query: %v", err)
    }

    t.Logf("got %d rows", len(rs.Rows))
    for i, row := range rs.Rows {
        t.Logf("row %d: %v", i, row)
    }

    if len(rs.Rows) != 1 {
        t.Errorf("expected 1 row, got %d", len(rs.Rows))
    }
}
