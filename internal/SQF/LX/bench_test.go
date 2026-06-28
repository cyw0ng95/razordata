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
