package EX

import (
	"context"
	"fmt"
	"testing"
)

// TestScalarFunctions_HexIIFMinMax verifies REQ000391 (hex), REQ000392 (iif/if),
// REQ000398 (max), and REQ000399 (min).
func TestScalarFunctions_HexIIFMinMax(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"a", "b", "c"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 2, 3)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (4, 5, 6)")

	probes := []struct {
		sql  string
		want string
	}{
		// REQ000391: hex(X)
		{"SELECT hex('hello') FROM t1 LIMIT 1", "68656C6C6F"},
		{"SELECT hex(255) FROM t1 LIMIT 1", "323535"},
		{"SELECT hex(NULL) FROM t1 LIMIT 1", "<nil>"},
		// REQ000392: iif(B,X,Y) / if() alias
		{"SELECT iif(a=1, 'yes', 'no') FROM t1 LIMIT 1", "yes"},
		{"SELECT iif(a=10, 'yes', 'no') FROM t1 LIMIT 1", "no"},
		{"SELECT iif(NULL, 'yes', 'no') FROM t1 LIMIT 1", "no"},
		{"SELECT if(a=1, 'yes', 'no') FROM t1 LIMIT 1", "yes"},
		{"SELECT if(a=10, 'yes', 'no') FROM t1 LIMIT 1", "no"},
		{"SELECT if(NULL, 'yes', 'no') FROM t1 LIMIT 1", "no"},
		// REQ000398: max(X,Y,...)
		{"SELECT max(a, b, c) FROM t1 LIMIT 1", "3"},
		{"SELECT max(1, NULL, 3) FROM t1 LIMIT 1", "3"},
		{"SELECT max(a) FROM t1 LIMIT 1", "4"},
		// REQ000399: min(X,Y,...)
		{"SELECT min(a, b, c) FROM t1 LIMIT 1", "1"},
		{"SELECT min(5, NULL, 3) FROM t1 LIMIT 1", "3"},
		{"SELECT min(a) FROM t1 LIMIT 1", "1"},
		// max/min with different types (collating)
		{"SELECT max('z', 'a') FROM t1 LIMIT 1", "z"},
		{"SELECT min('z', 'a') FROM t1 LIMIT 1", "a"},
		// all NULL args → NULL
		{"SELECT max(NULL, NULL) FROM t1 LIMIT 1", "<nil>"},
		{"SELECT min(NULL, NULL) FROM t1 LIMIT 1", "<nil>"},
	}

	for _, p := range probes {
		t.Run(p.sql, func(t *testing.T) {
			rs, err := ex.QueryAll(ctx, p.sql)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(rs) == 0 {
				t.Fatalf("expected results, got none")
			}
			got := fmt.Sprint(rs[0].Data[0].ToAny())
			if got != p.want {
				t.Errorf("got %q, want %q", got, p.want)
			}
		})
	}
}

// TestMinMax_AggregateStillWorks verifies that single-arg min/max continue
// to work as aggregates with GROUP BY after the multi-arg parser change.
func TestMinMax_AggregateStillWorks(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"a", "b", "c"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 2, 3)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (4, 5, 6)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 10, 20)")

	// Aggregate: single arg with GROUP BY
	rs, err := ex.QueryAll(ctx, "SELECT a, max(b), min(b) FROM t1 GROUP BY a ORDER BY a")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rs))
	}
	// a=1: max(b)=10, min(b)=2
	got1 := fmt.Sprintf("%v %v %v", rs[0].Data[0].ToAny(), rs[0].Data[1].ToAny(), rs[0].Data[2].ToAny())
	if got1 != "1 10 2" {
		t.Errorf("row1 got %q, want '1 10 2'", got1)
	}
	// a=4: max(b)=5, min(b)=5
	got2 := fmt.Sprintf("%v %v %v", rs[1].Data[0].ToAny(), rs[1].Data[1].ToAny(), rs[1].Data[2].ToAny())
	if got2 != "4 5 5" {
		t.Errorf("row2 got %q, want '4 5 5'", got2)
	}
}
