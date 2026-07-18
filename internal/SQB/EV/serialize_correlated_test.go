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

// TestExtractCorrelatedColumns_BareIdent_OnlyInOuter verifies REQ001470:
// a bare Ident in the subquery's WHERE that is NOT a column of the inner
// FROM table is treated as a correlated outer reference. Previously,
// bare idents were conservatively treated as inner, which caused
// correlated subqueries to be cached globally and return wrong results.
func TestExtractCorrelatedColumns_BareIdent_OnlyInOuter(t *testing.T) {
	DT.TablesMu.Lock()
	DT.Schemas["subq_t"] = []string{"b", "c"}
	DT.TablesMu.Unlock()
	defer func() {
		DT.TablesMu.Lock()
		delete(DT.Schemas, "subq_t")
		DT.TablesMu.Unlock()
	}()

	sel := &PS.Select{
		From:      "subq_t",
		FromAlias: "",
		Where: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "b"},
			Right: &PS.Ident{Name: "a"},
		},
	}
	cols := extractCorrelatedColumns(sel)
	// b is in subq_t columns → NOT correlated. a is NOT in subq_t → correlated.
	if len(cols) != 1 || cols[0] != "a" {
		t.Fatalf("extractCorrelatedColumns: got %v, want [a]", cols)
	}
}

// TestExtractCorrelatedColumns_BareIdent_BothInnerOuter verifies that
// a bare Ident matching a column in both the inner FROM table and the
// outer row is treated as NOT correlated (inner reference takes precedence).
func TestExtractCorrelatedColumns_BareIdent_BothInnerOuter(t *testing.T) {
	DT.TablesMu.Lock()
	DT.Schemas["subq_t"] = []string{"a", "b", "c"}
	DT.TablesMu.Unlock()
	defer func() {
		DT.TablesMu.Lock()
		delete(DT.Schemas, "subq_t")
		DT.TablesMu.Unlock()
	}()

	sel := &PS.Select{
		From:      "subq_t",
		FromAlias: "",
		Where: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "b"},
			Right: &PS.Ident{Name: "a"},
		},
	}
	cols := extractCorrelatedColumns(sel)
	// Both a and b are in subq_t columns → NOT correlated.
	if len(cols) != 0 {
		t.Fatalf("extractCorrelatedColumns: got %v, want []", cols)
	}
}

// TestExtractCorrelatedColumns_EmptyFrom verifies that subqueries
// without a FROM table (e.g. SELECT 1) are handled gracefully.
func TestExtractCorrelatedColumns_EmptyFrom(t *testing.T) {
	sel := &PS.Select{
		From: "",
		Where: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "b"},
			Right: &PS.Ident{Name: "a"},
		},
	}
	cols := extractCorrelatedColumns(sel)
	// No FROM table → no inner columns → all bare idents are correlated.
	if len(cols) != 2 {
		t.Fatalf("extractCorrelatedColumns: got %d correlated cols, want 2", len(cols))
	}
}

// TestExtractCorrelatedColumns_QualifiedName verifies that QualifiedName
// detection still works correctly alongside the new bare Ident logic.
func TestExtractCorrelatedColumns_QualifiedName(t *testing.T) {
	DT.TablesMu.Lock()
	DT.Schemas["inner"] = []string{"a", "b"}
	DT.TablesMu.Unlock()
	defer func() {
		DT.TablesMu.Lock()
		delete(DT.Schemas, "inner")
		DT.TablesMu.Unlock()
	}()

	sel := &PS.Select{
		From:      "inner",
		FromAlias: "inner",
		Where: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.QualifiedName{Table: "outer", Name: "x"},
			Right: &PS.QualifiedName{Table: "inner", Name: "a"},
		},
	}
	cols := extractCorrelatedColumns(sel)
	// "outer.x" is qualified with outer table → correlated.
	// "inner.a" is qualified with inner table alias → NOT correlated.
	if len(cols) != 1 || cols[0] != "x" {
		t.Fatalf("extractCorrelatedColumns: got %v, want [x]", cols)
	}
}
