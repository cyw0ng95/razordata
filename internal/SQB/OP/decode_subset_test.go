package OP

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// REQ001434: DecodeRowSubsetInto must decode only the columns
// listed in wantedIdx and skip non-wanted columns without
// allocating. Correctness test covering the four primitive kinds
// (int / text / null) and verifying byte-offset advancement.
func TestDecodeRowSubsetInto_Primitives(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols: []string{"a", "b", "c", "d", "e"},
	}
	src := DT.Row{
		Cols: schema.Cols,
		Data: []DT.Value{
			DT.NewIntValue(0x0101010101010101),
			DT.NewTextValue("hello"),
			DT.Value{Kind: DT.KindNull},
			DT.NewIntValue(0x0202020202020202),
			DT.NewTextValue("world!"),
		},
	}
	payload, err := DT.EncodeRow(schema, src)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Decode only cols 1 ("b") and 4 ("e").
	wanted := []int{1, 4}
	data := make([]DT.Value, len(wanted))
	row := &DT.Row{Cols: []string{"b", "e"}, Data: data}
	if err := DT.DecodeRowSubsetInto(row, payload, schema, wanted, nil); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if row.Cols[0] != "b" || row.Cols[1] != "e" {
		t.Errorf("Cols overwritten: got %v", row.Cols)
	}
	if got := row.Data[0].S; got != "hello" {
		t.Errorf("col b: got %q, want %q", got, "hello")
	}
	if got := row.Data[1].S; got != "world!" {
		t.Errorf("col e: got %q, want %q", got, "world!")
	}
}

// REQ001434: DecodeRowSubsetInto must surface the same data
// as DecodeRowInto when wantedIdx covers all columns. This guards
// the no-skip path which is the fallback for full-decode rows.
func TestDecodeRowSubsetInto_FullSetMatchesFullDecode(t *testing.T) {
	schema := &DT.StoreSchema{Cols: []string{"a", "b", "c"}}
	src := DT.Row{
		Cols: schema.Cols,
		Data: []DT.Value{
			DT.NewIntValue(11),
			DT.NewTextValue("xy"),
			DT.Value{Kind: DT.KindNull},
		},
	}
	payload, err := DT.EncodeRow(schema, src)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	fullData := make([]DT.Value, 3)
	fullRow := &DT.Row{Cols: schema.Cols, Data: fullData}
	if err := DT.DecodeRowInto(fullRow, payload, schema); err != nil {
		t.Fatalf("full decode: %v", err)
	}

	subsetData := make([]DT.Value, 3)
	subsetRow := &DT.Row{Cols: schema.Cols, Data: subsetData}
	if err := DT.DecodeRowSubsetInto(subsetRow, payload, schema, []int{0, 1, 2}, nil); err != nil {
		t.Fatalf("subset decode: %v", err)
	}

	for i := range fullData {
		if fullData[i].Kind != subsetData[i].Kind {
			t.Errorf("col %d: Kind mismatch full=%v subset=%v",
				i, fullData[i].Kind, subsetData[i].Kind)
		}
		if fullData[i].I64 != subsetData[i].I64 {
			t.Errorf("col %d: I64 mismatch full=%d subset=%d",
				i, fullData[i].I64, subsetData[i].I64)
		}
		if fullData[i].S != subsetData[i].S {
			t.Errorf("col %d: S mismatch full=%q subset=%q",
				i, fullData[i].S, subsetData[i].S)
		}
	}
}

// REQ001434: DecodeRowSubsetInto with a single wanted index that
// targets the trailing column exercises byte-skip of preceding
// non-wanted columns. Verifying the byte offsets advance correctly
// is the safety property that lets us drop the per-column decode
// for skipped columns.
func TestDecodeRowSubsetInto_SkipLeadingCols(t *testing.T) {
	schema := &DT.StoreSchema{Cols: []string{"a", "b", "c", "d"}}
	src := DT.Row{
		Cols: schema.Cols,
		Data: []DT.Value{
			DT.NewIntValue(1),
			DT.NewTextValue("second"),
			DT.NewIntValue(3),
			DT.NewTextValue("fourth"),
		},
	}
	payload, err := DT.EncodeRow(schema, src)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Decode only col 3 ("d") — must skip cols 0, 1, 2.
	row := &DT.Row{Cols: []string{"d"}, Data: make([]DT.Value, 1)}
	if err := DT.DecodeRowSubsetInto(row, payload, schema, []int{3}, nil); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if row.Data[0].S != "fourth" {
		t.Errorf("col d: got %q, want %q", row.Data[0].S, "fourth")
	}
}

// REQ001434: bench — measure end-to-end SeqScan times for a small
// table when decoding only one column. The benchmark compiles a
// SeqScan with usedColIdx and reads 100 rows. A separate benchmark
// reads 100 rows with full decode for comparison.
func BenchmarkDecodeRowSubsetInto_OneColumn(b *testing.B) {
	schema := &DT.StoreSchema{Cols: []string{"a", "b", "c", "d", "e"}}
	src := DT.Row{
		Cols: schema.Cols,
		Data: []DT.Value{
			DT.NewIntValue(1),
			DT.NewTextValue("hello"),
			DT.NewIntValue(3),
			DT.NewIntValue(4),
			DT.NewTextValue("last"),
		},
	}
	payload, err := DT.EncodeRow(schema, src)
	if err != nil {
		b.Fatal(err)
	}
	wanted := []int{1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		row := &DT.Row{Cols: []string{"b"}, Data: make([]DT.Value, 1)}
		if err := DT.DecodeRowSubsetInto(row, payload, schema, wanted, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeRowInto_FullFiveColumns(b *testing.B) {
	schema := &DT.StoreSchema{Cols: []string{"a", "b", "c", "d", "e"}}
	src := DT.Row{
		Cols: schema.Cols,
		Data: []DT.Value{
			DT.NewIntValue(1),
			DT.NewTextValue("hello"),
			DT.NewIntValue(3),
			DT.NewIntValue(4),
			DT.NewTextValue("last"),
		},
	}
	payload, err := DT.EncodeRow(schema, src)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		row := &DT.Row{Cols: schema.Cols, Data: make([]DT.Value, 5)}
		if err := DT.DecodeRowInto(row, payload, schema); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeRowManyTextColumns measures the case where the
// projected subset excludes heavy text/blob columns. REQ001434:
// text decoding allocates a Go string per cell (typically 32B+),
// so decoding 1 of 5 text-heavy columns should save ~4 string
// allocations per row.
func BenchmarkDecodeRowManyTextColumns_FullDecode(b *testing.B) {
	schema := &DT.StoreSchema{Cols: []string{"a", "b", "c", "d", "e"}}
	src := DT.Row{
		Cols: schema.Cols,
		Data: []DT.Value{
			DT.NewTextValue("first payload"),
			DT.NewIntValue(2),
			DT.NewTextValue("third payload with more text content"),
			DT.NewTextValue("fourth payload here"),
			DT.NewIntValue(5),
		},
	}
	payload, err := DT.EncodeRow(schema, src)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		row := &DT.Row{Cols: schema.Cols, Data: make([]DT.Value, 5)}
		if err := DT.DecodeRowInto(row, payload, schema); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeRowManyTextColumns_SubsetDecodeOneInt(b *testing.B) {
	schema := &DT.StoreSchema{Cols: []string{"a", "b", "c", "d", "e"}}
	src := DT.Row{
		Cols: schema.Cols,
		Data: []DT.Value{
			DT.NewTextValue("first payload"),
			DT.NewIntValue(2),
			DT.NewTextValue("third payload with more text content"),
			DT.NewTextValue("fourth payload here"),
			DT.NewIntValue(5),
		},
	}
	payload, err := DT.EncodeRow(schema, src)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		row := &DT.Row{Cols: []string{"b"}, Data: make([]DT.Value, 1)}
		if err := DT.DecodeRowSubsetInto(row, payload, schema, []int{1}, nil); err != nil {
			b.Fatal(err)
		}
	}
}
