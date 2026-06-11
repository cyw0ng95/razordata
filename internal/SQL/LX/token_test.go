package LX

import "testing"

func TestTokenStruct(t *testing.T) {
	token := Token{
		Type:    T_IDENT,
		Lexeme:  "foo",
		Literal: nil,
		Line:    1,
		Col:     5,
	}

	if token.Type != T_IDENT {
		t.Errorf("expected T_IDENT, got %v", token.Type)
	}
	if token.Lexeme != "foo" {
		t.Errorf("expected Lexeme 'foo', got %q", token.Lexeme)
	}
	if token.Line != 1 {
		t.Errorf("expected Line 1, got %d", token.Line)
	}
	if token.Col != 5 {
		t.Errorf("expected Col 5, got %d", token.Col)
	}
}

func TestTokenTypesExist(t *testing.T) {
	types := []TokenType{
		T_IDENT,
		T_STRING,
		T_INT,
		T_FLOAT,
		T_BIND,
		T_EQ,
		T_NE,
		T_LT,
		T_LE,
		T_GT,
		T_GE,
		T_PLUS,
		T_MINUS,
		T_STAR,
		T_SLASH,
		T_LPAREN,
		T_RPAREN,
		T_COMMA,
		T_DOT,
		T_SEMICOLON,
		T_COLON,
		T_CREATE,
		T_DROP,
		T_INSERT,
		T_UPDATE,
		T_DELETE,
		T_SELECT,
		T_FROM,
		T_WHERE,
		T_AND,
		T_OR,
		T_NOT,
		T_IN,
		T_BETWEEN,
		T_LIKE,
		T_IS,
		T_NULL,
		T_BEGIN,
		T_COMMIT,
		T_ROLLBACK,
		T_AS,
		T_BY,
		T_ASC,
		T_DESC,
		T_LIMIT,
		T_OFFSET,
		T_TABLE,
		T_INDEX,
		T_PRIMARY,
		T_KEY,
		T_NOTNULL,
		T_DEFAULT,
		T_UNIQUE,
		T_INT_KW,
		T_BIGINT,
		T_FLOAT_KW,
		T_BOOL,
		T_TEXT,
		T_BLOB,
		T_VARCHAR,
		T_TIMESTAMP,
		T_VALUES,
		T_SET,
		T_ORDER,
		T_JOIN,
		T_LEFT,
		T_RIGHT,
		T_INNER,
		T_CROSS,
		T_ON,
		T_USING,
		T_GROUP,
		T_HAVING,
		T_COUNT,
		T_SUM,
		T_AVG,
		T_MIN,
		T_MAX,
		T_DISTINCT,
		T_CASE,
		T_WHEN,
		T_THEN,
		T_ELSE,
		T_END,
		T_CAST,
		T_EXISTS,
		T_TRUE,
		T_FALSE,
		T_ANALYZE,
		T_VACUUM,
		T_OUTER,
		T_FULL,
	}

	for i, typ := range types {
		if typ == 0 {
			t.Errorf("token type at index %d has value 0", i)
		}
	}
}

func TestTokenIsEOF(t *testing.T) {
	eof := Token{Type: T_EOF}
	if !eof.IsEOF() {
		t.Error("expected IsEOF() to return true for T_EOF")
	}

	ident := Token{Type: T_IDENT}
	if ident.IsEOF() {
		t.Error("expected IsEOF() to return false for T_IDENT")
	}
}

func TestTokenIsError(t *testing.T) {
	errToken := Token{Type: T_ERROR}
	if !errToken.IsError() {
		t.Error("expected IsError() to return true for T_ERROR")
	}

	normal := Token{Type: T_IDENT, Lexeme: "foo"}
	if normal.IsError() {
		t.Error("expected IsError() to return false for normal token")
	}
}

func TestTokenString(t *testing.T) {
	cases := []struct {
		typ  TokenType
		lex  string
		want string
	}{
		{T_IDENT, "foo", "IDENT:foo"},
		{T_SELECT, "SELECT", "SELECT:SELECT"},
		{T_INT, "42", "INT:42"},
		{T_EOF, "", "EOF:"},
	}
	for _, c := range cases {
		got := Token{Type: c.typ, Lexeme: c.lex}.String()
		if got != c.want {
			t.Errorf("String() for %v = %q, want %q", c.typ, got, c.want)
		}
	}
}
