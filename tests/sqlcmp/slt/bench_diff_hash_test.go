//go:build !slt_corpus

package slt

import (
	"testing"
)

// BenchmarkDiffResultSets_EarlyMatch measures the impact of the
// early exact-match check in DiffResultSets on already-sorted result
// sets (the common case where results arrive in sort order).
func BenchmarkDiffResultSets_EarlyMatch(b *testing.B) {
	// Build a 30-row, 5-column result set, already sorted.
	// The select1 corpus averages ~30 rows per query.
	rows := make([][]Value, 30)
	expected := make([][]Value, 30)
	for i := range rows {
		rows[i] = []Value{
			{Kind: TypeInteger, Int: int64(i)},
			{Kind: TypeText, Text: "hello"},
			{Kind: TypeReal, Real: float64(i) * 1.5},
			{Kind: TypeInteger, Int: int64(i * 2)},
			{Kind: TypeText, Text: "world"},
		}
		expected[i] = rows[i]
	}

	rec := &Record{
		Sort:       RowSort,
		TypeString: "T",
	}
	rs := &ResultSet{Rows: rows}

	b.ResetTimer()
	for b.Loop() {
		DiffResultSets(rs, rec)
	}
}

// BenchmarkResultHash_Pooled measures per-call performance of resultHash
// with the pooled md5 hasher.
func BenchmarkResultHash_Pooled(b *testing.B) {
	rows := make([][]Value, 30)
	for i := range rows {
		rows[i] = []Value{
			{Kind: TypeInteger, Int: int64(i)},
			{Kind: TypeText, Text: "hello"},
			{Kind: TypeReal, Real: float64(i) * 1.5},
		}
	}

	rs := &ResultSet{Rows: rows}

	b.ResetTimer()
	for b.Loop() {
		resultHash(rs, RowSort)
	}
}