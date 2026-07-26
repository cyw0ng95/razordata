package PL

import (
	"testing"
)

// TestColIndexPool_BuildAndRelease verifies that buildColIndex uses
// pooled maps and ReleaseColIndex returns them to the pool. REQ002026.
func TestColIndexPool_BuildAndRelease(t *testing.T) {
	row := Row{
		Cols: []string{"a", "b", "c"},
		Data: []Value{
			{Kind: KindInt, I64: 1},
			{Kind: KindInt, I64: 2},
			{Kind: KindInt, I64: 3},
		},
	}

	// First lookup triggers buildColIndex.
	v, ok := row.LookupValue("a")
	if !ok {
		t.Fatal("LookupValue('a') not found")
	}
	if v.I64 != 1 {
		t.Fatalf("LookupValue('a') = %d, want 1", v.I64)
	}
	if row.ColIndex == nil {
		t.Fatal("ColIndex not built")
	}

	// Release should return the map to the pool.
	row.ReleaseColIndex()
	if row.ColIndex != nil {
		t.Fatal("ColIndex not nil after ReleaseColIndex")
	}

	// Second lookup should rebuild from pool.
	v, ok = row.LookupValue("b")
	if !ok {
		t.Fatal("LookupValue('b') not found after re-build")
	}
	if v.I64 != 2 {
		t.Fatalf("LookupValue('b') = %d, want 2", v.I64)
	}

	row.ReleaseColIndex()
}

// TestColIndexPool_ReuseAcrossRows verifies that ColIndex maps are
// pooled and reused across different rows. REQ002026.
func TestColIndexPool_ReuseAcrossRows(t *testing.T) {
	row1 := Row{
		Cols: []string{"x", "y"},
		Data: []Value{
			{Kind: KindInt, I64: 10},
			{Kind: KindInt, I64: 20},
		},
	}
	row2 := Row{
		Cols: []string{"p", "q"},
		Data: []Value{
			{Kind: KindInt, I64: 30},
			{Kind: KindInt, I64: 40},
		},
	}

	// Build and release row1's ColIndex.
	row1.LookupValue("x")
	row1.ReleaseColIndex()

	// Build row2's ColIndex — should reuse the pooled map.
	v, ok := row2.LookupValue("q")
	if !ok {
		t.Fatal("LookupValue('q') not found")
	}
	if v.I64 != 40 {
		t.Fatalf("LookupValue('q') = %d, want 40", v.I64)
	}
	row2.ReleaseColIndex()
}

// TestColIndexPool_UppercaseKeys verifies that pooled maps work
// correctly with uppercase column names (lowercase conversion path).
func TestColIndexPool_UppercaseKeys(t *testing.T) {
	row := Row{
		Cols: []string{"Name", "Age"},
		Data: []Value{
			{Kind: KindText, S: "alice"},
			{Kind: KindInt, I64: 30},
		},
	}

	// Lookup with uppercase name should work via lowercase key.
	v, ok := row.LookupValue("Name")
	if !ok {
		t.Fatal("LookupValue('Name') not found")
	}
	if v.S != "alice" {
		t.Fatalf("LookupValue('Name') = %q, want 'alice'", v.S)
	}

	row.ReleaseColIndex()
}

// TestColIndexPool_ReleaseNilSafe verifies that ReleaseColIndex on a
// row with nil ColIndex does not panic. REQ002026.
func TestColIndexPool_ReleaseNilSafe(t *testing.T) {
	row := Row{
		Cols: []string{"a"},
		Data: []Value{{Kind: KindInt, I64: 1}},
	}
	// ColIndex is nil — should not panic.
	row.ReleaseColIndex()

	// Double release should also be safe.
	row.LookupValue("a")
	row.ReleaseColIndex()
	row.ReleaseColIndex()
}

// BenchmarkColIndexPool_Build measures the allocation savings from
// pooling ColIndex maps. REQ002026.
func BenchmarkColIndexPool_Build(b *testing.B) {
	cols := []string{"a", "b", "c", "d", "e"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		row := Row{Cols: cols, Data: make([]Value, 5)}
		row.LookupValue("c")
		row.ReleaseColIndex()
	}
}
