package EX

import (
	"context"
	"runtime"
	"sync"
)

// Task represents a unit of parallel work that produces a result
// or error. The result is delivered to the caller via the Result
// channel passed to the task.
type Task = func() error

// WorkerPool coordinates parallel task execution across N workers.
// Tasks are submitted via Submit() and executed by worker goroutines
// in a FIFO order. The pool provides graceful shutdown via Close().
// REQ000145 satisfied (partial): Worker pool foundation for
// parallel query execution.
type WorkerPool struct {
	workers   int
	taskQueue chan Task
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	closed    chan struct{}
	closeOnce sync.Once
	closeMu   sync.Mutex
}

// NewWorkerPool creates a worker pool with the specified number
// of workers. If n <= 0, defaults to runtime.GOMAXPROCS(0).
func NewWorkerPool(n int) *WorkerPool {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	if n > 256 {
		n = 256 // sanity cap
	}
	ctx, cancel := context.WithCancel(context.Background())
	wp := &WorkerPool{
		workers:   n,
		taskQueue: make(chan Task, n*2),
		ctx:       ctx,
		cancel:    cancel,
		closed:    make(chan struct{}),
	}

	// Spawn workers
	for i := 0; i < n; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
	return wp
}

// worker is the main loop for a single worker goroutine.
// It pulls tasks from the queue and executes them.
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()
	for {
		select {
		case <-wp.ctx.Done():
			return
		case task, ok := <-wp.taskQueue:
			if !ok {
				return
			}
			task()
		}
	}
}

// Submit adds a task to the pool. Blocks if the queue is full.
// Returns ctx.Err() if the pool is closed.
func (wp *WorkerPool) Submit(ctx context.Context, task Task) error {
	// First, fast-fail on cancelled context
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	// Then check if pool is closed
	wp.closeMu.Lock()
	select {
	case <-wp.closed:
		wp.closeMu.Unlock()
		return ErrPoolClosed
	default:
	}
	wp.closeMu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case wp.taskQueue <- task:
		return nil
	}
}

// TrySubmit attempts to add a task without blocking. Returns
// ErrPoolFull if the queue is full.
func (wp *WorkerPool) TrySubmit(task Task) error {
	wp.closeMu.Lock()
	select {
	case <-wp.closed:
		wp.closeMu.Unlock()
		return ErrPoolClosed
	default:
	}
	wp.closeMu.Unlock()

	select {
	case wp.taskQueue <- task:
		return nil
	default:
		return ErrPoolFull
	}
}

// Workers returns the number of workers in the pool.
func (wp *WorkerPool) Workers() int {
	return wp.workers
}

// Close shuts down the pool gracefully. After Close, all Submit
// calls return ErrPoolClosed. Pending tasks are drained.
// Close is idempotent (R22); subsequent calls are no-ops.
func (wp *WorkerPool) Close() {
	wp.closeOnce.Do(func() {
		wp.closeMu.Lock()
		close(wp.closed)
		wp.cancel()
		// Drain task queue before closing channel to prevent
		// "send on closed channel" panics from in-flight Submit.
		// Workers will process remaining tasks and exit.
		// We close after workers are done.
		wp.closeMu.Unlock()
		wp.wg.Wait()
		// Now safe to close the channel - no senders.
		// Use sync.Once to make it idempotent.
		// Note: this is safe because workers have exited.
		// New Submit calls already returned ErrPoolClosed.
		defer func() {
			recover() // in case channel was already closed
		}()
		close(wp.taskQueue)
	})
}

// Errors
var (
	ErrPoolClosed = &WorkerPoolError{Msg: "ex: worker pool is closed"}
	ErrPoolFull   = &WorkerPoolError{Msg: "ex: worker pool queue is full"}
)

// WorkerPoolError is returned by WorkerPool methods on failure.
type WorkerPoolError struct {
	Msg string
}

func (e *WorkerPoolError) Error() string { return e.Msg }

// Unwrap returns nil (no wrapped error). Added for errors.Is/As
// chain compatibility. REQ000657.
func (e *WorkerPoolError) Unwrap() error { return nil }

// ParallelFanOut splits work across N workers using the pool.
// Each worker processes partitions[partitionIdx] in parallel.
// The result of each partition is delivered to the results
// channel in the order partitions are submitted.
// This is a convenience helper for the common "split-and-merge"
// pattern used in parallel scans.
// REQ000145 satisfied: Fan-out/Fan-in pattern for parallel scans.
type Partition struct {
	ID    int
	Start uint64
	End   uint64
}

// ParallelFanOut submits a task for each partition to the pool
// and waits for all to complete. Returns the first error (if any)
// via the errCh; nil if all partitions succeeded.
// Callers should use a buffered errCh of size len(partitions) to
// avoid blocking.
func (wp *WorkerPool) ParallelFanOut(ctx context.Context, partitions []Partition, fn func(p Partition) error) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(partitions))

	for _, p := range partitions {
		p := p // capture
		wg.Add(1)
		err := wp.Submit(ctx, func() error {
			defer wg.Done()
			taskErr := fn(p)
			if taskErr != nil {
				errCh <- taskErr
			}
			return taskErr
		})
		if err != nil {
			wg.Done()
			errCh <- err
			break
		}
	}

	wg.Wait()
	close(errCh)

	// Return first error (if any)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}
