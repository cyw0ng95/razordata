package LX

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

var ErrUnexpectedChar = errors.New("lx: unexpected character")
var ErrUnterminatedString = errors.New("lx: unterminated string")
var ErrInvalidInt = errors.New("lx: invalid integer literal")
var ErrIntOverflow = errors.New("lx: integer literal overflows int64")

// REQ001145: keyword lookup trie.
//
// Replaces the original `keywords` map[string]TokenType. The
// map was hot in the lexer because scanIdent called it for
// every identifier, but the per-call `strings.ToUpper` alloc
// was the actual bottleneck. The trie stores lowercase keys
// and lowercases input bytes on the fly, avoiding the ToUpper
// allocation entirely.
//
// REQ001154: compact child representation. Each node stores
// children as a sorted []childEntry slice instead of [27]*keywordNode.
// Most nodes have 1-3 children; the old 27-slot array wasted
// ~216 bytes of nil pointers per node. The compact form uses
// 16 bytes per child (byte + pointer + padding), so a node with
// 3 children is 72 bytes vs 224 bytes — a 3x reduction.
//
// Layout: sorted childEntry slice indexed by byte ('a'..'z', '_').
// Each node carries a `value` (0 = non-terminal). Lookup is
// O(input_len × avg_children) with linear scan on the tiny
// child slices (typically 1-3 entries).
//
// On lookup failure (no matching keyword), lookupKeyword
// returns (0, T_EOF), signalling the caller to treat the input
// as a plain identifier.
type childEntry struct {
	ch   byte // 'a'..'z' or '_'
	next *keywordNode
}

type keywordNode struct {
	children []childEntry
	value    TokenType // 0 = not a terminal here
}

var keywordTrie = buildKeywordTrie()

// REQ001151: branchless operator dispatch via 256-entry lookup table.
// opTable[c] holds the TokenType for single-char operator c.
// Multi-char operators (<, >, |, !) are handled by peek + dispatch.
// Unassigned entries are 0 (non-operator; T_EOF is only at index 0).
type opEntry struct {
	tok    TokenType
	lexeme string
}

var opTable [256]opEntry

func init() {
	opTable[0] = opEntry{tok: T_EOF}
	opTable['='] = opEntry{tok: T_EQ, lexeme: "="}
	opTable['<'] = opEntry{tok: T_LT, lexeme: "<"}
	opTable['>'] = opEntry{tok: T_GT, lexeme: ">"}
	opTable['+'] = opEntry{tok: T_PLUS, lexeme: "+"}
	opTable['-'] = opEntry{tok: T_MINUS, lexeme: "-"}
	opTable['*'] = opEntry{tok: T_STAR, lexeme: "*"}
	opTable['/'] = opEntry{tok: T_SLASH, lexeme: "/"}
	opTable['('] = opEntry{tok: T_LPAREN, lexeme: "("}
	opTable[')'] = opEntry{tok: T_RPAREN, lexeme: ")"}
	opTable[','] = opEntry{tok: T_COMMA, lexeme: ","}
	opTable['.'] = opEntry{tok: T_DOT, lexeme: "."}
	opTable[';'] = opEntry{tok: T_SEMICOLON, lexeme: ";"}
	opTable[':'] = opEntry{tok: T_COLON, lexeme: ":"}
	opTable['?'] = opEntry{tok: T_BIND, lexeme: "?"}
	opTable['&'] = opEntry{tok: T_BITAND, lexeme: "&"}
	opTable['|'] = opEntry{tok: T_BITOR, lexeme: "|"}
	opTable['^'] = opEntry{tok: T_BITXOR, lexeme: "^"}
	opTable['~'] = opEntry{tok: T_BITNOT, lexeme: "~"}
	opTable['%'] = opEntry{tok: T_MOD, lexeme: "%"}
	opTable['!'] = opEntry{tok: T_ERROR, lexeme: ""}
}

func buildKeywordTrie() *keywordNode {
	// Source list — must stay in sync with the historical
	// keywords map (now removed). Each entry is a keyword
	// token mapping.
	pairs := []struct {
		k string
		v TokenType
	}{
		{"CREATE", T_CREATE},
		{"DROP", T_DROP},
		{"INSERT", T_INSERT},
		{"INTO", T_INTO},
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
		{"GLOB", T_GLOB},
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
		{"CHECK", T_CHECK},
		{"UNIQUE", T_UNIQUE},
		{"INTEGER", T_INT_KW},
		{"INT", T_INT_KW},
		{"BIGINT", T_BIGINT},
		{"FLOAT", T_FLOAT_KW},
		{"REAL", T_FLOAT_KW},
		{"DOUBLE", T_FLOAT_KW},
		{"BOOL", T_BOOL},
		{"BOOLEAN", T_BOOL},
		{"TEXT", T_TEXT},
		{"BLOB", T_BLOB},
		{"VARCHAR", T_VARCHAR},
		{"TIMESTAMP", T_TIMESTAMP},
		{"NUMERIC", T_NUMERIC},
		{"DATE", T_DATE},
		{"TIME", T_TIME},
		{"JSON", T_JSON},
		{"DECIMAL", T_DECIMAL},
		{"VALUES", T_VALUES},
		{"SET", T_SET},
		{"ORDER", T_ORDER},
		{"JOIN", T_JOIN},
		{"LEFT", T_LEFT},
		{"RIGHT", T_RIGHT},
		{"INNER", T_INNER},
		{"CROSS", T_CROSS},
	{"NATURAL", T_NATURAL}, // REQ001359
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
		{"EXISTS", T_EXISTS},
		{"ANALYZE", T_ANALYZE},
		{"VACUUM", T_VACUUM},
		{"TRUNCATE", T_TRUNCATE},
		{"REINDEX", T_REINDEX},
		{"BEFORE", T_BEFORE},
		{"AFTER", T_AFTER},
		{"INSTEAD", T_INSTEAD},
		{"OF", T_OF},
		{"FOR", T_FOR},
		{"EACH", T_EACH},
		{"OUTER", T_OUTER},
		{"FULL", T_FULL},
		{"INTERVAL", T_INTERVAL},
		{"OVER", T_OVER},
		{"PARTITION", T_PARTITION},
		{"ROWS", T_ROWS},
		{"RANGE", T_RANGE},
		{"PRECEDING", T_PRECEDING},
		{"FOLLOWING", T_FOLLOWING},
		{"CURRENT", T_CURRENT},
		{"UNBOUNDED", T_UNBOUNDED},
		{"ROW_NUMBER", T_ROW_NUMBER},
		{"RANK", T_RANK},
		{"DENSE_RANK", T_DENSE_RANK},
		{"LAG", T_LAG},
		{"LEAD", T_LEAD},
		{"FIRST_VALUE", T_FIRST_VALUE},
		{"LAST_VALUE", T_LAST_VALUE},
		{"NTH_VALUE", T_NTH_VALUE},
		{"ROW", T_ROW},
		{"TRANSACTION", T_TRANSACTION},
		{"ISOLATION", T_ISOLATION},
		{"LEVEL", T_LEVEL},
		{"COALESCE", T_COALESCE},
		{"NULLIF", T_NULLIF},
		{"UNION", T_UNION},
		{"INTERSECT", T_INTERSECT},
		{"EXCEPT", T_EXCEPT},
		{"ALL", T_ALL},
		{"AUTOINCREMENT", T_AUTOINCREMENT},
		{"READ", T_READ},
		{"COMMITTED", T_COMMITTED},
		{"UNCOMMITTED", T_UNCOMMITTED},
		{"REPEATABLE", T_REPEATABLE},
		{"SERIALIZABLE", T_SERIALIZABLE},
		{"VIEW", T_VIEW},
		{"TRIGGER", T_TRIGGER},
		{"TEMP", T_TEMP},
		{"TEMPORARY", T_TEMPORARY},
		{"ALTER", T_ALTER},
		{"COLUMN", T_COLUMN},
		{"ADD", T_ADD},
		{"RENAME", T_RENAME},
		{"FETCH", T_FETCH},
		{"FIRST", T_FIRST},
		{"NEXT", T_NEXT},
		{"LAST", T_LAST},
		{"ONLY", T_ONLY},
		{"REFERENCES", T_REFERENCES},
		{"FOREIGN", T_FOREIGN},
		{"CASCADE", T_CASCADE},
		{"RESTRICT", T_RESTRICT},
		{"NO", T_NO},
		{"ACTION", T_ACTION},
		{"PRAGMA", T_PRAGMA},
		{"CAST", T_CAST},
		{"TRUE", T_TRUE},
		{"FALSE", T_FALSE},
		{"EXPLAIN", T_EXPLAIN},
		{"QUERY", T_QUERY},
		{"PLAN", T_PLAN},
		{"RETURNING", T_RETURNING},
		{"CONFLICT", T_CONFLICT},
		{"DO", T_DO},
		{"DIV", T_DIV},
		{"NOTHING", T_NOTHING},
		{"EXCLUDED", T_EXCLUDED},
		{"WITH", T_WITH},
		{"SAVEPOINT", T_SAVEPOINT},
		{"RELEASE", T_RELEASE},
		{"TO", T_TO},
		{"DEFERRED", T_DEFERRED},
		{"IMMEDIATE", T_IMMEDIATE},
		{"EXCLUSIVE", T_EXCLUSIVE},
		{"RAISE", T_RAISE},
		{"ESCAPE", T_ESCAPE},
		{"INDEXED", T_INDEXED},
		{"MATCH", T_MATCH},
		{"DEFERRABLE", T_DEFERRABLE},
		{"INITIALLY", T_INITIALLY},
		{"COLLATE", T_COLLATE},
		{"ATTACH", T_ATTACH},
		{"DETACH", T_DETACH},
		{"MATERIALIZED", T_MATERIALIZED},
		{"REFRESH", T_REFRESH},
		{"FILTER", T_FILTER},
		{"EXCLUDE", T_EXCLUDE},
		{"OTHERS", T_OTHERS},
		{"TIES", T_TIES},
		{"GROUPS", T_GROUPS},
	}
	root := &keywordNode{}
	for _, p := range pairs {
		cur := root
		for i := 0; i < len(p.k); i++ {
			idx := keywordCharIndex(p.k[i])
			if idx < 0 {
				panic("buildKeywordTrie: invalid keyword char: " + p.k)
			}
			ch := byte(idx)
			if idx == 26 {
				ch = '_'
			} else {
				ch = 'a' + byte(idx)
			}
			// Find existing child or append (children are kept sorted by ch).
			next := findChild(cur.children, ch)
			if next == nil {
				next = &keywordNode{}
				cur.children = append(cur.children, childEntry{ch: ch, next: next})
			}
			cur = next
		}
		cur.value = p.v
	}
	return root
}

// findChild returns the child node for byte ch from a childEntry
// slice. The slice is NOT sorted; we scan linearly. Most nodes
// have 1-3 children, so linear scan is optimal.
func findChild(cs []childEntry, ch byte) *keywordNode {
	for _, e := range cs {
		if e.ch == ch {
			return e.next
		}
	}
	return nil
}

// keywordCharIndex maps an ASCII letter (upper or lower) or '_'
// to a trie slot: 'a'..'z' → 0..25, '_' → 26. Returns -1 for
// any other byte (which terminates the lookup).
func keywordCharIndex(c byte) int {
	switch {
	case c >= 'a' && c <= 'z':
		return int(c - 'a')
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c == '_':
		return 26
	}
	return -1
}

// REQ001145: lookupKeyword walks the trie from input[0:] as
// far as possible, returning the number of bytes consumed by
// the matched keyword and its TokenType. Returns (0, T_EOF)
// when no match exists.
//
// Hot-path contract:
//   - allocation-free (no heap traffic)
//   - one byte compare per input char + linear scan of tiny
//     child slice (typically 1-3 entries)
//   - case-insensitive: input bytes are folded inline
//
// The first terminal reached is returned — for the SQL keyword
// set, no two keywords share a common prefix where one is a
// strict prefix of the other (ROW vs ROW_NUMBER is the only
// pair, and lookupKeyword matches the shortest terminal first
// so the lexer's outer loop can extend the match if the next
// byte continues the ident).
func lookupKeyword(input string) (int, TokenType) {
	cur := keywordTrie
	matched := 0
	var (
		bestLen  int
		bestType TokenType
	)
	for i := 0; i < len(input); i++ {
		idx := keywordCharIndex(input[i])
		if idx < 0 {
			break
		}
		ch := byte(idx)
		if idx == 26 {
			ch = '_'
		} else {
			ch = 'a' + byte(idx)
		}
		next := findChild(cur.children, ch)
		if next == nil {
			// REQ001154: linear scan on sorted childEntry slice.
			// Most nodes have 1-3 children; linear scan beats
			// binary search for this range.
			break
		}
		cur = next
		matched++
		if cur.value != 0 {
			// First terminal wins. The lexer re-checks the
			// trailing byte to disambiguate prefix-of-keyword
			// idents (e.g. SELECTOR → SELECT then OR is
			// already not a continuation, so SELECT matches).
			bestLen = matched
			// REQ001154: keep the deepest terminal so that
			// prefix collisions (INT vs INTEGER) resolve to
			// the longer keyword when the input matches all of it.
			bestType = cur.value
		}
	}
	return bestLen, bestType
}

type Lexer struct {
	input string
	pos   int
	line  uint32
	col   uint32
	// REQ001141: token-start position snapshot. captureStart() is
	// called once per Next(); all scanner functions read these
	// fields instead of stashing their own copies.
	startLine uint32
	startCol  uint32
	// REQ001140: ring buffer for lookahead. Two slots hold the
	// next two upcoming tokens; Peek/Peek2 fill them lazily so
	// repeated peeks at the same position cost O(1).
	//
	// Invariant: buf[0].valid implies buf[0] holds the token
	// that Next() will return next. After Next() consumes it,
	// buf[1] (if valid) is shifted into buf[0], and buf[1] is
	// marked invalid so the next scan fills it.
	buf [2]peekSlot
}

type peekSlot struct {
	tok   Token
	valid bool
}

// REQ001153: lexerPool amortizes per-query Lexer allocation
// across many small SQL statements (OLTP workloads).
var lexerPool = sync.Pool{
	New: func() any { return &Lexer{} },
}

// GetLexer retrieves a Lexer from the pool, resetting it for the
// given input. Callers must return the Lexer to the pool via
// PutLexer when done.
func GetLexer(input string) *Lexer {
	l := lexerPool.Get().(*Lexer)
	l.input = input
	l.pos = 0
	l.line = 1
	l.col = 1
	l.startLine = 0
	l.startCol = 0
	l.buf[0].valid = false
	l.buf[1].valid = false
	return l
}

// PutLexer returns a Lexer to the pool. The caller must not use l
// after calling PutLexer.
func PutLexer(l *Lexer) {
	l.input = ""
	lexerPool.Put(l)
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

// REQ001140: scan performs one full tokenization step. It is the
// non-cached version of Next() — it does NOT consult the buffer.
// Callers that go through Next()/Peek()/Peek2() never see the
// buffer state; scan() is the single source of truth for what
// the next token at the current cursor is.
//
// REQ001146 forward-progress guarantee: scan() must always
// advance `l.pos` by at least 1 byte when returning a non-EOF
// token. The dispatch below verifies this invariant — if a
// chosen scanner returns without consuming the lead byte, we
// fall through to scanOperator which emits T_ERROR and advances
// one byte. Without this guard, hostile input (e.g. bare UTF-8
// continuation bytes that scanIdent rejects) causes infinite
// loops.
func (l *Lexer) scan() Token {
	l.skipWhitespaceAndComments()
	if l.pos >= len(l.input) {
		return Token{Type: T_EOF, Lexeme: "", Line: l.line, Col: l.col}
	}
	l.captureStart()
	startPos := l.pos
	c := l.peek()
	if c == '\'' {
		return l.scanString()
	}
	if c < 0x80 {
		if isASCIILetter(c) {
			tok := l.scanIdent()
			if l.pos == startPos {
				return l.scanOperator()
			}
			return tok
		}
		if isASCIIDigit(c) {
			tok := l.scanNumber()
			if l.pos == startPos {
				return l.scanOperator()
			}
			return tok
		}
	} else {
		// REQ001146: only treat the byte as an identifier start
		// when the full rune is a letter/digit/underscore.
		r, w := utf8.DecodeRuneInString(l.input[l.pos:])
		if w >= 2 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
			tok := l.scanIdent()
			if l.pos == startPos {
				return l.scanOperator()
			}
			return tok
		}
		if w >= 2 && unicode.IsDigit(r) {
			tok := l.scanNumber()
			if l.pos == startPos {
				return l.scanOperator()
			}
			return tok
		}
	}
	return l.scanOperator()
}

// REQ001140: Next() consumes the next token. It returns the head
// slot (which must be valid) and rotates the buffer so the
// peeked-ahead token becomes the new head.
func (l *Lexer) Next() Token {
	if !l.buf[0].valid {
		l.buf[0].tok = l.scan()
		l.buf[0].valid = true
	}
	tok := l.buf[0].tok
	// Rotate: buf[1] (the pre-peeked token) becomes the new
	// head. buf[1] is now invalid so the next Next() will scan
	// into it.
	l.buf[0] = l.buf[1]
	l.buf[1].valid = false
	return tok
}

// REQ001140: Peek() returns the next token without consuming it.
// On entry, ensure buf[0] holds the upcoming token; if not,
// scan it. After returning buf[0], pre-scan buf[1] so Peek2()
// can return immediately on a subsequent call.
func (l *Lexer) Peek() Token {
	if !l.buf[0].valid {
		l.buf[0].tok = l.scan()
		l.buf[0].valid = true
	}
	if !l.buf[1].valid && l.buf[0].tok.Type != T_EOF {
		l.buf[1].tok = l.scan()
		l.buf[1].valid = true
	}
	return l.buf[0].tok
}

// REQ001140: Peek2 returns the second upcoming token without
// consuming either. Mirrors the legacy semantics (returns EOF
// if the first token is EOF).
func (l *Lexer) Peek2() Token {
	if !l.buf[0].valid {
		l.buf[0].tok = l.scan()
		l.buf[0].valid = true
	}
	if l.buf[0].tok.Type == T_EOF {
		return l.buf[0].tok
	}
	if !l.buf[1].valid {
		l.buf[1].tok = l.scan()
		l.buf[1].valid = true
	}
	return l.buf[1].tok
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

// REQ001146: advanceRune decodes one full rune at the current
// cursor and advances by its UTF-8 width. Newline handling still
// runs once per byte (a multi-byte rune cannot contain '\n' in
// valid UTF-8). Returns the decoded rune and its byte width, or
// (utf8.RuneError, 1) on invalid input — degenerate bytes are
// consumed one at a time so the lexer makes forward progress.
func (l *Lexer) advanceRune() (rune, int) {
	if l.pos >= len(l.input) {
		return 0, 0
	}
	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	if w == 0 {
		return 0, 0
	}
	for i := 0; i < w; i++ {
		l.advance()
	}
	return r, w
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

func (l *Lexer) skipWhitespaceAndComments() {
	for {
		c := l.peek()
		if c == 0 {
			return
		}

		// REQ001149: bulk-scan ASCII space/tab runs without per-byte
		// function call overhead. These two characters never change
		// the line number, so we can advance col in bulk.
		if c == ' ' || c == '\t' {
			start := l.pos
			for l.pos < len(l.input) {
				b := l.input[l.pos]
				if b != ' ' && b != '\t' {
					break
				}
				l.pos++
			}
			l.col += uint32(l.pos - start)
			continue
		}

		// REQ001144: ASCII fast-path for other whitespace.
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
				// REQ001150: use stack buffer for short strings to avoid
				// heap allocation. Fall back to strings.Builder for
				// strings exceeding the buffer.
				const stackBufSize = 256
				var buf [stackBufSize]byte
				n := 0
				for i := start; i < l.pos-1; i++ {
					if l.input[i] == '\'' && i+1 < l.pos-1 && l.input[i+1] == '\'' {
						if n >= stackBufSize {
							// Overflow: switch to strings.Builder
							var sb strings.Builder
							sb.Grow(n + (l.pos - 1 - i))
							sb.WriteString(string(buf[:]))
							sb.WriteByte('\'')
							i++
							for ; i < l.pos-1; i++ {
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
						buf[n] = '\''
						n++
						i++
					} else {
						if n >= stackBufSize {
							// Overflow: switch to strings.Builder
							var sb strings.Builder
							sb.Grow(n + (l.pos - 1 - i))
							sb.WriteString(string(buf[:]))
							sb.WriteByte(l.input[i])
							for i++; i < l.pos-1; i++ {
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
						buf[n] = l.input[i]
						n++
					}
				}
				v := string(buf[:n])
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
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		// REQ001144: ASCII fast-path for the inner ident loop.
		if c < 0x80 {
			if !isASCIILetterDigit(c) {
				break
			}
			l.advance()
			continue
		}
		// REQ001146: non-ASCII byte must form a complete UTF-8
		// rune. Decode the rune first; only consume its full
		// width when it is a letter/digit/underscore. Continuation
		// bytes (0x80..0xBF) are not valid stand-alone runes
		// and must terminate the ident without advancing —
		// otherwise we would orphan them as degenerate bytes.
		r, w := utf8.DecodeRuneInString(l.input[l.pos:])
		if w < 2 {
			// REQ001146 fallback: invalid-UTF-8 lead byte or
			// stray continuation byte. The pre-fix lexer
			// accepted these as single-char idents via
			// unicode.IsLetter(rune(c)); preserve that
			// behaviour when the byte is a letter/digit/_
			// and break otherwise. scan() guarantees forward
			// progress by routing non-ident bytes through
			// scanOperator which emits T_ERROR + advance.
			if !unicode.IsLetter(rune(c)) && !unicode.IsDigit(rune(c)) && c != '_' {
				break
			}
			l.advance()
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		l.advanceRune()
	}

ident := l.input[start:l.pos]

	// REQ001145: trie-based keyword lookup.
	if kwLen, kwType := lookupKeyword(ident); kwLen > 0 && kwLen == len(ident) {
		// REQ002108: for aggregate/function keywords, use canonical
		// uppercase Lexeme from tokenTypeNames so the parser doesn't
		// need to call strings.ToUpper. For other keywords, keep the
		// original input text (lexer preserves original case).
		switch kwType {
		case T_COUNT, T_SUM, T_AVG, T_MIN, T_MAX:
			return Token{Type: kwType, Lexeme: tokenTypeNames[kwType], Line: startLine, Col: startCol}
		}
		return Token{Type: kwType, Lexeme: ident, Line: startLine, Col: startCol}
	}

	// REQ002108: pre-lowercase identifiers so the parser doesn't need
	// to call strings.ToLower on every T_IDENT.
	return Token{Type: T_IDENT, Lexeme: toLowerIdent(ident), Line: startLine, Col: startCol}
}

// toLowerIdent returns a lowercased copy of ident. REQ002108.
// Uses a stack buffer for small identifiers (<64 bytes) to avoid heap alloc.
func toLowerIdent(ident string) string {
	// Fast path: already lowercase.
	allLower := true
	for i := 0; i < len(ident); i++ {
		if ident[i] >= 'A' && ident[i] <= 'Z' {
			allLower = false
			break
		}
	}
	if allLower {
		return ident
	}
	var buf [64]byte
	if len(ident) <= len(buf) {
		for i := 0; i < len(ident); i++ {
			c := ident[i]
			if c >= 'A' && c <= 'Z' {
				buf[i] = c + 32
			} else {
				buf[i] = c
			}
		}
		return string(buf[:len(ident)])
	}
	// Fallback for long identifiers.
	out := make([]byte, len(ident))
	for i := 0; i < len(ident); i++ {
		c := ident[i]
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 32
		} else {
			out[i] = c
		}
	}
	return string(out)
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
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		// REQ001144: ASCII fast-path for the inner number loop.
		if c < 0x80 {
			if !isASCIIDigit(c) && c != '.' {
				break
			}
		} else {
			// REQ001146: multi-byte non-ASCII digit runes (rare
			// but legal) must be consumed in full.
			r, w := utf8.DecodeRuneInString(l.input[l.pos:])
			if w == 0 || (!unicode.IsDigit(r) && r != '.') {
				break
			}
			// Reject non-ASCII dots — only ASCII '.' is the
			// decimal separator.
			if r == '.' {
				if hasDot {
					break
				}
				hasDot = true
				l.advanceRune()
				continue
			}
			l.advanceRune()
			continue
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
	if c == 0 {
		return Token{Type: T_ERROR, Lexeme: "", LitErr: ErrUnexpectedChar, Line: startLine, Col: startCol}
	}

	// REQ001151: multi-char operators need second-byte peek.
	switch c {
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
	case '|':
		if l.peek() == '|' {
			l.advance()
			return Token{Type: T_CONCAT, Lexeme: "||", Line: startLine, Col: startCol}
		}
		return Token{Type: T_BITOR, Lexeme: "|", Line: startLine, Col: startCol}
	case '!':
		if l.peek() == '=' {
			l.advance()
			return Token{Type: T_NE, Lexeme: "!=", Line: startLine, Col: startCol}
		}
		return Token{Type: T_ERROR, Lexeme: "", LitErr: ErrUnexpectedChar, Line: startLine, Col: startCol}
	}

	// REQ001151: single-char operators via branchless table lookup.
	entry := opTable[c]
	if entry.tok == 0 {
		return Token{Type: T_ERROR, Lexeme: "", LitErr: ErrUnexpectedChar, Line: startLine, Col: startCol}
	}
	return Token{Type: entry.tok, Lexeme: entry.lexeme, Line: startLine, Col: startCol}
}
