//go:build !slt_corpus

package EX

import (
	"context"
	"fmt"
	"testing"
)

func TestSLTDebugCorrelated(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE t1(a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 30; i++ {
		if _, err := ex.Exec(ctx, fmt.Sprintf("INSERT INTO t1 VALUES(%d,%d,%d,%d,%d)", i, i*2, i*3, i*4, i*5)); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		sql  string
		want int
	}{
		{"no-subq", "SELECT a FROM t1 WHERE a > 10 ORDER BY 1 LIMIT 5", 5},
		{"EXISTS non-corr", "SELECT a FROM t1 WHERE EXISTS(SELECT 1) ORDER BY 1 LIMIT 5", 5},
		{"EXISTS correlated", "SELECT a FROM t1 AS t WHERE EXISTS(SELECT 1 FROM t1 AS x WHERE x.b > t.b) ORDER BY 1 LIMIT 5", 5},
		{"scalar non-corr", "SELECT (SELECT count(*) FROM t1) FROM t1 ORDER BY 1 LIMIT 5", 5},
		{"scalar correlated", "SELECT (SELECT count(*) FROM t1 AS x WHERE x.b > t1.b) FROM t1 AS t1 ORDER BY 1 LIMIT 5", 5},
		{"NOT EXISTS correlated", "SELECT a FROM t1 AS t WHERE NOT EXISTS(SELECT 1 FROM t1 AS x WHERE x.b > t.b) ORDER BY 1", 1},
		{"expr arithmetic", "SELECT a+b*2+c*3+d*4+e*5, (a+b+c+d+e)/5 FROM t1 ORDER BY 1,2 LIMIT 5", 5},
		{"CASE expr", "SELECT CASE WHEN a<b-3 THEN 111 WHEN a<=b THEN 222 WHEN a<b+3 THEN 333 ELSE 444 END FROM t1 ORDER BY 1 LIMIT 5", 5},
		{"abs func", "SELECT abs(b-c) FROM t1 ORDER BY 1 LIMIT 5", 5},
		{"NOT BETWEEN", "SELECT a FROM t1 WHERE d NOT BETWEEN 110 AND 150 ORDER BY 1 LIMIT 5", 5},
		{"complex WHERE", "SELECT a FROM t1 WHERE (e>c OR e<d) AND d>e ORDER BY 1 LIMIT 5", 5},
		{"ORDER BY expr", "SELECT a+b*2+c*3 FROM t1 ORDER BY 1 LIMIT 5", 5},
		{"repeated query", "SELECT a FROM t1 WHERE a > 10 ORDER BY 1 LIMIT 5", 5},
	}

	for _, tt := range tests {
		rows, err := ex.QueryAll(ctx, tt.sql)
		if err != nil {
			t.Errorf("%s: err=%v", tt.name, err)
			continue
		}
		if len(rows) != tt.want {
			t.Errorf("%s: got %d rows, want %d", tt.name, len(rows), tt.want)
			if len(rows) > 0 && len(rows) <= 3 {
				for _, r := range rows {
					t.Logf("  %v", r.Data)
				}
			}
		}
	}
}
