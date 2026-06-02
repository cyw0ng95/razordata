package LX

import (
	"errors"
	"testing"
)

func TestLexerNext(t *testing.T) {
	l := NewLexer("SELECT * FROM t")
	tokens := []TokenType{T_SELECT, T_STAR, T_FROM, T_IDENT, T_EOF}

	for i, expected := range tokens {
		got := l.Next()
		if got.Type != expected {
			t.Errorf("token %d: expected %v, got %v", i, expected, got.Type)
		}
	}
}

func TestLexerPeek(t *testing.T) {
	l := NewLexer("abc")
	if l.peek() != 'a' {
		t.Errorf("expected 'a', got %q", l.peek())
	}
	l.advance()
	if l.peek() != 'b' {
		t.Errorf("expected 'b', got %q", l.peek())
	}
}

func TestLexerAdvance(t *testing.T) {
	l := NewLexer("abc")
	c := l.advance()
	if c != 'a' {
		t.Errorf("expected 'a', got %q", c)
	}
	if l.pos != 1 {
		t.Errorf("expected pos 1, got %d", l.pos)
	}
}

func TestLexerIdent(t *testing.T) {
	l := NewLexer("foo")
	tok := l.Next()
	if tok.Type != T_IDENT {
		t.Errorf("expected T_IDENT, got %v", tok.Type)
	}
	if tok.Lexeme != "foo" {
		t.Errorf("expected 'foo', got %q", tok.Lexeme)
	}
}

func TestLexerKeyword(t *testing.T) {
	l := NewLexer("SELECT")
	tok := l.Next()
	if tok.Type != T_SELECT {
		t.Errorf("expected T_SELECT, got %v", tok.Type)
	}
}

func TestLexerInt(t *testing.T) {
	l := NewLexer("123")
	tok := l.Next()
	if tok.Type != T_INT {
		t.Errorf("expected T_INT, got %v", tok.Type)
	}
	if tok.Lexeme != "123" {
		t.Errorf("expected '123', got %q", tok.Lexeme)
	}
	if tok.Literal != int64(123) {
		t.Errorf("expected Literal=int64(123), got %v (%T)", tok.Literal, tok.Literal)
	}
}

func TestLexerIntOverflow(t *testing.T) {
	l := NewLexer("99999999999999999999")
	tok := l.Next()
	if tok.Type != T_EOF || tok.Lexeme != "ERROR" {
		t.Errorf("expected overflow error token, got %v %q", tok.Type, tok.Lexeme)
	}
}

func TestLexerIntMaxInt64(t *testing.T) {
	l := NewLexer("9223372036854775807")
	tok := l.Next()
	if tok.Type != T_INT {
		t.Fatalf("expected T_INT for max int64, got %v", tok.Type)
	}
	if tok.Literal != int64(9223372036854775807) {
		t.Errorf("expected max int64 literal, got %v", tok.Literal)
	}
}

func TestLexerFloat(t *testing.T) {
	l := NewLexer("3.14")
	tok := l.Next()
	if tok.Type != T_FLOAT {
		t.Errorf("expected T_FLOAT, got %v", tok.Type)
	}
	if tok.Lexeme != "3.14" {
		t.Errorf("expected '3.14', got %q", tok.Lexeme)
	}
}

func TestLexerString(t *testing.T) {
	l := NewLexer("'hello'")
	tok := l.Next()
	if tok.Type != T_STRING {
		t.Errorf("expected T_STRING, got %v", tok.Type)
	}
	if tok.Literal != "hello" {
		t.Errorf("expected 'hello', got %q", tok.Literal)
	}
}

func TestLexerUnterminatedString(t *testing.T) {
	l := NewLexer("'unterminated")
	tok := l.Next()
	if tok.Type != T_EOF || tok.Lexeme != "ERROR" {
		t.Errorf("expected error token for unterminated string")
	}
}

func TestLexerStringEscapedQuote(t *testing.T) {
	l := NewLexer("'it''s here'")
	tok := l.Next()
	if tok.Type != T_STRING {
		t.Errorf("expected T_STRING, got %v", tok.Type)
	}
	if tok.Literal != "it's here" {
		t.Errorf("expected \"it's here\", got %q", tok.Literal)
	}
}

func TestLexerComment(t *testing.T) {
	l := NewLexer("SELECT -- this is a comment\nFROM")
	sel := l.Next()
	if sel.Type != T_SELECT {
		t.Errorf("expected T_SELECT, got %v", sel.Type)
	}
	from := l.Next()
	if from.Type != T_FROM {
		t.Errorf("expected T_FROM, got %v", from.Type)
	}
}

func TestLexerBlockComment(t *testing.T) {
	l := NewLexer("SELECT /* comment */ FROM")
	sel := l.Next()
	if sel.Type != T_SELECT {
		t.Errorf("expected T_SELECT, got %v", sel.Type)
	}
	from := l.Next()
	if from.Type != T_FROM {
		t.Errorf("expected T_FROM, got %v", from.Type)
	}
}

func TestLexerWhitespace(t *testing.T) {
	l := NewLexer("  SELECT\n\t  *  \r\n  FROM  ")
	sel := l.Next()
	if sel.Type != T_SELECT {
		t.Errorf("expected T_SELECT, got %v", sel.Type)
	}
	star := l.Next()
	if star.Type != T_STAR {
		t.Errorf("expected T_STAR, got %v", star.Type)
	}
	from := l.Next()
	if from.Type != T_FROM {
		t.Errorf("expected T_FROM, got %v", from.Type)
	}
}

func TestLexerOperators(t *testing.T) {
	cases := []struct {
		input    string
		expected TokenType
	}{
		{"=", T_EQ},
		{"!=", T_NE},
		{"<", T_LT},
		{"<=", T_LE},
		{">", T_GT},
		{">=", T_GE},
		{"+", T_PLUS},
		{"-", T_MINUS},
		{"*", T_STAR},
		{"/", T_SLASH},
		{"(", T_LPAREN},
		{")", T_RPAREN},
		{",", T_COMMA},
		{".", T_DOT},
		{";", T_SEMICOLON},
		{":", T_COLON},
		{"?", T_BIND},
	}

	for _, c := range cases {
		l := NewLexer(c.input)
		tok := l.Next()
		if tok.Type != c.expected {
			t.Errorf("input %q: expected %v, got %v", c.input, c.expected, tok.Type)
		}
	}
}

func TestLexerBindParam(t *testing.T) {
	l := NewLexer("?")
	tok := l.Next()
	if tok.Type != T_BIND {
		t.Errorf("expected T_BIND, got %v", tok.Type)
	}
}

func TestLexerEOF(t *testing.T) {
	l := NewLexer("SELECT")
	sel := l.Next()
	if sel.Type != T_SELECT {
		t.Errorf("expected T_SELECT, got %v", sel.Type)
	}
	eof := l.Next()
	if eof.Type != T_EOF {
		t.Errorf("expected T_EOF, got %v", eof.Type)
	}
	eof2 := l.Next()
	if eof2.Type != T_EOF {
		t.Errorf("expected T_EOF on repeated call, got %v", eof2.Type)
	}
}

func TestLexerLineCol(t *testing.T) {
	l := NewLexer("SELECT\nFROM")
	sel := l.Next()
	if sel.Line != 1 || sel.Col != 1 {
		t.Errorf("SELECT: expected (1,1), got (%d,%d)", sel.Line, sel.Col)
	}
	from := l.Next()
	if from.Line != 2 || from.Col != 1 {
		t.Errorf("FROM: expected (2,1), got (%d,%d)", from.Line, from.Col)
	}
}

func TestLexerLineColWithSpace(t *testing.T) {
	l := NewLexer("SELECT *\nFROM")
	sel := l.Next()
	if sel.Line != 1 || sel.Col != 1 {
		t.Errorf("SELECT: expected (1,1), got (%d,%d)", sel.Line, sel.Col)
	}
	star := l.Next()
	if star.Line != 1 || star.Col != 8 {
		t.Errorf("* expected (1,8), got (%d,%d)", star.Line, star.Col)
	}
	from := l.Next()
	if from.Line != 2 || from.Col != 1 {
		t.Errorf("FROM: expected (2,1), got (%d,%d)", from.Line, from.Col)
	}
}

func TestLexerCaseInsensitiveKeywords(t *testing.T) {
	l := NewLexer("select")
	tok := l.Next()
	if tok.Type != T_SELECT {
		t.Errorf("expected T_SELECT, got %v", tok.Type)
	}
}

func TestLexerMixedCase(t *testing.T) {
	l := NewLexer("SeLeCt")
	tok := l.Next()
	if tok.Type != T_SELECT {
		t.Errorf("expected T_SELECT, got %v", tok.Type)
	}
	if tok.Lexeme != "SeLeCt" {
		t.Errorf("expected 'SeLeCt', got %q", tok.Lexeme)
	}
}

func TestLexerAllKeywords(t *testing.T) {
	kwCases := []struct {
		kw  string
		typ TokenType
	}{
		{"CREATE", T_CREATE},
		{"DROP", T_DROP},
		{"INSERT", T_INSERT},
		{"UPDATE", T_UPDATE},
		{"DELETE", T_DELETE},
		{"SELECT", T_SELECT},
		{"FROM", T_FROM},
		{"WHERE", T_WHERE},
		{"AND", T_AND},
		{"OR", T_OR},
		{"NOT", T_NOT},
		{"IN", T_IN},
		{"BETWEEN", T_BETWEEN},
		{"LIKE", T_LIKE},
		{"IS", T_IS},
		{"NULL", T_NULL},
		{"BEGIN", T_BEGIN},
		{"COMMIT", T_COMMIT},
		{"ROLLBACK", T_ROLLBACK},
		{"AS", T_AS},
		{"BY", T_BY},
		{"ASC", T_ASC},
		{"DESC", T_DESC},
		{"LIMIT", T_LIMIT},
		{"OFFSET", T_OFFSET},
		{"TABLE", T_TABLE},
		{"INDEX", T_INDEX},
		{"PRIMARY", T_PRIMARY},
		{"KEY", T_KEY},
		{"NOTNULL", T_NOTNULL},
		{"DEFAULT", T_DEFAULT},
		{"UNIQUE", T_UNIQUE},
		{"VALUES", T_VALUES},
		{"SET", T_SET},
		{"ORDER", T_ORDER},
		{"JOIN", T_JOIN},
		{"LEFT", T_LEFT},
		{"RIGHT", T_RIGHT},
		{"INNER", T_INNER},
		{"CROSS", T_CROSS},
		{"ON", T_ON},
		{"USING", T_USING},
		{"GROUP", T_GROUP},
		{"HAVING", T_HAVING},
		{"COUNT", T_COUNT},
		{"SUM", T_SUM},
		{"AVG", T_AVG},
		{"MIN", T_MIN},
		{"MAX", T_MAX},
		{"DISTINCT", T_DISTINCT},
		{"CASE", T_CASE},
		{"WHEN", T_WHEN},
		{"THEN", T_THEN},
		{"ELSE", T_ELSE},
		{"END", T_END},
		{"CAST", T_CAST},
		{"EXISTS", T_EXISTS},
		{"INTEGER", T_INT_KW},
		{"INT", T_INT_KW},
		{"BIGINT", T_BIGINT},
		{"FLOAT", T_FLOAT_KW},
		{"REAL", T_FLOAT_KW},
		{"BOOL", T_BOOL},
		{"BOOLEAN", T_BOOL},
		{"TEXT", T_TEXT},
		{"BLOB", T_BLOB},
		{"VARCHAR", T_VARCHAR},
		{"TIMESTAMP", T_TIMESTAMP},
	}

	for _, c := range kwCases {
		l := NewLexer(c.kw)
		tok := l.Next()
		if tok.Type != c.typ {
			t.Errorf("keyword %q: expected %v, got %v", c.kw, c.typ, tok.Type)
		}
	}
}

func TestLexerErrorRecovery(t *testing.T) {
	l := NewLexer("@")
	tok := l.Next()
	if tok.Type != T_EOF || tok.Lexeme != "ERROR" {
		t.Errorf("expected error token, got %v %q", tok.Type, tok.Lexeme)
	}
}

func TestNewLexer(t *testing.T) {
	l := NewLexer("SELECT 1")
	if l.input != "SELECT 1" {
		t.Errorf("expected input 'SELECT 1', got %q", l.input)
	}
	if l.pos != 0 {
		t.Errorf("expected pos 0, got %d", l.pos)
	}
	if l.line != 1 {
		t.Errorf("expected line 1, got %d", l.line)
	}
	if l.col != 1 {
		t.Errorf("expected col 1, got %d", l.col)
	}
}

var errTest = errors.New("test error")

func TestErrors(t *testing.T) {
	if ErrUnexpectedChar.Error() != "lx: unexpected character" {
		t.Error("unexpected ErrUnexpectedChar message")
	}
	if ErrUnterminatedString.Error() != "lx: unterminated string" {
		t.Error("unexpected ErrUnterminatedString message")
	}
	if ErrIntOverflow.Error() != "lx: integer literal overflows int64" {
		t.Error("unexpected ErrIntOverflow message")
	}
	if ErrInvalidInt.Error() != "lx: invalid integer literal" {
		t.Error("unexpected ErrInvalidInt message")
	}
}
