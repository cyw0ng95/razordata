package OP

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// REQ001432: compileColRef correctness across qualified/unqualified
// column references and rows populated with bare vs qualified columns.
//
// The closure returned by compileColRef is the per-row column resolver
// used by every compiled projection expression. Optimizing it must not
// alter the resolved Value, including for the three patterns that
// previously exercised different code branches:
//   1. Unqualified ref against qualified Cols (multi-table join).
//   2. Qualified ref against qualified Cols (multi-table join).
//   3. Bare ref against bare Cols (single-table scan).
func TestCompileColRef_AllBranches(t *testing.T) {
	mkRow := func(cols []string, vals []DT.Value) DT.Row {
		return DT.Row{Cols: cols, Data: vals}
	}
	cases := []struct {
		name   string
		ref    string
		slot   int
		row    DT.Row
		wantOK bool
		want   DT.Value
	}{
		{
			name:   "qualified_match",
			ref:    "t1.a1",
			slot:   -1,
			row:    mkRow([]string{"t1.a1", "t1.b1"}, []DT.Value{DT.NewIntValue(42), DT.NewIntValue(7)}),
			wantOK: true,
			want:   DT.NewIntValue(42),
		},
		{
			name:   "bare_match_against_qualified_cols",
			ref:    "b1",
			slot:   -1,
			row:    mkRow([]string{"t1.a1", "t1.b1"}, []DT.Value{DT.NewIntValue(42), DT.NewIntValue(7)}),
			wantOK: true,
			want:   DT.NewIntValue(7),
		},
		{
			name:   "bare_match_against_bare_cols",
			ref:    "a1",
			slot:   -1,
			row:    mkRow([]string{"a1", "b1"}, []DT.Value{DT.NewIntValue(11), DT.NewIntValue(13)}),
			wantOK: true,
			want:   DT.NewIntValue(11),
		},
		{
			name:   "slot_hit",
			ref:    "t1.a1",
			slot:   0,
			row:    mkRow([]string{"t1.a1"}, []DT.Value{DT.NewIntValue(99)}),
			wantOK: true,
			want:   DT.NewIntValue(99),
		},
		{
			name:   "missing_column_returns_null",
			ref:    "z9",
			slot:   -1,
			row:    mkRow([]string{"t1.a1", "t1.b1"}, []DT.Value{DT.NewIntValue(1), DT.NewIntValue(2)}),
			wantOK: false,
			want:   DT.Value{Kind: KindNull},
		},
		{
			name:   "qualified_suffix_match_with_intermediate_tables",
			ref:    "x3",
			slot:   -1,
			row: mkRow(
				[]string{"t3.a3", "t3.b3", "t3.x3", "t2.a2", "t4.b4", "t9.b9"},
				[]DT.Value{DT.NewIntValue(1), DT.NewIntValue(2), DT.NewTextValue("table tn3 row 89"), DT.NewIntValue(4), DT.NewIntValue(5), DT.NewIntValue(6)},
			),
			wantOK: true,
			want:   DT.NewTextValue("table tn3 row 89"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn := compileColRef(tc.ref, tc.slot)
			got := fn(&tc.row)
			if got.Kind != tc.want.Kind {
				t.Fatalf("Kind: got %v, want %v", got.Kind, tc.want.Kind)
			}
			if got.I64 != tc.want.I64 {
				t.Fatalf("I64: got %d, want %d", got.I64, tc.want.I64)
			}
			if got.S != tc.want.S {
				t.Fatalf("S: got %q, want %q", got.S, tc.want.S)
			}
		})
	}
}

// REQ001432: compileColRef with ColIndex set must hit the O(1) map
// path regardless of whether the map is keyed by bare or qualified
// names. Previously only the qualified key was probed.
func TestCompileColRef_ColIndex_BareAndQualified(t *testing.T) {
	row := DT.Row{
		Cols:    []string{"a1", "b1", "c1"},
		Data:    []DT.Value{DT.NewIntValue(10), DT.NewIntValue(20), DT.NewIntValue(30)},
		ColIndex: map[string]int{
			"a1": 0,
			"b1": 1,
			"c1": 2,
		},
	}
	fn := compileColRef("b1", -1)
	if got := fn(&row); got.I64 != 20 {
		t.Errorf("bare lookup via ColIndex: got %d, want 20", got.I64)
	}
}

// REQ001432 benchmark: per-row cost of compileColRef for the common
// multi-table-join shape (18 qualified columns, no populated ColIndex
// to force the slow fallback path that exercises the fixed code).
func BenchmarkCompileColRef_SixTableJoin(b *testing.B) {
	cols := make([]string, 18)
	data := make([]DT.Value, 18)
	for i := range cols {
		tbl := (i / 3) + 1
		suffix := string(rune('a' + (i % 3)))
		cols[i] = "t" + intToStr(tbl) + "." + suffix + intToStr(tbl)
		data[i] = DT.NewIntValue(int64(i))
	}
	ref := compileColRef("b3", -1)
	row := DT.Row{Cols: cols, Data: data}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ref(&row)
	}
}

// intToStr avoids strconv import in the benchmark file.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
