package DT

import (
	"bytes"
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
		if row == nil {
			t.Fatal("AllocRow returned nil")
		}
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

func TestRowArena_ResetAfterAlloc(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"x"},
		ColIndex: map[string]int{"x": 0},
	}

	a := &RowArena{}
	row := a.AllocRow(1, schema)
	row.Data[0] = NewIntValue(42)
	a.Reset()

	// Reuse the arena after reset
	row2 := a.AllocRow(1, schema)
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
	err = DecodeRowInto(dec2, encoded, schema)
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
			if err := DecodeRowInto(row, encoded, schema); err != nil {
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
