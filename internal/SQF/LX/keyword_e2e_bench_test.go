package LX

import (
	"testing"
)

// REQ001145: end-to-end lexer benchmark with the trie in
// place. Compared to the previous map + strings.ToUpper path,
// ToUpper allocations are gone.
func BenchmarkLexer_KeywordLookup_EndToEnd(b *testing.B) {
	// A realistic SQL fragment with many keywords + some
	// idents (mix of hits and misses).
	input := "SELECT id, name FROM users WHERE active = 1 AND age > 30 ORDER BY id LIMIT 100"
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
