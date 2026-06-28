package LX

import (
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
	if tok.LitInt != int64(123) {
		t.Errorf("expected LitInt=int64(123), got %v", tok.LitInt)
	}
}

func TestLexerIntOverflow(t *testing.T) {
	l := NewLexer("99999999999999999999")
	tok := l.Next()
	if tok.Type != T_ERROR {
		t.Errorf("expected overflow error token, got %v %q", tok.Type, tok.Lexeme)
	}
}

func TestLexerIntMaxInt64(t *testing.T) {
	l := NewLexer("9223372036854775807")
	tok := l.Next()
	if tok.Type != T_INT {
		t.Fatalf("expected T_INT for max int64, got %v", tok.Type)
	}
	if tok.LitInt != int64(9223372036854775807) {
		t.Errorf("expected max int64 literal, got %v", tok.LitInt)
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
	if tok.LitStr != "hello" {
		t.Errorf("expected 'hello', got %q", tok.LitStr)
	}
}

func TestLexerUnterminatedString(t *testing.T) {
	l := NewLexer("'unterminated")
	tok := l.Next()
	if tok.Type != T_ERROR {
		t.Errorf("expected error token for unterminated string")
	}
}

func TestLexerStringEscapedQuote(t *testing.T) {
	l := NewLexer("'it''s here'")
	tok := l.Next()
	if tok.Type != T_STRING {
		t.Errorf("expected T_STRING, got %v", tok.Type)
	}
	if tok.LitStr != "it's here" {
		t.Errorf("expected \"it's here\", got %q", tok.LitStr)
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
		{"TRUE", T_TRUE},
		{"FALSE", T_FALSE},
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
	if tok.Type != T_ERROR {
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

// REQ000634: Peek2 on empty input returns double-EOF.
func TestLexerPeek2_Empty(t *testing.T) {
	l := NewLexer("")
	tok := l.Peek2()
	if tok.Type != T_EOF {
		t.Errorf("Peek2 on empty: expected T_EOF, got %v", tok.Type)
	}
}

// REQ000634: Peek2 after single token returns EOF for second.
func TestLexerPeek2_AfterSingleToken(t *testing.T) {
	l := NewLexer("SELECT")
	tok := l.Peek2()
	if tok.Type != T_EOF {
		t.Errorf("Peek2 after single token: expected T_EOF, got %v", tok.Type)
	}
}

// REQ000634: Peek2 does not advance the lexer position.
func TestLexerPeek2_DoesNotAdvance(t *testing.T) {
	l := NewLexer("SELECT *")
	tok2 := l.Peek2()
	_ = tok2
	tok := l.Next()
	if tok.Type != T_SELECT {
		t.Errorf("after Peek2, Next() = %v, want T_SELECT", tok.Type)
	}
}

// REQ000634: ParseIntLiteral empty string returns error.
func TestParseIntLiteral_Empty(t *testing.T) {
	_, err := ParseIntLiteral("")
	if err != ErrInvalidInt {
		t.Errorf("ParseIntLiteral(''): want ErrInvalidInt, got %v", err)
	}
}

// REQ000634: ParseIntLiteral overflow returns error.
func TestParseIntLiteral_Overflow(t *testing.T) {
	_, err := ParseIntLiteral("99999999999999999999")
	if err != ErrIntOverflow {
		t.Errorf("ParseIntLiteral(overflow): want ErrIntOverflow, got %v", err)
	}
}

// REQ000634: ParseIntLiteral max int64 succeeds.
func TestParseIntLiteral_MaxInt64(t *testing.T) {
	const max = 9223372036854775807
	v, err := ParseIntLiteral("9223372036854775807")
	if err != nil {
		t.Fatalf("ParseIntLiteral(max): %v", err)
	}
	if v != max {
		t.Errorf("got %d, want %d", v, max)
	}
}

// REQ000634: error sentinel messages.
func TestErrorSentinelMessages(t *testing.T) {
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

// REQ001143: typed accessors surface the typed literal fields.
func TestTokenTypedAccessors(t *testing.T) {
	// T_INT
	l := NewLexer("42")
	tok := l.Next()
	if got := tok.IntLit(); got != 42 {
		t.Errorf("IntLit() = %d, want 42", got)
	}

	// T_STRING
	l = NewLexer("'foo'")
	tok = l.Next()
	if got := tok.StrLit(); got != "foo" {
		t.Errorf("StrLit() = %q, want 'foo'", got)
	}

	// T_ERROR (unterminated string)
	l = NewLexer("'unterminated")
	tok = l.Next()
	if tok.ErrLit() != ErrUnterminatedString {
		t.Errorf("ErrLit() = %v, want ErrUnterminatedString", tok.ErrLit())
	}

	// T_ERROR (unexpected char)
	l = NewLexer("@")
	tok = l.Next()
	if tok.ErrLit() != ErrUnexpectedChar {
		t.Errorf("ErrLit() = %v, want ErrUnexpectedChar", tok.ErrLit())
	}

	// T_FLOAT populates LitFloat
	l = NewLexer("3.14")
	tok = l.Next()
	if tok.Type != T_FLOAT {
		t.Fatalf("expected T_FLOAT, got %v", tok.Type)
	}
	if tok.LitFloat != 3.14 {
		t.Errorf("LitFloat = %v, want 3.14", tok.LitFloat)
	}
}

// REQ001143: typed accessors must panic on wrong Type.
func TestTokenTypedAccessors_WrongTypePanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("IntLit on T_IDENT did not panic")
		}
	}()
	l := NewLexer("foo")
	tok := l.Next()
	_ = tok.IntLit()
}

// REQ001140: Peek does not advance and is idempotent within a
// token position. Two consecutive Peek() calls must return the
// same token.
func TestLexerPeekIdempotent(t *testing.T) {
	l := NewLexer("SELECT * FROM t")
	first := l.Peek()
	second := l.Peek()
	if first.Type != second.Type || first.Lexeme != second.Lexeme {
		t.Errorf("Peek not idempotent: %v then %v", first, second)
	}
	// Next() after Peek must return the same token.
	next := l.Next()
	if next.Type != first.Type || next.Lexeme != first.Lexeme {
		t.Errorf("Next() after Peek() = %v, want %v", next, first)
	}
}

// REQ001140: Peek then Peek2 should return the first and second
// upcoming tokens, respectively.
func TestLexerPeekAndPeek2(t *testing.T) {
	l := NewLexer("SELECT id FROM t")
	p1 := l.Peek()
	p2 := l.Peek2()
	if p1.Type != T_SELECT || p1.Lexeme != "SELECT" {
		t.Errorf("Peek() = %v, want SELECT", p1)
	}
	if p2.Type != T_IDENT || p2.Lexeme != "id" {
		t.Errorf("Peek2() = %v, want IDENT 'id'", p2)
	}
	// Next should emit SELECT then id then FROM then t then EOF.
	wantSeq := []struct {
		typ TokenType
		lex string
	}{
		{T_SELECT, "SELECT"},
		{T_IDENT, "id"},
		{T_FROM, "FROM"},
		{T_IDENT, "t"},
		{T_EOF, ""},
	}
	for i, want := range wantSeq {
		got := l.Next()
		if got.Type != want.typ || got.Lexeme != want.lex {
			t.Errorf("token %d: got %v %q, want %v %q", i, got.Type, got.Lexeme, want.typ, want.lex)
		}
	}
}

// REQ001140: Peek2 on EOF input returns EOF.
func TestLexerPeek2_EOF(t *testing.T) {
	l := NewLexer("")
	if got := l.Peek2(); got.Type != T_EOF {
		t.Errorf("Peek2 on empty input: got %v, want EOF", got)
	}
}

// REQ001140: After Next() at EOF, Peek() must continue to return
// EOF without panicking.
func TestLexerPeekAfterEOF(t *testing.T) {
	l := NewLexer("a")
	_ = l.Next()
	if got := l.Peek(); got.Type != T_EOF {
		t.Errorf("Peek after EOF: got %v, want EOF", got)
	}
	if got := l.Peek2(); got.Type != T_EOF {
		t.Errorf("Peek2 after EOF: got %v, want EOF", got)
	}
}

// REQ001140: Repeated Peek interleaved with Next consumes the
// correct sequence — cache must be invalidated when Next() advances.
func TestLexerPeekNextInterleave(t *testing.T) {
	l := NewLexer("a b c")
	// Peek 'a', Next (returns 'a'), Peek 'b', Next (returns 'b'),
	// Peek 'c', Next (returns 'c').
	if got := l.Peek(); got.Lexeme != "a" {
		t.Errorf("Peek1 = %q, want 'a'", got.Lexeme)
	}
	if got := l.Next(); got.Lexeme != "a" {
		t.Errorf("Next1 = %q, want 'a'", got.Lexeme)
	}
	if got := l.Peek(); got.Lexeme != "b" {
		t.Errorf("Peek2 = %q, want 'b'", got.Lexeme)
	}
	if got := l.Peek2(); got.Lexeme != "c" {
		t.Errorf("Peek2 = %q, want 'c'", got.Lexeme)
	}
	if got := l.Next(); got.Lexeme != "b" {
		t.Errorf("Next2 = %q, want 'b'", got.Lexeme)
	}
	if got := l.Next(); got.Lexeme != "c" {
		t.Errorf("Next3 = %q, want 'c'", got.Lexeme)
	}
	if got := l.Next(); got.Type != T_EOF {
		t.Errorf("Next4 = %v, want EOF", got.Type)
	}
}
