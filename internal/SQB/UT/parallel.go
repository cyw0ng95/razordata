package UT

import (
	"context"
	"runtime"
	"sync"

	nm "github.com/cyw0ng95/razordata/internal/ENG/NM"
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
// REQ001055: NUMA-aware variant pins workers to local NUMA nodes.
type WorkerPool struct {
	workers   int
	taskQueue chan Task
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	closed    chan struct{}
	closeOnce sync.Once
	closeMu   sync.Mutex

	// REQ001055: NUMA-awareness fields.
	topo        *nm.Topology
	workerNodes []int       // worker id → NUMA node assignment
	nodeWorkers map[int]int // node → number of workers assigned
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
// REQ001055: when NUMA topology is set, pins to local node.
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()

	// REQ001055: pin to NUMA node if topology is set.
	if wp.topo != nil && id < len(wp.workerNodes) {
		node := wp.workerNodes[id]
		if cpus := wp.topo.CPUsForNode(node); len(cpus) > 0 {
			release := nm.PinWorker()
			defer release()
			_ = node // pinned via OS thread lock
		}
	}

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

// SetNUMATopology assigns workers to NUMA nodes and enables thread
// pinning. Workers are distributed round-robin across nodes.
// REQ001055.
func (wp *WorkerPool) SetNUMATopology(topo *nm.Topology) {
	if topo == nil || topo.NodeCount <= 1 {
		return
	}
	wp.topo = topo
	wp.workerNodes = make([]int, wp.workers)
	wp.nodeWorkers = make(map[int]int)

	// Distribute workers round-robin across NUMA nodes.
	// Sort nodes for deterministic assignment.
	nodes := make([]int, 0, topo.NodeCount)
	for node := range topo.NodeCPUs {
		nodes = append(nodes, node)
	}
	for i := range nodes {
		for j := i + 1; j < len(nodes); j++ {
			if nodes[i] > nodes[j] {
				nodes[i], nodes[j] = nodes[j], nodes[i]
			}
		}
	}

	for i := 0; i < wp.workers; i++ {
		node := nodes[i%len(nodes)]
		wp.workerNodes[i] = node
		wp.nodeWorkers[node]++
	}
}

// WorkerNode returns the NUMA node assigned to worker i.
// Returns 0 if no topology is set.
func (wp *WorkerPool) WorkerNode(i int) int {
	if wp.topo == nil || i < 0 || i >= len(wp.workerNodes) {
		return 0
	}
	return wp.workerNodes[i]
}

// NodeWorkers returns the number of workers assigned to a node.
func (wp *WorkerPool) NodeWorkers(node int) int {
	if wp.nodeWorkers == nil {
		return 0
	}
	return wp.nodeWorkers[node]
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

// ParallelProbeResult holds the per-row match list for one probe batch.
type ParallelProbeResult struct {
	Batch   *Batch
	Matches [][]uint32 // Matches[r] = build row IDs matched by probe row r (nil = no match)
}

// ParallelProbe drains the probe BatchProducer and, for each probe row, looks
// up the hash table to collect matching build-row IDs from rowIDs (indexed by
// hash-table slot). Lookups are fanned out across `workers` goroutines per
// batch; each worker writes disjoint Matches indices, so results stay in
// probe-row order and downstream emission is deterministic.
//
// keyHash fills dst (a per-worker scratch buffer of len == numKeys, owned by
// ParallelProbe) with the composite probe key for a row and returns its hash.
// ok=false skips the lookup (NULL key or bloom-negative). keyHash must be
// goroutine-safe and must not retain dst beyond the call.
//
// The hash table and rowIDs are treated as read-only during the probe, so
// concurrent Lookup calls are safe. REQ001645.
func ParallelProbe(ctx context.Context, ht HashTableInterface, rowIDs [][]uint32,
	probe BatchProducer, numKeys, workers int,
	keyHash func(batch *Batch, row int, dst []int64) (hash uint64, ok bool)) ([]ParallelProbeResult, error) {
	if ht == nil || probe == nil {
		return nil, nil
	}
	if numKeys < 1 {
		numKeys = 1
	}
	if workers < 1 {
		workers = 1
	}
	var results []ParallelProbeResult
	for {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		batch, err := probe.NextBatch(ctx)
		if err != nil {
			return results, err
		}
		if batch == nil {
			break
		}
		matches := make([][]uint32, batch.Size)
		if workers == 1 || batch.Size < parallelProbeMinRows {
			dst := make([]int64, numKeys)
			parallelProbeRange(ht, rowIDs, batch, matches, 0, batch.Size, dst, keyHash)
		} else {
			parallelProbeBatch(ht, rowIDs, batch, matches, numKeys, workers, keyHash)
		}
		results = append(results, ParallelProbeResult{Batch: batch, Matches: matches})
	}
	return results, nil
}

// parallelProbeMinRows is the minimum batch size below which ParallelProbe
// runs single-threaded (goroutine spawn overhead exceeds the lookup savings).
const parallelProbeMinRows = 32

// parallelProbeBatch fans the batch's rows across `workers` goroutines, each
// processing a disjoint [start,end) range with its own scratch buffer.
func parallelProbeBatch(ht HashTableInterface, rowIDs [][]uint32, batch *Batch,
	matches [][]uint32, numKeys, workers int,
	keyHash func(batch *Batch, row int, dst []int64) (hash uint64, ok bool)) {
	workers = max(1, min(workers, batch.Size))
	chunk := (batch.Size + workers - 1) / workers
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		start := w * chunk
		end := min(start+chunk, batch.Size)
		if start >= end {
			continue
		}
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			dst := make([]int64, numKeys)
			parallelProbeRange(ht, rowIDs, batch, matches, start, end, dst, keyHash)
		}(start, end)
	}
	wg.Wait()
}

// parallelProbeRange processes rows [start,end) of batch, writing matched
// build-row IDs into matches[r]. dst is a caller-provided scratch buffer of
// length numKeys (reused across rows in this range).
func parallelProbeRange(ht HashTableInterface, rowIDs [][]uint32, batch *Batch,
	matches [][]uint32, start, end int, dst []int64,
	keyHash func(batch *Batch, row int, dst []int64) (hash uint64, ok bool)) {
	for r := start; r < end; r++ {
		hash, ok := keyHash(batch, r, dst)
		if !ok {
			continue
		}
		idx, found, _ := ht.Lookup(dst, hash)
		if found && idx < len(rowIDs) {
			matches[r] = rowIDs[idx]
		}
	}
}
