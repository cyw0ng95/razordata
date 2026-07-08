//go:build !slt_corpus

package slt

import (
	"crypto/md5"
	"fmt"
	"hash"
	"sort"
	"sync"
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

// BenchmarkResultHash_XXHASH measures per-call performance of resultHash
// with the pooled xxhash hasher (replacement for MD5).
func BenchmarkResultHash_XXHASH(b *testing.B) {
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

// BenchmarkResultHash_MD5_vs_XXHASH compares the old MD5 implementation
// against the xxhash replacement used by resultHash. This benchmark is
// kept as a reference so the speedup can be re-verified after dependency
// upgrades.
func BenchmarkResultHash_MD5_vs_XXHASH(b *testing.B) {
	rows := make([][]Value, 30)
	for i := range rows {
		rows[i] = []Value{
			{Kind: TypeInteger, Int: int64(i)},
			{Kind: TypeText, Text: "hello"},
			{Kind: TypeReal, Real: float64(i) * 1.5},
		}
	}
	hashByRow := func(rs *ResultSet, mode SortMode, hasher hash.Hash) string {
		r := rs.Rows
		idx := make([]int, len(r))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(i, j int) bool {
			return rowString(r[idx[i]]) < rowString(r[idx[j]])
		})
		for _, i := range idx {
			for _, cell := range r[i] {
				hasher.Write([]byte(cell.String()))
				hasher.Write([]byte("\n"))
			}
		}
		return fmt.Sprintf("%x", hasher.Sum(nil))
	}

	rs := &ResultSet{Rows: rows}

	var md5Pool = &sync.Pool{
		New: func() any { return md5.New() },
	}

	b.Run("MD5", func(b *testing.B) {
		for b.Loop() {
			h := md5Pool.Get().(hash.Hash)
			hashByRow(rs, RowSort, h)
			h.Reset()
			md5Pool.Put(h)
		}
	})

	b.Run("XXHASH", func(b *testing.B) {
		for b.Loop() {
			h := hashPool.Get().(hash.Hash)
			hashByRow(rs, RowSort, h)
			h.Reset()
			hashPool.Put(h)
		}
	})
}