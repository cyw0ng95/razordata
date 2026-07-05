package EV

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// BenchmarkIdentLookup measures Ident column reference evaluation via
// the runtime Lookup path (SlotIdx = -1). Baseline for REQ001202.
func BenchmarkIdentLookup(b *testing.B) {
	row := &Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: []Value{
			DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3), DT.NewIntValue(4), DT.NewIntValue(5),
		},
	}
	expr := &PS.Ident{Name: "c", SlotIdx: -1}

	b.ResetTimer()
	for range b.N {
		v, err := EvalValue(expr, row, nil)
		if err != nil {
			b.Fatal(err)
		}
		if v.Kind != KindInt || v.I64 != 3 {
			b.Fatalf("got %v, want 3", v)
		}
	}
}

// BenchmarkIdentSlotIdx measures Ident column reference evaluation via
// the pre-resolved SlotIdx fast path (REQ001202).
func BenchmarkIdentSlotIdx(b *testing.B) {
	row := &Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: []Value{
			DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3), DT.NewIntValue(4), DT.NewIntValue(5),
		},
	}
	expr := &PS.Ident{Name: "c", SlotIdx: 2}

	b.ResetTimer()
	for range b.N {
		v, err := EvalValue(expr, row, nil)
		if err != nil {
			b.Fatal(err)
		}
		if v.Kind != KindInt || v.I64 != 3 {
			b.Fatalf("got %v, want 3", v)
		}
	}
}

// BenchmarkIdentSlotIdx_Nth evaluates the Nth column (last column "e" at idx 4).
func BenchmarkIdentSlotIdx_Last(b *testing.B) {
	row := &Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: []Value{
			DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3), DT.NewIntValue(4), DT.NewIntValue(5),
		},
	}
	expr := &PS.Ident{Name: "e", SlotIdx: 4}

	b.ResetTimer()
	for range b.N {
		v, err := EvalValue(expr, row, nil)
		if err != nil {
			b.Fatal(err)
		}
		if v.Kind != KindInt || v.I64 != 5 {
			b.Fatalf("got %v, want 5", v)
		}
	}
}

// BenchmarkIdentLookup_Last evaluates the last column via runtime Lookup.
// Shows linear scan cost when the target is at the end.
func BenchmarkIdentLookup_Last(b *testing.B) {
	row := &Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: []Value{
			DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3), DT.NewIntValue(4), DT.NewIntValue(5),
		},
	}
	expr := &PS.Ident{Name: "e", SlotIdx: -1}

	b.ResetTimer()
	for range b.N {
		v, err := EvalValue(expr, row, nil)
		if err != nil {
			b.Fatal(err)
		}
		if v.Kind != KindInt || v.I64 != 5 {
			b.Fatalf("got %v, want 5", v)
		}
	}
}
