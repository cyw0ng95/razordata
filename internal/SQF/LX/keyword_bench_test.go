package LX

import (
	"sort"
	"testing"
)

// REQ001145: trie lookup benchmark. The trie is the production
// path; this measures its cost on a representative sample of
// keywords and idents (some hits, some misses).
func BenchmarkLexer_KeywordLookup_Trie(b *testing.B) {
	samples := []string{
		"SELECT", "FROM", "WHERE", "PRIMARY", "FOREIGN",
		"myTable", "col_name", "x", "INDEXED", "INSIDE",
		"INT", "INTEGER", "ROW", "ROW_NUMBER", "SELECTOR",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, s := range samples {
			_, _ = lookupKeyword(s)
		}
	}
}

// REQ001145: trie lookup benchmark restricted to keyword hits
// only — measures the hot path of the lexer.
func BenchmarkLexer_KeywordLookup_Trie_HitsOnly(b *testing.B) {
	samples := []string{
		"SELECT", "FROM", "WHERE", "INSERT", "UPDATE", "DELETE",
		"CREATE", "DROP", "TABLE", "INDEX", "PRIMARY", "FOREIGN",
		"REFERENCES", "AND", "OR", "NOT", "NULL", "IS", "IN",
		"BETWEEN", "LIKE", "GLOB", "JOIN", "LEFT", "RIGHT",
		"INNER", "OUTER", "FULL", "CROSS", "ON", "USING",
		"GROUP", "BY", "HAVING", "ORDER", "ASC", "DESC",
		"LIMIT", "OFFSET", "UNION", "INTERSECT", "EXCEPT",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, s := range samples {
			_, _ = lookupKeyword(s)
		}
	}
}

// REQ001145: trie lookup benchmark for ident misses — every
// sample is a non-keyword, exercising the failure path that
// returns (0, T_EOF).
func BenchmarkLexer_KeywordLookup_Trie_MissesOnly(b *testing.B) {
	samples := []string{
		"myTable", "col_name", "x", "y", "foo", "bar",
		"select_id", "from_clause", "where_cond", "indexed",
		"primary_key", "foreign_keys", "selecta", "fromt",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, s := range samples {
			_, _ = lookupKeyword(s)
		}
	}
}

// REQ001145: alternative baseline — sorted slice + binary
// search on a pre-built sorted list of keywords. Used for
// comparison: trie vs sorted slice lookup.
//
// Both implementations do not require an upper-cased input;
// the trie folds inline and the sorted slice here stores
// uppercase keys but is searched by upper-casing the input —
// which is exactly what the original map-based code did. We
// keep ToUpper out of the inner loop here for a fair
// comparison of lookup itself.
func BenchmarkLexer_KeywordLookup_SortedSlice(b *testing.B) {
	type entry struct {
		k string
		v TokenType
	}
	entries := make([]entry, 0, 200)
	// Reconstruct from trie via direct enumeration: walk the
	// trie and collect terminals. This keeps the benchmark
	// independent of the keyword source list.
	var walk func(n *keywordNode, prefix []byte)
	walk = func(n *keywordNode, prefix []byte) {
		if n.value != 0 {
			entries = append(entries, entry{string(prefix), n.value})
		}
		for i, c := range n.children {
			if c != nil {
				ch := byte('a')
				if i == 26 {
					ch = '_'
				} else {
					ch = byte('a' + i)
				}
				walk(c, append(prefix, ch))
			}
		}
	}
	walk(keywordTrie, nil)
	sort.Slice(entries, func(i, j int) bool { return entries[i].k < entries[j].k })
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.k
	}
	samples := []string{
		"SELECT", "FROM", "WHERE", "INSERT", "UPDATE", "DELETE",
		"CREATE", "DROP", "TABLE", "INDEX", "PRIMARY", "FOREIGN",
		"REFERENCES", "AND", "OR", "NOT", "NULL", "IS", "IN",
		"BETWEEN", "LIKE", "GLOB", "JOIN", "LEFT", "RIGHT",
		"INNER", "OUTER", "FULL", "CROSS", "ON", "USING",
		"GROUP", "BY", "HAVING", "ORDER", "ASC", "DESC",
		"LIMIT", "OFFSET", "UNION", "INTERSECT", "EXCEPT",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, s := range samples {
			_ = sort.SearchStrings(keys, s)
		}
	}
}
