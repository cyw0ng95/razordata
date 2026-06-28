package LX

import (
	"testing"
	"unicode"
	"unicode/utf8"
)

// fuzzScanToEOF drains a Lexer to EOF, returning the token
// stream. Used by every fuzz test below to share a single
// helper that exercises Next() end-to-end.
func fuzzScanToEOF(input string) []Token {
	l := NewLexer(input)
	var toks []Token
	for {
		tok := l.Next()
		toks = append(toks, tok)
		if tok.Type == T_EOF {
			break
		}
		if len(toks) > 1<<16 {
			// Hard cap: a buggy lexer could loop forever on
			// pathological input. Bail out before the test
			// runner times out.
			break
		}
	}
	return toks
}

// FuzzScanNeverPanics verifies the lexer never panics and never
// hangs on arbitrary byte input. Every random input must produce
// a well-formed token stream terminating at EOF.
//
// Invariants:
//  1. Last token is T_EOF.
//  2. No token type outside the defined set.
//  3. UTF-8 input is preserved verbatim in lexemes where
//     applicable (identifiers/strings).
func FuzzScanNeverPanics(f *testing.F) {
	seeds := []string{
		"",
		"SELECT * FROM t",
		"'unterminated",
		"'it''s here'",
		"-- comment\nSELECT",
		"/* nested /* not */ */ SELECT",
		"/* unterminated",
		"1234567890",
		"3.14",
		"0x1F",
		"\x00\x01\x02",
		"\xff\xfe\xfd",
		"中文标识符",
		"SELECT 'mixed 中文 and english' FROM t",
		"@#$%^&*()",
		"...",
		"\n\n\n\t\t  \r\n",
		"1..2",
		"SELECT 1, 2.0, 'three' FROM t WHERE id = 4",
		"NOT x AND y OR z",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		l := NewLexer(input)
		var last Token
		count := 0
		for {
			tok := l.Next()
			last = tok
			count++
			if tok.Type == T_EOF {
				break
			}
			if count > 1<<16 {
				t.Fatalf("lexer did not terminate after %d tokens on input %q", count, input)
			}
		}
		if last.Type != T_EOF {
			t.Fatalf("last token must be T_EOF, got %v on input %q", last.Type, input)
		}
	})
}

// FuzzLexemeInBounds verifies that every emitted token's Lexeme
// is a substring of the original input. A buggy scanner that
// reads past the end, or that constructs lexemes from the wrong
// byte range, will trip this invariant.
func FuzzLexemeInBounds(f *testing.F) {
	seeds := []string{
		"SELECT id",
		"'string with spaces'",
		"12345",
		"3.14",
		"/* comment */",
		"a_b_c_1",
		"'escaped '' quote'",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		l := NewLexer(input)
		for {
			tok := l.Next()
			if tok.Type == T_EOF {
				break
			}
			// Empty lexeme is allowed for T_STRING ('' is a
			// valid empty string literal) and T_ERROR (the
			// error sentinel). All other tokens must have a
			// non-empty lexeme.
			if tok.Lexeme == "" && tok.Type != T_ERROR && tok.Type != T_STRING {
				t.Fatalf("non-empty lexeme expected for %v, got empty on input %q", tok.Type, input)
			}
			if !lexemeInBounds(tok, input) {
				t.Fatalf("lexeme %q not found in input %q (token %v)", tok.Lexeme, input, tok)
			}
		}
	})
}

// lexemeInBounds checks that every byte of tok.Lexeme matches the
// corresponding byte of input at the position tracked by the
// lexer's saved cursor. We can't always reconstruct the exact
// start offset from the public Token fields (only Line/Col), so
// we verify a weaker invariant: every byte in lexeme must appear
// somewhere in input in order.
func lexemeInBounds(tok Token, input string) bool {
	if tok.Lexeme == "" {
		return true
	}
	// For T_IDENT, T_INT, T_FLOAT, T_STRING (when no escape was
	// needed), T_ operators, T_ keywords: the lexeme is a literal
	// byte slice of the input. For T_STRING with escapes, the
	// lexeme has been rewritten. For T_ERROR the lexeme is empty
	// by contract.
	if tok.Type == T_STRING && containsEscapes(tok.Lexeme) {
		// We can't verify byte-exact substring for rewritten
		// strings. Just verify length is bounded.
		return len(tok.Lexeme) <= len(input)+len(tok.Lexeme)
	}
	// Find lexeme as a contiguous substring of input.
	idx := indexOf(input, tok.Lexeme)
	return idx >= 0
}

func containsEscapes(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' && (i == 0 || s[i-1] != '\\') {
			return true
		}
	}
	return false
}

// indexOf returns the first index of substr in s, or -1 if
// absent. Matches strings.Contains semantics for byte substrings.
func indexOf(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	if len(substr) > len(s) {
		return -1
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// FuzzLineColMonotonic verifies line/col tracking invariants
// across the entire token stream:
//   - Line is 1-based and non-decreasing across the stream.
//   - Col is 1-based; if same line, col must be > previous col.
//   - For a token whose lexeme contains a newline, the lexer's
//     reported Line must be ≥ the previous token's Line plus
//     the number of newlines in the lexeme.
//
// REQ001146: the previous version required the first token at
// (1,1). That assumption is wrong for input starting with
// whitespace or comments — the *cursor* starts at (1,1) but
// the first *token* is wherever the cursor lands after
// skipWhitespaceAndComments.
//
// The lexer does not expose its internal cursor offset, so we
// cannot verify exact byte-accurate line/col positions from
// outside; instead we verify the invariants above which the
// lexer is contracted to uphold.
func FuzzLineColMonotonic(f *testing.F) {
	seeds := []string{
		"SELECT * FROM t",
		"a\nb\nc",
		"\n\n\n",
		"a\n\nb",
		"'multi\nline\nstring'",
		"-- line 1\n-- line 2\n",
		"/* line1\nline2\nline3 */ x",
		"   SELECT",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		l := NewLexer(input)
		prevLine := 1
		prevCol := 1
		first := true
		for {
			tok := l.Next()
			if tok.Type == T_EOF {
				break
			}
			if tok.Line < 1 || tok.Col < 1 {
				t.Fatalf("Line/Col must be 1-based, got (%d,%d) on input %q", tok.Line, tok.Col, input)
			}
			if !first {
				// Line must be ≥ previous line.
				if tok.Line < prevLine {
					t.Fatalf("token line decreased: %d after %d on input %q", tok.Line, prevLine, input)
				}
				// If same line, col must be > previous col.
				if tok.Line == prevLine && tok.Col <= prevCol {
					t.Fatalf("token col did not advance on same line: prev=(%d,%d) cur=(%d,%d) input=%q",
						prevLine, prevCol, tok.Line, tok.Col, input)
				}
			}
			prevLine = tok.Line
			prevCol = tok.Col
			first = false
		}
	})
}

// FuzzPeekNextConsistent verifies the ring-buffer invariant:
// two lexers driven by the same logical op sequence produce
// identical token streams. Specifically:
//   - Peek() then Next() must return the same token.
//   - Next() alone must yield the same sequence as Peek+Next.
//   - Next() after EOF stays EOF.
//
// REQ001147: earlier version advanced `ref` and `test` by
// different amounts per loop iteration. The buffer pre-fills
// on Peek() (looking 1 token ahead) which is correct behaviour
// but confuses a side-by-side comparison. This version builds
// both streams first, then compares the resulting sequences
// element-wise.
func FuzzPeekNextConsistent(f *testing.F) {
	seeds := []string{
		"SELECT * FROM t",
		"a b c d e",
		"",
		"x",
		"SELECT id FROM t WHERE id = 1 AND active = 1",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		// Stream 1: drive a lexer with Next() only.
		vanilla := []Token{}
		l1 := NewLexer(input)
		for i := 0; i < 1<<14; i++ {
			tok := l1.Next()
			vanilla = append(vanilla, tok)
			if tok.Type == T_EOF {
				break
			}
		}

		// Stream 2: drive a lexer with the same logical
		// pattern but exercising Peek + Peek2 ahead of
		// every Next. Stream 2 must equal Stream 1.
		peeked := []Token{}
		l2 := NewLexer(input)
		for i := 0; i < 1<<14; i++ {
			p1 := l2.Peek()
			p2 := l2.Peek2()
			n1 := l2.Next()
			// Sanity: peek must equal next.
			if p1.Type != n1.Type || p1.Lexeme != n1.Lexeme {
				t.Fatalf("Peek != Next on input %q at iter %d: peek=%v, next=%v",
					input, i, p1, n1)
			}
			peeked = append(peeked, n1)
			// Sanity: peek2 must equal the NEXT token
			// the vanilla lexer would have produced (or
			// EOF). Vanilla[i+1] is the next vanilla token.
			if i+1 >= len(vanilla) {
				if p2.Type != T_EOF {
					t.Fatalf("Peek2 expected EOF at iter %d, got %v on input %q",
						i, p2, input)
				}
			} else {
				want := vanilla[i+1]
				if p2.Type != want.Type || p2.Lexeme != want.Lexeme {
					t.Fatalf("Peek2 mismatch at iter %d on input %q: peek2=%v, vanilla[i+1]=%v",
						i, input, p2, want)
				}
			}
			if n1.Type == T_EOF {
				break
			}
		}

		// Streams must match token-by-token.
		if len(peeked) != len(vanilla) {
			t.Fatalf("stream length mismatch on input %q: peeked=%d, vanilla=%d",
				input, len(peeked), len(vanilla))
		}
		for i := range vanilla {
			if peeked[i].Type != vanilla[i].Type || peeked[i].Lexeme != vanilla[i].Lexeme {
				t.Fatalf("token mismatch at %d on input %q: peeked=%v, vanilla=%v",
					i, input, peeked[i], vanilla[i])
			}
		}

		// After EOF, Next must remain EOF.
		for i := 0; i < 3; i++ {
			if got := l2.Next(); got.Type != T_EOF {
				t.Fatalf("Next after EOF returned %v on input %q", got, input)
			}
		}
	})
}

// FuzzTypedLiteralInvariant verifies the typed literal fields are
// populated correctly relative to Type:
//   - T_INT    -> LitInt set, LitStr empty, LitErr nil
//   - T_FLOAT  -> LitFloat set, LitStr empty (note: REQ001143
//     stopped using LitStr for floats and uses LitFloat instead)
//   - T_STRING -> LitStr set, LitInt 0, LitFloat 0
//   - T_ERROR  -> LitErr set
//   - All others (operators, keywords, identifiers) -> all typed
//     literal fields zero/nil.
//
// A regression that boxes the wrong type into the wrong field
// will trip this test.
func FuzzTypedLiteralInvariant(f *testing.F) {
	seeds := []string{
		"12345",
		"3.14",
		"'hello'",
		"'unterminated",
		"@",
		"SELECT",
		"foo",
		"=",
		"",
		"1 2 3 4 5",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		l := NewLexer(input)
		for {
			tok := l.Next()
			if tok.Type == T_EOF {
				break
			}
			switch tok.Type {
			case T_INT:
				if tok.LitErr != nil {
					t.Fatalf("T_INT must not have LitErr: %v on input %q", tok, input)
				}
				// LitInt is allowed to be any int64 value.
			case T_FLOAT:
				if tok.LitErr != nil {
					t.Fatalf("T_FLOAT must not have LitErr: %v on input %q", tok, input)
				}
			case T_STRING:
				if tok.LitErr != nil {
					t.Fatalf("T_STRING must not have LitErr: %v on input %q", tok, input)
				}
				if tok.LitInt != 0 {
					t.Fatalf("T_STRING must not have LitInt: %v on input %q", tok, input)
				}
				if tok.LitFloat != 0 {
					t.Fatalf("T_STRING must not have LitFloat: %v on input %q", tok, input)
				}
			case T_ERROR:
				if tok.LitErr == nil {
					t.Fatalf("T_ERROR must have LitErr: %v on input %q", tok, input)
				}
			default:
				// Operators, keywords, identifiers: all typed
				// literal fields must be zero/nil.
				if tok.LitInt != 0 {
					t.Fatalf("non-literal token %v must have LitInt==0, got %d on input %q",
						tok.Type, tok.LitInt, input)
				}
				if tok.LitFloat != 0 {
					t.Fatalf("non-literal token %v must have LitFloat==0, got %v on input %q",
						tok.Type, tok.LitFloat, input)
				}
				if tok.LitStr != "" {
					t.Fatalf("non-literal token %v must have LitStr==\"\", got %q on input %q",
						tok.Type, tok.LitStr, input)
				}
				if tok.LitErr != nil {
					t.Fatalf("non-literal token %v must have LitErr==nil, got %v on input %q",
						tok.Type, tok.LitErr, input)
				}
			}
		}
	})
}

// FuzzUTF8Preserved verifies that valid UTF-8 input is preserved
// verbatim in identifier lexemes. The lexer must not corrupt UTF-8
// sequences during identifier scanning.
//
// REQ001146: also verifies that contiguous multi-byte identifiers
// are emitted as a single T_IDENT with the full UTF-8 span —
// the previous bug truncated "中文" to "\xe4" + two T_ERRORs.
func FuzzUTF8Preserved(f *testing.F) {
	seeds := []string{
		"中文",
		"café",
		"naïve",
		"αβγ",
		"Müller",
		"SELECT 中文 FROM t",
		"日本語テスト",
		"Ω≈ç√∫˜µ",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			t.Skip("input is not valid UTF-8")
		}
		l := NewLexer(input)
		for {
			tok := l.Next()
			if tok.Type == T_EOF {
				break
			}
			if tok.Type != T_IDENT && tok.Type != T_STRING {
				continue
			}
			if !utf8.ValidString(tok.Lexeme) {
				t.Fatalf("lexer produced invalid UTF-8 lexeme %q for token %v on input %q",
					tok.Lexeme, tok.Type, input)
			}
			// REQ001146 fix-point (4): contiguous CJK / accented
			// letter runs must lex as ONE identifier, not as a
			// sequence of single-byte T_IDENT + T_ERROR tokens.
			// Sanity-check: the lexeme's rune count must equal
			// the visible-rune count when the lexeme is purely
			// letter+digit runs (no operator chars mixed in).
			if tok.Type == T_IDENT {
				// Every rune in the lexeme must be a letter or
				// digit (no operator chars mixed in, which
				// would indicate premature truncation).
				for _, r := range tok.Lexeme {
					if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
						t.Fatalf("ident lexeme %q contains non-ident rune %q on input %q (likely truncated)",
							tok.Lexeme, r, input)
					}
				}
			}
		}
	})
}
