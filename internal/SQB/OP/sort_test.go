package OP

import (
	"context"
	"errors"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// countChild returns exactly n rows then ErrNoRows.
type countChild struct{ n int }

func (c *countChild) Next(context.Context) (Row, error) {
	if c.n <= 0 {
		return Row{}, ErrNoRows
	}
	c.n--
	return Row{Cols: []string{"a"}, Data: []Value{{Kind: KindInt, I64: int64(c.n)}}}, nil
}
func (c *countChild) Close() error { return nil }

func TestSort_MaterializationCap(t *testing.T) {
	// Sort materializes ALL rows from child on the first Next() call.
	// When sortBufferSize is set and child produces more rows than the cap,
	// Next() returns ErrSortTooManyRows.
	child := &countChild{n: 100}
	s := NewSort(child, []PS.OrderItem{{
		Expr: &PS.Ident{Name: "a", SlotIdx: 0},
	}})
	s.WithSortBufferSize(10)

	ctx := context.Background()

	// First Next() triggers materialization — should hit the cap immediately
	// because child produces 100 rows but cap is 10.
	_, err := s.Next(ctx)
	if err == nil {
		t.Fatal("expected ErrSortTooManyRows, got nil")
	}
	if !errors.Is(err, ErrSortTooManyRows) {
		t.Fatalf("expected ErrSortTooManyRows, got: %v", err)
	}
}

func TestSort_MaterializationCapBelowThreshold(t *testing.T) {
	// When child produces fewer rows than sortBufferSize, all rows succeed.
	child := &countChild{n: 5}
	s := NewSort(child, []PS.OrderItem{{
		Expr: &PS.Ident{Name: "a", SlotIdx: 0},
	}})
	s.WithSortBufferSize(10)

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, err := s.Next(ctx)
		if err != nil {
			t.Fatalf("expected row %d, got error: %v", i, err)
		}
	}

	// Verify exhausted
	_, err := s.Next(ctx)
	if !errors.Is(err, ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got: %v", err)
	}
}

func TestSort_MaterializationCapZeroMeansUnlimited(t *testing.T) {
	child := &countChild{n: 20}
	s := NewSort(child, []PS.OrderItem{{
		Expr: &PS.Ident{Name: "a", SlotIdx: 0},
	}})
	// sortBufferSize = 0 means unlimited
	s.WithSortBufferSize(0)

	ctx := context.Background()
	for i := 0; i < 20; i++ {
		_, err := s.Next(ctx)
		if err != nil {
			t.Fatalf("expected row %d, got error: %v", i, err)
		}
	}

	// Verify exhausted
	_, err := s.Next(ctx)
	if !errors.Is(err, ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got: %v", err)
	}
}
