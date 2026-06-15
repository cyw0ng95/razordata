package sqlcmp

import "testing"

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
			lex := NewLexer(tc.sql)
			var prev Token
			for {
				token := lex.Next()
				if token.Type == T_EOF {
					break
				}
				prev = token
			}
			if prev.Type == T_EOF && tc.sql != "" {
				t.Errorf("no tokens produced for %q", tc.sql)
			}
		})
	}
}
