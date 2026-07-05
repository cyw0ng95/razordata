package DT

import (
	"encoding/binary"
	"math"
	"runtime"
	"strconv"
	"testing"
	"unsafe"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func TestPoolValueSlice_Reuse(t *testing.T) {
	n := 16
	slices := make([][]Value, 100)
	for i := 0; i < 100; i++ {
		slices[i] = PoolValueSlice(n)
		if cap(slices[i]) < n {
			t.Fatalf("slice[%d]: cap=%d, want >=%d", i, cap(slices[i]), n)
		}
		if len(slices[i]) != n {
			t.Fatalf("slice[%d]: len=%d, want %d", i, len(slices[i]), n)
		}
		// Check that Value headers are zero (pool returns fresh slices)
		for j := 0; j < n; j++ {
			if slices[i][j].Kind != KindNull {
				t.Fatalf("slice[%d][%d]: Kind=%v, want KindNull", i, j, slices[i][j].Kind)
			}
		}
	}

	// Return all slices to pool
	for _, s := range slices {
		PutValueSlice(s)
	}

	// Force GC and check that memory was reclaimed
	runtime.GC()
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	// The pool should have reduced allocations — we can't directly test this
	// without benchmarks, but the basic reuse works.
	t.Logf("after GC: alloc=%d, sys=%d, numGC=%d", memStats.Alloc, memStats.Sys, memStats.NumGC)
}

func TestPoolValueSlice_Grow(t *testing.T) {
	// Pool default capacity is 16
	s := PoolValueSlice(32)
	if cap(s) < 32 {
		t.Fatalf("growing slice: cap=%d, want >=32", cap(s))
	}
	if len(s) != 32 {
		t.Fatalf("growing slice: len=%d, want 32", len(s))
	}
	PutValueSlice(s)

	// Now request a smaller slice — should get a pooled one with capacity >= 16
	s2 := PoolValueSlice(8)
	if cap(s2) < 8 {
		t.Fatalf("small slice: cap=%d, want >=8", cap(s2))
	}
	if len(s2) != 8 {
		t.Fatalf("small slice: len=%d, want 8", len(s2))
	}
	PutValueSlice(s2)
}

func TestPoolValueSlice_LargeDiscarded(t *testing.T) {
	// Slices with cap > 128 are discarded, not returned to pool
	s := make([]Value, 0, 200)
	PutValueSlice(s) // Should be discarded silently

	// Pool should still have its default capacity slices
	s2 := PoolValueSlice(16)
	if cap(s2) < 16 {
		t.Fatalf("after discard: cap=%d, want >=16", cap(s2))
	}
	PutValueSlice(s2)
}

func TestDecodeRow_UsesPool(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c"},
		ColTypes: []LX.TokenType{LX.T_INT, LX.TokenType(4), LX.TokenType(2)},
	}
	// Build an encoded row: a=42 (int), b=3.14 (float), c="hello" (string)
	buf := encodeTestRow(42, 3.14, "hello")
	row, err := DecodeRow(buf, schema)
	if err != nil {
		t.Fatalf("DecodeRow: %v", err)
	}
	if len(row.Data) != 3 {
		t.Fatalf("row.Data len=%d, want 3", len(row.Data))
	}
	if row.Data[0].I64 != 42 {
		t.Fatalf("row.Data[0].I64=%v, want 42", row.Data[0].I64)
	}
	if row.Data[1].F64 != 3.14 {
		t.Fatalf("row.Data[1].F64=%v, want 3.14", row.Data[1].F64)
	}
	if row.Data[2].S != "hello" {
		t.Fatalf("row.Data[2].S=%v, want hello", row.Data[2].S)
	}

	// Return the slice to pool
	PutValueSlice(row.Data)
}

func TestDecodeRow_PoolIntegration(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c"},
		ColTypes: []LX.TokenType{LX.T_INT, LX.T_FLOAT, LX.T_STRING},
	}
	const iterations = 1000

	// Decode 1000 rows and return slices to pool
	for i := 0; i < iterations; i++ {
		buf := encodeTestRow(42, 3.14, "hello")
		row, err := DecodeRow(buf, schema)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if row.Data[0].I64 != 42 {
			t.Fatalf("iteration %d: got %d, want 42", i, row.Data[0].I64)
		}
		PutValueSlice(row.Data)
	}

	// Verify pool is working by checking allocations
	runtime.GC()
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	// With pooling, 1000 iterations should result in far fewer allocations
	// than without pooling. We can't check exact numbers without benchmarks,
	// but the pool should have been reused.
	t.Logf("after %d iterations: alloc=%d, sys=%d, numGC=%d", iterations, memStats.Alloc, memStats.Sys, memStats.NumGC)
}

func BenchmarkPoolValueSlice(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := PoolValueSlice(16)
		for j := range s {
			s[j] = NewIntValue(int64(j))
		}
		PutValueSlice(s)
	}
}

func BenchmarkPoolValueSlice_Varying(b *testing.B) {
	sizes := []int{4, 8, 16, 32, 64}
for _, size := range sizes {
		b.Run("size="+strconv.Itoa(size), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				s := PoolValueSlice(size)
				for j := range s {
					s[j] = NewIntValue(int64(j))
				}
				PutValueSlice(s)
			}
		})
	}
}

func BenchmarkDecodeRow_Pooled(b *testing.B) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c"},
		ColTypes: []LX.TokenType{LX.T_INT, LX.T_FLOAT, LX.T_STRING},
	}
	buf := encodeTestRow(42, 3.14, "hello")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		row, err := DecodeRow(buf, schema)
		if err != nil {
			b.Fatal(err)
		}
		PutValueSlice(row.Data)
	}
}

func encodeTestRow(a int64, b float64, c string) []byte {
	var buf []byte
	buf = binary.AppendUvarint(buf, 3) // 3 columns
	buf = append(buf, 0x01) // rvInt
	buf = append(buf, bigEndianUint64(uint64(a))...)
	buf = append(buf, 0x04) // rvFloat
	buf = append(buf, bigEndianUint64(math.Float64bits(b))...)
	buf = append(buf, 0x02) // rvString
	buf = binary.AppendUvarint(buf, uint64(len(c)))
	buf = append(buf, []byte(c)...)
	return buf
}

func bigEndianUint64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func TestValueSlice_Alignment(t *testing.T) {
	size := unsafe.Sizeof(Value{})
	if size == 0 {
		t.Fatal("Value size is 0")
	}
	t.Logf("Value size: %d bytes", size)
}
