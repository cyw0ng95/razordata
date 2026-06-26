package EX

import (
	"context"
	"fmt"
	"testing"
)

func TestScalarFunctions_413_417(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"a"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t1 VALUES (42)")

	probes := []struct {
		sql  string
		want string
	}{
		// REQ000413: unhex(X[,Y])
		{"SELECT unhex('414243') FROM t1 LIMIT 1", "[65 66 67]"}, // "ABC"
		{"SELECT unhex(NULL) FROM t1 LIMIT 1", "<nil>"},
		{"SELECT unhex('ZZ') FROM t1 LIMIT 1", "<nil>"}, // invalid hex

		// REQ000414: unicode(X)
		{"SELECT unicode('A') FROM t1 LIMIT 1", "65"},
		{"SELECT unicode('α') FROM t1 LIMIT 1", "945"},
		{"SELECT unicode(NULL) FROM t1 LIMIT 1", "<nil>"},
		{"SELECT unicode('') FROM t1 LIMIT 1", "0"},
		{"SELECT unicode('AB') FROM t1 LIMIT 1", "65"}, // first char only

		// REQ000415: unistr(X)
		{"SELECT unistr('\\u0048\\u0065\\u006C\\u006C\\u006F') FROM t1 LIMIT 1", "Hello"}, // backslash-escaped unicode
		{"SELECT unistr('Hello') FROM t1 LIMIT 1", "Hello"},                               // no backslash, pass through
		{"SELECT unistr(NULL) FROM t1 LIMIT 1", "<nil>"},

		// REQ000416: unlikely(X)
		{"SELECT unlikely(42) FROM t1 LIMIT 1", "42"},
		{"SELECT unlikely(NULL) FROM t1 LIMIT 1", "<nil>"},

		// REQ000417: zeroblob(N)
		{"SELECT zeroblob(5) FROM t1 LIMIT 1", "[0 0 0 0 0]"},
		{"SELECT zeroblob(0) FROM t1 LIMIT 1", "[]"},
		{"SELECT zeroblob(NULL) FROM t1 LIMIT 1", "<nil>"},
		{"SELECT zeroblob(-1) FROM t1 LIMIT 1", "<nil>"},
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
