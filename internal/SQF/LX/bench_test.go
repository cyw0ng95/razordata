package LX

import "testing"

func BenchmarkLexerNext(b *testing.B) {
	input := "SELECT id, name, age FROM users WHERE age > 30 AND age < 50 ORDER BY age LIMIT 100"
	for i := 0; i < b.N; i++ {
		l := NewLexer(input)
		for {
			tok := l.Next()
			if tok.Type == T_EOF {
				break
			}
		}
	}
}

func BenchmarkLexerLongInput(b *testing.B) {
	input := ""
	for i := 0; i < 100; i++ {
		input += "SELECT id, name, age FROM users WHERE age > 30 AND age < 50 ORDER BY age LIMIT 100; "
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := NewLexer(input)
		for {
			tok := l.Next()
			if tok.Type == T_EOF {
				break
			}
		}
	}
}

// REQ001141: microbenchmark for the advance() hot path. Verifies
// the per-byte cost is dominated by the bounds check + branch on
// newline. Run with: go test -bench=BenchmarkLexerAdvance -benchmem ./internal/SQF/LX
func BenchmarkLexerAdvance(b *testing.B) {
	input := "SELECT id, name, age FROM users WHERE age > 30 AND age < 50 ORDER BY age LIMIT 100"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := NewLexer(input)
		for l.pos < len(l.input) {
			l.advance()
		}
	}
}

// REQ001144: ASCII-only throughput benchmark. Covers identifier,
// number, operator, and whitespace dispatch on a typical SQL query.
// Run with: go test -bench=BenchmarkLexerASCII -benchmem ./internal/SQF/LX
func BenchmarkLexerASCII(b *testing.B) {
	input := "SELECT id, name, age FROM users WHERE age > 30 AND age < 50 ORDER BY age LIMIT 100"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := NewLexer(input)
		for {
			tok := l.Next()
			if tok.Type == T_EOF {
				break
			}
		}
	}
}

// REQ001142: scanNumber throughput benchmark. Verifies that the
// switch from strings.Builder to direct string slicing yields zero
// heap allocations per integer/float token. Run with:
// go test -bench=BenchmarkLexerScanNumber -benchmem ./internal/SQF/LX
func BenchmarkLexerScanNumber(b *testing.B) {
	cases := []struct {
		name  string
		input string
	}{
		{"int", "1234567890"},
		{"float", "12345.6789"},
		{"mixed", "1 22 333 4444 55555 666666 7777777 88888888 999999999 0"},
	}
	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l := NewLexer(c.input)
				for {
					tok := l.Next()
					if tok.Type == T_EOF {
						break
					}
				}
			}
		})
	}
}

// REQ001143: literal-allocation benchmark. Verifies that int / float /
// string tokens no longer box their payload into the `any` interface
// header — the typed fields LitInt / LitFloat / LitStr are pure
// scalars. Run with:
// go test -bench=BenchmarkLexerLiteralAlloc -benchmem ./internal/SQF/LX
func BenchmarkLexerLiteralAlloc(b *testing.B) {
	input := "SELECT 1, 2.5, 'three', 4, 5.5, 'six' FROM t WHERE id = 7"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := NewLexer(input)
		for {
			tok := l.Next()
			if tok.Type == T_EOF {
				break
			}
		}
	}
}

// REQ001140: Peek throughput benchmark. The parser calls Peek() ~20
// times per query and Peek2() ~5 times. After the ring-buffer
// refactor, repeated peeks at the same position are O(1) — only the
// first call scans; subsequent calls return the cached head slot.
// Run with: go test -bench=BenchmarkLexerPeek -benchmem ./internal/SQF/LX
func BenchmarkLexerPeek(b *testing.B) {
	input := "SELECT id, name FROM users WHERE age > 30 AND active = 1"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := NewLexer(input)
		for j := 0; j < 10; j++ {
			_ = l.Peek()
			_ = l.Peek2()
		}
		// drain to EOF to reset for next iteration
		for {
			tok := l.Next()
			if tok.Type == T_EOF {
				break
			}
		}
	}
}
