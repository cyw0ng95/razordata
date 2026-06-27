package EX

import (
	"context"
	"sync/atomic"
	"testing"
)

// REQ001044: Executor-level WorkerPool lifecycle.
func TestExecutor_WorkerPoolLifecycle(t *testing.T) {
	// 1. NewExecutor creates a pool
	e := NewExecutor()
	pool := e.Pool()
	if pool == nil {
		t.Fatal("NewExecutor: pool is nil")
	}
	if pool.Workers() <= 0 {
		t.Fatalf("NewExecutor: pool.Workers()=%d, want >0", pool.Workers())
	}

	// 2. Submit works
	ctx := context.Background()
	var counter atomic.Int64
	err := pool.Submit(ctx, func() error {
		counter.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// 3. Close is idempotent
	e.Close()
	e.Close() // second call no panic

	// 4. Submit after Close returns ErrPoolClosed
	err = pool.Submit(ctx, func() error {
		return nil
	})
	if err != ErrPoolClosed {
		t.Fatalf("Submit after Close: got %v, want ErrPoolClosed", err)
	}

	// 5. ShallowCopy shares the same pool
	e2 := NewExecutor()
	defer e2.Close()
	e3 := e2.ShallowCopy()
	if e2.Pool() != e3.Pool() {
		t.Fatal("ShallowCopy: pool not shared")
	}

	// 6. Executor.Close shuts down shared pool
	e2.Close()
	err = e3.Pool().Submit(ctx, func() error { return nil })
	if err != ErrPoolClosed {
		t.Fatalf("Submit after Close on shared pool: got %v, want ErrPoolClosed", err)
	}
}
