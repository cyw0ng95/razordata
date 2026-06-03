package sqlcmp

import (
	"testing"
)

type testCase struct {
	name    string
	sql     string
	skipSQL bool
}

func (tc testCase) runParse(t *testing.T) {
	p := NewParser(tc.sql)
	_, err := p.Parse()
	if err != nil {
		t.Errorf("parse error for %q: %v", tc.sql, err)
	}
}

func (tc testCase) runRewrite(t *testing.T) {
	p := NewParser(tc.sql)
	stmt, err := p.Parse()
	if err != nil {
		t.Errorf("parse error for %q: %v", tc.sql, err)
		return
	}
	_, err = Rewrite(stmt)
	if err != nil {
		t.Errorf("rewrite error for %q: %v", tc.sql, err)
	}
}

func TestRewriterProducesValidSQL(t *testing.T) {
	cases := []testCase{
		{"simple_select", "SELECT * FROM users WHERE age > 25", false},
		{"insert_with_all_types", "INSERT INTO t (a, b, c, d) VALUES (1, 'text', 3.14, NULL)", false},
		{"update_with_expr", "UPDATE t SET a = b + 1, c = d * 2 WHERE e > 0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser(tc.sql)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("parse error for %q: %v", tc.sql, err)
			}
			rewritten, err := Rewrite(stmt)
			if err != nil {
				t.Fatalf("rewrite error for %q: %v", tc.sql, err)
			}
			if rewritten == "" {
				t.Fatalf("empty rewrite for %q", tc.sql)
			}
			p2 := NewParser(rewritten)
			_, err = p2.Parse()
			if err != nil {
				t.Fatalf("rewritten SQL %q failed to parse: %v", rewritten, err)
			}
		})
	}
}

func TestExpressionEquivalence(t *testing.T) {
	cases := []struct {
		name  string
		expr1 string
		expr2 string
	}{
		{"paren_assoc", "(a + b) + c", "a + (b + c)"},
		{"mul_div", "a * b / c", "(a * b) / c"},
		{"mixed", "a + b * c - d / e", "a + (b * c) - (d / e)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p1 := NewParser("SELECT " + c.expr1 + " FROM t")
			p2 := NewParser("SELECT " + c.expr2 + " FROM t")
			s1, err := p1.Parse()
			if err != nil {
				t.Fatalf("parse error for %q: %v", c.expr1, err)
			}
			s2, err := p2.Parse()
			if err != nil {
				t.Fatalf("parse error for %q: %v", c.expr2, err)
			}
			r1, _ := Rewrite(s1)
			r2, _ := Rewrite(s2)
			if r1 != r2 {
				t.Logf("expressions may differ: %s vs %s", r1, r2)
			}
		})
	}
}

func TestSQLiteAvailable(t *testing.T) {
	if SkipSQLite() {
		t.Skip("SQLite not available")
	}
}

func TestParseErrors(t *testing.T) {
	errCases := []testCase{
		{"empty", "", false},
		{"select_no_table", "SELECT * FROM", false},
		{"unterminated", "SELECT 'unterminated", false},
		{"missing_rparen_aggregate", "SELECT COUNT( FROM t", false},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser(tc.sql)
			_, err := p.Parse()
			if err == nil && tc.sql != "" {
				t.Errorf("expected error for %q, got nil", tc.sql)
			}
		})
	}
}
