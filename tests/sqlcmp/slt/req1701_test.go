//go:build !slt_corpus

package slt

import (
	"crypto/md5"
	"encoding/hex"
	"testing"
)

// TestREQ1701_RowString_Golden verifies rowString produces the exact
// tab-separated byte sequence for every ValueKind after the scratch-
// buffer refactor (REQ001701). The output must be byte-identical to
// the pre-refactor implementation so sort order and result hashes stay
// stable — the scratch buffer only reuses backing storage, it must not
// leak stale digits across values or rows.
func TestREQ1701_RowString_Golden(t *testing.T) {
	tests := []struct {
		name string
		row  []Value
		want string
	}{
		{"null", []Value{{Kind: TypeNull}}, "NULL"},
		{"integer min", []Value{{Kind: TypeInteger, Int: -9223372036854775808}}, "-9223372036854775808"},
		{"integer zero", []Value{{Kind: TypeInteger, Int: 0}}, "0"},
		{"integer max", []Value{{Kind: TypeInteger, Int: 9223372036854775807}}, "9223372036854775807"},
		{"real", []Value{{Kind: TypeReal, Real: 1.5}}, "1.500"},
		{"real neg rounded", []Value{{Kind: TypeReal, Real: -3.14159}}, "-3.142"},
		{"text", []Value{{Kind: TypeText, Text: "hello"}}, "hello"},
		{"blob", []Value{{Kind: TypeBlob, Text: "ABCD"}}, "ABCD"},
		{"multi all kinds", []Value{
			{Kind: TypeInteger, Int: 42},
			{Kind: TypeText, Text: "foo"},
			{Kind: TypeNull},
			{Kind: TypeReal, Real: 2.5},
		}, "42\tfoo\tNULL\t2.500"},
		{"empty row", []Value{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rowString(tc.row)
			if got != tc.want {
				t.Errorf("rowString(%v) = %q, want %q", tc.row, got, tc.want)
			}
		})
	}
}

// TestREQ1701_HashValues_Stable verifies hashValues produces a
// deterministic, byte-stable digest matching the canonical MD5 of the
// value string forms joined by newlines — proving the scratch buffer
// did not leak stale bytes into the hash (REQ001701).
func TestREQ1701_HashValues_Stable(t *testing.T) {
	vs := []Value{
		{Kind: TypeInteger, Int: 42},
		{Kind: TypeNull},
		{Kind: TypeReal, Real: 1.5},
		{Kind: TypeInteger, Int: -7},
	}
	// Canonical: each value's rendered digits/text + newline, matching
	// writeValueToBytes (NULL, strconv.AppendInt, strconv.AppendFloat 'f',3).
	want := md5.Sum([]byte("42\nNULL\n1.500\n-7\n"))
	wantHex := hex.EncodeToString(want[:])
	got := hashValues(vs)
	if got != wantHex {
		t.Errorf("hashValues = %s, want canonical %s", got, wantHex)
	}
	// Deterministic across repeated calls: the reused scratch buffer
	// must be reset per value so no stale tail leaks between calls.
	for i := range 5 {
		if h := hashValues(vs); h != got {
			t.Errorf("hashValues iter %d = %s, want %s (non-deterministic)", i, h, got)
		}
	}
}

// TestREQ1701_Scratch_PoolReuse verifies the pooled rowRenderBuf is
// returned to the pool after rowString and can be reused by the next
// call without leaking the previous row's content. REQ001701.
func TestREQ1701_Scratch_PoolReuse(t *testing.T) {
	// Fill with a long row to force builder/scratch growth, then render
	// a short row — the short result must not contain residual bytes.
	long := []Value{{Kind: TypeText, Text: "XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"}}
	_ = rowString(long)
	got := rowString([]Value{{Kind: TypeInteger, Int: 1}})
	if got != "1" {
		t.Errorf("rowString after long row = %q, want %q (scratch/builder leak)", got, "1")
	}
}

// BenchmarkREQ1701_RowString_AllKinds measures the pooled-builder +
// pooled-scratch path for a 4-column row spanning every ValueKind.
// REQ001701: strconv.AppendInt/AppendFloat now write into the pooled
// scratch []byte instead of allocating a fresh []byte per value.
func BenchmarkREQ1701_RowString_AllKinds(b *testing.B) {
	row := []Value{
		{Kind: TypeInteger, Int: 1234567890},
		{Kind: TypeText, Text: "hello world"},
		{Kind: TypeNull},
		{Kind: TypeReal, Real: 3.14159265},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = rowString(row)
	}
}
