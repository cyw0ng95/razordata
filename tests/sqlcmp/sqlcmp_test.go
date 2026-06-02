package sqlcmp

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
	"github.com/cyw0ng95/razordata/internal/SQL/RE"
)

func TestRewrite(t *testing.T) {
	inputs := []string{
		"SELECT * FROM t",
		"SELECT a, b FROM t",
		"SELECT * FROM t WHERE a = 1",
		"SELECT * FROM t ORDER BY a",
		"SELECT * FROM t LIMIT 10",
		"SELECT * FROM t OFFSET 5",
		"INSERT INTO t VALUES (1, 'a')",
		"INSERT INTO t (a, b) VALUES (1, 'a')",
		"UPDATE t SET a = 1",
		"UPDATE t SET a = 1 WHERE b = 2",
		"DELETE FROM t",
		"DELETE FROM t WHERE a = 1",
		"CREATE TABLE t (a INTEGER PRIMARY KEY, b TEXT)",
		"DROP TABLE t",
	}
	for _, input := range inputs {
		t.Run(safeName(input), func(t *testing.T) {
			p := PS.NewParser(input)
			stmt, err := p.Parse()
			if err != nil {
				t.Errorf("parse error for %q: %v", input, err)
				return
			}
			_, err = RE.Rewrite(stmt)
			if err != nil {
				t.Errorf("rewrite error for %q: %v", input, err)
			}
		})
	}
}

func TestLexerTokens(t *testing.T) {
	inputs := []string{
		"SELECT * FROM t",
		"INSERT INTO t VALUES (1, 'a')",
		"UPDATE t SET a = 1",
		"DELETE FROM t WHERE a = 1",
		"CREATE TABLE t (a INTEGER PRIMARY KEY)",
		"DROP TABLE t",
		"SELECT a + b * c FROM t",
		"SELECT 'hello' FROM t",
		"SELECT ? FROM t",
	}
	for _, input := range inputs {
		t.Run(safeName(input), func(t *testing.T) {
			lex := LX.NewLexer(input)
			for {
				token := lex.Next()
				if token.Type == LX.T_EOF {
					break
				}
			}
		})
	}
}

func safeName(s string) string {
	const maxLen = 20
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
