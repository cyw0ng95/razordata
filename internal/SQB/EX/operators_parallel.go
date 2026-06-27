package EX

import (
	"context"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// ParallelSeqScanRow is a row-based parallel table scan that splits
// the row range into N partitions and scans each in parallel using a
// WorkerPool. On the first call to Next(), all partitions are fanned
// out to workers and their results are merged in partition order.
// REQ001043.
type ParallelSeqScanRow struct {
	schema  []string
	types   []int
	colMap  map[string]int
	pool    *WorkerPool
	rows    []Row
	startID int
	endID   int
	rowBuf  []Row
	rowPos  int
	done    bool
	started bool
	startMu sync.Mutex
}

// NewParallelSeqScanRow creates a row-based parallel scan.
func NewParallelSeqScanRow(rows []Row, schema []string, types []int, pool *WorkerPool) *ParallelSeqScanRow {
	colMap := make(map[string]int, len(schema))
	for i, name := range schema {
		colMap[name] = i
	}
	return &ParallelSeqScanRow{
		schema:  schema,
		types:   types,
		colMap:  colMap,
		pool:    pool,
		rows:    rows,
		startID: 0,
		endID:   len(rows),
	}
}

// Next implements the Operator interface. On first call, fans out
// all row partitions to workers, collects results in order, then
// serves from the merged buffer.
func (p *ParallelSeqScanRow) Next(ctx context.Context) (Row, error) {
	for {
		if p.rowPos < len(p.rowBuf) {
			r := p.rowBuf[p.rowPos]
			p.rowPos++
			return r, nil
		}
		if p.done {
			return Row{}, ErrNoRows
		}
		if err := p.startScan(ctx); err != nil {
			return Row{}, err
		}
	}
}

func (p *ParallelSeqScanRow) startScan(ctx context.Context) error {
	p.startMu.Lock()
	defer p.startMu.Unlock()
	if p.started {
		return nil
	}
	p.started = true

	workers := p.pool.Workers()
	totalRows := p.endID - p.startID
	if totalRows <= 0 {
		p.done = true
		return nil
	}
	rowsPerWorker := totalRows / workers
	if rowsPerWorker < 1 {
		rowsPerWorker = 1
		workers = totalRows
	}

	type partResult struct {
		idx  int
		rows []Row
		err  error
	}
	resultCh := make(chan partResult, workers)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		start := p.startID + i*rowsPerWorker
		end := start + rowsPerWorker
		if i == workers-1 {
			end = p.endID
		}
		if start >= end {
			continue
		}
		i2, start2, end2 := i, start, end
		wg.Add(1)
		err := p.pool.Submit(ctx, func() error {
			defer wg.Done()
			out := make([]Row, 0, end2-start2)
			for idx := start2; idx < end2; idx++ {
				r := p.rows[idx]
				out = append(out, Row{
					Cols:  p.schema,
					Types: p.types,
					Data:  r.Data,
				})
			}
			resultCh <- partResult{idx: i2, rows: out}
			return nil
		})
		if err != nil {
			wg.Done()
			resultCh <- partResult{idx: i2, err: err}
			break
		}
	}

	wg.Wait()
	close(resultCh)

	ordered := make([][]Row, workers)
	for res := range resultCh {
		if res.err != nil {
			return res.err
		}
		ordered[res.idx] = res.rows
	}
	var all []Row
	for _, part := range ordered {
		all = append(all, part...)
	}
	p.rowBuf = all
	p.rowPos = 0
	p.done = true
	return nil
}

// Close cleans up.
func (p *ParallelSeqScanRow) Close() error {
	p.done = true
	return nil
}

// ParallelUnionAll runs UNION ALL children concurrently.
// Left and right children are drained in parallel via WorkerPool,
// and their rows are emitted in arrival order. REQ001052.
type ParallelUnionAll struct {
	left   Operator
	right  Operator
	pool   *WorkerPool
	rowBuf []Row
	rowPos int
	done   bool
	startMu sync.Mutex
	started bool
}

// NewParallelUnionAll creates a parallel UNION ALL operator.
func NewParallelUnionAll(left, right Operator, pool *WorkerPool) *ParallelUnionAll {
	return &ParallelUnionAll{
		left:  left,
		right: right,
		pool:  pool,
	}
}

// Next implements the Operator interface.
func (u *ParallelUnionAll) Next(ctx context.Context) (Row, error) {
	for {
		if u.rowPos < len(u.rowBuf) {
			r := u.rowBuf[u.rowPos]
			u.rowPos++
			return r, nil
		}
		if u.done {
			return Row{}, ErrNoRows
		}
		if err := u.start(ctx); err != nil {
			return Row{}, err
		}
	}
}

func (u *ParallelUnionAll) start(ctx context.Context) error {
	u.startMu.Lock()
	defer u.startMu.Unlock()
	if u.started {
		return nil
	}
	u.started = true

	// Drain left and right in parallel via pool
	type sideResult struct {
		rows []Row
		err  error
	}
	resultCh := make(chan sideResult, 2)
	var wg sync.WaitGroup

	// Left child
	wg.Add(1)
	u.pool.Submit(ctx, func() error {
		defer wg.Done()
		var out []Row
		for {
			r, err := u.left.Next(ctx)
			if err != nil {
				if err == ErrNoRows {
					break
				}
				resultCh <- sideResult{err: err}
				return err
			}
			out = append(out, r)
		}
		resultCh <- sideResult{rows: out}
		return nil
	})

	// Right child
	wg.Add(1)
	u.pool.Submit(ctx, func() error {
		defer wg.Done()
		var out []Row
		for {
			r, err := u.right.Next(ctx)
			if err != nil {
				if err == ErrNoRows {
					break
				}
				resultCh <- sideResult{err: err}
				return err
			}
			out = append(out, r)
		}
		resultCh <- sideResult{rows: out}
		return nil
	})

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var all []Row
	for res := range resultCh {
		if res.err != nil {
			return res.err
		}
		all = append(all, res.rows...)
	}
	u.rowBuf = all
	u.rowPos = 0
	u.done = true
	return nil
}

// Close cleans up.
func (u *ParallelUnionAll) Close() error {
	u.done = true
	u.left.Close()
	u.right.Close()
	return nil
}

// ParallelSeqScan performs a batch-based parallel table scan.
// (Original implementation preserved for backward compatibility with existing tests.)
type ParallelSeqScan struct {
	source         Operator
	schema         []string
	types          []LX.TokenType
	colMap         map[string]int
	pool           *WorkerPool
	rows           []Row
	startID        int
	endID          int
	done           bool
	pendingBatches []*Batch
	pendingIdx     int
}

// NewParallelSeqScan creates a parallel scan (batch-based).
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

func (p *ParallelSeqScan) NextBatch(ctx context.Context) (*Batch, error) {
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
	p.pendingBatches = nil
	p.pendingIdx = 0

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
			end = p.endID
		}
		if start >= end {
			break
		}
		wg.Add(1)
		err := p.pool.Submit(ctx, func() error {
			defer wg.Done()
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

	if len(p.pendingBatches) == 0 {
		return nil, nil
	}
	batch := p.pendingBatches[0]
	p.pendingIdx = 1
	return batch, nil
}

func (p *ParallelSeqScan) scanPartition(start, end int) *Batch {
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

func (p *ParallelSeqScan) Close() error {
	if p.source != nil {
		return p.source.Close()
	}
	return nil
}

// ParallelIndexScan performs a parallel index scan by splitting
// the key range into partitions. (Original implementation preserved.)
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

func (p *ParallelIndexScan) NextBatch(ctx context.Context) (*Batch, error) {
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

	p.done = true
	if len(p.pendingBatches) == 0 {
		return nil, nil
	}
	batch := p.pendingBatches[0]
	p.pendingIdx = 1
	return batch, nil
}

func (p *ParallelIndexScan) scanIndexRange(start, end int) *Batch {
	batch := GetBatch(len(p.schema))
	batch.Size = 0
	for i, name := range p.schema {
		batch.SetColumnName(i, name)
	}
	batch.SetColMap(p.colMap)

	for idx := start; idx < end && batch.Size < BatchSize; idx++ {
		row := p.rows[idx]
		if p.pred != nil {
			val, err := EvalValue(p.pred, &row, nil)
			if err != nil {
				continue
			}
			if !isValueTruthy(val) {
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

func (p *ParallelIndexScan) Close() error {
	return nil
}

// LX import anchor to prevent unused import error
var _ = LX.T_INT_KW
