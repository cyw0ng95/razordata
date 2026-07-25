//go:build slt_corpus

package slt

import (
	"context"
	"testing"
	"time"
)

func TestREQ002005_compareAliasVsNoAlias(t *testing.T) {
	driver := NewRazorDriver()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer driver.Close(context.Background())

	stmts := []string{
		"CREATE TABLE tab2(col0 INTEGER, col1 INTEGER, col2 INTEGER)",
		"INSERT INTO tab2 VALUES(64,77,40)",
		"INSERT INTO tab2 VALUES(75,67,58)",
		"INSERT INTO tab2 VALUES(46,51,23)",
	}
	for _, s := range stmts {
		if err := driver.Exec(ctx, s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}

	tests := []struct {
		q    string
		want int64
	}{
		{"SELECT MAX(col0) FROM tab2 WHERE col2 < col1*col0", 75},
		{"SELECT MAX(col0) FROM tab2 WHERE NOT col2 >= col1*col0", 75},
		{"SELECT MAX(col0) FROM tab2 WHERE col1-col0 > 0", 64},
		{"SELECT MAX(col0) FROM tab2 AS t WHERE col2 < col1*col0", 75},
		{"SELECT MAX(col0) FROM tab2 AS t WHERE NOT col2 >= col1*col0", 75},
		{"SELECT MAX(col0) FROM tab2 AS t WHERE col1-col0 > 0", 64},
	}

	for _, tt := range tests {
		rs, err := driver.Query(ctx, tt.q)
		if err != nil {
			t.Fatalf("query %q: %v", tt.q, err)
		}
		val := int64(-1)
		if len(rs.Rows) > 0 && len(rs.Rows[0]) > 0 {
			val = rs.Rows[0][0].Int
		}
		status := "OK"
		if val != tt.want {
			status = "WRONG"
		}
		t.Logf("[%s] %s -> %d (want %d)", status, tt.q, val, tt.want)
	}
}
