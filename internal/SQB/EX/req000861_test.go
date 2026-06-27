package EX

import (
	"context"
	"fmt"
	"testing"
)

// REQ000861: 4-table cross join with t1 IN filter + cross-table OR.
// Regression: nextBlock right-side prefixing was unconditional,
// producing double-prefixed columns like "t4.t3.a" instead of
// "t3.a". The compiled OR filter's suffix fallback then matched
// the wrong column (t1.d instead of t4.d), and when t1 was filtered
// the different distribution exposed the bug. See join.go:802.
func TestREQ000861_CrossTableORWithFilter(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()

	for ti := 1; ti <= 4; ti++ {
		setup := fmt.Sprintf("CREATE TABLE t%d (a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)", ti)
		if _, err := e.Exec(ctx, setup); err != nil {
			t.Fatalf("setup: %v", err)
		}
		for j := 0; j < 10; j++ {
			v := 100 + j
			insert := fmt.Sprintf("INSERT INTO t%d VALUES (%d, %d, %d, %d, %d)", ti, v, v, v, v, v)
			if _, err := e.Exec(ctx, insert); err != nil {
				t.Fatalf("insert: %v", err)
			}
		}
	}

	tests := []struct {
		name   string
		sql    string
		expect int
	}{
		{"no filter cross", "SELECT count(*) FROM t1,t2,t3,t4", 10000},
		{"t1 IN only", "SELECT count(*) FROM t1,t2,t3,t4 WHERE t1.a IN (101,103)", 2000},
		{"t2>105 only", "SELECT count(*) FROM t1,t2,t3,t4 WHERE t2.b>105", 4000},
		{"t4<103 only", "SELECT count(*) FROM t1,t2,t3,t4 WHERE t4.d<103", 3000},
		{"t2>105 AND t4<103", "SELECT count(*) FROM t1,t2,t3,t4 WHERE t2.b>105 AND t4.d<103", 1200},
		{"t2>105 OR t4<103", "SELECT count(*) FROM t1,t2,t3,t4 WHERE t2.b>105 OR t4.d<103", 5800},
		{"t1 IN AND (t2>105 OR t4<103)", "SELECT count(*) FROM t1,t2,t3,t4 WHERE t1.a IN (101,103) AND (t2.b>105 OR t4.d<103)", 1160},
		{"t1 IN AND t2>105", "SELECT count(*) FROM t1,t2,t3,t4 WHERE t1.a IN (101,103) AND t2.b>105", 800},
		{"t1 IN AND t4<103", "SELECT count(*) FROM t1,t2,t3,t4 WHERE t1.a IN (101,103) AND t4.d<103", 600},
		{"t1 IN AND (t2>105 AND t4<103)", "SELECT count(*) FROM t1,t2,t3,t4 WHERE t1.a IN (101,103) AND t2.b>105 AND t4.d<103", 240},
	}
	for _, tc := range tests {
		rows, err := e.QueryAll(ctx, tc.sql)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		got := int(rows[0].Data[0].I64)
		if got != tc.expect {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.expect)
		}
	}
}
