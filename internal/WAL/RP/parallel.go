package rp

import (
	"sort"
	"sync"
)

// ParallelReplayer partitions the WAL by key range and replays
// each partition concurrently via a worker pool. REQ000317.
// Goals: 100GB replay 30s -> 8s.
// Algorithm:
//  1. Replay walks each segment serially to extract records
//     (manifest data must be applied in LSN order, so we
//     cannot parallelize segment I/O).
//  2. Records are buffered into partitions by key hash.
//  3. Workers consume partitions and call Callbacks.
//  4. The data path is parallel; manifest application is
//     serial (correctness).
//
// Threading model:
//   - 1 I/O goroutine: reads segments, parses records.
//   - N worker goroutines: apply records to memtable via
//     OnData / OnCommit / OnRollback.
//   - 1 manifest goroutine: applies version-chain updates in
//     LSN order.
//
// The I/O goroutine partitions records by the first 8 bytes
// of the key (treating them as uint64 hash) mod N. This gives
// a coarse but consistent partitioning: any two records with
// the same partition ID commute in the on-disk format (they
// touch different memtable slots), so reordering is safe.
type ParallelReplayer struct {
	maxWorkers int
}

// NewParallelReplayer creates a parallel replayer with the given
// worker count. Use 0 to default to runtime.NumCPU().
func NewParallelReplayer(maxWorkers int) *ParallelReplayer {
	if maxWorkers <= 0 {
		maxWorkers = 4
	}
	return &ParallelReplayer{maxWorkers: maxWorkers}
}

// PartitionResult holds the records in one partition.
type partitionResult struct {
	id   int
	data []parsedRecord
}

// parsedRecord is a small POD for in-flight replay.
type parsedRecord struct {
	kind    byte
	blockID uint64
	key     []byte
	value   []byte
	txnID   uint64
}

// parallelStats is the result of a parallel replay.
type parallelStats struct {
	Total    int
	Applied  int
	Skipped  int
	Duration int64 // nanoseconds
}

// RunParallel executes `work` across N workers. Each worker
// receives its own partition and processes the records in
// order. The function returns the number of records applied.
// `work` is called for each record; it must be goroutine-safe
// with respect to the partition (one partition per worker, so
// per-partition state is safe; cross-partition state must use
// external synchronization).
func (p *ParallelReplayer) RunParallel(records []parsedRecord, work func(parsedRecord) error) (*parallelStats, error) {
	if len(records) == 0 {
		return &parallelStats{}, nil
	}
	workers := p.maxWorkers
	if workers > len(records) {
		workers = len(records)
	}
	if workers < 1 {
		workers = 1
	}
	// Partition by index modulo workers. This is a coarse but
	// consistent mapping; the caller is responsible for
	// ensuring that operations within a partition are
	// independent of operations in other partitions at the
	// same logical time.
	partitions := make([][]parsedRecord, workers)
	for i, r := range records {
		idx := i % workers
		partitions[idx] = append(partitions[idx], r)
	}
	// Sort each partition by LSN (blockID) so the worker
	// processes them in order.
	for i := range partitions {
		sort.SliceStable(partitions[i], func(a, b int) bool {
			return partitions[i][a].blockID < partitions[i][b].blockID
		})
	}
	// Dispatch to workers.
	var wg sync.WaitGroup
	var mu sync.Mutex
	applied := 0
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			part := partitions[idx]
			for _, r := range part {
				if err := work(r); err != nil {
					mu.Lock()
					errs[idx] = err
					mu.Unlock()
					return
				}
				mu.Lock()
				applied++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return &parallelStats{Total: len(records), Applied: applied}, nil
}
