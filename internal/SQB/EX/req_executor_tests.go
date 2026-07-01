package EX

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// REQ001044: WorkerPool lifecycle.
func TestExecutor_WorkerPoolLifecycle(t *testing.T) {
	e := NewExecutor()
	pool := e.Pool()
	if pool == nil {
		t.Fatal("NewExecutor: pool is nil")
	}
	if pool.Workers() <= 0 {
		t.Fatalf("NewExecutor: pool.Workers()=%d, want >0", pool.Workers())
	}
	ctx := context.Background()
	var counter atomic.Int64
	err := pool.Submit(ctx, func() error {
		counter.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	e.Close()
	e.Close()
	err = pool.Submit(ctx, func() error {
		return nil
	})
	if err != UT.ErrPoolClosed {
		t.Fatalf("Submit after Close: got %v, want ErrPoolClosed", err)
	}
	e2 := NewExecutor()
	defer e2.Close()
	e3 := e2.ShallowCopy()
	if e2.Pool() != e3.Pool() {
		t.Fatal("ShallowCopy: pool not shared")
	}
	e2.Close()
	err = e3.Pool().Submit(ctx, func() error { return nil })
	if err != UT.ErrPoolClosed {
		t.Fatalf("Submit after Close on shared pool: got %v, want ErrPoolClosed", err)
	}
}

// REQ001054: MaxParallelism configuration.
func TestREQ001054_MaxParallelism(t *testing.T) {
	t.Run("default is GOMAXPROCS", func(t *testing.T) {
		ex := NewExecutor()
		got := ex.MaxParallelism()
		want := runtime.GOMAXPROCS(0)
		if got != want {
			t.Errorf("MaxParallelism() = %d, want %d", got, want)
		}
	})
	t.Run("set to specific value", func(t *testing.T) {
		ex := NewExecutor()
		ex.SetMaxParallelism(2)
		got := ex.MaxParallelism()
		if got != 2 {
			t.Errorf("MaxParallelism() = %d, want 2", got)
		}
		pool := ex.Pool()
		if pool == nil {
			t.Fatal("Pool() is nil")
		}
		if pool.Workers() != 2 {
			t.Errorf("Pool.Workers() = %d, want 2", pool.Workers())
		}
	})
	t.Run("set to 0 defaults to GOMAXPROCS", func(t *testing.T) {
		ex := NewExecutor()
		ex.SetMaxParallelism(0)
		got := ex.MaxParallelism()
		want := runtime.GOMAXPROCS(0)
		if got != want {
			t.Errorf("MaxParallelism() = %d, want %d", got, want)
		}
	})
	t.Run("concurrent queries bounded by MaxParallelism", func(t *testing.T) {
		ex := NewExecutor()
		ex.SetMaxParallelism(2)
		pool := ex.Pool()
		if pool == nil {
			t.Fatal("Pool() is nil")
		}
		if pool.Workers() > 2 {
			t.Errorf("Pool.Workers() = %d, want <= 2", pool.Workers())
		}
	})
}