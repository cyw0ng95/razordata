package UT

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestWorkerPool_Basic verifies task execution and counting.
func TestWorkerPool_Basic(t *testing.T) {
	pool := NewWorkerPool(4)
	defer pool.Close()

	var counter int32
	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		err := pool.Submit(context.Background(), func() error {
			defer wg.Done()
			atomic.AddInt32(&counter, 1)
			return nil
		})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	wg.Wait()
	if counter != int32(n) {
		t.Errorf("counter = %d, want %d", counter, n)
	}
}

// TestWorkerPool_DefaultSize verifies GOMAXPROCS default.
func TestWorkerPool_DefaultSize(t *testing.T) {
	pool := NewWorkerPool(0)
	defer pool.Close()
	if pool.Workers() <= 0 {
		t.Error("expected positive worker count for default")
	}
}

// TestWorkerPool_ConcurrentSubmits verifies pool safety.
func TestWorkerPool_ConcurrentSubmits(t *testing.T) {
	pool := NewWorkerPool(8)
	defer pool.Close()

	const n = 1000
	var counter int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			err := pool.Submit(context.Background(), func() error {
				defer wg.Done()
				atomic.AddInt32(&counter, 1)
				return nil
			})
			if err != nil {
				t.Errorf("Submit: %v", err)
			}
		}()
	}
	wg.Wait()
	if counter != int32(n) {
		t.Errorf("counter = %d, want %d", counter, n)
	}
}

// TestWorkerPool_CloseIdempotent verifies Close can be called multiple times.
func TestWorkerPool_CloseIdempotent(t *testing.T) {
	pool := NewWorkerPool(2)
	pool.Close()
	pool.Close() // should not panic
}

// TestWorkerPool_SubmitAfterClose verifies error after close.
func TestWorkerPool_SubmitAfterClose(t *testing.T) {
	pool := NewWorkerPool(2)
	pool.Close()
	err := pool.Submit(context.Background(), func() error { return nil })
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed, got %v", err)
	}
}

// TestWorkerPool_TrySubmitFull verifies non-blocking submit.
func TestWorkerPool_TrySubmitFull(t *testing.T) {
	pool := NewWorkerPool(1)
	defer pool.Close()

	// Block the worker with a task
	block := make(chan struct{})
	workerStarted := make(chan struct{})
	err := pool.TrySubmit(func() error {
		close(workerStarted)
		<-block
		return nil
	})
	if err != nil {
		t.Fatalf("first TrySubmit: %v", err)
	}
	// Wait for the worker to actually start and pick up the task,
	// so we know the queue is empty before we fill it.
	<-workerStarted

	// Fill the queue (capacity = workers * 2 = 2)
	for i := 0; i < 2; i++ {
		_ = pool.TrySubmit(func() error { return nil })
	}

	// Next submit should fail
	err = pool.TrySubmit(func() error { return nil })
	if err != ErrPoolFull {
		t.Errorf("expected ErrPoolFull, got %v", err)
	}

	close(block)
}

// TestWorkerPool_ParallelFanOut verifies fan-out helper.
func TestWorkerPool_ParallelFanOut(t *testing.T) {
	pool := NewWorkerPool(4)
	defer pool.Close()

	partitions := []Partition{
		{ID: 0, Start: 0, End: 10},
		{ID: 1, Start: 10, End: 20},
		{ID: 2, Start: 20, End: 30},
		{ID: 3, Start: 30, End: 40},
	}

	var sum int64
	err := pool.ParallelFanOut(context.Background(), partitions, func(p Partition) error {
		atomic.AddInt64(&sum, int64(p.End-p.Start))
		return nil
	})
	if err != nil {
		t.Fatalf("ParallelFanOut: %v", err)
	}
	if sum != 40 {
		t.Errorf("sum = %d, want 40", sum)
	}
}

// TestWorkerPool_ParallelFanOut_Error verifies error propagation.
func TestWorkerPool_ParallelFanOut_Error(t *testing.T) {
	pool := NewWorkerPool(2)
	defer pool.Close()

	partitions := []Partition{{ID: 0, Start: 0, End: 10}}
	wantErr := &WorkerPoolError{Msg: "test error"}
	err := pool.ParallelFanOut(context.Background(), partitions, func(p Partition) error {
		return wantErr
	})
	if err != wantErr {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

// TestWorkerPool_TaskContextCancel verifies Submit respects context.
func TestWorkerPool_TaskContextCancel(t *testing.T) {
	pool := NewWorkerPool(1)
	defer pool.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := pool.Submit(ctx, func() error { return nil })
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestWorkerPool_TaskExecutionTiming verifies concurrent execution.
func TestWorkerPool_TaskExecutionTiming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing test in short mode")
	}
	pool := NewWorkerPool(4)
	defer pool.Close()

	// 4 tasks that each sleep 100ms; sequential would be 400ms.
	const n = 4
	const sleepMs = 100
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		pool.Submit(context.Background(), func() error {
			defer wg.Done()
			time.Sleep(sleepMs * time.Millisecond)
			return nil
		})
	}
	wg.Wait()
	elapsed := time.Since(start)

	// Parallel should be ~100ms; serial would be 400ms.
	// Allow 3x margin for scheduling overhead.
	if elapsed > 3*time.Duration(sleepMs)*time.Millisecond {
		t.Errorf("tasks did not run in parallel: %v elapsed", elapsed)
	}
}

// BenchmarkWorkerPool_Submit measures Submit throughput.
func BenchmarkWorkerPool_Submit(b *testing.B) {
	pool := NewWorkerPool(4)
	defer pool.Close()
	task := func() error { return nil }

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pool.Submit(context.Background(), task)
	}
}

// BenchmarkWorkerPool_Parallel measures parallel task throughput.
func BenchmarkWorkerPool_Parallel(b *testing.B) {
	pool := NewWorkerPool(4)
	defer pool.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for j := 0; j < 4; j++ {
			wg.Add(1)
			pool.Submit(context.Background(), func() error {
				defer wg.Done()
				return nil
			})
		}
		wg.Wait()
	}
}
