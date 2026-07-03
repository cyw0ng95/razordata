//go:build slt_corpus

package slt

import (
    "context"
    "testing"
)

func TestREQ001192_ManyTables(t *testing.T) {
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

    // Create 20 tables
    
    stmt("CREATE TABLE t01 (a01 INTEGER PRIMARY KEY, b01 INTEGER, x01 VARCHAR(40))")
    stmt("INSERT INTO t01 VALUES(1,3,'table t01 row 1')")
    stmt("INSERT INTO t01 VALUES(2,4,'table t01 row 2')")
    stmt("INSERT INTO t01 VALUES(3,5,'table t01 row 3')")
    stmt("INSERT INTO t01 VALUES(4,6,'table t01 row 4')")
    stmt("INSERT INTO t01 VALUES(5,7,'table t01 row 5')")


    stmt("CREATE TABLE t02 (a02 INTEGER PRIMARY KEY, b02 INTEGER, x02 VARCHAR(40))")
    stmt("INSERT INTO t02 VALUES(1,5,'table t02 row 1')")
    stmt("INSERT INTO t02 VALUES(2,7,'table t02 row 2')")
    stmt("INSERT INTO t02 VALUES(3,9,'table t02 row 3')")
    stmt("INSERT INTO t02 VALUES(4,1,'table t02 row 4')")
    stmt("INSERT INTO t02 VALUES(5,3,'table t02 row 5')")


    stmt("CREATE TABLE t03 (a03 INTEGER PRIMARY KEY, b03 INTEGER, x03 VARCHAR(40))")
    stmt("INSERT INTO t03 VALUES(1,7,'table t03 row 1')")
    stmt("INSERT INTO t03 VALUES(2,10,'table t03 row 2')")
    stmt("INSERT INTO t03 VALUES(3,3,'table t03 row 3')")
    stmt("INSERT INTO t03 VALUES(4,6,'table t03 row 4')")
    stmt("INSERT INTO t03 VALUES(5,9,'table t03 row 5')")


    stmt("CREATE TABLE t04 (a04 INTEGER PRIMARY KEY, b04 INTEGER, x04 VARCHAR(40))")
    stmt("INSERT INTO t04 VALUES(1,9,'table t04 row 1')")
    stmt("INSERT INTO t04 VALUES(2,3,'table t04 row 2')")
    stmt("INSERT INTO t04 VALUES(3,7,'table t04 row 3')")
    stmt("INSERT INTO t04 VALUES(4,1,'table t04 row 4')")
    stmt("INSERT INTO t04 VALUES(5,5,'table t04 row 5')")


    stmt("CREATE TABLE t05 (a05 INTEGER PRIMARY KEY, b05 INTEGER, x05 VARCHAR(40))")
    stmt("INSERT INTO t05 VALUES(1,1,'table t05 row 1')")
    stmt("INSERT INTO t05 VALUES(2,6,'table t05 row 2')")
    stmt("INSERT INTO t05 VALUES(3,1,'table t05 row 3')")
    stmt("INSERT INTO t05 VALUES(4,6,'table t05 row 4')")
    stmt("INSERT INTO t05 VALUES(5,1,'table t05 row 5')")


    stmt("CREATE TABLE t06 (a06 INTEGER PRIMARY KEY, b06 INTEGER, x06 VARCHAR(40))")
    stmt("INSERT INTO t06 VALUES(1,3,'table t06 row 1')")
    stmt("INSERT INTO t06 VALUES(2,9,'table t06 row 2')")
    stmt("INSERT INTO t06 VALUES(3,5,'table t06 row 3')")
    stmt("INSERT INTO t06 VALUES(4,1,'table t06 row 4')")
    stmt("INSERT INTO t06 VALUES(5,7,'table t06 row 5')")


    stmt("CREATE TABLE t07 (a07 INTEGER PRIMARY KEY, b07 INTEGER, x07 VARCHAR(40))")
    stmt("INSERT INTO t07 VALUES(1,5,'table t07 row 1')")
    stmt("INSERT INTO t07 VALUES(2,2,'table t07 row 2')")
    stmt("INSERT INTO t07 VALUES(3,9,'table t07 row 3')")
    stmt("INSERT INTO t07 VALUES(4,6,'table t07 row 4')")
    stmt("INSERT INTO t07 VALUES(5,3,'table t07 row 5')")


    stmt("CREATE TABLE t08 (a08 INTEGER PRIMARY KEY, b08 INTEGER, x08 VARCHAR(40))")
    stmt("INSERT INTO t08 VALUES(1,7,'table t08 row 1')")
    stmt("INSERT INTO t08 VALUES(2,5,'table t08 row 2')")
    stmt("INSERT INTO t08 VALUES(3,3,'table t08 row 3')")
    stmt("INSERT INTO t08 VALUES(4,1,'table t08 row 4')")
    stmt("INSERT INTO t08 VALUES(5,9,'table t08 row 5')")


    stmt("CREATE TABLE t09 (a09 INTEGER PRIMARY KEY, b09 INTEGER, x09 VARCHAR(40))")
    stmt("INSERT INTO t09 VALUES(1,9,'table t09 row 1')")
    stmt("INSERT INTO t09 VALUES(2,8,'table t09 row 2')")
    stmt("INSERT INTO t09 VALUES(3,7,'table t09 row 3')")
    stmt("INSERT INTO t09 VALUES(4,6,'table t09 row 4')")
    stmt("INSERT INTO t09 VALUES(5,5,'table t09 row 5')")


    stmt("CREATE TABLE t10 (a10 INTEGER PRIMARY KEY, b10 INTEGER, x10 VARCHAR(40))")
    stmt("INSERT INTO t10 VALUES(1,1,'table t10 row 1')")
    stmt("INSERT INTO t10 VALUES(2,1,'table t10 row 2')")
    stmt("INSERT INTO t10 VALUES(3,1,'table t10 row 3')")
    stmt("INSERT INTO t10 VALUES(4,1,'table t10 row 4')")
    stmt("INSERT INTO t10 VALUES(5,1,'table t10 row 5')")


    stmt("CREATE TABLE t11 (a11 INTEGER PRIMARY KEY, b11 INTEGER, x11 VARCHAR(40))")
    stmt("INSERT INTO t11 VALUES(1,3,'table t11 row 1')")
    stmt("INSERT INTO t11 VALUES(2,4,'table t11 row 2')")
    stmt("INSERT INTO t11 VALUES(3,5,'table t11 row 3')")
    stmt("INSERT INTO t11 VALUES(4,6,'table t11 row 4')")
    stmt("INSERT INTO t11 VALUES(5,7,'table t11 row 5')")


    stmt("CREATE TABLE t12 (a12 INTEGER PRIMARY KEY, b12 INTEGER, x12 VARCHAR(40))")
    stmt("INSERT INTO t12 VALUES(1,5,'table t12 row 1')")
    stmt("INSERT INTO t12 VALUES(2,7,'table t12 row 2')")
    stmt("INSERT INTO t12 VALUES(3,9,'table t12 row 3')")
    stmt("INSERT INTO t12 VALUES(4,1,'table t12 row 4')")
    stmt("INSERT INTO t12 VALUES(5,3,'table t12 row 5')")


    stmt("CREATE TABLE t13 (a13 INTEGER PRIMARY KEY, b13 INTEGER, x13 VARCHAR(40))")
    stmt("INSERT INTO t13 VALUES(1,7,'table t13 row 1')")
    stmt("INSERT INTO t13 VALUES(2,10,'table t13 row 2')")
    stmt("INSERT INTO t13 VALUES(3,3,'table t13 row 3')")
    stmt("INSERT INTO t13 VALUES(4,6,'table t13 row 4')")
    stmt("INSERT INTO t13 VALUES(5,9,'table t13 row 5')")


    stmt("CREATE TABLE t14 (a14 INTEGER PRIMARY KEY, b14 INTEGER, x14 VARCHAR(40))")
    stmt("INSERT INTO t14 VALUES(1,9,'table t14 row 1')")
    stmt("INSERT INTO t14 VALUES(2,3,'table t14 row 2')")
    stmt("INSERT INTO t14 VALUES(3,7,'table t14 row 3')")
    stmt("INSERT INTO t14 VALUES(4,1,'table t14 row 4')")
    stmt("INSERT INTO t14 VALUES(5,5,'table t14 row 5')")


    stmt("CREATE TABLE t15 (a15 INTEGER PRIMARY KEY, b15 INTEGER, x15 VARCHAR(40))")
    stmt("INSERT INTO t15 VALUES(1,1,'table t15 row 1')")
    stmt("INSERT INTO t15 VALUES(2,6,'table t15 row 2')")
    stmt("INSERT INTO t15 VALUES(3,1,'table t15 row 3')")
    stmt("INSERT INTO t15 VALUES(4,6,'table t15 row 4')")
    stmt("INSERT INTO t15 VALUES(5,1,'table t15 row 5')")


    stmt("CREATE TABLE t16 (a16 INTEGER PRIMARY KEY, b16 INTEGER, x16 VARCHAR(40))")
    stmt("INSERT INTO t16 VALUES(1,3,'table t16 row 1')")
    stmt("INSERT INTO t16 VALUES(2,9,'table t16 row 2')")
    stmt("INSERT INTO t16 VALUES(3,5,'table t16 row 3')")
    stmt("INSERT INTO t16 VALUES(4,1,'table t16 row 4')")
    stmt("INSERT INTO t16 VALUES(5,7,'table t16 row 5')")


    stmt("CREATE TABLE t17 (a17 INTEGER PRIMARY KEY, b17 INTEGER, x17 VARCHAR(40))")
    stmt("INSERT INTO t17 VALUES(1,5,'table t17 row 1')")
    stmt("INSERT INTO t17 VALUES(2,2,'table t17 row 2')")
    stmt("INSERT INTO t17 VALUES(3,9,'table t17 row 3')")
    stmt("INSERT INTO t17 VALUES(4,6,'table t17 row 4')")
    stmt("INSERT INTO t17 VALUES(5,3,'table t17 row 5')")


    stmt("CREATE TABLE t18 (a18 INTEGER PRIMARY KEY, b18 INTEGER, x18 VARCHAR(40))")
    stmt("INSERT INTO t18 VALUES(1,7,'table t18 row 1')")
    stmt("INSERT INTO t18 VALUES(2,5,'table t18 row 2')")
    stmt("INSERT INTO t18 VALUES(3,3,'table t18 row 3')")
    stmt("INSERT INTO t18 VALUES(4,1,'table t18 row 4')")
    stmt("INSERT INTO t18 VALUES(5,9,'table t18 row 5')")


    stmt("CREATE TABLE t19 (a19 INTEGER PRIMARY KEY, b19 INTEGER, x19 VARCHAR(40))")
    stmt("INSERT INTO t19 VALUES(1,9,'table t19 row 1')")
    stmt("INSERT INTO t19 VALUES(2,8,'table t19 row 2')")
    stmt("INSERT INTO t19 VALUES(3,7,'table t19 row 3')")
    stmt("INSERT INTO t19 VALUES(4,6,'table t19 row 4')")
    stmt("INSERT INTO t19 VALUES(5,5,'table t19 row 5')")


    stmt("CREATE TABLE t20 (a20 INTEGER PRIMARY KEY, b20 INTEGER, x20 VARCHAR(40))")
    stmt("INSERT INTO t20 VALUES(1,1,'table t20 row 1')")
    stmt("INSERT INTO t20 VALUES(2,1,'table t20 row 2')")
    stmt("INSERT INTO t20 VALUES(3,1,'table t20 row 3')")
    stmt("INSERT INTO t20 VALUES(4,1,'table t20 row 4')")
    stmt("INSERT INTO t20 VALUES(5,1,'table t20 row 5')")

    
    // Create the 4 tables used in the query
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
