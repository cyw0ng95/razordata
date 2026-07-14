package CO

import (
	"cmp"
	"slices"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// ReorderIndices returns indices 0..n-1 sorted by the cost of
// predicates[indices[i]] ascending. Equal-cost predicates preserve
// original order (stable sort).
//
// Returns nil when n <= 1 (no reordering needed).
// REQ001248, REQ001439.
func ReorderIndices(predicates []PS.Expr) []int {
	n := len(predicates)
	if n <= 1 {
		return nil
	}
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	slices.SortStableFunc(indices, func(a, b int) int {
		ci := Cost(predicates[a])
		cj := Cost(predicates[b])
		return cmp.Compare(ci, cj)
	})
	return indices
}

// OrderSlice returns a new slice with elements in the order specified by
// indices. indices must be a permutation of 0..len(predicates)-1.
// REQ001248, REQ001439.
func OrderSlice(predicates []PS.Expr, indices []int) []PS.Expr {
	if len(indices) != len(predicates) {
		return predicates
	}
	ordered := make([]PS.Expr, len(predicates))
	for i, idx := range indices {
		ordered[i] = predicates[idx]
	}
	return ordered
}
