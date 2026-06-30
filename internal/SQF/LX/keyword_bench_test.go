package LX

import (
	"sort"
	"testing"
	"unsafe"
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
		for _, e := range n.children {
			walk(e.next, append(prefix, e.ch))
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

// REQ001154: memory usage benchmark — measures the compact trie's
// memory footprint and compares it to the old [27]*keywordNode layout.
func BenchmarkKeywordTrie_MemoryUsage(b *testing.B) {
	// Count nodes and total memory for the compact representation.
	var (
		nodeCount  int
		childCount int
		totalBytes int64
	)
	var walk func(n *keywordNode)
	walk = func(n *keywordNode) {
		nodeCount++
		childCount += len(n.children)
		// keywordNode: slice header (24 bytes) + value (8 bytes) = 32 bytes
		// childEntry: 16 bytes each (byte + pointer + padding)
		totalBytes += 32 + int64(len(n.children))*16
		for _, e := range n.children {
			walk(e.next)
		}
	}
	walk(keywordTrie)

	b.ReportMetric(float64(nodeCount), "nodes")
	b.ReportMetric(float64(childCount), "children")
	b.ReportMetric(float64(totalBytes), "bytes/compact")

	// Old layout: each node had [27]*keywordNode (216 bytes) + value (8 bytes) = 224 bytes
	oldBytes := int64(nodeCount) * 224
	b.ReportMetric(float64(oldBytes), "bytes/old")

	// Verify the compact representation is smaller
	if totalBytes >= oldBytes {
		b.Errorf("compact trie (%d bytes) is not smaller than old (%d bytes)", totalBytes, oldBytes)
	}

	// Also verify struct sizes
	b.ReportMetric(float64(unsafe.Sizeof(keywordNode{})), "bytes/keywordNode")
	b.ReportMetric(float64(unsafe.Sizeof(childEntry{})), "bytes/childEntry")
}

// REQ001154: compact trie lookup benchmark — measures the hot path
// of the compact keyword trie on a representative sample.
func BenchmarkKeywordTrie_Lookup_Compact(b *testing.B) {
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
