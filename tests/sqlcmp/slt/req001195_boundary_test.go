package slt

import (
    "context"
    "testing"
)

func TestREQ001195_ExactBoundary(t *testing.T) {
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

    stmt("CREATE TABLE tab2 (col0 INTEGER, col1 INTEGER, col2 INTEGER)")
    stmt("INSERT INTO tab2 VALUES(64,77,40)")
    stmt("INSERT INTO tab2 VALUES(75,67,58)")
    stmt("INSERT INTO tab2 VALUES(46,51,23)")

    tests := []struct {
        sql  string
        want int
    }{
        // Basic
        {"SELECT * FROM tab2", 3},
        {"SELECT DISTINCT * FROM tab2", 3},
        {"SELECT DISTINCT col0, col1, col2 FROM tab2", 3},
        // Implicit alias on single table
        {"SELECT * FROM tab2 c0", 3},
        {"SELECT DISTINCT * FROM tab2 c0", 3},
        {"SELECT DISTINCT c0.col0, c0.col1, c0.col2 FROM tab2 c0", 3},
        {"SELECT DISTINCT col0, col1, col2 FROM tab2 c0", 3},
        // Explicit AS alias on single table
        {"SELECT * FROM tab2 AS c0", 3},
		{"SELECT DISTINCT * FROM tab2 AS c0", 3},
		{"SELECT DISTINCT c0.col0, c0.col1, c0.col2 FROM tab2 AS c0", 3},
		{"SELECT DISTINCT col0, col1, col2 FROM tab2 AS c0", 3},
        // Multi-table implicit
        {"SELECT * FROM tab2, tab2 c0", 9},
        {"SELECT DISTINCT * FROM tab2, tab2 c0", 9},
        {"SELECT DISTINCT c0.col0, c0.col1, c0.col2 FROM tab2, tab2 c0", 3},
        // Multi-table explicit AS
        {"SELECT * FROM tab2, tab2 AS c0", 9},
		{"SELECT DISTINCT * FROM tab2, tab2 AS c0", 9},
		{"SELECT DISTINCT * FROM tab2 AS c0, tab2", 9},
		{"SELECT DISTINCT * FROM tab2 AS c0, tab2 AS c1", 9},
		// Without DISTINCT but with AS
        // Without DISTINCT but with AS
        {"SELECT c0.col0, c0.col1, c0.col2 FROM tab2 AS c0", 3},
        // Aggregate with AS
        {"SELECT COUNT(*) FROM tab2 AS c0", 1},
        {"SELECT COUNT(DISTINCT c0.col0) FROM tab2 AS c0", 1},
    }

    for _, tt := range tests {
        t.Run(tt.sql, func(t *testing.T) {
            rs, err := driver.Query(ctx, tt.sql)
            if err != nil {
                t.Fatalf("query error: %v", err)
            }
            got := len(rs.Rows)
            if got != tt.want {
                t.Errorf("rows mismatch: got %d, want %d", got, tt.want)
            } else {
                t.Logf("OK: %s → %d rows", tt.sql, got)
            }
        })
    }
}
