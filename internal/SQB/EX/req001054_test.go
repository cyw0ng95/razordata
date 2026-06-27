package EX

import (
	"runtime"
	"testing"
)

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
		// Pool should have 2 workers
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
		// The pool should have 2 workers, not more
		if pool.Workers() > 2 {
			t.Errorf("Pool.Workers() = %d, want <= 2", pool.Workers())
		}
	})
}