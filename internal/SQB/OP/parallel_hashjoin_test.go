package OP

import (
	"context"
	"errors"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// TestParallelHashJoin_Basic verifies REQ001047: a parallel hash
// join over two small in-memory operators produces the expected
// matching rows.
func TestParallelHashJoin_Basic(t *testing.T) {
	pool := UT.NewWorkerPool(2)
	defer pool.Close()

	left := newMemOp([]pl.Row{kvr(1, 10), kvr(2, 20), kvr(3, 30)})
	right := newMemOp([]pl.Row{kvr(1, 100), kvr(2, 200), kvr(3, 300)})

	phj := NewParallelHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 0, pool)
	defer phj.Close()

	var count int
	for {
		_, err := phj.Next(context.Background())
		if errors.Is(err, pl.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		count++
	}
	if count != 3 {
		t.Fatalf("expected 3 matched rows, got %d", count)
	}
}

// TestParallelHashJoin_Close verifies REQ001047: Close is idempotent.
func TestParallelHashJoin_Close(t *testing.T) {
	pool := UT.NewWorkerPool(2)
	defer pool.Close()

	left := newMemOp([]pl.Row{kvr(1, 10)})
	right := newMemOp([]pl.Row{kvr(1, 100)})

	phj := NewParallelHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 0, pool)
	if err := phj.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := phj.Close(); err != nil {
		t.Fatalf("Close (idempotent): %v", err)
	}
}

// TestParallelHashJoin_NoMatch verifies REQ001047: no matching keys
// produces 0 rows.
func TestParallelHashJoin_NoMatch(t *testing.T) {
	pool := UT.NewWorkerPool(2)
	defer pool.Close()

	left := newMemOp([]pl.Row{kvr(1, 10)})
	right := newMemOp([]pl.Row{kvr(2, 200)})

	phj := NewParallelHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 0, pool)
	defer phj.Close()

	_, err := phj.Next(context.Background())
	if !errors.Is(err, pl.ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
}
