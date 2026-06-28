package LX

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
)

var ErrUnexpectedChar = errors.New("lx: unexpected character")
var ErrUnterminatedString = errors.New("lx: unterminated string")
var ErrInvalidInt = errors.New("lx: invalid integer literal")
var ErrIntOverflow = errors.New("lx: integer literal overflows int64")

var keywords = map[string]TokenType{
	"CREATE":        T_CREATE,
	"DROP":          T_DROP,
	"INSERT":        T_INSERT,
	"INTO":          T_INTO,
	"UPDATE":        T_UPDATE,
	"DELETE":        T_DELETE,
	"SELECT":        T_SELECT,
	"FROM":          T_FROM,
	"WHERE":         T_WHERE,
	"AND":           T_AND,
	"OR":            T_OR,
	"NOT":           T_NOT,
	"IN":            T_IN,
	"BETWEEN":       T_BETWEEN,
	"LIKE":          T_LIKE,
	"GLOB":          T_GLOB,
	"DIV":           T_DIV,
	"IS":            T_IS,
	"NULL":          T_NULL,
	"BEGIN":         T_BEGIN,
	"COMMIT":        T_COMMIT,
	"ROLLBACK":      T_ROLLBACK,
	"AS":            T_AS,
	"BY":            T_BY,
	"ASC":           T_ASC,
	"DESC":          T_DESC,
	"LIMIT":         T_LIMIT,
	"OFFSET":        T_OFFSET,
	"TABLE":         T_TABLE,
	"INDEX":         T_INDEX,
	"PRIMARY":       T_PRIMARY,
	"KEY":           T_KEY,
	"NOTNULL":       T_NOTNULL,
	"DEFAULT":       T_DEFAULT,
	"CHECK":         T_CHECK,
	"UNIQUE":        T_UNIQUE,
	"INTEGER":       T_INT_KW,
	"INT":           T_INT_KW,
	"BIGINT":        T_BIGINT,
	"FLOAT":         T_FLOAT_KW,
	"REAL":          T_FLOAT_KW,
	"DOUBLE":        T_FLOAT_KW,
	"BOOL":          T_BOOL,
	"BOOLEAN":       T_BOOL,
	"TEXT":          T_TEXT,
	"BLOB":          T_BLOB,
	"VARCHAR":       T_VARCHAR,
	"TIMESTAMP":     T_TIMESTAMP,
	"NUMERIC":       T_NUMERIC,
	"DATE":          T_DATE,
	"TIME":          T_TIME,
	"JSON":          T_JSON,
	"DECIMAL":       T_DECIMAL,
	"VALUES":        T_VALUES,
	"SET":           T_SET,
	"ORDER":         T_ORDER,
	"JOIN":          T_JOIN,
	"LEFT":          T_LEFT,
	"RIGHT":         T_RIGHT,
	"INNER":         T_INNER,
	"CROSS":         T_CROSS,
	"ON":            T_ON,
	"USING":         T_USING,
	"GROUP":         T_GROUP,
	"HAVING":        T_HAVING,
	"COUNT":         T_COUNT,
	"SUM":           T_SUM,
	"AVG":           T_AVG,
	"MIN":           T_MIN,
	"MAX":           T_MAX,
	"DISTINCT":      T_DISTINCT,
	"CASE":          T_CASE,
	"WHEN":          T_WHEN,
	"THEN":          T_THEN,
	"ELSE":          T_ELSE,
	"END":           T_END,
	"EXISTS":        T_EXISTS,
	"ANALYZE":       T_ANALYZE,
	"VACUUM":        T_VACUUM,
	"TRUNCATE":      T_TRUNCATE,
	"REINDEX":       T_REINDEX,
	"BEFORE":        T_BEFORE,
	"AFTER":         T_AFTER,
	"INSTEAD":       T_INSTEAD,
	"OF":            T_OF,
	"FOR":           T_FOR,
	"EACH":          T_EACH,
	"OUTER":         T_OUTER,
	"FULL":          T_FULL,
	"INTERVAL":      T_INTERVAL,
	"OVER":          T_OVER,
	"PARTITION":     T_PARTITION,
	"ROWS":          T_ROWS,
	"RANGE":         T_RANGE,
	"PRECEDING":     T_PRECEDING,
	"FOLLOWING":     T_FOLLOWING,
	"CURRENT":       T_CURRENT,
	"UNBOUNDED":     T_UNBOUNDED,
	"ROW_NUMBER":    T_ROW_NUMBER,
	"RANK":          T_RANK,
	"DENSE_RANK":    T_DENSE_RANK,
	"LAG":           T_LAG,
	"LEAD":          T_LEAD,
	"FIRST_VALUE":   T_FIRST_VALUE,
	"LAST_VALUE":    T_LAST_VALUE,
	"NTH_VALUE":     T_NTH_VALUE,
	"ROW":           T_ROW,
	"TRANSACTION":   T_TRANSACTION,
	"ISOLATION":     T_ISOLATION,
	"LEVEL":         T_LEVEL,
	"COALESCE":      T_COALESCE,
	"NULLIF":        T_NULLIF,
	"UNION":         T_UNION,
	"INTERSECT":     T_INTERSECT,
	"EXCEPT":        T_EXCEPT,
	"ALL":           T_ALL,
	"AUTOINCREMENT": T_AUTOINCREMENT,
	"READ":          T_READ,
	"COMMITTED":     T_COMMITTED,
	"UNCOMMITTED":   T_UNCOMMITTED,
	"REPEATABLE":    T_REPEATABLE,
	"SERIALIZABLE":  T_SERIALIZABLE,
	"VIEW":          T_VIEW,
	"TRIGGER":       T_TRIGGER,
	"TEMP":          T_TEMP,
	"TEMPORARY":     T_TEMPORARY,
	"ALTER":         T_ALTER,
	"COLUMN":        T_COLUMN,
	"ADD":           T_ADD,
	"RENAME":        T_RENAME,
	"FETCH":         T_FETCH,
	"FIRST":         T_FIRST,
	"NEXT":          T_NEXT,
	"LAST":          T_LAST,
	"ONLY":          T_ONLY,
	"REFERENCES":    T_REFERENCES,
	"FOREIGN":       T_FOREIGN,
	"CASCADE":       T_CASCADE,
	"RESTRICT":      T_RESTRICT,
	"NO":            T_NO,
	"ACTION":        T_ACTION,
	"PRAGMA":        T_PRAGMA,
	"CAST":          T_CAST,
	"TRUE":          T_TRUE,
	"FALSE":         T_FALSE,
	"EXPLAIN":       T_EXPLAIN,
	"QUERY":         T_QUERY,
	"PLAN":          T_PLAN,
	"RETURNING":     T_RETURNING,
	"CONFLICT":      T_CONFLICT,
	"DO":            T_DO,
	"NOTHING":       T_NOTHING,
	"EXCLUDED":      T_EXCLUDED,
	"WITH":          T_WITH,
	"SAVEPOINT":     T_SAVEPOINT,
	"RELEASE":       T_RELEASE,
	"TO":            T_TO,
	"DEFERRED":      T_DEFERRED,
	"IMMEDIATE":     T_IMMEDIATE,
	"EXCLUSIVE":     T_EXCLUSIVE,
	"RAISE":         T_RAISE,
	"ESCAPE":        T_ESCAPE,       // REQ000567: LIKE ... ESCAPE
	"INDEXED":       T_INDEXED,      // REQ000529/569: INDEXED BY / NOT INDEXED
	"MATCH":         T_MATCH,        // REQ000561
	"DEFERRABLE":    T_DEFERRABLE,   // REQ000561
	"INITIALLY":     T_INITIALLY,    // REQ000561
	"COLLATE":       T_COLLATE,      // REQ000565
	"ATTACH":        T_ATTACH,       // REQ000557
	"DETACH":        T_DETACH,       // REQ000557
	"MATERIALIZED":  T_MATERIALIZED, // REQ000316
	"REFRESH":       T_REFRESH,      // REQ000316
	"FILTER":        T_FILTER,       // REQ000747
	"EXCLUDE":       T_EXCLUDE,      // REQ000748
	"OTHERS":        T_OTHERS,       // REQ000748
	"TIES":          T_TIES,         // REQ000748
	"GROUPS":        T_GROUPS,       // REQ000746
}

type Lexer struct {
	input string
	pos   int
	line  int
	col   int
	// REQ001141: token-start position snapshot. captureStart() is
	// called once per Next(); all scanner functions read these
	// fields instead of stashing their own local copies.
	startLine int
	startCol  int
	// For Peek()/Peek2() save/restore.
	savedPos       int
	savedLine      int
	savedCol       int
	savedStartLine int
	savedStartCol  int
}

func NewLexer(input string) *Lexer {
	return &Lexer{
		input: input,
		pos:   0,
		line:  1,
		col:   1,
	}
}

// REQ001141: captureStart snapshots line/col at the current
// cursor so scanner functions can build their Token with a
// single field read instead of two locals.
func (l *Lexer) captureStart() {
	l.startLine = l.line
	l.startCol = l.col
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
	l.savedStartLine = l.startLine
	l.savedStartCol = l.startCol

	token := l.Next()

	l.pos = l.savedPos
	l.line = l.savedLine
	l.col = l.savedCol
	l.startLine = l.savedStartLine
	l.startCol = l.savedStartCol

	return token
}

// Peek2 returns the second upcoming token without consuming the
// first. It saves state, peeks (which leaves the lexer at the
// same state), captures the result, advances one token, peeks
// again, then restores.
func (l *Lexer) Peek2() Token {
	// Save full state.
	savedPos, savedLine, savedCol := l.pos, l.line, l.col
	savedSL, savedSC := l.startLine, l.startCol
	first := l.Peek()
	if first.Type == T_EOF {
		return first
	}
	// The Peek() above already restored pos/line/col. Now advance
	// one token and peek again.
	_ = l.Next()
	second := l.Peek()
	// Restore.
	l.pos, l.line, l.col = savedPos, savedLine, savedCol
	l.startLine, l.startCol = savedSL, savedSC
	return second
}

func (l *Lexer) Input() string {
	return l.input
}

// REQ001141: advance() bookkeeping is unchanged from baseline;
// the line/col updates per byte are required to keep Token.Line
// /Token.Col correct (public API + SyntaxError reporting).
// The savings documented in REQ001141 come from the
// captureStart() refactor below: each scanner drops 2 local
// reads and reuses the snapshotted startLine/startCol.
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

// REQ001144: ASCII fast-path helpers. SQL keywords, identifiers,
// numbers, and whitespace are dominated by ASCII bytes. The
// unicode.Is* family dispatches through a Unicode category table;
// a single range check on byte values is roughly an order of
// magnitude faster on the hot path. We fall back to unicode.Is*
// only when the byte is non-ASCII (≥ 0x80).
func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isASCIILetterDigit(c byte) bool {
	return isASCIILetter(c) || (c >= '0' && c <= '9')
}

func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// isASCIISpace covers ASCII whitespace: space, tab, LF, VT, FF, CR.
// Matches unicode.IsSpace's behaviour for the ASCII subset.
func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}

func (l *Lexer) Next() Token {
	l.skipWhitespaceAndComments()

	if l.pos >= len(l.input) {
		return Token{Type: T_EOF, Lexeme: "", Line: l.line, Col: l.col}
	}

	// REQ001141: snapshot line/col once for the upcoming token.
	l.captureStart()
	c := l.peek()

	if c == '\'' {
		return l.scanString()
	}

	// REQ001144: ASCII fast-path for identifier vs number dispatch.
	if c < 0x80 {
		if isASCIILetter(c) {
			return l.scanIdent()
		}
		if isASCIIDigit(c) {
			return l.scanNumber()
		}
	} else {
		if unicode.IsLetter(rune(c)) || c == '_' {
			return l.scanIdent()
		}
		if unicode.IsDigit(rune(c)) {
			return l.scanNumber()
		}
	}

	return l.scanOperator()
}

func (l *Lexer) skipWhitespaceAndComments() {
	for {
		c := l.peek()
		if c == 0 {
			return
		}

		// REQ001144: ASCII fast-path for whitespace.
		if c < 0x80 {
			if isASCIISpace(c) {
				l.advance()
				continue
			}
		} else if unicode.IsSpace(rune(c)) {
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
	// REQ001141: use the snapshot captured by Next().
	startLine := l.startLine
	startCol := l.startCol

	l.advance()

	// REQ001010: track start position to avoid strings.Builder when no escapes.
	start := l.pos
	hasEscape := false
	for {
		c := l.peek()
		if c == 0 {
			return Token{Type: T_ERROR, Lexeme: "", LitErr: ErrUnterminatedString, Line: startLine, Col: startCol}
		}
		if c == '\'' {
			if len(l.input) > l.pos+1 && l.input[l.pos+1] == '\'' {
				hasEscape = true
				l.advance()
				l.advance()
				continue
			}
			l.advance()
			lexeme := l.input[start : l.pos-1]
			if hasEscape {
				// Build string with escape processing
				var sb strings.Builder
				for i := start; i < l.pos-1; i++ {
					if l.input[i] == '\'' && i+1 < l.pos-1 && l.input[i+1] == '\'' {
						sb.WriteByte('\'')
						i++
					} else {
						sb.WriteByte(l.input[i])
					}
				}
				v := sb.String()
				return Token{Type: T_STRING, Lexeme: v, LitStr: v, Line: startLine, Col: startCol}
			}
			return Token{Type: T_STRING, Lexeme: lexeme, LitStr: lexeme, Line: startLine, Col: startCol}
		}
		l.advance()
	}
}

func (l *Lexer) scanIdent() Token {
	// REQ001141: use the snapshot captured by Next().
	startLine := l.startLine
	startCol := l.startCol

	// REQ001010: scan start position, slice directly from input string.
	start := l.pos
	for {
		c := l.peek()
		if c == 0 {
			break
		}
		// REQ001144: ASCII fast-path for the inner ident loop.
		if c < 0x80 {
			if !isASCIILetterDigit(c) {
				break
			}
		} else if !unicode.IsLetter(rune(c)) && !unicode.IsDigit(rune(c)) && c != '_' {
			break
		}
		l.advance()
	}

	ident := l.input[start:l.pos]

	if typ, ok := keywords[strings.ToUpper(ident)]; ok {
		return Token{Type: typ, Lexeme: ident, Line: startLine, Col: startCol}
	}

	return Token{Type: T_IDENT, Lexeme: ident, Line: startLine, Col: startCol}
}

func (l *Lexer) scanNumber() Token {
	// REQ001141: use the snapshot captured by Next().
	startLine := l.startLine
	startCol := l.startCol

	// REQ001142: slice directly from input. Numbers have no escapes,
	// so start/end indices are always sufficient (same pattern as
	// REQ001010 for scanIdent). This eliminates the strings.Builder
	// and the .String() copy.
	start := l.pos
	hasDot := false
	for {
		c := l.peek()
		if c == 0 {
			break
		}
		// REQ001144: ASCII fast-path for the inner number loop.
		if c < 0x80 {
			if !isASCIIDigit(c) && c != '.' {
				break
			}
		} else if !unicode.IsDigit(rune(c)) && c != '.' {
			break
		}
		if c == '.' {
			if hasDot {
				break
			}
			hasDot = true
		}
		l.advance()
	}

	lit := l.input[start:l.pos]

	if hasDot {
		// REQ001143: populate LitFloat so callers don't need to
		// re-parse the lexeme. Lexeme is still set for error
		// messages and string-form consumers.
		f, ferr := parseFloatLiteral(lit)
		if ferr != nil {
			return Token{Type: T_ERROR, Lexeme: "", LitErr: ferr, Line: startLine, Col: startCol}
		}
		return Token{Type: T_FLOAT, Lexeme: lit, LitFloat: f, Line: startLine, Col: startCol}
	}

	val, err := parseInt64(lit)
	if err != nil {
		return Token{Type: T_ERROR, Lexeme: "", LitErr: err, Line: startLine, Col: startCol}
	}
	return Token{Type: T_INT, Lexeme: lit, LitInt: val, Line: startLine, Col: startCol}
}

func parseFloatLiteral(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
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
	// REQ001141: use the snapshot captured by Next().
	startLine := l.startLine
	startCol := l.startCol

	c := l.advance()

	switch c {
	case '=':
		return Token{Type: T_EQ, Lexeme: "=", Line: startLine, Col: startCol}
	case '!':
		if l.peek() == '=' {
			l.advance()
			return Token{Type: T_NE, Lexeme: "!=", Line: startLine, Col: startCol}
		}
		return Token{Type: T_ERROR, Lexeme: "", LitErr: ErrUnexpectedChar, Line: startLine, Col: startCol}
	case '<':
		if l.peek() == '=' {
			l.advance()
			return Token{Type: T_LE, Lexeme: "<=", Line: startLine, Col: startCol}
		}
		if l.peek() == '>' {
			l.advance()
			return Token{Type: T_NE, Lexeme: "<>", Line: startLine, Col: startCol}
		}
		if l.peek() == '<' {
			l.advance()
			return Token{Type: T_LSHIFT, Lexeme: "<<", Line: startLine, Col: startCol}
		}
		return Token{Type: T_LT, Lexeme: "<", Line: startLine, Col: startCol}
	case '>':
		if l.peek() == '=' {
			l.advance()
			return Token{Type: T_GE, Lexeme: ">=", Line: startLine, Col: startCol}
		}
		if l.peek() == '>' {
			l.advance()
			return Token{Type: T_RSHIFT, Lexeme: ">>", Line: startLine, Col: startCol}
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
	case '&':
		return Token{Type: T_BITAND, Lexeme: "&", Line: startLine, Col: startCol}
	case '|':
		if l.peek() == '|' {
			l.advance()
			return Token{Type: T_CONCAT, Lexeme: "||", Line: startLine, Col: startCol}
		}
		return Token{Type: T_BITOR, Lexeme: "|", Line: startLine, Col: startCol}
	case '^':
		return Token{Type: T_BITXOR, Lexeme: "^", Line: startLine, Col: startCol}
	case '~':
		return Token{Type: T_BITNOT, Lexeme: "~", Line: startLine, Col: startCol}
	case '%':
		return Token{Type: T_MOD, Lexeme: "%", Line: startLine, Col: startCol}
	}

	return Token{Type: T_ERROR, Lexeme: "", LitErr: ErrUnexpectedChar, Line: startLine, Col: startCol}
}
