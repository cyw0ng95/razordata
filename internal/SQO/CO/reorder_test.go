package CO

import (
	"slices"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestReorderIndices_Empty(t *testing.T) {
	if got := ReorderIndices(nil); got != nil {
		t.Errorf("ReorderIndices(nil) = %v, want nil", got)
	}
	if got := ReorderIndices([]PS.Expr{}); got != nil {
		t.Errorf("ReorderIndices([]) = %v, want nil", got)
	}
}

func TestReorderIndices_Single(t *testing.T) {
	preds := []PS.Expr{&PS.NumberLiteral{Val: 1}}
	if got := ReorderIndices(preds); got != nil {
		t.Errorf("ReorderIndices(single) = %v, want nil", got)
	}
}

func TestReorderIndices_Stable(t *testing.T) {
	equalCost := PS.Expr(&PS.NumberLiteral{Val: 1})
	preds := []PS.Expr{equalCost, equalCost, equalCost}
	indices := ReorderIndices(preds)
	if len(indices) != 3 {
		t.Fatalf("len = %d, want 3", len(indices))
	}
	for i, idx := range indices {
		if idx != i {
			t.Errorf("stable sort broken: indices[%d] = %d", i, idx)
		}
	}
}

func TestReorderIndices_SortsByCostAscending(t *testing.T) {
	a := &PS.FunctionCall{Name: "expensive"}
	b := &PS.NumberLiteral{Val: 1}
	preds := []PS.Expr{a, b}
	indices := ReorderIndices(preds)
	if len(indices) != 2 {
		t.Fatalf("len = %d, want 2", len(indices))
	}
	if indices[0] != 1 {
		t.Errorf("cheaper predicate not first: indices[0] = %d", indices[0])
	}
	if indices[1] != 0 {
		t.Errorf("expensive predicate not second: indices[1] = %d", indices[1])
	}
}

func TestOrderSlice(t *testing.T) {
	preds := []PS.Expr{
		&PS.NumberLiteral{Val: 1},
		&PS.NumberLiteral{Val: 2},
		&PS.NumberLiteral{Val: 3},
	}
	ordered := OrderSlice(preds, []int{2, 0, 1})
	if len(ordered) != 3 {
		t.Fatalf("len = %d, want 3", len(ordered))
	}
	if !slices.Equal(ordered, []PS.Expr{
		preds[2], preds[0], preds[1],
	}) {
		t.Errorf("wrong order: %v", ordered)
	}
}

func TestOrderSlice_Mismatch(t *testing.T) {
	preds := []PS.Expr{&PS.NumberLiteral{Val: 1}}
	ordered := OrderSlice(preds, []int{0, 1})
	if !slices.Equal(ordered, preds) {
		t.Errorf("mismatched lengths should return original unchanged")
	}
}
