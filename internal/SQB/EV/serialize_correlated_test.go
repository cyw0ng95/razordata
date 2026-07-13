package EV

import (
	"bytes"
	"sync"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestCachedCorrelatedColIndices verifies that the per-subquery
// outer-row index cache returns stable indices across calls and
// produces a deterministic key. REQ001292.
func TestCachedCorrelatedColIndices(t *testing.T) {
	// Reset caches to ensure the test starts clean.
	correlatedColIndicesCache = sync.Map{}
	subqueryColRefCache = sync.Map{}

	sel := &PS.Select{
		From:      "x",
		FromAlias: "x",
		Where: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.QualifiedName{Table: "t1", Name: "c"},
			Right: &PS.Ident{Name: "a"},
		},
	}
	sub := &PS.SubqueryExpr{Subquery: sel}

	row := &Row{
		Cols: []string{"a", "b", "c", "d"},
		Data: []Value{DT.NewIntValue(7), DT.NewIntValue(8), DT.NewIntValue(9), DT.NewIntValue(10)},
		ColIndex: map[string]int{
			"a": 0, "b": 1, "c": 2, "d": 3,
		},
	}

	idx1 := cachedCorrelatedColIndices(sub, row)
	idx2 := cachedCorrelatedColIndices(sub, row)
	if len(idx1) == 0 || len(idx1) != len(idx2) {
		t.Fatalf("cache returned inconsistent indices: %v vs %v", idx1, idx2)
	}
	for i := range idx1 {
		if idx1[i] != idx2[i] {
			t.Fatalf("idx1[%d]=%d != idx2[%d]=%d", i, idx1[i], i, idx2[i])
		}
	}
}

// TestSerializeCorrelatedValues_PooledBuffer ensures the encoder
// produces stable keys for the same input and adapts to int / text
// / null kinds without panicking. REQ001292.
func TestSerializeCorrelatedValues_PooledBuffer(t *testing.T) {
	correlatedKeyBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	row := &Row{
		Cols: []string{"a", "b", "c"},
		Data: []Value{DT.NewIntValue(1), DT.NewTextValue("hello"), DT.NullValue()},
	}
	idxs := []int{0, 1, 2}

	k1 := serializeCorrelatedValues(row, idxs)
	k2 := serializeCorrelatedValues(row, idxs)
	if k1 != k2 {
		t.Fatalf("non-deterministic key: %q vs %q", k1, k2)
	}
	if k1 != "1,hello,NULL" {
		t.Fatalf("key = %q, want %q", k1, "1,hello,NULL")
	}
}

// TestSerializeCorrelatedValues_OutOfRangeIndex ensures an outer
// row that is smaller than the correlated index slice produces
// the literal "NULL" placeholder. REQ001292.
func TestSerializeCorrelatedValues_OutOfRangeIndex(t *testing.T) {
	correlatedKeyBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	row := &Row{
		Cols: []string{"a"},
		Data: []Value{DT.NewIntValue(1)},
	}
	idxs := []int{5, -1}
	if got := serializeCorrelatedValues(row, idxs); got != "NULL,NULL" {
		t.Fatalf("got %q, want NULL,NULL", got)
	}
}
