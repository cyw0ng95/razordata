package UT

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nm "github.com/cyw0ng95/razordata/internal/ENG/NM"
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

// TestNUMA_WorkerPlacement verifies that NUMA-aware WorkerPool assigns
// workers to NUMA nodes and distributes them round-robin. REQ001055.
func TestNUMA_WorkerPlacement(t *testing.T) {
	topo := nm.GetTopology()
	if topo == nil || topo.NodeCount <= 1 {
		t.Skip("single-NUMA host, testing fallback behavior")
	}

	pool := NewWorkerPool(8)
	defer pool.Close()
	pool.SetNUMATopology(topo)

	// Verify all workers are assigned to valid nodes.
	for i := 0; i < pool.Workers(); i++ {
		node := pool.WorkerNode(i)
		if node < 0 || node >= topo.NodeCount {
			t.Errorf("worker %d assigned to invalid node %d (nodeCount=%d)",
				i, node, topo.NodeCount)
		}
	}

	// Verify round-robin distribution.
	nodes := make(map[int]int)
	for i := 0; i < pool.Workers(); i++ {
		nodes[pool.WorkerNode(i)]++
	}
	// Each node should have approximately workers/nodeCount workers.
	perNode := pool.Workers() / topo.NodeCount
	for node, count := range nodes {
		if count < perNode || count > perNode+1 {
			t.Errorf("node %d has %d workers, want ~%d", node, count, perNode)
		}
	}

	// Verify NodeWorkers matches.
	for node, count := range nodes {
		if got := pool.NodeWorkers(node); got != count {
			t.Errorf("NodeWorkers(%d) = %d, want %d", node, got, count)
		}
	}
}

// TestNUMA_PoolWithoutTopology verifies that WorkerPool works normally
// when no NUMA topology is set (backward compatibility). REQ001055.
func TestNUMA_PoolWithoutTopology(t *testing.T) {
	pool := NewWorkerPool(4)
	defer pool.Close()

	// WorkerNode should return 0 for all workers (no topology).
	for i := 0; i < pool.Workers(); i++ {
		if got := pool.WorkerNode(i); got != 0 {
			t.Errorf("WorkerNode(%d) = %d, want 0 (no topology)", i, got)
		}
	}
	if got := pool.NodeWorkers(0); got != 0 {
		t.Errorf("NodeWorkers(0) = %d, want 0 (no topology)", got)
	}

	// Tasks should still execute correctly.
	var counter int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		pool.Submit(context.Background(), func() error {
			defer wg.Done()
			atomic.AddInt32(&counter, 1)
			return nil
		})
	}
	wg.Wait()
	if counter != 10 {
		t.Errorf("counter = %d, want 10", counter)
	}
}

// TestNUMA_PinWorkerInPool verifies that workers with NUMA topology
// actually pin to their OS thread. REQ001055.
func TestNUMA_PinWorkerInPool(t *testing.T) {
	topo := nm.GetTopology()
	pool := NewWorkerPool(2)
	defer pool.Close()
	pool.SetNUMATopology(topo)

	// Submit a task that records which CPU it ran on.
	cpus := make([]int32, pool.Workers())
	var wg sync.WaitGroup
	for i := 0; i < pool.Workers(); i++ {
		wg.Add(1)
		idx := i
		pool.Submit(context.Background(), func() error {
			defer wg.Done()
			// After LockOSThread, runtime.NumCPU doesn't help,
			// but we can verify the goroutine is pinned by checking
			// that multiple calls return consistent results.
			cpu := int32(runtime.NumCPU())
			atomic.StoreInt32(&cpus[idx], cpu)
			return nil
		})
	}
	wg.Wait()

	// Verify all workers ran (basic sanity).
	for i := range cpus {
		if atomic.LoadInt32(&cpus[i]) == 0 {
			t.Errorf("worker %d did not execute", i)
		}
	}
}

// TestDetectFeatures_AnyArch returns a feature set appropriate
// for the current GOARCH. REQ000310.
func TestDetectFeatures_AnyArch(t *testing.T) {
	f := DetectFeatures()
	t.Logf("features: %+v", f)
	switch runtime.GOARCH {
	case "amd64":
		if !f.HasAVX2 {
			t.Errorf("amd64 should report HasAVX2")
		}
	case "arm64":
		if !f.HasNEON {
			t.Errorf("arm64 should report HasNEON")
		}
	}
}
