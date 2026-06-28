package EX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// REQ001052: Parallel UNION ALL correctness test.
func TestParallelUnionAll_Correctness(t *testing.T) {
	pool := UT.NewWorkerPool(2)
	defer pool.Close()
	ctx := context.Background()

	// Left: [10, 20], Right: [30, 40, 50]
	left := newArrayScan([]Row{
		{Data: []Value{{Kind: KindInt, I64: 10}}},
		{Data: []Value{{Kind: KindInt, I64: 20}}},
	})
	right := newArrayScan([]Row{
		{Data: []Value{{Kind: KindInt, I64: 30}}},
		{Data: []Value{{Kind: KindInt, I64: 40}}},
		{Data: []Value{{Kind: KindInt, I64: 50}}},
	})

	u := NewParallelUnionAll(left, right, pool)
	defer u.Close()

	var vals []int64
	for {
		r, err := u.Next(ctx)
		if err != nil {
			break
		}
		vals = append(vals, r.Data[0].I64)
	}
	if len(vals) != 5 {
		t.Fatalf("got %d rows, want 5", len(vals))
	}
	// Check all expected values present (order may vary)
	m := map[int64]bool{10: true, 20: true, 30: true, 40: true, 50: true}
	for _, v := range vals {
		if !m[v] {
			t.Errorf("unexpected value %d", v)
		}
		delete(m, v)
	}
	if len(m) > 0 {
		t.Errorf("missing values: %v", m)
	}
}

// REQ001052: empty sides.
func TestParallelUnionAll_EmptySides(t *testing.T) {
	pool := UT.NewWorkerPool(2)
	defer pool.Close()
	ctx := context.Background()

	// Both empty
	u := NewParallelUnionAll(newArrayScan(nil), newArrayScan(nil), pool)
	defer u.Close()
	_, err := u.Next(ctx)
	if err != ErrNoRows {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}

	// Left empty
	u2 := NewParallelUnionAll(newArrayScan(nil), newArrayScan([]Row{
		{Data: []Value{{Kind: KindInt, I64: 5}}},
	}), pool)
	defer u2.Close()
	r, err := u2.Next(ctx)
	if err != nil {
		t.Fatalf("left empty: %v", err)
	}
	if r.Data[0].I64 != 5 {
		t.Errorf("got %d, want 5", r.Data[0].I64)
	}
	_, err = u2.Next(ctx)
	if err != ErrNoRows {
		t.Fatalf("expected ErrNoRows after drain, got %v", err)
	}
}

// arrayScan is a simple Operator that yields rows from a slice.
type arrayScan struct {
	rows []Row
	pos  int
}

func newArrayScan(rows []Row) *arrayScan { return &arrayScan{rows: rows} }
func (a *arrayScan) Next(context.Context) (Row, error) {
	if a.pos >= len(a.rows) {
		return Row{}, ErrNoRows
	}
	r := a.rows[a.pos]
	a.pos++
	return r, nil
}
func (a *arrayScan) Close() error { return nil }