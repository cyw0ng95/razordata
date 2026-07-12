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

// TestSort_SmallResultFastPath verifies REQ001520: < 64 rows use
// the in-place SortStableFunc fast path instead of keyCache+indices.
func TestSort_SmallResultFastPath(t *testing.T) {
	// 5 rows — below 64 threshold, uses fast path
	child := &countChild{n: 5}
	s := NewSort(child, []PS.OrderItem{{
		Expr: &PS.Ident{Name: "a", SlotIdx: 0},
	}})

	ctx := context.Background()
	var rows []Row
	for i := 0; i < 5; i++ {
		r, err := s.Next(ctx)
		if err != nil {
			t.Fatalf("expected row %d, got error: %v", i, err)
		}
		rows = append(rows, r)
	}
	// countChild produces 4,3,2,1,0 → sorted ascending → 0,1,2,3,4
	for i := 0; i < len(rows); i++ {
		if rows[i].Data[0].I64 != int64(i) {
			t.Errorf("row %d: got %d, want %d", i, rows[i].Data[0].I64, i)
		}
	}
	_, err := s.Next(ctx)
	if !errors.Is(err, ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got: %v", err)
	}
}

// TestSort_SmallResultFastPath_Descending verifies descending order
// in the REQ001520 small-result fast path.
func TestSort_SmallResultFastPath_Descending(t *testing.T) {
	child := &countChild{n: 5}
	s := NewSort(child, []PS.OrderItem{{
		Expr: &PS.Ident{Name: "a", SlotIdx: 0},
		Desc: true,
	}})

	ctx := context.Background()
	var rows []Row
	for i := 0; i < 5; i++ {
		r, err := s.Next(ctx)
		if err != nil {
			t.Fatalf("expected row %d, got error: %v", i, err)
		}
		rows = append(rows, r)
	}
	// countChild produces 4,3,2,1,0 → sorted descending → 4,3,2,1,0
	for i := 0; i < len(rows); i++ {
		if rows[i].Data[0].I64 != int64(4-i) {
			t.Errorf("row %d: got %d, want %d", i, rows[i].Data[0].I64, 4-i)
		}
	}
	_, err := s.Next(ctx)
	if !errors.Is(err, ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got: %v", err)
	}
}

// TestSort_LargeResult_UsesKeyCache checks that >= 64 rows use the
// keyCache+indices path (original behavior preserved). REQ001520.
func TestSort_LargeResult_UsesKeyCache(t *testing.T) {
	child := &countChild{n: 100}
	s := NewSort(child, []PS.OrderItem{{
		Expr: &PS.Ident{Name: "a", SlotIdx: 0},
	}})
	s.WithSortBufferSize(200)

	ctx := context.Background()
	var rows []Row
	for i := 0; i < 100; i++ {
		r, err := s.Next(ctx)
		if err != nil {
			t.Fatalf("expected row %d, got error: %v", i, err)
		}
		rows = append(rows, r)
	}
	// countChild produces 99,98,...,0 → sorted ascending
	for i := 0; i < len(rows); i++ {
		if rows[i].Data[0].I64 != int64(i) {
			t.Errorf("row %d: got %d, want %d", i, rows[i].Data[0].I64, i)
		}
	}
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
