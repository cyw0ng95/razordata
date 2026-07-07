package OP

import (
	"context"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// noopChild immediately returns ErrNoRows.
type noopChild struct{}

func (noopChild) Next(context.Context) (Row, error) { return Row{}, ErrNoRows }
func (noopChild) Close() error                      { return nil }

func BenchmarkSortExtractKeys(b *testing.B) {
	row := Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: make([]Value, 5),
	}
	buf := make([]Row, 10)
	for i := range buf {
		cp := Row{Cols: row.Cols, Data: make([]Value, 5)}
		for j := range cp.Data {
			cp.Data[j] = Value{Kind: KindInt, I64: int64((10 - i) * 100 + j)}
		}
		cp.Data[3] = Value{Kind: KindText, S: "hello"}
		buf[i] = cp
	}

	// Pre-set buf directly so Sort.Next skips child.Next() materialization.
	// Sort.Next checks !s.materialized and reads from child first. To bypass
	// child, we prime the buffer and set materialized=true externally via a
	// helper that re-extracts keys from the existing buffer.
	benchKeyExtract := func(name string, keys []PS.OrderItem, b *testing.B) {
		b.Run(name, func(b *testing.B) {
			s := &Sort{
				child: noopChild{},
				buf:   buf,
				keys:  keys,
			}
			b.ResetTimer()
			for range b.N {
				s.materialized = false
				s.pos = 0
				_, _ = s.Next(context.Background())
			}
		})
	}

	benchKeyExtract("EvalValue-1key",
		[]PS.OrderItem{{Expr: &PS.Ident{Name: "d", SlotIdx: -1}}}, b)
	benchKeyExtract("SlotIdx-1key",
		[]PS.OrderItem{{Expr: &PS.Ident{Name: "d", SlotIdx: 3}}}, b)
	benchKeyExtract("EvalValue-2keys",
		[]PS.OrderItem{
			{Expr: &PS.Ident{Name: "a", SlotIdx: -1}},
			{Expr: &PS.Ident{Name: "b", SlotIdx: -1}},
		}, b)
	benchKeyExtract("SlotIdx-2keys",
		[]PS.OrderItem{
			{Expr: &PS.Ident{Name: "a", SlotIdx: 0}},
			{Expr: &PS.Ident{Name: "b", SlotIdx: 1}},
		}, b)

	bigBuf := make([]Row, 100)
	for i := range bigBuf {
		cp := Row{Cols: row.Cols, Data: make([]Value, 5)}
		for j := range cp.Data {
			cp.Data[j] = Value{Kind: KindInt, I64: int64((100 - i) * 1000 + j)}
		}
		bigBuf[i] = cp
	}

	benchBigKeyExtract := func(name string, keys []PS.OrderItem, b *testing.B) {
		b.Run(name, func(b *testing.B) {
			s := &Sort{
				child: noopChild{},
				buf:   bigBuf,
				keys:  keys,
			}
			b.ResetTimer()
			for range b.N {
				s.materialized = false
				s.pos = 0
				_, _ = s.Next(context.Background())
			}
		})
	}

	benchBigKeyExtract("EvalValue-100rows",
		[]PS.OrderItem{{Expr: &PS.Ident{Name: "c", SlotIdx: -1}}}, b)
	benchBigKeyExtract("SlotIdx-100rows",
		[]PS.OrderItem{{Expr: &PS.Ident{Name: "c", SlotIdx: 2}}}, b)
}

func TestSort_PreOrdered_Skip(t *testing.T) {
	// Create a simple child that returns 3 rows
	child := &sliceScan{
		rows: []Row{
			{Cols: []string{"a"}, Data: []Value{{Kind: KindInt, I64: 1}}},
			{Cols: []string{"a"}, Data: []Value{{Kind: KindInt, I64: 2}}},
			{Cols: []string{"a"}, Data: []Value{{Kind: KindInt, I64: 3}}},
		},
	}

	s := NewSort(child, []PS.OrderItem{{Expr: &PS.Ident{Name: "a"}}})
	s.SetPreOrdered()

	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		row, err := s.Next(ctx)
		if err != nil {
			t.Fatalf("unexpected error at row %d: %v", i, err)
		}
		if row.Data[0].I64 != int64(i) {
			t.Errorf("row %d: got %d, want %d", i, row.Data[0].I64, i)
		}
	}
	_, err := s.Next(ctx)
	if err != ErrNoRows {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
}

func BenchmarkSort_PreOrdered(b *testing.B) {
	rows := make([]Row, 1000)
	for i := range rows {
		rows[i] = Row{
			Cols: []string{"a", "b"},
			Data: []Value{{Kind: KindInt, I64: int64(i)}, {Kind: KindInt, I64: int64(i * 2)}},
		}
	}
	child := &sliceScan{rows: rows}
	s := NewSort(child, []PS.OrderItem{{Expr: &PS.Ident{Name: "a"}}})
	s.SetPreOrdered()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Reset child position
		child.pos = 0
		ctx := context.Background()
		for {
			_, err := s.Next(ctx)
			if err != nil {
				break
			}
		}
	}
}
