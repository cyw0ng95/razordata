package AG

import (
	"context"
	"slices"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// ParallelHashAggregate partitions input by group key hash, builds
// N partial hash DT.Tables in parallel, and merges results. REQ001046.
type ParallelHashAggregate struct {
	child     Operator
	groupCols []PS.Expr
	aggs      []PS.Expr
	pool      *UT.WorkerPool
	params    []any
	buf       []Row
	pos       int
	startMu   sync.Mutex
	started   bool
}

func NewParallelHashAggregate(child Operator, groupCols, aggs []PS.Expr, pool *UT.WorkerPool) *ParallelHashAggregate {
	return &ParallelHashAggregate{child: child, groupCols: groupCols, aggs: aggs, pool: pool}
}

func (a *ParallelHashAggregate) WithParams(p []any) Operator {
	a.params = p
	if a.child != nil {
		if w, ok := a.child.(interface{ WithParams([]any) Operator }); ok {
			w.WithParams(p)
		}
	}
	return a
}

func (a *ParallelHashAggregate) Next(ctx context.Context) (Row, error) {
	if !a.started {
		a.startMu.Lock()
		if !a.started {
			a.started = true
			a.startMu.Unlock()
			if err := a.materialize(ctx); err != nil {
				return Row{}, err
			}
		} else {
			a.startMu.Unlock()
		}
	}
	if a.pos >= len(a.buf) {
		return Row{}, ErrNoRows
	}
	r := a.buf[a.pos]
	a.pos++
	return r, nil
}

func (a *ParallelHashAggregate) Close() error {
	a.started = true
	a.buf = nil
	a.pos = 0
	return a.child.Close()
}

// NextBatch drains the materialized result buffer in batches of BatchSize.
// On first call, triggers materialization if not already started.
// REQ001982.
func (a *ParallelHashAggregate) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if !a.started {
		a.startMu.Lock()
		if !a.started {
			a.started = true
			a.startMu.Unlock()
			if err := a.materialize(ctx); err != nil {
				return nil, err
			}
		} else {
			a.startMu.Unlock()
		}
	}
	remaining := len(a.buf) - a.pos
	if remaining <= 0 {
		return nil, nil
	}
	batchSize := remaining
	if batchSize > UT.BatchSize {
		batchSize = UT.BatchSize
	}
	batch := rowsToBatchAG(a.buf[a.pos : a.pos+batchSize])
	a.pos += batchSize
	return batch, nil
}

// rowsToBatchAG converts a slice of Rows to a columnar Batch.
// REQ001982.
func rowsToBatchAG(rows []Row) *UT.Batch {
	if len(rows) == 0 {
		return nil
	}
	first := rows[0]
	nCols := len(first.Cols)
	if nCols == 0 {
		nCols = len(first.Data)
	}
	batch := UT.GetBatch(nCols)
	for i := 0; i < nCols; i++ {
		if i < len(first.Cols) {
			batch.SetColumnName(i, first.Cols[i])
		}
	}
	for _, row := range rows {
		for i := 0; i < nCols; i++ {
			var val any
			isNull := true
			if i < len(row.Data) {
				v := row.Data[i]
				if v.Kind != KindNull {
					isNull = false
					val = v.ToAny()
				}
			}
			var typ LX.TokenType
			if i < len(row.Types) {
				typ = row.Types[i]
			} else if i < len(row.Data) {
				switch row.Data[i].Kind {
				case KindInt:
					typ = LX.T_BIGINT
				case KindFloat:
					typ = LX.T_FLOAT_KW
				case KindText, KindBlob:
					typ = LX.T_TEXT
				case KindBool:
					typ = LX.T_BOOL
				}
			}
			batch.AppendRow(i, typ, val, isNull)
		}
		batch.AdvanceSize()
	}
	return batch
}

// partialAgg holds a worker's partial aggregation state.
type partialAgg struct {
	key    Row
	rows   int
	count  int64
	sum    float64
	min    float64
	max    float64
	hasVal bool
}

func (a *ParallelHashAggregate) materialize(ctx context.Context) error {
	// Drain all child rows
	var allRows []Row
	for {
		r, err := a.child.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return err
		}
		allRows = append(allRows, r)
	}
	if len(allRows) == 0 {
		a.buf = []Row{}
		return nil
	}

	workers := a.pool.Workers()
	if workers < 2 {
		workers = 2
	}
	if len(allRows) < 1000 {
		workers = 1 // small input, skip parallel
	}

	if workers <= 1 {
		return a.sequentialAgg(allRows)
	}

	return a.parallelAgg(ctx, allRows, workers)
}

func (a *ParallelHashAggregate) sequentialAgg(rows []Row) error {
	m := make(map[string][]Row)
	var order []string
	for _, r := range rows {
		key, err := evalGroupKey(a.groupCols, &r, a.params)
		if err != nil {
			return err
		}
		ks := groupKeyString(key)
		if _, ok := m[ks]; !ok {
			m[ks] = nil
			order = append(order, ks)
		}
		m[ks] = append(m[ks], r)
	}
	return a.buildResults(order, m)
}

func (a *ParallelHashAggregate) parallelAgg(ctx context.Context, rows []Row, workers int) error {
	// Partition rows by hash of group key
	partitions := make([][]Row, workers)
	for _, r := range rows {
		key, err := evalGroupKey(a.groupCols, &r, a.params)
		if err != nil {
			return err
		}
		h := hashGroupKey(key) % uint64(workers)
		partitions[h] = append(partitions[h], r)
	}

	type partResult struct {
		idx     int
		order   []string
		buckets map[string][]Row
		err     error
	}
	resultCh := make(chan partResult, workers)
	var wg sync.WaitGroup

	for i, part := range partitions {
		if len(part) == 0 {
			continue
		}
		i2, part2 := i, part
		wg.Add(1)
		err := a.pool.Submit(ctx, func() error {
			defer wg.Done()
			buckets := make(map[string][]Row)
			var order []string
			for _, r := range part2 {
				key, err := evalGroupKey(a.groupCols, &r, a.params)
				if err != nil {
					resultCh <- partResult{idx: i2, err: err}
					return err
				}
				ks := groupKeyString(key)
				if _, ok := buckets[ks]; !ok {
					buckets[ks] = nil
					order = append(order, ks)
				}
				buckets[ks] = append(buckets[ks], r)
			}
			resultCh <- partResult{idx: i2, order: order, buckets: buckets}
			return nil
		})
		if err != nil {
			wg.Done()
			return err
		}
	}

	wg.Wait()
	close(resultCh)

	// Merge partial buckets
	merged := make(map[string][]Row)
	var mergedOrder []string
	for res := range resultCh {
		if res.err != nil {
			return res.err
		}
		for _, ks := range res.order {
			if _, ok := merged[ks]; !ok {
				merged[ks] = nil
				mergedOrder = append(mergedOrder, ks)
			}
			merged[ks] = append(merged[ks], res.buckets[ks]...)
		}
	}

	slices.SortStableFunc(mergedOrder, func(i, j string) int {
		ai := merged[i][0]
		aj := merged[j][0]
		return keysLessByDistinctCmp(ai, aj, a.groupCols)
	})

	for _, ks := range mergedOrder {
		rows := merged[ks]
		key := rows[0]
		keyVals, _ := evalGroupKey(a.groupCols, &key, a.params)
		out := Row{}
		for i, gc := range a.groupCols {
			out.Cols = append(out.Cols, groupColName(gc))
			out.Data = append(out.Data, keyVals[i])
		}
		for _, ag := range a.aggs {
			v, err := EvalAggregateOver(ag, rows, a.params)
			if err != nil {
				return err
			}
			out.Cols = append(out.Cols, aggregateColName(ag))
			out.Data = append(out.Data, DT.ValueFromAny(v))
		}
		a.buf = append(a.buf, out)
	}
	return nil
}

func (a *ParallelHashAggregate) buildResults(order []string, buckets map[string][]Row) error {
	slices.SortStableFunc(order, func(i, j string) int {
		ai := buckets[i][0]
		aj := buckets[j][0]
		return keysLessByDistinctCmp(ai, aj, a.groupCols)
	})
	for _, ks := range order {
		rows := buckets[ks]
		key := rows[0]
		keyVals, _ := evalGroupKey(a.groupCols, &key, a.params)
		out := Row{}
		for i, gc := range a.groupCols {
			out.Cols = append(out.Cols, groupColName(gc))
			out.Data = append(out.Data, keyVals[i])
		}
		for _, ag := range a.aggs {
			v, err := EvalAggregateOver(ag, rows, a.params)
			if err != nil {
				return err
			}
			out.Cols = append(out.Cols, aggregateColName(ag))
			out.Data = append(out.Data, DT.ValueFromAny(v))
		}
		a.buf = append(a.buf, out)
	}
	return nil
}

// hashGroupKey computes a hash of the group key values for partitioning.
func hashGroupKey(key []Value) uint64 {
	var h uint64
	for _, v := range key {
		switch v.Kind {
		case KindInt:
			h = h*31 + uint64(v.I64)
		case KindFloat:
			h = h*31 + uint64(v.F64)
		case KindText:
			for _, c := range v.S {
				h = h*31 + uint64(c)
			}
		case KindBool:
			if v.Bo {
				h = h*31 + 1
			}
		case KindBlob:
			for _, c := range v.B {
				h = h*31 + uint64(c)
			}
		}
	}
	return h
}
