package sqlcmp

import (
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
	"github.com/cyw0ng95/razordata/internal/SQL/RE"
)

type testCase struct {
	name    string
	sql     string
	skipSQL bool
}

func (tc testCase) runParse(t *testing.T) {
	p := PS.NewParser(tc.sql)
	_, err := p.Parse()
	if err != nil {
		t.Errorf("parse error for %q: %v", tc.sql, err)
	}
}

func (tc testCase) runRewrite(t *testing.T) {
	p := PS.NewParser(tc.sql)
	stmt, err := p.Parse()
	if err != nil {
		t.Errorf("parse error for %q: %v", tc.sql, err)
		return
	}
	_, err = RE.Rewrite(stmt)
	if err != nil {
		t.Errorf("rewrite error for %q: %v", tc.sql, err)
	}
}

var selectCases = []testCase{
	{"simple_select", "SELECT * FROM t", false},
	{"select_columns", "SELECT a, b, c FROM t", false},
	{"select_where", "SELECT * FROM t WHERE a = 1", false},
	{"select_where_ne", "SELECT * FROM t WHERE a != 1", false},
	{"select_where_lt", "SELECT * FROM t WHERE a < 1", false},
	{"select_where_le", "SELECT * FROM t WHERE a <= 1", false},
	{"select_where_gt", "SELECT * FROM t WHERE a > 1", false},
	{"select_where_ge", "SELECT * FROM t WHERE a >= 1", false},
	{"select_order_by", "SELECT * FROM t ORDER BY a", false},
	{"select_order_by_desc", "SELECT * FROM t ORDER BY a DESC", false},
	{"select_limit", "SELECT * FROM t LIMIT 10", false},
	{"select_offset", "SELECT * FROM t OFFSET 5", false},
	{"select_limit_offset", "SELECT * FROM t LIMIT 10 OFFSET 5", false},
	{"select_where_and", "SELECT * FROM t WHERE a = 1 AND b = 2", false},
	{"select_where_or", "SELECT * FROM t WHERE a = 1 OR b = 2", false},
	{"select_where_and_or", "SELECT * FROM t WHERE a = 1 AND b = 2 OR c = 3", false},
	{"select_where_not", "SELECT * FROM t WHERE NOT a = 1", false},
	{"select_where_in", "SELECT * FROM t WHERE a IN (1, 2, 3)", false},
	{"select_where_between", "SELECT * FROM t WHERE a BETWEEN 1 AND 10", false},
	{"select_where_like", "SELECT * FROM t WHERE a LIKE '%foo%'", false},
	{"select_where_is_null", "SELECT * FROM t WHERE a IS NULL", false},
	{"select_where_is_not_null", "SELECT * FROM t WHERE a IS NOT NULL", false},
	{"select_expr_plus", "SELECT a + b FROM t", false},
	{"select_expr_minus", "SELECT a - b FROM t", false},
	{"select_expr_star", "SELECT a * b FROM t", false},
	{"select_expr_slash", "SELECT a / b FROM t", false},
	{"select_expr_precedence", "SELECT a + b * c FROM t", false},
	{"select_expr_precedence2", "SELECT a * b + c FROM t", false},
	{"select_expr_parens", "SELECT (a + b) * c FROM t", false},
	{"select_expr_unary_minus", "SELECT -a FROM t", false},
	{"select_expr_unary_plus", "SELECT +a FROM t", false},
	{"select_int", "SELECT 42 FROM t", false},
	{"select_float", "SELECT 3.14 FROM t", false},
	{"select_string", "SELECT 'hello world' FROM t", false},
	{"select_param", "SELECT ? FROM t", false},
	{"select_star_where", "SELECT * FROM t WHERE a = 1", false},
	{"select_all_cols", "SELECT * FROM t WHERE a = 1 AND b = 2 ORDER BY c LIMIT 10 OFFSET 5", false},
}

func TestSelectRewrite(t *testing.T) {
	for _, tc := range selectCases {
		t.Run(tc.name, tc.runRewrite)
	}
}

func TestSelectParse(t *testing.T) {
	for _, tc := range selectCases {
		t.Run(tc.name, tc.runParse)
	}
}

var dmlCases = []testCase{
	{"insert_values", "INSERT INTO t VALUES (1, 'a')", false},
	{"insert_multirow", "INSERT INTO t VALUES (1, 'a'), (2, 'b')", false},
	{"insert_with_cols", "INSERT INTO t (a, b) VALUES (1, 'a')", false},
	{"insert_with_cols_multirow", "INSERT INTO t (a, b) VALUES (1, 'a'), (2, 'b')", false},
	{"update_set", "UPDATE t SET a = 1", false},
	{"update_set_multi", "UPDATE t SET a = 1, b = 2", false},
	{"update_where", "UPDATE t SET a = 1 WHERE b = 2", false},
	{"update_where_and", "UPDATE t SET a = 1 WHERE b = 2 AND c = 3", false},
	{"update_expr", "UPDATE t SET a = b + 1", false},
	{"update_where_expr", "UPDATE t SET a = 1 WHERE b = c + 2", false},
	{"delete_all", "DELETE FROM t", false},
	{"delete_where", "DELETE FROM t WHERE a = 1", false},
	{"delete_where_and", "DELETE FROM t WHERE a = 1 AND b = 2", false},
}

func TestDMLRewrite(t *testing.T) {
	for _, tc := range dmlCases {
		t.Run(tc.name, tc.runRewrite)
	}
}

func TestDMLParse(t *testing.T) {
	for _, tc := range dmlCases {
		t.Run(tc.name, tc.runParse)
	}
}

var ddlCases = []testCase{
	{"create_table_simple", "CREATE TABLE t (a INTEGER)", false},
	{"create_table_text", "CREATE TABLE t (a TEXT)", false},
	{"create_table_real", "CREATE TABLE t (a REAL)", false},
	{"create_table_blob", "CREATE TABLE t (a BLOB)", false},
	{"create_table_pk", "CREATE TABLE t (a INTEGER PRIMARY KEY)", false},
	{"create_table_pk_col", "CREATE TABLE t (a INTEGER PRIMARY KEY, b TEXT)", false},
	{"create_table_notnull", "CREATE TABLE t (a TEXT NOT NULL)", false},
	{"create_table_pk_notnull", "CREATE TABLE t (a INTEGER PRIMARY KEY NOT NULL)", false},
	{"create_table_pk_constraint", "CREATE TABLE t (a INTEGER, b TEXT, PRIMARY KEY (a))", false},
	{"create_table_multi_col", "CREATE TABLE t (a INTEGER, b TEXT, c REAL)", false},
	{"create_table_all_types", "CREATE TABLE t (a INTEGER, b TEXT, c REAL, d BLOB)", false},
	{"create_table_default", "CREATE TABLE t (a INTEGER DEFAULT 0)", false},
	{"create_table_unique", "CREATE TABLE t (a TEXT UNIQUE)", false},
	{"drop_table", "DROP TABLE t", false},
}

func TestDDLRewrite(t *testing.T) {
	for _, tc := range ddlCases {
		t.Run(tc.name, tc.runRewrite)
	}
}

func TestDDLParse(t *testing.T) {
	for _, tc := range ddlCases {
		t.Run(tc.name, tc.runParse)
	}
}

var lexerCases = []testCase{
	{"keyword_select", "SELECT", false},
	{"keyword_insert", "INSERT", false},
	{"keyword_update", "UPDATE", false},
	{"keyword_delete", "DELETE", false},
	{"keyword_create", "CREATE", false},
	{"keyword_drop", "DROP", false},
	{"keyword_from", "FROM", false},
	{"keyword_where", "WHERE", false},
	{"keyword_and", "AND", false},
	{"keyword_or", "OR", false},
	{"keyword_not", "NOT", false},
	{"keyword_null", "NULL", false},
	{"keyword_order", "ORDER", false},
	{"keyword_by", "BY", false},
	{"keyword_limit", "LIMIT", false},
	{"keyword_offset", "OFFSET", false},
	{"keyword_primary", "PRIMARY", false},
	{"keyword_key", "KEY", false},
	{"keyword_notnull", "NOTNULL", false},
	{"keyword_default", "DEFAULT", false},
	{"keyword_unique", "UNIQUE", false},
	{"keyword_into", "INTO", false},
	{"keyword_values", "VALUES", false},
	{"keyword_set", "SET", false},
	{"keyword_table", "TABLE", false},
	{"case_insensitive", "select * from t", false},
	{"mixed_case", "SeLeCt * FrOm t", false},
	{"ident_with_underscore", "SELECT a_b FROM t", false},
	{"ident_with_digits", "SELECT a1b2 FROM t", false},
	{"string_single_quote", "SELECT 'hello'", false},
	{"string_with_quote", "SELECT 'it''s fine'", false},
	{"string_with_newline", "SELECT 'line1\nline2'", false},
	{"comment_single", "SELECT * FROM t -- comment", false},
	{"comment_block", "SELECT * FROM t /* comment */", false},
	{"whitespace_tabs", "SELECT\t*\tFROM\tt", false},
	{"whitespace_newlines", "SELECT\n*\nFROM\nt", false},
	{"bind_param", "SELECT ?", false},
	{"int_positive", "SELECT 42", false},
	{"int_negative", "SELECT -42", false},
	{"float_simple", "SELECT 3.14", false},
	{"float_negative", "SELECT -3.14", false},
	{"operators", "SELECT 1+2-3*4/5 FROM t", false},
}

func TestLexerRoundTrip(t *testing.T) {
	for _, tc := range lexerCases {
		t.Run(tc.name, func(t *testing.T) {
			lex := LX.NewLexer(tc.sql)
			var prev LX.Token
			for {
				token := lex.Next()
				if token.Type == LX.T_EOF {
					break
				}
				prev = token
			}
			if prev.Type == LX.T_EOF && tc.sql != "" {
				t.Errorf("no tokens produced for %q", tc.sql)
			}
		})
	}
}

func TestRewriterRoundTrip(t *testing.T) {
	inputs := []string{
		"SELECT * FROM t",
		"SELECT a, b FROM t",
		"SELECT * FROM t WHERE a = 1",
		"SELECT * FROM t ORDER BY a DESC",
		"SELECT * FROM t LIMIT 10 OFFSET 5",
		"SELECT * FROM t WHERE a = 1 AND b = 2",
		"SELECT a + b * c FROM t",
		"SELECT (a + b) * c FROM t",
		"SELECT 'hello' FROM t",
		"SELECT ? FROM t",
		"INSERT INTO t VALUES (1, 'a')",
		"INSERT INTO t (a, b) VALUES (1, 'a')",
		"UPDATE t SET a = 1 WHERE b = 2",
		"DELETE FROM t WHERE a = 1",
		"CREATE TABLE t (a INTEGER PRIMARY KEY, b TEXT NOT NULL)",
		"DROP TABLE t",
	}
	for _, input := range inputs {
		t.Run(safeName(input), func(t *testing.T) {
			p := PS.NewParser(input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("parse error for %q: %v", input, err)
			}
			sql, err := RE.Rewrite(stmt)
			if err != nil {
				t.Fatalf("rewrite error for %q: %v", input, err)
			}
			if sql == "" {
				t.Errorf("empty rewrite for %q", input)
			}
			if strings.Contains(sql, "ERROR") {
				t.Errorf("rewrite contains ERROR for %q: %s", input, sql)
			}
		})
	}
}

func TestSQLiteAvailable(t *testing.T) {
	r := NewRunner()
	if r.SkipSQLite() {
		t.Skip("SQLite not available")
	}
}

func TestParseErrors(t *testing.T) {
	errCases := []testCase{
		{"empty", "", false},
		{"select_no_from", "SELECT *", false},
		{"select_no_table", "SELECT * FROM", false},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			p := PS.NewParser(tc.sql)
			_, err := p.Parse()
			if err == nil && tc.sql != "" {
				t.Errorf("expected error for %q, got nil", tc.sql)
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
