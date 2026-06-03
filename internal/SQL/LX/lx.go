package LX

import (
	"errors"
	"strings"
	"unicode"
)

var ErrUnexpectedChar = errors.New("lx: unexpected character")
var ErrUnterminatedString = errors.New("lx: unterminated string")
var ErrInvalidInt = errors.New("lx: invalid integer literal")
var ErrIntOverflow = errors.New("lx: integer literal overflows int64")

var keywords = map[string]TokenType{
	"CREATE":    T_CREATE,
	"DROP":      T_DROP,
	"INSERT":    T_INSERT,
	"INTO":      T_INTO,
	"UPDATE":    T_UPDATE,
	"DELETE":    T_DELETE,
	"SELECT":    T_SELECT,
	"FROM":      T_FROM,
	"WHERE":     T_WHERE,
	"AND":       T_AND,
	"OR":        T_OR,
	"NOT":       T_NOT,
	"IN":        T_IN,
	"BETWEEN":   T_BETWEEN,
	"LIKE":      T_LIKE,
	"IS":        T_IS,
	"NULL":      T_NULL,
	"BEGIN":     T_BEGIN,
	"COMMIT":    T_COMMIT,
	"ROLLBACK":  T_ROLLBACK,
	"AS":        T_AS,
	"BY":        T_BY,
	"ASC":       T_ASC,
	"DESC":      T_DESC,
	"LIMIT":     T_LIMIT,
	"OFFSET":    T_OFFSET,
	"TABLE":     T_TABLE,
	"INDEX":     T_INDEX,
	"PRIMARY":   T_PRIMARY,
	"KEY":       T_KEY,
	"NOTNULL":   T_NOTNULL,
	"DEFAULT":   T_DEFAULT,
	"UNIQUE":    T_UNIQUE,
	"INTEGER":   T_INT_KW,
	"INT":       T_INT_KW,
	"BIGINT":    T_BIGINT,
	"FLOAT":     T_FLOAT_KW,
	"REAL":      T_FLOAT_KW,
	"DOUBLE":    T_FLOAT_KW,
	"BOOL":      T_BOOL,
	"BOOLEAN":   T_BOOL,
	"TEXT":      T_TEXT,
	"BLOB":      T_BLOB,
	"VARCHAR":   T_VARCHAR,
	"TIMESTAMP": T_TIMESTAMP,
	"VALUES":    T_VALUES,
	"SET":       T_SET,
	"ORDER":     T_ORDER,
	"JOIN":      T_JOIN,
	"LEFT":      T_LEFT,
	"RIGHT":     T_RIGHT,
	"INNER":     T_INNER,
	"CROSS":     T_CROSS,
	"ON":        T_ON,
	"USING":     T_USING,
	"GROUP":     T_GROUP,
	"HAVING":    T_HAVING,
	"COUNT":     T_COUNT,
	"SUM":       T_SUM,
	"AVG":       T_AVG,
	"MIN":       T_MIN,
	"MAX":       T_MAX,
	"DISTINCT":  T_DISTINCT,
	"CASE":      T_CASE,
	"WHEN":      T_WHEN,
	"THEN":      T_THEN,
	"ELSE":      T_ELSE,
	"END":       T_END,
	"CAST":      T_CAST,
	"EXISTS":    T_EXISTS,
	"TRUE":      T_TRUE,
	"FALSE":     T_FALSE,
}

type Lexer struct {
	input     string
	pos       int
	line      int
	col       int
	savedPos  int
	savedLine int
	savedCol  int
}

func NewLexer(input string) *Lexer {
	return &Lexer{
		input: input,
		pos:   0,
		line:  1,
		col:   1,
	}
}

func (l *Lexer) peek() byte {
	if l.pos >= len(l.input) {
		return 0
	}
	return l.input[l.pos]
}

func (l *Lexer) Peek() Token {
	l.savedPos = l.pos
	l.savedLine = l.line
	l.savedCol = l.col

	token := l.Next()

	l.pos = l.savedPos
	l.line = l.savedLine
	l.col = l.savedCol

	return token
}

func (l *Lexer) advance() byte {
	if l.pos >= len(l.input) {
		return 0
	}
	c := l.input[l.pos]
	l.pos++
	if c == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return c
}

func (l *Lexer) Next() Token {
	l.skipWhitespaceAndComments()

	if l.pos >= len(l.input) {
		return Token{Type: T_EOF, Lexeme: "", Line: l.line, Col: l.col}
	}

	c := l.peek()

	if c == '\'' {
		return l.scanString()
	}

	if unicode.IsLetter(rune(c)) || c == '_' {
		return l.scanIdent()
	}

	if unicode.IsDigit(rune(c)) {
		return l.scanNumber()
	}

	return l.scanOperator()
}

func (l *Lexer) skipWhitespaceAndComments() {
	for {
		c := l.peek()
		if c == 0 {
			return
		}

		if unicode.IsSpace(rune(c)) {
			l.advance()
			continue
		}

		if c == '-' && len(l.input) > l.pos+1 && l.input[l.pos+1] == '-' {
			l.advance()
			l.advance()
			for l.peek() != '\n' && l.peek() != 0 {
				l.advance()
			}
			continue
		}

		if c == '/' && len(l.input) > l.pos+1 && l.input[l.pos+1] == '*' {
			l.advance()
			l.advance()
			for {
				if l.peek() == '*' && len(l.input) > l.pos+1 && l.input[l.pos+1] == '/' {
					l.advance()
					l.advance()
					break
				}
				if l.advance() == 0 {
					break
				}
			}
			continue
		}

		return
	}
}

func (l *Lexer) scanString() Token {
	startLine := l.line
	startCol := l.col

	l.advance()

	var sb strings.Builder
	for {
		c := l.peek()
		if c == 0 {
			return Token{Type: T_ERROR, Lexeme: "", Literal: ErrUnterminatedString, Line: startLine, Col: startCol}
		}
		if c == '\'' {
			if len(l.input) > l.pos+1 && l.input[l.pos+1] == '\'' {
				sb.WriteByte('\'')
				l.advance()
				l.advance()
				continue
			}
			l.advance()
			return Token{Type: T_STRING, Lexeme: sb.String(), Literal: sb.String(), Line: startLine, Col: startCol}
		}
		sb.WriteByte(l.advance())
	}
}

func (l *Lexer) scanIdent() Token {
	startLine := l.line
	startCol := l.col

	var sb strings.Builder
	for {
		c := l.peek()
		if c == 0 || (!unicode.IsLetter(rune(c)) && !unicode.IsDigit(rune(c)) && c != '_') {
			break
		}
		sb.WriteByte(l.advance())
	}

	ident := sb.String()

	if typ, ok := keywords[strings.ToUpper(ident)]; ok {
		return Token{Type: typ, Lexeme: ident, Line: startLine, Col: startCol}
	}

	return Token{Type: T_IDENT, Lexeme: ident, Line: startLine, Col: startCol}
}

func (l *Lexer) scanNumber() Token {
	startLine := l.line
	startCol := l.col

	var sb strings.Builder
	hasDot := false

	for {
		c := l.peek()
		if c == 0 || (!unicode.IsDigit(rune(c)) && c != '.') {
			break
		}
		if c == '.' {
			if hasDot {
				break
			}
			hasDot = true
		}
		sb.WriteByte(l.advance())
	}

	lit := sb.String()

	if hasDot {
		return Token{Type: T_FLOAT, Lexeme: lit, Literal: lit, Line: startLine, Col: startCol}
	}

	val, err := parseInt64(lit)
	if err != nil {
		return Token{Type: T_ERROR, Lexeme: "", Literal: err, Line: startLine, Col: startCol}
	}
	return Token{Type: T_INT, Lexeme: lit, Literal: val, Line: startLine, Col: startCol}
}

func parseInt64(s string) (int64, error) {
	return ParseIntLiteral(s)
}

func ParseIntLiteral(s string) (int64, error) {
	if s == "" {
		return 0, ErrInvalidInt
	}
	var val int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrInvalidInt
		}
		d := int64(c - '0')
		if val > (1<<63-1-d)/10 {
			return 0, ErrIntOverflow
		}
		val = val*10 + d
	}
	return val, nil
}

func (l *Lexer) scanOperator() Token {
	startLine := l.line
	startCol := l.col

	c := l.advance()

	switch c {
	case '=':
		return Token{Type: T_EQ, Lexeme: "=", Line: startLine, Col: startCol}
	case '!':
		if l.peek() == '=' {
			l.advance()
			return Token{Type: T_NE, Lexeme: "!=", Line: startLine, Col: startCol}
		}
		return Token{Type: T_ERROR, Lexeme: "", Literal: ErrUnexpectedChar, Line: startLine, Col: startCol}
	case '<':
		if l.peek() == '=' {
			l.advance()
			return Token{Type: T_LE, Lexeme: "<=", Line: startLine, Col: startCol}
		}
		return Token{Type: T_LT, Lexeme: "<", Line: startLine, Col: startCol}
	case '>':
		if l.peek() == '=' {
			l.advance()
			return Token{Type: T_GE, Lexeme: ">=", Line: startLine, Col: startCol}
		}
		return Token{Type: T_GT, Lexeme: ">", Line: startLine, Col: startCol}
	case '+':
		return Token{Type: T_PLUS, Lexeme: "+", Line: startLine, Col: startCol}
	case '-':
		return Token{Type: T_MINUS, Lexeme: "-", Line: startLine, Col: startCol}
	case '*':
		return Token{Type: T_STAR, Lexeme: "*", Line: startLine, Col: startCol}
	case '/':
		return Token{Type: T_SLASH, Lexeme: "/", Line: startLine, Col: startCol}
	case '(':
		return Token{Type: T_LPAREN, Lexeme: "(", Line: startLine, Col: startCol}
	case ')':
		return Token{Type: T_RPAREN, Lexeme: ")", Line: startLine, Col: startCol}
	case ',':
		return Token{Type: T_COMMA, Lexeme: ",", Line: startLine, Col: startCol}
	case '.':
		return Token{Type: T_DOT, Lexeme: ".", Line: startLine, Col: startCol}
	case ';':
		return Token{Type: T_SEMICOLON, Lexeme: ";", Line: startLine, Col: startCol}
	case ':':
		return Token{Type: T_COLON, Lexeme: ":", Line: startLine, Col: startCol}
	case '?':
		return Token{Type: T_BIND, Lexeme: "?", Line: startLine, Col: startCol}
	}

	return Token{Type: T_ERROR, Lexeme: "", Literal: ErrUnexpectedChar, Line: startLine, Col: startCol}
}
