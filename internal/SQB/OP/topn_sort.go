// REQ001230: Top-N sort heap — replace full materializing sort with bounded
// heap when LIMIT is present. O(N log K) vs O(N log N) where K << N.
package OP

import (
	"container/heap"
	"context"
	"slices"

	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TopNSort retains only the top-K rows via a bounded heap, eliminating
// the sort of discarded rows. For ASC, a max-heap keeps the K smallest;
// for DESC, a min-heap keeps the K largest.
type TopNSort struct {
	child        Operator
	keys         []PS.OrderItem
	limit        int64
	params       []any
	buf          []Row
	pos          int
	materialized bool
}

// NewTopNSort creates a TopNSort that retains only the top limit rows.
func NewTopNSort(child Operator, keys []PS.OrderItem, limit int64) *TopNSort {
	return &TopNSort{child: child, keys: keys, limit: limit}
}

// WithParams propagates bound placeholders (R16-1..2).
func (t *TopNSort) WithParams(p []any) Operator {
	t.params = p
	return t
}

// Child returns the child operator.
func (t *TopNSort) Child() Operator      { return t.child }
func (t *TopNSort) SetChild(c Operator)  { t.child = c }
func (t *TopNSort) Keys() []PS.OrderItem { return t.keys }

func (t *TopNSort) Next(ctx context.Context) (Row, error) {
	if !t.materialized {
		t.materialize(ctx)
	}
	if t.pos >= len(t.buf) {
		return Row{}, ErrNoRows
	}
	r := t.buf[t.pos]
	t.pos++
	return r, nil
}

func (t *TopNSort) materialize(ctx context.Context) {
	limit := int(t.limit)
	if limit < 1 {
		limit = 1
	}

	// Determine heap type: for multi-key sorts, use the first key's direction.
	// If any key is DESC and no key is ASC (mixed), default to ASC heap.
	isDesc := len(t.keys) > 0 && t.keys[0].Desc

	// Build a bounded heap
	h := &topNHeap{
		keys: t.keys,
		desc: isDesc,
	}
	heap.Init(h)

	for {
		if err := ctx.Err(); err != nil {
			break
		}
		row, err := t.child.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return
		}

		// Extract sort keys for this row
		keys := make([]Value, len(t.keys))
		for j, k := range t.keys {
			v, err := EV.EvalValue(k.Expr, &row, t.params)
			if err != nil {
				return
			}
			keys[j] = v
		}

		if h.Len() < limit {
			heap.Push(h, topNItem{row: row, keys: keys})
		} else {
			// Compare with the "worst" (root) — if new row is better, replace
			if h.isBetterThanWorst(keys) {
				h.rows[0] = row
				h.keyCache[0] = keys
				heap.Fix(h, 0)
			}
		}
	}

	// Drain heap in sorted order
	t.buf = make([]Row, 0, h.Len())
	for h.Len() > 0 {
		item := heap.Pop(h).(topNItem)
		t.buf = append(t.buf, item.row)
	}

	// The heap pops worst-first, so buf is in reverse order.
	// Reverse to get correct ASC/DESC order.
	slices.Reverse(t.buf)
	t.materialized = true
}

func (t *TopNSort) Close() error {
	t.buf = nil
	t.pos = 0
	t.materialized = false
	return t.child.Close()
}

// topNItem holds a row and its pre-extracted sort keys.
type topNItem struct {
	row  Row
	keys []Value
}

// topNHeap implements heap.Interface.
// For ASC (desc=false): max-heap, root is the "largest" among the K smallest.
// For DESC (desc=true):  min-heap, root is the "smallest" among the K largest.
type topNHeap struct {
	rows     []Row
	keyCache [][]Value
	keys     []PS.OrderItem
	desc     bool
}

func (h *topNHeap) Len() int { return len(h.rows) }

func (h *topNHeap) Less(i, j int) bool {
	c := compareKeySets(h.keyCache[i], h.keyCache[j], h.keys)
	if h.desc {
		return c > 0
	}
	return c > 0
}

func (h *topNHeap) Swap(i, j int) {
	h.rows[i], h.rows[j] = h.rows[j], h.rows[i]
	h.keyCache[i], h.keyCache[j] = h.keyCache[j], h.keyCache[i]
}

func (h *topNHeap) Push(x any) {
	item := x.(topNItem)
	h.rows = append(h.rows, item.row)
	h.keyCache = append(h.keyCache, item.keys)
}

func (h *topNHeap) Pop() any {
	n := len(h.rows)
	row := h.rows[n-1]
	h.rows = h.rows[:n-1]
	h.keyCache = h.keyCache[:n-1]
	return topNItem{row: row}
}

// isBetterThanWorst returns true if the given key values would sort before
// the root of the heap (i.e., the row should replace the root).
func (h *topNHeap) isBetterThanWorst(keys []Value) bool {
	if h.Len() == 0 {
		return true
	}
	c := compareKeySets(keys, h.keyCache[0], h.keys)
	// For ASC: "better" = smaller (c < 0). Root is the largest.
	// For DESC: "better" = larger (c > 0). Root is the smallest.
	if h.desc {
		return c < 0
	}
	return c < 0
}

// compareKeySets compares two key-value sets using the sort key order.
// Returns -1 if a < b, 0 if equal, 1 if a > b.
func compareKeySets(a, b []Value, keys []PS.OrderItem) int {
	for ki := range a {
		if keys[ki].NullsOrder != 0 {
			if a[ki].IsNull() && !b[ki].IsNull() {
				return -int(keys[ki].NullsOrder)
			}
			if b[ki].IsNull() && !a[ki].IsNull() {
				return int(keys[ki].NullsOrder)
			}
		}
		c := pl.CompareValue(a[ki], b[ki])
		if c == 0 {
			continue
		}
		if keys[ki].Desc {
			return -c
		}
		return c
	}
	return 0
}
