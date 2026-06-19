package EX

import (
	"context"
	"fmt"
	"testing"
)

func TestScalarFunctions_386_400(t *testing.T) {
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
		// REQ000386: char(X1,...,XN)
		{"SELECT char(65, 66, 67) FROM t1 LIMIT 1", "ABC"},
		{"SELECT char(72, 101, 108, 108, 111) FROM t1 LIMIT 1", "Hello"},
		{"SELECT char(NULL) FROM t1 LIMIT 1", "<nil>"},
		{"SELECT char() FROM t1 LIMIT 1", ""},

		// REQ000387: concat(X,...)
		{"SELECT concat('a', 'b', 'c') FROM t1 LIMIT 1", "abc"},
		{"SELECT concat('hello', ' ', 'world') FROM t1 LIMIT 1", "hello world"},
		{"SELECT concat(NULL, 'b') FROM t1 LIMIT 1", "<nil>"},
		{"SELECT concat(1, 2, 3) FROM t1 LIMIT 1", "123"},

		// REQ000388: concat_ws(SEP,X,...)
		{"SELECT concat_ws('-', 'a', 'b', 'c') FROM t1 LIMIT 1", "a-b-c"},
		{"SELECT concat_ws(NULL, 'a', 'b') FROM t1 LIMIT 1", "<nil>"},
		{"SELECT concat_ws(',', 'x', 'y', 'z') FROM t1 LIMIT 1", "x,y,z"},

		// REQ000389: format(FORMAT,...)
		{"SELECT format('hello %s', 'world') FROM t1 LIMIT 1", "hello world"},
		{"SELECT format('value=%d', 42) FROM t1 LIMIT 1", "value=42"},
		{"SELECT format('%f', 3.14) FROM t1 LIMIT 1", "3.140000"},
		{"SELECT format(NULL, 'x') FROM t1 LIMIT 1", "<nil>"},

		// REQ000390: glob(X,Y)
		{"SELECT glob('*.txt', 'hello.txt') FROM t1 LIMIT 1", "1"},
		{"SELECT glob('*.txt', 'hello.md') FROM t1 LIMIT 1", "0"},
		{"SELECT glob('h*', 'hello') FROM t1 LIMIT 1", "1"},
		{"SELECT glob('h*', NULL) FROM t1 LIMIT 1", "<nil>"},
		{"SELECT glob(NULL, 'hello') FROM t1 LIMIT 1", "<nil>"},

		// REQ000393: instr(X,Y)
		{"SELECT instr('hello world', 'world') FROM t1 LIMIT 1", "7"},
		{"SELECT instr('hello', 'z') FROM t1 LIMIT 1", "0"},
		{"SELECT instr(NULL, 'x') FROM t1 LIMIT 1", "<nil>"},
		{"SELECT instr('abc', NULL) FROM t1 LIMIT 1", "<nil>"},

		// REQ000394: last_insert_rowid()
		{"SELECT last_insert_rowid() FROM t1 LIMIT 1", "0"},

		// REQ000395: likelihood(X,Y)
		{"SELECT likelihood(42, 0.5) FROM t1 LIMIT 1", "42"},
		{"SELECT likelihood(NULL, 0.5) FROM t1 LIMIT 1", "<nil>"},

		// REQ000396: likely(X)
		{"SELECT likely(42) FROM t1 LIMIT 1", "42"},
		{"SELECT likely(NULL) FROM t1 LIMIT 1", "<nil>"},

		// REQ000397: ltrim(X[,Y])
		{"SELECT ltrim('  hello  ') FROM t1 LIMIT 1", "hello  "},
		{"SELECT ltrim('xyzhello', 'xyz') FROM t1 LIMIT 1", "hello"},
		{"SELECT ltrim(NULL) FROM t1 LIMIT 1", "<nil>"},

		// REQ000400: octet_length(X)
		{"SELECT octet_length('hello') FROM t1 LIMIT 1", "5"},
		{"SELECT octet_length('世界') FROM t1 LIMIT 1", "6"},
		{"SELECT octet_length(NULL) FROM t1 LIMIT 1", "<nil>"},
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
			got := fmt.Sprint(rs[0].Data[0])
			if got != p.want {
				t.Errorf("got %q, want %q", got, p.want)
			}
		})
	}
}
