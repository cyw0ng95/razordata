package EX

import (
	"context"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// ParallelSeqScan performs a parallel table scan by splitting
// the row range into N partitions and processing each in
// parallel using a WorkerPool. Results are merged via a
// channel-based fan-in pattern.
//
// The split is static: the row range [startKey, endKey) is
// divided evenly among workers. For skewed data, this may
// lead to load imbalance (a v2 improvement would use
// morsel-driven work stealing).
//
// Internal: pending batches are buffered in pendingBatches
// to support multiple NextBatch calls (one batch per call).
//
// REQ000145 satisfied: Parallel SeqScan using fan-out/fan-in.
type ParallelSeqScan struct {
	source        Operator
	schema        []string
	types         []LX.TokenType
	colMap        map[string]int
	pool          *WorkerPool
	rows          []Row
	startID       int
	endID         int
	done          bool
	pendingBatches []*Batch // batches from previous partition scans
	pendingIdx    int
}

// NewParallelSeqScan creates a parallel scan. The source operator
// is the underlying data source. The pool provides workers for
// parallel partition processing. rows is the full data set to
// scan (in this in-memory implementation).
func NewParallelSeqScan(source Operator, schema []string, types []LX.TokenType, pool *WorkerPool, rows []Row) *ParallelSeqScan {
	colMap := make(map[string]int, len(schema))
	for i, name := range schema {
		colMap[name] = i
	}
	return &ParallelSeqScan{
		source:  source,
		schema:  schema,
		types:   types,
		colMap:  colMap,
		pool:    pool,
		rows:    rows,
		startID: 0,
		endID:   len(rows),
	}
}

// NextBatch produces the next batch. Splits the remaining row
// range across workers, gathers partial batches, and returns
// them in sequence.
func (p *ParallelSeqScan) NextBatch(ctx context.Context) (*Batch, error) {
	// First, drain any pending batches from previous partition scans
	if p.pendingIdx < len(p.pendingBatches) {
		batch := p.pendingBatches[p.pendingIdx]
		p.pendingIdx++
		return batch, nil
	}

	if p.done || p.startID >= p.endID {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Clear old pending batches
	p.pendingBatches = nil
	p.pendingIdx = 0

	// Split remaining range across workers
	workers := p.pool.Workers()
	totalRows := p.endID - p.startID
	rowsPerWorker := totalRows / workers
	if rowsPerWorker < 1 {
		rowsPerWorker = 1
		workers = totalRows
	}

	type partialResult struct {
		batch *Batch
		err   error
	}
	resultCh := make(chan partialResult, workers)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		start := p.startID + i*rowsPerWorker
		end := start + rowsPerWorker
		if i == workers-1 {
			end = p.endID // last worker takes remainder
		}
		if start >= end {
			break
		}

		wg.Add(1)
		err := p.pool.Submit(ctx, func() error {
			defer wg.Done()
			// Each worker scans up to BatchSize rows from its partition
			partitionSize := end - start
			if partitionSize > BatchSize {
				partitionSize = BatchSize
			}
			batch := p.scanPartition(start, start+partitionSize)
			resultCh <- partialResult{batch: batch}
			return nil
		})
		if err != nil {
			wg.Done()
			return nil, err
		}
	}

	// Wait for all workers, then close channel
	wg.Wait()
	close(resultCh)

	// Collect all results into pendingBatches
	for res := range resultCh {
		if res.err != nil {
			return nil, res.err
		}
		if res.batch != nil {
			p.pendingBatches = append(p.pendingBatches, res.batch)
		}
	}

	// Advance position by what was actually scanned
	scanned := 0
	for i := 0; i < workers; i++ {
		partitionSize := rowsPerWorker
		if i == workers-1 {
			partitionSize = p.endID - p.startID - i*rowsPerWorker
		}
		if partitionSize > BatchSize {
			partitionSize = BatchSize
		}
		scanned += partitionSize
	}
	p.startID += scanned
	if p.startID >= p.endID {
		p.done = true
	}

	// Return first batch
	if len(p.pendingBatches) == 0 {
		return nil, nil
	}
	batch := p.pendingBatches[0]
	p.pendingIdx = 1
	return batch, nil
}

// scanPartition produces all batches from rows[start:end].
// Returns nil if no rows were scanned.
func (p *ParallelSeqScan) scanPartition(start, end int) *Batch {
	// For simplicity, return only the first batch per partition.
	// Larger scans are handled by repeated calls.
	batch := GetBatch(len(p.schema))
	batch.Size = 0
	for i, name := range p.schema {
		batch.SetColumnName(i, name)
	}
	batch.SetColMap(p.colMap)

	rowCount := end - start
	if rowCount > BatchSize {
		rowCount = BatchSize
	}
	for idx := start; idx < start+rowCount; idx++ {
		row := p.rows[idx]
		for i, colName := range p.schema {
			val, ok := row.Lookup(colName)
			if !ok {
				val = nil
			}
			isNull := val == nil
			batch.AppendRow(i, p.types[i], val, isNull)
		}
		batch.AdvanceSize()
	}

	if batch.Size == 0 {
		batch.Put()
		return nil
	}
	return batch
}

// Close releases resources.
func (p *ParallelSeqScan) Close() error {
	if p.source != nil {
		return p.source.Close()
	}
	return nil
}

// ParallelIndexScan performs a parallel index scan by splitting
// the key range into partitions. Each worker scans its partition
// in parallel.
//
// In the current in-memory implementation, the "index" is a
// pre-sorted slice of Row with the index column as the key.
type ParallelIndexScan struct {
	indexCol       string
	rows           []Row
	pred           PS.Expr
	pool           *WorkerPool
	schema         []string
	types          []LX.TokenType
	colMap         map[string]int
	pendingBatches []*Batch
	pendingIdx     int
	done           bool
}

// NewParallelIndexScan creates a parallel index scan.
func NewParallelIndexScan(rows []Row, indexCol string, schema []string, types []LX.TokenType, pred PS.Expr, pool *WorkerPool) *ParallelIndexScan {
	colMap := make(map[string]int, len(schema))
	for i, name := range schema {
		colMap[name] = i
	}
	return &ParallelIndexScan{
		indexCol: indexCol,
		rows:     rows,
		pred:     pred,
		pool:     pool,
		schema:   schema,
		types:    types,
		colMap:   colMap,
	}
}

// NextBatch produces the next batch from the index scan.
func (p *ParallelIndexScan) NextBatch(ctx context.Context) (*Batch, error) {
	// First, drain any pending batches
	if p.pendingIdx < len(p.pendingBatches) {
		batch := p.pendingBatches[p.pendingIdx]
		p.pendingIdx++
		return batch, nil
	}

	if p.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Clear old pending
	p.pendingBatches = nil
	p.pendingIdx = 0

	workers := p.pool.Workers()
	totalRows := len(p.rows)
	if totalRows == 0 {
		p.done = true
		return nil, nil
	}
	rowsPerWorker := totalRows / workers
	if rowsPerWorker < 1 {
		rowsPerWorker = 1
		workers = totalRows
	}

	type partialResult struct {
		batch *Batch
		err   error
	}
	resultCh := make(chan partialResult, workers)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		start := i * rowsPerWorker
		end := start + rowsPerWorker
		if i == workers-1 {
			end = totalRows
		}
		if start >= end {
			break
		}

		wg.Add(1)
		err := p.pool.Submit(ctx, func() error {
			defer wg.Done()
			batch := p.scanIndexRange(start, end)
			resultCh <- partialResult{batch: batch}
			return nil
		})
		if err != nil {
			wg.Done()
			return nil, err
		}
	}

	wg.Wait()
	close(resultCh)

	for res := range resultCh {
		if res.err != nil {
			return nil, res.err
		}
		if res.batch != nil {
			p.pendingBatches = append(p.pendingBatches, res.batch)
		}
	}

	p.done = true // IndexScan is single-pass in current design

	if len(p.pendingBatches) == 0 {
		return nil, nil
	}
	batch := p.pendingBatches[0]
	p.pendingIdx = 1
	return batch, nil
}

// scanIndexRange scans rows in [start, end) and applies the
// predicate (if any) to produce a columnar batch.
func (p *ParallelIndexScan) scanIndexRange(start, end int) *Batch {
	batch := GetBatch(len(p.schema))
	batch.Size = 0
	for i, name := range p.schema {
		batch.SetColumnName(i, name)
	}
	batch.SetColMap(p.colMap)

	for idx := start; idx < end && batch.Size < BatchSize; idx++ {
		row := p.rows[idx]
		// Apply predicate if present
		if p.pred != nil {
			val, err := Eval(p.pred, &row, nil)
			if err != nil {
				continue
			}
			if b, ok := val.(bool); !ok || !b {
				continue
			}
		}
		for i, colName := range p.schema {
			val, ok := row.Lookup(colName)
			if !ok {
				val = nil
			}
			isNull := val == nil
			batch.AppendRow(i, p.types[i], val, isNull)
		}
		batch.AdvanceSize()
	}

	if batch.Size == 0 {
		batch.Put()
		return nil
	}
	return batch
}

// Close releases resources.
func (p *ParallelIndexScan) Close() error {
	return nil
}

// LX import anchor to prevent unused import error
var _ = LX.T_INT_KW
