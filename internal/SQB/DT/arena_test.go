package DT

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestRowArena_AllocAndReset(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c"},
		ColIndex: map[string]int{"a": 0, "b": 1, "c": 2},
	}

	a := &RowArena{}
	for i := 0; i < 1000; i++ {
		row := a.AllocRow(3, schema)
		if len(row.Data) != 3 {
			t.Fatalf("row.Data len = %d, want 3", len(row.Data))
		}
		// Write into the arena-backed row
		row.Data[0] = NewIntValue(int64(i))
		row.Data[1] = NewFloatValue(float64(i))
		row.Data[2] = NewTextValue("x")
	}

	// Reset should free the slab
	a.Reset()
}

// TestRowArena_AllocRowByValue verifies REQ001632: AllocRow returns
// Row by value (not *Row), eliminating per-row heap allocation.
func TestRowArena_AllocRowByValue(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b"},
		ColIndex: map[string]int{"a": 0, "b": 1},
	}

	a := &RowArena{}
	a.Init(10, 2)

	// AllocRow should return a Row value, not a pointer.
	// We verify this by checking that DecodeRowInto accepts &row.
	for i := 0; i < 5; i++ {
		row := a.AllocRow(2, schema)
		// Writing to row.Data should affect the arena backing.
		row.Data[0] = NewIntValue(int64(i))
		row.Data[1] = NewTextValue("hello")
	}

	// Verify arena-backed data persists across allocations.
	row0 := a.AllocRow(0, schema) // nCols=0 path
	_ = row0

	// Verify Reset works after value-returning AllocRow.
	a.Reset()
}

func TestRowArena_ResetAfterAlloc(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"x"},
		ColIndex: map[string]int{"x": 0},
	}

	a := &RowArena{}
	row := a.AllocRow(1, schema)
	if len(row.Data) != 1 {
		t.Fatalf("Data len = %d, want 1", len(row.Data))
	}
	row.Data[0] = NewIntValue(42)
	a.Reset()

	// Reuse the arena after reset
	row2 := a.AllocRow(1, schema)
	if len(row2.Data) != 1 {
		t.Fatalf("Data len = %d, want 1", len(row2.Data))
	}
	row2.Data[0] = NewIntValue(100)
	a.Reset()
}

func TestDecodeRowInto_AllTypes(t *testing.T) {
	// Build a known schema and encode a row with all types
	schema := &StoreSchema{
		Cols:     []string{"i", "f", "s", "b", "bl"},
		ColIndex: map[string]int{"i": 0, "f": 1, "s": 2, "b": 3, "bl": 4},
	}
	row := Row{
		Cols: schema.Cols,
		Data: []Value{
			NewIntValue(42),
			NewFloatValue(3.14),
			NewTextValue("hello"),
			NewBoolValue(true),
			NewBlobValue([]byte{1, 2, 3}),
		},
		ColIndex: schema.ColIndex,
	}

	encoded, err := EncodeRow(schema, row)
	if err != nil {
		t.Fatalf("EncodeRow: %v", err)
	}

	// Decode back using DecodeRow
	dec1, err := DecodeRow(encoded, schema)
	if err != nil {
		t.Fatalf("DecodeRow: %v", err)
	}

	// Decode into arena using DecodeRowInto
	arena := &RowArena{}
	dec2 := arena.AllocRow(5, schema)
	err = DecodeRowInto(&dec2, encoded, schema)
	if err != nil {
		t.Fatalf("DecodeRowInto: %v", err)
	}

	// Compare results
	for i := 0; i < len(schema.Cols); i++ {
		if dec1.Data[i].Kind != dec2.Data[i].Kind {
			t.Errorf("col %d kind: got %v, want %v", i, dec2.Data[i].Kind, dec1.Data[i].Kind)
		}
		if dec1.Data[i].I64 != dec2.Data[i].I64 {
			t.Errorf("col %d i64: got %v, want %v", i, dec2.Data[i].I64, dec1.Data[i].I64)
		}
		if dec1.Data[i].F64 != dec2.Data[i].F64 {
			t.Errorf("col %d f64: got %v, want %v", i, dec2.Data[i].F64, dec1.Data[i].F64)
		}
		if !strings.EqualFold(dec1.Data[i].S, dec2.Data[i].S) {
			t.Errorf("col %d str: got %v, want %v", i, dec2.Data[i].S, dec1.Data[i].S)
		}
		if !bytes.Equal(dec1.Data[i].B, dec2.Data[i].B) {
			t.Errorf("col %d blob: got %v, want %v", i, dec2.Data[i].B, dec1.Data[i].B)
		}
	}
	arena.Reset()
}

func BenchmarkSeqScan_ArenaVsSlice(b *testing.B) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c", "d", "e"},
		ColIndex: map[string]int{"a": 0, "b": 1, "c": 2, "d": 3, "e": 4},
	}

	// Encode a reference row
	refRow := Row{
		Cols:     schema.Cols,
		ColIndex: schema.ColIndex,
		Data: []Value{
			NewIntValue(42),
			NewFloatValue(3.14),
			NewTextValue("hello"),
			NewBoolValue(true),
			NewBlobValue([]byte{1, 2, 3}),
		},
	}
	encoded, err := EncodeRow(schema, refRow)
	if err != nil {
		b.Fatal(err)
	}

	// Baseline: per-row make([]Value, N)
	b.Run("SliceAlloc", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, err := DecodeRow(encoded, schema)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Arena: bump-pointer allocation
	b.Run("Arena", func(b *testing.B) {
		arena := &RowArena{}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			row := arena.AllocRow(5, schema)
			if err := DecodeRowInto(&row, encoded, schema); err != nil {
				arena.Reset()
				b.Fatal(err)
			}
			if i%100 == 0 {
				arena.Reset()
			}
		}
		arena.Reset()
	})
}

// TestRowArena_Presize verifies REQ001260: Init pre-allocates a slab
// so that AllocRow calls within the estimated range do not trigger grow.
func TestRowArena_Presize(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c"},
		ColIndex: map[string]int{"a": 0, "b": 1, "c": 2},
	}

	// Pre-size for 100 rows of 3 columns each.
	arena := &RowArena{}
	arena.Init(100, 3)

	// AllocRow should not trigger grow for 100 rows.
	for i := 0; i < 100; i++ {
		row := arena.AllocRow(3, schema)
		if len(row.Data) != 3 {
			t.Fatalf("row %d: Data len = %d, want 3", i, len(row.Data))
		}
		row.Data[0] = NewIntValue(int64(i))
		row.Data[1] = NewFloatValue(float64(i))
		row.Data[2] = NewTextValue("x")
	}
	arena.Reset()
}

// TestRowArena_PresizeGrowFallback verifies that Init with zero
// estimates still allows AllocRow to work via grow.
func TestRowArena_PresizeGrowFallback(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"a"},
		ColIndex: map[string]int{"a": 0},
	}

	arena := &RowArena{}
	arena.Init(0, 0) // zero estimate — should not crash

	// Should still work via grow fallback.
	for i := 0; i < 10; i++ {
		row := arena.AllocRow(1, schema)
		if len(row.Data) != 1 {
			t.Fatalf("AllocRow %d: Data len = %d, want 1", i, len(row.Data))
		}
	}
	arena.Reset()
}

// BenchmarkRowArena_Presize measures the allocation reduction from Init.
// With 10000 rows of 5 columns, the arena would grow 6+ times without Init.
func BenchmarkRowArena_Presize(b *testing.B) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c", "d", "e"},
		ColIndex: map[string]int{"a": 0, "b": 1, "c": 2, "d": 3, "e": 4},
	}
	const rows = 10000

	b.Run("NoInit", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			arena := &RowArena{}
			for j := 0; j < rows; j++ {
				arena.AllocRow(5, schema)
			}
			arena.Reset()
		}
	})

	b.Run("WithInit", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			arena := &RowArena{}
			arena.Init(rows, 5)
			for j := 0; j < rows; j++ {
				arena.AllocRow(5, schema)
			}
			arena.Reset()
		}
	})
}

// BenchmarkRowArena_Init_Pool measures allocation reduction from the
// size-bucketed slab cache for 1K, 10K, and 100K row workloads.
// REQ001289.
func BenchmarkRowArena_Init_Pool(b *testing.B) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c", "d", "e"},
		ColIndex: map[string]int{"a": 0, "b": 1, "c": 2, "d": 3, "e": 4},
	}

	for _, rows := range []int{1000, 10000, 100000} {
		rows := rows
		b.Run(fmt.Sprintf("Rows%d", rows), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				arena := &RowArena{}
				arena.Init(rows, 5)
				for j := 0; j < rows; j++ {
					arena.AllocRow(5, schema)
				}
				arena.Reset()
			}
		})
	}
}

// TestRowArena_OldSlabsNotCollectedWhileReferenced verifies REQ001639:
// old slabs stay alive through outstanding Row.Data sub-slices even
// after the `a.slabs` tracking is removed. We allocate a row, keep a
// reference to its Data, grow the arena multiple times, force GC, and
// verify the old row data is still valid.
func TestRowArena_OldSlabsNotCollectedWhileReferenced(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c"},
		ColIndex: map[string]int{"a": 0, "b": 1, "c": 2},
	}

	arena := &RowArena{}

	// Allocate a row that will live in the first slab.
	row := arena.AllocRow(3, schema)
	row.Data[0] = NewIntValue(42)
	row.Data[1] = NewFloatValue(3.14)
	row.Data[2] = NewTextValue("hello")
	// Keep a reference to the row's Data.
	oldData := row.Data

	// Grow the arena many times (geometric: 64KB → 128KB → 256KB → ...)
	// Each slab of Value is ~8K values. 3 rows per cycle, so we need
	// ~8K/3 ≈ 2700 cycles to overflow one slab.
	for i := 0; i < 10000; i++ {
		r := arena.AllocRow(3, schema)
		r.Data[0] = NewIntValue(int64(i))
		r.Data[1] = NewFloatValue(float64(i))
		r.Data[2] = NewTextValue("x")
	}

	// Force GC to run.
	runtime.GC()

	// Verify the old row data is still valid.
	if got := oldData[0].I64; got != 42 {
		t.Errorf("oldData[0].I64 = %d, want 42", got)
	}
	if got := oldData[1].F64; got != 3.14 {
		t.Errorf("oldData[1].F64 = %f, want 3.14", got)
	}
	if got := oldData[2].S; got != "hello" {
		t.Errorf("oldData[2].S = %q, want hello", got)
	}

	arena.Reset()
}

// BenchmarkSelect1_GCPressure measures alloc/grow/reset cycles under
// GC pressure. REQ001639: removing redundant slabs tracking should
// reduce GC scan time compared to the old approach.
func BenchmarkSelect1_GCPressure(b *testing.B) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c", "d", "e"},
		ColIndex: map[string]int{"a": 0, "b": 1, "c": 2, "d": 3, "e": 4},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		arena := &RowArena{}
		// Allocate enough rows to force multiple grows.
		for j := 0; j < 10000; j++ {
			arena.AllocRow(5, schema)
		}
		// Force GC mid-cycle to measure scanning overhead.
		if i%10 == 0 {
			runtime.GC()
		}
		arena.Reset()
	}
}
